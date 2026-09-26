package rotation

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/config"
	"github.com/azylman/mirrormere/internal/domain"
	"github.com/azylman/mirrormere/internal/events"
	"github.com/azylman/mirrormere/internal/layout"
)

type mockBroadcaster struct {
	mu     sync.Mutex
	events []string
	datas  [][]byte
}

func (m *mockBroadcaster) Publish(eventType string, data []byte) *events.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, eventType)
	m.datas = append(m.datas, data)
	return &events.Event{
		Type: eventType,
		Data: data,
	}
}

func (m *mockBroadcaster) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.events)
}

func makeTestSnapshot(totalScreens int, intervalSec int, pauseDurationSec int) *config.Snapshot {
	interval := intervalSec
	pauseSec := pauseDurationSec
	cfg := &config.Config{
		Display: config.DisplayConfig{
			Rotation: config.RotationConfig{
				IntervalSeconds:      &interval,
				PauseDurationSeconds: &pauseSec,
			},
		},
	}

	screens := make([]layout.Screen, 0, totalScreens)
	for i := 0; i < totalScreens; i++ {
		screens = append(screens, layout.Screen{
			Index: i,
			Widgets: []layout.PlacedWidget{
				{
					WidgetID:   "widget-screen",
					Origin:     [2]int{0, 0},
					Dimensions: domain.NewDimension(3, 2),
				},
			},
		})
	}

	return &config.Snapshot{
		Config: cfg,
		Layout: &layout.Layout{
			TotalScreens: totalScreens,
			Screens:      screens,
		},
	}
}

func TestCoordinator_LifecycleAndBasics(t *testing.T) {
	t.Parallel()

	mb := &mockBroadcaster{}
	snap := makeTestSnapshot(2, 30, 120)
	coord := NewCoordinator(Config{
		Broadcaster: mb,
	}, snap)
	defer coord.Stop()

	if coord.CurrentScreen() != 0 {
		t.Fatalf("expected current screen 0, got %d", coord.CurrentScreen())
	}
	if coord.TotalScreens() != 2 {
		t.Fatalf("expected total screens 2, got %d", coord.TotalScreens())
	}
	if coord.IsPaused() {
		t.Fatalf("expected isPaused=false")
	}
	if coord.IntervalSeconds() != 30 {
		t.Fatalf("expected interval 30, got %d", coord.IntervalSeconds())
	}

	data := coord.ScreenRotateData()
	if data.CurrentScreen != 0 || data.TotalScreens != 2 || len(data.Widgets) != 1 {
		t.Fatalf("unexpected screen rotate data: %+v", data)
	}
}

func TestCoordinator_NilSnapshot(t *testing.T) {
	t.Parallel()

	coord := NewCoordinator(Config{}, nil)
	defer coord.Stop()

	if coord.TotalScreens() != 0 {
		t.Fatalf("expected 0 total screens for nil snapshot")
	}
	if coord.IntervalSeconds() != 30 {
		t.Fatalf("expected default 30s interval for nil snapshot")
	}

	// Navigation on nil snapshot returns ErrNoScreens
	if _, err := coord.SelectScreen(0); !errors.Is(err, ErrNoScreens) {
		t.Fatalf("expected ErrNoScreens, got %v", err)
	}
	if _, err := coord.AdvanceScreen("next"); !errors.Is(err, ErrNoScreens) {
		t.Fatalf("expected ErrNoScreens, got %v", err)
	}

	// UpdateConfig with nil snapshot
	data := coord.UpdateConfig(nil)
	if data.TotalScreens != 0 {
		t.Fatalf("expected 0 total screens after nil update")
	}
}

func TestCoordinator_SelectScreen(t *testing.T) {
	t.Parallel()

	mb := &mockBroadcaster{}
	snap := makeTestSnapshot(3, 30, 120)
	coord := NewCoordinator(Config{
		Broadcaster: mb,
	}, snap)
	defer coord.Stop()

	// Out of bounds: negative
	if _, err := coord.SelectScreen(-1); !errors.Is(err, ErrInvalidScreenIndex) {
		t.Fatalf("expected ErrInvalidScreenIndex, got %v", err)
	}
	// Out of bounds: >= total
	if _, err := coord.SelectScreen(3); !errors.Is(err, ErrInvalidScreenIndex) {
		t.Fatalf("expected ErrInvalidScreenIndex, got %v", err)
	}

	// Valid select
	data, err := coord.SelectScreen(1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data.CurrentScreen != 1 {
		t.Fatalf("expected current screen 1, got %d", data.CurrentScreen)
	}
	if coord.CurrentScreen() != 1 {
		t.Fatalf("expected current screen 1, got %d", coord.CurrentScreen())
	}
	if mb.count() != 1 {
		t.Fatalf("expected 1 broadcast event, got %d", mb.count())
	}

	// Select again
	data, err = coord.SelectScreen(2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data.CurrentScreen != 2 {
		t.Fatalf("expected current screen 2, got %d", data.CurrentScreen)
	}
	if mb.count() != 2 {
		t.Fatalf("expected 2 broadcast events, got %d", mb.count())
	}
}

func TestCoordinator_AdvanceScreen(t *testing.T) {
	t.Parallel()

	mb := &mockBroadcaster{}
	snap := makeTestSnapshot(3, 30, 120)
	coord := NewCoordinator(Config{
		Broadcaster: mb,
	}, snap)
	defer coord.Stop()

	// Advance "next"
	data, err := coord.AdvanceScreen("next")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data.CurrentScreen != 1 {
		t.Fatalf("expected 1, got %d", data.CurrentScreen)
	}

	// Advance default (empty string) -> "next"
	data, err = coord.AdvanceScreen("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data.CurrentScreen != 2 {
		t.Fatalf("expected 2, got %d", data.CurrentScreen)
	}

	// Wraparound forward
	data, err = coord.AdvanceScreen("next")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data.CurrentScreen != 0 {
		t.Fatalf("expected 0, got %d", data.CurrentScreen)
	}

	// Advance "prev" with wraparound
	data, err = coord.AdvanceScreen("prev")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data.CurrentScreen != 2 {
		t.Fatalf("expected 2, got %d", data.CurrentScreen)
	}

	// Advance "prev" backward
	data, err = coord.AdvanceScreen("prev")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data.CurrentScreen != 1 {
		t.Fatalf("expected 1, got %d", data.CurrentScreen)
	}

	// Invalid direction
	if _, err := coord.AdvanceScreen("up"); !errors.Is(err, ErrInvalidDirection) {
		t.Fatalf("expected ErrInvalidDirection, got %v", err)
	}
}

func TestCoordinator_SingleScreenNoOp(t *testing.T) {
	t.Parallel()

	mb := &mockBroadcaster{}
	snap := makeTestSnapshot(1, 30, 120)
	coord := NewCoordinator(Config{
		Broadcaster: mb,
	}, snap)
	defer coord.Stop()

	// Advance should be safe no-op on single screen
	data, err := coord.AdvanceScreen("next")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data.CurrentScreen != 0 {
		t.Fatalf("expected 0, got %d", data.CurrentScreen)
	}
	if mb.count() != 1 {
		t.Fatalf("expected 1 broadcast, got %d", mb.count())
	}
}

func TestCoordinator_PauseAndResume(t *testing.T) {
	t.Parallel()

	snap := makeTestSnapshot(2, 30, 120)
	coord := NewCoordinator(Config{}, snap)
	defer coord.Stop()

	// Default pause duration (duration < 0)
	curr, err := coord.PauseRotation(-1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if curr != 0 {
		t.Fatalf("expected screen 0, got %d", curr)
	}
	if !coord.IsPaused() {
		t.Fatalf("expected isPaused=true")
	}

	// Indefinite pause (duration == 0)
	curr, err = coord.PauseRotation(0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !coord.IsPaused() {
		t.Fatalf("expected isPaused=true")
	}

	// Resume
	curr, err = coord.ResumeRotation()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if coord.IsPaused() {
		t.Fatalf("expected isPaused=false")
	}

	// Resume while already resumed (idempotent)
	curr, err = coord.ResumeRotation()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if coord.IsPaused() {
		t.Fatalf("expected isPaused=false")
	}
}

func TestCoordinator_AutoResumeTimer(t *testing.T) {
	t.Parallel()

	snap := makeTestSnapshot(2, 30, 120)
	coord := NewCoordinator(Config{}, snap)
	defer coord.Stop()

	// Short pause for auto-resume
	_, err := coord.PauseRotation(15 * time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !coord.IsPaused() {
		t.Fatalf("expected isPaused=true")
	}

	// Wait for auto-resume
	time.Sleep(35 * time.Millisecond)
	if coord.IsPaused() {
		t.Fatalf("expected auto-resume to clear paused flag")
	}
}

func TestCoordinator_AutoRotateTimer(t *testing.T) {
	t.Parallel()

	mb := &mockBroadcaster{}
	snap := makeTestSnapshot(2, 1, 120) // 1 second interval
	coord := NewCoordinator(Config{
		Broadcaster: mb,
	}, snap)
	defer coord.Stop()

	// Manually trigger rotation timeout directly to test generation and rotation logic deterministically
	coord.mu.Lock()
	gen := coord.timerGen
	coord.mu.Unlock()

	coord.onRotateTimeout(gen)

	if coord.CurrentScreen() != 1 {
		t.Fatalf("expected screen 1 after timeout, got %d", coord.CurrentScreen())
	}
	if mb.count() != 1 {
		t.Fatalf("expected 1 broadcast, got %d", mb.count())
	}

	// Trigger again -> wrap to 0
	coord.mu.Lock()
	gen2 := coord.timerGen
	coord.mu.Unlock()

	coord.onRotateTimeout(gen2)
	if coord.CurrentScreen() != 0 {
		t.Fatalf("expected screen 0, got %d", coord.CurrentScreen())
	}

	// Stale generation should be ignored
	coord.onRotateTimeout(gen) // old gen
	if coord.CurrentScreen() != 0 {
		t.Fatalf("expected screen to remain 0 after stale generation")
	}
}

func TestCoordinator_UpdateConfig(t *testing.T) {
	t.Parallel()

	mb := &mockBroadcaster{}
	snap := makeTestSnapshot(3, 30, 120)
	coord := NewCoordinator(Config{
		Broadcaster: mb,
	}, snap)
	defer coord.Stop()

	// Move to screen 2
	_, _ = coord.SelectScreen(2)
	if coord.CurrentScreen() != 2 {
		t.Fatalf("expected screen 2")
	}

	// Reload config reducing screens to 2 (indices 0 and 1) -> currentScreen must clamp to 0
	newSnap := makeTestSnapshot(2, 15, 60)
	data := coord.UpdateConfig(newSnap)

	if coord.CurrentScreen() != 0 {
		t.Fatalf("expected currentScreen clamped to 0, got %d", coord.CurrentScreen())
	}
	if data.TotalScreens != 2 {
		t.Fatalf("expected totalScreens 2, got %d", data.TotalScreens)
	}
	if coord.IntervalSeconds() != 15 {
		t.Fatalf("expected interval 15, got %d", coord.IntervalSeconds())
	}
}

func TestCoordinator_Stop(t *testing.T) {
	t.Parallel()

	snap := makeTestSnapshot(2, 30, 120)
	coord := NewCoordinator(Config{}, snap)
	coord.Stop()

	if _, err := coord.SelectScreen(0); err == nil {
		t.Fatalf("expected error on stopped coordinator")
	}
	if _, err := coord.AdvanceScreen("next"); err == nil {
		t.Fatalf("expected error on stopped coordinator")
	}
	if _, err := coord.PauseRotation(10); err == nil {
		t.Fatalf("expected error on stopped coordinator")
	}
	if _, err := coord.ResumeRotation(); err == nil {
		t.Fatalf("expected error on stopped coordinator")
	}
}

func TestCoordinator_Concurrency(t *testing.T) {
	t.Parallel()

	mb := &mockBroadcaster{}
	snap := makeTestSnapshot(4, 30, 120)
	coord := NewCoordinator(Config{
		Broadcaster: mb,
	}, snap)
	defer coord.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if id%3 == 0 {
				_, _ = coord.AdvanceScreen("next")
			} else if id%3 == 1 {
				_, _ = coord.SelectScreen(id % 4)
			} else {
				_, _ = coord.PauseRotation(10 * time.Millisecond)
				_, _ = coord.ResumeRotation()
			}
		}(i)
	}
	wg.Wait()

	if coord.CurrentScreen() < 0 || coord.CurrentScreen() >= 4 {
		t.Fatalf("currentScreen out of bounds: %d", coord.CurrentScreen())
	}
}

func TestCoordinator_BranchCoverage(t *testing.T) {
	t.Parallel()

	// 1. Arm with interval <= 0 (manual mode)
	snapManual := makeTestSnapshot(2, 0, 120)
	coordManual := NewCoordinator(Config{}, snapManual)
	defer coordManual.Stop()

	// 2. onRotateTimeout when stopped or paused or layout == nil
	snap := makeTestSnapshot(2, 30, 120)
	coord := NewCoordinator(Config{}, snap)

	coord.mu.Lock()
	gen := coord.timerGen
	coord.paused = true
	coord.mu.Unlock()
	coord.onRotateTimeout(gen) // should return because paused

	coord.mu.Lock()
	coord.paused = false
	coord.snapshot = nil
	coord.mu.Unlock()
	coord.onRotateTimeout(gen) // should return because snapshot is nil

	coord.Stop()
	coord.onRotateTimeout(gen) // should return because stopped

	// 3. onPauseTimeout when stopped or not paused
	coord2 := NewCoordinator(Config{}, snap)
	coord2.onPauseTimeout(123) // not paused -> return
	coord2.Stop()
	coord2.onPauseTimeout(123) // stopped -> return

	// 4. publishRotate with nil broadcaster
	coord3 := NewCoordinator(Config{Broadcaster: nil}, snap)
	coord3.publishRotate(events.ScreenRotateData{})
	coord3.Stop()
}
