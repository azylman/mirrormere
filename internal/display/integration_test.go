package display_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azylman/mirrormere/internal/config"
	"github.com/azylman/mirrormere/internal/display"
	"github.com/azylman/mirrormere/internal/domain"
	"github.com/azylman/mirrormere/internal/render"
	"github.com/azylman/mirrormere/internal/server"
	"github.com/azylman/mirrormere/internal/widget"
)

type staticSnapshotProvider struct {
	snapshot *config.Snapshot
}

func (p *staticSnapshotProvider) CurrentSnapshot() *config.Snapshot {
	return p.snapshot
}

func (p *staticSnapshotProvider) GetWidgetState(widgetID string) (any, string, string, bool) {
	return map[string]any{}, "healthy", "", true
}

func (p *staticSnapshotProvider) CurrentStatus() config.Status {
	return config.Status{
		ConfigStatus: config.ConfigStatusOK,
	}
}


func TestWalkingSkeleton_EndToEnd(t *testing.T) {
	t.Parallel()

	// 1. Prepare configuration snapshot with spacer widgets
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
			Rotation: config.RotationConfig{
				IntervalSeconds: &interval,
			},
			Widgets: []config.WidgetConfig{
				{
					ID:         "spacer-1",
					Type:       "spacer",
					Dimensions: []int{3, 2},
				},
				{
					ID:         "spacer-2",
					Type:       "spacer",
					Dimensions: []int{3, 2},
				},
			},
		},
	}

	packages := map[string]*domain.Package{
		"spacer": spacerPkg,
	}

	snap := &config.Snapshot{
		Config:   cfg,
		Packages: packages,
	}
	provider := &staticSnapshotProvider{snapshot: snap}

	// 2. Wire rendering, display, and server
	renderEngine := render.NewEngine(loader, provider)
	renderH := render.NewHandler(renderEngine, loader)
	displayH := display.NewHandler(
		display.WithTimezoneProvider(func() string {
			if snap := provider.CurrentSnapshot(); snap != nil && snap.Config != nil {
				return snap.Config.Timezone
			}
			return ""
		}),
	)

	srv := server.New(server.Config{
		RenderHandler:  renderH,
		DisplayHandler: displayH,
	})

	router := srv.Routes()

	// 3. Verify GET /display renders base HTML shell
	reqDisplay := httptest.NewRequest(http.MethodGet, "/display", nil)
	recDisplay := httptest.NewRecorder()
	router.ServeHTTP(recDisplay, reqDisplay)

	if recDisplay.Code != http.StatusOK {
		t.Fatalf("expected 200 for /display, got %d", recDisplay.Code)
	}
	displayBody := recDisplay.Body.String()
	if !strings.Contains(displayBody, "id=\"mirrormere-app\"") {
		t.Errorf("expected mirrormere-app container in /display")
	}
	if !strings.Contains(displayBody, `data-timezone="America/Los_Angeles"`) {
		t.Errorf("expected data-timezone=\"America/Los_Angeles\" in /display")
	}
	if !strings.Contains(displayBody, "id=\"grid-canvas\"") {
		t.Errorf("expected grid-canvas container in /display")
	}
	if !strings.Contains(displayBody, "src=\"static/js/sse.js\"") {
		t.Errorf("expected sse.js script tag in /display")
	}
	if !strings.Contains(displayBody, "src=\"static/js/carousel.js\"") {
		t.Errorf("expected carousel.js script tag in /display")
	}
	if !strings.Contains(displayBody, "src=\"static/js/display.js\"") {
		t.Errorf("expected display.js script tag in /display")
	}

	// 4. Verify static assets (/static/js/sse.js, carousel.js, display.js, hud.css)
	staticAssets := []string{
		"/static/js/sse.js",
		"/static/js/carousel.js",
		"/static/js/display.js",
		"/static/css/hud.css",
	}

	for _, assetURL := range staticAssets {
		req := httptest.NewRequest(http.MethodGet, assetURL, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 for %s, got %d", assetURL, rec.Code)
		}
		if rec.Body.Len() == 0 {
			t.Errorf("expected non-empty response body for %s", assetURL)
		}
	}

	// 5. Verify GET /style.css
	reqStyle := httptest.NewRequest(http.MethodGet, "/style.css", nil)
	recStyle := httptest.NewRecorder()
	router.ServeHTTP(recStyle, reqStyle)

	if recStyle.Code != http.StatusOK {
		t.Fatalf("expected 200 for /style.css, got %d", recStyle.Code)
	}
	if recStyle.Header().Get("Content-Type") != "text/css; charset=utf-8" {
		t.Errorf("expected Content-Type text/css, got %q", recStyle.Header().Get("Content-Type"))
	}

	// 6. Verify GET /api/widgets/spacer-1/render renders the spacer widget
	reqRender := httptest.NewRequest(http.MethodGet, "/api/widgets/spacer-1/render", nil)
	recRender := httptest.NewRecorder()
	router.ServeHTTP(recRender, reqRender)

	if recRender.Code != http.StatusOK {
		t.Fatalf("expected 200 for spacer render, got %d", recRender.Code)
	}
	renderBody := recRender.Body.String()
	if !strings.Contains(renderBody, "widget-spacer") {
		t.Errorf("expected widget-spacer class in render output, got %s", renderBody)
	}
	if !strings.Contains(renderBody, "data-widget-id=\"spacer-1\"") {
		t.Errorf("expected data-widget-id=\"spacer-1\" in render output, got %s", renderBody)
	}
	if !strings.Contains(renderBody, "data-widget-type=\"spacer\"") {
		t.Errorf("expected data-widget-type=\"spacer\" in render output, got %s", renderBody)
	}
}
