package watcher

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/azylman/mirrormere/internal/config"
	"github.com/fsnotify/fsnotify"
)

const (
	// DefaultDebounceDuration is the standard 100ms debounce settle window per SPEC-012 §1.
	DefaultDebounceDuration = 100 * time.Millisecond
	// DefaultMaxDebounceDuration is the maximum delay before forcing a debounce flush to prevent starvation.
	DefaultMaxDebounceDuration = 1000 * time.Millisecond
	// DefaultConfigFileName is the standard configuration file name.
	DefaultConfigFileName = "config.yaml"
	// DefaultStyleFileName is the standard custom stylesheet file name.
	DefaultStyleFileName = "custom.css"
)

// Config defines directory paths and debounce timings for filesystem monitoring.
type Config struct {
	ConfigDir           string        // e.g. "/config"
	ConfigFileName      string        // default: "config.yaml"
	StyleFileName       string        // default: "custom.css"
	BuiltinWidgetsDir   string        // e.g. "/app/widgets"
	CustomWidgetsDir    string        // e.g. "/config/widgets"
	DebounceDuration    time.Duration // default: 100ms
	MaxDebounceDuration time.Duration // default: 1000ms
}

// Watcher coordinates in-process filesystem monitoring and reload dispatching.
type Watcher struct {
	cfg           Config
	fsWatcher     *fsnotify.Watcher
	configManager *config.Manager
	packageLoader config.PackageLoader
	dispatcher    Dispatcher
	logger        *slog.Logger

	mu          sync.Mutex
	watchedDirs map[string]bool
	settledHook func()

	stopCh chan struct{}
	doneWg sync.WaitGroup
}

// New constructs a Watcher. Call Start to begin filesystem monitoring.
func New(
	cfg Config,
	mgr *config.Manager,
	loader config.PackageLoader,
	dispatcher Dispatcher,
	logger *slog.Logger,
) *Watcher {
	if cfg.ConfigFileName == "" {
		cfg.ConfigFileName = DefaultConfigFileName
	}
	if cfg.StyleFileName == "" {
		cfg.StyleFileName = DefaultStyleFileName
	}
	if cfg.DebounceDuration <= 0 {
		cfg.DebounceDuration = DefaultDebounceDuration
	}
	if cfg.MaxDebounceDuration <= 0 {
		cfg.MaxDebounceDuration = DefaultMaxDebounceDuration
	}
	if logger == nil {
		logger = slog.Default()
	}

	return &Watcher{
		cfg:           cfg,
		configManager: mgr,
		packageLoader: loader,
		dispatcher:    dispatcher,
		logger:        logger,
		watchedDirs:   make(map[string]bool),
		stopCh:        make(chan struct{}),
	}
}

// SetSettledHook registers an optional callback invoked whenever a debounce batch completes flushing.
// Used for deterministic, sleep-free synchronization in unit tests.
func (w *Watcher) SetSettledHook(hook func()) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.settledHook = hook
}

// Start initializes the fsnotify watcher, registers directory descriptors, and spawns the coordinator loop.
func (w *Watcher) Start(ctx context.Context) error {
	if w.cfg.ConfigDir == "" {
		return fmt.Errorf("watcher config error: ConfigDir cannot be empty")
	}

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create fsnotify watcher: %w", err)
	}
	w.fsWatcher = fsw

	// 1. Recursively add ConfigDir
	if err := w.addDirRecursive(w.cfg.ConfigDir); err != nil {
		_ = w.fsWatcher.Close() //nolint:errcheck
		return fmt.Errorf("failed to watch config directory %q: %w", w.cfg.ConfigDir, err)
	}

	// 2. Recursively add CustomWidgetsDir if present
	if w.cfg.CustomWidgetsDir != "" {
		if err := w.addDirRecursive(w.cfg.CustomWidgetsDir); err != nil {
			w.logger.Debug("custom widgets directory not present at startup", "path", w.cfg.CustomWidgetsDir, "error", err)
		}
	}

	// 3. Recursively add BuiltinWidgetsDir if present
	if w.cfg.BuiltinWidgetsDir != "" {
		if err := w.addDirRecursive(w.cfg.BuiltinWidgetsDir); err != nil {
			w.logger.Debug("builtin widgets directory not present at startup", "path", w.cfg.BuiltinWidgetsDir, "error", err)
		}
	}

	w.doneWg.Add(1)
	go w.run(ctx)

	return nil
}

// Close gracefully stops the watcher and coordinator loop.
func (w *Watcher) Close() error {
	w.mu.Lock()
	select {
	case <-w.stopCh:
		w.mu.Unlock()
		return nil
	default:
		close(w.stopCh)
	}
	w.mu.Unlock()

	var err error
	if w.fsWatcher != nil {
		err = w.fsWatcher.Close()
	}
	w.doneWg.Wait()
	return err
}

func (w *Watcher) addDirRecursive(root string) error {
	root = filepath.Clean(root)
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("path %q is not a directory", root)
	}

	return filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() {
			return nil
		}
		// Skip hidden directories (e.g. .git, .cache) unless it is root
		if path != root && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}

		w.mu.Lock()
		alreadyWatched := w.watchedDirs[path]
		w.mu.Unlock()

		if !alreadyWatched {
			if err := w.fsWatcher.Add(path); err != nil {
				w.logger.Debug("failed to add watch descriptor", "path", path, "error", err)
				return nil
			}
			w.mu.Lock()
			w.watchedDirs[path] = true
			w.mu.Unlock()
		}
		return nil
	})
}

func (w *Watcher) removeDir(path string) {
	path = filepath.Clean(path)
	w.mu.Lock()
	defer w.mu.Unlock()

	// Remove path and any nested entries
	for dir := range w.watchedDirs {
		if dir == path || strings.HasPrefix(dir, path+string(filepath.Separator)) {
			_ = w.fsWatcher.Remove(dir) //nolint:errcheck
			delete(w.watchedDirs, dir)
		}
	}
}

func (w *Watcher) run(ctx context.Context) {
	defer w.doneWg.Done()

	var debounceTimer *time.Timer
	var debounceTimerC <-chan time.Time
	var batchStartTime time.Time

	dirtyConfig := false
	dirtyStyle := false
	dirtyWidgets := make(map[string]bool)

	stopDebounce := func() {
		if debounceTimer != nil {
			if !debounceTimer.Stop() {
				select {
				case <-debounceTimer.C:
				default:
				}
			}
			debounceTimer = nil
			debounceTimerC = nil
		}
	}

	defer stopDebounce()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopCh:
			return

		case err, ok := <-w.fsWatcher.Errors:
			if !ok {
				return
			}
			w.logger.Warn("filesystem watcher error", "error", err)

		case ev, ok := <-w.fsWatcher.Events:
			if !ok {
				return
			}

			// 1. Immediately ignore temporary editor/swap files per SPEC-012 §1
			if IsTemporaryFile(ev.Name) {
				continue
			}

			// 2. Handle directory creation (dynamically register watches)
			if ev.Op&fsnotify.Create != 0 {
				if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
					_ = w.addDirRecursive(ev.Name) //nolint:errcheck
				}
			}

			// 3. Handle directory removal (cleanup watches)
			if ev.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
				w.removeDir(ev.Name)
			}

			// 4. Classify event target
			target := ClassifyPath(ev.Name, w.cfg)
			if target.Type == TargetUnknown {
				continue
			}

			// 5. Accumulate dirty flags
			switch target.Type {
			case TargetConfig:
				dirtyConfig = true
			case TargetStyle:
				dirtyStyle = true
			case TargetWidget:
				dirtyWidgets[target.WidgetType] = true
			}

			now := time.Now()
			if batchStartTime.IsZero() {
				batchStartTime = now
			}

			// 6. Check MaxDebounceDuration ceiling
			if now.Sub(batchStartTime) >= w.cfg.MaxDebounceDuration {
				stopDebounce()
				w.flush(dirtyConfig, dirtyStyle, dirtyWidgets)
				dirtyConfig = false
				dirtyStyle = false
				dirtyWidgets = make(map[string]bool)
				batchStartTime = time.Time{}
				continue
			}

			// 7. Reset debounce timer
			if debounceTimer == nil {
				debounceTimer = time.NewTimer(w.cfg.DebounceDuration)
				debounceTimerC = debounceTimer.C
			} else {
				stopDebounce()
				debounceTimer = time.NewTimer(w.cfg.DebounceDuration)
				debounceTimerC = debounceTimer.C
			}

		case <-debounceTimerC:
			debounceTimer = nil
			debounceTimerC = nil
			w.flush(dirtyConfig, dirtyStyle, dirtyWidgets)
			dirtyConfig = false
			dirtyStyle = false
			dirtyWidgets = make(map[string]bool)
			batchStartTime = time.Time{}
		}
	}
}

func (w *Watcher) flush(dirtyConfig, dirtyStyle bool, dirtyWidgets map[string]bool) {
	// 1. Config Reload (LKGC Pipeline)
	if dirtyConfig {
		configPath := filepath.Join(w.cfg.ConfigDir, w.cfg.ConfigFileName)
		data, err := os.ReadFile(configPath)
		if err != nil {
			w.logger.Error("failed to read configuration file for reload", "path", configPath, "error", err)
		} else if w.configManager != nil {
			snap, diff, rerr := w.configManager.Reload(data)
			if rerr == nil && w.dispatcher != nil {
				if err := w.dispatcher.DispatchConfigReload(snap, diff); err != nil {
					w.logger.Error("failed to dispatch config reload", "error", err)
				}
			}
		}
	}

	// 2. Style Reload (custom.css)
	if dirtyStyle {
		if w.dispatcher != nil {
			event := StyleReloadEvent{
				File:      w.cfg.StyleFileName,
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			}
			if err := w.dispatcher.DispatchStyleReload(event); err != nil {
				w.logger.Error("failed to dispatch style reload", "error", err)
			}
		}
	}

	// 3. Widget Reload (Package Completeness Verification)
	if len(dirtyWidgets) > 0 {
		types := make([]string, 0, len(dirtyWidgets))
		for t := range dirtyWidgets {
			types = append(types, t)
		}
		sort.Strings(types)

		for _, widgetType := range types {
			if w.packageLoader != nil {
				_, err := w.packageLoader.LoadPackage(widgetType)
				if err != nil {
					w.logger.Warn(fmt.Sprintf("[widget.watcher] warning=\"widget package '%s' is incomplete: %s; waiting for manifest.yaml and views/widget.html\"", widgetType, err.Error()))
					continue
				}
			}
			if w.dispatcher != nil {
				event := WidgetReloadEvent{Type: widgetType}
				if err := w.dispatcher.DispatchWidgetReload(event); err != nil {
					w.logger.Error("failed to dispatch widget reload", "type", widgetType, "error", err)
				}
			}
		}
	}

	// 4. Test hook for deterministic synchronization
	w.mu.Lock()
	hook := w.settledHook
	w.mu.Unlock()
	if hook != nil {
		hook()
	}
}
