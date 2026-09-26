package render_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/config"
	"github.com/azylman/mirrormere/internal/domain"
	"github.com/azylman/mirrormere/internal/layout"
	"github.com/azylman/mirrormere/internal/render"
)

type mockSnapshotProvider struct {
	snapshot *config.Snapshot
	states   map[string]mockState
	status   config.Status
}

type mockState struct {
	data  any
	state string
}

func (m *mockSnapshotProvider) CurrentSnapshot() *config.Snapshot {
	return m.snapshot
}

func (m *mockSnapshotProvider) GetWidgetState(widgetID string) (any, string, bool) {
	if m.states == nil {
		return nil, "", false
	}
	st, ok := m.states[widgetID]
	if !ok {
		return nil, "", false
	}
	return st.data, st.state, true
}

func (m *mockSnapshotProvider) CurrentStatus() config.Status {
	return m.status
}

type mockResolver struct {
	packages map[string]*domain.Package
}

func (m *mockResolver) LoadPackage(widgetType string) (*domain.Package, error) {
	if m.packages == nil {
		return nil, errors.New("package not found")
	}
	pkg, ok := m.packages[widgetType]
	if !ok {
		return nil, errors.New("package not found")
	}
	return pkg, nil
}

func createTestPackage(t *testing.T, dir, widgetType, viewContent string) *domain.Package {
	t.Helper()
	pkgDir := filepath.Join(dir, widgetType)
	viewsDir := filepath.Join(pkgDir, "views")
	assetsDir := filepath.Join(pkgDir, "assets")
	if err := os.MkdirAll(viewsDir, 0755); err != nil {
		t.Fatalf("failed to create views dir: %v", err)
	}
	if err := os.MkdirAll(assetsDir, 0755); err != nil {
		t.Fatalf("failed to create assets dir: %v", err)
	}

	manifestPath := filepath.Join(pkgDir, "manifest.yaml")
	manifestContent := `id: ` + widgetType + `
version: 1.0.0
name: Test ` + widgetType + `
description: Test widget
author: Tester
entrypoint: views/widget.html
default_dimensions:
  cols: 2
  rows: 1
`
	if err := os.WriteFile(manifestPath, []byte(manifestContent), 0644); err != nil {
		t.Fatalf("failed to write manifest: %v", err)
	}

	viewPath := filepath.Join(viewsDir, "widget.html")
	if err := os.WriteFile(viewPath, []byte(viewContent), 0644); err != nil {
		t.Fatalf("failed to write view: %v", err)
	}

	// Write a sample asset
	if err := os.WriteFile(filepath.Join(assetsDir, "icon.svg"), []byte("<svg>icon</svg>"), 0644); err != nil {
		t.Fatalf("failed to write asset: %v", err)
	}

	return &domain.Package{
		Type:         widgetType,
		Source:       "builtin",
		Dir:          pkgDir,
		ManifestPath: manifestPath,
		ViewPath:     viewPath,
		AssetsDir:    assetsDir,
		Manifest: domain.WidgetManifest{
			Name:              widgetType,
			Version:           "1.0.0",
			Provider:          "http",
			DefaultDimensions: domain.NewDimension(2, 1),
		},
	}
}

func TestEngine_RenderWidget_Success(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	viewTmpl := `<div id="{{.ID}}" class="widget-{{.Type}}" data-theme="{{.Theme}}" data-online="{{.Online}}">
  <span class="state">{{.State}}</span>
  <span class="dim">{{.Dimensions.Cols}}x{{.Dimensions.Rows}}</span>
  <span class="origin">{{index .Origin 0}},{{index .Origin 1}}</span>
  <span class="assets">{{.Assets}}</span>
  <span class="data">{{.Data.temperature}}</span>
  <span class="config-title">{{.Config.title}}</span>
</div>`

	pkg := createTestPackage(t, tmpDir, "weather", viewTmpl)

	snap := &config.Snapshot{
		Config: &config.Config{
			Display: config.DisplayConfig{
				Widgets: []config.WidgetConfig{
					{
						ID:   "living-room-weather",
						Type: "weather",
						Config: map[string]any{
							"title":     "Living Room",
							"token_env": "SECRET_KEY",
							"password":  "secret123",
							"nested": map[string]any{
								"auth_token": "bearer123",
								"safe_label": "Sensors",
							},
						},
					},
				},
			},
		},
		Layout: &layout.Layout{
			Screens: []layout.Screen{
				{
					Index: 0,
					Widgets: []layout.PlacedWidget{
						{
							WidgetID:   "living-room-weather",
							Type:       "weather",
							Origin:     [2]int{2, 1},
							Dimensions: domain.NewDimension(3, 1),
						},
					},
				},
			},
		},
		Packages: map[string]*domain.Package{
			"weather": pkg,
		},
	}

	provider := &mockSnapshotProvider{
		snapshot: snap,
		states: map[string]mockState{
			"living-room-weather": {
				data:  map[string]any{"temperature": 72.5},
				state: "healthy",
			},
		},
		status: config.Status{ConfigStatus: config.ConfigStatusOK},
	}

	resolver := &mockResolver{packages: map[string]*domain.Package{"weather": pkg}}
	fixedTime := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

	engine := render.NewEngine(resolver, provider, render.WithNowFunc(func() time.Time { return fixedTime }))

	out, err := engine.RenderWidget(context.Background(), "living-room-weather")
	if err != nil {
		t.Fatalf("unexpected render error: %v", err)
	}

	htmlStr := string(out)
	if !strings.Contains(htmlStr, `id="living-room-weather"`) {
		t.Errorf("missing widget ID in output: %s", htmlStr)
	}
	if !strings.Contains(htmlStr, `class="widget-weather"`) {
		t.Errorf("missing widget type class: %s", htmlStr)
	}
	if !strings.Contains(htmlStr, `data-theme="dark"`) {
		t.Errorf("missing theme: %s", htmlStr)
	}
	if !strings.Contains(htmlStr, `data-online=`) || !strings.Contains(htmlStr, `true`) {
		t.Errorf("missing online flag: %s", htmlStr)
	}
	if !strings.Contains(htmlStr, `class="state">healthy<`) {
		t.Errorf("missing state: %s", htmlStr)
	}
	if !strings.Contains(htmlStr, `class="dim">3x1<`) {
		t.Errorf("missing placed dimensions: %s", htmlStr)
	}
	if !strings.Contains(htmlStr, `class="origin">2,1<`) {
		t.Errorf("missing placed origin: %s", htmlStr)
	}
	if !strings.Contains(htmlStr, `class="assets">/widget-types/weather/assets<`) {
		t.Errorf("missing assets path: %s", htmlStr)
	}
	if !strings.Contains(htmlStr, `class="data">72.5<`) {
		t.Errorf("missing domain data: %s", htmlStr)
	}
	if !strings.Contains(htmlStr, `class="config-title">Living Room<`) {
		t.Errorf("missing config title: %s", htmlStr)
	}
	if strings.Contains(htmlStr, "SECRET_KEY") || strings.Contains(htmlStr, "secret123") || strings.Contains(htmlStr, "bearer123") {
		t.Errorf("secrets leaked in output: %s", htmlStr)
	}
}

func TestEngine_UnplacedWidget_Fallback(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	viewTmpl := `<div>{{.Dimensions.Cols}}x{{.Dimensions.Rows}} at {{index .Origin 0}},{{index .Origin 1}}</div>`
	pkg := createTestPackage(t, tmpDir, "spacer", viewTmpl)

	snap := &config.Snapshot{
		Config: &config.Config{
			Display: config.DisplayConfig{
				Widgets: []config.WidgetConfig{
					{
						ID:         "offscreen-spacer",
						Type:       "spacer",
						Dimensions: []int{2, 1},
					},
				},
			},
		},
		Layout: &layout.Layout{
			Screens: []layout.Screen{}, // empty screens
		},
		Packages: map[string]*domain.Package{
			"spacer": pkg,
		},
	}

	provider := &mockSnapshotProvider{snapshot: snap}
	resolver := &mockResolver{packages: map[string]*domain.Package{"spacer": pkg}}
	engine := render.NewEngine(resolver, provider)

	out, err := engine.RenderWidget(context.Background(), "offscreen-spacer")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(out), "2x1 at 0,0") {
		t.Errorf("unexpected fallback geometry output: %s", out)
	}
}

func TestEngine_ErrorsAndEdgeCases(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	pkg := createTestPackage(t, tmpDir, "broken", `{{.InvalidField | nonExistentFunc}}`)

	snap := &config.Snapshot{
		Config: &config.Config{
			Display: config.DisplayConfig{
				Widgets: []config.WidgetConfig{
					{ID: "w-broken", Type: "broken"},
					{ID: "w-missing-pkg", Type: "nonexistent"},
				},
			},
		},
	}

	provider := &mockSnapshotProvider{snapshot: snap}
	resolver := &mockResolver{packages: map[string]*domain.Package{"broken": pkg}}
	engine := render.NewEngine(resolver, provider)

	// 1. Empty widget ID
	if _, err := engine.RenderWidget(context.Background(), ""); err == nil {
		t.Error("expected error for empty widget ID")
	}

	// 2. Unconfigured provider
	engineNilProvider := render.NewEngine(resolver, nil)
	if _, err := engineNilProvider.RenderWidget(context.Background(), "w-broken"); err == nil {
		t.Error("expected error when provider is nil")
	}

	// 3. Provider with nil snapshot
	engineNilSnap := render.NewEngine(resolver, &mockSnapshotProvider{snapshot: nil})
	if _, err := engineNilSnap.RenderWidget(context.Background(), "w-broken"); err == nil {
		t.Error("expected error when snapshot is nil")
	}

	// 4. Widget not declared in config
	_, err := engine.RenderWidget(context.Background(), "unknown-widget")
	if err == nil {
		t.Fatal("expected error for unknown widget")
	}
	var notFound render.WidgetNotFoundError
	if !errors.As(err, &notFound) {
		t.Errorf("expected WidgetNotFoundError, got %T: %v", err, err)
	}

	// 5. Package not found
	if _, err := engine.RenderWidget(context.Background(), "w-missing-pkg"); err == nil {
		t.Error("expected error for missing package")
	}

	// 6. Template parse/syntax error
	if _, err := engine.RenderWidget(context.Background(), "w-broken"); err == nil {
		t.Error("expected error for broken template syntax")
	}
}

func TestEngine_CacheAndInvalidation(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	pkg := createTestPackage(t, tmpDir, "cached-w", `<span>v1: {{.ID}}</span>`)

	snap := &config.Snapshot{
		Config: &config.Config{
			Display: config.DisplayConfig{
				Widgets: []config.WidgetConfig{
					{ID: "instance-1", Type: "cached-w"},
				},
			},
		},
		Packages: map[string]*domain.Package{"cached-w": pkg},
	}

	provider := &mockSnapshotProvider{snapshot: snap}
	resolver := &mockResolver{packages: map[string]*domain.Package{"cached-w": pkg}}
	engine := render.NewEngine(resolver, provider)

	// First render: compiles and caches
	out1, err := engine.RenderWidget(context.Background(), "instance-1")
	if err != nil {
		t.Fatalf("first render failed: %v", err)
	}
	if !strings.Contains(string(out1), "v1: instance-1") {
		t.Errorf("unexpected output: %s", out1)
	}

	// Modify template file on disk
	if err := os.WriteFile(pkg.ViewPath, []byte(`<span>v2: {{.ID}}</span>`), 0644); err != nil {
		t.Fatalf("failed to update template: %v", err)
	}

	// Second render without invalidation: serves cached v1
	out2, err := engine.RenderWidget(context.Background(), "instance-1")
	if err != nil {
		t.Fatalf("second render failed: %v", err)
	}
	if !strings.Contains(string(out2), "v1: instance-1") {
		t.Errorf("expected cached v1 output, got %s", out2)
	}

	// Invalidate specific widget type
	engine.Invalidate("cached-w")

	// Third render: picks up v2
	out3, err := engine.RenderWidget(context.Background(), "instance-1")
	if err != nil {
		t.Fatalf("third render failed: %v", err)
	}
	if !strings.Contains(string(out3), "v2: instance-1") {
		t.Errorf("expected updated v2 output after Invalidate, got %s", out3)
	}

	// Test InvalidateAll
	engine.InvalidateAll()
	out4, err := engine.RenderWidget(context.Background(), "instance-1")
	if err != nil {
		t.Fatalf("render after InvalidateAll failed: %v", err)
	}
	if !strings.Contains(string(out4), "v2: instance-1") {
		t.Errorf("expected v2 output, got %s", out4)
	}
}

func TestEngine_OnlineStatus(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	pkg := createTestPackage(t, tmpDir, "status-w", `online: {{.Online}}`)

	snap := &config.Snapshot{
		Config: &config.Config{
			Display: config.DisplayConfig{
				Widgets: []config.WidgetConfig{
					{ID: "w-status", Type: "status-w"},
				},
			},
		},
		Packages: map[string]*domain.Package{"status-w": pkg},
	}

	provider := &mockSnapshotProvider{
		snapshot: snap,
		status:   config.Status{ConfigStatus: config.ConfigStatusError},
	}
	resolver := &mockResolver{packages: map[string]*domain.Package{"status-w": pkg}}
	engine := render.NewEngine(resolver, provider)

	out, err := engine.RenderWidget(context.Background(), "w-status")
	if err != nil {
		t.Fatalf("render error: %v", err)
	}
	if !strings.Contains(string(out), "online: false") {
		t.Errorf("expected online: false when system status is error, got %s", out)
	}
}

func TestSanitizeConfig(t *testing.T) {
	t.Parallel()

	// 1. Nil config
	if res := render.SanitizeConfig(nil); len(res) != 0 {
		t.Errorf("expected empty map for nil config, got %v", res)
	}

	// 2. Complex nested structure with slice and maps
	cfg := map[string]any{
		"title":     "Safe",
		"token":     "leak1",
		"auth_key":  "leak2",
		"api_key":   "leak3",
		"user_pass": "leak4",
		"my_env":    "VAR",
		"list": []any{
			"simple-string",
			map[string]any{
				"safe_item":   "ok",
				"secret_item": "hide",
			},
		},
		"nested": map[string]any{
			"public": "show",
			"secret": "hide",
		},
	}

	sanitized := render.SanitizeConfig(cfg)
	if sanitized["title"] != "Safe" {
		t.Errorf("missing safe title")
	}
	if _, exists := sanitized["token"]; exists {
		t.Errorf("token leaked")
	}
	if _, exists := sanitized["auth_key"]; exists {
		t.Errorf("auth_key leaked")
	}
	if _, exists := sanitized["api_key"]; exists {
		t.Errorf("api_key leaked")
	}
	if _, exists := sanitized["user_pass"]; exists {
		t.Errorf("password leaked")
	}
	if _, exists := sanitized["my_env"]; exists {
		t.Errorf("my_env leaked")
	}

	list, ok := sanitized["list"].([]any)
	if !ok || len(list) != 2 {
		t.Fatalf("expected list of length 2")
	}
	subMap, ok := list[1].(map[string]any)
	if !ok {
		t.Fatalf("expected subMap in list")
	}
	if subMap["safe_item"] != "ok" {
		t.Errorf("expected safe_item preserved")
	}
	if _, exists := subMap["secret_item"]; exists {
		t.Errorf("secret_item leaked in slice map")
	}
}

func TestEngine_MissingTemplateFileAndExecutionError(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	pkg := createTestPackage(t, tmpDir, "missing-view", `test`)
	// Remove view file
	_ = os.Remove(pkg.ViewPath)

	snap := &config.Snapshot{
		Config: &config.Config{
			Display: config.DisplayConfig{
				Widgets: []config.WidgetConfig{
					{ID: "w-missing-view", Type: "missing-view"},
				},
			},
		},
		Packages: map[string]*domain.Package{"missing-view": pkg},
	}
	provider := &mockSnapshotProvider{snapshot: snap}
	resolver := &mockResolver{packages: map[string]*domain.Package{"missing-view": pkg}}
	engine := render.NewEngine(resolver, provider)

	if _, err := engine.RenderWidget(context.Background(), "w-missing-view"); err == nil {
		t.Error("expected error when view file is missing from disk")
	}

	// Test Execution Error: template with bad execution call
	pkgExecErr := createTestPackage(t, tmpDir, "exec-err", `{{.Data.NonExistentField.SubField}}`)
	snap2 := &config.Snapshot{
		Config: &config.Config{
			Display: config.DisplayConfig{
				Widgets: []config.WidgetConfig{
					{ID: "w-exec-err", Type: "exec-err"},
				},
			},
		},
		Packages: map[string]*domain.Package{"exec-err": pkgExecErr},
	}
	provider2 := &mockSnapshotProvider{
		snapshot: snap2,
		states: map[string]mockState{
			"w-exec-err": {data: "not-a-map", state: "healthy"},
		},
	}
	resolver2 := &mockResolver{packages: map[string]*domain.Package{"exec-err": pkgExecErr}}
	engine2 := render.NewEngine(resolver2, provider2)

	if _, err := engine2.RenderWidget(context.Background(), "w-exec-err"); err == nil {
		t.Error("expected error when template execution fails")
	}
}
