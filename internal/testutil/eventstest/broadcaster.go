// Package eventstest holds a recording event broadcaster for tests. It lives
// apart from testutil so internal/events' own tests can import testutil
// without an import cycle.
package eventstest

import (
	"fmt"
	"sync"
	"time"

	"github.com/azylman/mirrormere/internal/events"
)

const publishBuffer = 64

// Broadcaster records every published event. The zero value is ready to use.
type Broadcaster struct {
	mu        sync.Mutex
	events    []*events.Event
	publishCh chan *events.Event
}

func (b *Broadcaster) channel() chan *events.Event {
	if b.publishCh == nil {
		b.publishCh = make(chan *events.Event, publishBuffer)
	}
	return b.publishCh
}

// Publish records an event of eventType carrying data and returns it.
func (b *Broadcaster) Publish(eventType string, data []byte) *events.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	evt := &events.Event{ID: fmt.Sprintf("evt_%d", len(b.events)+1), Type: eventType, Data: data, Timestamp: time.Now()}
	b.events = append(b.events, evt)
	select {
	case b.channel() <- evt:
	default:
	}
	return evt
}

// Events returns a copy of every event published so far, in order.
func (b *Broadcaster) Events() []*events.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]*events.Event(nil), b.events...)
}

// WaitForPublish returns the next published event not yet consumed by an
// earlier WaitForPublish, or false when none arrives within timeout.
func (b *Broadcaster) WaitForPublish(timeout time.Duration) (*events.Event, bool) {
	b.mu.Lock()
	ch := b.channel()
	b.mu.Unlock()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case evt := <-ch:
		return evt, true
	case <-timer.C:
		return nil, false
	}
}
