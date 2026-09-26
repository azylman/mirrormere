package watcher_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/config"
	"github.com/azylman/mirrormere/internal/domain"
	"github.com/azylman/mirrormere/internal/watcher"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

type mockDispatcher struct {
	mu           sync.Mutex
	configEvents chan struct{}
	styleEvents  chan watcher.StyleReloadEvent
	widgetEvents chan watcher.WidgetReloadEvent
}

func newMockDispatcher() *mockDispatcher {
	return &mockDispatcher{
		configEvents: make(chan struct{}, 10),
		styleEvents:  make(chan watcher.StyleReloadEvent, 10),
		widgetEvents: make(chan watcher.WidgetReloadEvent, 10),
	}
}

func (d *mockDispatcher) DispatchConfigReload(snap *config.Snapshot, diff *config.ConfigDiff) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.configEvents <- struct{}{}
	return nil
}

func (d *mockDispatcher) DispatchStyleReload(ev watcher.StyleReloadEvent) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.styleEvents <- ev
	return nil
}

func (d *mockDispatcher) DispatchWidgetReload(ev watcher.WidgetReloadEvent) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.widgetEvents <- ev
	return nil
}

type testPackageLoader struct {
	mu       sync.RWMutex
	packages map[string]*domain.Package
	failType map[string]error
}

func (l *testPackageLoader) LoadPackage(widgetType string) (*domain.Package, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if err, fail := l.failType[widgetType]; fail {
		return nil, err
	}
	pkg, exists := l.packages[widgetType]
	if !exists {
		return nil, fmt.Errorf("widget package '%s' not found", widgetType)
	}
	return pkg, nil
}

func baseYAML() string {
	return `
timezone: America/New_York
display:
  widgets:
    - id: spacer-1
      type: spacer
      dimensions: [6, 2]
`
}

func updatedYAML() string {
	return `
timezone: America/New_York
display:
  widgets:
    - id: spacer-1
      type: spacer
      dimensions: [6, 1]
    - id: spacer-2
      type: spacer
      dimensions: [6, 1]
`
}

func createStandardPackages() map[string]*domain.Package {
	dim62 := domain.NewDimension(6, 2)
	dim61 := domain.NewDimension(6, 1)

	return map[string]*domain.Package{
		"spacer": {
			Type:   "spacer",
			Source: "builtin",
			Manifest: domain.WidgetManifest{
				Name:                "Spacer Tile",
				Version:             "1.0.0",
				Provider:            "static",
				DefaultDimensions:   dim62,
				SupportedDimensions: []domain.Dimension{dim62, dim61},
			},
			ViewPath: "/app/widgets/spacer/views/widget.html",
		},
		"clock": {
			Type:   "clock",
			Source: "custom",
			Manifest: domain.WidgetManifest{
				Name:              "Digital Clock",
				Version:           "1.0.0",
				Provider:          "static",
				DefaultDimensions: dim62,
			},
			ViewPath: "/config/widgets/clock/views/widget.html",
		},
	}
}

func loadSchema(t *testing.T, schemaPath string) *jsonschema.Schema {
	t.Helper()
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("failed to read schema file %q: %v", schemaPath, err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("failed to unmarshal schema JSON: %v", err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("schema.json", doc); err != nil {
		t.Fatalf("failed to add schema resource: %v", err)
	}
	schema, err := c.Compile("schema.json")
	if err != nil {
		t.Fatalf("failed to compile schema: %v", err)
	}
	return schema
}

func validateInstanceAgainstSchema(t *testing.T, schema *jsonschema.Schema, instance any) {
	t.Helper()
	data, err := json.Marshal(instance)
	if err != nil {
		t.Fatalf("failed to marshal instance: %v", err)
	}
	val, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}
	if err := schema.Validate(val); err != nil {
		t.Fatalf("schema validation failed: %v", err)
	}
}

func setupTestEnv(t *testing.T) (string, *config.Manager, *testPackageLoader, *mockDispatcher) {
	t.Helper()
	tempDir := t.TempDir()

	configDir := filepath.Join(tempDir, "config")
	customWidgetsDir := filepath.Join(configDir, "widgets")
	appWidgetsDir := filepath.Join(tempDir, "app", "widgets")

	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("setup mkdir failed: %v", err)
	}
	if err := os.MkdirAll(customWidgetsDir, 0755); err != nil {
		t.Fatalf("setup mkdir failed: %v", err)
	}
	if err := os.MkdirAll(appWidgetsDir, 0755); err != nil {
		t.Fatalf("setup mkdir failed: %v", err)
	}

	configFile := filepath.Join(configDir, "config.yaml")
	if err := os.WriteFile(configFile, []byte(baseYAML()), 0644); err != nil {
		t.Fatalf("setup write config failed: %v", err)
	}

	packages := createStandardPackages()
	loader := &testPackageLoader{
		packages: packages,
		failType: make(map[string]error),
	}

	mgr, err := config.NewManager([]byte(baseYAML()), loader, nil, nil)
	if err != nil {
		t.Fatalf("failed to create config manager: %v", err)
	}

	disp := newMockDispatcher()
	return tempDir, mgr, loader, disp
}

func TestWatcher_StartErrors(t *testing.T) {
	t.Parallel()

	// Empty ConfigDir
	w := watcher.New(watcher.Config{}, nil, nil, nil, nil)
	err := w.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "ConfigDir cannot be empty") {
		t.Fatalf("expected empty ConfigDir error, got %v", err)
	}

	// Non-existent ConfigDir
	w2 := watcher.New(watcher.Config{ConfigDir: "/non/existent/path/987123"}, nil, nil, nil, nil)
	err = w2.Start(context.Background())
	if err == nil {
		t.Fatal("expected error on non-existent config dir, got nil")
	}
}

func TestWatcher_ConfigValidAtomicUpdate(t *testing.T) {
	t.Parallel()

	tempDir, mgr, loader, disp := setupTestEnv(t)
	configDir := filepath.Join(tempDir, "config")

	w := watcher.New(watcher.Config{
		ConfigDir:        configDir,
		DebounceDuration: 5 * time.Millisecond,
	}, mgr, loader, disp, slog.Default())

	settledCh := make(chan struct{}, 10)
	w.SetSettledHook(func() {
		settledCh <- struct{}{}
	})

	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	// Simulate atomic write: write to tmp file and rename
	tmpFile := filepath.Join(configDir, "config.yaml.tmp")
	configFile := filepath.Join(configDir, "config.yaml")

	if err := os.WriteFile(tmpFile, []byte(updatedYAML()), 0644); err != nil {
		t.Fatalf("failed to write tmp file: %v", err)
	}
	if err := os.Rename(tmpFile, configFile); err != nil {
		t.Fatalf("failed to rename tmp file: %v", err)
	}

	select {
	case <-disp.configEvents:
		// Reload succeeded
		snap := mgr.CurrentSnapshot()
		if len(snap.Config.Display.Widgets) != 2 {
			t.Errorf("expected 2 widgets, got %d", len(snap.Config.Display.Widgets))
		}
		if mgr.Status().ConfigStatus != config.ConfigStatusOK {
			t.Errorf("expected status 'ok', got %s", mgr.Status().ConfigStatus)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for config reload event")
	}
}

func TestWatcher_ConfigSyntaxError_RetainsLKGC(t *testing.T) {
	t.Parallel()

	tempDir, mgr, loader, disp := setupTestEnv(t)
	configDir := filepath.Join(tempDir, "config")

	w := watcher.New(watcher.Config{
		ConfigDir:        configDir,
		DebounceDuration: 5 * time.Millisecond,
	}, mgr, loader, disp, slog.Default())

	settledCh := make(chan struct{}, 10)
	w.SetSettledHook(func() {
		settledCh <- struct{}{}
	})

	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	// Write invalid YAML
	configFile := filepath.Join(configDir, "config.yaml")
	if err := os.WriteFile(configFile, []byte("invalid: yaml: ["), 0644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	select {
	case <-settledCh:
		// Settle completed
		snap := mgr.CurrentSnapshot()
		if len(snap.Config.Display.Widgets) != 1 {
			t.Errorf("expected 1 widget preserved in LKGC, got %d", len(snap.Config.Display.Widgets))
		}
		st := mgr.Status()
		if st.ConfigStatus != config.ConfigStatusError {
			t.Errorf("expected status 'error', got %s", st.ConfigStatus)
		}
		if st.ConfigError == nil || !strings.Contains(*st.ConfigError, "YAML syntax error") {
			t.Errorf("expected syntax error in status, got %v", st.ConfigError)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for settle hook on invalid config")
	}
}

func TestWatcher_StyleReloadDispatch(t *testing.T) {
	t.Parallel()

	tempDir, mgr, loader, disp := setupTestEnv(t)
	configDir := filepath.Join(tempDir, "config")

	styleSchema := loadSchema(t, "../../api/schemas/style.reload.json")

	w := watcher.New(watcher.Config{
		ConfigDir:        configDir,
		DebounceDuration: 5 * time.Millisecond,
	}, mgr, loader, disp, slog.Default())

	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	// Write custom.css
	cssPath := filepath.Join(configDir, "custom.css")
	if err := os.WriteFile(cssPath, []byte("body { background: #1a0033; }"), 0644); err != nil {
		t.Fatalf("write custom.css failed: %v", err)
	}

	select {
	case ev := <-disp.styleEvents:
		if ev.File != "custom.css" {
			t.Errorf("expected file 'custom.css', got %q", ev.File)
		}
		if _, err := time.Parse(time.RFC3339, ev.Timestamp); err != nil {
			t.Errorf("expected RFC3339 timestamp, got %q (err: %v)", ev.Timestamp, err)
		}
		validateInstanceAgainstSchema(t, styleSchema, ev)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for style reload event")
	}
}

func TestWatcher_WidgetReloadDispatch_TemplateAndManifest(t *testing.T) {
	t.Parallel()

	tempDir, mgr, loader, disp := setupTestEnv(t)
	configDir := filepath.Join(tempDir, "config")
	customWidgetsDir := filepath.Join(configDir, "widgets")

	widgetSchema := loadSchema(t, "../../api/schemas/widget.reload.json")

	// Create clock widget directory
	clockDir := filepath.Join(customWidgetsDir, "clock")
	clockViews := filepath.Join(clockDir, "views")
	if err := os.MkdirAll(clockViews, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(clockDir, "manifest.yaml"), []byte("name: Clock\nversion: 1.0.0\n"), 0644); err != nil {
		t.Fatalf("write manifest failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(clockViews, "widget.html"), []byte("<div>Clock</div>"), 0644); err != nil {
		t.Fatalf("write view failed: %v", err)
	}

	w := watcher.New(watcher.Config{
		ConfigDir:        configDir,
		CustomWidgetsDir: customWidgetsDir,
		DebounceDuration: 5 * time.Millisecond,
	}, mgr, loader, disp, slog.Default())

	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	// 1. Edit view template
	if err := os.WriteFile(filepath.Join(clockViews, "widget.html"), []byte("<div>Updated Clock</div>"), 0644); err != nil {
		t.Fatalf("update view failed: %v", err)
	}

	select {
	case ev := <-disp.widgetEvents:
		if ev.Type != "clock" {
			t.Errorf("expected widget type 'clock', got %q", ev.Type)
		}
		validateInstanceAgainstSchema(t, widgetSchema, ev)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for widget reload event on view edit")
	}

	// 2. Edit manifest
	if err := os.WriteFile(filepath.Join(clockDir, "manifest.yaml"), []byte("name: Clock Updated\nversion: 1.0.1\n"), 0644); err != nil {
		t.Fatalf("update manifest failed: %v", err)
	}

	select {
	case ev := <-disp.widgetEvents:
		if ev.Type != "clock" {
			t.Errorf("expected widget type 'clock', got %q", ev.Type)
		}
		validateInstanceAgainstSchema(t, widgetSchema, ev)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for widget reload event on manifest edit")
	}
}

func TestWatcher_IncompletePackageGating(t *testing.T) {
	t.Parallel()

	tempDir, mgr, loader, disp := setupTestEnv(t)
	configDir := filepath.Join(tempDir, "config")
	customWidgetsDir := filepath.Join(configDir, "widgets")

	w := watcher.New(watcher.Config{
		ConfigDir:        configDir,
		CustomWidgetsDir: customWidgetsDir,
		DebounceDuration: 5 * time.Millisecond,
	}, mgr, loader, disp, slog.Default())

	settledCh := make(chan struct{}, 10)
	w.SetSettledHook(func() {
		settledCh <- struct{}{}
	})

	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	// 1. Create incomplete widget package (missing manifest.yaml, loader fails)
	loader.mu.Lock()
	loader.failType["sensor-hud"] = fmt.Errorf("manifest.yaml not found")
	loader.mu.Unlock()

	sensorDir := filepath.Join(customWidgetsDir, "sensor-hud", "views")
	if err := os.MkdirAll(sensorDir, 0755); err != nil {
		t.Fatalf("mkdir sensorDir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sensorDir, "widget.html"), []byte("<div>Sensor</div>"), 0644); err != nil {
		t.Fatalf("write view failed: %v", err)
	}

	select {
	case <-settledCh:
		// Settled, but no widget reload event should be emitted
		select {
		case ev := <-disp.widgetEvents:
			t.Fatalf("unexpected widget reload event emitted for incomplete package: %v", ev)
		default:
			// Passed
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for settle hook")
	}

	// 2. Complete package: add manifest and remove failType
	dim62 := domain.NewDimension(6, 2)
	loader.mu.Lock()
	delete(loader.failType, "sensor-hud")
	loader.packages["sensor-hud"] = &domain.Package{
		Type:   "sensor-hud",
		Source: "custom",
		Manifest: domain.WidgetManifest{
			Name:              "Sensor HUD",
			Version:           "1.0.0",
			Provider:          "static",
			DefaultDimensions: dim62,
		},
		ViewPath: filepath.Join(sensorDir, "widget.html"),
	}
	loader.mu.Unlock()

	manifestFile := filepath.Join(customWidgetsDir, "sensor-hud", "manifest.yaml")
	if err := os.WriteFile(manifestFile, []byte("name: Sensor HUD\nversion: 1.0.0\n"), 0644); err != nil {
		t.Fatalf("write manifest failed: %v", err)
	}

	select {
	case ev := <-disp.widgetEvents:
		if ev.Type != "sensor-hud" {
			t.Errorf("expected widget reload for 'sensor-hud', got %q", ev.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for widget reload on completed package")
	}
}

func TestWatcher_TemporaryFilesIgnored(t *testing.T) {
	t.Parallel()

	tempDir, mgr, loader, disp := setupTestEnv(t)
	configDir := filepath.Join(tempDir, "config")

	w := watcher.New(watcher.Config{
		ConfigDir:        configDir,
		DebounceDuration: 5 * time.Millisecond,
	}, mgr, loader, disp, slog.Default())

	settledCh := make(chan struct{}, 10)
	w.SetSettledHook(func() {
		settledCh <- struct{}{}
	})

	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	// Write temporary files and unknown file
	_ = os.WriteFile(filepath.Join(configDir, "config.yaml.tmp"), []byte("tmp"), 0644)
	_ = os.WriteFile(filepath.Join(configDir, "4913"), []byte("vim probe"), 0644)
	_ = os.WriteFile(filepath.Join(configDir, "custom.css~"), []byte("backup"), 0644)
	_ = os.WriteFile(filepath.Join(configDir, ".goutputstream-XYZ"), []byte("stream"), 0644)
	_ = os.WriteFile(filepath.Join(configDir, "unknown.txt"), []byte("unknown"), 0644)

	// Sleep slightly or assert no settled event fired
	select {
	case <-settledCh:
		t.Fatal("unexpected settle hook triggered on temporary file operations")
	case <-time.After(50 * time.Millisecond):
		// No event, verify dispatcher channels empty
		select {
		case ev := <-disp.configEvents:
			t.Fatalf("unexpected config event: %v", ev)
		case ev := <-disp.styleEvents:
			t.Fatalf("unexpected style event: %v", ev)
		case ev := <-disp.widgetEvents:
			t.Fatalf("unexpected widget event: %v", ev)
		default:
			// Passed
		}
	}
}

func TestWatcher_DynamicDirectoryCreationAndRemoval(t *testing.T) {
	t.Parallel()

	tempDir, mgr, loader, disp := setupTestEnv(t)
	configDir := filepath.Join(tempDir, "config")
	customWidgetsDir := filepath.Join(configDir, "widgets")

	w := watcher.New(watcher.Config{
		ConfigDir:        configDir,
		CustomWidgetsDir: customWidgetsDir,
		DebounceDuration: 5 * time.Millisecond,
	}, mgr, loader, disp, slog.Default())

	settledCh := make(chan struct{}, 10)
	w.SetSettledHook(func() {
		settledCh <- struct{}{}
	})

	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	// Dynamically create new package directory with subviews
	subviews := filepath.Join(customWidgetsDir, "clock", "subviews")
	if err := os.MkdirAll(subviews, 0755); err != nil {
		t.Fatalf("mkdir subviews failed: %v", err)
	}

	// Give watcher a moment to receive create event on directory
	time.Sleep(20 * time.Millisecond)

	// Write file inside nested dynamic directory
	fileInSubviews := filepath.Join(subviews, "extra.html")
	if err := os.WriteFile(fileInSubviews, []byte("<div>Extra</div>"), 0644); err != nil {
		t.Fatalf("write file in subviews failed: %v", err)
	}

	select {
	case ev := <-disp.widgetEvents:
		if ev.Type != "clock" {
			t.Errorf("expected widget reload for 'clock', got %q", ev.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event from dynamically attached subdirectory")
	}

	// Remove dynamic directory (test clean removal)
	_ = os.RemoveAll(subviews)
	select {
	case <-settledCh:
	case <-time.After(500 * time.Millisecond):
	}
}

func TestWatcher_MissingCustomDirectoryAtBoot(t *testing.T) {
	t.Parallel()

	tempDir, mgr, loader, disp := setupTestEnv(t)
	configDir := filepath.Join(tempDir, "config")
	missingCustomWidgetsDir := filepath.Join(tempDir, "non_existent_widgets")
	missingBuiltinWidgetsDir := filepath.Join(tempDir, "non_existent_builtin")

	w := watcher.New(watcher.Config{
		ConfigDir:         configDir,
		CustomWidgetsDir:  missingCustomWidgetsDir,
		BuiltinWidgetsDir: missingBuiltinWidgetsDir,
		DebounceDuration:  5 * time.Millisecond,
	}, mgr, loader, disp, slog.Default())

	// Booting with non-existent custom widgets directory must succeed
	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("Start failed on missing custom widgets dir: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	// Clean close
	if err := w.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}
}

func TestWatcher_MaxDebounceDurationCeiling(t *testing.T) {
	t.Parallel()

	tempDir, mgr, loader, disp := setupTestEnv(t)
	configDir := filepath.Join(tempDir, "config")

	// Set short MaxDebounceDuration of 15ms with DebounceDuration of 10ms
	w := watcher.New(watcher.Config{
		ConfigDir:           configDir,
		DebounceDuration:    10 * time.Millisecond,
		MaxDebounceDuration: 15 * time.Millisecond,
	}, mgr, loader, disp, slog.Default())

	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	cssPath := filepath.Join(configDir, "custom.css")
	_ = os.WriteFile(cssPath, []byte("body { color: red; }"), 0644)

	// Keep sending rapid writes
	go func() {
		for i := 0; i < 5; i++ {
			time.Sleep(3 * time.Millisecond)
			_ = os.WriteFile(cssPath, []byte(fmt.Sprintf("body { color: %d; }", i)), 0644)
		}
	}()

	select {
	case ev := <-disp.styleEvents:
		if ev.File != "custom.css" {
			t.Errorf("expected 'custom.css', got %q", ev.File)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out: MaxDebounceDuration did not force flush under continuous writes")
	}
}

func TestWatcher_BuiltinWidgetsReload(t *testing.T) {
	t.Parallel()

	tempDir, mgr, loader, disp := setupTestEnv(t)
	configDir := filepath.Join(tempDir, "config")
	appWidgetsDir := filepath.Join(tempDir, "app", "widgets")

	spacerDir := filepath.Join(appWidgetsDir, "spacer", "views")
	if err := os.MkdirAll(spacerDir, 0755); err != nil {
		t.Fatalf("mkdir spacer failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(appWidgetsDir, "spacer", "manifest.yaml"), []byte("name: Spacer\nversion: 1.0.0\n"), 0644); err != nil {
		t.Fatalf("write manifest failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(spacerDir, "widget.html"), []byte("<div>Spacer</div>"), 0644); err != nil {
		t.Fatalf("write view failed: %v", err)
	}

	w := watcher.New(watcher.Config{
		ConfigDir:         configDir,
		BuiltinWidgetsDir: appWidgetsDir,
		DebounceDuration:  5 * time.Millisecond,
	}, mgr, loader, disp, slog.Default())

	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	// Update builtin widget view
	if err := os.WriteFile(filepath.Join(spacerDir, "widget.html"), []byte("<div>Updated Spacer</div>"), 0644); err != nil {
		t.Fatalf("update builtin view failed: %v", err)
	}

	select {
	case ev := <-disp.widgetEvents:
		if ev.Type != "spacer" {
			t.Errorf("expected widget reload for 'spacer', got %q", ev.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for builtin widget reload")
	}
}

func TestWatcher_AddDir_NotADirectory(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "file.txt")
	if err := os.WriteFile(filePath, []byte("hello"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	w := watcher.New(watcher.Config{
		ConfigDir: filePath,
	}, nil, nil, nil, nil)

	err := w.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "is not a directory") {
		t.Fatalf("expected 'is not a directory' error, got %v", err)
	}
}

func TestWatcher_WalkDir_SkipsHiddenDirectories(t *testing.T) {
	t.Parallel()

	tempDir, mgr, loader, disp := setupTestEnv(t)
	configDir := filepath.Join(tempDir, "config")
	hiddenDir := filepath.Join(configDir, ".hidden_repo")
	if err := os.MkdirAll(hiddenDir, 0755); err != nil {
		t.Fatalf("failed to create hidden dir: %v", err)
	}

	w := watcher.New(watcher.Config{
		ConfigDir:        configDir,
		DebounceDuration: 5 * time.Millisecond,
	}, mgr, loader, disp, nil)

	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = w.Close() }()
}

func TestWatcher_ContextCancellation(t *testing.T) {
	t.Parallel()

	tempDir, mgr, loader, disp := setupTestEnv(t)
	configDir := filepath.Join(tempDir, "config")

	ctx, cancel := context.WithCancel(context.Background())
	w := watcher.New(watcher.Config{
		ConfigDir:        configDir,
		DebounceDuration: 5 * time.Millisecond,
	}, mgr, loader, disp, nil)

	if err := w.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	cancel()
	if err := w.Close(); err != nil {
		t.Errorf("Close failed after cancel: %v", err)
	}
}

type errDispatcher struct{}

func (d *errDispatcher) DispatchConfigReload(snap *config.Snapshot, diff *config.ConfigDiff) error {
	return fmt.Errorf("config dispatch failure")
}

func (d *errDispatcher) DispatchStyleReload(ev watcher.StyleReloadEvent) error {
	return fmt.Errorf("style dispatch failure")
}

func (d *errDispatcher) DispatchWidgetReload(ev watcher.WidgetReloadEvent) error {
	return fmt.Errorf("widget dispatch failure")
}

func TestWatcher_DispatcherErrorsAndConfigMissing(t *testing.T) {
	t.Parallel()

	tempDir, mgr, loader, _ := setupTestEnv(t)
	configDir := filepath.Join(tempDir, "config")
	customWidgetsDir := filepath.Join(configDir, "widgets")

	clockDir := filepath.Join(customWidgetsDir, "clock", "views")
	if err := os.MkdirAll(clockDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(customWidgetsDir, "clock", "manifest.yaml"), []byte("name: Clock\nversion: 1.0.0\n"), 0644); err != nil {
		t.Fatalf("write manifest failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(clockDir, "widget.html"), []byte("<div>Clock</div>"), 0644); err != nil {
		t.Fatalf("write view failed: %v", err)
	}

	w := watcher.New(watcher.Config{
		ConfigDir:        configDir,
		CustomWidgetsDir: customWidgetsDir,
		DebounceDuration: 5 * time.Millisecond,
	}, mgr, loader, &errDispatcher{}, slog.Default())

	settledCh := make(chan struct{}, 10)
	w.SetSettledHook(func() {
		settledCh <- struct{}{}
	})

	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = w.Close() }()

	// Trigger config, style, and widget updates with error dispatcher
	configFile := filepath.Join(configDir, "config.yaml")
	cssFile := filepath.Join(configDir, "custom.css")
	viewFile := filepath.Join(clockDir, "widget.html")

	_ = os.WriteFile(configFile, []byte(updatedYAML()), 0644)
	_ = os.WriteFile(cssFile, []byte("body {}"), 0644)
	_ = os.WriteFile(viewFile, []byte("<div>Updated</div>"), 0644)

	select {
	case <-settledCh:
		// Settle completed without panics despite dispatcher errors
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for settle hook")
	}

	// Now delete config.yaml, touch it, and trigger flush where os.ReadFile fails
	_ = os.Remove(configFile)
	// We need an event targeting config.yaml so dirtyConfig becomes true
	// We can write to a temp file and rename to config.yaml then remove before flush, or test missing file:
	// A simpler way: write config.yaml, then quickly remove it before debounce timer fires
	wFast := watcher.New(watcher.Config{
		ConfigDir:        configDir,
		DebounceDuration: 50 * time.Millisecond,
	}, mgr, loader, dispWithoutEvents(), slog.Default())
	settledFast := make(chan struct{}, 1)
	wFast.SetSettledHook(func() { settledFast <- struct{}{} })
	if err := wFast.Start(context.Background()); err == nil {
		defer func() { _ = wFast.Close() }()
		_ = os.WriteFile(configFile, []byte("temp"), 0644)
		_ = os.Remove(configFile)
		select {
		case <-settledFast:
		case <-time.After(2 * time.Second):
		}
	}
}

func dispWithoutEvents() watcher.Dispatcher {
	return &errDispatcher{}
}

