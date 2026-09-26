package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/azylman/mirrormere/internal/config"
	"github.com/azylman/mirrormere/internal/domain"
	"github.com/azylman/mirrormere/internal/events"
)

// Broadcaster defines the interface for emitting Server-Sent Events over the event bus.
type Broadcaster interface {
	Publish(eventType string, data []byte) *events.Event
}

// StateSink defines the interface for updating shared display state snapshots.
type StateSink interface {
	SetWidgetState(widgetID string, data any, state string, timestamp ...string)
	SetProvidersStatus(providers map[string]string)
}

// CoordinatorConfig specifies dependencies and options for ProviderCoordinator.
type CoordinatorConfig struct {
	Registry    Registry
	Broadcaster Broadcaster
	StateSink   StateSink
	Cache       *SWRCache
	Logger      *slog.Logger
	NowFunc     func() time.Time
	Backoff     *BackoffPolicy
}

type worker struct {
	widgetID   string
	widgetType string
	provider   Provider
	interval   time.Duration
	refreshCh  chan struct{}
	cancel     context.CancelFunc
	done       chan struct{}
	subWg      sync.WaitGroup
}

// ProviderCoordinator orchestrates lifecycle and sync loops for all active widget providers.
// Complies with SPEC-003, SPEC-006, and SPEC-013 Chunk 3.1.
type ProviderCoordinator struct {
	mu          sync.RWMutex
	registry    Registry
	broadcaster Broadcaster
	stateSink   StateSink
	cache       *SWRCache
	logger      *slog.Logger
	nowFunc     func() time.Time
	backoff     *BackoffPolicy
	workers     map[string]*worker
	snapshot    *config.Snapshot
	eventSink   chan WidgetPayload
	sinkCtx     context.Context
	sinkCancel  context.CancelFunc
	sinkWg      sync.WaitGroup
	stopped     bool
}

// NewCoordinator constructs an initialized ProviderCoordinator.
func NewCoordinator(cfg CoordinatorConfig, initialSnapshot *config.Snapshot) *ProviderCoordinator {
	reg := cfg.Registry
	if reg == nil {
		reg = NewRegistry()
	}
	cache := cfg.Cache
	if cache == nil {
		cache = NewSWRCache()
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	nowFn := cfg.NowFunc
	if nowFn == nil {
		nowFn = time.Now
	}
	bo := cfg.Backoff
	if bo == nil {
		bo = DefaultBackoffPolicy()
	}

	sinkCtx, sinkCancel := context.WithCancel(context.Background())

	c := &ProviderCoordinator{
		registry:    reg,
		broadcaster: cfg.Broadcaster,
		stateSink:   cfg.StateSink,
		cache:       cache,
		logger:      logger,
		nowFunc:     nowFn,
		backoff:     bo,
		workers:     make(map[string]*worker),
		snapshot:    initialSnapshot,
		eventSink:   make(chan WidgetPayload, 64),
		sinkCtx:     sinkCtx,
		sinkCancel:  sinkCancel,
	}

	c.sinkWg.Add(1)
	go c.listenEventSink()

	if initialSnapshot != nil && initialSnapshot.Config != nil {
		c.startWorkersLocked(initialSnapshot)
	}

	return c
}

// Cache returns the coordinator's in-memory SWR cache.
func (c *ProviderCoordinator) Cache() *SWRCache {
	return c.cache
}

// GetWidgetState retrieves current state and domain data for a widget instance,
// directly satisfying the render.StateSnapshotProvider contract.
func (c *ProviderCoordinator) GetWidgetState(widgetID string) (any, string, bool) {
	if p, ok := c.cache.Get(widgetID); ok {
		return p.Data, p.State, true
	}
	return nil, "", false
}

// GetWidgetTimestamp retrieves the cached timestamp for a widget instance,
// directly satisfying the render.Engine timestamp provider contract.
func (c *ProviderCoordinator) GetWidgetTimestamp(widgetID string) (string, bool) {
	if p, ok := c.cache.Get(widgetID); ok && p.Timestamp != "" {
		return p.Timestamp, true
	}
	return "", false
}

// RefreshWidget triggers an immediate, out-of-band poll for the given widgetID.
func (c *ProviderCoordinator) RefreshWidget(widgetID string) error {
	c.mu.RLock()
	w, ok := c.workers[widgetID]
	c.mu.RUnlock()

	if !ok {
		return fmt.Errorf("no active provider worker for widget %q", widgetID)
	}

	select {
	case w.refreshCh <- struct{}{}:
	default:
		// Refresh already queued
	}
	return nil
}

// PushUpdate records an unsolicited domain data update (e.g. from inbound webhook)
// into the SWR cache and broadcasts widget.update immediately.
func (c *ProviderCoordinator) PushUpdate(widgetID string, data any) (WidgetPayload, error) {
	now := c.nowFunc()
	payload, _ := c.cache.RecordPush(widgetID, data, now)

	if c.stateSink != nil {
		c.stateSink.SetWidgetState(widgetID, payload.Data, payload.State, payload.Timestamp)
		c.stateSink.SetProvidersStatus(c.cache.GetStatusMap())
	}

	c.broadcastUpdate(payload)
	return payload, nil
}

// UpdateConfig reconciles active providers and workers against a new configuration snapshot per SPEC-012.
func (c *ProviderCoordinator) UpdateConfig(snap *config.Snapshot) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.stopped {
		return errors.New("coordinator is stopped")
	}
	if snap == nil || snap.Config == nil {
		return errors.New("snapshot or config is nil")
	}

	oldSnap := c.snapshot
	c.snapshot = snap

	// Diff widgets to determine added, removed, and modified
	var diff *config.ConfigDiff
	if oldSnap != nil && oldSnap.Config != nil {
		diff = config.DiffConfigs(oldSnap.Config, snap.Config)
	} else {
		diff = &config.ConfigDiff{
			Added: snap.Config.Display.Widgets,
		}
	}

	// 1. Terminate removed widgets
	for _, w := range diff.Removed {
		c.stopWorkerLocked(w.ID)
		c.cache.Purge(w.ID)
	}

	// 2. Restart or reconfigure modified widgets
	for _, m := range diff.Modified {
		if m.DomainChange {
			c.cache.Purge(m.ID)
			nowStr := c.nowFunc().UTC().Format(time.RFC3339)
			if c.stateSink != nil {
				c.stateSink.SetWidgetState(m.ID, map[string]any{}, StateDegraded, nowStr)
				c.stateSink.SetProvidersStatus(c.cache.GetStatusMap())
			}
			c.broadcastUpdate(WidgetPayload{
				WidgetID:  m.ID,
				Timestamp: nowStr,
				State:     StateDegraded,
				Data:      map[string]any{},
			})
		}
		c.stopWorkerLocked(m.ID)
		if m.NewInstance != nil {
			c.startSingleWorkerLocked(m.NewInstance, snap)
		}
	}

	// 3. Start newly added widgets
	for i := range diff.Added {
		c.startSingleWorkerLocked(&diff.Added[i], snap)
	}

	return nil
}

// Stop gracefully shuts down all worker goroutines, subscriptions, and event listeners.
func (c *ProviderCoordinator) Stop() error {
	return c.Shutdown(context.Background())
}

// Shutdown gracefully shuts down with context timeout/cancellation support.
func (c *ProviderCoordinator) Shutdown(ctx context.Context) error {
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return nil
	}
	c.stopped = true

	workersToStop := make([]*worker, 0, len(c.workers))
	for _, w := range c.workers {
		workersToStop = append(workersToStop, w)
	}
	c.workers = make(map[string]*worker)
	c.mu.Unlock()

	defer func() {
		c.sinkCancel()
		c.sinkWg.Wait()
	}()

	// 1. Signal all workers to stop
	for _, w := range workersToStop {
		w.cancel()
	}

	// 2. Wait for all workers to terminate
	for _, w := range workersToStop {
		select {
		case <-w.done:
		case <-ctx.Done():
			return ctx.Err()
		}
		w.subWg.Wait()
		if err := w.provider.Shutdown(ctx); err != nil {
			c.logger.Warn("provider shutdown error", "widget_id", w.widgetID, "error", err)
		}
	}

	return nil
}

func (c *ProviderCoordinator) startWorkersLocked(snap *config.Snapshot) {
	for i := range snap.Config.Display.Widgets {
		w := &snap.Config.Display.Widgets[i]
		c.startSingleWorkerLocked(w, snap)
	}
}

func (c *ProviderCoordinator) startSingleWorkerLocked(w *config.WidgetConfig, snap *config.Snapshot) {
	providerName := resolveProviderName(w, snap)
	var pkg *domain.Package
	if snap.Packages != nil {
		pkg = snap.Packages[w.Type]
	}

	// Native Spacer Optimization (SPEC-005): Zero-overhead padding, no polling ticker needed
	if w.Type == "spacer" || providerName == "spacer" {
		now := c.nowFunc()
		payload, _ := c.cache.RecordSuccess(w.ID, map[string]any{}, now)
		if c.stateSink != nil {
			c.stateSink.SetWidgetState(w.ID, payload.Data, payload.State, payload.Timestamp)
			c.stateSink.SetProvidersStatus(c.cache.GetStatusMap())
		}
		c.broadcastUpdate(payload)
		return
	}

	p, err := c.registry.Create(providerName)
	if err != nil {
		c.logger.Error("failed to create provider for widget", "widget_id", w.ID, "provider", providerName, "error", err)
		c.recordColdBootError(w.ID, err)
		return
	}

	// Merge configuration map including transport details
	cfgMap := c.buildProviderConfig(w)
	initCtx, cancelInit := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelInit()

	if err := p.Init(initCtx, cfgMap); err != nil {
		c.logger.Error("failed to initialize provider for widget", "widget_id", w.ID, "provider", providerName, "error", err)
		c.recordColdBootError(w.ID, err)
		return
	}

	interval := resolveInterval(w, pkg)
	workerCtx, workerCancel := context.WithCancel(context.Background())
	wrk := &worker{
		widgetID:   w.ID,
		widgetType: w.Type,
		provider:   p,
		interval:   interval,
		refreshCh:  make(chan struct{}, 1),
		cancel:     workerCancel,
		done:       make(chan struct{}),
	}
	c.workers[w.ID] = wrk

	// Start subscription if provider supports push events
	wrk.subWg.Add(1)
	go func() {
		defer wrk.subWg.Done()
		if err := p.Subscribe(workerCtx, c.eventSink); err != nil && !errors.Is(err, context.Canceled) {
			c.logger.Warn("provider subscribe error", "widget_id", wrk.widgetID, "error", err)
		}
	}()

	// Start worker loop
	go c.runWorker(workerCtx, wrk)
}

func (c *ProviderCoordinator) stopWorkerLocked(widgetID string) {
	w, ok := c.workers[widgetID]
	if !ok {
		return
	}
	delete(c.workers, widgetID)
	w.cancel()

	stopTimer := time.NewTimer(3 * time.Second)
	defer stopTimer.Stop()

	select {
	case <-w.done:
	case <-stopTimer.C:
		c.logger.Warn("worker termination timed out", "widget_id", widgetID)
	}

	w.subWg.Wait()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := w.provider.Shutdown(ctx); err != nil {
		c.logger.Warn("provider shutdown error", "widget_id", w.widgetID, "error", err)
	}
}

func (c *ProviderCoordinator) runWorker(ctx context.Context, w *worker) {
	defer close(w.done)

	// 1. Initial immediate fetch
	err := c.fetchAndRecord(ctx, w)
	delay := c.backoff.ComputeRetryDelay(err, c.cache.GetConsecutiveFailures(w.widgetID), w.interval)

	timer := time.NewTimer(delay)
	defer func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.refreshCh:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			fetchErr := c.fetchAndRecord(ctx, w)
			nextDelay := c.backoff.ComputeRetryDelay(fetchErr, c.cache.GetConsecutiveFailures(w.widgetID), w.interval)
			timer.Reset(nextDelay)
		case <-timer.C:
			fetchErr := c.fetchAndRecord(ctx, w)
			nextDelay := c.backoff.ComputeRetryDelay(fetchErr, c.cache.GetConsecutiveFailures(w.widgetID), w.interval)
			timer.Reset(nextDelay)
		}
	}
}

func (c *ProviderCoordinator) fetchAndRecord(ctx context.Context, w *worker) error {
	fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	data, err := w.provider.Fetch(fetchCtx)
	if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return err
	}
	now := c.nowFunc()

	if err == nil {
		payload, _ := c.cache.RecordSuccess(w.widgetID, data, now)
		if c.stateSink != nil {
			c.stateSink.SetWidgetState(w.widgetID, payload.Data, payload.State, payload.Timestamp)
			c.stateSink.SetProvidersStatus(c.cache.GetStatusMap())
		}
		c.broadcastUpdate(payload)
		return nil
	}

	c.logger.Warn("provider fetch failed", "widget_id", w.widgetID, "error", err)
	payload, transitioned := c.cache.RecordFailure(w.widgetID, err, now)
	if c.stateSink != nil {
		c.stateSink.SetWidgetState(w.widgetID, payload.Data, payload.State, payload.Timestamp)
		c.stateSink.SetProvidersStatus(c.cache.GetStatusMap())
	}

	// Only broadcast over SSE when operational state actually transitions (suppress degraded retry spam)
	if transitioned {
		c.broadcastUpdate(payload)
	}

	return err
}

func (c *ProviderCoordinator) recordColdBootError(widgetID string, err error) {
	now := c.nowFunc()
	payload, _ := c.cache.RecordFailure(widgetID, err, now)
	if c.stateSink != nil {
		c.stateSink.SetWidgetState(widgetID, payload.Data, payload.State, payload.Timestamp)
		c.stateSink.SetProvidersStatus(c.cache.GetStatusMap())
	}
	c.broadcastUpdate(payload)
}

func (c *ProviderCoordinator) listenEventSink() {
	defer c.sinkWg.Done()

	for {
		select {
		case <-c.sinkCtx.Done():
			return
		case payload, ok := <-c.eventSink:
			if !ok {
				return
			}
			now := c.nowFunc()
			p, _ := c.cache.RecordPush(payload.WidgetID, payload.Data, now)
			if c.stateSink != nil {
				c.stateSink.SetWidgetState(p.WidgetID, p.Data, p.State, p.Timestamp)
				c.stateSink.SetProvidersStatus(c.cache.GetStatusMap())
			}
			c.broadcastUpdate(p)
		}
	}
}

func (c *ProviderCoordinator) broadcastUpdate(payload WidgetPayload) {
	if c.broadcaster == nil {
		return
	}
	data, err := json.Marshal(payload)
	if err != nil {
		c.logger.Error("failed to marshal widget.update payload", "widget_id", payload.WidgetID, "error", err)
		return
	}
	c.broadcaster.Publish(events.EventWidgetUpdate, data)
}

func (c *ProviderCoordinator) buildProviderConfig(w *config.WidgetConfig) map[string]any {
	cfgMap := make(map[string]any)
	if w.Config != nil {
		for k, v := range w.Config {
			cfgMap[k] = v
		}
	}
	if w.Endpoint != "" {
		cfgMap["endpoint"] = w.Endpoint
	}
	if w.Method != "" {
		cfgMap["method"] = w.Method
	}
	if w.Token != "" {
		cfgMap["token"] = w.Token
	}
	if w.TokenEnv != "" {
		cfgMap["token_env"] = w.TokenEnv
	}
	return cfgMap
}

func resolveProviderName(w *config.WidgetConfig, snap *config.Snapshot) string {
	if snap != nil && snap.Packages != nil {
		if pkg := snap.Packages[w.Type]; pkg != nil && pkg.Manifest.Provider != "" {
			return pkg.Manifest.Provider
		}
	}
	return w.Type
}

func resolveInterval(w *config.WidgetConfig, pkg *domain.Package) time.Duration {
	if w.RefreshIntervalSeconds != nil && *w.RefreshIntervalSeconds > 0 {
		return time.Duration(*w.RefreshIntervalSeconds) * time.Second
	}
	if pkg != nil && pkg.Manifest.Refresh.IntervalSeconds > 0 {
		return time.Duration(pkg.Manifest.Refresh.IntervalSeconds) * time.Second
	}
	return 60 * time.Second
}
