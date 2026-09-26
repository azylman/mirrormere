package watcher_test

import (
	"testing"

	"github.com/azylman/mirrormere/internal/watcher"
)

func TestIsTemporaryFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		filename string
		expected bool
	}{
		{"normal yaml", "config.yaml", false},
		{"normal css", "custom.css", false},
		{"normal html", "widget.html", false},
		{"tmp file", "config.yaml.tmp", true},
		{"vim swap file", ".config.yaml.swp", true},
		{"backup file", "custom.css~", true},
		{"vim probe 4913", "4913", true},
		{"vim probe path", "/config/4913", true},
		{"gnome stream file", ".goutputstream-ABC123", true},
		{"emacs lock file", ".#config.yaml", true},
		{"macOS DS_Store", ".DS_Store", true},
		{"nested normal file", "/config/widgets/clock/views/widget.html", false},
		{"nested tmp file", "/config/widgets/clock/views/widget.html.tmp", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			actual := watcher.IsTemporaryFile(tc.filename)
			if actual != tc.expected {
				t.Errorf("IsTemporaryFile(%q) = %v; want %v", tc.filename, actual, tc.expected)
			}
		})
	}
}

func TestClassifyPath(t *testing.T) {
	t.Parallel()

	cfg := watcher.Config{
		ConfigDir:         "/config",
		ConfigFileName:    "config.yaml",
		StyleFileName:     "custom.css",
		CustomWidgetsDir:  "/config/widgets",
		BuiltinWidgetsDir: "/app/widgets",
	}

	tests := []struct {
		name           string
		path           string
		expectedType   watcher.TargetType
		expectedWidget string
	}{
		{
			name:         "config.yaml in config dir",
			path:         "/config/config.yaml",
			expectedType: watcher.TargetConfig,
		},
		{
			name:         "custom.css in config dir",
			path:         "/config/custom.css",
			expectedType: watcher.TargetStyle,
		},
		{
			name:         "temporary config file",
			path:         "/config/config.yaml.tmp",
			expectedType: watcher.TargetUnknown,
		},
		{
			name:         "temporary custom.css file",
			path:         "/config/custom.css~",
			expectedType: watcher.TargetUnknown,
		},
		{
			name:         "random file in config dir",
			path:         "/config/other.txt",
			expectedType: watcher.TargetUnknown,
		},
		{
			name:           "custom widget manifest",
			path:           "/config/widgets/clock/manifest.yaml",
			expectedType:   watcher.TargetWidget,
			expectedWidget: "clock",
		},
		{
			name:           "custom widget view",
			path:           "/config/widgets/sensor-hud/views/widget.html",
			expectedType:   watcher.TargetWidget,
			expectedWidget: "sensor-hud",
		},
		{
			name:         "custom widget temporary file",
			path:         "/config/widgets/sensor-hud/manifest.yaml.tmp",
			expectedType: watcher.TargetUnknown,
		},
		{
			name:           "builtin widget view",
			path:           "/app/widgets/spacer/views/widget.html",
			expectedType:   watcher.TargetWidget,
			expectedWidget: "spacer",
		},
		{
			name:         "hidden directory in custom widgets",
			path:         "/config/widgets/.git/config",
			expectedType: watcher.TargetUnknown,
		},
		{
			name:         "file outside watched directories",
			path:         "/var/log/syslog",
			expectedType: watcher.TargetUnknown,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			info := watcher.ClassifyPath(tc.path, cfg)
			if info.Type != tc.expectedType {
				t.Errorf("ClassifyPath(%q).Type = %v; want %v", tc.path, info.Type, tc.expectedType)
			}
			if info.WidgetType != tc.expectedWidget {
				t.Errorf("ClassifyPath(%q).WidgetType = %q; want %q", tc.path, info.WidgetType, tc.expectedWidget)
			}
		})
	}
}
