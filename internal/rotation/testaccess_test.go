package rotation

import "github.com/azylman/mirrormere/internal/events"

// Test-only accessors; production code reads state through snapshots and published events.
func (c *Coordinator) CurrentScreen() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.currentScreen
}

func (c *Coordinator) ScreenRotateData() events.ScreenRotateData {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.buildRotateDataLocked()
}
