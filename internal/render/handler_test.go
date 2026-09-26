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
	// Write extra assets
	assetsDir := filepath.Join(pkg.Dir, "assets")
	subDir := filepath.Join(assetsDir, "sub")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("failed to create sub assets dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "nested.png"), []byte("pngdata"), 0644); err != nil {
		t.Fatalf("failed to write nested asset: %v", err)
	}

	snap := &config.Snapshot{
		Config: &config.Config{
			Display: config.DisplayConfig{
				Widgets: []config.WidgetConfig{
					{
						ID:   "sensor-1",
						Type: "sensor",
					},
				},
			},
		},
		Layout: &layout.Layout{
			Screens: []layout.Screen{
				{
					Index: 0,
					Widgets: []layout.PlacedWidget{
						{WidgetID: "sensor-1", Type: "sensor", Dimensions: domain.NewDimension(2, 1)},
					},
				},
			},
		},
		Packages: map[string]*domain.Package{
			"sensor": pkg,
		},
	}

	provider := &mockSnapshotProvider{
		snapshot: snap,
		states: map[string]mockState{
			"sensor-1": {data: map[string]any{"temp": "68F"}, state: "healthy"},
		},
	}

	resolver := &mockResolver{packages: map[string]*domain.Package{"sensor": pkg}}
	engine := render.NewEngine(resolver, provider)

	customCSS := filepath.Join(tmpDir, "custom.css")
	defaultCSS := filepath.Join(tmpDir, "hud.css")

	handler := render.NewHandler(
		engine,
		resolver,
		render.WithCustomCSSPath(customCSS),
		render.WithDefaultCSSPath(defaultCSS),
		render.WithDefaultCSSContent([]byte("/* embedded test css */")),
	)

	return handler, tmpDir, pkg
}

func TestHandler_GetWidgetRender(t *testing.T) {
	t.Parallel()

	handler, _, _ := setupTestServer(t)

	// Case 1: Success 200 GET
	req := httptest.NewRequest(http.MethodGet, "/api/widgets/sensor-1/render", nil)
	rec := httptest.NewRecorder()
	handler.GetWidgetRender(rec, req, "sensor-1")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("expected Content-Type text/html; charset=utf-8, got %q", ct)
	}
	if !strings.Contains(rec.Body.String(), `<div class="sensor">68F</div>`) {
		t.Errorf("unexpected render body: %s", rec.Body.String())
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("expected CORS origin '*', got %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}

	// Case 2: HEAD 200 with empty body
	reqHead := httptest.NewRequest(http.MethodHead, "/api/widgets/sensor-1/render", nil)
	recHead := httptest.NewRecorder()
	handler.GetWidgetRender(recHead, reqHead, "sensor-1")
	if recHead.Code != http.StatusOK {
		t.Fatalf("expected HEAD status 200, got %d", recHead.Code)
	}
	if recHead.Body.Len() != 0 {
		t.Errorf("expected empty body for HEAD, got %d bytes", recHead.Body.Len())
	}

	// Case 3: OPTIONS 204 No Content
	reqOptions := httptest.NewRequest(http.MethodOptions, "/api/widgets/sensor-1/render", nil)
	recOptions := httptest.NewRecorder()
	handler.GetWidgetRender(recOptions, reqOptions, "sensor-1")
	if recOptions.Code != http.StatusNoContent {
		t.Fatalf("expected status 204 for OPTIONS, got %d", recOptions.Code)
	}

	// Case 4: Method Not Allowed (POST)
	reqPost := httptest.NewRequest(http.MethodPost, "/api/widgets/sensor-1/render", nil)
	recPost := httptest.NewRecorder()
	handler.GetWidgetRender(recPost, reqPost, "sensor-1")
	if recPost.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status 405 for POST, got %d", recPost.Code)
	}

	// Case 5: 404 Widget Not Found
	req404 := httptest.NewRequest(http.MethodGet, "/api/widgets/missing-widget/render", nil)
	rec404 := httptest.NewRecorder()
	handler.GetWidgetRender(rec404, req404, "missing-widget")
	if rec404.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rec404.Code)
	}
	var errResp api.ErrorResponse
	if err := json.Unmarshal(rec404.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("failed to decode error json: %v", err)
	}
	if !strings.Contains(errResp.Error, "widget 'missing-widget' not found") {
		t.Errorf("unexpected error message: %q", errResp.Error)
	}

	// Case 6: Unconfigured Engine returns 500
	unconfiguredHandler := render.NewHandler(nil, nil)
	rec500 := httptest.NewRecorder()
	unconfiguredHandler.GetWidgetRender(rec500, req, "sensor-1")
	if rec500.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500 for unconfigured engine, got %d", rec500.Code)
	}

	// Case 7: Rendering execution failure returns 500
	tmpDir := t.TempDir()
	brokenPkg := createTestPackage(t, tmpDir, "broken", `{{.Data.Bad.Property}}`)
	snapBroken := &config.Snapshot{
		Config: &config.Config{
			Display: config.DisplayConfig{
				Widgets: []config.WidgetConfig{{ID: "b1", Type: "broken"}},
			},
		},
		Packages: map[string]*domain.Package{"broken": brokenPkg},
	}
	provBroken := &mockSnapshotProvider{
		snapshot: snapBroken,
		states:   map[string]mockState{"b1": {data: "string-not-map"}},
	}
	engBroken := render.NewEngine(&mockResolver{packages: map[string]*domain.Package{"broken": brokenPkg}}, provBroken)
	hBroken := render.NewHandler(engBroken, nil)
	recBroken := httptest.NewRecorder()
	hBroken.GetWidgetRender(recBroken, req, "b1")
	if recBroken.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500 on template failure, got %d", recBroken.Code)
	}
	var err500 api.ErrorResponse
	if err := json.Unmarshal(recBroken.Body.Bytes(), &err500); err != nil {
		t.Fatalf("failed to decode 500 error json: %v", err)
	}
	if !strings.Contains(err500.Error, "failed to render widget") {
		t.Errorf("expected 'failed to render widget' in error, got %q", err500.Error)
	}
}

func TestHandler_GetWidgetAsset(t *testing.T) {
	t.Parallel()

	handler, _, _ := setupTestServer(t)

	// Case 1: Valid asset GET
	req := httptest.NewRequest(http.MethodGet, "/widget-types/sensor/assets/icon.svg", nil)
	rec := httptest.NewRecorder()
	handler.GetWidgetAsset(rec, req, "sensor", "icon.svg")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for valid asset, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "<svg>icon</svg>") {
		t.Errorf("unexpected asset body: %s", rec.Body.String())
	}

	// Case 2: Nested sub-asset GET
	reqSub := httptest.NewRequest(http.MethodGet, "/widget-types/sensor/assets/sub/nested.png", nil)
	recSub := httptest.NewRecorder()
	handler.GetWidgetAsset(recSub, reqSub, "sensor", "sub/nested.png")
	if recSub.Code != http.StatusOK {
		t.Fatalf("expected 200 for nested asset, got %d", recSub.Code)
	}
	if recSub.Body.String() != "pngdata" {
		t.Errorf("unexpected nested asset body: %s", recSub.Body.String())
	}

	// Case 3: Path Traversal defense tests (all should return 400 Bad Request)
	traversalPaths := []string{
		"../icon.svg",
		"sub/../../manifest.yaml",
		"/etc/passwd",
		"icon.svg/../..",
		"..",
		".",
		"",
	}
	for _, p := range traversalPaths {
		t.Run("traversal_"+p, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequest(http.MethodGet, "/widget-types/sensor/assets/"+p, nil)
			w := httptest.NewRecorder()
			handler.GetWidgetAsset(w, r, "sensor", p)
			if w.Code != http.StatusBadRequest {
				t.Errorf("expected 400 for traversal path %q, got %d", p, w.Code)
			}
			var errResp api.ErrorResponse
			if err := json.Unmarshal(w.Body.Bytes(), &errResp); err != nil {
				t.Fatalf("failed to decode error json: %v", err)
			}
			if !strings.Contains(errResp.Error, "path traversal detected") {
				t.Errorf("expected traversal error message, got %q", errResp.Error)
			}
		})
	}

	// Case 4: Unknown package returns 404
	reqNoPkg := httptest.NewRequest(http.MethodGet, "/widget-types/unknown/assets/icon.svg", nil)
	recNoPkg := httptest.NewRecorder()
	handler.GetWidgetAsset(recNoPkg, reqNoPkg, "unknown", "icon.svg")
	if recNoPkg.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown package, got %d", recNoPkg.Code)
	}

	// Case 5: Non-existent asset file in valid package returns 404
	reqMissingFile := httptest.NewRequest(http.MethodGet, "/widget-types/sensor/assets/nonexistent.png", nil)
	recMissingFile := httptest.NewRecorder()
	handler.GetWidgetAsset(recMissingFile, reqMissingFile, "sensor", "nonexistent.png")
	if recMissingFile.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for non-existent file, got %d", recMissingFile.Code)
	}

	// Case 6: Directory path requested returns 404
	reqDir := httptest.NewRequest(http.MethodGet, "/widget-types/sensor/assets/sub", nil)
	recDir := httptest.NewRecorder()
	handler.GetWidgetAsset(recDir, reqDir, "sensor", "sub")
	if recDir.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when requesting directory, got %d", recDir.Code)
	}

	// Case 7: OPTIONS and HEAD
	reqOptions := httptest.NewRequest(http.MethodOptions, "/widget-types/sensor/assets/icon.svg", nil)
	recOptions := httptest.NewRecorder()
	handler.GetWidgetAsset(recOptions, reqOptions, "sensor", "icon.svg")
	if recOptions.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for OPTIONS, got %d", recOptions.Code)
	}

	reqPost := httptest.NewRequest(http.MethodPost, "/widget-types/sensor/assets/icon.svg", nil)
	recPost := httptest.NewRecorder()
	handler.GetWidgetAsset(recPost, reqPost, "sensor", "icon.svg")
	if recPost.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for POST, got %d", recPost.Code)
	}

	// Case 8: Package resolver unset returns 500
	noResolverHandler := render.NewHandler(nil, nil)
	recNoRes := httptest.NewRecorder()
	noResolverHandler.GetWidgetAsset(recNoRes, req, "sensor", "icon.svg")
	if recNoRes.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when resolver is unset, got %d", recNoRes.Code)
	}

	// Case 9: Package without assets directory returns 404
	pkgNoAssets := &domain.Package{
		Type:      "no-assets",
		AssetsDir: "",
	}
	resolverNoAssets := &mockResolver{packages: map[string]*domain.Package{"no-assets": pkgNoAssets}}
	handlerNoAssets := render.NewHandler(nil, resolverNoAssets)
	recNoAssets := httptest.NewRecorder()
	handlerNoAssets.GetWidgetAsset(recNoAssets, req, "no-assets", "any.png")
	if recNoAssets.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when package has no assets, got %d", recNoAssets.Code)
	}
}

func TestHandler_ServeStyle(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	customPath := filepath.Join(tmpDir, "custom.css")
	defaultPath := filepath.Join(tmpDir, "hud.css")

	embeddedContent := []byte("/* embedded fallback */")

	// Phase 1: Both files absent -> Serves embedded content
	h := render.NewHandler(
		nil, nil,
		render.WithCustomCSSPath(customPath),
		render.WithDefaultCSSPath(defaultPath),
		render.WithDefaultCSSContent(embeddedContent),
	)

	req := httptest.NewRequest(http.MethodGet, "/style.css", nil)
	rec := httptest.NewRecorder()
	h.ServeStyle(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if rec.Header().Get("Content-Type") != "text/css; charset=utf-8" {
		t.Errorf("expected text/css; charset=utf-8, got %q", rec.Header().Get("Content-Type"))
	}
	if rec.Header().Get("Cache-Control") != "public, max-age=0, must-revalidate" {
		t.Errorf("expected Cache-Control header, got %q", rec.Header().Get("Cache-Control"))
	}
	if rec.Body.String() != string(embeddedContent) {
		t.Errorf("expected embedded fallback content, got %s", rec.Body.String())
	}

	// Phase 2: Default hud.css created -> Serves default hud.css
	if err := os.WriteFile(defaultPath, []byte("/* disk hud.css */"), 0644); err != nil {
		t.Fatalf("failed to write hud.css: %v", err)
	}
	rec2 := httptest.NewRecorder()
	h.ServeStyle(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec2.Code)
	}
	if rec2.Body.String() != "/* disk hud.css */" {
		t.Errorf("expected disk hud.css content, got %s", rec2.Body.String())
	}

	// Phase 3: Custom custom.css created -> Serves custom.css overriding default
	if err := os.WriteFile(customPath, []byte("/* user custom.css */"), 0644); err != nil {
		t.Fatalf("failed to write custom.css: %v", err)
	}
	rec3 := httptest.NewRecorder()
	h.ServeStyle(rec3, req)
	if rec3.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec3.Code)
	}
	if rec3.Body.String() != "/* user custom.css */" {
		t.Errorf("expected user custom.css content, got %s", rec3.Body.String())
	}

	// Phase 4: OPTIONS, HEAD, POST
	reqOptions := httptest.NewRequest(http.MethodOptions, "/style.css", nil)
	recOptions := httptest.NewRecorder()
	h.ServeStyle(recOptions, reqOptions)
	if recOptions.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for OPTIONS, got %d", recOptions.Code)
	}

	reqHead := httptest.NewRequest(http.MethodHead, "/style.css", nil)
	recHead := httptest.NewRecorder()
	h.ServeStyle(recHead, reqHead)
	if recHead.Code != http.StatusOK {
		t.Fatalf("expected 200 for HEAD, got %d", recHead.Code)
	}

	reqPost := httptest.NewRequest(http.MethodPost, "/style.css", nil)
	recPost := httptest.NewRecorder()
	h.ServeStyle(recPost, reqPost)
	if recPost.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for POST, got %d", recPost.Code)
	}
}
