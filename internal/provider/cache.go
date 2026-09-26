package provider

import (
	"sync"
	"time"
)

type cacheEntry struct {
	widgetID            string
	data                any
	state               string
	timestamp           string
	lastSuccess         time.Time
	lastAttempt         time.Time
	consecutiveFailures int
	lastError           error
	hasLKG              bool
}

// SWRCache maintains the in-memory Stale-While-Revalidate state registry for all widgets.
// Thread-safe for concurrent read and write access per SPEC-003 §2.
type SWRCache struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry
}

// NewSWRCache constructs an initialized SWRCache.
func NewSWRCache() *SWRCache {
	return &SWRCache{
		entries: make(map[string]*cacheEntry),
	}
}

// Get returns the cached payload for the given widgetID, if present.
func (c *SWRCache) Get(widgetID string) (WidgetPayload, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	e, ok := c.entries[widgetID]
	if !ok {
		return WidgetPayload{}, false
	}
	return WidgetPayload{
		WidgetID:  e.widgetID,
		Timestamp: e.timestamp,
		State:     e.state,
		Data:      e.data,
	}, true
}

// RecordSuccess records a successful data fetch. It sets the state to "healthy",
// records LKG data, stamps the current RFC 3339 timestamp, resets failure counters,
// and reports whether the state transitioned.
func (c *SWRCache) RecordSuccess(widgetID string, data any, now time.Time) (WidgetPayload, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e, ok := c.entries[widgetID]
	if !ok {
		e = &cacheEntry{widgetID: widgetID}
		c.entries[widgetID] = e
	}

	oldState := e.state
	nowStr := now.UTC().Format(time.RFC3339)

	e.data = data
	e.state = StateHealthy
	e.timestamp = nowStr
	e.lastSuccess = now
	e.lastAttempt = now
	e.consecutiveFailures = 0
	e.lastError = nil
	e.hasLKG = true

	transitioned := (oldState != StateHealthy)

	return WidgetPayload{
		WidgetID:  widgetID,
		Timestamp: nowStr,
		State:     StateHealthy,
		Data:      data,
	}, transitioned
}

// RecordFailure records a failed data fetch. Under Stale-While-Revalidate:
// - If LKG data exists, state transitions to "degraded", LKG data is preserved,
//   and timestamp retains the timestamp of the last successful fetch.
// - If no LKG data exists (cold-boot), state transitions to "error" with empty fallback data.
// Returns the active payload and whether an operational state transition occurred.
func (c *SWRCache) RecordFailure(widgetID string, err error, now time.Time) (WidgetPayload, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e, ok := c.entries[widgetID]
	if !ok {
		e = &cacheEntry{widgetID: widgetID}
		c.entries[widgetID] = e
	}

	oldState := e.state
	e.lastAttempt = now
	e.consecutiveFailures++
	e.lastError = err

	var payload WidgetPayload
	var transitioned bool

	if e.hasLKG {
		e.state = StateDegraded
		transitioned = (oldState != StateDegraded)
		payload = WidgetPayload{
			WidgetID:  widgetID,
			Timestamp: e.timestamp, // Retains timestamp of last successful fetch per SPEC-003 §2
			State:     StateDegraded,
			Data:      e.data, // LKG frozen and preserved
		}
	} else {
		e.state = StateError
		if e.data == nil {
			e.data = map[string]any{}
		}
		nowStr := now.UTC().Format(time.RFC3339)
		e.timestamp = nowStr
		transitioned = (oldState != StateError)
		payload = WidgetPayload{
			WidgetID:  widgetID,
			Timestamp: nowStr,
			State:     StateError,
			Data:      e.data,
		}
	}

	return payload, transitioned
}

// RecordPush records an unsolicited push payload (e.g. from inbound webhook).
func (c *SWRCache) RecordPush(widgetID string, data any, now time.Time) (WidgetPayload, bool) {
	return c.RecordSuccess(widgetID, data, now)
}

// Purge evicts a widget instance from the cache (e.g. when removed or domain parameters change).
func (c *SWRCache) Purge(widgetID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, widgetID)
}

// GetAll returns a snapshot map of all cached widget payloads.
func (c *SWRCache) GetAll() map[string]WidgetPayload {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make(map[string]WidgetPayload, len(c.entries))
	for id, e := range c.entries {
		out[id] = WidgetPayload{
			WidgetID:  e.widgetID,
			Timestamp: e.timestamp,
			State:     e.state,
			Data:      e.data,
		}
	}
	return out
}

// GetStatusMap returns a map of widgetID to operational state string for header/status reporting.
func (c *SWRCache) GetStatusMap() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if len(c.entries) == 0 {
		return nil
	}
	out := make(map[string]string, len(c.entries))
	for id, e := range c.entries {
		out[id] = e.state
	}
	return out
}

// GetConsecutiveFailures returns the number of consecutive fetch failures for widgetID.
func (c *SWRCache) GetConsecutiveFailures(widgetID string) int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if e, ok := c.entries[widgetID]; ok {
		return e.consecutiveFailures
	}
	return 0
}

// GetLastError returns the most recent error recorded for widgetID.
func (c *SWRCache) GetLastError(widgetID string) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if e, ok := c.entries[widgetID]; ok {
		return e.lastError
	}
	return nil
}

// GetLastSuccess returns the timestamp of the last successful fetch.
func (c *SWRCache) GetLastSuccess(widgetID string) (time.Time, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if e, ok := c.entries[widgetID]; ok && e.hasLKG {
		return e.lastSuccess, true
	}
	return time.Time{}, false
}
