package watcher

import "github.com/azylman/mirrormere/internal/config"

// RemoveDir exposes removeDir for internal package test assertions.
func (w *Watcher) RemoveDir(path string) {
	w.removeDir(path)
}

// HasWatchedDir checks if a directory path is in watchedDirs for test assertions.
func (w *Watcher) HasWatchedDir(path string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, exists := w.watchedDirs[path]
	return exists
}

// AddWatchedDirForTest records a directory path in watchedDirs for test assertions.
func (w *Watcher) AddWatchedDirForTest(path string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.watchedDirs[path] = true
}

// SendErrorForTest sends a synthetic error to fsWatcher.Errors for test assertions.
func (w *Watcher) SendErrorForTest(err error) {
	w.fsWatcher.Errors <- err
}

// IsWidgetTypeReferencedForTest exposes isWidgetTypeReferenced for unit testing edge cases.
func IsWidgetTypeReferencedForTest(snap *config.Snapshot, widgetType string) bool {
	return isWidgetTypeReferenced(snap, widgetType)
}

