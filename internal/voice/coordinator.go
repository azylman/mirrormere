package voice

import (
	"encoding/json"
	"errors"
	"sync"

	"github.com/azylman/mirrormere/internal/events"
)

// Standard voice lifecycle states per SPEC-006 §2.G and SPEC-011 §3.
const (
	StateIdle         = "idle"
	StateListening    = "listening"
	StateTranscribing = "transcribing"
	StateThinking     = "thinking"
	StateSynthesizing = "synthesizing"
	StateSpeaking     = "speaking"
	StateError        = "error"
)

// ErrInvalidState is returned when state is not one of the allowed lifecycle states.
var ErrInvalidState = errors.New("invalid voice state: must be one of 'idle', 'listening', 'transcribing', 'thinking', 'synthesizing', 'speaking', 'error'")

// ValidStates defines the set of valid voice state enum values.
var ValidStates = map[string]struct{}{
	StateIdle:         {},
	StateListening:    {},
	StateTranscribing: {},
	StateThinking:     {},
	StateSynthesizing: {},
	StateSpeaking:     {},
	StateError:        {},
}

// IsValidState reports whether s is a recognized voice lifecycle state.
func IsValidState(s string) bool {
	_, ok := ValidStates[s]
	return ok
}

// VoiceStateSink accepts updated voice state for initial connection hydration sync.
type VoiceStateSink interface {
	SetVoiceState(state *events.VoiceStateData)
}

// Coordinator tracks and manages authoritative in-memory voice pipeline state.
// It broadcasts updates across the SSE event hub and synchronizes initial connection hydration.
type Coordinator struct {
	mu         sync.RWMutex
	state      string
	transcript *string
	reply      *string
	ttsEngine  *string
	hub        *events.Hub
	sink       VoiceStateSink
}

// NewCoordinator constructs a VoiceCoordinator with default settings (idle state, nil fields).
func NewCoordinator(hub *events.Hub, sink ...VoiceStateSink) *Coordinator {
	var s VoiceStateSink
	if len(sink) > 0 {
		s = sink[0]
	}
	c := &Coordinator{
		state: StateIdle,
		hub:   hub,
		sink:  s,
	}
	if s != nil {
		s.SetVoiceState(&events.VoiceStateData{
			State: StateIdle,
		})
	}
	return c
}

// GetState returns the current voice pipeline state data.
func (c *Coordinator) GetState() events.VoiceStateData {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return events.VoiceStateData{
		State:      c.state,
		Transcript: c.transcript,
		Reply:      c.reply,
		TTSEngine:  c.ttsEngine,
	}
}

// SetState updates the voice pipeline state and broadcasts voice.state.
func (c *Coordinator) SetState(state string, transcript, reply, ttsEngine *string) (events.VoiceStateData, error) {
	if !IsValidState(state) {
		return events.VoiceStateData{}, ErrInvalidState
	}

	c.mu.Lock()
	c.state = state
	c.transcript = transcript
	c.reply = reply
	c.ttsEngine = ttsEngine
	data := events.VoiceStateData{
		State:      c.state,
		Transcript: c.transcript,
		Reply:      c.reply,
		TTSEngine:  c.ttsEngine,
	}
	c.mu.Unlock()

	c.publishState(data)
	return data, nil
}

// Reset resets the coordinator to default idle state and broadcasts voice.state.
func (c *Coordinator) Reset() events.VoiceStateData {
	data, err := c.SetState(StateIdle, nil, nil, nil)
	if err != nil {
		return events.VoiceStateData{State: StateIdle}
	}
	return data
}

func (c *Coordinator) publishState(data events.VoiceStateData) {
	if c.sink != nil {
		c.sink.SetVoiceState(&data)
	}

	if c.hub != nil {
		payload, err := json.Marshal(data)
		if err == nil {
			c.hub.Publish(events.EventVoiceState, payload)
		}
	}
}
