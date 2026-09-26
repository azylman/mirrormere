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

	// Invalid action
	if err := coord.ForwardAction(context.Background(), "cast", "rewind", nil); !errors.Is(err, ErrInvalidAction) {
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

