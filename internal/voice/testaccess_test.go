package voice

import "github.com/azylman/mirrormere/internal/events"

// GetState is a test-only accessor for the current pipeline state.
func (c *Coordinator) GetState() events.VoiceStateData {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snapshotLocked()
}

// WithClock is a test-only option that replaces the hub's real clock for
// inactivity deadlines and the thinking heartbeat.
func WithClock(c clock) HubOption {
	return func(h *Hub) { h.clock = c }
}
