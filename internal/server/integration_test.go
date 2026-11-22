package server_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azylman/mirrormere/internal/config"
	"github.com/azylman/mirrormere/internal/display"
	"github.com/azylman/mirrormere/internal/domain"
	"github.com/azylman/mirrormere/internal/events"
	"github.com/azylman/mirrormere/internal/render"
	"github.com/azylman/mirrormere/internal/server"
	"github.com/azylman/mirrormere/internal/widget"
)

func TestWalkingSkeleton_EndToEnd(t *testing.T) {
	t.Parallel()

	repoWidgetsDir := filepath.Join("..", "..", "widgets")
	loader := widget.NewLoader(repoWidgetsDir, t.TempDir())
	spacerPkg, err := loader.LoadPackage("spacer")
	if err != nil {
		t.Fatalf("failed to load spacer package: %v", err)
	}

	interval := 30
	cfg := &config.Config{
		Timezone: "America/Los_Angeles",
		Display: config.DisplayConfig{
			Rotation: config.RotationConfig{IntervalSeconds: &interval},
			Widgets: []config.WidgetConfig{
				{ID: "spacer-1", Type: "spacer", Dimensions: []int{3, 2}},
				{ID: "spacer-2", Type: "spacer", Dimensions: []int{3, 2}},
			},
		},
	}
	provider := events.NewInMemoryStateProvider(nil)
	provider.SetSnapshot(&config.Snapshot{Config: cfg, Packages: map[string]*domain.Package{"spacer": spacerPkg}})
	provider.SetWidgetState("spacer-1", map[string]any{}, "healthy")

	renderEngine := render.NewEngine(loader, provider)
	renderH := render.NewHandler(renderEngine, loader, render.WithCSSPath(filepath.Join("..", "..", render.DefaultCSSPath)))
	displayH := display.NewHandler(display.WithTimezoneProvider(func() string {
		if snap := provider.CurrentSnapshot(); snap != nil && snap.Config != nil {
			return snap.Config.Timezone
		}
		return ""
	}))

	router := server.New(server.Config{DisplayHandler: displayH, RenderHandler: renderH}).Routes()

	tests := []struct {
		url      string
		wantType string
		wantSubs []string
	}{
		{"/display", "text/html; charset=utf-8", []string{`id="root"`, `data-timezone="America/Los_Angeles"`, `data-grid="6x2"`, `src="static/ui/app.js"`}},
		{"/display?ui=legacy", "text/html; charset=utf-8", []string{`id="root"`, `src="static/ui/app.js"`}},
		{"/static/js/perf.js", "text/javascript; charset=utf-8", []string{"PerformanceObserver"}},
		{"/style.css", "text/css; charset=utf-8", nil},
		{"/api/widgets/spacer-1/render", "text/html; charset=utf-8", []string{"widget-spacer", `data-widget-id="spacer-1"`, `data-widget-type="spacer"`}},
	}
	for _, tc := range tests {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.url, http.NoBody))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 for %s, got %d", tc.url, rec.Code)
		}
		if tc.wantType != "" && rec.Header().Get("Content-Type") != tc.wantType {
			t.Errorf("expected Content-Type %q for %s, got %q", tc.wantType, tc.url, rec.Header().Get("Content-Type"))
		}
		for _, sub := range tc.wantSubs {
			if !strings.Contains(rec.Body.String(), sub) {
				t.Errorf("expected %q in %s output, got %s", sub, tc.url, rec.Body.String())
			}
		}
	}
}
