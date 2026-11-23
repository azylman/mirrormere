package video

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/events"
	"github.com/azylman/mirrormere/internal/testutil"
)

func WithHTTPClient(client *http.Client) CoordinatorOption {
	return func(c *Coordinator) {
		if client != nil {
			c.httpClient = client
		}
	}
}

func WithTimerFunc(tf TimerFunc) CoordinatorOption {
	return func(c *Coordinator) {
		if tf != nil {
			c.timerFunc = tf
		}
	}
}

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
	timers []*mockTimer
}

func (f *mockTimerFactory) TimerFunc(_ time.Duration, fn func()) Timer {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := &mockTimer{fn: fn}
	f.timers = append(f.timers, t)
	return t
}

func (f *mockTimerFactory) fireAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.timers {
		t.fire()
	}
}

type mockSink struct {
	mu    sync.Mutex
	state *events.VideoStateData
}

func (s *mockSink) SetVideoState(st *events.VideoStateData) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = st
}

func mkStream(id, url, sType, prio string, timeout int) VideoStream {
	return VideoStream{ID: id, StreamURL: url, Type: sType, Priority: prio, TimeoutSeconds: timeout}
}

func mustTrigger(t *testing.T, coord *Coordinator, s VideoStream) VideoState {
	t.Helper()
	st, err := coord.Trigger(s)
	if err != nil {
		t.Fatalf("unexpected Trigger(%+v) error: %v", s, err)
	}
	return st
}

func mustDismiss(t *testing.T, coord *Coordinator, id string) VideoState {
	t.Helper()
	st, err := coord.Dismiss(id)
	if err != nil {
		t.Fatalf("unexpected Dismiss(%q) error: %v", id, err)
	}
	return st
}

func mustUpdatePlayerState(t *testing.T, coord *Coordinator, id string, update VideoStream) VideoState {
	t.Helper()
	st, err := coord.UpdatePlayerState(id, update)
	if err != nil {
		t.Fatalf("unexpected UpdatePlayerState(%q, %+v) error: %v", id, update, err)
	}
	return st
}

func TestCoordinator_InitAndClose(t *testing.T) {
	t.Parallel()
	sink := &mockSink{}
	hub := events.NewHub(events.HubConfig{}, nil, nil)
	t.Cleanup(hub.Close)
	coord := NewCoordinator(hub, WithSink(sink))
	t.Cleanup(coord.Close)

	if st := coord.GetState(); st.Mode != ModeWidgets || st.Primary != nil || st.Pip != nil {
		t.Fatalf("unexpected init state: %+v", st)
	}
	if sink.state == nil || sink.state.Mode != ModeWidgets {
		t.Fatalf("unexpected seeded sink: %+v", sink.state)
	}
	coord.Close()
	if _, err := coord.UpdatePlayerState("c", VideoStream{PlayerState: PlayerStatePlaying}); !errors.Is(err, ErrStreamNotFound) {
		t.Fatalf("UpdatePlayerState on closed coordinator = %v, want ErrStreamNotFound", err)
	}

	var capturedCB func()
	c2 := NewCoordinator(nil, WithTimerFunc(func(_ time.Duration, f func()) Timer {
		capturedCB = f
		return &mockTimer{}
	}))
	mustTrigger(t, c2, mkStream("t", "u", "", PriorityTemporary, 10))
	c2.Close()
	if capturedCB != nil {
		capturedCB()
	}
	if (&realTimer{}).Stop() {
		t.Fatal("expected false on nil timer Stop")
	}
}

func TestCoordinator_TriggerValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		stream     VideoStream
		closeFirst bool
		wantErr    error
	}{
		{"empty", VideoStream{}, false, ErrInvalidStream},
		{"no_url", VideoStream{ID: "c"}, false, ErrInvalidStream},
		{"reserved_all", VideoStream{ID: "all", StreamURL: "u"}, false, ErrInvalidStream},
		{"reserved_star", VideoStream{ID: "*", StreamURL: "u"}, false, ErrInvalidStream},
		{"bad_type", VideoStream{ID: "c", StreamURL: "u", Type: "mp4"}, false, ErrInvalidStream},
		{"bad_priority", VideoStream{ID: "c", StreamURL: "u", Priority: "urgent"}, false, ErrInvalidStream},
		{"bad_player_state", VideoStream{ID: "c", StreamURL: "u", PlayerState: "stopped"}, false, ErrInvalidPlayerState},
		{"closed", mkStream("c", "u", "", "", 0), true, ErrStreamNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			coord := NewCoordinator(nil)
			t.Cleanup(coord.Close)
			if tc.closeFirst {
				coord.Close()
			}
			if _, err := coord.Trigger(tc.stream); !errors.Is(err, tc.wantErr) {
				t.Fatalf("Trigger() err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestCoordinator_Dismiss(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, id   string
		closeFirst bool
		wantErr    error
	}{
		{"empty", "", false, ErrInvalidStream},
		{"not_found", "missing", false, ErrStreamNotFound},
		{"all_idle", "all", false, ErrStreamNotFound},
		{"closed_id", "c", true, ErrStreamNotFound},
		{"closed_all", "all", true, ErrStreamNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			coord := NewCoordinator(nil)
			t.Cleanup(coord.Close)
			if tc.closeFirst {
				coord.Close()
			}
			if _, err := coord.Dismiss(tc.id); !errors.Is(err, tc.wantErr) {
				t.Fatalf("Dismiss(%q) err = %v, want %v", tc.id, err, tc.wantErr)
			}
		})
	}
}

func assertPriorityStackTransitions(t *testing.T, s1, s2, s3, s4, s5, s6 VideoState) {
	t.Helper()
	assertActiveStreamStates(t, s1, s2, s3, s4)
	assertDismissalStreamStates(t, s5, s6)
}

func assertActiveStreamStates(t *testing.T, s1, s2, s3, s4 VideoState) {
	t.Helper()
	if s1.Mode != ModeVideo || s1.Primary == nil || s1.Primary.Muted || s1.Pip != nil {
		t.Fatalf("unexpected s1: %+v", s1)
	}
	if s2.Primary.ID != "chromecast" || s2.Pip == nil || s2.Pip.ID != "doorbell" || !s2.Pip.Muted {
		t.Fatalf("unexpected s2: %+v", s2)
	}
	if s3.Pip.ID != "backyard" || !s3.Pip.Muted {
		t.Fatalf("unexpected s3: %+v", s3)
	}
	if s4.Pip.StreamURL != "http://backyard-hd" || s4.Pip.Type != TypeHLS || !s4.Pip.Muted {
		t.Fatalf("unexpected s4: %+v", s4)
	}
}

func assertDismissalStreamStates(t *testing.T, s5, s6 VideoState) {
	t.Helper()
	if s5.Primary == nil || s5.Primary.ID != "backyard" || s5.Primary.Muted || s5.Pip != nil {
		t.Fatalf("unexpected s5: %+v", s5)
	}
	if s6.Mode != ModeWidgets || s6.Primary != nil || s6.Pip != nil {
		t.Fatalf("unexpected s6: %+v", s6)
	}
}

func assertPlayerStateUpdateCase(t *testing.T, coord *Coordinator, id, state, wantPS string, wantErr error) {
	t.Helper()
	snap, err := coord.UpdatePlayerState(id, VideoStream{PlayerState: state})
	if !errors.Is(err, wantErr) {
		t.Fatalf("UpdatePlayerState(%s, %s) err=%v, want %v", id, state, err, wantErr)
	}
	if wantErr != nil {
		return
	}
	var ps string
	if id == "cast" && snap.Primary != nil {
		ps = snap.Primary.PlayerState
	}
	if id == "doorbell" && snap.Pip != nil {
		ps = snap.Pip.PlayerState
	}
	if ps != wantPS {
		t.Fatalf("got player state %s, want %s", ps, wantPS)
	}
}

func assertMetadataPreservation(t *testing.T, sMeta, sPatch, sRetrig, sPurge VideoState) {
	t.Helper()
	if sMeta.Primary.Title != "Episode 1" || sMeta.Primary.Position != 10.5 || sMeta.Primary.PositionUpdatedAt == 0 {
		t.Fatalf("unexpected sMeta: %+v", sMeta.Primary)
	}
	if sPatch.Primary.Title != "Episode 1" || sPatch.Primary.Position != 25.0 || sPatch.Primary.PlayerState != PlayerStatePaused {
		t.Fatalf("unexpected sPatch: %+v", sPatch.Primary)
	}
	if sRetrig.Primary.StreamURL != "http://cast-hd" || sRetrig.Primary.Title != "Episode 1" || sRetrig.Primary.Position != 25.0 {
		t.Fatalf("unexpected sRetrig: %+v", sRetrig.Primary)
	}
	if sPurge.Primary.Title != "" || sPurge.Primary.Position != 0 || sPurge.Primary.Duration != 0 {
		t.Fatalf("unexpected sPurge: %+v", sPurge.Primary)
	}
}

func assertMovieMetadataTransitions(t *testing.T, coord *Coordinator) {
	t.Helper()
	mustTrigger(t, coord, VideoStream{
		ID: "chromecast", StreamURL: "http://cast-tv", Priority: PriorityPersistent,
		Title: "Ep 1", Series: "Show A", Season: "1", Episode: "2", Duration: 3600,
	})
	sMovie, err := coord.UpdatePlayerState("chromecast", VideoStream{
		PlayerState: PlayerStatePlaying,
		Title:       "Movie Title",
	})
	if err != nil || sMovie.Primary.Title != "Movie Title" || sMovie.Primary.Series != "" || sMovie.Primary.Season != "" || sMovie.Primary.Episode != "" || sMovie.Primary.Duration != 3600 {
		t.Fatalf("expected series/season/episode cleared and duration preserved on movie title update, got: %+v (err: %v)", sMovie.Primary, err)
	}
	mustTrigger(t, coord, VideoStream{
		ID: "chromecast", StreamURL: "http://cast-tv", Priority: PriorityPersistent,
		Title: "Ep 2", Series: "Show A", Season: "1", Episode: "3", Duration: 1800,
	})
	sRetrigMovie, err := coord.Trigger(VideoStream{
		ID: "chromecast", StreamURL: "http://cast-movie", Priority: PriorityPersistent,
		Title: "Standalone Movie",
	})
	if err != nil || sRetrigMovie.Primary.Title != "Standalone Movie" || sRetrigMovie.Primary.Series != "" || sRetrigMovie.Primary.Season != "" || sRetrigMovie.Primary.Episode != "" || sRetrigMovie.Primary.Duration != 0 {
		t.Fatalf("expected re-triggered movie stream not to inherit stale episode metadata, got: %+v (err: %v)", sRetrigMovie.Primary, err)
	}
}

func TestCoordinator_PriorityStackAndTimers(t *testing.T) {
	t.Parallel()
	tf := &mockTimerFactory{}
	sink := &mockSink{}
	hub := events.NewHub(events.HubConfig{}, nil, nil)
	defer hub.Close()
	subCtx, subCancel := context.WithCancel(context.Background())
	defer subCancel()
	subCh, unsub := hub.Subscribe(subCtx)
	defer unsub()

	coord := NewCoordinator(hub, WithSink(sink), WithTimerFunc(tf.TimerFunc))
	defer coord.Close()

	// 1-6. Priority stack transitions
	s1 := mustTrigger(t, coord, mkStream("chromecast", "http://cast", TypeWebRTC, PriorityPersistent, 0))
	select {
	case evt := <-subCh:
		if evt.Type != events.EventVideoState {
			t.Fatalf("expected video.state event, got %s", evt.Type)
		}
	default:
		t.Fatal("expected SSE event published")
	}
	s2 := mustTrigger(t, coord, mkStream("doorbell", "http://doorbell", TypeWebRTC, PriorityTemporary, 30))
	s3 := mustTrigger(t, coord, mkStream("backyard", "http://backyard", TypeWebRTC, PriorityTemporary, 0))
	s4 := mustTrigger(t, coord, mkStream("backyard", "http://backyard-hd", TypeHLS, PriorityTemporary, 0))
	s5 := mustDismiss(t, coord, "chromecast")
	s6 := mustDismiss(t, coord, "backyard")
	assertPriorityStackTransitions(t, s1, s2, s3, s4, s5, s6)

	// 7. Demotion and timer expiry
	mustTrigger(t, coord, mkStream("doorbell", "http://doorbell", "", PriorityTemporary, 45))
	s7 := mustTrigger(t, coord, mkStream("chromecast", "http://cast", "", PriorityPersistent, 0))
	tf.fireAll()
	st7 := coord.GetState()
	if s7.Primary.ID != "chromecast" || s7.Pip == nil || !s7.Pip.Muted || st7.Primary == nil || st7.Pip != nil {
		t.Fatalf("unexpected demotion/timer: s7=%+v st7=%+v", s7, st7)
	}

	// 8-11. Primary timer expiry, Dismiss("all"), and Dismiss("*")
	mustDismiss(t, coord, "chromecast")
	mustTrigger(t, coord, mkStream("alert", "http://alert", "", PriorityTemporary, 45))
	tf.fireAll()
	mustTrigger(t, coord, mkStream("p", "http://p", "", PriorityPersistent, 0))
	mustTrigger(t, coord, mkStream("a", "http://a", "", PriorityTemporary, 30))
	sAll := mustDismiss(t, coord, "all")
	mustTrigger(t, coord, mkStream("p", "http://p", "", PriorityPersistent, 0))
	sW := mustDismiss(t, coord, "*")
	defSnap := mustTrigger(t, coord, VideoStream{ID: "def", StreamURL: "http://def"})
	if sAll.Mode != ModeWidgets || sW.Mode != ModeWidgets || defSnap.Primary.Priority != PriorityPersistent || defSnap.Primary.TimeoutSeconds != 0 {
		t.Fatal("unexpected dismiss/defaults")
	}
}

func TestCoordinator_PlayerStateAndMetadata(t *testing.T) {
	t.Parallel()
	coord := NewCoordinator(nil)
	defer coord.Close()

	mustTrigger(t, coord, mkStream("cast", "http://cast", "", PriorityPersistent, 0))
	mustTrigger(t, coord, mkStream("doorbell", "http://doorbell", "", PriorityTemporary, 45))

	for _, tc := range []struct {
		id, state string
		wantPS    string
		wantErr   error
	}{
		{"cast", PlayerStatePaused, PlayerStatePaused, nil},
		{"doorbell", PlayerStateBuffering, PlayerStateBuffering, nil},
		{"cast", PlayerStateIdle, PlayerStateIdle, nil},
		{"cast", "unknown", "", ErrInvalidPlayerState},
		{"missing", PlayerStatePlaying, "", ErrStreamNotFound},
		{"", PlayerStatePlaying, "", ErrInvalidStream},
	} {
		assertPlayerStateUpdateCase(t, coord, tc.id, tc.state, tc.wantPS, tc.wantErr)
	}

	// Metadata preservation and patching
	sMeta := mustTrigger(t, coord, VideoStream{
		ID: "chromecast", StreamURL: "http://cast", Priority: PriorityPersistent,
		Title: "Episode 1", Series: "Show A", Season: "1", Episode: "1", Position: 10.5, Duration: 3600,
	})
	sPatch := mustUpdatePlayerState(t, coord, "chromecast", VideoStream{PlayerState: PlayerStatePaused, Position: 25.0})
	sRetrig := mustTrigger(t, coord, VideoStream{ID: "chromecast", StreamURL: "http://cast-hd"})
	sPurge := mustUpdatePlayerState(t, coord, "chromecast", VideoStream{PlayerState: PlayerStateIdle})
	assertMetadataPreservation(t, sMeta, sPatch, sRetrig, sPurge)

	assertMovieMetadataTransitions(t, coord)

	for _, check := range []struct {
		id, ps  string
		wantErr error
	}{
		{"", PlayerStatePlaying, ErrInvalidStream},
		{"missing", PlayerStatePlaying, ErrStreamNotFound},
		{"chromecast", "invalid", ErrInvalidPlayerState},
	} {
		if _, err := coord.UpdatePlayerState(check.id, VideoStream{PlayerState: check.ps}); !errors.Is(err, check.wantErr) {
			t.Fatalf("UpdatePlayerState(%s, %s) got %v, want %v", check.id, check.ps, err, check.wantErr)
		}
	}

	mustTrigger(t, coord, VideoStream{ID: "pip-1", StreamURL: "http://pip", Priority: PriorityTemporary, TimeoutSeconds: 45, Position: 50.0})
	sPipRetrig := mustTrigger(t, coord, VideoStream{ID: "pip-1", StreamURL: "http://pip-updated", Priority: PriorityTemporary, TimeoutSeconds: 45})
	sPipUp := mustUpdatePlayerState(t, coord, "pip-1", VideoStream{PlayerState: PlayerStatePlaying, Title: "PipT", Series: "PipS", Season: "S1", Episode: "E1", Duration: 1200.0, Position: 60.0, PositionUpdatedAt: 12345})
	sRewind := mustUpdatePlayerState(t, coord, "pip-1", VideoStream{Position: 0, PositionUpdatedAt: 98765})
	if sPipRetrig.Pip.Position != 50.0 || sPipUp.Pip.Title != "PipT" || sPipUp.Pip.Position != 60.0 || sRewind.Pip.Position != 0 || sRewind.Pip.PositionUpdatedAt != 98765 {
		t.Fatal("unexpected pip metadata")
	}
	preserveMetadata(&VideoStream{Title: "Keep"}, nil)
}

func TestCoordinator_ForwardAction(t *testing.T) {
	t.Parallel()
	var receivedAction ActionRequest
	var mu sync.Mutex

	sidecar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/fail" {
			http.Error(w, "server error", http.StatusInternalServerError)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&receivedAction); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer sidecar.Close()

	coord := NewCoordinator(nil, WithHTTPClient(sidecar.Client()))
	defer coord.Close()

	if err := coord.ForwardAction(context.Background(), "cast", ActionPlay, nil); !errors.Is(err, ErrStreamNotFound) {
		t.Fatalf("expected ErrStreamNotFound, got %v", err)
	}
	mustTrigger(t, coord, VideoStream{ID: "non-ctrl", StreamURL: "http://v", Priority: PriorityPersistent})
	if err := coord.ForwardAction(context.Background(), "non-ctrl", ActionPlay, nil); !errors.Is(err, ErrNotControllable) {
		t.Fatalf("expected ErrNotControllable, got %v", err)
	}
	mustTrigger(t, coord, VideoStream{ID: "no-url", StreamURL: "http://v", Priority: PriorityPersistent, Controllable: true})
	if err := coord.ForwardAction(context.Background(), "no-url", ActionPlay, nil); !errors.Is(err, ErrNotControllable) {
		t.Fatalf("expected ErrNotControllable for empty control_url, got %v", err)
	}

	mustTrigger(t, coord, VideoStream{ID: "cast", StreamURL: "http://v", Priority: PriorityPersistent, Controllable: true, ControlURL: sidecar.URL + "/action"})
	mustTrigger(t, coord, VideoStream{ID: "pip-ctrl", StreamURL: "http://v", Priority: PriorityTemporary, Controllable: true, ControlURL: sidecar.URL + "/pip-action"})

	for _, act := range []string{ActionTogglePlayback, ActionRewind, ActionFastForward, ActionBack, ActionHome, ActionCaptions, ActionNext, ActionPrevious} {
		if err := coord.ForwardAction(context.Background(), "cast", act, nil); err != nil {
			t.Fatalf("action %s got err %v", act, err)
		}
	}

	for _, tc := range []struct {
		id, act string
		val     any
		wantErr error
	}{
		{"cast", ActionSeek, 128.5, nil},
		{"pip-ctrl", ActionPause, nil, nil},
		{"cast", "invalid_action", nil, ErrInvalidAction},
		{"", ActionPlay, nil, ErrInvalidStream},
	} {
		if err := coord.ForwardAction(context.Background(), tc.id, tc.act, tc.val); !errors.Is(err, tc.wantErr) {
			t.Fatalf("action %s got err %v, want %v", tc.act, err, tc.wantErr)
		} else if tc.wantErr == nil {
			mu.Lock()
			if receivedAction.ID != tc.id || receivedAction.Action != tc.act {
				t.Fatalf("received action mismatch: %+v", receivedAction)
			}
			if tc.act == ActionSeek && testutil.As[float64](t, receivedAction.Value) != 128.5 {
				t.Fatalf("seek value mismatch: %v", receivedAction.Value)
			}
			mu.Unlock()
		}
	}

	mustTrigger(t, coord, VideoStream{ID: "fail", StreamURL: "http://v", Priority: PriorityPersistent, Controllable: true, ControlURL: sidecar.URL + "/fail"})
	if err := coord.ForwardAction(context.Background(), "fail", ActionPlay, nil); !errors.Is(err, ErrControllerUnreachable) {
		t.Fatalf("expected ErrControllerUnreachable, got %v", err)
	}
}

func TestCoordinator_Concurrency(t *testing.T) {
	t.Parallel()
	coord := NewCoordinator(nil)
	defer coord.Close()

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if id%2 == 0 {
				if _, err := coord.Trigger(mkStream("cast", "http://stream", "", PriorityPersistent, 0)); err != nil {
					t.Errorf("Trigger failed: %v", err)
				}
			} else {
				if _, err := coord.Trigger(mkStream("doorbell", "http://stream", "", PriorityTemporary, 45)); err != nil {
					t.Errorf("Trigger failed: %v", err)
				}
			}
			if st := coord.GetState(); st.Mode == "" {
				t.Error("expected non-empty mode from GetState")
			}
			if _, err := coord.UpdatePlayerState("cast", VideoStream{PlayerState: PlayerStatePlaying}); err != nil && !errors.Is(err, ErrStreamNotFound) {
				t.Errorf("UpdatePlayerState failed: %v", err)
			}
			if id%5 == 0 {
				if _, err := coord.Dismiss("doorbell"); err != nil && !errors.Is(err, ErrStreamNotFound) {
					t.Errorf("Dismiss failed: %v", err)
				}
			}
		}(i)
	}
	wg.Wait()
}
