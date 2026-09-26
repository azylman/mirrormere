package widget_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azylman/mirrormere/internal/config"
	"github.com/azylman/mirrormere/internal/domain"
	"github.com/azylman/mirrormere/internal/widget"
)

func createPackageFixture(t *testing.T, baseDir, widgetType string, manifestContent string, viewContent string, hasAssets bool) string {
	t.Helper()
	pkgDir := filepath.Join(baseDir, widgetType)
	viewsDir := filepath.Join(pkgDir, "views")
	if err := os.MkdirAll(viewsDir, 0755); err != nil {
		t.Fatalf("failed to create package fixture dirs: %v", err)
	}

	if manifestContent != "" {
		manifestPath := filepath.Join(pkgDir, widget.ManifestFilename)
		if err := os.WriteFile(manifestPath, []byte(manifestContent), 0644); err != nil {
			t.Fatalf("failed to write manifest fixture: %v", err)
		}
	}

	if viewContent != "" {
		viewPath := filepath.Join(pkgDir, widget.ViewFilename)
		if err := os.WriteFile(viewPath, []byte(viewContent), 0644); err != nil {
			t.Fatalf("failed to write view fixture: %v", err)
		}
	}

	if hasAssets {
		assetsDir := filepath.Join(pkgDir, widget.AssetsDirname)
		if err := os.MkdirAll(assetsDir, 0755); err != nil {
			t.Fatalf("failed to create assets dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(assetsDir, "icon.svg"), []byte("<svg></svg>"), 0644); err != nil {
			t.Fatalf("failed to write asset fixture: %v", err)
		}
	}

	return pkgDir
}

const standardCalendarManifest = `
name: Family Calendar Agenda
version: "1.0.0"
provider: calendar-agenda
capabilities:
  - ambient-static
  - touch-interactive
default_dimensions: [4, 2]
supported_dimensions:
  - [4, 2]
  - [2, 1]
refresh:
  interval_seconds: 300
config_schema:
  type: object
  properties:
    view:
      type: string
`

const standardWeatherManifest = `
name: Local Weather
version: "1.0.0"
provider: weather-forecast
default_dimensions: [2, 1]
refresh:
  interval_seconds: 900
`

func TestLoader_DiscoverAndWholePackageOverride(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	builtinDir := filepath.Join(tmpDir, "builtin")
	customDir := filepath.Join(tmpDir, "custom")

	// 1. Built-in packages: calendar-agenda and weather-forecast
	createPackageFixture(t, builtinDir, "calendar-agenda", standardCalendarManifest, "<div class='calendar'></div>", true)
	createPackageFixture(t, builtinDir, "weather-forecast", standardWeatherManifest, "<div class='weather'></div>", false)

	// 2. Custom override for calendar-agenda (with different name, version 2.0.0, no assets)
	customCalendarManifest := `
name: Custom Family Calendar
version: "2.0.0"
provider: calendar-agenda
default_dimensions: [4, 2]
refresh:
  interval_seconds: 600
`
	createPackageFixture(t, customDir, "calendar-agenda", customCalendarManifest, "<div class='custom-calendar'></div>", false)

	// 3. Custom new package: sensor-card (provider: http with response_schema)
	sensorCardManifest := `
name: Sensor Card
version: "1.0.0"
provider: http
default_dimensions: [2, 1]
response_schema:
  type: object
  required:
    - value
  properties:
    value:
      type: number
`
	createPackageFixture(t, customDir, "sensor-card", sensorCardManifest, "<div class='sensor'></div>", true)

	loader := widget.NewLoader(builtinDir, customDir)
	registry, err := loader.Discover()
	if err != nil {
		t.Fatalf("unexpected discover error: %v", err)
	}

	if len(registry) != 3 {
		t.Fatalf("expected 3 packages in registry, got %d", len(registry))
	}

	// Verify calendar-agenda was overridden by custom package
	calPkg := registry["calendar-agenda"]
	if calPkg == nil {
		t.Fatal("missing calendar-agenda package")
	}
	if !calPkg.IsCustom() {
		t.Error("expected calendar-agenda to be custom override")
	}
	if calPkg.Manifest.Version != "2.0.0" {
		t.Errorf("expected version 2.0.0 from custom override, got %s", calPkg.Manifest.Version)
	}
	if calPkg.HasAssets() {
		t.Error("expected custom package without assets to have HasAssets() == false, not inherit builtin assets")
	}

	// Verify weather-forecast resolved from builtin
	weatherPkg := registry["weather-forecast"]
	if weatherPkg == nil {
		t.Fatal("missing weather-forecast package")
	}
	if !weatherPkg.IsBuiltin() {
		t.Error("expected weather-forecast to be builtin")
	}

	// Verify sensor-card resolved from custom
	sensorPkg := registry["sensor-card"]
	if sensorPkg == nil {
		t.Fatal("missing sensor-card package")
	}
	if !sensorPkg.IsCustom() || !sensorPkg.HasAssets() {
		t.Errorf("expected custom sensor-card with assets: %+v", sensorPkg)
	}
}

func TestLoader_IncompletePackageErrors(t *testing.T) {
	t.Parallel()

	// 1. Missing manifest.yaml in custom package
	tmpDir1 := t.TempDir()
	builtinDir1 := filepath.Join(tmpDir1, "builtin")
	customDir1 := filepath.Join(tmpDir1, "custom")
	createPackageFixture(t, customDir1, "incomplete-widget", "", "<div></div>", false)

	loader1 := widget.NewLoader(builtinDir1, customDir1)
	_, err1 := loader1.Discover()
	if err1 == nil || !strings.Contains(err1.Error(), "missing required file manifest.yaml") {
		t.Fatalf("expected missing manifest.yaml error, got %v", err1)
	}

	// 2. Missing views/widget.html in custom package
	tmpDir2 := t.TempDir()
	builtinDir2 := filepath.Join(tmpDir2, "builtin")
	customDir2 := filepath.Join(tmpDir2, "custom")
	createPackageFixture(t, customDir2, "incomplete-view", standardWeatherManifest, "", false)

	loader2 := widget.NewLoader(builtinDir2, customDir2)
	_, err2 := loader2.Discover()
	if err2 == nil || !strings.Contains(err2.Error(), "missing required file manifest.yaml (or views/widget.html)") {
		t.Fatalf("expected missing views/widget.html error, got %v", err2)
	}
}

func TestLoader_ManifestValidationErrors(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	builtinDir := filepath.Join(tmpDir, "builtin")
	customDir := filepath.Join(tmpDir, "custom")

	// Invalid YAML syntax
	createPackageFixture(t, builtinDir, "bad-yaml", "name: [unclosed", "<div></div>", false)
	loader := widget.NewLoader(builtinDir, customDir)
	_, err := loader.Discover()
	if err == nil || !strings.Contains(err.Error(), "manifest syntax error") {
		t.Fatalf("expected manifest syntax error, got %v", err)
	}

	// Disallowed default keyword in config_schema
	tmpDir2 := t.TempDir()
	b2 := filepath.Join(tmpDir2, "builtin")
	c2 := filepath.Join(tmpDir2, "custom")
	defaultManifest := `
name: Default Test
version: "1.0.0"
provider: test
default_dimensions: [2, 1]
config_schema:
  type: object
  properties:
    theme:
      type: string
      default: dark
`
	createPackageFixture(t, b2, "has-default", defaultManifest, "<div></div>", false)
	loader2 := widget.NewLoader(b2, c2)
	_, err2 := loader2.Discover()
	if err2 == nil || !strings.Contains(err2.Error(), "declares disallowed keyword 'default'") {
		t.Fatalf("expected disallowed default error, got %v", err2)
	}
}

func TestLoader_LoadPackage(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	builtinDir := filepath.Join(tmpDir, "builtin")
	customDir := filepath.Join(tmpDir, "custom")

	createPackageFixture(t, builtinDir, "calendar-agenda", standardCalendarManifest, "<div></div>", false)
	createPackageFixture(t, customDir, "custom-widget", standardWeatherManifest, "<div></div>", false)

	loader := widget.NewLoader(builtinDir, customDir)

	// Load from custom
	pkgCustom, err := loader.LoadPackage("custom-widget")
	if err != nil {
		t.Fatalf("unexpected error loading custom widget: %v", err)
	}
	if !pkgCustom.IsCustom() {
		t.Error("expected package to be custom")
	}

	// Load from builtin
	pkgBuiltin, err := loader.LoadPackage("calendar-agenda")
	if err != nil {
		t.Fatalf("unexpected error loading builtin widget: %v", err)
	}
	if !pkgBuiltin.IsBuiltin() {
		t.Error("expected package to be builtin")
	}

	// Load non-existent
	_, err = loader.LoadPackage("non-existent")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found error, got %v", err)
	}
}

func TestLoader_ApplyDefaults(t *testing.T) {
	t.Parallel()

	registry := map[string]*domain.Package{
		"calendar-agenda": {
			Type: "calendar-agenda",
			Manifest: domain.WidgetManifest{
				Name:              "Family Calendar",
				DefaultDimensions: domain.NewDimension(4, 2),
				Refresh:           domain.ManifestRefresh{IntervalSeconds: 300},
			},
		},
		"weather-forecast": {
			Type: "weather-forecast",
			Manifest: domain.WidgetManifest{
				Name:              "Weather Forecast",
				DefaultDimensions: domain.NewDimension(2, 1),
				Refresh:           domain.ManifestRefresh{IntervalSeconds: 900},
			},
		},
	}

	loader := widget.NewLoader("/app/widgets", "/config/widgets")

	// 1. Widget with omitted dimensions and refresh
	customInterval := 120
	cfg := &config.Config{
		Display: config.DisplayConfig{
			Widgets: []config.WidgetConfig{
				{
					ID:   "w1",
					Type: "calendar-agenda",
					// dimensions omitted
					// refresh omitted
				},
				{
					ID:                     "w2",
					Type:                   "weather-forecast",
					Dimensions:             []int{3, 2}, // explicitly overridden
					RefreshIntervalSeconds: &customInterval,
				},
			},
		},
	}

	if err := loader.ApplyDefaults(cfg, registry); err != nil {
		t.Fatalf("unexpected error applying defaults: %v", err)
	}

	// Assert w1 took default dimensions [4, 2] and refresh 300
	w1 := cfg.Display.Widgets[0]
	if len(w1.Dimensions) != 2 || w1.Dimensions[0] != 4 || w1.Dimensions[1] != 2 {
		t.Errorf("expected w1 dimensions [4, 2], got %v", w1.Dimensions)
	}
	if w1.RefreshIntervalSeconds == nil || *w1.RefreshIntervalSeconds != 300 {
		t.Errorf("expected w1 refresh 300, got %v", w1.RefreshIntervalSeconds)
	}

	// Assert w2 retained explicit dimensions [3, 2] and refresh 120
	w2 := cfg.Display.Widgets[1]
	if len(w2.Dimensions) != 2 || w2.Dimensions[0] != 3 || w2.Dimensions[1] != 2 {
		t.Errorf("expected w2 dimensions [3, 2], got %v", w2.Dimensions)
	}
	if w2.RefreshIntervalSeconds == nil || *w2.RefreshIntervalSeconds != 120 {
		t.Errorf("expected w2 refresh 120, got %v", w2.RefreshIntervalSeconds)
	}

	// Unknown widget type error
	badCfg := &config.Config{
		Display: config.DisplayConfig{
			Widgets: []config.WidgetConfig{
				{ID: "w-unknown", Type: "unknown-type"},
			},
		},
	}
	if err := loader.ApplyDefaults(badCfg, registry); err == nil || !strings.Contains(err.Error(), "unknown widget type") {
		t.Fatalf("expected unknown widget type error, got %v", err)
	}

	// Nil config error
	if err := loader.ApplyDefaults(nil, registry); err == nil {
		t.Fatal("expected error for nil config, got nil")
	}

	// Missing valid default_dimensions in manifest
	invalidDimRegistry := map[string]*domain.Package{
		"corrupt-widget": {
			Type: "corrupt-widget",
			Manifest: domain.WidgetManifest{
				Name:              "Corrupt Widget",
				DefaultDimensions: domain.NewDimension(0, 0), // invalid
			},
		},
	}
	corruptCfg := &config.Config{
		Display: config.DisplayConfig{
			Widgets: []config.WidgetConfig{
				{ID: "w-corrupt", Type: "corrupt-widget"},
			},
		},
	}
	if err := loader.ApplyDefaults(corruptCfg, invalidDimRegistry); err == nil || !strings.Contains(err.Error(), "has no valid default_dimensions") {
		t.Fatalf("expected invalid default_dimensions error, got %v", err)
	}
}

func TestCheckPackageCompleteness(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	// 1. Complete package
	completeDir := createPackageFixture(t, tmpDir, "complete", "name: test", "<div></div>", false)
	if !widget.CheckPackageCompleteness(completeDir) {
		t.Error("expected package to be complete")
	}

	// 2. Missing manifest
	noManifestDir := createPackageFixture(t, tmpDir, "no-manifest", "", "<div></div>", false)
	if widget.CheckPackageCompleteness(noManifestDir) {
		t.Error("expected complete to be false when manifest missing")
	}

	// 3. Missing view
	noViewDir := createPackageFixture(t, tmpDir, "no-view", "name: test", "", false)
	if widget.CheckPackageCompleteness(noViewDir) {
		t.Error("expected complete to be false when view missing")
	}
}

func TestLoader_ScanDirEdgeCases(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	builtinDir := filepath.Join(tmpDir, "builtin")
	customDir := filepath.Join(tmpDir, "custom")
	if err := os.MkdirAll(builtinDir, 0755); err != nil {
		t.Fatalf("failed to create builtinDir: %v", err)
	}
	if err := os.MkdirAll(customDir, 0755); err != nil {
		t.Fatalf("failed to create customDir: %v", err)
	}

	// 1. Non-directory file in builtinDir (e.g. README.md) should be skipped cleanly
	readmePath := filepath.Join(builtinDir, "README.md")
	if err := os.WriteFile(readmePath, []byte("# Widgets"), 0644); err != nil {
		t.Fatalf("failed to write readme: %v", err)
	}

	createPackageFixture(t, builtinDir, "calendar-agenda", standardCalendarManifest, "<div></div>", false)

	loader := widget.NewLoader(builtinDir, customDir)
	registry, err := loader.Discover()
	if err != nil {
		t.Fatalf("unexpected error with non-directory file present: %v", err)
	}
	if len(registry) != 1 || registry["calendar-agenda"] == nil {
		t.Errorf("expected only calendar-agenda in registry, got %d entries", len(registry))
	}

	// 2. baseDir is a file instead of a directory -> scanDir returns error
	filePath := filepath.Join(tmpDir, "some-file.txt")
	if err := os.WriteFile(filePath, []byte("content"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	badLoader := widget.NewLoader(filePath, customDir)
	if _, err := badLoader.Discover(); err == nil || !strings.Contains(err.Error(), "failed to read widget directory") {
		t.Fatalf("expected error reading widget directory from file path, got %v", err)
	}
}

func TestDefaultLoader(t *testing.T) {
	t.Parallel()

	loader := widget.DefaultLoader()
	if loader == nil {
		t.Fatal("expected DefaultLoader to return non-nil instance")
	}
}
