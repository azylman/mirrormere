package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/config"
	"github.com/azylman/mirrormere/internal/events"
	"github.com/azylman/mirrormere/internal/render"
	"github.com/azylman/mirrormere/internal/widget"
)

type reloaderTestEnv struct {
	tmpDir        string
	customCSSPath string
	builtinDir    string
	customDir     string
	loader        *widget.Loader
	renderEngine  *render.Engine
	configManager *config.Manager
	hub           *events.Hub
	eventCh       <-chan *events.Event
	reloader      *AssetReloader
}

func setupReloaderTest(t *testing.T, initialCSS bool) *reloaderTestEnv {
	t.Helper()

	tmpDir := t.TempDir()
	customCSSPath := filepath.Join(tmpDir, "custom.css")
	builtinDir := filepath.Join(tmpDir, "builtin")
	customDir := filepath.Join(tmpDir, "custom")

	if err := os.MkdirAll(builtinDir, 0755); err != nil {
		t.Fatalf("failed to create builtin dir: %v", err)
	}
	if err := os.MkdirAll(customDir, 0755); err != nil {
		t.Fatalf("failed to create custom dir: %v", err)
	}

	// Create initial builtin widget
	widgetDir := filepath.Join(builtinDir, "test-widget")
	if err := os.MkdirAll(filepath.Join(widgetDir, "views"), 0755); err != nil {
		t.Fatalf("failed to create widget views dir: %v", err)
	}
	manifestContent := `name: test-widget
version: "1.0.0"
description: Initial description
provider: spacer
default_dimensions: [6, 2]
supported_dimensions:
  - [6, 2]
`
	if err := os.WriteFile(filepath.Join(widgetDir, "manifest.yaml"), []byte(manifestContent), 0644); err != nil {
		t.Fatalf("failed to write manifest: %v", err)
	}
	viewContent := `<div>Initial View</div>`
	if err := os.WriteFile(filepath.Join(widgetDir, "views", "widget.html"), []byte(viewContent), 0644); err != nil {
		t.Fatalf("failed to write view: %v", err)
	}

	if initialCSS {
		if err := os.WriteFile(customCSSPath, []byte("body { background: #000; }"), 0644); err != nil {
			t.Fatalf("failed to write initial custom.css: %v", err)
		}
	}

	loader := widget.NewLoader(builtinDir, customDir)
	initialYAML := []byte(`timezone: UTC
display:
  widgets:
    - id: test-instance
      type: test-widget
      dimensions: [6, 2]
`)
	configManager, err := config.NewManager(initialYAML, loader, nil, nil)
	if err != nil {
		t.Fatalf("failed to create config manager: %v", err)
	}

	stateProvider := events.NewInMemoryStateProvider(configManager)
	hub := events.NewHub(events.HubConfig{}, stateProvider, nil)
	t.Cleanup(func() { hub.Close() })

	eventCh, unsub := hub.Subscribe(context.Background())
	t.Cleanup(unsub)

	renderEngine := render.NewEngine(loader, stateProvider)

	reloader := NewAssetReloader(customCSSPath, loader, builtinDir, customDir, renderEngine, configManager, hub, nil)

	return &reloaderTestEnv{
		tmpDir:        tmpDir,
		customCSSPath: customCSSPath,
		builtinDir:    builtinDir,
		customDir:     customDir,
		loader:        loader,
		renderEngine:  renderEngine,
		configManager: configManager,
		hub:           hub,
		eventCh:       eventCh,
		reloader:      reloader,
	}
}

func expectEvent(t *testing.T, ch <-chan *events.Event, expectedType string, timeout time.Duration) *events.Event {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				t.Fatalf("event channel closed waiting for %s", expectedType)
			}
			if evt.Type == expectedType {
				return evt
			}
		case <-deadline:
			t.Fatalf("timeout waiting for event %s", expectedType)
		}
	}
}

func expectNoEvent(t *testing.T, ch <-chan *events.Event, timeout time.Duration) {
	t.Helper()
	select {
	case evt := <-ch:
		t.Fatalf("unexpected event received: %s (data: %s)", evt.Type, string(evt.Data))
	case <-time.After(timeout):
	}
}

func drainEvents(ch <-chan *events.Event) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

func TestAssetReloader_CSSModified_DispatchesStyleReload(t *testing.T) {
	env := setupReloaderTest(t, true)
	drainEvents(env.eventCh)

	// Modify custom.css
	if err := os.WriteFile(env.customCSSPath, []byte("body { background: #9b59b6; }"), 0644); err != nil {
		t.Fatalf("failed to update custom.css: %v", err)
	}

	if !env.reloader.ReloadCSS() {
		t.Fatal("expected ReloadCSS to return true on file modification")
	}

	evt := expectEvent(t, env.eventCh, events.EventStyleReload, 2*time.Second)
	var payload events.StyleReloadEvent
	if err := json.Unmarshal(evt.Data, &payload); err != nil {
		t.Fatalf("failed to unmarshal style.reload payload: %v", err)
	}
	if payload.File != "custom.css" {
		t.Errorf("expected File 'custom.css', got %q", payload.File)
	}
	if payload.Timestamp == "" {
		t.Error("expected non-empty timestamp in style.reload event")
	}

	// Immediate re-check without changes returns false
	if env.reloader.ReloadCSS() {
		t.Error("expected ReloadCSS to return false when custom.css unchanged")
	}
	expectNoEvent(t, env.eventCh, 100*time.Millisecond)
}

func TestAssetReloader_CSSUnchanged_NoEvent(t *testing.T) {
	env := setupReloaderTest(t, true)
	drainEvents(env.eventCh)

	if env.reloader.ReloadCSS() {
		t.Fatal("expected ReloadCSS to return false when CSS file is unchanged")
	}
	expectNoEvent(t, env.eventCh, 100*time.Millisecond)
}

func TestAssetReloader_CSSCreatedAndDeleted_DispatchesEvent(t *testing.T) {
	env := setupReloaderTest(t, false) // custom.css does not exist initially
	drainEvents(env.eventCh)

	// 1. Initial check when file is missing -> false
	if env.reloader.ReloadCSS() {
		t.Fatal("expected ReloadCSS to return false when file is missing")
	}
	expectNoEvent(t, env.eventCh, 100*time.Millisecond)

	// 2. Create custom.css
	if err := os.WriteFile(env.customCSSPath, []byte("body { color: white; }"), 0644); err != nil {
		t.Fatalf("failed to create custom.css: %v", err)
	}

	if !env.reloader.ReloadCSS() {
		t.Fatal("expected ReloadCSS to return true when file is created")
	}
	evt := expectEvent(t, env.eventCh, events.EventStyleReload, 2*time.Second)
	var payload events.StyleReloadEvent
	if err := json.Unmarshal(evt.Data, &payload); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}
	if payload.File != "custom.css" {
		t.Errorf("expected File 'custom.css', got %q", payload.File)
	}

	// 3. Delete custom.css
	if err := os.Remove(env.customCSSPath); err != nil {
		t.Fatalf("failed to remove custom.css: %v", err)
	}

	if !env.reloader.ReloadCSS() {
		t.Fatal("expected ReloadCSS to return true when file is deleted")
	}
	expectEvent(t, env.eventCh, events.EventStyleReload, 2*time.Second)

	// 4. Repeated check while still deleted -> false
	if env.reloader.ReloadCSS() {
		t.Fatal("expected ReloadCSS to return false on subsequent check while deleted")
	}
	expectNoEvent(t, env.eventCh, 100*time.Millisecond)
}

func TestAssetReloader_WidgetViewModified_EvictsCacheAndDispatchesWidgetReload(t *testing.T) {
	env := setupReloaderTest(t, false)
	drainEvents(env.eventCh)

	// Render widget to populate template cache
	initialHTML, err := env.renderEngine.RenderWidget(context.Background(), "test-instance")
	if err != nil {
		t.Fatalf("failed to render initial widget: %v", err)
	}
	if string(initialHTML) != "<div>Initial View</div>" {
		t.Fatalf("unexpected initial HTML: %s", string(initialHTML))
	}

	// Modify widget template view
	viewPath := filepath.Join(env.builtinDir, "test-widget", "views", "widget.html")
	if err := os.WriteFile(viewPath, []byte("<div>Updated View Content</div>"), 0644); err != nil {
		t.Fatalf("failed to update widget.html: %v", err)
	}

	if !env.reloader.ReloadWidgets() {
		t.Fatal("expected ReloadWidgets to return true when view modified")
	}

	evt := expectEvent(t, env.eventCh, events.EventWidgetReload, 2*time.Second)
	var reloadPayload events.WidgetReloadEvent
	if err := json.Unmarshal(evt.Data, &reloadPayload); err != nil {
		t.Fatalf("failed to unmarshal widget.reload payload: %v", err)
	}
	if reloadPayload.Type != "test-widget" {
		t.Errorf("expected Type 'test-widget', got %q", reloadPayload.Type)
	}

	// Re-rendering should reflect the updated template without restarting server
	updatedHTML, err := env.renderEngine.RenderWidget(context.Background(), "test-instance")
	if err != nil {
		t.Fatalf("failed to re-render widget: %v", err)
	}
	if string(updatedHTML) != "<div>Updated View Content</div>" {
		t.Fatalf("expected re-rendered HTML to reflect new view, got: %s", string(updatedHTML))
	}

	// Re-checking without modifications returns false
	if env.reloader.ReloadWidgets() {
		t.Error("expected ReloadWidgets to return false when nothing changed")
	}
	expectNoEvent(t, env.eventCh, 100*time.Millisecond)
}

func TestAssetReloader_WidgetManifestModified_EvictsCacheAndDispatchesWidgetReload(t *testing.T) {
	env := setupReloaderTest(t, false)
	drainEvents(env.eventCh)

	manifestPath := filepath.Join(env.builtinDir, "test-widget", "manifest.yaml")
	newManifest := `name: test-widget
version: "1.0.1"
description: Updated Manifest Description
provider: spacer
default_dimensions: [6, 2]
supported_dimensions:
  - [6, 2]
`
	if err := os.WriteFile(manifestPath, []byte(newManifest), 0644); err != nil {
		t.Fatalf("failed to update manifest.yaml: %v", err)
	}

	if !env.reloader.ReloadWidgets() {
		t.Fatal("expected ReloadWidgets to return true when manifest modified")
	}

	evt := expectEvent(t, env.eventCh, events.EventWidgetReload, 2*time.Second)
	var reloadPayload events.WidgetReloadEvent
	if err := json.Unmarshal(evt.Data, &reloadPayload); err != nil {
		t.Fatalf("failed to unmarshal widget.reload payload: %v", err)
	}
	if reloadPayload.Type != "test-widget" {
		t.Errorf("expected Type 'test-widget', got %q", reloadPayload.Type)
	}
}

func TestAssetReloader_WidgetIncomplete_SetsPackageErrorAndDispatchesStatus(t *testing.T) {
	env := setupReloaderTest(t, false)
	drainEvents(env.eventCh)

	// Create incomplete package under customDir (manifest only, no view)
	incompleteDir := filepath.Join(env.customDir, "broken-card")
	if err := os.MkdirAll(incompleteDir, 0755); err != nil {
		t.Fatalf("failed to create broken-card dir: %v", err)
	}
	manifestContent := `name: broken-card
version: "1.0.0"
description: Missing view template
provider: spacer
default_dimensions: [6, 2]
`
	if err := os.WriteFile(filepath.Join(incompleteDir, "manifest.yaml"), []byte(manifestContent), 0644); err != nil {
		t.Fatalf("failed to write manifest: %v", err)
	}

	if !env.reloader.ReloadWidgets() {
		t.Fatal("expected ReloadWidgets to return true when incomplete package detected")
	}

	// Verify error status set on configManager
	status := env.configManager.Status()
	if status.ConfigStatus != config.ConfigStatusError {
		t.Fatalf("expected config status 'error', got %q", status.ConfigStatus)
	}
	if status.ConfigError == nil || *status.ConfigError == "" {
		t.Fatal("expected non-empty config error message")
	}

	// Verify system.status event dispatched
	evt := expectEvent(t, env.eventCh, events.EventSystemStatus, 2*time.Second)
	var statusPayload events.SystemStatusData
	if err := json.Unmarshal(evt.Data, &statusPayload); err != nil {
		t.Fatalf("failed to unmarshal system.status payload: %v", err)
	}
	if statusPayload.ConfigStatus != "error" {
		t.Errorf("expected status 'error', got %q", statusPayload.ConfigStatus)
	}

	// Verify that widget.reload was NOT dispatched for the broken package
	expectNoEvent(t, env.eventCh, 100*time.Millisecond)

	// Repeated check without change returns false
	if env.reloader.ReloadWidgets() {
		t.Error("expected ReloadWidgets to return false on duplicate check of incomplete package")
	}
	expectNoEvent(t, env.eventCh, 100*time.Millisecond)
}

func TestAssetReloader_WidgetPromoted_ClearsPackageErrorAndDispatchesWidgetReload(t *testing.T) {
	env := setupReloaderTest(t, false)
	drainEvents(env.eventCh)

	// 1. Create incomplete package
	pkgDir := filepath.Join(env.customDir, "promo-widget")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatalf("failed to create pkg dir: %v", err)
	}
	manifestContent := `name: promo-widget
version: "1.0.0"
description: Promoting from incomplete to complete
provider: spacer
default_dimensions: [6, 2]
`
	if err := os.WriteFile(filepath.Join(pkgDir, "manifest.yaml"), []byte(manifestContent), 0644); err != nil {
		t.Fatalf("failed to write manifest: %v", err)
	}

	if !env.reloader.ReloadWidgets() {
		t.Fatal("expected ReloadWidgets to return true for incomplete package")
	}
	expectEvent(t, env.eventCh, events.EventSystemStatus, 2*time.Second)

	if env.configManager.Status().ConfigStatus != config.ConfigStatusError {
		t.Fatalf("expected error status, got %v", env.configManager.Status().ConfigStatus)
	}

	// 2. Supply missing view to promote package to complete
	if err := os.MkdirAll(filepath.Join(pkgDir, "views"), 0755); err != nil {
		t.Fatalf("failed to create views dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "views", "widget.html"), []byte("<div>Promoted!</div>"), 0644); err != nil {
		t.Fatalf("failed to write view template: %v", err)
	}

	if !env.reloader.ReloadWidgets() {
		t.Fatal("expected ReloadWidgets to return true when package is promoted")
	}

	// Should dispatch widget.reload for promo-widget
	reloadEvt := expectEvent(t, env.eventCh, events.EventWidgetReload, 2*time.Second)
	var reloadPayload events.WidgetReloadEvent
	if err := json.Unmarshal(reloadEvt.Data, &reloadPayload); err != nil {
		t.Fatalf("failed to unmarshal widget.reload: %v", err)
	}
	if reloadPayload.Type != "promo-widget" {
		t.Errorf("expected Type 'promo-widget', got %q", reloadPayload.Type)
	}

	// Should dispatch system.status indicating OK status
	statusEvt := expectEvent(t, env.eventCh, events.EventSystemStatus, 2*time.Second)
	var statusPayload events.SystemStatusData
	if err := json.Unmarshal(statusEvt.Data, &statusPayload); err != nil {
		t.Fatalf("failed to unmarshal system.status: %v", err)
	}
	if statusPayload.ConfigStatus != "ok" {
		t.Errorf("expected status 'ok', got %q", statusPayload.ConfigStatus)
	}

	if env.configManager.Status().ConfigStatus != config.ConfigStatusOK {
		t.Fatalf("expected config status to return to OK, got %v", env.configManager.Status().ConfigStatus)
	}
}

func TestAssetReloader_WidgetDeleted_DispatchesWidgetReload(t *testing.T) {
	env := setupReloaderTest(t, false)
	drainEvents(env.eventCh)

	// Create a custom package
	customPkgDir := filepath.Join(env.customDir, "ephemeral-card")
	if err := os.MkdirAll(filepath.Join(customPkgDir, "views"), 0755); err != nil {
		t.Fatalf("failed to create views dir: %v", err)
	}
	manifest := `name: ephemeral-card
version: "1.0.0"
description: To be deleted
provider: spacer
default_dimensions: [6, 2]
`
	if err := os.WriteFile(filepath.Join(customPkgDir, "manifest.yaml"), []byte(manifest), 0644); err != nil {
		t.Fatalf("failed to write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(customPkgDir, "views", "widget.html"), []byte("<div>Ephemeral</div>"), 0644); err != nil {
		t.Fatalf("failed to write view: %v", err)
	}

	// Register it
	if !env.reloader.ReloadWidgets() {
		t.Fatal("expected ReloadWidgets to return true when new custom package added")
	}
	expectEvent(t, env.eventCh, events.EventWidgetReload, 2*time.Second)

	// Delete entire widget directory
	if err := os.RemoveAll(customPkgDir); err != nil {
		t.Fatalf("failed to remove ephemeral-card: %v", err)
	}

	// Reload widgets
	if !env.reloader.ReloadWidgets() {
		t.Fatal("expected ReloadWidgets to return true when package deleted")
	}

	delEvt := expectEvent(t, env.eventCh, events.EventWidgetReload, 2*time.Second)
	var delPayload events.WidgetReloadEvent
	if err := json.Unmarshal(delEvt.Data, &delPayload); err != nil {
		t.Fatalf("failed to unmarshal reload event: %v", err)
	}
	if delPayload.Type != "ephemeral-card" {
		t.Errorf("expected Type 'ephemeral-card', got %q", delPayload.Type)
	}

	// Repeated check returns false
	if env.reloader.ReloadWidgets() {
		t.Error("expected ReloadWidgets to return false on subsequent check")
	}
	expectNoEvent(t, env.eventCh, 100*time.Millisecond)
}

func TestAssetReloader_IncompleteWidgetDeleted_ClearsError(t *testing.T) {
	env := setupReloaderTest(t, false)
	drainEvents(env.eventCh)

	// Create incomplete package
	pkgDir := filepath.Join(env.customDir, "bad-pkg")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatalf("failed to create bad-pkg dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "manifest.yaml"), []byte("name: bad-pkg\nversion: \"1.0.0\"\nprovider: spacer\n"), 0644); err != nil {
		t.Fatalf("failed to write manifest: %v", err)
	}

	if !env.reloader.ReloadWidgets() {
		t.Fatal("expected ReloadWidgets to return true for incomplete package")
	}
	expectEvent(t, env.eventCh, events.EventSystemStatus, 2*time.Second)
	if env.configManager.Status().ConfigStatus != config.ConfigStatusError {
		t.Fatal("expected error status")
	}

	// Remove incomplete directory
	if err := os.RemoveAll(pkgDir); err != nil {
		t.Fatalf("failed to remove pkgDir: %v", err)
	}

	if !env.reloader.ReloadWidgets() {
		t.Fatal("expected ReloadWidgets to return true when incomplete package is deleted")
	}

	statusEvt := expectEvent(t, env.eventCh, events.EventSystemStatus, 2*time.Second)
	var statusPayload events.SystemStatusData
	if err := json.Unmarshal(statusEvt.Data, &statusPayload); err != nil {
		t.Fatalf("failed to unmarshal system.status: %v", err)
	}
	if statusPayload.ConfigStatus != "ok" {
		t.Errorf("expected status 'ok', got %q", statusPayload.ConfigStatus)
	}
	if env.configManager.Status().ConfigStatus != config.ConfigStatusOK {
		t.Fatal("expected status to return to OK after deleting incomplete package")
	}
}

func TestAssetReloader_NonExistentCustomDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	nonExistentDir := filepath.Join(tmpDir, "missing-custom-widgets")
	loader := widget.NewLoader("", nonExistentDir)

	reloader := NewAssetReloader(
		filepath.Join(tmpDir, "missing.css"),
		loader,
		"",
		nonExistentDir,
		nil,
		nil,
		nil,
		nil,
	)

	cssReloaded, widgetsReloaded := reloader.Reload()
	if cssReloaded || widgetsReloaded {
		t.Errorf("expected (false, false) with non-existent directories, got (%v, %v)", cssReloaded, widgetsReloaded)
	}
}

func TestAssetReloader_ReloadCombined(t *testing.T) {
	env := setupReloaderTest(t, true)
	drainEvents(env.eventCh)

	// Modify CSS
	if err := os.WriteFile(env.customCSSPath, []byte("body { color: red; }"), 0644); err != nil {
		t.Fatalf("failed to modify css: %v", err)
	}

	// Modify widget view
	viewPath := filepath.Join(env.builtinDir, "test-widget", "views", "widget.html")
	if err := os.WriteFile(viewPath, []byte("<div>Combined Reload View</div>"), 0644); err != nil {
		t.Fatalf("failed to modify view: %v", err)
	}

	cssReloaded, widgetsReloaded := env.reloader.Reload()
	if !cssReloaded {
		t.Error("expected cssReloaded to be true")
	}
	if !widgetsReloaded {
		t.Error("expected widgetsReloaded to be true")
	}

	expectEvent(t, env.eventCh, events.EventStyleReload, 2*time.Second)
	expectEvent(t, env.eventCh, events.EventWidgetReload, 2*time.Second)
}

func TestAssetReloader_PackageWithAssets_HashedAndReloaded(t *testing.T) {
	env := setupReloaderTest(t, false)
	drainEvents(env.eventCh)

	// Create a widget package with static assets
	pkgDir := filepath.Join(env.customDir, "asset-widget")
	if err := os.MkdirAll(filepath.Join(pkgDir, "views"), 0755); err != nil {
		t.Fatalf("failed to create views dir: %v", err)
	}
	assetsDir := filepath.Join(pkgDir, "assets", "icons")
	if err := os.MkdirAll(assetsDir, 0755); err != nil {
		t.Fatalf("failed to create assets dir: %v", err)
	}

	manifestContent := `name: asset-widget
version: "1.0.0"
description: Tests asset hashing
provider: spacer
default_dimensions: [6, 2]
supported_dimensions:
  - [6, 2]
`
	if err := os.WriteFile(filepath.Join(pkgDir, "manifest.yaml"), []byte(manifestContent), 0644); err != nil {
		t.Fatalf("failed to write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "views", "widget.html"), []byte("<div>With Assets</div>"), 0644); err != nil {
		t.Fatalf("failed to write view: %v", err)
	}
	assetFile := filepath.Join(assetsDir, "icon.svg")
	if err := os.WriteFile(assetFile, []byte("<svg>icon v1</svg>"), 0644); err != nil {
		t.Fatalf("failed to write asset: %v", err)
	}

	// First reload discovers and registers the asset widget
	if !env.reloader.ReloadWidgets() {
		t.Fatal("expected ReloadWidgets to register asset widget")
	}
	expectEvent(t, env.eventCh, events.EventWidgetReload, 2*time.Second)

	// Second reload without modifications should be a no-op
	if env.reloader.ReloadWidgets() {
		t.Fatal("expected no reload when assets unchanged")
	}

	// Modify asset file
	if err := os.WriteFile(assetFile, []byte("<svg>icon v2 modified</svg>"), 0644); err != nil {
		t.Fatalf("failed to modify asset: %v", err)
	}

	// Reload should detect modified asset
	if !env.reloader.ReloadWidgets() {
		t.Fatal("expected ReloadWidgets to detect modified asset file")
	}
	expectEvent(t, env.eventCh, events.EventWidgetReload, 2*time.Second)
}

func TestAssetReloader_CompletePackageBecomesIncomplete(t *testing.T) {
	env := setupReloaderTest(t, false)
	drainEvents(env.eventCh)

	// Create a complete widget
	pkgDir := filepath.Join(env.customDir, "breaking-widget")
	if err := os.MkdirAll(filepath.Join(pkgDir, "views"), 0755); err != nil {
		t.Fatalf("failed to create views dir: %v", err)
	}
	manifestContent := `name: breaking-widget
version: "1.0.0"
description: Tests transition from complete to broken
provider: spacer
default_dimensions: [6, 2]
supported_dimensions:
  - [6, 2]
`
	if err := os.WriteFile(filepath.Join(pkgDir, "manifest.yaml"), []byte(manifestContent), 0644); err != nil {
		t.Fatalf("failed to write manifest: %v", err)
	}
	viewPath := filepath.Join(pkgDir, "views", "widget.html")
	if err := os.WriteFile(viewPath, []byte("<div>Valid</div>"), 0644); err != nil {
		t.Fatalf("failed to write view: %v", err)
	}

	// Discover complete package
	if !env.reloader.ReloadWidgets() {
		t.Fatal("expected initial reload to discover package")
	}
	expectEvent(t, env.eventCh, events.EventWidgetReload, 2*time.Second)

	// Break package by removing view template
	if err := os.Remove(viewPath); err != nil {
		t.Fatalf("failed to remove view: %v", err)
	}

	// Reload should detect incomplete package, evict cache, set package error
	if !env.reloader.ReloadWidgets() {
		t.Fatal("expected ReloadWidgets to report change when package breaks")
	}

	expectEvent(t, env.eventCh, events.EventSystemStatus, 2*time.Second)
	if env.configManager.Status().ConfigStatus != config.ConfigStatusError {
		t.Errorf("expected error status, got %v", env.configManager.Status().ConfigStatus)
	}
}

func TestAssetReloader_NilSafety(t *testing.T) {
	reloader := NewAssetReloader("", nil, "", "", nil, nil, nil, nil)
	if reloader.ReloadCSS() {
		t.Error("expected ReloadCSS on nil instance to return false")
	}
	if reloader.ReloadWidgets() {
		t.Error("expected ReloadWidgets on nil instance to return false")
	}
	css, wid := reloader.Reload()
	if css || wid {
		t.Errorf("expected (false, false), got (%v, %v)", css, wid)
	}
}
