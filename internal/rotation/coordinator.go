package rotation

import (
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/azylman/mirrormere/internal/config"
	"github.com/azylman/mirrormere/internal/events"
)

var (
	// ErrInvalidScreenIndex indicates the requested screen index is out of bounds [0, total_screens - 1].
	ErrInvalidScreenIndex = errors.New("invalid screen_index: must be between 0 and total_screens - 1")
	// ErrInvalidDirection indicates an advance direction other than 'next' or 'prev'.
	ErrInvalidDirection = errors.New("invalid direction: must be 'next' or 'prev'")
	// ErrNoScreens indicates an advance or select was attempted without a configured layout or when total_screens == 0.
	ErrNoScreens = errors.New("no screens available in active layout")
)

// Broadcaster decouples the rotation coordinator from the concrete events.Hub.
type Broadcaster interface {
	Publish(eventType string, data []byte) *events.Event
}

// Config specifies dependencies for Coordinator initialization.
type Config struct {
	Broadcaster Broadcaster
	NowFunc     func() time.Time
	Logger      *slog.Logger
}

// Coordinator manages screen layout indexing, automated periodic rotation, and pause/resume lifecycle.
type Coordinator struct {
	mu            sync.RWMutex
	broadcaster   Broadcaster
	nowFunc       func() time.Time
	logger        *slog.Logger
	snapshot      *config.Snapshot
	currentScreen int
	paused        bool
	rotateTimer   *time.Timer
	pauseTimer    *time.Timer
	timerGen      uint64
	pauseGen      uint64
	stopped       bool
}

// NewCoordinator constructs and activates a Coordinator instance.
func NewCoordinator(cfg Config, initialSnapshot *config.Snapshot) *Coordinator {
	now := cfg.NowFunc
	if now == nil {
		now = time.Now
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	c := &Coordinator{
		broadcaster: cfg.Broadcaster,
		nowFunc:     now,
		logger:      logger,
		snapshot:    initialSnapshot,
	}

	c.mu.Lock()
	if initialSnapshot != nil && initialSnapshot.Layout != nil {
		c.armRotationTimerLocked()
	}
	c.mu.Unlock()

	return c
}

// CurrentScreen returns the active 0-indexed screen number.
func (c *Coordinator) CurrentScreen() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.currentScreen
}

// TotalScreens returns the total number of screens in the active solved layout.
func (c *Coordinator) TotalScreens() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.snapshot == nil || c.snapshot.Layout == nil {
		return 0
	}
	return c.snapshot.Layout.TotalScreens
}

// IsPaused returns whether automated screen rotation is currently suspended.
func (c *Coordinator) IsPaused() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.paused
}

// IntervalSeconds returns the configured periodic rotation interval in seconds.
func (c *Coordinator) IntervalSeconds() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.snapshot == nil || c.snapshot.Config == nil {
		return 30
	}
	return c.snapshot.Config.Display.Rotation.GetIntervalSeconds()
}

// ScreenRotateData snapshots the active screen layout payload matching SPEC-006 §2.C.
func (c *Coordinator) ScreenRotateData() events.ScreenRotateData {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.buildRotateDataLocked()
}

// SelectScreen navigates directly to a target screen index and broadcasts screen.rotate.
func (c *Coordinator) SelectScreen(index int) (events.ScreenRotateData, error) {
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return events.ScreenRotateData{}, errors.New("coordinator stopped")
	}

	if c.snapshot == nil || c.snapshot.Layout == nil || c.snapshot.Layout.TotalScreens <= 0 {
		c.mu.Unlock()
		return events.ScreenRotateData{}, ErrNoScreens
	}

	total := c.snapshot.Layout.TotalScreens
	if index < 0 || index >= total {
		c.mu.Unlock()
		return events.ScreenRotateData{}, ErrInvalidScreenIndex
	}

	c.currentScreen = index
	c.armRotationTimerLocked()
	data := c.buildRotateDataLocked()
	c.mu.Unlock()

	c.publishRotate(data)
	return data, nil
}

// AdvanceScreen navigates sequentially forward ("next") or backward ("prev") and broadcasts screen.rotate.
func (c *Coordinator) AdvanceScreen(direction string) (events.ScreenRotateData, error) {
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return events.ScreenRotateData{}, errors.New("coordinator stopped")
	}

	if c.snapshot == nil || c.snapshot.Layout == nil || c.snapshot.Layout.TotalScreens <= 0 {
		c.mu.Unlock()
		return events.ScreenRotateData{}, ErrNoScreens
	}

	total := c.snapshot.Layout.TotalScreens
	if total == 1 {
		// Single screen: rotation is a safe no-op per Edge Case 1
		c.currentScreen = 0
		data := c.buildRotateDataLocked()
		c.mu.Unlock()
		c.publishRotate(data)
		return data, nil
	}

	switch direction {
	case "", "next":
		c.currentScreen = (c.currentScreen + 1) % total
	case "prev":
		c.currentScreen = (c.currentScreen - 1 + total) % total
	default:
		c.mu.Unlock()
		return events.ScreenRotateData{}, ErrInvalidDirection
	}

	c.armRotationTimerLocked()
	data := c.buildRotateDataLocked()
	c.mu.Unlock()

	c.publishRotate(data)
	return data, nil
}

// PauseRotation suspends automated rotation. If duration > 0, an auto-resume timer will
// unpause rotation after duration. If duration == 0, rotation remains paused indefinitely.
// If duration < 0, it defaults to the configured rotation.pause_duration_seconds (default 120s).
func (c *Coordinator) PauseRotation(duration time.Duration) (int, error) {
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return 0, errors.New("coordinator stopped")
	}

	c.paused = true
	c.stopRotationTimerLocked()
	c.stopPauseTimerLocked()

	current := c.currentScreen

	if duration < 0 {
		defaultSec := 120
		if c.snapshot != nil && c.snapshot.Config != nil {
			defaultSec = c.snapshot.Config.Display.Rotation.GetPauseDurationSeconds()
		}
		duration = time.Duration(defaultSec) * time.Second
	}

	if duration > 0 {
		c.pauseGen++
		gen := c.pauseGen
		c.pauseTimer = time.AfterFunc(duration, func() {
			c.onPauseTimeout(gen)
		})
	}

	c.mu.Unlock()
	return current, nil
}

// ResumeRotation cancels any pending auto-resume timer, re-arms periodic rotation, and unpauses.
func (c *Coordinator) ResumeRotation() (int, error) {
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return 0, errors.New("coordinator stopped")
	}

	c.paused = false
	c.stopPauseTimerLocked()
	c.armRotationTimerLocked()
	current := c.currentScreen
	c.mu.Unlock()

	return current, nil
}

// UpdateConfig updates the running configuration and layout snapshot.
// It clamps currentScreen if the new screen count decreased, updates timers, and broadcasts screen.rotate.
func (c *Coordinator) UpdateConfig(snap *config.Snapshot) events.ScreenRotateData {
	c.mu.Lock()
	c.snapshot = snap

	if snap == nil || snap.Layout == nil || snap.Layout.TotalScreens <= 0 {
		c.currentScreen = 0
		c.stopRotationTimerLocked()
		c.mu.Unlock()
		return events.ScreenRotateData{}
	}

	total := snap.Layout.TotalScreens
	if c.currentScreen >= total {
		c.currentScreen = 0
	}

	c.armRotationTimerLocked()
	data := c.buildRotateDataLocked()
	c.mu.Unlock()

	c.publishRotate(data)
	return data
}

// Stop terminates active timers and ceases background coordination.
func (c *Coordinator) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopped = true
	c.stopRotationTimerLocked()
	c.stopPauseTimerLocked()
}

func (c *Coordinator) armRotationTimerLocked() {
	c.stopRotationTimerLocked()

	if c.stopped || c.paused || c.snapshot == nil || c.snapshot.Layout == nil {
		return
	}

	total := c.snapshot.Layout.TotalScreens
	if total <= 1 {
		// Single screen: timer must remain dormant per Edge Case 1
		return
	}

	intervalSec := 30
	if c.snapshot.Config != nil {
		intervalSec = c.snapshot.Config.Display.Rotation.GetIntervalSeconds()
	}
	if intervalSec <= 0 {
		// Manual mode: timer disabled per Edge Case 3
		return
	}

	c.timerGen++
	gen := c.timerGen
	interval := time.Duration(intervalSec) * time.Second

	c.rotateTimer = time.AfterFunc(interval, func() {
		c.onRotateTimeout(gen)
	})
}

func (c *Coordinator) stopRotationTimerLocked() {
	c.timerGen++
	if c.rotateTimer != nil {
		c.rotateTimer.Stop()
		c.rotateTimer = nil
	}
}

func (c *Coordinator) stopPauseTimerLocked() {
	c.pauseGen++
	if c.pauseTimer != nil {
		c.pauseTimer.Stop()
		c.pauseTimer = nil
	}
}

func (c *Coordinator) onRotateTimeout(gen uint64) {
	c.mu.Lock()
	if c.stopped || c.paused || c.timerGen != gen {
		c.mu.Unlock()
		return
	}

	if c.snapshot == nil || c.snapshot.Layout == nil || c.snapshot.Layout.TotalScreens <= 1 {
		c.mu.Unlock()
		return
	}

	total := c.snapshot.Layout.TotalScreens
	c.currentScreen = (c.currentScreen + 1) % total
	c.armRotationTimerLocked()
	data := c.buildRotateDataLocked()
	c.mu.Unlock()

	c.publishRotate(data)
}

func (c *Coordinator) onPauseTimeout(gen uint64) {
	c.mu.Lock()
	if c.stopped || !c.paused || c.pauseGen != gen {
		c.mu.Unlock()
		return
	}

	c.paused = false
	c.armRotationTimerLocked()
	c.mu.Unlock()
}

func (c *Coordinator) buildRotateDataLocked() events.ScreenRotateData {
	var data events.ScreenRotateData
	data.CurrentScreen = c.currentScreen
	data.TotalScreens = 1
	data.IntervalSeconds = 30
	data.Widgets = make([]events.ScreenRotateWidget, 0)

	if c.snapshot != nil {
		if c.snapshot.Config != nil {
			data.IntervalSeconds = c.snapshot.Config.Display.Rotation.GetIntervalSeconds()
		}
		if c.snapshot.Layout != nil {
			data.TotalScreens = c.snapshot.Layout.TotalScreens
			if c.currentScreen < len(c.snapshot.Layout.Screens) {
				screen := c.snapshot.Layout.Screens[c.currentScreen]
				for _, pw := range screen.Widgets {
					data.Widgets = append(data.Widgets, events.ScreenRotateWidget{
						WidgetID:   pw.WidgetID,
						Origin:     pw.Origin,
						Dimensions: pw.Dimensions,
					})
				}
			}
		}
	}

	return data
}

func (c *Coordinator) publishRotate(data events.ScreenRotateData) {
	if c.broadcaster == nil {
		return
	}
	bytes, err := json.Marshal(data)
	if err != nil {
		c.logger.Error("failed to marshal screen.rotate event", "error", err)
		return
	}
	c.broadcaster.Publish(events.EventScreenRotate, bytes)
}
