package render

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"os"
	"sync"
	"time"

	"github.com/azylman/mirrormere/internal/config"
	"github.com/azylman/mirrormere/internal/domain"
)

// PackageResolver defines operations needed to look up widget packages.
type PackageResolver interface {
	LoadPackage(widgetType string) (*domain.Package, error)
}

// StateSnapshotProvider supplies runtime configuration snapshots and widget states.
type StateSnapshotProvider interface {
	CurrentSnapshot() *config.Snapshot
	GetWidgetState(widgetID string) (data any, state string, timestamp string, ok bool)
	CurrentStatus() config.Status
}

// WidgetNotFoundError signals that a widget instance does not exist in the active configuration.
type WidgetNotFoundError struct {
	WidgetID string
}

func (e WidgetNotFoundError) Error() string {
	return fmt.Sprintf("widget '%s' not found in active configuration", e.WidgetID)
}

// EngineOption configures Engine instances.
type EngineOption func(*Engine)

// WithNowFunc overrides the clock for deterministic testing.
func WithNowFunc(fn func() time.Time) EngineOption {
	return func(e *Engine) {
		e.nowFunc = fn
	}
}

// Engine manages widget template compilation, in-memory template caching, and server-side rendering.
type Engine struct {
	mu       sync.RWMutex
	cache    map[string]*template.Template
	resolver PackageResolver
	provider StateSnapshotProvider
	nowFunc  func() time.Time
}

// NewEngine constructs a thread-safe Engine.
func NewEngine(resolver PackageResolver, provider StateSnapshotProvider, opts ...EngineOption) *Engine {
	e := &Engine{
		cache:    make(map[string]*template.Template),
		resolver: resolver,
		provider: provider,
		nowFunc:  time.Now,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Invalidate evicts a single widget type from the compiled template cache.
func (e *Engine) Invalidate(widgetType string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.cache, widgetType)
}

// InvalidateAll flushes all compiled templates from memory.
func (e *Engine) InvalidateAll() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cache = make(map[string]*template.Template)
}

// RenderWidget executes the HTML template for the given widget instance, returning the rendered HTML fragment.
func (e *Engine) RenderWidget(ctx context.Context, widgetID string) ([]byte, error) {
	if widgetID == "" {
		return nil, WidgetNotFoundError{WidgetID: ""}
	}

	if e.provider == nil {
		return nil, fmt.Errorf("state snapshot provider is unconfigured")
	}

	snap := e.provider.CurrentSnapshot()
	if snap == nil || snap.Config == nil {
		return nil, fmt.Errorf("no active configuration snapshot available")
	}

	// 1. Locate declared widget instance
	var targetWidget *config.WidgetConfig
	for i := range snap.Config.Display.Widgets {
		if snap.Config.Display.Widgets[i].ID == widgetID {
			targetWidget = &snap.Config.Display.Widgets[i]
			break
		}
	}
	if targetWidget == nil {
		return nil, WidgetNotFoundError{WidgetID: widgetID}
	}

	// 2. Resolve widget package
	var pkg *domain.Package
	if snap.Packages != nil {
		pkg = snap.Packages[targetWidget.Type]
	}
	if pkg == nil && e.resolver != nil {
		var err error
		pkg, err = e.resolver.LoadPackage(targetWidget.Type)
		if err != nil {
			return nil, fmt.Errorf("failed to load package for widget type %q: %w", targetWidget.Type, err)
		}
	}
	if pkg == nil {
		return nil, fmt.Errorf("widget package for type %q not found", targetWidget.Type)
	}

	// 3. Obtain or compile template
	tmpl, err := e.getOrCompileTemplate(pkg)
	if err != nil {
		return nil, err
	}

	// 4. Build execution context
	tmplCtx := e.buildContext(widgetID, targetWidget, pkg, snap)

	// 5. Execute template
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, tmplCtx); err != nil {
		return nil, fmt.Errorf("failed to execute template for widget %q: %w", widgetID, err)
	}

	return buf.Bytes(), nil
}

func (e *Engine) getOrCompileTemplate(pkg *domain.Package) (*template.Template, error) {
	e.mu.RLock()
	cached, ok := e.cache[pkg.Type]
	e.mu.RUnlock()
	if ok {
		return cached, nil
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// Double-check after acquiring write lock
	if cached, ok := e.cache[pkg.Type]; ok {
		return cached, nil
	}

	content, err := os.ReadFile(pkg.ViewPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read widget view template at %q: %w", pkg.ViewPath, err)
	}

	funcMap := StandardFuncMap(e.nowFunc)
	tmpl, err := template.New(pkg.Type).Funcs(funcMap).Parse(string(content))
	if err != nil {
		return nil, fmt.Errorf("template syntax error in %q: %w", pkg.ViewPath, err)
	}

	e.cache[pkg.Type] = tmpl
	return tmpl, nil
}

func (e *Engine) buildContext(widgetID string, w *config.WidgetConfig, pkg *domain.Package, snap *config.Snapshot) Context {
	origin := [2]int{0, 0}
	var dim domain.Dimension
	placed := false

	if snap.Layout != nil {
		for _, screen := range snap.Layout.Screens {
			for _, pw := range screen.Widgets {
				if pw.WidgetID == widgetID {
					origin = pw.Origin
					dim = pw.Dimensions
					placed = true
					break
				}
			}
			if placed {
				break
			}
		}
	}

	if !placed {
		if len(w.Dimensions) == 2 {
			if d, err := domain.DimensionFromSlice(w.Dimensions); err == nil {
				dim = d
			}
		}
		if !dim.IsValid() && pkg != nil && pkg.Manifest.DefaultDimensions.IsValid() {
			dim = pkg.Manifest.DefaultDimensions
		}
		if !dim.IsValid() {
			dim = domain.NewDimension(1, 1)
		}
	}

	var wData any = map[string]any{}
	wState := "healthy"
	var timestamp string
	if e.provider != nil {
		if d, s, ts, ok := e.provider.GetWidgetState(widgetID); ok {
			if d != nil {
				wData = d
			}
			if s != "" {
				wState = s
			}
			timestamp = ts
		}
	}

	if timestamp == "" && e.provider != nil {
		if tsProvider, ok := e.provider.(interface{ GetWidgetTimestamp(string) (string, bool) }); ok {
			if ts, ok := tsProvider.GetWidgetTimestamp(widgetID); ok && ts != "" {
				timestamp = ts
			}
		}
	}

	online := true
	if e.provider != nil {
		st := e.provider.CurrentStatus()
		if st.ConfigStatus == config.ConfigStatusError {
			online = false
		}
	}

	return Context{
		ID:         widgetID,
		Type:       w.Type,
		Dimensions: dim,
		Data:       wData,
		State:      wState,
		Timestamp:  timestamp,
		Config:     SanitizeConfig(w.Config),
		Origin:     origin,
		Theme:      "dark",
		Online:     online,
		Assets:     fmt.Sprintf("/widget-types/%s/assets", w.Type),
	}
}
