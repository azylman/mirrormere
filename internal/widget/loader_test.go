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

func createPackageFixture(t *testing.T, baseDir, widgetType, manifest, view string, hasAssets bool) string {
	t.Helper()
	pkgDir := filepath.Join(baseDir, widgetType)
	_ = os.MkdirAll(filepath.Join(pkgDir, "views"), 0755)
	if manifest != "" {
		_ = os.WriteFile(filepath.Join(pkgDir, widget.ManifestFilename), []byte(manifest), 0644)
	}
	if view != "" {
		_ = os.WriteFile(filepath.Join(pkgDir, widget.ViewFilename), []byte(view), 0644)
	}
	if hasAssets {
		_ = os.MkdirAll(filepath.Join(pkgDir, widget.AssetsDirname), 0755)
		_ = os.WriteFile(filepath.Join(pkgDir, widget.AssetsDirname, "icon.svg"), []byte("<svg></svg>"), 0644)
	}
	return pkgDir
}

const (
	calManifest     = "name: Cal\nversion: \"1.0.0\"\nprovider: calendar-agenda\ndefault_dimensions: [4, 2]\nrefresh:\n  interval_seconds: 300\n"
	weatherManifest = "name: Weather\nversion: \"1.0.0\"\nprovider: weather-forecast\ndefault_dimensions: [2, 1]\nrefresh:\n  interval_seconds: 900\n"
	defaultManifest = "name: T\nversion: \"1.0.0\"\nprovider: t\ndefault_dimensions: [2, 1]\nconfig_schema:\n  type: object\n  properties:\n    theme:\n      type: string\n      default: dark\n"
)

func TestLoader_DiscoverAndWholePackageOverride(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	b, c := filepath.Join(tmp, "b"), filepath.Join(tmp, "c")
	createPackageFixture(t, b, "calendar-agenda", calManifest, "<div></div>", true)
	createPackageFixture(t, b, "weather-forecast", weatherManifest, "<div></div>", false)
	createPackageFixture(t, c, "calendar-agenda", "name: Custom Cal\nversion: \"2.0.0\"\nprovider: calendar-agenda\ndefault_dimensions: [4, 2]\nrefresh:\n  interval_seconds: 600\n", "<div>custom</div>", false)
	createPackageFixture(t, c, "sensor-card", "name: Sensor\nversion: \"1.0.0\"\nprovider: http\ndefault_dimensions: [2, 1]\nresponse_schema:\n  type: object\n  required: [value]\n  properties:\n    value: {type: number}\n", "<div>sensor</div>", true)

	reg, err := widget.NewLoader(b, c).Discover()
	cal, w, s := reg["calendar-agenda"], reg["weather-forecast"], reg["sensor-card"]
	if err != nil || len(reg) != 3 || cal == nil || !cal.IsCustom() || cal.Manifest.Version != "2.0.0" || cal.HasAssets() || w == nil || !w.IsBuiltin() || s == nil || !s.IsCustom() || !s.HasAssets() {
		t.Fatalf("unexpected Discover(): len=%d, err=%v", len(reg), err)
	}
}

func TestLoader_DiscoverErrorsAndEdgeCases(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, pkgName, manifest, view, loadPkg, wantErr string
		inCustom, isDirFile                             bool
	}{
		{"missing_manifest", "bad", "", "<div></div>", "", "missing required file manifest.yaml", true, false},
		{"missing_view", "bad", weatherManifest, "", "", "missing required file manifest.yaml (or views/widget.html)", true, false},
		{"yaml_syntax_error", "bad-yaml", "name: [unclosed", "<div></div>", "", "manifest syntax error", false, false},
		{"disallowed_default", "has-default", defaultManifest, "<div></div>", "", "declares disallowed keyword 'default'", false, false},
		{"non_dir_skipped", "calendar-agenda", calManifest, "<div></div>", "", "", false, true},
		{"hidden_dirs_skipped", ".git", "", "", ".git", "not found", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tmp := t.TempDir()
			b, c := filepath.Join(tmp, "b"), filepath.Join(tmp, "c")
			_ = os.MkdirAll(b, 0755)
			_ = os.MkdirAll(c, 0755)
			if tc.isDirFile {
				_ = os.WriteFile(filepath.Join(b, "README.md"), []byte("#"), 0644)
			}
			if tc.loadPkg == ".git" {
				_ = os.MkdirAll(filepath.Join(c, ".git"), 0755)
				_ = os.MkdirAll(filepath.Join(b, ".vscode"), 0755)
				createPackageFixture(t, b, "calendar-agenda", calManifest, "<div></div>", false)
			} else {
				dir := b
				if tc.inCustom {
					dir = c
				}
				createPackageFixture(t, dir, tc.pkgName, tc.manifest, tc.view, false)
			}
			loader := widget.NewLoader(b, c)
			reg, err := loader.Discover()
			if tc.loadPkg != "" {
				_, err = loader.LoadPackage(tc.loadPkg)
			}
			if (err != nil) != (tc.wantErr != "") || (tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr)) ||
				(tc.wantErr == "" && (len(reg) != 1 || reg["calendar-agenda"] == nil)) {
				t.Fatalf("Discover/LoadPackage = (%v, %v), wantErr=%q", reg, err, tc.wantErr)
			}
		})
	}

	t.Run("scan_dir_file_error", func(t *testing.T) {
		t.Parallel()
		f := filepath.Join(t.TempDir(), "file.txt")
		_ = os.WriteFile(f, []byte("x"), 0644)
		if _, err := widget.NewLoader(f, t.TempDir()).Discover(); err == nil || !strings.Contains(err.Error(), "failed to read widget directory") {
			t.Fatalf("expected read error, got %v", err)
		}
	})
}

func TestLoader_LoadPackage(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	b, c := filepath.Join(tmp, "b"), filepath.Join(tmp, "c")
	createPackageFixture(t, b, "builtin-w", calManifest, "<div></div>", false)
	createPackageFixture(t, c, "custom-w", weatherManifest, "<div></div>", false)

	loader := widget.NewLoader(b, c)
	if widget.DefaultLoader() == nil {
		t.Fatal("DefaultLoader returned nil")
	}

	for _, tc := range []struct{ name, pkgType, wantSource, wantErr string }{
		{"custom", "custom-w", "custom", ""},
		{"builtin", "builtin-w", "builtin", ""},
		{"missing", "non-existent", "", "not found"},
		{"hidden", ".hidden", "", "not found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pkg, err := loader.LoadPackage(tc.pkgType)
			if (err != nil) != (tc.wantErr != "") || (tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr)) || (err == nil && pkg.Source != tc.wantSource) {
				t.Fatalf("LoadPackage(%q) = (%v, %v), want source=%q, err=%q", tc.pkgType, pkg, err, tc.wantSource, tc.wantErr)
			}
		})
	}
}

func TestLoader_ApplyDefaults(t *testing.T) {
	t.Parallel()
	mkPkg := func(name string, c, r, ref int) *domain.Package {
		return &domain.Package{Type: name, Manifest: domain.WidgetManifest{Name: name, DefaultDimensions: domain.NewDimension(c, r), Refresh: domain.ManifestRefresh{IntervalSeconds: ref}}}
	}
	reg := map[string]*domain.Package{"cal": mkPkg("C", 4, 2, 300), "weather": mkPkg("W", 2, 1, 900), "corrupt": {Type: "corrupt"}}
	ci := 120
	wCal, wWea := config.WidgetConfig{ID: "w1", Type: "cal"}, config.WidgetConfig{ID: "w2", Type: "weather", Dimensions: []int{3, 2}, RefreshIntervalSeconds: &ci}
	wCfg := func(ws ...config.WidgetConfig) *config.Config { return &config.Config{Display: config.DisplayConfig{Widgets: ws}} }
	for _, tc := range []struct {
		name, wantErr string
		cfg           *config.Config
		checkDims     [2]int
		checkRef      int
	}{
		{"success", "", wCfg(wCal, wWea), [2]int{4, 2}, 300},
		{"nil", "configuration cannot be nil", nil, [2]int{}, 0},
		{"unknown", "unknown widget type", wCfg(config.WidgetConfig{Type: "u"}), [2]int{}, 0},
		{"corrupt", "has no valid default_dimensions", wCfg(config.WidgetConfig{Type: "corrupt"}), [2]int{}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := widget.NewLoader("", "").ApplyDefaults(tc.cfg, reg)
			if (err != nil) != (tc.wantErr != "") || (tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
			if tc.wantErr == "" {
				w1, w2 := tc.cfg.Display.Widgets[0], tc.cfg.Display.Widgets[1]
				if w1.Dimensions[0] != tc.checkDims[0] || w1.Dimensions[1] != tc.checkDims[1] || *w1.RefreshIntervalSeconds != tc.checkRef ||
					w2.Dimensions[0] != 3 || w2.Dimensions[1] != 2 || *w2.RefreshIntervalSeconds != 120 {
					t.Fatalf("widgets mismatch: w1=%+v, w2=%+v", w1, w2)
				}
			}
		})
	}
}

func TestCheckPackageCompleteness(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	for _, tc := range []struct{ name, manifest, view string; want bool }{
		{"complete", "name: test", "<div></div>", true},
		{"no_manifest", "", "<div></div>", false},
		{"no_view", "name: test", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if widget.CheckPackageCompleteness(createPackageFixture(t, tmp, tc.name, tc.manifest, tc.view, false)) != tc.want {
				t.Fatalf("%s failed", tc.name)
			}
		})
	}
}

func TestBuiltinPackagesAndAliases(t *testing.T) {
	t.Parallel()
	loader := widget.NewLoader(filepath.Join("..", "..", "widgets"), t.TempDir())
	reg, err := loader.Discover()
	if err != nil {
		t.Fatalf("Discover() failed: %v", err)
	}
	for _, tc := range []struct {
		name, wantType, wantProv     string
		wantCols, wantRows, suppDims int
		wantCap                      string
		hasConf                      bool
	}{
		{"spacer", "spacer", "spacer", 1, 1, 12, "ambient-static", false},
		{"weather-forecast", "weather-forecast", "weather-forecast", 2, 1, 0, "ambient-static", false},
		{"live-view", "live-view", "live-view", 3, 2, 0, "touch-interactive", true},
		{"calendar-feed", "calendar-feed", "calendar-feed", 4, 2, 0, "", false},
		{"calendar-grid", "calendar-grid", "calendar-feed", 6, 2, 0, "", false},
		{"calendar-agenda", "calendar-feed", "calendar-feed", 4, 2, 0, "", false},
		{"calendar-family", "calendar-grid", "calendar-feed", 6, 2, 0, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pkg, err := loader.LoadPackage(tc.name)
			if err != nil || pkg.Type != tc.wantType || pkg.Manifest.Provider != tc.wantProv ||
				pkg.Manifest.DefaultDimensions.Cols != tc.wantCols || pkg.Manifest.DefaultDimensions.Rows != tc.wantRows ||
				(tc.wantCap != "" && !pkg.Manifest.HasCapability(tc.wantCap)) ||
				(tc.suppDims > 0 && len(pkg.Manifest.SupportedDimensions) != tc.suppDims) ||
				(tc.hasConf && pkg.Manifest.ConfigSchema == nil) || pkg.Manifest.Validate() != nil ||
				reg[tc.name] == nil {
				t.Fatalf("LoadPackage(%q) = (%+v, %v)", tc.name, pkg, err)
			}
		})
	}
}
