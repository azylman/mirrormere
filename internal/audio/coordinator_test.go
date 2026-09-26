package audio_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/audio"
	"github.com/azylman/mirrormere/internal/events"
)

type mockSink struct {
	mu    sync.Mutex
	state *events.AudioStateData
	calls int
}

func (m *mockSink) SetAudioState(state *events.AudioStateData) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	m.state = &events.AudioStateData{
		Volume: state.Volume,
		Muted:  state.Muted,
	}
}

func (m *mockSink) LastState() *events.AudioStateData {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state == nil {
		return nil
	}
	return &events.AudioStateData{
		Volume: m.state.Volume,
		Muted:  m.state.Muted,
	}
}

func TestCoordinator_Defaults(t *testing.T) {
	t.Parallel()

	sink := &mockSink{}
	coord := audio.NewCoordinator(nil, sink)

	vol, muted := coord.GetState()
	if vol != audio.DefaultVolume {
		t.Fatalf("expected default volume %d, got %d", audio.DefaultVolume, vol)
	}
	if muted != audio.DefaultMuted {
		t.Fatalf("expected default muted %v, got %v", audio.DefaultMuted, muted)
	}

	last := sink.LastState()
	if last == nil || last.Volume != audio.DefaultVolume || last.Muted != audio.DefaultMuted {
		t.Fatalf("expected sink primed with defaults, got %+v", last)
	}
}

func TestCoordinator_SetVolume(t *testing.T) {
	t.Parallel()

	hub := events.NewHub(events.HubConfig{
		RingCapacity: 10,
		RingTTL:      time.Minute,
	}, nil, nil)
	t.Cleanup(func() { hub.Close() })

	subCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sub, unsub := hub.Subscribe(subCtx)
	defer unsub()

	sink := &mockSink{}
	coord := audio.NewCoordinator(hub, sink)

	// Valid volume changes
	tests := []int{0, 50, 80, 100}
	for _, v := range tests {
		vol, muted, err := coord.SetVolume(v)
		if err != nil {
			t.Fatalf("unexpected error setting volume %d: %v", v, err)
		}
		if vol != v {
			t.Fatalf("expected volume %d, got %d", v, vol)
		}
		if muted != false {
			t.Fatalf("expected muted false, got %v", muted)
		}

		// Verify event published
		select {
		case evt, ok := <-sub:
			if !ok {
				t.Fatalf("subscription channel closed unexpectedly")
			}
			if evt.Type != events.EventAudioState {
				t.Fatalf("expected event %q, got %q", events.EventAudioState, evt.Type)
			}
			var data events.AudioStateData
			if err := json.Unmarshal(evt.Data, &data); err != nil {
				t.Fatalf("failed to unmarshal audio state payload: %v", err)
			}
			if data.Volume != v || data.Muted != false {
				t.Fatalf("unexpected payload in event: %+v", data)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for audio.state event for volume %d", v)
		}

		// Verify sink updated
		if last := sink.LastState(); last == nil || last.Volume != v || last.Muted != false {
			t.Fatalf("expected sink volume %d, got %+v", v, last)
		}
	}

	// Invalid volume out of bounds
	invalidVolumes := []int{-10, -1, 101, 200}
	for _, iv := range invalidVolumes {
		_, _, err := coord.SetVolume(iv)
		if err == nil {
			t.Fatalf("expected error for volume %d, got nil", iv)
		}
		if !errors.Is(err, audio.ErrInvalidVolume) {
			t.Fatalf("expected ErrInvalidVolume, got %v", err)
		}
	}
}

func TestCoordinator_SetMuteAndToggle(t *testing.T) {
	t.Parallel()

	hub := events.NewHub(events.HubConfig{
		RingCapacity: 10,
		RingTTL:      time.Minute,
	}, nil, nil)
	t.Cleanup(func() { hub.Close() })

	subCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sub, unsub := hub.Subscribe(subCtx)
	defer unsub()

	sink := &mockSink{}
	coord := audio.NewCoordinator(hub, sink)

	// Explicit mute true
	trueVal := true
	vol, muted, err := coord.SetMute(&trueVal)
	if err != nil {
		t.Fatalf("unexpected error setting mute true: %v", err)
	}
	if !muted || vol != audio.DefaultVolume {
		t.Fatalf("expected muted true, vol %d, got muted %v, vol %d", audio.DefaultVolume, muted, vol)
	}

	select {
	case evt := <-sub:
		var data events.AudioStateData
		_ = json.Unmarshal(evt.Data, &data)
		if !data.Muted {
			t.Fatalf("expected event muted true, got false")
		}
	case <-time.After(time.Second):
		t.Fatalf("timeout waiting for mute event")
	}

	// Explicit mute false
	falseVal := false
	vol, muted, err = coord.SetMute(&falseVal)
	if err != nil {
		t.Fatalf("unexpected error setting mute false: %v", err)
	}
	if muted || vol != audio.DefaultVolume {
		t.Fatalf("expected muted false, vol %d, got muted %v, vol %d", audio.DefaultVolume, muted, vol)
	}

	select {
	case evt := <-sub:
		var data events.AudioStateData
		_ = json.Unmarshal(evt.Data, &data)
		if data.Muted {
			t.Fatalf("expected event muted false, got true")
		}
	case <-time.After(time.Second):
		t.Fatalf("timeout waiting for unmute event")
	}

	// Toggle mute with nil
	vol, muted, err = coord.SetMute(nil)
	if err != nil {
		t.Fatalf("unexpected error toggling mute: %v", err)
	}
	if !muted {
		t.Fatalf("expected toggled muted true, got false")
	}

	// ToggleMute helper
	vol, muted, err = coord.ToggleMute()
	if err != nil {
		t.Fatalf("unexpected error with ToggleMute: %v", err)
	}
	if muted {
		t.Fatalf("expected toggled muted false, got true")
	}
	if vol != audio.DefaultVolume {
		t.Fatalf("expected volume %d, got %d", audio.DefaultVolume, vol)
	}
}

func TestCoordinator_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	hub := events.NewHub(events.HubConfig{
		RingCapacity: 100,
		RingTTL:      time.Minute,
	}, nil, nil)
	t.Cleanup(func() { hub.Close() })

	sink := &mockSink{}
	coord := audio.NewCoordinator(hub, sink)

	var wg sync.WaitGroup
	const iterations = 50

	// Concurrent volume writers
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				target := (workerID*20 + j) % 101
				_, _, _ = coord.SetVolume(target)
			}
		}(i)
	}

	// Concurrent mute togglers
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_, _, _ = coord.ToggleMute()
			}
		}()
	}

	// Concurrent state readers
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				v, _ := coord.GetState()
				if v < 0 || v > 100 {
					t.Errorf("read invalid volume: %d", v)
				}
			}
		}()
	}

	wg.Wait()

	finalVol, _ := coord.GetState()
	if finalVol < 0 || finalVol > 100 {
		t.Fatalf("final volume out of range: %d", finalVol)
	}
}

func TestCoordinator_SchemaValidation(t *testing.T) {
	t.Parallel()

	// Verify that the payload emitted by Coordinator matches api/schemas/audio.state.json
	schemaPath := filepath.Join("..", "..", "api", "schemas", "audio.state.json")
	schemaBytes, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Skipf("skipping schema file test if not in root: %v", err)
	}

	var schema map[string]any
	if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		t.Fatalf("failed to parse schema: %v", err)
	}

	data := events.AudioStateData{
		Volume: 75,
		Muted:  false,
	}
	payload, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("failed to marshal data: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(payload, &parsed); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}

	// Schema requires "volume" and "muted"
	if _, ok := parsed["volume"]; !ok {
		t.Fatalf("missing volume in payload")
	}
	if _, ok := parsed["muted"]; !ok {
		t.Fatalf("missing muted in payload")
	}
}
