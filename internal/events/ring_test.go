package events

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestRingBuffer_DefaultsAndLimits(t *testing.T) {
	t.Parallel()

	r := NewRingBuffer(0, 0, nil)
	if r.capacity != DefaultRingCapacity {
		t.Errorf("expected default capacity %d, got %d", DefaultRingCapacity, r.capacity)
	}
	if r.ttl != DefaultRingTTL {
		t.Errorf("expected default TTL %v, got %v", DefaultRingTTL, r.ttl)
	}
	if r.nowFunc == nil {
		t.Fatal("expected nowFunc to be initialized")
	}
}

func TestRingBuffer_AddAndCount(t *testing.T) {
	t.Parallel()

	currTime := time.Date(2026, 9, 25, 20, 0, 0, 0, time.UTC)
	clock := func() time.Time { return currTime }

	r := NewRingBuffer(3, 5*time.Minute, clock)
	if r.Count() != 0 {
		t.Fatalf("expected count 0, got %d", r.Count())
	}

	evt1 := &Event{ID: "evt_1", Type: "test"}
	r.Add(evt1)
	if r.Count() != 1 {
		t.Errorf("expected count 1, got %d", r.Count())
	}
	if evt1.Timestamp != currTime {
		t.Errorf("expected event timestamp %v, got %v", currTime, evt1.Timestamp)
	}

	r.Add(&Event{ID: "evt_2", Type: "test"})
	r.Add(&Event{ID: "evt_3", Type: "test"})
	if r.Count() != 3 {
		t.Errorf("expected count 3, got %d", r.Count())
	}

	// Add 4th event to wrap around
	r.Add(&Event{ID: "evt_4", Type: "test"})
	if r.Count() != 3 {
		t.Errorf("expected count capped at 3, got %d", r.Count())
	}
}

func TestRingBuffer_ReplaySince(t *testing.T) {
	t.Parallel()

	currTime := time.Date(2026, 9, 25, 20, 0, 0, 0, time.UTC)
	clock := func() time.Time { return currTime }

	r := NewRingBuffer(5, 5*time.Minute, clock)

	// 1. Empty buffer
	if evts, ok := r.ReplaySince("evt_1"); ok || evts != nil {
		t.Errorf("expected (nil, false) on empty buffer, got (%v, %v)", evts, ok)
	}

	// 2. Empty ID or oversized ID
	if evts, ok := r.ReplaySince(""); ok || evts != nil {
		t.Errorf("expected (nil, false) on empty ID, got (%v, %v)", evts, ok)
	}
	longID := strings.Repeat("x", MaxEventIDLength+1)
	if evts, ok := r.ReplaySince(longID); ok || evts != nil {
		t.Errorf("expected (nil, false) on oversized ID, got (%v, %v)", evts, ok)
	}

	// Populate buffer with 4 events
	for i := 1; i <= 4; i++ {
		r.Add(&Event{
			ID:        fmt.Sprintf("evt_%d", i),
			Type:      "test",
			Timestamp: currTime,
		})
	}

	// 3. ID not found
	if evts, ok := r.ReplaySince("evt_999"); ok || evts != nil {
		t.Errorf("expected (nil, false) on unknown ID, got (%v, %v)", evts, ok)
	}

	// 4. Replay from latest event (0 missed events)
	evts, ok := r.ReplaySince("evt_4")
	if !ok {
		t.Fatal("expected replay hit for evt_4")
	}
	if len(evts) != 0 {
		t.Errorf("expected 0 events, got %d", len(evts))
	}

	// 5. Replay from middle event evt_2 (should return evt_3 and evt_4)
	evts, ok = r.ReplaySince("evt_2")
	if !ok {
		t.Fatal("expected replay hit for evt_2")
	}
	if len(evts) != 2 {
		t.Fatalf("expected 2 events, got %d", len(evts))
	}
	if evts[0].ID != "evt_3" || evts[1].ID != "evt_4" {
		t.Errorf("unexpected replay sequence: %v, %v", evts[0].ID, evts[1].ID)
	}

	// 6. TTL Expiration check
	// Advance clock by 5m1s
	currTime = currTime.Add(5*time.Minute + time.Second)
	evts, ok = r.ReplaySince("evt_2")
	if ok || evts != nil {
		t.Errorf("expected (nil, false) for expired event, got (%v, %v)", evts, ok)
	}

	// 7. Clock skew (negative duration)
	currTime = time.Date(2026, 9, 25, 19, 0, 0, 0, time.UTC)
	evts, ok = r.ReplaySince("evt_2")
	if ok || evts != nil {
		t.Errorf("expected (nil, false) for negative duration clock jump, got (%v, %v)", evts, ok)
	}
}

func TestRingBuffer_WrapAroundReplay(t *testing.T) {
	t.Parallel()

	currTime := time.Date(2026, 9, 25, 20, 0, 0, 0, time.UTC)
	clock := func() time.Time { return currTime }

	// Capacity 3
	r := NewRingBuffer(3, 5*time.Minute, clock)

	r.Add(&Event{ID: "evt_1", Type: "test"})
	r.Add(&Event{ID: "evt_2", Type: "test"})
	r.Add(&Event{ID: "evt_3", Type: "test"})
	// Overwrites evt_1
	r.Add(&Event{ID: "evt_4", Type: "test"})

	// evt_1 was overwritten and should not be found
	if evts, ok := r.ReplaySince("evt_1"); ok || evts != nil {
		t.Errorf("expected overwritten evt_1 to miss replay, got (%v, %v)", evts, ok)
	}

	// Replay from evt_2 should yield evt_3 and evt_4
	evts, ok := r.ReplaySince("evt_2")
	if !ok {
		t.Fatal("expected replay hit for evt_2")
	}
	if len(evts) != 2 || evts[0].ID != "evt_3" || evts[1].ID != "evt_4" {
		t.Errorf("unexpected replay results: %v", evts)
	}
}
