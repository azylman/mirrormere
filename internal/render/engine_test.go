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
	"github.com/azylman/mirrormere/internal/provider"
	"github.com/azylman/mirrormere/internal/render"
	"github.com/azylman/mirrormere/internal/tasks"
	"github.com/azylman/mirrormere/internal/widget"
)

type mockSnapshotProvider struct {
	snapshot *config.Snapshot
	states   map[string]mockState
	status   config.Status
}

type mockState struct {
	data      any
	state     string
	timestamp string
}

func (m *mockSnapshotProvider) CurrentSnapshot() *config.Snapshot {
	return m.snapshot
}

func (m *mockSnapshotProvider) GetWidgetState(widgetID string) (any, string, string, bool) {
	if m.states == nil {
		return nil, "", "", false
	}
	st, ok := m.states[widgetID]
	if !ok {
		return nil, "", "", false
	}
	return st.data, st.state, st.timestamp, true
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

func TestEngine_RenderWidget_CachedTimestampStaleness(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	viewTmpl := `<div id="{{.ID}}"><span class="timestamp">{{.Timestamp}}</span></div>`
	pkg := createTestPackage(t, tmpDir, "sensor", viewTmpl)

	cachedTimeStr := "2026-09-25T11:00:00Z"
	nowTime := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

	snap := &config.Snapshot{
		Config: &config.Config{
			Display: config.DisplayConfig{
				Widgets: []config.WidgetConfig{
					{
						ID:   "cached-sensor",
						Type: "sensor",
					},
					{
						ID:   "uncached-sensor",
						Type: "sensor",
					},
				},
			},
		},
		Layout:   &layout.Layout{Screens: []layout.Screen{}},
		Packages: map[string]*domain.Package{"sensor": pkg},
	}

	provider := &mockSnapshotProvider{
		snapshot: snap,
		states: map[string]mockState{
			"cached-sensor": {
				data:      map[string]any{"val": 42},
				state:     "healthy",
				timestamp: cachedTimeStr,
			},
		},
		status: config.Status{ConfigStatus: config.ConfigStatusOK},
	}
	resolver := &mockResolver{packages: map[string]*domain.Package{"sensor": pkg}}
	engine := render.NewEngine(resolver, provider, render.WithNowFunc(func() time.Time { return nowTime }))

	// 1. Cached widget renders cached timestamp from 1 hour ago, NOT nowTime
	outCached, err := engine.RenderWidget(context.Background(), "cached-sensor")
	if err != nil {
		t.Fatalf("failed to render cached widget: %v", err)
	}
	if !strings.Contains(string(outCached), `<span class="timestamp">2026-09-25T11:00:00Z</span>`) {
		t.Errorf("expected cached timestamp %s, got: %s", cachedTimeStr, outCached)
	}
	if strings.Contains(string(outCached), "2026-09-25T12:00:00Z") {
		t.Errorf("cached widget incorrectly rendered current render time instead of cached timestamp: %s", outCached)
	}

	// 2. Uncached widget renders empty string for .Timestamp
	outUncached, err := engine.RenderWidget(context.Background(), "uncached-sensor")
	if err != nil {
		t.Fatalf("failed to render uncached widget: %v", err)
	}
	if !strings.Contains(string(outUncached), `<span class="timestamp"></span>`) {
		t.Errorf("expected empty timestamp for uncached widget, got: %s", outUncached)
	}
	if strings.Contains(string(outUncached), "2026-09-25T12:00:00Z") {
		t.Errorf("uncached widget incorrectly rendered current render time instead of empty string: %s", outUncached)
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

	// 2. Complex nested structure verifying domain keys preserved and sensitive keys stripped
	cfg := map[string]any{
		"title":        "Safe",
		"author":       "J.R.R. Tolkien",
		"authors":      []any{"Frodo", "Sam"},
		"show_passed":  true,
		"passengers":   4,
		"bypass_cache": true,
		"compass":      "north",
		"max_tokens":   100,
		"token":        "leak1",
		"secret":       "leak2",
		"password":     "leak3",
		"api_key":      "leak4",
		"apikey":       "leak5",
		"auth":         "leak6",
		"auth_token":   "leak7",
		"access_token": "leak8",
		"token_env":    "TOKEN_VAR",
		"my_env":       "MY_VAR",
		"list": []any{
			"simple-string",
			map[string]any{
				"safe_item":   "ok",
				"show_passed": false,
				"author":      "Nested Author",
				"secret":      "hide",
				"nested_env":  "ENV_VAR",
			},
		},
		"nested": map[string]any{
			"public":      "show",
			"bypass_cache": true,
			"secret":      "hide",
			"sub_token_env": "SUB_VAR",
		},
	}

	sanitized := render.SanitizeConfig(cfg)

	// Domain keys preserved
	if sanitized["title"] != "Safe" {
		t.Errorf("expected title preserved, got %v", sanitized["title"])
	}
	if sanitized["author"] != "J.R.R. Tolkien" {
		t.Errorf("expected author preserved, got %v", sanitized["author"])
	}
	if sanitized["show_passed"] != true {
		t.Errorf("expected show_passed preserved, got %v", sanitized["show_passed"])
	}
	if sanitized["passengers"] != 4 {
		t.Errorf("expected passengers preserved, got %v", sanitized["passengers"])
	}
	if sanitized["bypass_cache"] != true {
		t.Errorf("expected bypass_cache preserved, got %v", sanitized["bypass_cache"])
	}
	if sanitized["compass"] != "north" {
		t.Errorf("expected compass preserved, got %v", sanitized["compass"])
	}
	if sanitized["max_tokens"] != 100 {
		t.Errorf("expected max_tokens preserved, got %v", sanitized["max_tokens"])
	}

	// Sensitive keys stripped
	sensitiveKeys := []string{
		"token", "secret", "password", "api_key", "apikey",
		"auth", "auth_token", "access_token", "token_env", "my_env",
	}
	for _, k := range sensitiveKeys {
		if _, exists := sanitized[k]; exists {
			t.Errorf("sensitive key %q leaked in sanitized config", k)
		}
	}

	// Nested list verification
	list, ok := sanitized["list"].([]any)
	if !ok || len(list) != 2 {
		t.Fatalf("expected list of length 2")
	}
	subMap, ok := list[1].(map[string]any)
	if !ok {
		t.Fatalf("expected subMap in list")
	}
	if subMap["safe_item"] != "ok" || subMap["show_passed"] != false || subMap["author"] != "Nested Author" {
		t.Errorf("expected nested domain keys preserved in slice map, got: %v", subMap)
	}
	if _, exists := subMap["secret"]; exists {
		t.Errorf("secret leaked in slice map")
	}
	if _, exists := subMap["nested_env"]; exists {
		t.Errorf("nested_env leaked in slice map")
	}

	// Nested map verification
	nestedMap, ok := sanitized["nested"].(map[string]any)
	if !ok {
		t.Fatalf("expected nested map")
	}
	if nestedMap["public"] != "show" || nestedMap["bypass_cache"] != true {
		t.Errorf("expected nested domain keys preserved in map, got: %v", nestedMap)
	}
	if _, exists := nestedMap["secret"]; exists {
		t.Errorf("secret leaked in nested map")
	}
	if _, exists := nestedMap["sub_token_env"]; exists {
		t.Errorf("sub_token_env leaked in nested map")
	}
}

func TestEngine_RenderWidget_DomainConfigKeysReachTemplate(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	viewTmpl := `<div id="{{.ID}}">` +
		`<span class="author">{{.Config.author}}</span>` +
		`<span class="show-passed">{{.Config.show_passed}}</span>` +
		`<span class="bypass-cache">{{.Config.bypass_cache}}</span>` +
		`<span class="max-tokens">{{.Config.max_tokens}}</span>` +
		`</div>`
	pkg := createTestPackage(t, tmpDir, "book-widget", viewTmpl)

	snap := &config.Snapshot{
		Config: &config.Config{
			Display: config.DisplayConfig{
				Widgets: []config.WidgetConfig{
					{
						ID:   "book-info",
						Type: "book-widget",
						Config: map[string]any{
							"author":       "J.R.R. Tolkien",
							"show_passed":  true,
							"bypass_cache": true,
							"max_tokens":   500,
							"token_env":    "SECRET_VAR_NAME",
							"password":     "raw-secret-leak",
						},
					},
				},
			},
		},
		Layout:   &layout.Layout{Screens: []layout.Screen{}},
		Packages: map[string]*domain.Package{"book-widget": pkg},
	}

	provider := &mockSnapshotProvider{
		snapshot: snap,
		states: map[string]mockState{
			"book-info": {
				data:      map[string]any{},
				state:     "healthy",
				timestamp: "2026-09-25T12:00:00Z",
			},
		},
		status: config.Status{ConfigStatus: config.ConfigStatusOK},
	}
	resolver := &mockResolver{packages: map[string]*domain.Package{"book-widget": pkg}}
	engine := render.NewEngine(resolver, provider)

	out, err := engine.RenderWidget(context.Background(), "book-info")
	if err != nil {
		t.Fatalf("failed to render widget: %v", err)
	}

	outStr := string(out)
	if !strings.Contains(outStr, `<span class="author">J.R.R. Tolkien</span>`) {
		t.Errorf("missing .Config.author in rendered HTML: %s", outStr)
	}
	if !strings.Contains(outStr, `<span class="show-passed">true</span>`) {
		t.Errorf("missing .Config.show_passed in rendered HTML: %s", outStr)
	}
	if !strings.Contains(outStr, `<span class="bypass-cache">true</span>`) {
		t.Errorf("missing .Config.bypass_cache in rendered HTML: %s", outStr)
	}
	if !strings.Contains(outStr, `<span class="max-tokens">500</span>`) {
		t.Errorf("missing .Config.max_tokens in rendered HTML: %s", outStr)
	}
	if strings.Contains(outStr, "SECRET_VAR_NAME") || strings.Contains(outStr, "raw-secret-leak") {
		t.Errorf("sensitive credentials leaked into rendered HTML: %s", outStr)
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

func TestRenderBuiltinWeatherForecastWidget(t *testing.T) {
	t.Parallel()

	repoWidgetsDir := filepath.Join("..", "..", "widgets")
	loader := widget.NewLoader(repoWidgetsDir, t.TempDir())
	pkg, err := loader.LoadPackage("weather-forecast")
	if err != nil {
		t.Fatalf("failed to load weather-forecast package: %v", err)
	}

	snap := &config.Snapshot{
		Config: &config.Config{
			Display: config.DisplayConfig{
				Widgets: []config.WidgetConfig{
					{ID: "w-weather-loaded", Type: "weather-forecast", Dimensions: []int{2, 1}},
					{ID: "w-weather-loading", Type: "weather-forecast", Dimensions: []int{2, 1}},
				},
			},
		},
		Packages: map[string]*domain.Package{"weather-forecast": pkg},
	}

	weatherData := provider.WeatherSnapshot{
		Current: provider.WeatherCurrent{
			Temperature:   72.4,
			FeelsLike:     70.5,
			Humidity:      45,
			WindSpeed:     5.5,
			Units:         provider.WeatherUnits{Temperature: "°F", WindSpeed: "mph"},
			ConditionCode: 1,
			ConditionText: "Mainly Clear",
			Icon:          "weather-partly-cloudy",
		},
		Hourly: []provider.WeatherHourly{
			{Time: "2026-09-26T12:00:00Z", Temp: 72.4, PrecipProb: 0, Icon: "weather-partly-cloudy"},
		},
		Daily: []provider.WeatherDaily{
			{Date: "2026-09-26", TempMax: 75.0, TempMin: 55.0, PrecipProbMax: 10, ConditionText: "Mainly Clear", Icon: "weather-partly-cloudy"},
		},
	}

	p := &mockSnapshotProvider{
		snapshot: snap,
		states: map[string]mockState{
			"w-weather-loaded":  {data: weatherData, state: "healthy", timestamp: "2026-09-26T12:00:00Z"},
			"w-weather-loading": {data: nil, state: "healthy"},
		},
	}

	resolver := &mockResolver{packages: map[string]*domain.Package{"weather-forecast": pkg}}
	engine := render.NewEngine(resolver, p)

	// 1. Render loaded widget
	htmlLoaded, err := engine.RenderWidget(context.Background(), "w-weather-loaded")
	if err != nil {
		t.Fatalf("RenderWidget failed on loaded weather: %v", err)
	}
	if !strings.Contains(string(htmlLoaded), "72°F") && !strings.Contains(string(htmlLoaded), "72") {
		t.Errorf("expected 72 in rendered output, got: %s", htmlLoaded)
	}
	if !strings.Contains(string(htmlLoaded), "Mainly Clear") {
		t.Errorf("expected 'Mainly Clear' in rendered output, got: %s", htmlLoaded)
	}
	if !strings.Contains(string(htmlLoaded), "weather-partly-cloudy") {
		t.Errorf("expected icon token in rendered output, got: %s", htmlLoaded)
	}

	// 2. Render loading widget (data: nil)
	htmlLoading, err := engine.RenderWidget(context.Background(), "w-weather-loading")
	if err != nil {
		t.Fatalf("RenderWidget failed on loading weather: %v", err)
	}
	if !strings.Contains(string(htmlLoading), "Loading weather forecast...") {
		t.Errorf("expected loading message in rendered output, got: %s", htmlLoading)
	}
}

func TestEngine_RenderWidget_CalendarAgendaPackage(t *testing.T) {
	t.Parallel()

	repoWidgetsDir := filepath.Join("..", "..", "widgets")
	loader := widget.NewLoader(repoWidgetsDir, t.TempDir())
	pkg, err := loader.LoadPackage("calendar-agenda")
	if err != nil {
		t.Fatalf("failed to load calendar-agenda package: %v", err)
	}

	snap := &config.Snapshot{
		Config: &config.Config{
			Display: config.DisplayConfig{
				Widgets: []config.WidgetConfig{
					{ID: "w-cal-loaded", Type: "calendar-agenda", Dimensions: []int{4, 2}},
					{ID: "w-cal-empty", Type: "calendar-agenda", Dimensions: []int{4, 2}},
					{ID: "w-cal-loading", Type: "calendar-agenda", Dimensions: []int{4, 2}},
				},
			},
		},
		Packages: map[string]*domain.Package{"calendar-agenda": pkg},
	}

	calData := provider.CalendarSnapshot{
		LastSync:   "2026-09-26T12:00:00Z",
		SyncStatus: "ok",
		Events: []provider.CalendarEvent{
			{
				ID:           "evt_1",
				CalendarName: "Family Calendar",
				Color:        "#3b82f6",
				Title:        "Soccer Practice",
				Start:        "2026-09-26T18:30:00Z",
				End:          "2026-09-26T19:30:00Z",
				AllDay:       false,
				Location:     "Community Park Field 2",
			},
			{
				ID:           "evt_2",
				CalendarName: "Home",
				Color:        "#10b981",
				Title:        "Trash & Recycling",
				Start:        "2026-09-27",
				End:          "2026-09-27",
				AllDay:       true,
			},
		},
	}

	emptyCalData := provider.CalendarSnapshot{
		LastSync:   "2026-09-26T12:00:00Z",
		SyncStatus: "ok",
		Events:     []provider.CalendarEvent{},
	}

	p := &mockSnapshotProvider{
		snapshot: snap,
		states: map[string]mockState{
			"w-cal-loaded":  {data: calData, state: "healthy", timestamp: "2026-09-26T12:00:00Z"},
			"w-cal-empty":   {data: emptyCalData, state: "healthy", timestamp: "2026-09-26T12:00:00Z"},
			"w-cal-loading": {data: nil, state: "healthy"},
		},
	}

	resolver := &mockResolver{packages: map[string]*domain.Package{"calendar-agenda": pkg}}
	engine := render.NewEngine(resolver, p)

	// 1. Render loaded calendar
	htmlLoaded, err := engine.RenderWidget(context.Background(), "w-cal-loaded")
	if err != nil {
		t.Fatalf("RenderWidget failed on loaded calendar: %v", err)
	}
	htmlStr := string(htmlLoaded)
	if !strings.Contains(htmlStr, "Soccer Practice") {
		t.Errorf("expected 'Soccer Practice' in output: %s", htmlStr)
	}
	if !strings.Contains(htmlStr, "Community Park Field 2") {
		t.Errorf("expected 'Community Park Field 2' in output: %s", htmlStr)
	}
	if !strings.Contains(htmlStr, "Family Calendar") {
		t.Errorf("expected 'Family Calendar' in output: %s", htmlStr)
	}
	if !strings.Contains(htmlStr, "Trash") {
		t.Errorf("expected 'Trash' in output: %s", htmlStr)
	}
	if !strings.Contains(htmlStr, "All Day") {
		t.Errorf("expected 'All Day' in output: %s", htmlStr)
	}

	// 2. Render empty calendar
	htmlEmpty, err := engine.RenderWidget(context.Background(), "w-cal-empty")
	if err != nil {
		t.Fatalf("RenderWidget failed on empty calendar: %v", err)
	}
	if !strings.Contains(string(htmlEmpty), "No upcoming events") {
		t.Errorf("expected 'No upcoming events' in empty output: %s", htmlEmpty)
	}

	// 3. Render loading calendar
	htmlLoading, err := engine.RenderWidget(context.Background(), "w-cal-loading")
	if err != nil {
		t.Fatalf("RenderWidget failed on loading calendar: %v", err)
	}
	if !strings.Contains(string(htmlLoading), "Loading calendar agenda...") {
		t.Errorf("expected 'Loading calendar agenda...' in loading output: %s", htmlLoading)
	}
}

func TestEngine_RenderWidget_TasksPackage(t *testing.T) {
	t.Parallel()

	repoWidgetsDir := filepath.Join("..", "..", "widgets")
	loader := widget.NewLoader(repoWidgetsDir, t.TempDir())
	pkg, err := loader.LoadPackage("tasks")
	if err != nil {
		t.Fatalf("failed to load tasks package: %v", err)
	}

	snap := &config.Snapshot{
		Config: &config.Config{
			Display: config.DisplayConfig{
				Widgets: []config.WidgetConfig{
					{ID: "w-tasks-loaded", Type: "tasks", Dimensions: []int{2, 1}},
					{ID: "w-tasks-empty", Type: "tasks", Dimensions: []int{2, 1}},
					{ID: "w-tasks-loading", Type: "tasks", Dimensions: []int{2, 1}},
				},
			},
		},
		Packages: map[string]*domain.Package{"tasks": pkg},
	}

	assignee := "Alex"
	dueDate := "2026-09-26"
	now := time.Now().UTC()

	tasksData := tasks.TasksSnapshot{
		List: tasks.List{
			ID:        "groceries",
			Name:      "Groceries",
			Source:    "local",
			Sections:  []string{"Produce", "Dairy"},
			CreatedAt: now,
			UpdatedAt: now,
		},
		Items: []tasks.ListItem{
			{
				ID:        "i1",
				ListID:    "groceries",
				Title:     "Oat Milk",
				Done:      false,
				Section:   "Dairy",
				Position:  0,
				Assignee:  &assignee,
				DueDate:   &dueDate,
				CreatedAt: now,
				UpdatedAt: now,
			},
			{
				ID:        "i2",
				ListID:    "groceries",
				Title:     "Coffee Beans",
				Done:      true,
				Section:   "Pantry",
				Position:  1,
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
	}

	emptyTasksData := tasks.TasksSnapshot{
		List: tasks.List{
			ID:        "empty-list",
			Name:      "Chores",
			Source:    "local",
			CreatedAt: now,
			UpdatedAt: now,
		},
		Items: []tasks.ListItem{},
	}

	p := &mockSnapshotProvider{
		snapshot: snap,
		states: map[string]mockState{
			"w-tasks-loaded":  {data: tasksData, state: "healthy", timestamp: "2026-09-26T12:00:00Z"},
			"w-tasks-empty":   {data: emptyTasksData, state: "healthy", timestamp: "2026-09-26T12:00:00Z"},
			"w-tasks-loading": {data: nil, state: "healthy"},
		},
	}

	resolver := &mockResolver{packages: map[string]*domain.Package{"tasks": pkg}}
	engine := render.NewEngine(resolver, p)

	// 1. Render loaded tasks
	htmlLoaded, err := engine.RenderWidget(context.Background(), "w-tasks-loaded")
	if err != nil {
		t.Fatalf("RenderWidget failed on loaded tasks: %v", err)
	}
	htmlStr := string(htmlLoaded)
	if !strings.Contains(htmlStr, "Groceries") {
		t.Errorf("expected 'Groceries' in output: %s", htmlStr)
	}
	if !strings.Contains(htmlStr, "Oat Milk") {
		t.Errorf("expected 'Oat Milk' in output: %s", htmlStr)
	}
	if !strings.Contains(htmlStr, "Coffee Beans") {
		t.Errorf("expected 'Coffee Beans' in output: %s", htmlStr)
	}
	if !strings.Contains(htmlStr, "task-item-done") {
		t.Errorf("expected 'task-item-done' in output: %s", htmlStr)
	}
	if !strings.Contains(htmlStr, "@Alex") {
		t.Errorf("expected '@Alex' in output: %s", htmlStr)
	}
	if !strings.Contains(htmlStr, "#Dairy") {
		t.Errorf("expected '#Dairy' in output: %s", htmlStr)
	}

	// 2. Render empty tasks
	htmlEmpty, err := engine.RenderWidget(context.Background(), "w-tasks-empty")
	if err != nil {
		t.Fatalf("RenderWidget failed on empty tasks: %v", err)
	}
	if !strings.Contains(string(htmlEmpty), "All tasks completed") {
		t.Errorf("expected 'All tasks completed' in empty output: %s", htmlEmpty)
	}

	// 3. Render loading tasks
	htmlLoading, err := engine.RenderWidget(context.Background(), "w-tasks-loading")
	if err != nil {
		t.Fatalf("RenderWidget failed on loading tasks: %v", err)
	}
	if !strings.Contains(string(htmlLoading), "Loading checklist...") {
		t.Errorf("expected 'Loading checklist...' in loading output: %s", htmlLoading)
	}
}

