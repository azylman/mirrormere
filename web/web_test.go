package web_test

import (
	"os"
	"testing"
)

func TestFilesystemContent(t *testing.T) {
	t.Parallel()

	// Verify the React shell template is present on disk
	reactHTML, err := os.ReadFile("templates/display_react.html")
	if err != nil {
		t.Fatalf("failed to read templates/display_react.html: %v", err)
	}
	if len(reactHTML) == 0 {
		t.Errorf("expected non-empty templates/display_react.html")
	}

	// Verify static assets are present on disk
	expectedFiles := []string{
		"static/css/hud.css",
		"static/js/perf.js",
	}

	for _, file := range expectedFiles {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("failed to read file %q: %v", file, err)
		}
		if len(data) == 0 {
			t.Errorf("expected non-empty content for file %q", file)
		}
	}
}
