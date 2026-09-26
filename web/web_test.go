package web_test

import (
	"testing"

	"github.com/azylman/mirrormere/web"
)

func TestEmbeddedContent(t *testing.T) {
	t.Parallel()

	// Verify display.html is embedded
	displayHTML, err := web.Content.ReadFile("templates/display.html")
	if err != nil {
		t.Fatalf("failed to read embedded templates/display.html: %v", err)
	}
	if len(displayHTML) == 0 {
		t.Errorf("expected non-empty templates/display.html")
	}

	// Verify static assets are embedded
	expectedFiles := []string{
		"static/css/hud.css",
		"static/js/sse.js",
		"static/js/carousel.js",
		"static/js/display.js",
	}

	for _, file := range expectedFiles {
		data, err := web.Content.ReadFile(file)
		if err != nil {
			t.Fatalf("failed to read embedded file %q: %v", file, err)
		}
		if len(data) == 0 {
			t.Errorf("expected non-empty content for embedded file %q", file)
		}
	}
}
