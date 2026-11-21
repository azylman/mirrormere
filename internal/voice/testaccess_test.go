package voice

import "github.com/azylman/mirrormere/internal/events"

// GetState is a test-only accessor for the current pipeline state.
func (c *Coordinator) GetState() events.VoiceStateData {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snapshotLocked()
}
