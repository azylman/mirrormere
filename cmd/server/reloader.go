package main

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/azylman/mirrormere/internal/config"
	"github.com/azylman/mirrormere/internal/domain"
	"github.com/azylman/mirrormere/internal/events"
	"github.com/azylman/mirrormere/internal/render"
	"github.com/azylman/mirrormere/internal/widget"
)

// AssetReloader coordinates detection of custom stylesheet and widget package modifications,
// invalidating compiled template caches and broadcasting SSE reload notifications.
type AssetReloader struct {
	sync.Mutex

	customCSSPath string
	loader        *widget.Loader
	builtinDir    string
	customDir     string
	renderEngine  *render.Engine
	configManager *config.Manager
	hub           *events.Hub
	logger        *slog.Logger

	lastCSSHash    string
	pkgHashes      map[string]string
	incompletePkgs map[string]string
}

// NewAssetReloader constructs and seeds an AssetReloader instance.
func NewAssetReloader(
	customCSSPath string, loader *widget.Loader, builtinDir, customDir string,
	renderEngine *render.Engine, configManager *config.Manager, hub *events.Hub, logger *slog.Logger,
) *AssetReloader {
	if logger == nil {
		logger = slog.Default()
	}
	ar := &AssetReloader{
		customCSSPath:  customCSSPath,
		loader:         loader,
		builtinDir:     builtinDir,
		customDir:      customDir,
		renderEngine:   renderEngine,
		configManager:  configManager,
		hub:            hub,
		logger:         logger,
		pkgHashes:      make(map[string]string),
		incompletePkgs: make(map[string]string),
	}
	ar.seedInitialState()
	return ar
}

func (r *AssetReloader) readCSSHash(warnLog string) (string, bool) {
	if r.customCSSPath == "" {
		return "", false
	}
	data, err := os.ReadFile(r.customCSSPath)
	if err != nil {
		if !os.IsNotExist(err) {
			r.logger.Warn(warnLog, "path", r.customCSSPath, "error", err)
		}
		return "", true
	}
	return hashBytes(data), true
}

func (r *AssetReloader) seedInitialState() {
	r.Lock()
	defer r.Unlock()
	if hash, ok := r.readCSSHash("failed to read custom CSS file during initial seeding"); ok {
		r.lastCSSHash = hash
	}
	if r.loader != nil {
		for _, widgetType := range r.discoverWidgetTypesLocked() {
			if pkg, err := r.loader.LoadPackage(widgetType); err == nil {
				r.pkgHashes[widgetType] = r.computePackageHash(pkg)
			}
		}
	}
}

// ReloadCSS re-checks custom.css on disk, updating internal hash state and broadcasting style.reload if modified.
func (r *AssetReloader) ReloadCSS() bool {
	r.Lock()
	defer r.Unlock()
	currentHash, ok := r.readCSSHash("failed to read custom CSS file on reload")
	if !ok || currentHash == r.lastCSSHash {
		return false
	}
	r.lastCSSHash = currentHash

	evt := events.StyleReloadEvent{
		File:      filepath.Base(r.customCSSPath),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	if r.hub != nil {
		if err := r.hub.DispatchStyleReload(evt); err != nil {
			r.logger.Error("failed to dispatch style reload", "error", err)
		}
	}
	return true
}

func (r *AssetReloader) evictDeletedWidgetLocked(widgetType string) bool {
	if r.renderEngine != nil {
		r.renderEngine.Invalidate(widgetType)
	}
	statusChanged := r.configManager != nil && r.configManager.ClearPackageError(widgetType)

	if _, wasIncomplete := r.incompletePkgs[widgetType]; wasIncomplete {
		delete(r.incompletePkgs, widgetType)
		statusChanged = true
	}
	delete(r.pkgHashes, widgetType)
	if r.hub != nil {
		if err := r.hub.DispatchWidgetReload(events.WidgetReloadEvent{Type: widgetType}); err != nil {
			r.logger.Error("failed to dispatch widget reload for deleted package", "type", widgetType, "error", err)
		}
	}
	return statusChanged
}

func (r *AssetReloader) recordWidgetLoadErrorLocked(widgetType string, err error) bool {
	statusChanged := false
	lastErr, hadErr := r.incompletePkgs[widgetType]
	if !hadErr || lastErr != err.Error() {
		if r.configManager != nil {
			r.configManager.SetPackageError(widgetType, err)
		}
		r.incompletePkgs[widgetType] = err.Error()
		statusChanged = true
	}
	if _, hadHash := r.pkgHashes[widgetType]; hadHash {
		delete(r.pkgHashes, widgetType)
		if r.renderEngine != nil {
			r.renderEngine.Invalidate(widgetType)
		}
	}
	return statusChanged
}

func (r *AssetReloader) updateLoadedWidgetLocked(widgetType string, pkg *domain.Package) (bool, bool) {
	statusChanged := r.configManager != nil && r.configManager.ClearPackageError(widgetType)

	if _, wasIncomplete := r.incompletePkgs[widgetType]; wasIncomplete {
		delete(r.incompletePkgs, widgetType)
		statusChanged = true
	}

	widgetsChanged := false
	newHash := r.computePackageHash(pkg)
	if oldHash, hadHash := r.pkgHashes[widgetType]; !hadHash || oldHash != newHash {
		r.pkgHashes[widgetType] = newHash
		if r.renderEngine != nil {
			r.renderEngine.Invalidate(widgetType)
		}
		if r.hub != nil {
			if err := r.hub.DispatchWidgetReload(events.WidgetReloadEvent{Type: widgetType}); err != nil {
				r.logger.Error("failed to dispatch widget reload", "type", widgetType, "error", err)
			}
		}
		widgetsChanged = true
	}
	return widgetsChanged, statusChanged
}

// ReloadWidgets re-scans widget packages, invalidating template caches and broadcasting widget.reload or system.status.
func (r *AssetReloader) ReloadWidgets() bool {
	r.Lock()
	defer r.Unlock()
	if r.loader == nil {
		return false
	}

	types := r.discoverWidgetTypesLocked()
	var widgetsChanged, statusChanged bool

	for _, widgetType := range types {
		if !r.packageDirExists(widgetType) {
			if r.evictDeletedWidgetLocked(widgetType) {
				statusChanged = true
			}
			widgetsChanged = true
			continue
		}

		pkg, err := r.loader.LoadPackage(widgetType)
		if err != nil {
			if r.recordWidgetLoadErrorLocked(widgetType, err) {
				statusChanged = true
			}
			continue
		}

		wChanged, sChanged := r.updateLoadedWidgetLocked(widgetType, pkg)
		if wChanged {
			widgetsChanged = true
		}
		if sChanged {
			statusChanged = true
		}
	}

	if statusChanged && r.hub != nil && r.configManager != nil {
		if err := r.hub.DispatchStatus(r.configManager.Status()); err != nil {
			r.logger.Error("failed to dispatch status on widget reload", "error", err)
		}
	}
	return widgetsChanged || statusChanged
}

// Reload executes both ReloadCSS and ReloadWidgets.
func (r *AssetReloader) Reload() (cssReloaded bool, widgetsReloaded bool) {
	return r.ReloadCSS(), r.ReloadWidgets()
}

func (r *AssetReloader) packageDirExists(widgetType string) bool {
	for _, dir := range []string{r.customDir, r.builtinDir} {
		if dir != "" {
			if fi, err := os.Stat(filepath.Join(dir, widgetType)); err == nil && fi.IsDir() {
				return true
			}
		}
	}
	return false
}

func (r *AssetReloader) discoverWidgetTypesLocked() []string {
	typeSet := make(map[string]bool)
	for _, src := range []struct{ dir, label string }{{r.customDir, "custom"}, {r.builtinDir, "builtin"}} {
		if src.dir == "" {
			continue
		}
		entries, err := os.ReadDir(src.dir)
		if err == nil {
			for _, entry := range entries {
				if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
					typeSet[entry.Name()] = true
				}
			}
		} else if !os.IsNotExist(err) {
			r.logger.Warn("failed to read "+src.label+" widgets directory", "dir", src.dir, "error", err)
		}
	}
	for t := range r.pkgHashes {
		typeSet[t] = true
	}
	for t := range r.incompletePkgs {
		typeSet[t] = true
	}
	types := make([]string, 0, len(typeSet))
	for t := range typeSet {
		types = append(types, t)
	}
	sort.Strings(types)
	return types
}

func (r *AssetReloader) computePackageHash(pkg *domain.Package) string {
	hasher := sha256.New()
	hasher.Write([]byte(pkg.Source))
	hasher.Write([]byte{0})

	for _, f := range []struct{ path, desc string }{{pkg.ManifestPath, "manifest"}, {pkg.ViewPath, "view template"}} {
		if data, err := os.ReadFile(f.path); err != nil {
			r.logger.Warn("failed to read package "+f.desc+" for hash", "path", f.path, "error", err)
		} else {
			hasher.Write(data)
		}
		hasher.Write([]byte{0})
	}

	if pkg.AssetsDir != "" {
		if walkErr := filepath.Walk(pkg.AssetsDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return err
			}
			rel, relErr := filepath.Rel(pkg.AssetsDir, path)
			if relErr != nil {
				return relErr
			}
			hasher.Write([]byte(rel))
			if data, readErr := os.ReadFile(path); readErr == nil {
				hasher.Write(data)
			}
			return nil
		}); walkErr != nil {
			r.logger.Warn("failed to walk package assets for hash", "path", pkg.AssetsDir, "error", walkErr)
		}
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
