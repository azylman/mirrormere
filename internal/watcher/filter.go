package watcher

import (
	"path/filepath"
	"strings"
)

// IsTemporaryFile returns true if the filename matches temporary, swap, backup, or editor probe file patterns.
func IsTemporaryFile(name string) bool {
	base := filepath.Base(name)
	if strings.HasSuffix(base, ".tmp") ||
		strings.HasSuffix(base, ".swp") ||
		strings.HasSuffix(base, "~") ||
		base == "4913" ||
		strings.HasPrefix(base, ".goutputstream-") ||
		strings.HasPrefix(base, ".#") ||
		base == ".DS_Store" {
		return true
	}
	return false
}

// TargetType represents the category of file or directory being modified.
type TargetType int

const (
	// TargetUnknown indicates an unclassified or ignored file event.
	TargetUnknown TargetType = iota
	// TargetConfig indicates an update to config.yaml.
	TargetConfig
	// TargetStyle indicates an update to custom.css.
	TargetStyle
	// TargetWidget indicates an update within a widget package directory.
	TargetWidget
)

// TargetInfo describes the identified target of a filesystem event.
type TargetInfo struct {
	Type       TargetType
	WidgetType string
	IsManifest bool
}

// ClassifyPath determines the TargetInfo for a given filesystem path based on watcher configuration.
func ClassifyPath(cleanPath string, cfg Config) TargetInfo {
	if IsTemporaryFile(cleanPath) {
		return TargetInfo{Type: TargetUnknown}
	}

	cleanPath = filepath.Clean(cleanPath)

	// 1. Check if it targets a widget under CustomWidgetsDir first (in case CustomWidgetsDir is inside ConfigDir)
	if cfg.CustomWidgetsDir != "" {
		customDir := filepath.Clean(cfg.CustomWidgetsDir)
		rel, err := filepath.Rel(customDir, cleanPath)
		if err == nil && !strings.HasPrefix(rel, "..") && rel != "." {
			parts := strings.Split(rel, string(filepath.Separator))
			widgetType := parts[0]
			if widgetType != "" && !strings.HasPrefix(widgetType, ".") {
				isManifest := filepath.Base(cleanPath) == "manifest.yaml"
				return TargetInfo{Type: TargetWidget, WidgetType: widgetType, IsManifest: isManifest}
			}
		}
	}

	// 2. Check if it targets a widget under BuiltinWidgetsDir
	if cfg.BuiltinWidgetsDir != "" {
		builtinDir := filepath.Clean(cfg.BuiltinWidgetsDir)
		rel, err := filepath.Rel(builtinDir, cleanPath)
		if err == nil && !strings.HasPrefix(rel, "..") && rel != "." {
			parts := strings.Split(rel, string(filepath.Separator))
			widgetType := parts[0]
			if widgetType != "" && !strings.HasPrefix(widgetType, ".") {
				isManifest := filepath.Base(cleanPath) == "manifest.yaml"
				return TargetInfo{Type: TargetWidget, WidgetType: widgetType, IsManifest: isManifest}
			}
		}
	}

	// 3. Check if it targets config.yaml or custom.css under ConfigDir
	if cfg.ConfigDir != "" {
		configDir := filepath.Clean(cfg.ConfigDir)
		rel, err := filepath.Rel(configDir, cleanPath)
		if err == nil && !strings.HasPrefix(rel, "..") {
			if rel == cfg.ConfigFileName {
				return TargetInfo{Type: TargetConfig}
			}
			if rel == cfg.StyleFileName {
				return TargetInfo{Type: TargetStyle}
			}
		}
	}

	return TargetInfo{Type: TargetUnknown}
}
