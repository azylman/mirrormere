package video

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/azylman/mirrormere/internal/events"
)

// VideoStateSink accepts updated video state for initial connection hydration sync.
type VideoStateSink interface {
	SetVideoState(state *events.VideoStateData)
}

// CoordinatorOption configures Coordinator instances.
type CoordinatorOption func(*Coordinator)

// WithSink registers a VideoStateSink for hydration synchronization.
func WithSink(sink VideoStateSink) CoordinatorOption {
	return func(c *Coordinator) {
		c.sink = sink
	}
}

// WithHTTPClient overrides the HTTP client used for sidecar action forwarding.
func WithHTTPClient(client *http.Client) CoordinatorOption {
	return func(c *Coordinator) {
		if client != nil {
			c.httpClient = client
		}
	}
}

// WithTimerFunc overrides the countdown timer constructor for deterministic testing.
func WithTimerFunc(tf TimerFunc) CoordinatorOption {
	return func(c *Coordinator) {
		if tf != nil {
			c.timerFunc = tf
		}
	}
}

// Coordinator manages the authoritative in-memory video priority stack, auto-dismiss timers,
// transport action forwarding, and real-time SSE broadcasts per SPEC-004 §1–§4 and SPEC-006 §4.
type Coordinator struct {
	mu         sync.RWMutex
	mode       string
	primary    *VideoStream
	pip        *VideoStream
	timers     map[string]Timer
	hub        *events.Hub
	sink       VideoStateSink
	httpClient *http.Client
	timerFunc  TimerFunc
	closed     bool
}

// NewCoordinator constructs a VideoCoordinator initialized in widgets mode.
func NewCoordinator(hub *events.Hub, opts ...CoordinatorOption) *Coordinator {
	c := &Coordinator{
		mode:       ModeWidgets,
		timers:     make(map[string]Timer),
		hub:        hub,
		httpClient: &http.Client{Timeout: 2 * time.Second},
		timerFunc:  defaultTimerFunc,
	}

	for _, opt := range opts {
		opt(c)
	}

	// Seed hydration state sink on initialization
	if c.sink != nil {
		c.sink.SetVideoState(&events.VideoStateData{
			Mode:    ModeWidgets,
			Primary: nil,
			Pip:     nil,
		})
	}

	return c
}

// GetState returns an isolated snapshot of the current video presentation state.
func (c *Coordinator) GetState() VideoState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snapshotLocked()
}

func (c *Coordinator) snapshotLocked() VideoState {
	return VideoState{
		Mode:    c.mode,
		Primary: cloneStream(c.primary),
		Pip:     cloneStream(c.pip),
	}
}

func cloneStream(s *VideoStream) *VideoStream {
	if s == nil {
		return nil
	}
	cp := *s
	return &cp
}

// Trigger mounts or updates a video stream within the priority stack according to SPEC-004 §4.
func (c *Coordinator) Trigger(stream VideoStream) (VideoState, error) {
	stream.ID = strings.TrimSpace(stream.ID)
	stream.StreamURL = strings.TrimSpace(stream.StreamURL)
	if stream.ID == "" || stream.StreamURL == "" || stream.ID == "all" || stream.ID == "*" {
		return VideoState{}, ErrInvalidStream
	}

	if stream.Type == "" {
		stream.Type = TypeWebRTC
	} else if stream.Type != TypeWebRTC && stream.Type != TypeHLS && stream.Type != TypeMJPEG {
		return VideoState{}, ErrInvalidStream
	}

	if stream.Priority == "" {
		stream.Priority = PriorityPersistent
	} else if stream.Priority != PriorityPersistent && stream.Priority != PriorityTemporary {
		return VideoState{}, ErrInvalidStream
	}

	if stream.Priority == PriorityTemporary {
		if stream.TimeoutSeconds <= 0 {
			stream.TimeoutSeconds = DefaultTemporaryTimeoutSeconds
		}
	} else {
		stream.TimeoutSeconds = 0
	}

	if stream.PlayerState == "" {
		stream.PlayerState = PlayerStatePlaying
	} else if stream.PlayerState != PlayerStatePlaying && stream.PlayerState != PlayerStatePaused && stream.PlayerState != PlayerStateBuffering {
		return VideoState{}, ErrInvalidPlayerState
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return VideoState{}, ErrStreamNotFound
	}

	// Case 1: Stream re-trigger with same ID
	if c.primary != nil && c.primary.ID == stream.ID {
		c.stopTimerLocked(stream.ID)
		stream.Muted = false
		c.primary = &stream
		if stream.Priority == PriorityTemporary {
			c.startTimerLocked(stream.ID, time.Duration(stream.TimeoutSeconds)*time.Second)
		}
		snap := c.snapshotLocked()
		c.mu.Unlock()
		c.publishState(snap)
		return snap, nil
	}

	if c.pip != nil && c.pip.ID == stream.ID {
		c.stopTimerLocked(stream.ID)
		stream.Muted = true
		c.pip = &stream
		if stream.Priority == PriorityTemporary {
			c.startTimerLocked(stream.ID, time.Duration(stream.TimeoutSeconds)*time.Second)
		}
		snap := c.snapshotLocked()
		c.mu.Unlock()
		c.publishState(snap)
		return snap, nil
	}

	// Case 2: Persistent Stream Trigger
	if stream.Priority == PriorityPersistent {
		stream.Muted = false
		if c.primary != nil {
			if c.primary.Priority == PriorityTemporary {
				// Demote existing temporary stream from Primary to PiP
				if c.pip != nil {
					c.stopTimerLocked(c.pip.ID) // Previous PiP evicted
				}
				c.primary.Muted = true
				c.pip = c.primary
			} else {
				// Previous persistent stream in Primary is replaced
				c.stopTimerLocked(c.primary.ID)
			}
		}
		c.primary = &stream
		c.mode = ModeVideo
	} else {
		// Case 3: Temporary Stream Trigger
		if c.primary != nil && c.primary.Priority == PriorityPersistent {
			// Persistent media is active: incoming alert docks into PiP with muted audio
			if c.pip != nil {
				c.stopTimerLocked(c.pip.ID) // Evict previous PiP alert
			}
			stream.Muted = true
			c.pip = &stream
			c.startTimerLocked(stream.ID, time.Duration(stream.TimeoutSeconds)*time.Second)
			c.mode = ModeVideo
		} else {
			// Idle or previous temporary alert in primary: take Primary fullscreen with audio
			if c.primary != nil {
				c.stopTimerLocked(c.primary.ID)
			}
			stream.Muted = false
			c.primary = &stream
			c.startTimerLocked(stream.ID, time.Duration(stream.TimeoutSeconds)*time.Second)
			c.mode = ModeVideo
		}
	}

	snap := c.snapshotLocked()
	c.mu.Unlock()

	c.publishState(snap)
	return snap, nil
}

// Dismiss unmounts an active video stream by ID per SPEC-004 §4.
// If id is "all" or "*", all active streams are unmounted and the mode returns to widgets per SPEC-004 §2.
func (c *Coordinator) Dismiss(id string) (VideoState, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return VideoState{}, ErrInvalidStream
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return VideoState{}, ErrStreamNotFound
	}

	if id == "all" || id == "*" {
		if c.primary == nil && c.pip == nil {
			c.mu.Unlock()
			return VideoState{}, ErrStreamNotFound
		}
		for tid := range c.timers {
			c.stopTimerLocked(tid)
		}
		c.primary = nil
		c.pip = nil
		c.mode = ModeWidgets
		snap := c.snapshotLocked()
		c.mu.Unlock()
		c.publishState(snap)
		return snap, nil
	}

	if c.pip != nil && c.pip.ID == id {
		c.stopTimerLocked(id)
		c.pip = nil
		snap := c.snapshotLocked()
		c.mu.Unlock()
		c.publishState(snap)
		return snap, nil
	}

	if c.primary != nil && c.primary.ID == id {
		c.stopTimerLocked(id)
		if c.pip != nil {
			// Promote PiP alert to Primary fullscreen with unmuted audio
			c.primary = c.pip
			c.pip = nil
			c.primary.Muted = false
			// Timer for promoted stream continues running unaffected
		} else {
			c.primary = nil
			c.mode = ModeWidgets
		}
		snap := c.snapshotLocked()
		c.mu.Unlock()
		c.publishState(snap)
		return snap, nil
	}

	c.mu.Unlock()
	return VideoState{}, ErrStreamNotFound
}

// DismissAll unmounts all active video streams and returns to widgets mode per SPEC-004 §2.
func (c *Coordinator) DismissAll() (VideoState, error) {
	return c.Dismiss("all")
}

// SetPlayerState updates the transport state on an active stream per SPEC-004 §3.4.
func (c *Coordinator) SetPlayerState(id string, playerState string) (VideoState, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return VideoState{}, ErrInvalidStream
	}

	if playerState != PlayerStatePlaying && playerState != PlayerStatePaused && playerState != PlayerStateBuffering {
		return VideoState{}, ErrInvalidPlayerState
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return VideoState{}, ErrStreamNotFound
	}

	found := false
	if c.primary != nil && c.primary.ID == id {
		c.primary.PlayerState = playerState
		found = true
	} else if c.pip != nil && c.pip.ID == id {
		c.pip.PlayerState = playerState
		found = true
	}

	if !found {
		c.mu.Unlock()
		return VideoState{}, ErrStreamNotFound
	}

	snap := c.snapshotLocked()
	c.mu.Unlock()

	c.publishState(snap)
	return snap, nil
}

// ForwardAction proxies playback control commands to a controllable stream's sidecar webhook.
func (c *Coordinator) ForwardAction(ctx context.Context, id, action string, value any) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrInvalidStream
	}

	if action != ActionTogglePlayback && action != ActionPlay && action != ActionPause {
		return ErrInvalidAction
	}

	c.mu.RLock()
	var targetStream *VideoStream
	if c.primary != nil && c.primary.ID == id {
		targetStream = c.primary
	} else if c.pip != nil && c.pip.ID == id {
		targetStream = c.pip
	}

	if targetStream == nil {
		c.mu.RUnlock()
		return ErrStreamNotFound
	}

	if !targetStream.Controllable || strings.TrimSpace(targetStream.ControlURL) == "" {
		c.mu.RUnlock()
		return ErrNotControllable
	}

	controlURL := targetStream.ControlURL
	c.mu.RUnlock()

	// Dispatch HTTP POST to control_url with strict 2-second timeout
	reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	payload, err := json.Marshal(ActionRequest{
		ID:     id,
		Action: action,
		Value:  value,
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, controlURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return ErrControllerUnreachable
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return ErrControllerUnreachable
	}

	return nil
}

func (c *Coordinator) startTimerLocked(id string, d time.Duration) {
	if d <= 0 {
		return
	}
	c.stopTimerLocked(id)
	c.timers[id] = c.timerFunc(d, func() {
		c.handleTimerExpired(id)
	})
}

func (c *Coordinator) stopTimerLocked(id string) {
	if t, ok := c.timers[id]; ok {
		t.Stop()
		delete(c.timers, id)
	}
}

func (c *Coordinator) handleTimerExpired(id string) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}

	delete(c.timers, id)

	var snap VideoState
	shouldPublish := false

	if c.pip != nil && c.pip.ID == id {
		c.pip = nil
		snap = c.snapshotLocked()
		shouldPublish = true
	} else if c.primary != nil && c.primary.ID == id {
		if c.pip != nil {
			c.primary = c.pip
			c.pip = nil
			c.primary.Muted = false
		} else {
			c.primary = nil
			c.mode = ModeWidgets
		}
		snap = c.snapshotLocked()
		shouldPublish = true
	}

	c.mu.Unlock()

	if shouldPublish {
		c.publishState(snap)
	}
}

func (c *Coordinator) publishState(state VideoState) {
	data := events.VideoStateData{
		Mode:    state.Mode,
		Primary: state.Primary,
		Pip:     state.Pip,
	}

	if c.sink != nil {
		c.sink.SetVideoState(&data)
	}

	if c.hub != nil {
		payload, err := json.Marshal(data)
		if err == nil {
			c.hub.Publish(events.EventVideoState, payload)
		}
	}
}

// Close terminates all active timers and stops the coordinator.
func (c *Coordinator) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	for id, t := range c.timers {
		t.Stop()
		delete(c.timers, id)
	}
}
