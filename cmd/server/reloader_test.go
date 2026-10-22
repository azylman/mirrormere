package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/config"
	"github.com/azylman/mirrormere/internal/domain"
	"github.com/azylman/mirrormere/internal/events"
	"github.com/azylman/mirrormere/internal/render"
	"github.com/azylman/mirrormere/internal/widget"
)

type reloaderTestEnv struct {
	customCSSPath, builtinDir, customDir string
	renderEngine                         *render.Engine
	configManager                        *config.Manager
	eventCh                              <-chan *events.Event
	reloader                             *AssetReloader
}

func setupReloaderTest(t *testing.T, initialCSS bool) *reloaderTestEnv {
	t.Helper()
	tmp := t.TempDir()
	css, bDir, cDir := filepath.Join(tmp, "c.css"), filepath.Join(tmp, "b"), filepath.Join(tmp, "c")
	_ = os.MkdirAll(bDir, 0o755)
	_ = os.MkdirAll(cDir, 0o755)
	writeWidgetPkg(t, bDir, "test-widget", "default", "<div>Initial View</div>", "", "")
	if initialCSS {
		_ = os.WriteFile(css, []byte("body { background: #000; }"), 0o644)
	}
	loader := widget.NewLoader(bDir, cDir)
	cfgMgr, err := config.NewManager([]byte("timezone: UTC\ndisplay:\n  widgets:\n    - id: test-instance\n      type: test-widget\n      dimensions: [6, 2]\n"), loader, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	sp := events.NewInMemoryStateProvider(cfgMgr)
	hub := events.NewHub(events.HubConfig{}, sp, nil)
	t.Cleanup(func() { hub.Close() })
	ch, unsub := hub.Subscribe(context.Background())
	t.Cleanup(unsub)
	eng := render.NewEngine(loader, sp)
	return &reloaderTestEnv{
		customCSSPath: css, builtinDir: bDir, customDir: cDir, renderEngine: eng,
		configManager: cfgMgr, eventCh: ch, reloader: NewAssetReloader(css, loader, bDir, cDir, eng, cfgMgr, hub, nil),
	}
}

func writeWidgetPkg(t *testing.T, dir, name, manifest, view, assetName, assetBody string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if manifest == "default" {
		manifest = "name: " + name + "\nversion: \"1.0.0\"\nprovider: spacer\ndefault_dimensions: [6, 2]\n"
	}
	if manifest != "" {
		_ = os.MkdirAll(p, 0o755)
		_ = os.WriteFile(filepath.Join(p, "manifest.yaml"), []byte(manifest), 0o644)
	}
	if view != "" {
		_ = os.MkdirAll(filepath.Join(p, "views"), 0o755)
		_ = os.WriteFile(filepath.Join(p, "views", "widget.html"), []byte(view), 0o644)
	}
	if assetName != "" {
		ap := filepath.Join(p, "assets", assetName)
		_ = os.MkdirAll(filepath.Dir(ap), 0o755)
		_ = os.WriteFile(ap, []byte(assetBody), 0o644)
	}
	return p
}

func expectEvent(t *testing.T, ch <-chan *events.Event, exp string, timeout time.Duration) *events.Event {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				t.Fatalf("channel closed waiting for %s", exp)
			}
			if evt.Type == exp {
				return evt
			}
		case <-deadline:
			t.Fatalf("timeout waiting for %s", exp)
		}
	}
}

func expectNoEvent(t *testing.T, ch <-chan *events.Event) {
	t.Helper()
	select {
	case evt := <-ch:
		t.Fatalf("unexpected %s", evt.Type)
	default:
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

func TestAssetReloader_ReloadCSS(t *testing.T) {
	tests := []struct {
		name                              string
		initialCSS, deleteCSS, wantReload bool
		modifyCSS                         string
	}{
		{"modified", true, false, true, "body { background: #9b59b6; }"},
		{"unchanged", true, false, false, ""},
		{"created", false, false, true, "body { color: white; }"},
		{"deleted", true, true, true, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := setupReloaderTest(t, tc.initialCSS)
			drainEvents(env.eventCh)
			if tc.deleteCSS {
				_ = os.Remove(env.customCSSPath)
			} else if tc.modifyCSS != "" {
				_ = os.WriteFile(env.customCSSPath, []byte(tc.modifyCSS), 0o644)
			}
			if got := env.reloader.ReloadCSS(); got != tc.wantReload {
				t.Fatalf("ReloadCSS() = %v, want %v", got, tc.wantReload)
			}
			if tc.wantReload {
				evt := expectEvent(t, env.eventCh, events.EventStyleReload, 2*time.Second)
				var payload events.StyleReloadEvent
				if err := json.Unmarshal(evt.Data, &payload); err != nil || payload.File != "c.css" {
					t.Fatalf("unexpected payload: %v, err=%v", payload, err)
				}
			}
			if env.reloader.ReloadCSS() {
				t.Error("expected subsequent false")
			}
			expectNoEvent(t, env.eventCh)
		})
	}
}

func TestAssetReloader_ReloadWidgets(t *testing.T) {
	tests := []struct {
		name, setup, act, wantEvt string
		wantReload, isComb        bool
		wantSt                    config.ConfigStatus
	}{
		{"view_mod", "builtin", "modify_view", events.EventWidgetReload, true, false, ""},
		{"manifest_mod", "builtin", "modify_manifest", events.EventWidgetReload, true, false, ""},
		{"assets_mod", "custom_assets", "modify_asset", events.EventWidgetReload, true, false, ""},
		{"incomplete_err", "custom_incomplete", "none", events.EventSystemStatus, true, false, config.ConfigStatusError},
		{"promo_ok", "custom_incomplete", "promote", events.EventWidgetReload, true, false, config.ConfigStatusOK},
		{"complete_to_incomplete", "custom", "remove_view", events.EventSystemStatus, true, false, config.ConfigStatusError},
		{"del_pkg", "custom", "delete_pkg", events.EventWidgetReload, true, false, ""},
		{"del_incomplete", "custom_incomplete", "delete_pkg", events.EventSystemStatus, true, false, config.ConfigStatusOK},
		{"combined", "builtin", "combined", events.EventWidgetReload, true, true, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := setupReloaderTest(t, tc.isComb)
			drainEvents(env.eventCh)
			if tc.setup == "builtin" {
				_, _ = env.renderEngine.RenderWidgetForNode(context.Background(), "test-instance", "")
			} else if tc.setup != "" {
				name, man, view, aName, aBody := "c-pkg", "default", "<div>c</div>", "", ""
				if tc.setup == "custom_incomplete" {
					name, view = "bad-pkg", ""
				}
				if tc.setup == "custom_assets" {
					name, aName, aBody = "a-pkg", "icons/i.svg", "<svg>1</svg>"
				}
				writeWidgetPkg(t, env.customDir, name, man, view, aName, aBody)
				if tc.act != "none" {
					env.reloader.ReloadWidgets()
					drainEvents(env.eventCh)
				}
			}
			pBuiltin := filepath.Join(env.builtinDir, "test-widget")
			pCustom := filepath.Join(env.customDir, "c-pkg")
			switch tc.act {
			case "modify_view":
				_ = os.WriteFile(filepath.Join(pBuiltin, "views", "widget.html"), []byte("<div>New View</div>"), 0o644)
			case "modify_manifest":
				_ = os.WriteFile(filepath.Join(pBuiltin, "manifest.yaml"), []byte("name: test-widget\nversion: \"1.0.1\"\nprovider: spacer\ndefault_dimensions: [6, 2]\n"), 0o644)
			case "modify_asset":
				_ = os.WriteFile(filepath.Join(env.customDir, "a-pkg", "assets", "icons", "i.svg"), []byte("<svg>2</svg>"), 0o644)
			case "remove_view":
				_ = os.Remove(filepath.Join(pCustom, "views", "widget.html"))
			case "promote":
				writeWidgetPkg(t, env.customDir, "bad-pkg", "", "<div>promoted</div>", "", "")
			case "delete_pkg":
				_ = os.RemoveAll(pCustom)
				_ = os.RemoveAll(filepath.Join(env.customDir, "bad-pkg"))
			case "combined":
				_ = os.WriteFile(env.customCSSPath, []byte("body { color: red; }"), 0o644)
				_ = os.WriteFile(filepath.Join(pBuiltin, "views", "widget.html"), []byte("<div>Combined View</div>"), 0o644)
			}

			if tc.isComb {
				if c, w := env.reloader.Reload(); !c || !w {
					t.Fatalf("Reload() = (%v, %v)", c, w)
				}
				expectEvent(t, env.eventCh, events.EventStyleReload, 2*time.Second)
				expectEvent(t, env.eventCh, events.EventWidgetReload, 2*time.Second)
				return
			}
			if got := env.reloader.ReloadWidgets(); got != tc.wantReload {
				t.Fatalf("ReloadWidgets() = %v, want %v", got, tc.wantReload)
			}
			if tc.wantEvt != "" {
				expectEvent(t, env.eventCh, tc.wantEvt, 2*time.Second)
			}
			if tc.wantSt != "" {
				if tc.wantEvt != events.EventSystemStatus {
					expectEvent(t, env.eventCh, events.EventSystemStatus, 2*time.Second)
				}
				if st := env.configManager.Status().ConfigStatus; st != tc.wantSt {
					t.Fatalf("status = %v, want %v", st, tc.wantSt)
				}
			}
			if env.reloader.ReloadWidgets() {
				t.Error("expected subsequent false")
			}
			expectNoEvent(t, env.eventCh)
			if tc.act == "modify_view" {
				if h, _ := env.renderEngine.RenderWidgetForNode(context.Background(), "test-instance", ""); string(h) != "<div>New View</div>" {
					t.Fatalf("rendered = %s", string(h))
				}
			}
		})
	}
}

func TestAssetReloader_EdgeCases(t *testing.T) {
	for _, tc := range []struct{ name, target string }{
		{"nil_safety", "nil_safety"}, {"missing_dir", "missing_dir"}, {"read_errors", "read_errors"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			switch tc.target {
			case "nil_safety":
				r := NewAssetReloader("", nil, "", "", nil, nil, nil, nil)
				if r.ReloadCSS() || r.ReloadWidgets() || func() bool {
					c, w := r.Reload()
					return c || w
				}() {
					t.Fatal("expected false")
				}
			case "missing_dir":
				m := filepath.Join(tmp, "m")
				r := NewAssetReloader(filepath.Join(tmp, "m.css"), widget.NewLoader("", m), "", m, nil, nil, nil, nil)
				if c, w := r.Reload(); c || w {
					t.Fatal("expected false")
				}
			case "read_errors":
				cd, cf, bf := filepath.Join(tmp, "cd"), filepath.Join(tmp, "cf"), filepath.Join(tmp, "bf")
				_ = os.MkdirAll(cd, 0o755)
				_ = os.WriteFile(cf, []byte("x"), 0o644)
				_ = os.WriteFile(bf, []byte("x"), 0o644)
				r := NewAssetReloader(cd, nil, bf, cf, nil, nil, nil, nil)
				if r.ReloadCSS() || r.ReloadWidgets() {
					t.Fatal("expected false on read errors")
				}
				if r.computePackageHash(&domain.Package{ManifestPath: filepath.Join(tmp, "m"), ViewPath: filepath.Join(tmp, "v")}) == "" {
					t.Fatal("hash empty")
				}
			}
		})
	}
}
