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
}

func (m *mockSink) SetAudioState(state *events.AudioStateData) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = &events.AudioStateData{Volume: state.Volume, Muted: state.Muted}
}

func (m *mockSink) LastState() *events.AudioStateData {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state == nil {
		return nil
	}
	return &events.AudioStateData{Volume: m.state.Volume, Muted: m.state.Muted}
}

func assertAudioEvent(t *testing.T, sub <-chan *events.Event, wantVol int, wantMuted bool) {
	t.Helper()
	select {
	case evt, ok := <-sub:
		if !ok {
			t.Fatal("subscription channel closed unexpectedly")
		}
		if evt.Type != events.EventAudioState {
			t.Fatalf("expected event %q, got %q", events.EventAudioState, evt.Type)
		}
		var data events.AudioStateData
		if err := json.Unmarshal(evt.Data, &data); err != nil {
			t.Fatalf("failed to unmarshal audio state payload: %v", err)
		}
		if data.Volume != wantVol || data.Muted != wantMuted {
			t.Fatalf("unexpected payload in event: got vol %d, muted %v; want vol %d, muted %v", data.Volume, data.Muted, wantVol, wantMuted)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for audio.state event")
	}
}

func TestCoordinator_VolumeAndMute(t *testing.T) {
	t.Parallel()

	sink := &mockSink{}
	if v, m := audio.NewCoordinator(nil, sink).GetState(); v != audio.DefaultVolume || m != audio.DefaultMuted {
		t.Fatalf("defaults without hub: got vol %d, muted %v", v, m)
	}
	if last := sink.LastState(); last == nil || last.Volume != audio.DefaultVolume || last.Muted != audio.DefaultMuted {
		t.Fatalf("sink defaults: got %+v", last)
	}

	hub := events.NewHub(events.HubConfig{RingCapacity: 10, RingTTL: time.Minute}, nil, nil)
	t.Cleanup(func() { hub.Close() })
	subCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sub, unsub := hub.Subscribe(subCtx)
	defer unsub()

	coord := audio.NewCoordinator(hub, sink)

	for _, v := range []int{0, 50, 80, 100} {
		vol, muted, err := coord.SetVolume(v)
		if err != nil || vol != v || muted {
			t.Fatalf("SetVolume(%d): got (%d, %v, %v)", v, vol, muted, err)
		}
		assertAudioEvent(t, sub, v, false)
		if last := sink.LastState(); last == nil || last.Volume != v || last.Muted {
			t.Fatalf("sink mismatch: %+v", last)
		}
	}

	for _, iv := range []int{-10, -1, 101, 200} {
		if _, _, err := coord.SetVolume(iv); !errors.Is(err, audio.ErrInvalidVolume) {
			t.Fatalf("SetVolume(%d) error = %v, want ErrInvalidVolume", iv, err)
		}
	}

	trueVal, falseVal := true, false
	muteOps := []struct {
		op        func() (int, bool, error)
		wantMuted bool
	}{
		{func() (int, bool, error) { return coord.SetMute(&trueVal) }, true},
		{func() (int, bool, error) { return coord.SetMute(&falseVal) }, false},
		{func() (int, bool, error) { return coord.SetMute(nil) }, true},
		{coord.ToggleMute, false},
	}
	for i, tc := range muteOps {
		vol, muted, err := tc.op()
		if err != nil || vol != 100 || muted != tc.wantMuted {
			t.Fatalf("mute op %d: got (%d, %v, %v), want vol 100, muted %v", i, vol, muted, err, tc.wantMuted)
		}
		assertAudioEvent(t, sub, 100, tc.wantMuted)
		if last := sink.LastState(); last == nil || last.Volume != 100 || last.Muted != tc.wantMuted {
			t.Fatalf("sink mismatch on mute op %d: %+v", i, last)
		}
	}
}

func TestCoordinator_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	hub := events.NewHub(events.HubConfig{RingCapacity: 100, RingTTL: time.Minute}, nil, nil)
	t.Cleanup(func() { hub.Close() })
	sink := &mockSink{}
	coord := audio.NewCoordinator(hub, sink)

	var wg sync.WaitGroup
	const iterations = 50

	for i := 0; i < 4; i++ {
		wg.Add(3)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_, _, _ = coord.SetVolume((workerID*20 + j) % 101)
			}
		}(i)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_, _, _ = coord.ToggleMute()
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				if v, _ := coord.GetState(); v < 0 || v > 100 {
					t.Errorf("read invalid volume: %d", v)
				}
			}
		}()
	}
	wg.Wait()

	if finalVol, _ := coord.GetState(); finalVol < 0 || finalVol > 100 {
		t.Fatalf("final volume out of range: %d", finalVol)
	}
}

func TestCoordinator_SchemaValidation(t *testing.T) {
	t.Parallel()

	schemaBytes, err := os.ReadFile(filepath.Join("..", "..", "api", "schemas", "audio.state.json"))
	if err != nil {
		t.Skipf("skipping schema file test if not in root: %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		t.Fatalf("failed to parse schema: %v", err)
	}

	payload, err := json.Marshal(events.AudioStateData{Volume: 75, Muted: false})
	if err != nil {
		t.Fatalf("failed to marshal data: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(payload, &parsed); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}
	if _, ok := parsed["volume"]; !ok {
		t.Fatalf("missing volume in payload")
	}
	if _, ok := parsed["muted"]; !ok {
		t.Fatalf("missing muted in payload")
	}
}
