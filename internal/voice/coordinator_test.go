package voice

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/events"
)

type mockSink struct {
	mu     sync.Mutex
	states []*events.VoiceStateData
}

func (m *mockSink) SetVoiceState(state *events.VoiceStateData) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states = append(m.states, state)
}

func (m *mockSink) LastState() *events.VoiceStateData {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.states) == 0 {
		return nil
	}
	return m.states[len(m.states)-1]
}

func strPtr(s string) *string {
	return &s
}

func TestCoordinator_StateAndStatusTransitions(t *testing.T) {
	t.Parallel()

	// Initial defaults
	if st := NewCoordinator(nil).GetState(); st.State != StateIdle || st.Transcript != nil || st.Reply != nil || st.TTSEngine != nil || st.Status != nil {
		t.Fatalf("expected idle with nil fields, got %+v", st)
	}
	sinkInit := &mockSink{}
	_ = NewCoordinator(nil, sinkInit)
	if last := sinkInit.LastState(); last == nil || last.State != StateIdle {
		t.Fatalf("expected initial sink state idle, got %+v", last)
	}

	// IsValidState validation
	for _, tc := range []struct {
		s     string
		valid bool
	}{
		{StateIdle, true}, {StateListening, true}, {StateTranscribing, true},
		{StateThinking, true}, {StateSynthesizing, true}, {StateSpeaking, true},
		{StateError, true}, {"", false}, {"running", false}, {"LISTENING", false},
	} {
		if got := IsValidState(tc.s); got != tc.valid {
			t.Errorf("IsValidState(%q) = %v, want %v", tc.s, got, tc.valid)
		}
	}

	// Valid states lifecycle with Hub and Sink
	sink := &mockSink{}
	hub := events.NewHub(events.HubConfig{RingCapacity: 10, RingTTL: time.Minute}, nil, nil)
	t.Cleanup(func() { hub.Close() })
	subCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sub, unsub := hub.Subscribe(subCtx)
	defer unsub()

	c := NewCoordinator(hub, sink)

	validCases := []struct {
		state      string
		transcript *string
		reply      *string
		engine     *string
		status     *string
	}{
		{StateIdle, nil, nil, nil, nil},
		{StateListening, strPtr("listening..."), nil, nil, nil},
		{StateTranscribing, strPtr("Turn on kitchen"), nil, nil, nil},
		{StateThinking, strPtr("Turn on kitchen lights"), nil, nil, strPtr("⚡ Running tool...")},
		{StateSynthesizing, strPtr("Turn on kitchen lights"), strPtr("Turning on lights"), strPtr("kokoro"), nil},
		{StateSpeaking, strPtr("Turn on kitchen lights"), strPtr("Turning on lights"), strPtr("kokoro"), nil},
		{StateError, nil, nil, nil, strPtr("network error")},
	}

	for _, tc := range validCases {
		st, err := c.SetFullState(tc.state, tc.transcript, tc.reply, tc.engine, tc.status)
		if err != nil || st.State != tc.state {
			t.Fatalf("SetFullState(%s): err=%v, state=%s", tc.state, err, st.State)
		}
		if last := sink.LastState(); last == nil || last.State != tc.state {
			t.Fatalf("sink state mismatch: %+v", last)
		}
		select {
		case evt := <-sub:
			var vs events.VoiceStateData
			if err := json.Unmarshal(evt.Data, &vs); err != nil || vs.State != tc.state {
				t.Fatalf("SSE mismatch for state %s: %+v", tc.state, vs)
			}
		case <-time.After(time.Second):
			t.Fatalf("timeout waiting for SSE event for state %s", tc.state)
		}
	}

	// SetState clears status
	st, err := c.SetState(StateThinking, strPtr("q"), nil, nil)
	if err != nil || st.Status != nil {
		t.Fatalf("SetState should clear status: %+v", st)
	}
	<-sub

	// SetStatus updates status preserving state
	newStatus := "⚡ Querying Home Assistant..."
	st = c.SetStatus(&newStatus)
	if st.State != StateThinking || st.Status == nil || *st.Status != newStatus {
		t.Fatalf("SetStatus mismatch: %+v", st)
	}
	<-sub

	// Reset clears all fields and returns to idle
	st = c.Reset()
	if st.State != StateIdle || st.Transcript != nil || st.Reply != nil || st.TTSEngine != nil || st.Status != nil {
		t.Fatalf("Reset mismatch: %+v", st)
	}
	<-sub

	// Invalid states
	for _, inv := range []string{"", "IDLE", "pending", "waiting", "unknown", " listening"} {
		if _, err := c.SetState(inv, nil, nil, nil); !errors.Is(err, ErrInvalidState) {
			t.Errorf("SetState(%q) error = %v, want ErrInvalidState", inv, err)
		}
		if _, err := c.SetFullState(inv, nil, nil, nil, nil); !errors.Is(err, ErrInvalidState) {
			t.Errorf("SetFullState(%q) error = %v, want ErrInvalidState", inv, err)
		}
	}
}

func TestCoordinator_IdleTransitions(t *testing.T) {
	t.Parallel()
	states := []string{StateListening, StateTranscribing, StateThinking, StateSpeaking, StateSynthesizing, StateError}
	for _, st := range states {
		txt := "test"
		c := NewCoordinator(nil)
		c.Transition(st, &txt, &txt, &txt, &txt)
		if s, err := c.SetState(StateIdle, nil, nil, nil); err != nil || s.State != StateIdle {
			t.Fatalf("SetState(StateIdle) from %s: err=%v, state=%s", st, err, s.State)
		}

		c.Transition(st, &txt, &txt, &txt, &txt)
		if s, err := c.SetFullState(StateIdle, nil, nil, nil, nil); err != nil || s.State != StateIdle {
			t.Fatalf("SetFullState(StateIdle) from %s: err=%v, state=%s", st, err, s.State)
		}

		c.Transition(st, &txt, &txt, &txt, &txt)
		if s := c.Reset(); s.State != StateIdle {
			t.Fatalf("Reset() from %s: state=%s", st, s.State)
		}

		c.Transition(st, &txt, &txt, &txt, &txt)
		if s := c.Transition(StateIdle, nil, nil, nil, nil); s.State != StateIdle {
			t.Fatalf("Transition(StateIdle) from %s: state=%s", st, s.State)
		}
	}
}

func TestCoordinator_SpeakingSafetyWatchdog(t *testing.T) {
	t.Parallel()

	t.Run("auto-resets to idle after speaking timeout", func(t *testing.T) {
		t.Parallel()
		hub := events.NewHub(events.HubConfig{RingCapacity: 10, RingTTL: time.Minute}, nil, nil)
		t.Cleanup(func() { hub.Close() })
		subCtx, cancel := context.WithCancel(context.Background())
		defer cancel()
		sub, unsub := hub.Subscribe(subCtx)
		defer unsub()

		c := NewCoordinator(hub)
		c.SetSpeakingTimeout(10 * time.Millisecond)
		txt := "hello"
		c.Transition(StateSpeaking, &txt, nil, nil, nil)

		select {
		case <-sub:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for speaking event")
		}

		select {
		case evt := <-sub:
			var data events.VoiceStateData
			if err := json.Unmarshal(evt.Data, &data); err != nil || data.State != StateIdle {
				t.Fatalf("expected auto-reset to idle, got %+v", data)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for watchdog auto-reset")
		}
		if st := c.GetState(); st.State != StateIdle {
			t.Fatalf("expected state idle, got %s", st.State)
		}
	})

	t.Run("defaults to 30s when timeout non-positive", func(t *testing.T) {
		t.Parallel()
		c := NewCoordinator(nil)
		c.SetSpeakingTimeout(0)
		txt := "hello"
		c.Transition(StateSpeaking, &txt, nil, nil, nil)
		c.Stop()
	})

	t.Run("transition out of speaking cancels watchdog", func(t *testing.T) {
		t.Parallel()
		c := NewCoordinator(nil)
		c.SetSpeakingTimeout(10 * time.Millisecond)
		txt := "hello"
		c.Transition(StateSpeaking, &txt, nil, nil, nil)
		c.Transition(StateError, nil, nil, nil, nil)

		tm := time.NewTimer(25 * time.Millisecond)
		defer tm.Stop()
		<-tm.C

		if st := c.GetState(); st.State != StateError {
			t.Fatalf("expected state error to remain, got %s", st.State)
		}
	})

	t.Run("Reset cancels watchdog timer", func(t *testing.T) {
		t.Parallel()
		c := NewCoordinator(nil)
		c.SetSpeakingTimeout(10 * time.Millisecond)
		txt := "hello"
		c.Transition(StateSpeaking, &txt, nil, nil, nil)
		c.Reset()
		c.Transition(StateListening, nil, nil, nil, nil)

		tm := time.NewTimer(25 * time.Millisecond)
		defer tm.Stop()
		<-tm.C

		if st := c.GetState(); st.State != StateListening {
			t.Fatalf("expected state listening to remain, got %s", st.State)
		}
	})

	t.Run("Stop cancels watchdog timer", func(t *testing.T) {
		t.Parallel()
		c := NewCoordinator(nil)
		c.SetSpeakingTimeout(10 * time.Millisecond)
		txt := "hello"
		c.Transition(StateSpeaking, &txt, nil, nil, nil)
		c.Stop()

		tm := time.NewTimer(25 * time.Millisecond)
		defer tm.Stop()
		<-tm.C

		if st := c.GetState(); st.State != StateSpeaking {
			t.Fatalf("expected state speaking after Stop(), got %s", st.State)
		}
		c.Reset()
	})
}

func TestCoordinator_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	c := NewCoordinator(nil)
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(idx int) {
			defer wg.Done()
			states := []string{StateIdle, StateListening, StateTranscribing, StateThinking, StateSynthesizing, StateSpeaking, StateError}
			state := states[idx%len(states)]
			txt := "query"
			_, _ = c.SetState(state, &txt, nil, nil)
		}(i)

		go func() {
			defer wg.Done()
			_ = c.GetState()
		}()
	}

	wg.Wait()
}

func TestVoiceStateData_SchemaCompliance(t *testing.T) {
	t.Parallel()

	schemaPath := filepath.Join("..", "..", "api", "schemas", "voice.state.json")
	schemaBytes, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Skipf("skipping schema file test if not in root: %v", err)
	}

	var schema map[string]any
	if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		t.Fatalf("failed to parse schema: %v", err)
	}

	transcript := "Hello"
	reply := "Hi"
	engine := "piper"
	data := events.VoiceStateData{
		State:      StateSpeaking,
		Transcript: &transcript,
		Reply:      &reply,
		TTSEngine:  &engine,
	}

	payload, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("failed to marshal data: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(payload, &parsed); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}

	if parsed["state"] != StateSpeaking || parsed["transcript"] != transcript || parsed["reply"] != reply || parsed["tts_engine"] != engine {
		t.Fatalf("mismatched parsed schema data: %+v", parsed)
	}
}
