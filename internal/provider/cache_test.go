package provider_test

import (
	"errors"
	"fmt"
	"maps"
	"sync"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/provider"
)

// TestSWRCache_LifecycleAndStateTransitions drives one widget through the SWR
// state machine; each step runs against the state the previous steps left.
func TestSWRCache_LifecycleAndStateTransitions(t *testing.T) {
	t.Parallel()
	const wID = "sensor-weather"
	t0 := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	errCold, errTrans := errors.New("timeout on cold boot"), errors.New("502 bad gateway")
	steps := []struct {
		name      string
		op        string
		data      map[string]any
		err       error
		at        time.Duration
		wantState string
		wantTS    time.Duration
		wantTrans bool
		wantFails int
		wantData  map[string]any
	}{
		{"cold failure serves an empty map", "failure", nil, errCold, 0, provider.StateError, 0, true, 1, map[string]any{}},
		{"repeat cold failure", "failure", nil, errCold, 5 * time.Second, provider.StateError, 5 * time.Second, false, 2, map[string]any{}},
		{"first success", "success", map[string]any{"temp": 72.5}, nil, 30 * time.Second, provider.StateHealthy, 30 * time.Second, true, 0, map[string]any{"temp": 72.5}},
		{"repeat success", "success", map[string]any{"temp": 73.0}, nil, 90 * time.Second, provider.StateHealthy, 90 * time.Second, false, 0, map[string]any{"temp": 73.0}},
		{"failure keeps last good", "failure", nil, errTrans, 150 * time.Second, provider.StateDegraded, 90 * time.Second, true, 1, map[string]any{"temp": 73.0}},
		{"repeat degraded failure", "failure", nil, errTrans, 160 * time.Second, provider.StateDegraded, 90 * time.Second, false, 2, map[string]any{"temp": 73.0}},
		{"recovery", "success", map[string]any{"temp": 71.8}, nil, 190 * time.Second, provider.StateHealthy, 190 * time.Second, true, 0, map[string]any{"temp": 71.8}},
		{"push while healthy", "push", map[string]any{"temp": 70.0}, nil, 200 * time.Second, provider.StateHealthy, 200 * time.Second, false, 0, map[string]any{"temp": 70.0}},
	}

	cache := provider.NewSWRCache()
	if _, ok := cache.Get(wID); ok || cache.GetConsecutiveFailures(wID) != 0 || cache.GetStatusMap() != nil {
		t.Fatal("expected empty initial cache")
	}
	for _, st := range steps {
		var got provider.WidgetPayload
		var trans bool
		switch st.op {
		case "failure":
			got, trans = cache.RecordFailure(wID, st.err, t0.Add(st.at))
		case "success":
			got, trans = cache.RecordSuccess(wID, st.data, t0.Add(st.at))
		default:
			got, trans = cache.RecordPush(wID, st.data, t0.Add(st.at))
		}
		if wantTS := t0.Add(st.wantTS).Format(time.RFC3339); got.State != st.wantState || got.Timestamp != wantTS || trans != st.wantTrans {
			t.Fatalf("%s: state=%q ts=%q trans=%v; want %q %q %v", st.name, got.State, got.Timestamp, trans, st.wantState, wantTS, st.wantTrans)
		}
		if fails := cache.GetConsecutiveFailures(wID); fails != st.wantFails {
			t.Fatalf("%s: consecutive failures = %d, want %d", st.name, fails, st.wantFails)
		}
		if data, ok := got.Data.(map[string]any); !ok || !maps.Equal(data, st.wantData) {
			t.Fatalf("%s: data = %v, want %v", st.name, got.Data, st.wantData)
		}
	}
	if got := cache.GetStatusMap(); len(got) != 1 || got[wID] != provider.StateHealthy {
		t.Fatalf("status map = %v, want only %s healthy", got, wID)
	}
	cache.Purge(wID)
	if _, ok := cache.Get(wID); ok || cache.GetStatusMap() != nil {
		t.Fatal("expected cache eviction after Purge")
	}
}

func TestSWRCache_ConcurrentAccessSafety(t *testing.T) {
	t.Parallel()
	cache := provider.NewSWRCache()
	now := time.Now()
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func(id string, idx int) {
			defer wg.Done()
			for j := range 100 {
				switch (idx + j) % 3 {
				case 0:
					cache.RecordSuccess(id, map[string]any{"iter": j}, now)
				case 1:
					cache.RecordFailure(id, errors.New("transient"), now)
				default:
					_, _ = cache.Get(id)
					_ = cache.GetStatusMap()
				}
			}
		}(fmt.Sprintf("widget-%d", i%5), i)
	}
	wg.Wait()
}
