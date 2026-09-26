package events

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestHub_PublishAndSubscribe(t *testing.T) {
	t.Parallel()

	currTime := time.Date(2026, 9, 25, 20, 0, 0, 0, time.UTC)
	clock := func() time.Time { return currTime }

	cfg := HubConfig{
		RingCapacity:      10,
		RingTTL:           5 * time.Minute,
		HeartbeatInterval: 15 * time.Second,
		SubscriberBuffer:  4,
		NowFunc:           clock,
	}

	hub := NewHub(cfg, nil, nil)
	defer hub.Close()

	if hub.SubscriberCount() != 0 {
		t.Fatalf("expected 0 subscribers initially, got %d", hub.SubscriberCount())
	}
	if hub.StateProvider() != nil {
		t.Error("expected nil StateProvider")
	}
	if hub.RingBuffer() == nil {
		t.Error("expected non-nil RingBuffer")
	}
	if hub.IDGenerator() == nil {
		t.Error("expected non-nil IDGenerator")
	}
	if hub.Config().RingCapacity != 10 {
		t.Errorf("expected RingCapacity 10, got %d", hub.Config().RingCapacity)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch1, unsub1 := hub.Subscribe(ctx)
	ch2, unsub2 := hub.Subscribe(ctx)

	if hub.SubscriberCount() != 2 {
		t.Fatalf("expected 2 subscribers, got %d", hub.SubscriberCount())
	}

	evt := hub.Publish(EventWidgetUpdate, []byte("{\"status\":\"ok\"}"))
	if evt == nil || evt.ID == "" {
		t.Fatal("expected published event with ID")
	}

	// Read from subscriber 1
	select {
	case received := <-ch1:
		if received.ID != evt.ID || received.Type != EventWidgetUpdate {
			t.Errorf("unexpected event received on ch1: %+v", received)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for event on ch1")
	}

	// Read from subscriber 2
	select {
	case received := <-ch2:
		if received.ID != evt.ID {
			t.Errorf("unexpected event received on ch2: %+v", received)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for event on ch2")
	}

	// Unsubscribe sub1
	unsub1()
	if hub.SubscriberCount() != 1 {
		t.Fatalf("expected 1 subscriber after unsub1, got %d", hub.SubscriberCount())
	}

	// Verify ch1 is closed
	_, ok := <-ch1
	if ok {
		t.Error("expected ch1 to be closed after unsubscribe")
	}

	unsub2()
	if hub.SubscriberCount() != 0 {
		t.Fatalf("expected 0 subscribers after unsub2, got %d", hub.SubscriberCount())
	}
}

func TestHub_SlowConsumerDrop(t *testing.T) {
	t.Parallel()

	cfg := HubConfig{
		RingCapacity:      10,
		RingTTL:           5 * time.Minute,
		HeartbeatInterval: 15 * time.Second,
		SubscriberBuffer:  1, // buffer capacity 1
	}

	hub := NewHub(cfg, nil, nil)
	defer hub.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, unsub := hub.Subscribe(ctx)
	defer unsub()

	// Fill buffer
	hub.Publish(EventWidgetUpdate, []byte("1"))
	// Second publish overflows buffer and terminates subscriber per SPEC-006
	hub.Publish(EventWidgetUpdate, []byte("2"))

	if hub.SubscriberCount() != 0 {
		t.Fatalf("expected subscriber to be evicted, count: %d", hub.SubscriberCount())
	}

	select {
	case received := <-ch:
		if string(received.Data) != "1" {
			t.Errorf("expected first event '1', got %s", string(received.Data))
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout reading from ch")
	}

	// Next read must return closed channel
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("expected channel to be closed after slow consumer eviction")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for closed channel")
	}
}

func TestHub_ConcurrentPublishAndSubscribe(t *testing.T) {
	t.Parallel()

	cfg := HubConfig{
		RingCapacity:     500,
		SubscriberBuffer: 100,
	}

	hub := NewHub(cfg, nil, nil)
	defer hub.Close()

	const numSubscribers = 10
	const numPublishers = 10
	const eventsPerPublisher = 20

	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for i := 0; i < numSubscribers; i++ {
		ch, unsub := hub.Subscribe(ctx)
		wg.Add(1)
		go func(c <-chan *Event, un func()) {
			defer wg.Done()
			defer un()
			for {
				select {
				case <-ctx.Done():
					return
				case _, ok := <-c:
					if !ok {
						return
					}
				}
			}
		}(ch, unsub)
	}

	var pubWg sync.WaitGroup
	for i := 0; i < numPublishers; i++ {
		pubWg.Add(1)
		go func() {
			defer pubWg.Done()
			for j := 0; j < eventsPerPublisher; j++ {
				hub.Publish(EventWidgetUpdate, []byte("{}"))
			}
		}()
	}

	pubWg.Wait()
	cancel()
	wg.Wait()
}

func TestHub_Close(t *testing.T) {
	t.Parallel()

	hub := NewHub(HubConfig{}, nil, nil)

	ctx := context.Background()
	ch, _ := hub.Subscribe(ctx)

	hub.Close()
	// Repeated close is idempotent
	hub.Close()

	if hub.SubscriberCount() != 0 {
		t.Errorf("expected 0 subscribers after close, got %d", hub.SubscriberCount())
	}

	// Verify channel closed
	_, ok := <-ch
	if ok {
		t.Error("expected subscriber channel to be closed after hub.Close()")
	}

	// Publish after close is no-op
	hub.Publish(EventWidgetUpdate, []byte("{}"))
}

func TestHub_ReplayAndHydration(t *testing.T) {
	t.Parallel()

	hub := NewHub(HubConfig{}, nil, nil)
	defer hub.Close()

	evt := hub.Publish(EventSystemStatus, []byte("{}"))
	replayed, found := hub.ReplaySince(evt.ID)
	if !found || len(replayed) != 0 {
		t.Errorf("expected replay hit with 0 subsequent events, got (%v, %v)", replayed, found)
	}

	batch := hub.BuildHydration()
	if len(batch) < 6 {
		t.Errorf("expected at least 6 hydration events, got %d", len(batch))
	}
}

func TestHub_PublishNilAndContextDone(t *testing.T) {
	t.Parallel()

	hub := NewHub(HubConfig{}, nil, nil)
	defer hub.Close()

	// Nil event publish is a no-op
	hub.PublishEvent(nil)

	// Subscriber whose context is already canceled
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	hub.Subscribe(ctx)
	if hub.SubscriberCount() != 1 {
		t.Fatalf("expected 1 subscriber, got %d", hub.SubscriberCount())
	}

	// Publish should clean up the canceled subscriber
	hub.Publish(EventWidgetUpdate, []byte("{}"))
	if hub.SubscriberCount() != 0 {
		t.Errorf("expected 0 subscribers after context done unsubscription, got %d", hub.SubscriberCount())
	}
}
