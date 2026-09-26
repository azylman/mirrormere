package web_test

import (
	"os"
	"os/exec"
	"testing"
)

func TestFilesystemContent(t *testing.T) {
	t.Parallel()

	// Verify display.html is present on disk
	displayHTML, err := os.ReadFile("templates/display.html")
	if err != nil {
		t.Fatalf("failed to read templates/display.html: %v", err)
	}
	if len(displayHTML) == 0 {
		t.Errorf("expected non-empty templates/display.html")
	}

	// Verify static assets are present on disk
	expectedFiles := []string{
		"static/css/hud.css",
		"static/js/sse.js",
		"static/js/audio.js",
		"static/js/carousel.js",
		"static/js/video.js",
		"static/js/video_hud.js",
		"static/js/voice.js",
		"static/js/display.js",
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

func TestVideoPlayer_NodeRunner(t *testing.T) {
	t.Parallel()

	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node executable not found in PATH; skipping client-side JS video tests")
	}

	cmd := exec.Command(nodePath, "--test", "test/video.test.js")
	cmd.Env = append(os.Environ(), "TZ=UTC")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node video.test.js failed: %v\nOutput:\n%s", err, string(out))
	}
}

func TestVideoHUD_NodeRunner(t *testing.T) {
	t.Parallel()

	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node executable not found in PATH; skipping client-side JS video HUD tests")
	}

	cmd := exec.Command(nodePath, "--test", "test/video_hud.test.js")
	cmd.Env = append(os.Environ(), "TZ=UTC")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node video_hud.test.js failed: %v\nOutput:\n%s", err, string(out))
	}
}

func TestAudio_NodeRunner(t *testing.T) {
	t.Parallel()

	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node executable not found in PATH; skipping client-side JS audio tests")
	}

	cmd := exec.Command(nodePath, "--test", "test/audio.test.js")
	cmd.Env = append(os.Environ(), "TZ=UTC")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node audio.test.js failed: %v\nOutput:\n%s", err, string(out))
	}
}

func TestVoice_NodeRunner(t *testing.T) {
	t.Parallel()

	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node executable not found in PATH; skipping client-side JS voice HUD tests")
	}

	cmd := exec.Command(nodePath, "--test", "test/voice.test.js")
	cmd.Env = append(os.Environ(), "TZ=UTC")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node voice.test.js failed: %v\nOutput:\n%s", err, string(out))
	}
}



