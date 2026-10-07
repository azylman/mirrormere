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

func TestNewCoordinator(t *testing.T) {
	t.Parallel()

	t.Run("without sink or hub", func(t *testing.T) {
		t.Parallel()
		c := NewCoordinator(nil)
		st := c.GetState()
		if st.State != StateIdle {
			t.Fatalf("expected state %q, got %q", StateIdle, st.State)
		}
		if st.Transcript != nil || st.Reply != nil || st.TTSEngine != nil {
			t.Fatalf("expected nil fields, got %+v", st)
		}
	})

	t.Run("with sink sets initial state", func(t *testing.T) {
		t.Parallel()
		sink := &mockSink{}
		c := NewCoordinator(nil, sink)
		st := c.GetState()
		if st.State != StateIdle {
			t.Fatalf("expected state %q, got %q", StateIdle, st.State)
		}
		last := sink.LastState()
		if last == nil || last.State != StateIdle {
			t.Fatalf("expected sink initial state %q, got %+v", StateIdle, last)
		}
	})
}

func TestCoordinator_SetState_Valid(t *testing.T) {
	t.Parallel()

	validStates := []string{
		StateIdle,
		StateListening,
		StateTranscribing,
		StateThinking,
		StateSynthesizing,
		StateSpeaking,
		StateError,
	}

	for _, s := range validStates {
		t.Run(s, func(t *testing.T) {
			t.Parallel()
			sink := &mockSink{}
			hub := events.NewHub(events.HubConfig{}, nil, nil)
			defer hub.Close()

			subCtx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sub, unsub := hub.Subscribe(subCtx)
			defer unsub()

			c := NewCoordinator(hub, sink)

			transcript := "Turn on kitchen lights"
			reply := "Turning on lights"
			engine := "kokoro"

			st, err := c.SetState(s, &transcript, &reply, &engine)
			if err != nil {
				t.Fatalf("unexpected error setting valid state %q: %v", s, err)
			}
			if st.State != s {
				t.Fatalf("expected state %q, got %q", s, st.State)
			}
			if *st.Transcript != transcript || *st.Reply != reply || *st.TTSEngine != engine {
				t.Fatalf("expected values matched, got %+v", st)
			}

			// Verify sink
			last := sink.LastState()
			if last == nil || last.State != s {
				t.Fatalf("expected sink state %q, got %+v", s, last)
			}

			// Verify SSE broadcast
			select {
			case evt := <-sub:
				if evt.Type != events.EventVoiceState {
					t.Fatalf("expected event %q, got %q", events.EventVoiceState, evt.Type)
				}
				var vs events.VoiceStateData
				if err := json.Unmarshal(evt.Data, &vs); err != nil {
					t.Fatalf("failed to unmarshal SSE event: %v", err)
				}
				if vs.State != s || *vs.Transcript != transcript || *vs.Reply != reply || *vs.TTSEngine != engine {
					t.Fatalf("mismatched SSE event data: %+v", vs)
				}
			default:
				t.Fatal("expected SSE event to be published, none received")
			}
		})
	}
}

func TestCoordinator_SetState_Invalid(t *testing.T) {
	t.Parallel()

	invalidStates := []string{
		"",
		"IDLE",
		"pending",
		"waiting",
		"unknown",
		" listening",
	}

	for _, invalid := range invalidStates {
		t.Run(invalid, func(t *testing.T) {
			t.Parallel()
			c := NewCoordinator(nil)
			_, err := c.SetState(invalid, nil, nil, nil)
			if err == nil {
				t.Fatalf("expected error for invalid state %q, got nil", invalid)
			}
			if !errors.Is(err, ErrInvalidState) {
				t.Fatalf("expected ErrInvalidState, got %v", err)
			}
		})
	}
}

func TestCoordinator_Reset(t *testing.T) {
	t.Parallel()

	sink := &mockSink{}
	c := NewCoordinator(nil, sink)

	txt := "something"
	_, err := c.SetState(StateSpeaking, &txt, &txt, &txt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	reset := c.Reset()
	if reset.State != StateIdle || reset.Transcript != nil || reset.Reply != nil || reset.TTSEngine != nil {
		t.Fatalf("expected reset to idle with nil fields, got %+v", reset)
	}

	st := c.GetState()
	if st.State != StateIdle || st.Transcript != nil || st.Reply != nil || st.TTSEngine != nil {
		t.Fatalf("expected coordinator state idle with nil fields, got %+v", st)
	}
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

func TestIsValidState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		state string
		valid bool
	}{
		{StateIdle, true},
		{StateListening, true},
		{StateTranscribing, true},
		{StateThinking, true},
		{StateSynthesizing, true},
		{StateSpeaking, true},
		{StateError, true},
		{"", false},
		{"running", false},
		{"LISTENING", false},
	}

	for _, tc := range tests {
		got := IsValidState(tc.state)
		if got != tc.valid {
			t.Errorf("IsValidState(%q) = %v, expected %v", tc.state, got, tc.valid)
		}
	}
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

	if parsed["state"] != StateSpeaking {
		t.Fatalf("expected state %q, got %v", StateSpeaking, parsed["state"])
	}
	if parsed["transcript"] != transcript {
		t.Fatalf("expected transcript %q, got %v", transcript, parsed["transcript"])
	}
	if parsed["reply"] != reply {
		t.Fatalf("expected reply %q, got %v", reply, parsed["reply"])
	}
	if parsed["tts_engine"] != engine {
		t.Fatalf("expected tts_engine %q, got %v", engine, parsed["tts_engine"])
	}
}

func TestCoordinator_StatusAndFullState(t *testing.T) {
	t.Parallel()
	sink := &mockSink{}
	hub := events.NewHub(events.HubConfig{}, nil, nil)
	defer hub.Close()

	subCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sub, unsub := hub.Subscribe(subCtx)
	defer unsub()

	c := NewCoordinator(hub, sink)

	status := "⚡ Running tool..."
	transcript := "Turn on lights"
	st, err := c.SetFullState(StateThinking, &transcript, nil, nil, &status)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if st.Status == nil || *st.Status != status {
		t.Fatalf("expected status %q, got %v", status, st.Status)
	}

	// Verify SSE received status
	select {
	case evt := <-sub:
		var vs events.VoiceStateData
		if err := json.Unmarshal(evt.Data, &vs); err != nil {
			t.Fatalf("failed to unmarshal SSE event: %v", err)
		}
		if vs.Status == nil || *vs.Status != status {
			t.Fatalf("expected SSE status %q, got %v", status, vs.Status)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for SSE event")
	}

	// Verify SetStatus
	newStatus := "⚡ Querying Home Assistant..."
	st2 := c.SetStatus(&newStatus)
	if st2.Status == nil || *st2.Status != newStatus {
		t.Fatalf("expected status %q, got %v", newStatus, st2.Status)
	}
	if st2.State != StateThinking {
		t.Fatalf("expected state unchanged, got %q", st2.State)
	}

	// Reset while in StateThinking clears status and returns to idle
	resetState := c.Reset()
	if resetState.State != StateIdle || resetState.Status != nil {
		t.Fatalf("expected idle with nil status, got %+v", resetState)
	}
}

func TestCoordinator_IdleTransitionFromActiveStates(t *testing.T) {
	t.Parallel()

	activeInputStates := []string{StateListening, StateTranscribing, StateThinking}
	for _, activeState := range activeInputStates {
		t.Run("allows idle when in "+activeState, func(t *testing.T) {
			t.Parallel()
			c := NewCoordinator(nil)
			q := "query"
			_, err := c.SetState(activeState, &q, nil, nil)
			if err != nil {
				t.Fatalf("unexpected error setting %s: %v", activeState, err)
			}

			// Try SetState(StateIdle)
			st, err := c.SetState(StateIdle, nil, nil, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if st.State != StateIdle {
				t.Fatalf("expected SetState(StateIdle) to transition to idle from %s, got %q", activeState, st.State)
			}
			if c.GetState().State != StateIdle {
				t.Fatalf("expected GetState() to be %q, got %q", StateIdle, c.GetState().State)
			}

			// Reset back to activeState
			_, _ = c.SetState(activeState, &q, nil, nil)

			// Try SetFullState(StateIdle)
			stFull, err := c.SetFullState(StateIdle, nil, nil, nil, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if stFull.State != StateIdle {
				t.Fatalf("expected SetFullState(StateIdle) to transition to idle from %s, got %q", activeState, stFull.State)
			}

			// Reset back to activeState
			_, _ = c.SetState(activeState, &q, nil, nil)

			// Try Reset()
			stReset := c.Reset()
			if stReset.State != StateIdle {
				t.Fatalf("expected Reset() to transition to idle from %s, got %q", activeState, stReset.State)
			}

			// Reset back to activeState
			_, _ = c.SetState(activeState, &q, nil, nil)

			// Try Transition(StateIdle)
			stTrans := c.Transition(StateIdle, nil, nil, nil, nil)
			if stTrans.State != StateIdle {
				t.Fatalf("expected Transition(StateIdle) to transition to idle from %s, got %q", activeState, stTrans.State)
			}
		})
	}

	resettableStates := []string{StateSpeaking, StateSynthesizing, StateError}
	for _, stName := range resettableStates {
		t.Run("allows idle when in "+stName, func(t *testing.T) {
			t.Parallel()
			c := NewCoordinator(nil)
			txt := "text"
			_, err := c.SetState(stName, &txt, &txt, nil)
			if err != nil {
				t.Fatalf("unexpected error setting %s: %v", stName, err)
			}

			st, err := c.SetState(StateIdle, nil, nil, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if st.State != StateIdle {
				t.Fatalf("expected state to transition to idle from %s, got %q", stName, st.State)
			}
		})
	}
}

func TestCoordinator_SpeakingSafetyWatchdog(t *testing.T) {
	t.Parallel()

	t.Run("auto-resets to idle after speaking timeout", func(t *testing.T) {
		t.Parallel()
		c := NewCoordinator(nil)
		c.SetSpeakingTimeout(50 * time.Millisecond)

		txt := "hello"
		rep := "hi"
		c.Transition(StateSpeaking, &txt, &rep, nil, nil)
		if c.GetState().State != StateSpeaking {
			t.Fatalf("expected state speaking, got %s", c.GetState().State)
		}

		// Wait for watchdog timer to fire
		time.Sleep(100 * time.Millisecond)

		if c.GetState().State != StateIdle {
			t.Fatalf("expected watchdog to auto-reset to idle, got %s", c.GetState().State)
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
		c.SetSpeakingTimeout(50 * time.Millisecond)

		txt := "hello"
		rep := "hi"
		c.Transition(StateSpeaking, &txt, &rep, nil, nil)
		if c.GetState().State != StateSpeaking {
			t.Fatalf("expected state speaking, got %s", c.GetState().State)
		}

		// Transition out of speaking
		c.Transition(StateError, nil, nil, nil, nil)
		if c.GetState().State != StateError {
			t.Fatalf("expected state error, got %s", c.GetState().State)
		}

		time.Sleep(100 * time.Millisecond)

		// Watchdog must not have reset StateError back to StateIdle
		if c.GetState().State != StateError {
			t.Fatalf("expected state to remain error, got %s", c.GetState().State)
		}
	})

	t.Run("Reset cancels watchdog timer", func(t *testing.T) {
		t.Parallel()
		c := NewCoordinator(nil)
		c.SetSpeakingTimeout(50 * time.Millisecond)

		txt := "hello"
		rep := "hi"
		c.Transition(StateSpeaking, &txt, &rep, nil, nil)
		c.Reset()
		if c.GetState().State != StateIdle {
			t.Fatalf("expected state idle, got %s", c.GetState().State)
		}

		// Transition to listening before watchdog would fire
		c.Transition(StateListening, nil, nil, nil, nil)

		time.Sleep(100 * time.Millisecond)

		// Late idle and watchdog timer must not have reset StateListening
		if c.GetState().State != StateListening {
			t.Fatalf("expected state to remain listening, got %s", c.GetState().State)
		}
	})
}

func TestCoordinator_Stop(t *testing.T) {
	t.Parallel()
	c := NewCoordinator(nil)
	c.SetSpeakingTimeout(50 * time.Millisecond)

	txt := "hello"
	rep := "hi"
	c.Transition(StateSpeaking, &txt, &rep, nil, nil)
	c.Stop()

	time.Sleep(100 * time.Millisecond)

	// Since Stop cancelled the timer, it should still be in speaking
	if c.GetState().State != StateSpeaking {
		t.Fatalf("expected state speaking after Stop(), got %s", c.GetState().State)
	}
	c.Reset()
}
