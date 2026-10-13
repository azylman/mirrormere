package video

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/events"
)

type mockTimer struct {
	stopped bool
	fn      func()
}

func (m *mockTimer) Stop() bool {
	m.stopped = true
	return true
}

func (m *mockTimer) fire() {
	if !m.stopped && m.fn != nil {
		m.fn()
	}
}

type mockTimerFactory struct {
	mu     sync.Mutex
	timers map[string]*mockTimer
}

func newMockTimerFactory() *mockTimerFactory {
	return &mockTimerFactory{timers: make(map[string]*mockTimer)}
}

func (f *mockTimerFactory) TimerFunc(d time.Duration, fn func()) Timer {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := &mockTimer{fn: fn}
	// We'll store by pointer string or auto-key
	key := time.Now().Format(time.RFC3339Nano)
	f.timers[key] = t
	return t
}

func (f *mockTimerFactory) fireAll() {
	f.mu.Lock()
	list := make([]*mockTimer, 0, len(f.timers))
	for _, t := range f.timers {
		list = append(list, t)
	}
	f.mu.Unlock()

	for _, t := range list {
		t.fire()
	}
}

type mockSink struct {
	mu    sync.Mutex
	state *events.VideoStateData
}

func (s *mockSink) SetVideoState(state *events.VideoStateData) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
}

func (s *mockSink) getState() *events.VideoStateData {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func TestCoordinator_DefaultsAndSeeding(t *testing.T) {
	t.Parallel()

	sink := &mockSink{}
	hub := events.NewHub(events.HubConfig{}, nil, nil)
	defer hub.Close()

	coord := NewCoordinator(hub, WithSink(sink))
	defer coord.Close()

	state := coord.GetState()
	if state.Mode != ModeWidgets {
		t.Fatalf("expected initial mode 'widgets', got %s", state.Mode)
	}
	if state.Primary != nil || state.Pip != nil {
		t.Fatalf("expected nil primary and pip on init")
	}

	sinkState := sink.getState()
	if sinkState == nil || sinkState.Mode != ModeWidgets {
		t.Fatalf("expected sink to be seeded with mode 'widgets', got %+v", sinkState)
	}
}

func TestCoordinator_TriggerValidation(t *testing.T) {
	t.Parallel()

	coord := NewCoordinator(nil)
	defer coord.Close()

	// Empty ID or StreamURL
	if _, err := coord.Trigger(VideoStream{}); !errors.Is(err, ErrInvalidStream) {
		t.Fatalf("expected ErrInvalidStream, got %v", err)
	}
	if _, err := coord.Trigger(VideoStream{ID: "cast"}); !errors.Is(err, ErrInvalidStream) {
		t.Fatalf("expected ErrInvalidStream, got %v", err)
	}

	// Reserved sentinels 'all' and '*'
	if _, err := coord.Trigger(VideoStream{ID: "all", StreamURL: "http://x"}); !errors.Is(err, ErrInvalidStream) {
		t.Fatalf("expected ErrInvalidStream for reserved id 'all', got %v", err)
	}
	if _, err := coord.Trigger(VideoStream{ID: "*", StreamURL: "http://x"}); !errors.Is(err, ErrInvalidStream) {
		t.Fatalf("expected ErrInvalidStream for reserved id '*', got %v", err)
	}

	// Invalid type
	if _, err := coord.Trigger(VideoStream{ID: "c", StreamURL: "http://x", Type: "mp4"}); !errors.Is(err, ErrInvalidStream) {
		t.Fatalf("expected ErrInvalidStream for mp4, got %v", err)
	}

	// Invalid priority
	if _, err := coord.Trigger(VideoStream{ID: "c", StreamURL: "http://x", Priority: "urgent"}); !errors.Is(err, ErrInvalidStream) {
		t.Fatalf("expected ErrInvalidStream for urgent, got %v", err)
	}

	// Invalid player state
	if _, err := coord.Trigger(VideoStream{ID: "c", StreamURL: "http://x", PlayerState: "stopped"}); !errors.Is(err, ErrInvalidPlayerState) {
		t.Fatalf("expected ErrInvalidPlayerState for stopped, got %v", err)
	}
}

func TestCoordinator_TriggerAndPriorityStack(t *testing.T) {
	t.Parallel()

	tf := newMockTimerFactory()
	sink := &mockSink{}
	hub := events.NewHub(events.HubConfig{}, nil, nil)
	defer hub.Close()

	subCtx, subCancel := context.WithCancel(context.Background())
	defer subCancel()
	subCh, unsub := hub.Subscribe(subCtx)
	defer unsub()

	coord := NewCoordinator(hub, WithSink(sink), WithTimerFunc(tf.TimerFunc))
	defer coord.Close()

	// 1. Idle -> Trigger Persistent (Chromecast)
	s1, err := coord.Trigger(VideoStream{
		ID:           "chromecast",
		StreamURL:    "http://127.0.0.1:1984/cast",
		Type:         TypeWebRTC,
		Priority:     PriorityPersistent,
		Controllable: true,
		ControlURL:   "http://cast-watcher:8090/action",
	})
	if err != nil {
		t.Fatalf("unexpected trigger error: %v", err)
	}
	if s1.Mode != ModeVideo || s1.Primary == nil || s1.Primary.ID != "chromecast" || s1.Primary.Muted {
		t.Fatalf("expected chromecast primary unmuted, got %+v", s1.Primary)
	}
	if s1.Pip != nil {
		t.Fatalf("expected nil pip")
	}

	// Drain SSE event
	select {
	case evt := <-subCh:
		if evt.Type != events.EventVideoState {
			t.Fatalf("expected video.state event, got %s", evt.Type)
		}
	default:
		t.Fatalf("expected SSE event published")
	}

	// 2. Persistent active -> Doorbell triggers (Temporary)
	s2, err := coord.Trigger(VideoStream{
		ID:             "doorbell",
		StreamURL:      "http://ha:1984/doorbell",
		Type:           TypeWebRTC,
		Priority:       PriorityTemporary,
		TimeoutSeconds: 30,
	})
	if err != nil {
		t.Fatalf("unexpected trigger error: %v", err)
	}
	if s2.Primary.ID != "chromecast" || s2.Primary.Muted {
		t.Fatalf("expected chromecast to remain primary unmuted")
	}
	if s2.Pip == nil || s2.Pip.ID != "doorbell" || !s2.Pip.Muted {
		t.Fatalf("expected doorbell in PiP with muted=true, got %+v", s2.Pip)
	}

	// 3. Second alert triggers while doorbell is in PiP -> evicts prior alert
	s3, err := coord.Trigger(VideoStream{
		ID:        "backyard",
		StreamURL: "http://ha:1984/backyard",
		Type:      TypeWebRTC,
		Priority:  PriorityTemporary,
	})
	if err != nil {
		t.Fatalf("unexpected trigger error: %v", err)
	}
	if s3.Pip.ID != "backyard" || !s3.Pip.Muted {
		t.Fatalf("expected backyard in PiP, got %+v", s3.Pip)
	}

	// 4. Re-trigger backyard with same ID -> updates properties & muted stays true
	s4, err := coord.Trigger(VideoStream{
		ID:        "backyard",
		StreamURL: "http://ha:1984/backyard-hd",
		Type:      TypeHLS,
		Priority:  PriorityTemporary,
	})
	if err != nil {
		t.Fatalf("unexpected trigger error: %v", err)
	}
	if s4.Pip.StreamURL != "http://ha:1984/backyard-hd" || s4.Pip.Type != TypeHLS || !s4.Pip.Muted {
		t.Fatalf("expected backyard updated in PiP with muted=true, got %+v", s4.Pip)
	}

	// 5. Persistent dismisses while backyard is in PiP -> backyard promoted to Primary (unmuted)
	s5, err := coord.Dismiss("chromecast")
	if err != nil {
		t.Fatalf("unexpected dismiss error: %v", err)
	}
	if s5.Primary == nil || s5.Primary.ID != "backyard" || s5.Primary.Muted {
		t.Fatalf("expected backyard promoted to primary with muted=false, got %+v", s5.Primary)
	}
	if s5.Pip != nil {
		t.Fatalf("expected nil pip after promotion")
	}
	if s5.Mode != ModeVideo {
		t.Fatalf("expected mode video, got %s", s5.Mode)
	}

	// 6. Dismiss backyard -> returns to widgets
	s6, err := coord.Dismiss("backyard")
	if err != nil {
		t.Fatalf("unexpected dismiss error: %v", err)
	}
	if s6.Mode != ModeWidgets || s6.Primary != nil || s6.Pip != nil {
		t.Fatalf("expected mode widgets with nil streams, got %+v", s6)
	}
}

func TestCoordinator_DemotionAndTimerExpiry(t *testing.T) {
	t.Parallel()

	tf := newMockTimerFactory()
	coord := NewCoordinator(nil, WithTimerFunc(tf.TimerFunc))
	defer coord.Close()

	// 1. Idle -> Trigger Doorbell (Temporary in Primary, unmuted)
	s1, err := coord.Trigger(VideoStream{
		ID:             "doorbell",
		StreamURL:      "http://ha:1984/doorbell",
		Priority:       PriorityTemporary,
		TimeoutSeconds: 45,
	})
	if err != nil {
		t.Fatalf("unexpected trigger error: %v", err)
	}
	if s1.Primary == nil || s1.Primary.ID != "doorbell" || s1.Primary.Muted {
		t.Fatalf("expected doorbell in primary unmuted, got %+v", s1.Primary)
	}

	// 2. Persistent triggers while Doorbell in Primary -> Doorbell demoted to PiP (muted)
	s2, err := coord.Trigger(VideoStream{
		ID:        "chromecast",
		StreamURL: "http://127.0.0.1:1984/cast",
		Priority:  PriorityPersistent,
	})
	if err != nil {
		t.Fatalf("unexpected trigger error: %v", err)
	}
	if s2.Primary.ID != "chromecast" || s2.Primary.Muted {
		t.Fatalf("expected chromecast primary unmuted")
	}
	if s2.Pip == nil || s2.Pip.ID != "doorbell" || !s2.Pip.Muted {
		t.Fatalf("expected doorbell demoted to PiP with muted=true, got %+v", s2.Pip)
	}

	// 3. Fire auto-dismiss timer for doorbell -> PiP unmounts cleanly, Chromecast uninterrupted
	tf.fireAll()
	s3 := coord.GetState()
	if s3.Primary == nil || s3.Primary.ID != "chromecast" {
		t.Fatalf("expected chromecast primary still active")
	}
	if s3.Pip != nil {
		t.Fatalf("expected pip to be nil after timer expiry, got %+v", s3.Pip)
	}

	// 4. Timer expiry when temporary was in primary directly
	s4, _ := coord.Dismiss("chromecast")
	if s4.Mode != ModeWidgets {
		t.Fatalf("expected widgets mode")
	}

	_, _ = coord.Trigger(VideoStream{
		ID:        "doorbell-2",
		StreamURL: "http://ha:1984/doorbell-2",
		Priority:  PriorityTemporary,
	})
	if coord.GetState().Primary == nil {
		t.Fatalf("expected doorbell-2 in primary")
	}
	tf.fireAll()
	if coord.GetState().Mode != ModeWidgets || coord.GetState().Primary != nil {
		t.Fatalf("expected widgets mode after timer expiry on primary")
	}
}

func TestCoordinator_SetPlayerState(t *testing.T) {
	t.Parallel()

	coord := NewCoordinator(nil)
	defer coord.Close()

	_, err := coord.Trigger(VideoStream{
		ID:        "cast",
		StreamURL: "http://127.0.0.1:1984/cast",
		Priority:  PriorityPersistent,
	})
	if err != nil {
		t.Fatalf("trigger error: %v", err)
	}

	_, err = coord.Trigger(VideoStream{
		ID:        "doorbell",
		StreamURL: "http://ha/doorbell",
		Priority:  PriorityTemporary,
	})
	if err != nil {
		t.Fatalf("trigger error: %v", err)
	}

	// Update primary player state
	s1, err := coord.SetPlayerState("cast", PlayerStatePaused)
	if err != nil {
		t.Fatalf("set player state error: %v", err)
	}
	if s1.Primary.PlayerState != PlayerStatePaused {
		t.Fatalf("expected paused player state on primary")
	}

	// Update PiP player state
	s2, err := coord.SetPlayerState("doorbell", PlayerStateBuffering)
	if err != nil {
		t.Fatalf("set player state error: %v", err)
	}
	if s2.Pip.PlayerState != PlayerStateBuffering {
		t.Fatalf("expected buffering player state on pip")
	}

	// Update primary player state to idle
	sIdle, err := coord.SetPlayerState("cast", PlayerStateIdle)
	if err != nil {
		t.Fatalf("set player state idle error: %v", err)
	}
	if sIdle.Primary.PlayerState != PlayerStateIdle {
		t.Fatalf("expected idle player state on primary, got %s", sIdle.Primary.PlayerState)
	}

	// Errors
	if _, err := coord.SetPlayerState("cast", "unknown"); !errors.Is(err, ErrInvalidPlayerState) {
		t.Fatalf("expected ErrInvalidPlayerState, got %v", err)
	}
	if _, err := coord.SetPlayerState("missing", PlayerStatePlaying); !errors.Is(err, ErrStreamNotFound) {
		t.Fatalf("expected ErrStreamNotFound, got %v", err)
	}
	if _, err := coord.SetPlayerState("", PlayerStatePlaying); !errors.Is(err, ErrInvalidStream) {
		t.Fatalf("expected ErrInvalidStream, got %v", err)
	}
}

func TestCoordinator_ForwardAction(t *testing.T) {
	t.Parallel()

	var receivedAction ActionRequest
	var mu sync.Mutex

	sidecar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/fail" {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if r.URL.Path == "/timeout" {
			time.Sleep(100 * time.Millisecond)
			http.Error(w, "timeout", http.StatusGatewayTimeout)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&receivedAction)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer sidecar.Close()

	coord := NewCoordinator(nil, WithHTTPClient(sidecar.Client()))
	defer coord.Close()

	// Stream not found
	if err := coord.ForwardAction(context.Background(), "cast", ActionPlay, nil); !errors.Is(err, ErrStreamNotFound) {
		t.Fatalf("expected ErrStreamNotFound, got %v", err)
	}

	// Mount non-controllable stream
	_, _ = coord.Trigger(VideoStream{
		ID:           "non-ctrl",
		StreamURL:    "http://video",
		Priority:     PriorityPersistent,
		Controllable: false,
	})
	if err := coord.ForwardAction(context.Background(), "non-ctrl", ActionPlay, nil); !errors.Is(err, ErrNotControllable) {
		t.Fatalf("expected ErrNotControllable, got %v", err)
	}

	// Mount controllable stream with empty control_url
	_, _ = coord.Trigger(VideoStream{
		ID:           "no-url",
		StreamURL:    "http://video",
		Priority:     PriorityPersistent,
		Controllable: true,
		ControlURL:   "",
	})
	if err := coord.ForwardAction(context.Background(), "no-url", ActionPlay, nil); !errors.Is(err, ErrNotControllable) {
		t.Fatalf("expected ErrNotControllable for empty control_url, got %v", err)
	}

	// Mount valid controllable stream
	_, _ = coord.Trigger(VideoStream{
		ID:           "cast",
		StreamURL:    "http://video",
		Priority:     PriorityPersistent,
		Controllable: true,
		ControlURL:   sidecar.URL + "/action",
	})

	// Success
	if err := coord.ForwardAction(context.Background(), "cast", ActionTogglePlayback, nil); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	mu.Lock()
	if receivedAction.ID != "cast" || receivedAction.Action != ActionTogglePlayback {
		t.Fatalf("unexpected sidecar action received: %+v", receivedAction)
	}
	mu.Unlock()

	// Success (rewind)
	if err := coord.ForwardAction(context.Background(), "cast", ActionRewind, nil); err != nil {
		t.Fatalf("expected success for rewind, got %v", err)
	}
	mu.Lock()
	if receivedAction.ID != "cast" || receivedAction.Action != ActionRewind {
		t.Fatalf("unexpected sidecar action received for rewind: %+v", receivedAction)
	}
	mu.Unlock()

	// Success (fast_forward)
	if err := coord.ForwardAction(context.Background(), "cast", ActionFastForward, nil); err != nil {
		t.Fatalf("expected success for fast_forward, got %v", err)
	}
	mu.Lock()
	if receivedAction.ID != "cast" || receivedAction.Action != ActionFastForward {
		t.Fatalf("unexpected sidecar action received for fast_forward: %+v", receivedAction)
	}
	mu.Unlock()

	// Success (back, home, captions, next, previous)
	for _, act := range []string{ActionBack, ActionHome, ActionCaptions, ActionNext, ActionPrevious} {
		if err := coord.ForwardAction(context.Background(), "cast", act, nil); err != nil {
			t.Fatalf("expected success for %s, got %v", act, err)
		}
		mu.Lock()
		if receivedAction.ID != "cast" || receivedAction.Action != act {
			t.Fatalf("unexpected sidecar action received for %s: %+v", act, receivedAction)
		}
		mu.Unlock()
	}

	// Success (seek with float64 value)
	if err := coord.ForwardAction(context.Background(), "cast", ActionSeek, 128.5); err != nil {
		t.Fatalf("expected success for ActionSeek, got %v", err)
	}
	mu.Lock()
	if receivedAction.ID != "cast" || receivedAction.Action != ActionSeek {
		t.Fatalf("unexpected sidecar action received for seek: %+v", receivedAction)
	}
	if v, ok := receivedAction.Value.(float64); !ok || v != 128.5 {
		t.Fatalf("expected seek value 128.5, got %v (%T)", receivedAction.Value, receivedAction.Value)
	}
	mu.Unlock()

	// Invalid action
	if err := coord.ForwardAction(context.Background(), "cast", "unknown_action", nil); !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("expected ErrInvalidAction, got %v", err)
	}
	if err := coord.ForwardAction(context.Background(), "", ActionPlay, nil); !errors.Is(err, ErrInvalidStream) {
		t.Fatalf("expected ErrInvalidStream, got %v", err)
	}

	// Upstream error (500)
	_, _ = coord.Trigger(VideoStream{
		ID:           "cast-fail",
		StreamURL:    "http://video",
		Priority:     PriorityPersistent,
		Controllable: true,
		ControlURL:   sidecar.URL + "/fail",
	})
	if err := coord.ForwardAction(context.Background(), "cast-fail", ActionPlay, nil); !errors.Is(err, ErrControllerUnreachable) {
		t.Fatalf("expected ErrControllerUnreachable on 500, got %v", err)
	}
}

func TestCoordinator_Close(t *testing.T) {
	t.Parallel()

	coord := NewCoordinator(nil)
	coord.Close()

	if _, err := coord.Trigger(VideoStream{ID: "c", StreamURL: "http://x"}); !errors.Is(err, ErrStreamNotFound) {
		t.Fatalf("expected ErrStreamNotFound on closed coordinator, got %v", err)
	}
	if _, err := coord.Dismiss("c"); !errors.Is(err, ErrStreamNotFound) {
		t.Fatalf("expected ErrStreamNotFound on closed coordinator, got %v", err)
	}
	if _, err := coord.SetPlayerState("c", PlayerStatePlaying); !errors.Is(err, ErrStreamNotFound) {
		t.Fatalf("expected ErrStreamNotFound on closed coordinator, got %v", err)
	}
}

func TestCoordinator_DismissValidation(t *testing.T) {
	t.Parallel()

	coord := NewCoordinator(nil)
	defer coord.Close()

	if _, err := coord.Dismiss(""); !errors.Is(err, ErrInvalidStream) {
		t.Fatalf("expected ErrInvalidStream, got %v", err)
	}
	if _, err := coord.Dismiss("unknown"); !errors.Is(err, ErrStreamNotFound) {
		t.Fatalf("expected ErrStreamNotFound, got %v", err)
	}
}

func TestCoordinator_DismissAll(t *testing.T) {
	t.Parallel()

	// 1. Dismiss("all") when both primary and PiP are active
	coord := NewCoordinator(nil)
	defer coord.Close()

	_, err := coord.Trigger(VideoStream{
		ID:        "chromecast",
		StreamURL: "http://127.0.0.1:1984/cast",
		Type:      TypeWebRTC,
		Priority:  PriorityPersistent,
	})
	if err != nil {
		t.Fatalf("unexpected trigger error: %v", err)
	}

	_, err = coord.Trigger(VideoStream{
		ID:             "doorbell",
		StreamURL:      "http://127.0.0.1:1984/doorbell",
		Type:           TypeWebRTC,
		Priority:       PriorityTemporary,
		TimeoutSeconds: 30,
	})
	if err != nil {
		t.Fatalf("unexpected trigger error: %v", err)
	}

	state := coord.GetState()
	if state.Mode != ModeVideo || state.Primary == nil || state.Pip == nil {
		t.Fatalf("expected video mode with both primary and pip active, got %+v", state)
	}

	snap, err := coord.Dismiss("all")
	if err != nil {
		t.Fatalf("unexpected Dismiss('all') error: %v", err)
	}
	if snap.Mode != ModeWidgets || snap.Primary != nil || snap.Pip != nil {
		t.Fatalf("expected widgets mode with nil streams, got %+v", snap)
	}

	// 2. DismissAll() method when single stream is active
	_, err = coord.Trigger(VideoStream{
		ID:        "porch",
		StreamURL: "http://127.0.0.1:1984/porch",
		Type:      TypeWebRTC,
		Priority:  PriorityPersistent,
	})
	if err != nil {
		t.Fatalf("unexpected trigger error: %v", err)
	}

	snap2, err := coord.DismissAll()
	if err != nil {
		t.Fatalf("unexpected DismissAll error: %v", err)
	}
	if snap2.Mode != ModeWidgets || snap2.Primary != nil || snap2.Pip != nil {
		t.Fatalf("expected widgets mode, got %+v", snap2)
	}

	// 3. DismissAll() on idle coordinator returns ErrStreamNotFound
	if _, err := coord.DismissAll(); !errors.Is(err, ErrStreamNotFound) {
		t.Fatalf("expected ErrStreamNotFound on idle coordinator, got %v", err)
	}

	// 4. Wildcard Dismiss("*")
	_, err = coord.Trigger(VideoStream{
		ID:        "camera",
		StreamURL: "http://127.0.0.1:1984/camera",
		Type:      TypeWebRTC,
		Priority:  PriorityPersistent,
	})
	if err != nil {
		t.Fatalf("unexpected trigger error: %v", err)
	}

	snap3, err := coord.Dismiss("*")
	if err != nil {
		t.Fatalf("unexpected Dismiss('*') error: %v", err)
	}
	if snap3.Mode != ModeWidgets || snap3.Primary != nil || snap3.Pip != nil {
		t.Fatalf("expected widgets mode, got %+v", snap3)
	}

	// 5. DismissAll() on closed coordinator
	closedCoord := NewCoordinator(nil)
	closedCoord.Close()
	if _, err := closedCoord.DismissAll(); !errors.Is(err, ErrStreamNotFound) {
		t.Fatalf("expected ErrStreamNotFound on closed coordinator, got %v", err)
	}
}

func TestCoordinator_SchemaValidation(t *testing.T) {
	t.Parallel()

	schemaPath := filepath.Join("..", "..", "api", "schemas", "video.state.json")
	schemaBytes, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Skipf("skipping schema file test if not in root: %v", err)
	}

	var schema map[string]any
	if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		t.Fatalf("failed to parse schema: %v", err)
	}

	// Validate active state payload
	state := VideoState{
		Mode: ModeVideo,
		Primary: &VideoStream{
			ID:           "chromecast",
			StreamURL:    "http://127.0.0.1:1984/cast",
			Type:         TypeWebRTC,
			Priority:     PriorityPersistent,
			Controllable: true,
			PlayerState:  PlayerStatePlaying,
			Muted:        false,
		},
		Pip: &VideoStream{
			ID:             "doorbell",
			StreamURL:      "http://ha:1984/doorbell",
			Type:           TypeWebRTC,
			Priority:       PriorityTemporary,
			TimeoutSeconds: 45,
			Muted:          true,
		},
	}

	payload, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(payload, &parsed); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if parsed["mode"] != "video" {
		t.Fatalf("expected mode 'video'")
	}
	if parsed["primary"] == nil || parsed["pip"] == nil {
		t.Fatalf("expected primary and pip to be objects")
	}

	// Validate idle state payload
	idleState := VideoState{
		Mode:    ModeWidgets,
		Primary: nil,
		Pip:     nil,
	}
	idlePayload, err := json.Marshal(idleState)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var parsedIdle map[string]any
	if err := json.Unmarshal(idlePayload, &parsedIdle); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if parsedIdle["mode"] != "widgets" {
		t.Fatalf("expected mode 'widgets'")
	}
	if parsedIdle["primary"] != nil || parsedIdle["pip"] != nil {
		t.Fatalf("expected null primary and pip in idle state")
	}
}

func TestCoordinator_Concurrency(t *testing.T) {
	t.Parallel()

	coord := NewCoordinator(nil)
	defer coord.Close()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if id%2 == 0 {
				_, _ = coord.Trigger(VideoStream{
					ID:        "cast",
					StreamURL: "http://stream",
					Priority:  PriorityPersistent,
				})
			} else {
				_, _ = coord.Trigger(VideoStream{
					ID:        "doorbell",
					StreamURL: "http://stream",
					Priority:  PriorityTemporary,
				})
			}
			_ = coord.GetState()
			_, _ = coord.SetPlayerState("cast", PlayerStatePlaying)
			if id%5 == 0 {
				_, _ = coord.Dismiss("doorbell")
			}
		}(i)
	}

	wg.Wait()
}

func TestRealTimer_NilTimer(t *testing.T) {
	t.Parallel()

	rt := &realTimer{t: nil}
	if rt.Stop() {
		t.Fatalf("expected false on nil timer Stop")
	}
}

func TestCoordinator_PipForwardAction(t *testing.T) {
	t.Parallel()

	var receivedAction ActionRequest
	sidecar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&receivedAction)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer sidecar.Close()

	coord := NewCoordinator(nil, WithHTTPClient(sidecar.Client()))
	defer coord.Close()

	// Persistent in primary
	_, _ = coord.Trigger(VideoStream{
		ID:        "cast",
		StreamURL: "http://cast",
		Priority:  PriorityPersistent,
	})

	// Controllable in PiP
	_, _ = coord.Trigger(VideoStream{
		ID:           "pip-ctrl",
		StreamURL:    "http://pip",
		Priority:     PriorityTemporary,
		Controllable: true,
		ControlURL:   sidecar.URL + "/pip-action",
	})

	if err := coord.ForwardAction(context.Background(), "pip-ctrl", ActionPause, nil); err != nil {
		t.Fatalf("unexpected ForwardAction error on pip: %v", err)
	}
	if receivedAction.ID != "pip-ctrl" || receivedAction.Action != ActionPause {
		t.Fatalf("unexpected action: %+v", receivedAction)
	}
}

func TestCoordinator_TimerExpiredWhileClosed(t *testing.T) {
	t.Parallel()

	var capturedCB func()
	tf := func(d time.Duration, f func()) Timer {
		capturedCB = f
		return &mockTimer{}
	}

	coord := NewCoordinator(nil, WithTimerFunc(tf))
	_, _ = coord.Trigger(VideoStream{
		ID:        "temp",
		StreamURL: "http://temp",
		Priority:  PriorityTemporary,
	})

	coord.Close()
	if capturedCB != nil {
		capturedCB() // should do nothing since coord is closed
	}
}

func TestCoordinator_Trigger_DefaultPriorityPersistent(t *testing.T) {
	t.Parallel()

	coord := NewCoordinator(nil)
	defer coord.Close()

	state, err := coord.Trigger(VideoStream{
		ID:        "stream-no-priority",
		StreamURL: "http://stream",
	})
	if err != nil {
		t.Fatalf("unexpected error triggering stream without priority: %v", err)
	}

	if state.Primary == nil {
		t.Fatalf("expected primary stream to be mounted")
	}
	if state.Primary.Priority != PriorityPersistent {
		t.Errorf("expected Priority to default to %q, got %q", PriorityPersistent, state.Primary.Priority)
	}
	if state.Primary.TimeoutSeconds != 0 {
		t.Errorf("expected TimeoutSeconds for persistent stream to be 0, got %d", state.Primary.TimeoutSeconds)
	}
}

func TestCoordinator_MetadataAndPreserve(t *testing.T) {
	t.Parallel()

	coord := NewCoordinator(nil)
	defer coord.Close()

	// 1. Trigger stream with metadata and Position > 0, PositionUpdatedAt == 0
	s1, err := coord.Trigger(VideoStream{
		ID:        "chromecast",
		StreamURL: "http://127.0.0.1:1984/cast",
		Title:     "Episode 1",
		Series:    "Show A",
		Season:    "1",
		Episode:   "1",
		Position:  10.5,
		Duration:  3600,
	})
	if err != nil {
		t.Fatalf("unexpected trigger error: %v", err)
	}
	if s1.Primary.Title != "Episode 1" || s1.Primary.Series != "Show A" || s1.Primary.Season != "1" || s1.Primary.Episode != "1" {
		t.Fatalf("metadata not populated: %+v", s1.Primary)
	}
	if s1.Primary.Position != 10.5 || s1.Primary.Duration != 3600 {
		t.Fatalf("position/duration not populated: %+v", s1.Primary)
	}
	if s1.Primary.PositionUpdatedAt == 0 {
		t.Fatalf("expected PositionUpdatedAt to be auto-populated")
	}

	// 2. UpdatePlayerState patches stream metadata without wiping existing
	s2, err := coord.UpdatePlayerState("chromecast", VideoStream{
		PlayerState: PlayerStatePaused,
		Position:    25.0,
	})
	if err != nil {
		t.Fatalf("unexpected UpdatePlayerState error: %v", err)
	}
	if s2.Primary.PlayerState != PlayerStatePaused {
		t.Fatalf("expected paused player state, got %s", s2.Primary.PlayerState)
	}
	if s2.Primary.Title != "Episode 1" || s2.Primary.Series != "Show A" {
		t.Fatalf("expected existing title and series preserved, got title=%s series=%s", s2.Primary.Title, s2.Primary.Series)
	}
	if s2.Primary.Position != 25.0 {
		t.Fatalf("expected updated position 25.0, got %f", s2.Primary.Position)
	}

	// 3. Re-trigger stream with same ID preserves existing metadata when omitted
	s3, err := coord.Trigger(VideoStream{
		ID:        "chromecast",
		StreamURL: "http://127.0.0.1:1984/cast-hd",
	})
	if err != nil {
		t.Fatalf("unexpected re-trigger error: %v", err)
	}
	if s3.Primary.StreamURL != "http://127.0.0.1:1984/cast-hd" {
		t.Fatalf("stream URL not updated")
	}
	if s3.Primary.Title != "Episode 1" || s3.Primary.Series != "Show A" {
		t.Fatalf("metadata lost on re-trigger: %+v", s3.Primary)
	}
	if s3.Primary.Position != 25.0 {
		t.Fatalf("position lost on re-trigger: %+v", s3.Primary)
	}

	// 4. UpdatePlayerState with PlayerStateIdle purges position, duration, and stale show metadata
	sIdle, err := coord.UpdatePlayerState("chromecast", VideoStream{PlayerState: PlayerStateIdle})
	if err != nil {
		t.Fatalf("unexpected UpdatePlayerState idle error: %v", err)
	}
	if sIdle.Primary.PlayerState != PlayerStateIdle {
		t.Fatalf("expected idle player state, got %s", sIdle.Primary.PlayerState)
	}
	if sIdle.Primary.Position != 0 || sIdle.Primary.Duration != 0 {
		t.Fatalf("expected zeroed position and duration on idle, got pos=%f dur=%f", sIdle.Primary.Position, sIdle.Primary.Duration)
	}
	if sIdle.Primary.Title != "" || sIdle.Primary.Series != "" {
		t.Fatalf("expected purged title and series on idle, got title=%s series=%s", sIdle.Primary.Title, sIdle.Primary.Series)
	}

	// 5. UpdatePlayerState validation
	if _, err := coord.UpdatePlayerState("", VideoStream{PlayerState: PlayerStatePlaying}); !errors.Is(err, ErrInvalidStream) {
		t.Fatalf("expected ErrInvalidStream for empty id, got %v", err)
	}
	if _, err := coord.UpdatePlayerState("missing", VideoStream{PlayerState: PlayerStatePlaying}); !errors.Is(err, ErrStreamNotFound) {
		t.Fatalf("expected ErrStreamNotFound for missing stream, got %v", err)
	}
	if _, err := coord.UpdatePlayerState("chromecast", VideoStream{PlayerState: "invalid"}); !errors.Is(err, ErrInvalidPlayerState) {
		t.Fatalf("expected ErrInvalidPlayerState for invalid state, got %v", err)
	}
}

func TestCoordinator_UpdatePlayerStateAndPipMetadata(t *testing.T) {
	t.Parallel()

	coord := NewCoordinator(nil)
	defer coord.Close()

	// Trigger primary persistent
	_, _ = coord.Trigger(VideoStream{
		ID:        "primary-1",
		StreamURL: "http://primary",
		Priority:  PriorityPersistent,
	})

	// Trigger pip temporary with position > 0 and position_updated_at == 0
	_, err := coord.Trigger(VideoStream{
		ID:             "pip-1",
		StreamURL:      "http://pip",
		Priority:       PriorityTemporary,
		TimeoutSeconds: 45,
		Position:       50.0,
	})
	if err != nil {
		t.Fatalf("failed to trigger pip: %v", err)
	}

	// Re-trigger pip-1 with position == 0 to test preserveMetadata in pip re-trigger
	sPipRetrigger, err := coord.Trigger(VideoStream{
		ID:             "pip-1",
		StreamURL:      "http://pip-updated",
		Priority:       PriorityTemporary,
		TimeoutSeconds: 45,
	})
	if err != nil {
		t.Fatalf("failed to re-trigger pip: %v", err)
	}
	if sPipRetrigger.Pip.Position != 50.0 {
		t.Fatalf("expected preserved position on pip retrigger, got %f", sPipRetrigger.Pip.Position)
	}

	// UpdatePlayerState on PiP stream with full fields
	sUpdate, err := coord.UpdatePlayerState("pip-1", VideoStream{
		PlayerState:       PlayerStatePlaying,
		Title:             "New Pip Title",
		Series:            "New Pip Series",
		Season:            "S02",
		Episode:           "E04",
		Duration:          1200.0,
		Position:          60.0,
		PositionUpdatedAt: 123456789,
	})
	if err != nil {
		t.Fatalf("failed to update pip player state: %v", err)
	}
	if sUpdate.Pip.Title != "New Pip Title" || sUpdate.Pip.Series != "New Pip Series" || sUpdate.Pip.Season != "S02" || sUpdate.Pip.Episode != "E04" || sUpdate.Pip.Duration != 1200.0 || sUpdate.Pip.Position != 60.0 || sUpdate.Pip.PositionUpdatedAt != 123456789 {
		t.Fatalf("unexpected pip stream fields: %+v", sUpdate.Pip)
	}

	// UpdatePlayerState with position == 0 and position_updated_at > 0 (e.g. rewind to start)
	sZero, err := coord.UpdatePlayerState("pip-1", VideoStream{
		Position:          0,
		PositionUpdatedAt: 987654321,
	})
	if err != nil {
		t.Fatalf("failed to update position to zero: %v", err)
	}
	if sZero.Pip.Position != 0 || sZero.Pip.PositionUpdatedAt != 987654321 {
		t.Fatalf("unexpected zero position update: %+v", sZero.Pip)
	}

	// preserveMetadata edge cases: nil existing
	target := &VideoStream{Title: "Keep"}
	preserveMetadata(target, nil)
	if target.Title != "Keep" {
		t.Fatalf("preserveMetadata mutated target on nil existing")
	}

	// Closed coordinator returns ErrStreamNotFound
	coord.Close()
	if _, err := coord.UpdatePlayerState("pip-1", VideoStream{PlayerState: PlayerStatePlaying}); !errors.Is(err, ErrStreamNotFound) {
		t.Fatalf("expected ErrStreamNotFound on closed coordinator, got %v", err)
	}
}

