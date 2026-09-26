package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/azylman/mirrormere/internal/config"
)

const (
	// DefaultSubscriberBuffer is the per-subscriber buffered channel capacity.
	DefaultSubscriberBuffer = 128
	// DefaultHeartbeatInterval is the SSE keep-alive ping period per SPEC-006 §2.J.
	DefaultHeartbeatInterval = 15 * time.Second
)

// RotationCoordinator abstracts screen rotation coordination for configuration reload.
type RotationCoordinator interface {
	UpdateConfig(snap *config.Snapshot) ScreenRotateData
}

// ProviderCoordinator abstracts provider reconciliation for configuration reload.
type ProviderCoordinator interface {
	UpdateConfig(snap *config.Snapshot) error
}

// HubConfig configures buffering, timing, and capacity for the SSE event hub.
type HubConfig struct {
	RingCapacity      int
	RingTTL           time.Duration
	HeartbeatInterval time.Duration
	SubscriberBuffer  int
	NowFunc           func() time.Time
}

// ApplyDefaults sets nominal fallback settings.
func (c *HubConfig) ApplyDefaults() {
	if c.RingCapacity <= 0 {
		c.RingCapacity = DefaultRingCapacity
	}
	if c.RingTTL <= 0 {
		c.RingTTL = DefaultRingTTL
	}
	if c.HeartbeatInterval <= 0 {
		c.HeartbeatInterval = DefaultHeartbeatInterval
	}
	if c.SubscriberBuffer <= 0 {
		c.SubscriberBuffer = DefaultSubscriberBuffer
	}
	if c.NowFunc == nil {
		c.NowFunc = time.Now
	}
}

type sendResult int

const (
	sendSuccess sendResult = iota
	sendBufferFull
	sendClosed
)

type subscriber struct {
	id     uint64
	ch     chan *Event
	ctx    context.Context
	mu     sync.Mutex
	closed bool
}

func (s *subscriber) trySend(evt *Event) sendResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return sendClosed
	}
	select {
	case s.ch <- evt:
		return sendSuccess
	default:
		return sendBufferFull
	}
}

func (s *subscriber) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.ch)
	}
}

// Hub coordinates event publishing, ring-buffer retention, and concurrent SSE fan-out.
type Hub struct {
	cfg                 HubConfig
	mu                  sync.RWMutex
	subscribers         map[uint64]*subscriber
	ring                *RingBuffer
	idGen               *IDGenerator
	stateProvider       StateProvider
	rotationCoordinator RotationCoordinator
	providerCoordinator ProviderCoordinator
	nextSubID           atomic.Uint64
	logger              *slog.Logger
	closed              atomic.Bool
}

// NewHub constructs a configured Hub instance.
func NewHub(cfg HubConfig, stateProvider StateProvider, logger *slog.Logger) *Hub {
	cfg.ApplyDefaults()
	if logger == nil {
		logger = slog.Default()
	}

	return &Hub{
		cfg:           cfg,
		subscribers:   make(map[uint64]*subscriber),
		ring:          NewRingBuffer(cfg.RingCapacity, cfg.RingTTL, cfg.NowFunc),
		idGen:         NewIDGenerator(),
		stateProvider: stateProvider,
		logger:        logger,
	}
}

// StateProvider returns the active state provider.
func (h *Hub) StateProvider() StateProvider {
	return h.stateProvider
}

// SetRotationCoordinator registers a screen rotation coordinator.
func (h *Hub) SetRotationCoordinator(c RotationCoordinator) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.rotationCoordinator = c
}

// RotationCoordinator returns the registered screen rotation coordinator.
func (h *Hub) RotationCoordinator() RotationCoordinator {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.rotationCoordinator
}

// SetProviderCoordinator registers a provider coordinator.
func (h *Hub) SetProviderCoordinator(c ProviderCoordinator) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.providerCoordinator = c
}

// ProviderCoordinator returns the registered provider coordinator.
func (h *Hub) ProviderCoordinator() ProviderCoordinator {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.providerCoordinator
}

// Config returns the active hub configuration snapshot.
func (h *Hub) Config() HubConfig {
	return h.cfg
}

// RingBuffer returns the underlying ring buffer.
func (h *Hub) RingBuffer() *RingBuffer {
	return h.ring
}

// IDGenerator returns the ID generator instance.
func (h *Hub) IDGenerator() *IDGenerator {
	return h.idGen
}

// Publish creates and broadcasts an event with the supplied type and payload.
func (h *Hub) Publish(eventType string, data []byte) *Event {
	now := h.cfg.NowFunc()
	evt := &Event{
		ID:        h.idGen.Next(now),
		Type:      eventType,
		Data:      data,
		Timestamp: now,
	}
	h.PublishEvent(evt)
	return evt
}

// PublishEvent appends a prepared event to the ring buffer and fans out to all active subscribers.
func (h *Hub) PublishEvent(evt *Event) {
	if h.closed.Load() || evt == nil {
		return
	}

	now := h.cfg.NowFunc()
	if evt.Timestamp.IsZero() {
		evt.Timestamp = now
	}
	if evt.ID == "" {
		evt.ID = h.idGen.Next(now)
	}

	// 1. Retain in ring buffer
	h.ring.Add(evt)

	// 2. Update stateProvider if event is screen.rotate or audio.state
	if h.stateProvider != nil {
		if evt.Type == EventScreenRotate {
			var rd ScreenRotateData
			if err := json.Unmarshal(evt.Data, &rd); err == nil {
				if isp, ok := h.stateProvider.(*InMemoryStateProvider); ok {
					isp.SetScreenRotateData(&rd)
				}
			}
		} else if evt.Type == EventAudioState {
			var ad AudioStateData
			if err := json.Unmarshal(evt.Data, &ad); err == nil {
				if isp, ok := h.stateProvider.(*InMemoryStateProvider); ok {
					isp.SetAudioState(&ad)
				}
			}
		} else if evt.Type == EventVideoState {
			var vd VideoStateData
			if err := json.Unmarshal(evt.Data, &vd); err == nil {
				if isp, ok := h.stateProvider.(*InMemoryStateProvider); ok {
					isp.SetVideoState(&vd)
				}
			}
		} else if evt.Type == EventVoiceState {
			var vs VoiceStateData
			if err := json.Unmarshal(evt.Data, &vs); err == nil {
				if isp, ok := h.stateProvider.(*InMemoryStateProvider); ok {
					isp.SetVoiceState(&vs)
				}
			}
		}
	}

	// 3. Snapshot active subscribers under read lock
	h.mu.RLock()
	subs := make([]*subscriber, 0, len(h.subscribers))
	for _, sub := range h.subscribers {
		subs = append(subs, sub)
	}
	h.mu.RUnlock()

	// 4. Non-blocking fan-out with slow-consumer disconnection per SPEC-006
	for _, sub := range subs {
		if sub.ctx.Err() != nil {
			h.Unsubscribe(sub.id)
			continue
		}

		res := sub.trySend(evt)
		switch res {
		case sendSuccess:
			// Delivered successfully
		case sendBufferFull:
			// Buffer full on slow consumer: terminate subscriber to force client reconnect & state resync
			h.logger.Warn("slow SSE consumer detected; terminating connection to force resync",
				"subscriber_id", sub.id,
				"event_type", evt.Type,
				"event_id", evt.ID,
			)
			h.Unsubscribe(sub.id)
		case sendClosed:
			// Already closed
		}
	}
}

// Subscribe registers a new subscriber channel and returns a receive channel along with an unsubscription function.
func (h *Hub) Subscribe(ctx context.Context) (<-chan *Event, func()) {
	subID := h.nextSubID.Add(1)
	sub := &subscriber{
		id:  subID,
		ch:  make(chan *Event, h.cfg.SubscriberBuffer),
		ctx: ctx,
	}

	h.mu.Lock()
	if !h.closed.Load() {
		h.subscribers[subID] = sub
	} else {
		sub.close()
	}
	h.mu.Unlock()

	unsubscribe := func() {
		h.Unsubscribe(subID)
	}

	return sub.ch, unsubscribe
}

// Unsubscribe removes a subscriber by ID and closes its channel.
func (h *Hub) Unsubscribe(subID uint64) {
	h.mu.Lock()
	sub, exists := h.subscribers[subID]
	if exists {
		delete(h.subscribers, subID)
	}
	h.mu.Unlock()

	if exists {
		sub.close()
	}
}

// SubscriberCount returns the current count of active subscribers.
func (h *Hub) SubscriberCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subscribers)
}

// ReplaySince queries the ring buffer for missed events after lastEventID.
func (h *Hub) ReplaySince(lastEventID string) ([]*Event, bool) {
	return h.ring.ReplaySince(lastEventID)
}

// BuildHydration constructs the 7-step initial state hydration batch.
func (h *Hub) BuildHydration() []*Event {
	return BuildHydrationBatch(h.stateProvider, h.idGen, h.cfg.NowFunc())
}

// Close shuts down the hub and unregisters all subscribers.
func (h *Hub) Close() {
	if h.closed.CompareAndSwap(false, true) {
		h.mu.Lock()
		subs := make([]*subscriber, 0, len(h.subscribers))
		for id, sub := range h.subscribers {
			delete(h.subscribers, id)
			subs = append(subs, sub)
		}
		h.mu.Unlock()

		for _, sub := range subs {
			sub.close()
		}
	}
}
