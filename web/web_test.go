package web_test

import (
	"os"
	"os/exec"
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

func TestHeaderClock_HouseholdTimezone_NodeRunner(t *testing.T) {
	t.Parallel()

	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node executable not found in PATH; skipping client-side JS clock tests")
	}

	cmd := exec.Command(nodePath, "--test", "test/clock.test.js")
	cmd.Env = append(os.Environ(), "TZ=UTC")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node clock.test.js failed: %v\nOutput:\n%s", err, string(out))
	}
}

func TestSSEClient_ReconnectionReplay_NodeRunner(t *testing.T) {
	t.Parallel()

	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node executable not found in PATH; skipping client-side JS SSE tests")
	}

	cmd := exec.Command(nodePath, "--test", "test/sse.test.js")
	cmd.Env = append(os.Environ(), "TZ=UTC")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node sse.test.js failed: %v\nOutput:\n%s", err, string(out))
	}
}


