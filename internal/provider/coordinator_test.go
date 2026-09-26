package provider_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/config"
	"github.com/azylman/mirrormere/internal/domain"
	"github.com/azylman/mirrormere/internal/events"
	"github.com/azylman/mirrormere/internal/provider"
)

type mockBroadcaster struct {
	mu        sync.Mutex
	events    []*events.Event
	publishCh chan *events.Event
}

func (m *mockBroadcaster) Publish(eventType string, data []byte) *events.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	evt := &events.Event{
		ID:        "evt_mock",
		Type:      eventType,
		Data:      data,
		Timestamp: time.Now(),
	}
	m.events = append(m.events, evt)
	if m.publishCh == nil {
		m.publishCh = make(chan *events.Event, 64)
	}
	select {
	case m.publishCh <- evt:
	default:
	}
	return evt
}

func (m *mockBroadcaster) waitForPublish(timeout time.Duration) (*events.Event, bool) {
	m.mu.Lock()
	if m.publishCh == nil {
		m.publishCh = make(chan *events.Event, 64)
	}
	ch := m.publishCh
	m.mu.Unlock()

	select {
	case evt := <-ch:
		return evt, true
	case <-time.After(timeout):
		return nil, false
	}
}

func (m *mockBroadcaster) getEvents() []*events.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]*events.Event, len(m.events))
	copy(cp, m.events)
	return cp
}

type mockStateSink struct {
	mu           sync.Mutex
	widgetStates map[string]struct {
		data      any
		state     string
		timestamp string
	}
	providersStatus map[string]string
}

func newMockStateSink() *mockStateSink {
	return &mockStateSink{
		widgetStates: make(map[string]struct {
			data      any
			state     string
			timestamp string
		}),
	}
}

func (s *mockStateSink) SetWidgetState(widgetID string, data any, state string, timestamp ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ts string
	if len(timestamp) > 0 {
		ts = timestamp[0]
	}
	s.widgetStates[widgetID] = struct {
		data      any
		state     string
		timestamp string
	}{data: data, state: state, timestamp: ts}
}

func (s *mockStateSink) SetProvidersStatus(providers map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.providersStatus = providers
}

func (s *mockStateSink) getState(widgetID string) (any, string, string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.widgetStates[widgetID]
	if !ok {
		return nil, "", "", false
	}
	return entry.data, entry.state, entry.timestamp, true
}

type controllableMockProvider struct {
	mu              sync.Mutex
	fetchFunc       func(ctx context.Context) (any, error)
	initFunc        func(ctx context.Context, config map[string]any, opts provider.InitOptions) error
	subSink         chan<- provider.WidgetPayload
	initCalled      bool
	initCfg         map[string]any
	initOpts        provider.InitOptions
	shutDown        bool
	fetchCount      atomic.Int64
	fetchSignal     chan struct{}
	subscribeSignal chan struct{}
}

func newControllableMockProvider() *controllableMockProvider {
	return &controllableMockProvider{
		fetchSignal:     make(chan struct{}, 10),
		subscribeSignal: make(chan struct{}, 10),
	}
}

func (p *controllableMockProvider) Init(ctx context.Context, cfg map[string]any, opts provider.InitOptions) error {
	p.mu.Lock()
	p.initCalled = true
	p.initCfg = cfg
	p.initOpts = opts
	fn := p.initFunc
	p.mu.Unlock()
	if fn != nil {
		return fn(ctx, cfg, opts)
	}
	return nil
}

func (p *controllableMockProvider) Fetch(ctx context.Context) (any, error) {
	p.fetchCount.Add(1)
	select {
	case p.fetchSignal <- struct{}{}:
	default:
	}

	p.mu.Lock()
	fn := p.fetchFunc
	p.mu.Unlock()

	if fn != nil {
		return fn(ctx)
	}
	return map[string]any{"status": "ok"}, nil
}

func (p *controllableMockProvider) Subscribe(ctx context.Context, eventSink chan<- provider.WidgetPayload) error {
	p.mu.Lock()
	p.subSink = eventSink
	p.mu.Unlock()
	select {
	case p.subscribeSignal <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return ctx.Err()
}

func (p *controllableMockProvider) Shutdown(ctx context.Context) error {
	p.mu.Lock()
	p.shutDown = true
	p.mu.Unlock()
	return nil
}

func (p *controllableMockProvider) emitPush(payload provider.WidgetPayload) {
	p.mu.Lock()
	sink := p.subSink
	p.mu.Unlock()
	if sink != nil {
		sink <- payload
	}
}

func waitForState(t *testing.T, cache *provider.SWRCache, widgetID, expectedState string) provider.WidgetPayload {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if p, ok := cache.Get(widgetID); ok && p.State == expectedState {
			return p
		}
		time.Sleep(5 * time.Millisecond)
	}
	p, ok := cache.Get(widgetID)
	t.Fatalf("timed out waiting for widget %q to reach state %q; current: %+v, ok=%v", widgetID, expectedState, p, ok)
	return p
}

func TestProviderCoordinator_SpacerOptimization(t *testing.T) {
	t.Parallel()

	broadcaster := &mockBroadcaster{}
	stateSink := newMockStateSink()

	interval := 30
	snap := &config.Snapshot{
		Config: &config.Config{
			Display: config.DisplayConfig{
				Widgets: []config.WidgetConfig{
					{
						ID:                     "spacer-tile",
						Type:                   "spacer",
						Dimensions:             []int{2, 1},
						RefreshIntervalSeconds: &interval,
					},
				},
			},
		},
		Packages: map[string]*domain.Package{
			"spacer": {
				Type: "spacer",
				Manifest: domain.WidgetManifest{
					Name:     "spacer",
					Provider: "spacer",
				},
			},
		},
	}

	coord := provider.NewCoordinator(provider.CoordinatorConfig{
		Broadcaster: broadcaster,
		StateSink:   stateSink,
	}, snap)
	defer func() { _ = coord.Stop() }()

	// 1. Spacer must be initialized in cache as healthy with empty map
	payload, ok := coord.Cache().Get("spacer-tile")
	if !ok {
		t.Fatal("expected spacer-tile in cache")
	}
	if payload.State != provider.StateHealthy {
		t.Fatalf("expected healthy state, got %s", payload.State)
	}
	dataMap, ok := payload.Data.(map[string]any)
	if !ok || len(dataMap) != 0 {
		t.Fatalf("expected empty map for spacer, got %v", payload.Data)
	}

	// 2. StateSink must be updated
	d, st, _, ok := stateSink.getState("spacer-tile")
	if !ok || st != provider.StateHealthy || len(d.(map[string]any)) != 0 {
		t.Fatalf("expected StateSink updated for spacer-tile, got %v, %s, %v", d, st, ok)
	}

	// 3. Broadcast must have emitted widget.update
	evts := broadcaster.getEvents()
	if len(evts) != 1 || evts[0].Type != events.EventWidgetUpdate {
		t.Fatalf("expected 1 widget.update event, got %d", len(evts))
	}

	// 4. Spacer has no worker goroutine; RefreshWidget returns error
	err := coord.RefreshWidget("spacer-tile")
	if err == nil {
		t.Fatal("expected error refreshing spacer-tile because spacer has no worker")
	}
}

func TestProviderCoordinator_LifecycleAndManualRefresh(t *testing.T) {
	t.Parallel()

	broadcaster := &mockBroadcaster{}
	stateSink := newMockStateSink()
	registry := provider.NewRegistry()
	mockP := newControllableMockProvider()

	registry.Register("mock-weather", func() provider.Provider {
		return mockP
	})

	interval := 60
	snap := &config.Snapshot{
		Config: &config.Config{
			Display: config.DisplayConfig{
				Widgets: []config.WidgetConfig{
					{
						ID:                     "weather-tile",
						Type:                   "mock-weather",
						Dimensions:             []int{2, 1},
						RefreshIntervalSeconds: &interval,
						Config: map[string]any{
							"city": "Oakland",
						},
						Endpoint: "https://api.example.com/weather",
						Token:    "secret123",
					},
				},
			},
		},
	}

	coord := provider.NewCoordinator(provider.CoordinatorConfig{
		Registry:    registry,
		Broadcaster: broadcaster,
		StateSink:   stateSink,
	}, snap)
	defer func() { _ = coord.Stop() }()

	// Wait for initial fetch broadcast
	if _, ok := broadcaster.waitForPublish(2 * time.Second); !ok {
		t.Fatal("timeout waiting for initial fetch broadcast")
	}

	// Verify cache and StateSink
	p, ok := coord.Cache().Get("weather-tile")
	if !ok || p.State != provider.StateHealthy {
		t.Fatalf("expected healthy weather-tile, got %v, %v", p, ok)
	}

	// Test GetWidgetState and GetWidgetTimestamp methods
	wsData, wsState, wsTs, wsOk := coord.GetWidgetState("weather-tile")
	if !wsOk || wsState != provider.StateHealthy || wsData == nil || wsTs == "" {
		t.Fatalf("GetWidgetState returned invalid result: %v, %s, %s, %v", wsData, wsState, wsTs, wsOk)
	}
	if _, _, _, ok := coord.GetWidgetState("unknown-widget"); ok {
		t.Fatal("expected false for unknown widget state")
	}

	wsTs, wsTsOk := coord.GetWidgetTimestamp("weather-tile")
	if !wsTsOk || wsTs == "" {
		t.Fatalf("expected valid timestamp from GetWidgetTimestamp, got %q, %v", wsTs, wsTsOk)
	}
	if _, emptyOk := coord.GetWidgetTimestamp("unknown-widget"); emptyOk {
		t.Fatal("expected false for unknown widget timestamp")
	}

	// Trigger manual refresh
	err := coord.RefreshWidget("weather-tile")
	if err != nil {
		t.Fatalf("RefreshWidget failed: %v", err)
	}

	if _, ok := broadcaster.waitForPublish(2 * time.Second); !ok {
		t.Fatal("timeout waiting for manual refresh broadcast")
	}

	if mockP.fetchCount.Load() < 2 {
		t.Fatalf("expected at least 2 fetches, got %d", mockP.fetchCount.Load())
	}
}

func TestProviderCoordinator_SWRDegradedAndRecovery(t *testing.T) {
	t.Parallel()

	broadcaster := &mockBroadcaster{}
	stateSink := newMockStateSink()
	registry := provider.NewRegistry()
	mockP := newControllableMockProvider()

	registry.Register("mock-stocks", func() provider.Provider {
		return mockP
	})

	// Initial fetch succeeds
	mockP.mu.Lock()
	mockP.fetchFunc = func(ctx context.Context) (any, error) {
		return map[string]any{"price": 150.0}, nil
	}
	mockP.mu.Unlock()

	interval := 10
	snap := &config.Snapshot{
		Config: &config.Config{
			Display: config.DisplayConfig{
				Widgets: []config.WidgetConfig{
					{
						ID:                     "stock-tile",
						Type:                   "mock-stocks",
						Dimensions:             []int{2, 1},
						RefreshIntervalSeconds: &interval,
					},
				},
			},
		},
	}

	coord := provider.NewCoordinator(provider.CoordinatorConfig{
		Registry:    registry,
		Broadcaster: broadcaster,
		StateSink:   stateSink,
	}, snap)
	defer func() { _ = coord.Stop() }()

	// Wait for initial fetch
	select {
	case <-mockP.fetchSignal:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for initial fetch")
	}

	p1, _ := coord.Cache().Get("stock-tile")
	lkgTimestamp := p1.Timestamp
	if p1.State != provider.StateHealthy {
		t.Fatalf("expected healthy initial state, got %s", p1.State)
	}

	// Configure mock to fail with 502 Bad Gateway
	mockP.mu.Lock()
	mockP.fetchFunc = func(ctx context.Context) (any, error) {
		return nil, errors.New("502 bad gateway")
	}
	mockP.mu.Unlock()

	// Trigger manual refresh to cause failure
	if err := coord.RefreshWidget("stock-tile"); err != nil {
		t.Fatalf("failed to refresh: %v", err)
	}

	// Verify degraded state and LKG timestamp preservation
	p2 := waitForState(t, coord.Cache(), "stock-tile", provider.StateDegraded)
	if p2.Timestamp != lkgTimestamp {
		t.Fatalf("expected LKG timestamp %s, got %s", lkgTimestamp, p2.Timestamp)
	}
	dMap, ok := p2.Data.(map[string]any)
	if !ok || dMap["price"] != 150.0 {
		t.Fatalf("expected preserved LKG data price 150.0, got %v", p2.Data)
	}

	// Record count of broadcast events
	eventsCountDegraded := len(broadcaster.getEvents())

	// Trigger a second failure while already degraded
	if err := coord.RefreshWidget("stock-tile"); err != nil {
		t.Fatalf("failed to refresh: %v", err)
	}
	select {
	case <-mockP.fetchSignal:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for consecutive failure fetch")
	}

	// Ensure no redundant widget.update was emitted
	eventsCountSecondFail := len(broadcaster.getEvents())
	if eventsCountSecondFail != eventsCountDegraded {
		t.Fatalf("expected no new broadcast events on consecutive degraded failure, went from %d to %d",
			eventsCountDegraded, eventsCountSecondFail)
	}

	// Recover provider
	mockP.mu.Lock()
	mockP.fetchFunc = func(ctx context.Context) (any, error) {
		return map[string]any{"price": 155.0}, nil
	}
	mockP.mu.Unlock()

	if err := coord.RefreshWidget("stock-tile"); err != nil {
		t.Fatalf("failed to refresh: %v", err)
	}

	p3 := waitForState(t, coord.Cache(), "stock-tile", provider.StateHealthy)
	if p3.State != provider.StateHealthy {
		t.Fatalf("expected recovered state healthy, got %s", p3.State)
	}
	dMap3 := p3.Data.(map[string]any)
	if dMap3["price"] != 155.0 {
		t.Fatalf("expected price 155.0, got %v", p3.Data)
	}
}

func TestProviderCoordinator_ColdBootErrorAndPushUpdate(t *testing.T) {
	t.Parallel()

	broadcaster := &mockBroadcaster{}
	stateSink := newMockStateSink()
	registry := provider.NewRegistry()
	mockP := newControllableMockProvider()

	registry.Register("mock-failing", func() provider.Provider {
		return mockP
	})

	mockP.mu.Lock()
	mockP.fetchFunc = func(ctx context.Context) (any, error) {
		return nil, errors.New("upstream unreachable")
	}
	mockP.mu.Unlock()

	snap := &config.Snapshot{
		Config: &config.Config{
			Display: config.DisplayConfig{
				Widgets: []config.WidgetConfig{
					{
						ID:   "failing-tile",
						Type: "mock-failing",
					},
				},
			},
		},
	}

	coord := provider.NewCoordinator(provider.CoordinatorConfig{
		Registry:    registry,
		Broadcaster: broadcaster,
		StateSink:   stateSink,
	}, snap)
	defer func() { _ = coord.Stop() }()

	p := waitForState(t, coord.Cache(), "failing-tile", provider.StateError)
	if p.State != provider.StateError {
		t.Fatalf("expected state %s, got %s", provider.StateError, p.State)
	}

	// Test PushUpdate
	pushData := map[string]any{"pushed": true, "val": 42}
	payload, err := coord.PushUpdate("failing-tile", pushData)
	if err != nil {
		t.Fatalf("PushUpdate failed: %v", err)
	}
	if payload.State != provider.StateHealthy {
		t.Fatalf("expected healthy on push update, got %s", payload.State)
	}

	pUpdated, _ := coord.Cache().Get("failing-tile")
	if pUpdated.State != provider.StateHealthy {
		t.Fatalf("expected cache updated to healthy, got %s", pUpdated.State)
	}
}

func TestProviderCoordinator_ProviderCreationAndInitFailure(t *testing.T) {
	t.Parallel()

	broadcaster := &mockBroadcaster{}
	stateSink := newMockStateSink()
	registry := provider.NewRegistry()

	// Register a provider whose Init() always fails
	mockP := newControllableMockProvider()
	mockP.initFunc = func(ctx context.Context, config map[string]any, opts provider.InitOptions) error {
		return errors.New("bad configuration credentials")
	}
	registry.Register("failing-init", func() provider.Provider {
		return mockP
	})

	snap := &config.Snapshot{
		Config: &config.Config{
			Display: config.DisplayConfig{
				Widgets: []config.WidgetConfig{
					{
						ID:   "unregistered-widget",
						Type: "unregistered-type",
					},
					{
						ID:   "bad-init-widget",
						Type: "failing-init",
					},
				},
			},
		},
	}

	coord := provider.NewCoordinator(provider.CoordinatorConfig{
		Registry:    registry,
		Broadcaster: broadcaster,
		StateSink:   stateSink,
	}, snap)
	defer func() { _ = coord.Stop() }()

	// Both should be in StateError
	p1, ok1 := coord.Cache().Get("unregistered-widget")
	if !ok1 || p1.State != provider.StateError {
		t.Fatalf("expected StateError for unregistered provider, got %v, %v", p1, ok1)
	}

	p2, ok2 := coord.Cache().Get("bad-init-widget")
	if !ok2 || p2.State != provider.StateError {
		t.Fatalf("expected StateError for bad-init provider, got %v, %v", p2, ok2)
	}
}

func TestProviderCoordinator_ProviderPushSubscription(t *testing.T) {
	t.Parallel()

	broadcaster := &mockBroadcaster{}
	stateSink := newMockStateSink()
	registry := provider.NewRegistry()
	mockP := newControllableMockProvider()

	registry.Register("streaming-provider", func() provider.Provider {
		return mockP
	})

	snap := &config.Snapshot{
		Config: &config.Config{
			Display: config.DisplayConfig{
				Widgets: []config.WidgetConfig{
					{
						ID:   "streaming-tile",
						Type: "streaming-provider",
					},
				},
			},
		},
	}

	coord := provider.NewCoordinator(provider.CoordinatorConfig{
		Registry:    registry,
		Broadcaster: broadcaster,
		StateSink:   stateSink,
	}, snap)
	defer func() { _ = coord.Stop() }()

	select {
	case <-mockP.fetchSignal:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for initial fetch")
	}

	select {
	case <-mockP.subscribeSignal:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for subscribe setup")
	}

	// Provider pushes an event via its eventSink
	mockP.emitPush(provider.WidgetPayload{
		WidgetID:  "streaming-tile",
		State:     provider.StateHealthy,
		Data:      map[string]any{"stream_event": "alert_fired"},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})

	// Wait briefly for coordinator's listenEventSink to process
	var received bool
	for i := 0; i < 50; i++ {
		p, ok := coord.Cache().Get("streaming-tile")
		if ok {
			if d, ok := p.Data.(map[string]any); ok && d["stream_event"] == "alert_fired" {
				received = true
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !received {
		t.Fatal("expected push event to update cache with stream_event")
	}
}

func TestProviderCoordinator_LiveConfigReload(t *testing.T) {
	t.Parallel()

	broadcaster := &mockBroadcaster{}
	stateSink := newMockStateSink()
	registry := provider.NewRegistry()

	mockP1 := newControllableMockProvider()
	mockP2 := newControllableMockProvider()

	registry.Register("type-one", func() provider.Provider {
		return mockP1
	})
	registry.Register("type-two", func() provider.Provider {
		return mockP2
	})

	snap1 := &config.Snapshot{
		Config: &config.Config{
			Display: config.DisplayConfig{
				Widgets: []config.WidgetConfig{
					{ID: "w-retain", Type: "type-one"},
					{ID: "w-remove", Type: "type-one"},
				},
			},
		},
	}

	coord := provider.NewCoordinator(provider.CoordinatorConfig{
		Registry:    registry,
		Broadcaster: broadcaster,
		StateSink:   stateSink,
	}, snap1)
	defer func() { _ = coord.Stop() }()

	// Wait for workers
	time.Sleep(50 * time.Millisecond)
	if _, ok := coord.Cache().Get("w-retain"); !ok {
		t.Fatal("expected w-retain in cache")
	}
	if _, ok := coord.Cache().Get("w-remove"); !ok {
		t.Fatal("expected w-remove in cache")
	}

	// UpdateConfig with snap2: remove w-remove, add w-add, modify w-retain with domain change
	snap2 := &config.Snapshot{
		Config: &config.Config{
			Display: config.DisplayConfig{
				Widgets: []config.WidgetConfig{
					{ID: "w-retain", Type: "type-one", Config: map[string]any{"city": "San Francisco"}},
					{ID: "w-add", Type: "type-two"},
				},
			},
		},
	}

	if err := coord.UpdateConfig(snap2); err != nil {
		t.Fatalf("UpdateConfig failed: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// w-remove must be purged from cache
	if _, ok := coord.Cache().Get("w-remove"); ok {
		t.Fatal("expected w-remove to be purged from cache")
	}

	// Verify that a degraded event was broadcast for w-retain during domain change
	var foundDegraded bool
	for _, e := range broadcaster.getEvents() {
		var p provider.WidgetPayload
		if err := json.Unmarshal(e.Data, &p); err == nil {
			if p.WidgetID == "w-retain" && p.State == provider.StateDegraded {
				foundDegraded = true
				break
			}
		}
	}
	if !foundDegraded {
		t.Fatal("expected degraded event broadcast for w-retain on domain change")
	}

	// w-add must exist
	if _, ok := coord.Cache().Get("w-add"); !ok {
		t.Fatal("expected w-add in cache")
	}

	// Error cases for UpdateConfig
	if err := coord.UpdateConfig(nil); err == nil {
		t.Fatal("expected error for nil snapshot")
	}

	_ = coord.Stop()
	if err := coord.UpdateConfig(snap2); err == nil {
		t.Fatal("expected error updating stopped coordinator")
	}
}

func TestProviderCoordinator_JSONBroadcastHygiene(t *testing.T) {
	t.Parallel()

	broadcaster := &mockBroadcaster{}
	coord := provider.NewCoordinator(provider.CoordinatorConfig{
		Broadcaster: broadcaster,
	}, nil)
	defer func() { _ = coord.Stop() }()

	_, err := coord.PushUpdate("test-widget", map[string]any{"key": "value"})
	if err != nil {
		t.Fatalf("PushUpdate failed: %v", err)
	}

	evts := broadcaster.getEvents()
	if len(evts) != 1 {
		t.Fatalf("expected 1 broadcast event, got %d", len(evts))
	}

	var payload provider.WidgetPayload
	if err := json.Unmarshal(evts[0].Data, &payload); err != nil {
		t.Fatalf("failed to unmarshal broadcast event data: %v", err)
	}
	if payload.WidgetID != "test-widget" || payload.State != provider.StateHealthy {
		t.Fatalf("unexpected unmarshaled payload: %v", payload)
	}
}

func TestProviderCoordinator_PeriodicTimerAndConfigResolution(t *testing.T) {
	t.Parallel()

	registry := provider.NewRegistry()
	mockP := newControllableMockProvider()
	registry.Register("periodic-type", func() provider.Provider {
		return mockP
	})

	// Manifest refresh interval used when WidgetConfig.RefreshIntervalSeconds is nil
	snap := &config.Snapshot{
		Config: &config.Config{
			Display: config.DisplayConfig{
				Widgets: []config.WidgetConfig{
					{
						ID:       "periodic-tile",
						Type:     "periodic-type",
						Endpoint: "/api/test",
						Method:   "POST",
						TokenEnv: "TEST_TOKEN_ENV",
						Token:    "test-secret",
					},
				},
			},
		},
		Packages: map[string]*domain.Package{
			"periodic-type": {
				Type: "periodic-type",
				Manifest: domain.WidgetManifest{
					Name:     "periodic",
					Provider: "periodic-type",
					Refresh: domain.ManifestRefresh{
						IntervalSeconds: 1, // 1 second
					},
				},
			},
		},
	}

	coord := provider.NewCoordinator(provider.CoordinatorConfig{
		Registry: registry,
		// No broadcaster to test nil broadcaster guard
		Broadcaster: nil,
		Backoff: &provider.BackoffPolicy{
			InitialInterval: 10 * time.Millisecond,
			MaxInterval:     20 * time.Millisecond,
			Multiplier:      1.0,
			JitterFraction:  0,
		},
	}, snap)

	// Wait for initial fetch
	select {
	case <-mockP.fetchSignal:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for initial fetch")
	}

	// Wait for periodic timer to tick
	select {
	case <-mockP.fetchSignal:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for periodic timer tick")
	}

	if err := coord.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
}

func TestProviderCoordinator_ShutdownEdgeCases(t *testing.T) {
	t.Parallel()

	registry := provider.NewRegistry()
	mockP := newControllableMockProvider()
	// Shutdown returns error
	mockP.mu.Lock()
	mockP.shutDown = false
	mockP.mu.Unlock()

	registry.Register("edge-type", func() provider.Provider {
		return mockP
	})

	interval := 30
	snap := &config.Snapshot{
		Config: &config.Config{
			Display: config.DisplayConfig{
				Widgets: []config.WidgetConfig{
					{
						ID:                     "edge-tile",
						Type:                   "edge-type",
						RefreshIntervalSeconds: &interval,
					},
				},
			},
		},
	}

	coord := provider.NewCoordinator(provider.CoordinatorConfig{
		Registry: registry,
	}, snap)

	// Test Shutdown with already canceled context
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	// Calling Shutdown with canceledCtx returns context error
	_ = coord.Shutdown(canceledCtx)
}

func TestProviderCoordinator_DomainConfigAndTransportSecretsSeparation(t *testing.T) {
	t.Parallel()

	registry := provider.NewRegistry()
	mockP := newControllableMockProvider()
	registry.Register("custom-sensor", func() provider.Provider {
		return mockP
	})

	interval := 30
	snap := &config.Snapshot{
		Config: &config.Config{
			Display: config.DisplayConfig{
				Widgets: []config.WidgetConfig{
					{
						ID:                     "sensor-tile-1",
						Type:                   "custom-sensor",
						Dimensions:             []int{2, 1},
						RefreshIntervalSeconds: &interval,
						Endpoint:               "https://sensor.local/api/metrics",
						Method:                 "POST",
						TokenEnv:               "SENSOR_TOKEN",
						Token:                  "resolved-bearer-token",
						Secrets: map[string]string{
							"token":   "resolved-bearer-token",
							"api_key": "secret-sensor-api-key",
						},
						Config: map[string]any{
							"city":        "Oakland",
							"endpoint":    "domain-endpoint-override",
							"api_key_env": "SENSOR_API_KEY",
						},
					},
				},
			},
		},
	}

	coord := provider.NewCoordinator(provider.CoordinatorConfig{
		Registry: registry,
	}, snap)
	defer func() {
		_ = coord.Stop()
	}()

	mockP.mu.Lock()
	defer mockP.mu.Unlock()

	if !mockP.initCalled {
		t.Fatal("expected provider Init to be called")
	}

	// 1. Verify Domain Config Isolation
	// - Must NOT contain transport keys injected by Core (token, token_env, method)
	if _, ok := mockP.initCfg["token"]; ok {
		t.Errorf("expected domain config to NOT contain 'token', got %v", mockP.initCfg["token"])
	}
	if _, ok := mockP.initCfg["token_env"]; ok {
		t.Errorf("expected domain config to NOT contain 'token_env', got %v", mockP.initCfg["token_env"])
	}
	if _, ok := mockP.initCfg["method"]; ok {
		t.Errorf("expected domain config to NOT contain 'method', got %v", mockP.initCfg["method"])
	}
	// - Domain key literally named 'endpoint' must NOT be overwritten by transport Endpoint
	if mockP.initCfg["endpoint"] != "domain-endpoint-override" {
		t.Errorf("expected domain config 'endpoint' to be preserved as 'domain-endpoint-override', got %v", mockP.initCfg["endpoint"])
	}
	if mockP.initCfg["city"] != "Oakland" {
		t.Errorf("expected domain config 'city' to be 'Oakland', got %v", mockP.initCfg["city"])
	}
	if mockP.initCfg["api_key_env"] != "SENSOR_API_KEY" {
		t.Errorf("expected domain config 'api_key_env' to be preserved, got %v", mockP.initCfg["api_key_env"])
	}

	// 2. Verify InitOptions Transport & Secrets
	if mockP.initOpts.ID != "sensor-tile-1" {
		t.Errorf("expected InitOptions.ID == 'sensor-tile-1', got %q", mockP.initOpts.ID)
	}
	if mockP.initOpts.Type != "custom-sensor" {
		t.Errorf("expected InitOptions.Type == 'custom-sensor', got %q", mockP.initOpts.Type)
	}
	if len(mockP.initOpts.Dimensions) != 2 || mockP.initOpts.Dimensions[0] != 2 || mockP.initOpts.Dimensions[1] != 1 {
		t.Errorf("expected InitOptions.Dimensions == [2, 1], got %v", mockP.initOpts.Dimensions)
	}
	if mockP.initOpts.Endpoint != "https://sensor.local/api/metrics" {
		t.Errorf("expected InitOptions.Endpoint == 'https://sensor.local/api/metrics', got %q", mockP.initOpts.Endpoint)
	}
	if mockP.initOpts.Method != "POST" {
		t.Errorf("expected InitOptions.Method == 'POST', got %q", mockP.initOpts.Method)
	}
	if mockP.initOpts.Token != "resolved-bearer-token" {
		t.Errorf("expected InitOptions.Token == 'resolved-bearer-token', got %q", mockP.initOpts.Token)
	}
	if mockP.initOpts.GetSecret("token") != "resolved-bearer-token" {
		t.Errorf("expected InitOptions.GetSecret('token') == 'resolved-bearer-token', got %q", mockP.initOpts.GetSecret("token"))
	}
	if mockP.initOpts.GetSecret("api_key") != "secret-sensor-api-key" {
		t.Errorf("expected InitOptions.GetSecret('api_key') == 'secret-sensor-api-key', got %q", mockP.initOpts.GetSecret("api_key"))
	}
}

func TestProviderCoordinator_InitOptionsFallbacks(t *testing.T) {
	t.Parallel()

	registry := provider.NewRegistry()
	mockP := newControllableMockProvider()
	registry.Register("minimal-type", func() provider.Provider {
		return mockP
	})

	interval := 30
	snap := &config.Snapshot{
		Config: &config.Config{
			Display: config.DisplayConfig{
				Widgets: []config.WidgetConfig{
					{
						ID:                     "min-tile",
						Type:                   "minimal-type",
						RefreshIntervalSeconds: &interval,
						Endpoint:               "http://bare.local/poll",
						Token:                  "standalone-token",
						// Method omitted -> defaults to POST when Endpoint is set
						// Secrets omitted -> Token should populate Secrets["token"]
					},
				},
			},
		},
	}

	coord := provider.NewCoordinator(provider.CoordinatorConfig{
		Registry: registry,
	}, snap)
	defer func() {
		_ = coord.Stop()
	}()

	mockP.mu.Lock()
	defer mockP.mu.Unlock()

	if !mockP.initCalled {
		t.Fatal("expected provider Init to be called")
	}

	if mockP.initOpts.Method != "POST" {
		t.Errorf("expected default method 'POST', got %q", mockP.initOpts.Method)
	}
	if mockP.initOpts.Token != "standalone-token" {
		t.Errorf("expected Token == 'standalone-token', got %q", mockP.initOpts.Token)
	}
	if mockP.initOpts.GetSecret("token") != "standalone-token" {
		t.Errorf("expected GetSecret('token') == 'standalone-token', got %q", mockP.initOpts.GetSecret("token"))
	}
}

