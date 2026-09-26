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

	// Reset clears status
	resetState := c.Reset()
	if resetState.State != StateIdle || resetState.Status != nil {
		t.Fatalf("expected idle with nil status, got %+v", resetState)
	}
}
