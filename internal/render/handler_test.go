package render_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azylman/mirrormere/internal/api"
	"github.com/azylman/mirrormere/internal/config"
	"github.com/azylman/mirrormere/internal/domain"
	"github.com/azylman/mirrormere/internal/layout"
	"github.com/azylman/mirrormere/internal/render"
)

func setupTestServer(t *testing.T) (*render.Handler, string, *domain.Package) {
	t.Helper()
	tmpDir := t.TempDir()

	pkg := createTestPackage(t, tmpDir, "sensor", `<div class="sensor">{{.Data.temp}}</div>`)
	assetsDir := filepath.Join(pkg.Dir, "assets")
	subDir := filepath.Join(assetsDir, "sub")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("failed to create sub assets dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "nested.png"), []byte("pngdata"), 0644); err != nil {
		t.Fatalf("failed to write nested asset: %v", err)
	}

	brokenPkg := createTestPackage(t, tmpDir, "broken", `{{.Data.Bad.Property}}`)
	calPkg := createTestPackage(t, tmpDir, "cal", `<div class="cal" data-date="{{.Config.date}}" data-view="{{.Config.default_view}}">OK</div>`)
	noAssetsPkg := &domain.Package{
		Type:      "no-assets",
		AssetsDir: "",
	}

	snap := &config.Snapshot{
		Config: &config.Config{
			Display: config.DisplayConfig{
				Widgets: []config.WidgetConfig{
					{ID: "sensor-1", Type: "sensor"},
					{ID: "b1", Type: "broken"},
					{ID: "cal-1", Type: "cal", Config: map[string]any{"default_view": "week"}},
				},
			},
		},
		Layout: &layout.Layout{
			Screens: []layout.Screen{
				{
					Index: 0,
					Widgets: []layout.PlacedWidget{
						{WidgetID: "sensor-1", Type: "sensor", Dimensions: domain.NewDimension(2, 1)},
						{WidgetID: "b1", Type: "broken", Dimensions: domain.NewDimension(2, 1)},
						{WidgetID: "cal-1", Type: "cal", Dimensions: domain.NewDimension(2, 1)},
					},
				},
			},
		},
		Packages: map[string]*domain.Package{
			"sensor":    pkg,
			"broken":    brokenPkg,
			"cal":       calPkg,
			"no-assets": noAssetsPkg,
		},
	}

	provider := &mockSnapshotProvider{
		snapshot: snap,
		states: map[string]mockState{
			"sensor-1": {data: map[string]any{"temp": "68F"}, state: "healthy"},
			"b1":       {data: "string-not-map", state: "healthy"},
			"cal-1":    {data: map[string]any{}, state: "healthy"},
		},
	}

	resolver := &mockResolver{packages: map[string]*domain.Package{
		"sensor":    pkg,
		"broken":    brokenPkg,
		"cal":       calPkg,
		"no-assets": noAssetsPkg,
	}}
	engine := render.NewEngine(resolver, provider)

	defaultCSS := filepath.Join(tmpDir, "hud.css")

	handler := render.NewHandler(
		engine,
		resolver,
		render.WithCSSPath(defaultCSS),
	)

	return handler, tmpDir, pkg
}

func TestHandler_GetWidgetRender(t *testing.T) {
	t.Parallel()

	handler, _, _ := setupTestServer(t)

	tests := []struct {
		name       string
		method     string
		path       string
		widgetID   string
		nilEngine  bool
		wantStatus int
		wantBody   []string
		wantEmpty  bool
		wantErrMsg string
		checkCORS  bool
		wantAllow  string
	}{
		{
			name:       "success GET 200",
			method:     http.MethodGet,
			path:       "/api/widgets/sensor-1/render",
			widgetID:   "sensor-1",
			wantStatus: http.StatusOK,
			wantBody:   []string{`<div class="sensor">68F</div>`},
			checkCORS:  true,
		},
		{
			name:       "HEAD 200 empty body",
			method:     http.MethodHead,
			path:       "/api/widgets/sensor-1/render",
			widgetID:   "sensor-1",
			wantStatus: http.StatusOK,
			wantEmpty:  true,
		},
		{
			name:       "OPTIONS 204",
			method:     http.MethodOptions,
			path:       "/api/widgets/sensor-1/render",
			widgetID:   "sensor-1",
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "POST 405 method not allowed",
			method:     http.MethodPost,
			path:       "/api/widgets/sensor-1/render",
			widgetID:   "sensor-1",
			wantStatus: http.StatusMethodNotAllowed,
			wantAllow:  "GET, HEAD, OPTIONS",
			wantErrMsg: "method not allowed",
		},
		{
			name:       "404 widget not found",
			method:     http.MethodGet,
			path:       "/api/widgets/missing-widget/render",
			widgetID:   "missing-widget",
			wantStatus: http.StatusNotFound,
			wantErrMsg: "widget 'missing-widget' not found",
		},
		{
			name:       "500 nil engine",
			method:     http.MethodGet,
			path:       "/api/widgets/sensor-1/render",
			widgetID:   "sensor-1",
			nilEngine:  true,
			wantStatus: http.StatusInternalServerError,
			wantErrMsg: "rendering engine not configured",
		},
		{
			name:       "500 template execution error",
			method:     http.MethodGet,
			path:       "/api/widgets/b1/render",
			widgetID:   "b1",
			wantStatus: http.StatusInternalServerError,
			wantErrMsg: "failed to render widget",
		},
		{
			name:       "valid date override",
			method:     http.MethodGet,
			path:       "/api/widgets/cal-1/render?date=2026-10-15",
			widgetID:   "cal-1",
			wantStatus: http.StatusOK,
			wantBody:   []string{`data-date="2026-10-15"`, `data-view="week"`},
		},
		{name: "valid view override day", method: http.MethodGet, path: "/api/widgets/cal-1/render?view=day", widgetID: "cal-1", wantStatus: http.StatusOK, wantBody: []string{`data-view="day"`}},
		{name: "valid view override week", method: http.MethodGet, path: "/api/widgets/cal-1/render?view=week", widgetID: "cal-1", wantStatus: http.StatusOK, wantBody: []string{`data-view="week"`}},
		{name: "valid view override month", method: http.MethodGet, path: "/api/widgets/cal-1/render?view=month", widgetID: "cal-1", wantStatus: http.StatusOK, wantBody: []string{`data-view="month"`}},
		{name: "valid view override DAY uppercase", method: http.MethodGet, path: "/api/widgets/cal-1/render?view=DAY", widgetID: "cal-1", wantStatus: http.StatusOK, wantBody: []string{`data-view="day"`}},
		{name: "valid view override Month mixed case", method: http.MethodGet, path: "/api/widgets/cal-1/render?view=Month", widgetID: "cal-1", wantStatus: http.StatusOK, wantBody: []string{`data-view="month"`}},
		{
			name:       "valid date and view combined",
			method:     http.MethodGet,
			path:       "/api/widgets/cal-1/render?date=2026-11-20&view=month",
			widgetID:   "cal-1",
			wantStatus: http.StatusOK,
			wantBody:   []string{`data-date="2026-11-20"`, `data-view="month"`},
		},
		{name: "invalid date bad text", method: http.MethodGet, path: "/api/widgets/cal-1/render?date=invalid", widgetID: "cal-1", wantStatus: http.StatusBadRequest, wantErrMsg: "invalid date parameter: must be YYYY-MM-DD"},
		{name: "invalid date bad month", method: http.MethodGet, path: "/api/widgets/cal-1/render?date=2026-13-01", widgetID: "cal-1", wantStatus: http.StatusBadRequest, wantErrMsg: "invalid date parameter: must be YYYY-MM-DD"},
		{name: "invalid date leap day nonleap", method: http.MethodGet, path: "/api/widgets/cal-1/render?date=2026-02-30", widgetID: "cal-1", wantStatus: http.StatusBadRequest, wantErrMsg: "invalid date parameter: must be YYYY-MM-DD"},
		{name: "invalid date US format", method: http.MethodGet, path: "/api/widgets/cal-1/render?date=10-15-2026", widgetID: "cal-1", wantStatus: http.StatusBadRequest, wantErrMsg: "invalid date parameter: must be YYYY-MM-DD"},
		{name: "invalid date slash format", method: http.MethodGet, path: "/api/widgets/cal-1/render?date=2026/10/15", widgetID: "cal-1", wantStatus: http.StatusBadRequest, wantErrMsg: "invalid date parameter: must be YYYY-MM-DD"},
		{name: "invalid view year", method: http.MethodGet, path: "/api/widgets/cal-1/render?view=year", widgetID: "cal-1", wantStatus: http.StatusBadRequest, wantErrMsg: "invalid view parameter: must be day, week, or month"},
		{name: "invalid view agenda", method: http.MethodGet, path: "/api/widgets/cal-1/render?view=agenda", widgetID: "cal-1", wantStatus: http.StatusBadRequest, wantErrMsg: "invalid view parameter: must be day, week, or month"},
		{name: "invalid view list", method: http.MethodGet, path: "/api/widgets/cal-1/render?view=list", widgetID: "cal-1", wantStatus: http.StatusBadRequest, wantErrMsg: "invalid view parameter: must be day, week, or month"},
		{name: "invalid view random", method: http.MethodGet, path: "/api/widgets/cal-1/render?view=invalid", widgetID: "cal-1", wantStatus: http.StatusBadRequest, wantErrMsg: "invalid view parameter: must be day, week, or month"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := handler
			if tc.nilEngine {
				h = render.NewHandler(nil, nil)
			}
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rec := httptest.NewRecorder()
			h.GetWidgetRender(rec, req, tc.widgetID)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if tc.wantEmpty && rec.Body.Len() != 0 {
				t.Errorf("expected empty body, got %d bytes", rec.Body.Len())
			}
			if tc.checkCORS {
				if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
					t.Errorf("CORS origin = %q, want *", got)
				}
				if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
					t.Errorf("Content-Type = %q, want text/html; charset=utf-8", got)
				}
			}
			if tc.wantAllow != "" {
				if got := rec.Header().Get("Allow"); got != tc.wantAllow {
					t.Errorf("Allow header = %q, want %q", got, tc.wantAllow)
				}
			}
			for _, s := range tc.wantBody {
				if !strings.Contains(rec.Body.String(), s) {
					t.Errorf("body missing %q: %s", s, rec.Body.String())
				}
			}
			if tc.wantErrMsg != "" {
				var errResp api.ErrorResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
					t.Fatalf("failed to decode error json: %v", err)
				}
				if !strings.Contains(errResp.Error, tc.wantErrMsg) {
					t.Errorf("error msg = %q, want substring %q", errResp.Error, tc.wantErrMsg)
				}
			}
		})
	}
}

func TestHandler_GetWidgetAsset(t *testing.T) {
	t.Parallel()

	handler, _, _ := setupTestServer(t)

	tests := []struct {
		name        string
		method      string
		widgetType  string
		assetPath   string
		nilResolver bool
		wantStatus  int
		wantBody    string
		wantErrMsg  string
	}{
		{name: "valid svg asset GET", method: http.MethodGet, widgetType: "sensor", assetPath: "icon.svg", wantStatus: http.StatusOK, wantBody: "<svg>icon</svg>"},
		{name: "nested png asset GET", method: http.MethodGet, widgetType: "sensor", assetPath: "sub/nested.png", wantStatus: http.StatusOK, wantBody: "pngdata"},
		{name: "traversal dot dot prefix", method: http.MethodGet, widgetType: "sensor", assetPath: "../icon.svg", wantStatus: http.StatusBadRequest, wantErrMsg: "path traversal detected"},
		{name: "traversal nested escape", method: http.MethodGet, widgetType: "sensor", assetPath: "sub/../../manifest.yaml", wantStatus: http.StatusBadRequest, wantErrMsg: "path traversal detected"},
		{name: "traversal absolute path", method: http.MethodGet, widgetType: "sensor", assetPath: "/etc/passwd", wantStatus: http.StatusBadRequest, wantErrMsg: "path traversal detected"},
		{name: "traversal suffix escape", method: http.MethodGet, widgetType: "sensor", assetPath: "icon.svg/../..", wantStatus: http.StatusBadRequest, wantErrMsg: "path traversal detected"},
		{name: "traversal bare dot dot", method: http.MethodGet, widgetType: "sensor", assetPath: "..", wantStatus: http.StatusBadRequest, wantErrMsg: "path traversal detected"},
		{name: "traversal bare dot", method: http.MethodGet, widgetType: "sensor", assetPath: ".", wantStatus: http.StatusBadRequest, wantErrMsg: "path traversal detected"},
		{name: "traversal empty string", method: http.MethodGet, widgetType: "sensor", assetPath: "", wantStatus: http.StatusBadRequest, wantErrMsg: "path traversal detected"},
		{name: "unknown package 404", method: http.MethodGet, widgetType: "unknown", assetPath: "icon.svg", wantStatus: http.StatusNotFound, wantErrMsg: "asset not found"},
		{name: "missing file 404", method: http.MethodGet, widgetType: "sensor", assetPath: "nonexistent.png", wantStatus: http.StatusNotFound, wantErrMsg: "asset not found"},
		{name: "directory path 404", method: http.MethodGet, widgetType: "sensor", assetPath: "sub", wantStatus: http.StatusNotFound, wantErrMsg: "asset not found"},
		{name: "OPTIONS preflight 204", method: http.MethodOptions, widgetType: "sensor", assetPath: "icon.svg", wantStatus: http.StatusNoContent},
		{name: "POST method not allowed 405", method: http.MethodPost, widgetType: "sensor", assetPath: "icon.svg", wantStatus: http.StatusMethodNotAllowed, wantErrMsg: "method not allowed"},
		{name: "nil resolver 500", method: http.MethodGet, widgetType: "sensor", assetPath: "icon.svg", nilResolver: true, wantStatus: http.StatusInternalServerError, wantErrMsg: "package resolver not configured"},
		{name: "empty assets dir 404", method: http.MethodGet, widgetType: "no-assets", assetPath: "any.png", wantStatus: http.StatusNotFound, wantErrMsg: "asset not found"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := handler
			if tc.nilResolver {
				h = render.NewHandler(nil, nil)
			}
			req := httptest.NewRequest(tc.method, "/widget-types/"+tc.widgetType+"/assets/"+tc.assetPath, nil)
			rec := httptest.NewRecorder()
			h.GetWidgetAsset(rec, req, tc.widgetType, tc.assetPath)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if tc.wantBody != "" && !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Errorf("body missing %q: %s", tc.wantBody, rec.Body.String())
			}
			if tc.wantErrMsg != "" {
				var errResp api.ErrorResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
					t.Fatalf("failed to decode error json: %v", err)
				}
				if !strings.Contains(errResp.Error, tc.wantErrMsg) {
					t.Errorf("error msg = %q, want substring %q", errResp.Error, tc.wantErrMsg)
				}
			}
		})
	}
}

func TestHandler_ServeStyle(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	validCSS := filepath.Join(tmpDir, "valid.css")
	if err := os.WriteFile(validCSS, []byte("/* test stylesheet */"), 0644); err != nil {
		t.Fatalf("failed to write valid css: %v", err)
	}
	customCSS := filepath.Join(tmpDir, "custom.css")
	if err := os.WriteFile(customCSS, []byte("/* custom stylesheet */"), 0644); err != nil {
		t.Fatalf("failed to write custom css: %v", err)
	}
	missingCSS := filepath.Join(tmpDir, "nonexistent.css")

	tests := []struct {
		name            string
		method          string
		cssPath         string
		setPath         string
		checkPath       string
		wantStatus      int
		wantBody        string
		wantEmpty       bool
		wantContentType string
		wantCacheCtrl   string
		wantErrMsg      string
	}{
		{name: "missing stylesheet 404", method: http.MethodGet, cssPath: missingCSS, wantStatus: http.StatusNotFound, wantErrMsg: "stylesheet not found"},
		{name: "empty stylesheet path 404", method: http.MethodGet, cssPath: "", wantStatus: http.StatusNotFound, wantErrMsg: "stylesheet not configured"},
		{name: "valid stylesheet GET 200", method: http.MethodGet, cssPath: validCSS, wantStatus: http.StatusOK, wantBody: "/* test stylesheet */", wantContentType: "text/css; charset=utf-8", wantCacheCtrl: "public, max-age=0, must-revalidate"},
		{name: "custom stylesheet SetCSSPath 200", method: http.MethodGet, cssPath: validCSS, setPath: customCSS, checkPath: customCSS, wantStatus: http.StatusOK, wantBody: "/* custom stylesheet */"},
		{name: "OPTIONS preflight 204", method: http.MethodOptions, cssPath: validCSS, wantStatus: http.StatusNoContent},
		{name: "HEAD request 200", method: http.MethodHead, cssPath: validCSS, wantStatus: http.StatusOK, wantEmpty: true},
		{name: "POST method not allowed 405", method: http.MethodPost, cssPath: validCSS, wantStatus: http.StatusMethodNotAllowed, wantErrMsg: "method not allowed"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := render.NewHandler(nil, nil, render.WithCSSPath(tc.cssPath))
			if tc.setPath != "" {
				h.SetCSSPath(tc.setPath)
			}
			if tc.checkPath != "" {
				if got := h.CSSPath(); got != tc.checkPath {
					t.Fatalf("CSSPath() = %q, want %q", got, tc.checkPath)
				}
			}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, "/style.css", nil)
			h.ServeStyle(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if tc.wantEmpty && rec.Body.Len() != 0 {
				t.Errorf("expected empty body, got %d bytes", rec.Body.Len())
			}
			if tc.wantContentType != "" {
				if got := rec.Header().Get("Content-Type"); got != tc.wantContentType {
					t.Errorf("Content-Type = %q, want %q", got, tc.wantContentType)
				}
			}
			if tc.wantCacheCtrl != "" {
				if got := rec.Header().Get("Cache-Control"); got != tc.wantCacheCtrl {
					t.Errorf("Cache-Control = %q, want %q", got, tc.wantCacheCtrl)
				}
			}
			if tc.wantBody != "" && rec.Body.String() != tc.wantBody {
				t.Errorf("body = %q, want %q", rec.Body.String(), tc.wantBody)
			}
			if tc.wantErrMsg != "" {
				var errResp api.ErrorResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
					t.Fatalf("failed to decode error json: %v", err)
				}
				if !strings.Contains(errResp.Error, tc.wantErrMsg) {
					t.Errorf("error msg = %q, want substring %q", errResp.Error, tc.wantErrMsg)
				}
			}
		})
	}
}
