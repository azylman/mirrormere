package provider

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Standard operational states for widget payloads per SPEC-003 §2.
const (
	StateHealthy  = "healthy"
	StateDegraded = "degraded"
	StateError    = "error"
)

// WidgetPayload represents the standard JSON payload envelope published over the SSE bus
// or stored in cache per SPEC-003 §2 and SPEC-006 §2.A.
type WidgetPayload struct {
	WidgetID  string `json:"widget_id"`
	Timestamp string `json:"timestamp"`
	State     string `json:"state"`
	Data      any    `json:"data"`
}

// Provider defines the lifecycle and synchronization contract for all widget data fetchers.
// Complies with SPEC-003 §1 and Phase 3 Chunk 3.1.
type Provider interface {
	Init(ctx context.Context, config map[string]any) error
	Fetch(ctx context.Context) (any, error)
	Subscribe(ctx context.Context, eventSink chan<- WidgetPayload) error
	Shutdown(ctx context.Context) error
}

// Factory constructs a new instance of a Provider.
type Factory func() Provider

// Registry manages provider factories for in-process widgets.
type Registry interface {
	Register(providerType string, factory Factory)
	Create(providerType string) (Provider, error)
	Has(providerType string) bool
}

// ErrProviderNotRegistered indicates no factory is registered for the requested provider type.
var ErrProviderNotRegistered = errors.New("provider not registered")

// DefaultRegistry implements a thread-safe Registry.
type DefaultRegistry struct {
	mu        sync.RWMutex
	factories map[string]Factory
}

// NewRegistry constructs a Registry pre-populated with built-in providers (e.g. spacer).
func NewRegistry() *DefaultRegistry {
	r := &DefaultRegistry{
		factories: make(map[string]Factory),
	}
	r.Register("spacer", func() Provider {
		return NewSpacerProvider()
	})
	return r
}

// Register registers a factory for a given provider type.
func (r *DefaultRegistry) Register(providerType string, factory Factory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[providerType] = factory
}

// Create instantiates a provider for the given provider type.
func (r *DefaultRegistry) Create(providerType string) (Provider, error) {
	r.mu.RLock()
	factory, ok := r.factories[providerType]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrProviderNotRegistered, providerType)
	}
	return factory(), nil
}

// Has checks whether a factory exists for the provider type.
func (r *DefaultRegistry) Has(providerType string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.factories[providerType]
	return ok
}
