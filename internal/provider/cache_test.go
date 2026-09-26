package provider_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/provider"
)

func TestSWRCache_LifecycleAndStateTransitions(t *testing.T) {
	t.Parallel()

	cache := provider.NewSWRCache()
	widgetID := "sensor-weather"
	t0 := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)

	// 1. Initial State: non-existent widget
	if _, ok := cache.Get(widgetID); ok {
		t.Fatal("expected empty cache for new widgetID")
	}
	if cache.GetConsecutiveFailures(widgetID) != 0 {
		t.Fatalf("expected 0 failures, got %d", cache.GetConsecutiveFailures(widgetID))
	}
	if cache.GetLastError(widgetID) != nil {
		t.Fatal("expected nil last error")
	}
	if _, ok := cache.GetLastSuccess(widgetID); ok {
		t.Fatal("expected no last success time")
	}

	// 2. Cold Boot Failure (no LKG exists)
	errColdBoot := errors.New("connection timeout on cold boot")
	p1, transitioned := cache.RecordFailure(widgetID, errColdBoot, t0)
	if !transitioned {
		t.Fatal("expected transitioned=true on first cold-boot failure")
	}
	if p1.State != provider.StateError {
		t.Fatalf("expected state %q, got %q", provider.StateError, p1.State)
	}
	if p1.WidgetID != widgetID {
		t.Fatalf("expected widget ID %q, got %q", widgetID, p1.WidgetID)
	}
	if p1.Timestamp != t0.Format(time.RFC3339) {
		t.Fatalf("expected timestamp %q, got %q", t0.Format(time.RFC3339), p1.Timestamp)
	}
	if dataMap, ok := p1.Data.(map[string]any); !ok || len(dataMap) != 0 {
		t.Fatalf("expected empty fallback map, got %v", p1.Data)
	}
	if cache.GetConsecutiveFailures(widgetID) != 1 {
		t.Fatalf("expected 1 failure, got %d", cache.GetConsecutiveFailures(widgetID))
	}
	if !errors.Is(cache.GetLastError(widgetID), errColdBoot) {
		t.Fatalf("expected last error %v, got %v", errColdBoot, cache.GetLastError(widgetID))
	}

	// 3. Second Consecutive Failure without LKG (should suppress state transition)
	t1 := t0.Add(5 * time.Second)
	p2, transitioned := cache.RecordFailure(widgetID, errColdBoot, t1)
	if transitioned {
		t.Fatal("expected transitioned=false on consecutive error failure")
	}
	if p2.State != provider.StateError {
		t.Fatalf("expected state %q, got %q", provider.StateError, p2.State)
	}
	if cache.GetConsecutiveFailures(widgetID) != 2 {
		t.Fatalf("expected 2 failures, got %d", cache.GetConsecutiveFailures(widgetID))
	}

	// 4. Initial Success (Transition from error -> healthy)
	t2 := t0.Add(30 * time.Second)
	freshData := map[string]any{"temp": 72.5, "conditions": "sunny"}
	p3, transitioned := cache.RecordSuccess(widgetID, freshData, t2)
	if !transitioned {
		t.Fatal("expected transitioned=true on error -> healthy")
	}
	if p3.State != provider.StateHealthy {
		t.Fatalf("expected state %q, got %q", provider.StateHealthy, p3.State)
	}
	if p3.Timestamp != t2.Format(time.RFC3339) {
		t.Fatalf("expected timestamp %q, got %q", t2.Format(time.RFC3339), p3.Timestamp)
	}
	if cache.GetConsecutiveFailures(widgetID) != 0 {
		t.Fatalf("expected 0 failures after success, got %d", cache.GetConsecutiveFailures(widgetID))
	}
	if cache.GetLastError(widgetID) != nil {
		t.Fatalf("expected nil last error after success, got %v", cache.GetLastError(widgetID))
	}
	lastSuccess, ok := cache.GetLastSuccess(widgetID)
	if !ok || !lastSuccess.Equal(t2) {
		t.Fatalf("expected last success %v, got %v", t2, lastSuccess)
	}

	// 5. Subsequent Success (healthy -> healthy, transitioned=false)
	t3 := t2.Add(60 * time.Second)
	updatedData := map[string]any{"temp": 73.0, "conditions": "sunny"}
	p4, transitioned := cache.RecordSuccess(widgetID, updatedData, t3)
	if transitioned {
		t.Fatal("expected transitioned=false on healthy -> healthy")
	}
	if p4.Timestamp != t3.Format(time.RFC3339) {
		t.Fatalf("expected updated timestamp %q, got %q", t3.Format(time.RFC3339), p4.Timestamp)
	}

	// 6. Transient Failure with LKG (Transition healthy -> degraded)
	t4 := t3.Add(60 * time.Second)
	errTransient := errors.New("upstream 502 bad gateway")
	p5, transitioned := cache.RecordFailure(widgetID, errTransient, t4)
	if !transitioned {
		t.Fatal("expected transitioned=true on healthy -> degraded")
	}
	if p5.State != provider.StateDegraded {
		t.Fatalf("expected state %q, got %q", provider.StateDegraded, p5.State)
	}
	// CRITICAL SPEC-003: Timestamp retains the last successful fetch timestamp (t3), NOT the failure time (t4)
	if p5.Timestamp != t3.Format(time.RFC3339) {
		t.Fatalf("expected LKG timestamp %q, got %q", t3.Format(time.RFC3339), p5.Timestamp)
	}
	// LKG Data is frozen and preserved
	dm, ok := p5.Data.(map[string]any)
	if !ok || dm["temp"] != 73.0 {
		t.Fatalf("expected preserved LKG data with temp 73.0, got %v", p5.Data)
	}
	if cache.GetConsecutiveFailures(widgetID) != 1 {
		t.Fatalf("expected 1 failure, got %d", cache.GetConsecutiveFailures(widgetID))
	}

	// 7. Subsequent Failure while Degraded (transitioned=false to suppress SSE event spam)
	t5 := t4.Add(10 * time.Second)
	p6, transitioned := cache.RecordFailure(widgetID, errTransient, t5)
	if transitioned {
		t.Fatal("expected transitioned=false on consecutive degraded failure")
	}
	if p6.State != provider.StateDegraded {
		t.Fatalf("expected state %q, got %q", provider.StateDegraded, p6.State)
	}
	if p6.Timestamp != t3.Format(time.RFC3339) {
		t.Fatalf("expected LKG timestamp retained %q, got %q", t3.Format(time.RFC3339), p6.Timestamp)
	}
	if cache.GetConsecutiveFailures(widgetID) != 2 {
		t.Fatalf("expected 2 failures, got %d", cache.GetConsecutiveFailures(widgetID))
	}

	// 8. Upstream Recovery (degraded -> healthy)
	t6 := t5.Add(30 * time.Second)
	recoveredData := map[string]any{"temp": 71.8, "conditions": "clear"}
	p7, transitioned := cache.RecordSuccess(widgetID, recoveredData, t6)
	if !transitioned {
		t.Fatal("expected transitioned=true on degraded -> healthy recovery")
	}
	if p7.State != provider.StateHealthy {
		t.Fatalf("expected state %q, got %q", provider.StateHealthy, p7.State)
	}
	if p7.Timestamp != t6.Format(time.RFC3339) {
		t.Fatalf("expected fresh timestamp %q, got %q", t6.Format(time.RFC3339), p7.Timestamp)
	}
	if cache.GetConsecutiveFailures(widgetID) != 0 {
		t.Fatalf("expected failure count reset to 0, got %d", cache.GetConsecutiveFailures(widgetID))
	}

	// 9. RecordPush
	t7 := t6.Add(10 * time.Second)
	pushData := map[string]any{"temp": 70.0, "conditions": "dusk"}
	pPush, _ := cache.RecordPush(widgetID, pushData, t7)
	if pPush.State != provider.StateHealthy {
		t.Fatalf("expected healthy on push, got %s", pPush.State)
	}

	// 10. GetAll & GetStatusMap
	all := cache.GetAll()
	if len(all) != 1 || all[widgetID].State != provider.StateHealthy {
		t.Fatalf("unexpected GetAll result: %v", all)
	}
	statusMap := cache.GetStatusMap()
	if len(statusMap) != 1 || statusMap[widgetID] != provider.StateHealthy {
		t.Fatalf("unexpected GetStatusMap result: %v", statusMap)
	}

	// 11. Purge
	cache.Purge(widgetID)
	if _, ok := cache.Get(widgetID); ok {
		t.Fatal("expected widget to be evicted after Purge")
	}
	if len(cache.GetAll()) != 0 {
		t.Fatal("expected empty GetAll after Purge")
	}
	if cache.GetStatusMap() != nil {
		t.Fatal("expected nil GetStatusMap after Purge")
	}
}

func TestSWRCache_ConcurrentAccessSafety(t *testing.T) {
	t.Parallel()

	cache := provider.NewSWRCache()
	now := time.Now()

	var wg sync.WaitGroup
	workers := 20
	iterations := 100

	for i := 0; i < workers; i++ {
		wg.Add(1)
		wID := fmt.Sprintf("widget-%d", i%5)
		go func(id string, workerIdx int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				if (workerIdx+j)%3 == 0 {
					cache.RecordSuccess(id, map[string]any{"iter": j}, now)
				} else if (workerIdx+j)%3 == 1 {
					cache.RecordFailure(id, errors.New("transient error"), now)
				} else {
					_, _ = cache.Get(id)
					_ = cache.GetAll()
					_ = cache.GetStatusMap()
				}
			}
		}(wID, i)
	}

	wg.Wait()
}
