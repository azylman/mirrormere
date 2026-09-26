package widget

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/azylman/mirrormere/internal/config"
	"github.com/azylman/mirrormere/internal/domain"
	"gopkg.in/yaml.v3"
)

const (
	// DefaultBuiltinDir is the standard container location for built-in widgets.
	DefaultBuiltinDir = "/app/widgets"
	// DefaultCustomDir is the standard volume mount location for custom user widgets.
	DefaultCustomDir = "/config/widgets"

	// ManifestFilename is the required widget manifest definition file.
	ManifestFilename = "manifest.yaml"
	// ViewFilename is the required semantic HTML template file.
	ViewFilename = "views/widget.html"
	// AssetsDirname is the optional static assets directory.
	AssetsDirname = "assets"
)

// Loader discovers and loads widget packages from disk.
type Loader struct {
	builtinDir string
	customDir  string
}

// NewLoader constructs a Loader for the given directories.
func NewLoader(builtinDir, customDir string) *Loader {
	return &Loader{
		builtinDir: builtinDir,
		customDir:  customDir,
	}
}

// DefaultLoader constructs a Loader with standard production paths.
func DefaultLoader() *Loader {
	return NewLoader(DefaultBuiltinDir, DefaultCustomDir)
}

// Discover scans both custom and built-in widget directories and returns the resolved package registry.
// Enforces the whole-package overriding invariant: if a custom package exists, it completely shadows
// the built-in package. If a custom package is incomplete at discovery/startup, an error is returned.
func (l *Loader) Discover() (map[string]*domain.Package, error) {
	registry := make(map[string]*domain.Package)

	// 1. Scan custom overrides (/config/widgets) first
	if err := l.scanDir(l.customDir, "custom", registry); err != nil {
		return nil, err
	}

	// 2. Scan core built-ins (/app/widgets) second
	if err := l.scanDir(l.builtinDir, "builtin", registry); err != nil {
		return nil, err
	}

	return registry, nil
}

func (l *Loader) scanDir(baseDir, source string, registry map[string]*domain.Package) error {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read widget directory %q: %w", baseDir, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		widgetType := entry.Name()

		// If already resolved in registry (e.g. custom overrides builtin), skip per whole-package invariant
		if _, exists := registry[widgetType]; exists {
			continue
		}

		pkgDir := filepath.Join(baseDir, widgetType)
		pkg, err := l.loadPackageFromDir(pkgDir, widgetType, source)
		if err != nil {
			return err
		}
		registry[widgetType] = pkg
	}

	return nil
}

// LoadPackage resolves a single widget package by type, adhering to whole-package precedence.
func (l *Loader) LoadPackage(widgetType string) (*domain.Package, error) {
	// Check custom first
	customPkgDir := filepath.Join(l.customDir, widgetType)
	if fi, err := os.Stat(customPkgDir); err == nil && fi.IsDir() {
		return l.loadPackageFromDir(customPkgDir, widgetType, "custom")
	}

	// Check builtin second
	builtinPkgDir := filepath.Join(l.builtinDir, widgetType)
	if fi, err := os.Stat(builtinPkgDir); err == nil && fi.IsDir() {
		return l.loadPackageFromDir(builtinPkgDir, widgetType, "builtin")
	}

	return nil, fmt.Errorf("widget package '%s' not found", widgetType)
}

func (l *Loader) loadPackageFromDir(pkgDir, widgetType, source string) (*domain.Package, error) {
	manifestPath := filepath.Join(pkgDir, ManifestFilename)
	viewPath := filepath.Join(pkgDir, ViewFilename)

	// Check manifest existence
	if _, err := os.Stat(manifestPath); err != nil {
		return nil, fmt.Errorf("widget package at %s is incomplete: missing required file manifest.yaml (or views/widget.html); overriding a widget type requires a complete package", pkgDir)
	}

	// Check views/widget.html existence
	if _, err := os.Stat(viewPath); err != nil {
		return nil, fmt.Errorf("widget package at %s is incomplete: missing required file manifest.yaml (or views/widget.html); overriding a widget type requires a complete package", pkgDir)
	}

	// Check optional assets directory
	var assetsDir string
	assetsCandidate := filepath.Join(pkgDir, AssetsDirname)
	if fi, err := os.Stat(assetsCandidate); err == nil && fi.IsDir() {
		assetsDir = assetsCandidate
	}

	// Parse and validate manifest
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest at %q: %w", manifestPath, err)
	}

	var manifest domain.WidgetManifest
	if err := yaml.Unmarshal(manifestData, &manifest); err != nil {
		return nil, fmt.Errorf("manifest syntax error in %q: %w", manifestPath, err)
	}

	if err := manifest.Validate(); err != nil {
		return nil, fmt.Errorf("invalid manifest at %q: %w", manifestPath, err)
	}

	return &domain.Package{
		Type:         widgetType,
		Source:       source,
		Dir:          pkgDir,
		ManifestPath: manifestPath,
		ViewPath:     viewPath,
		AssetsDir:    assetsDir,
		Manifest:     manifest,
	}, nil
}

// CheckPackageCompleteness verifies whether a package directory contains both required files
// (manifest.yaml and views/widget.html).
func CheckPackageCompleteness(pkgDir string) bool {
	manifestPath := filepath.Join(pkgDir, ManifestFilename)
	if _, err := os.Stat(manifestPath); err != nil {
		return false
	}

	viewPath := filepath.Join(pkgDir, ViewFilename)
	if _, err := os.Stat(viewPath); err != nil {
		return false
	}

	return true
}

// ApplyDefaults applies manifest default_dimensions and refresh intervals to widget instances in cfg.
func (l *Loader) ApplyDefaults(cfg *config.Config, registry map[string]*domain.Package) error {
	if cfg == nil {
		return fmt.Errorf("configuration cannot be nil")
	}

	for i := range cfg.Display.Widgets {
		w := &cfg.Display.Widgets[i]
		pkg, exists := registry[w.Type]
		if !exists {
			return fmt.Errorf("widget '%s': unknown widget type '%s'", w.ID, w.Type)
		}

		// Fallback dimensions if omitted
		if len(w.Dimensions) == 0 {
			if !pkg.Manifest.DefaultDimensions.IsValid() {
				return fmt.Errorf("[Mirrormere Config Error] widget '%s' omits 'dimensions' and package manifest '%s' has no valid default_dimensions", w.ID, w.Type)
			}
			w.Dimensions = pkg.Manifest.DefaultDimensions.Slice()
		}

		// Fallback refresh interval if omitted
		if w.RefreshIntervalSeconds == nil && pkg.Manifest.Refresh.IntervalSeconds > 0 {
			interval := pkg.Manifest.Refresh.IntervalSeconds
			w.RefreshIntervalSeconds = &interval
		}
	}

	return nil
}
