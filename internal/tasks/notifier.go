package tasks

import (
	"sync"
)

// ChangeNotifier coordinates real-time notification dispatching when a list changes per SPEC-008.
type ChangeNotifier struct {
	mu        sync.RWMutex
	listeners map[string]map[uint64]chan<- struct{}
	nextID    uint64
}

var (
	defaultNotifierOnce sync.Once
	defaultNotifier     *ChangeNotifier
)

// DefaultChangeNotifier returns the process-wide default ChangeNotifier instance.
func DefaultChangeNotifier() *ChangeNotifier {
	defaultNotifierOnce.Do(func() {
		defaultNotifier = NewChangeNotifier()
	})
	return defaultNotifier
}

// NewChangeNotifier constructs an initialized ChangeNotifier.
func NewChangeNotifier() *ChangeNotifier {
	return &ChangeNotifier{
		listeners: make(map[string]map[uint64]chan<- struct{}),
	}
}

// RegisterListener subscribes a channel to change notifications for listID.
// Returns an unsubscribe function that removes the listener on shutdown.
func (n *ChangeNotifier) RegisterListener(listID string, ch chan<- struct{}) func() {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.listeners[listID] == nil {
		n.listeners[listID] = make(map[uint64]chan<- struct{})
	}
	id := n.nextID
	n.nextID++
	n.listeners[listID][id] = ch

	return func() {
		n.mu.Lock()
		defer n.mu.Unlock()
		if m, ok := n.listeners[listID]; ok {
			delete(m, id)
			if len(m) == 0 {
				delete(n.listeners, listID)
			}
		}
	}
}

// NotifyChange notifies all registered listeners that listID has updated.
// Sends are strictly non-blocking to prevent slow listeners from blocking ingestion.
func (n *ChangeNotifier) NotifyChange(listID string) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	for _, ch := range n.listeners[listID] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
