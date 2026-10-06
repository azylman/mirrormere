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
	customCSSPath  string
	loader         *widget.Loader
	builtinDir     string
	customDir      string
	renderEngine   *render.Engine
	configManager  *config.Manager
	hub            *events.Hub
	logger         *slog.Logger

	lastCSSHash    string
	pkgHashes      map[string]string
	incompletePkgs map[string]string
}

// NewAssetReloader constructs and seeds an AssetReloader instance.
func NewAssetReloader(
	customCSSPath string,
	loader *widget.Loader,
	builtinDir, customDir string,
	renderEngine *render.Engine,
	configManager *config.Manager,
	hub *events.Hub,
	logger *slog.Logger,
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

func (r *AssetReloader) seedInitialState() {
	r.Lock()
	defer r.Unlock()

	// 1. Seed custom.css hash
	if r.customCSSPath != "" {
		if data, err := os.ReadFile(r.customCSSPath); err == nil {
			r.lastCSSHash = hashBytes(data)
		} else {
			if !os.IsNotExist(err) {
				r.logger.Warn("failed to read custom CSS file during initial seeding", "path", r.customCSSPath, "error", err)
			}
			r.lastCSSHash = ""
		}
	}

	// 2. Seed widget package hashes
	if r.loader != nil {
		types := r.discoverWidgetTypesLocked()
		for _, widgetType := range types {
			pkg, err := r.loader.LoadPackage(widgetType)
			if err == nil {
				r.pkgHashes[widgetType] = r.computePackageHash(pkg)
			}
		}
	}
}

// ReloadCSS re-checks custom.css on disk, updating internal hash state and broadcasting style.reload if modified.
// Returns true if a style reload was dispatched.
func (r *AssetReloader) ReloadCSS() bool {
	r.Lock()
	defer r.Unlock()

	if r.customCSSPath == "" {
		return false
	}

	var currentHash string
	data, err := os.ReadFile(r.customCSSPath)
	if err == nil {
		currentHash = hashBytes(data)
	} else {
		if !os.IsNotExist(err) {
			r.logger.Warn("failed to read custom CSS file on reload", "path", r.customCSSPath, "error", err)
		}
		currentHash = ""
	}

	if currentHash == r.lastCSSHash {
		return false
	}

	// Hash changed (modified, created, or deleted)
	r.lastCSSHash = currentHash

	nowStr := time.Now().UTC().Format(time.RFC3339)
	evt := events.StyleReloadEvent{
		File:      filepath.Base(r.customCSSPath),
		Timestamp: nowStr,
	}
	if r.hub != nil {
		if err := r.hub.DispatchStyleReload(evt); err != nil {
			r.logger.Error("failed to dispatch style reload", "error", err)
		}
	}
	return true
}

// ReloadWidgets re-scans widget packages, invalidating template caches and broadcasting widget.reload or system.status.
// Returns true if any widget packages or package error statuses were modified.
func (r *AssetReloader) ReloadWidgets() bool {
	r.Lock()
	defer r.Unlock()

	if r.loader == nil {
		return false
	}

	types := r.discoverWidgetTypesLocked()
	var widgetsChanged bool
	var statusChanged bool

	for _, widgetType := range types {
		if !r.packageDirExists(widgetType) {
			// Package was deleted
			if r.renderEngine != nil {
				r.renderEngine.Invalidate(widgetType)
			}
			if r.configManager != nil && r.configManager.ClearPackageError(widgetType) {
				statusChanged = true
			}
			if _, wasIncomplete := r.incompletePkgs[widgetType]; wasIncomplete {
				delete(r.incompletePkgs, widgetType)
				statusChanged = true
			}
			_, hadHash := r.pkgHashes[widgetType]
			if hadHash {
				delete(r.pkgHashes, widgetType)
			}
			if r.hub != nil {
				if err := r.hub.DispatchWidgetReload(events.WidgetReloadEvent{Type: widgetType}); err != nil {
					r.logger.Error("failed to dispatch widget reload for deleted package", "type", widgetType, "error", err)
				}
			}
			widgetsChanged = true
			continue
		}

		// Package directory exists; attempt to load
		pkg, err := r.loader.LoadPackage(widgetType)
		if err != nil {
			// Incomplete package
			lastErr, hadErr := r.incompletePkgs[widgetType]
			if !hadErr || lastErr != err.Error() {
				if r.configManager != nil {
					r.configManager.SetPackageError(widgetType, err)
				}
				r.incompletePkgs[widgetType] = err.Error()
				statusChanged = true
			}

			// If it was previously complete and is now broken, evict cache
			if _, hadHash := r.pkgHashes[widgetType]; hadHash {
				delete(r.pkgHashes, widgetType)
				if r.renderEngine != nil {
					r.renderEngine.Invalidate(widgetType)
				}
			}
			continue
		}

		// Package is complete
		// Clear any previous package error if present
		if r.configManager != nil && r.configManager.ClearPackageError(widgetType) {
			statusChanged = true
		}
		if _, wasIncomplete := r.incompletePkgs[widgetType]; wasIncomplete {
			delete(r.incompletePkgs, widgetType)
			statusChanged = true
		}

		// Check hash
		newHash := r.computePackageHash(pkg)
		oldHash, hadHash := r.pkgHashes[widgetType]
		if !hadHash || oldHash != newHash {
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
	cssReloaded = r.ReloadCSS()
	widgetsReloaded = r.ReloadWidgets()
	return cssReloaded, widgetsReloaded
}

func (r *AssetReloader) packageDirExists(widgetType string) bool {
	if r.customDir != "" {
		if fi, err := os.Stat(filepath.Join(r.customDir, widgetType)); err == nil && fi.IsDir() {
			return true
		}
	}
	if r.builtinDir != "" {
		if fi, err := os.Stat(filepath.Join(r.builtinDir, widgetType)); err == nil && fi.IsDir() {
			return true
		}
	}
	return false
}

func (r *AssetReloader) discoverWidgetTypesLocked() []string {
	typeSet := make(map[string]bool)

	// 1. Scan custom directory
	if r.customDir != "" {
		entries, err := os.ReadDir(r.customDir)
		if err == nil {
			for _, entry := range entries {
				if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
					typeSet[entry.Name()] = true
				}
			}
		} else if !os.IsNotExist(err) {
			r.logger.Warn("failed to read custom widgets directory", "dir", r.customDir, "error", err)
		}
	}

	// 2. Scan builtin directory
	if r.builtinDir != "" {
		entries, err := os.ReadDir(r.builtinDir)
		if err == nil {
			for _, entry := range entries {
				if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
					typeSet[entry.Name()] = true
				}
			}
		} else if !os.IsNotExist(err) {
			r.logger.Warn("failed to read builtin widgets directory", "dir", r.builtinDir, "error", err)
		}
	}

	// 3. Include any currently tracked packages (in case their directories were removed)
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

	manifestData, err := os.ReadFile(pkg.ManifestPath)
	if err != nil {
		r.logger.Warn("failed to read package manifest for hash", "path", pkg.ManifestPath, "error", err)
	} else {
		hasher.Write(manifestData)
	}
	hasher.Write([]byte{0})

	viewData, err := os.ReadFile(pkg.ViewPath)
	if err != nil {
		r.logger.Warn("failed to read package view template for hash", "path", pkg.ViewPath, "error", err)
	} else {
		hasher.Write(viewData)
	}
	hasher.Write([]byte{0})

	if pkg.AssetsDir != "" {
		if walkErr := filepath.Walk(pkg.AssetsDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
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
