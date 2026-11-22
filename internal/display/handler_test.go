package display_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azylman/mirrormere/internal/display"
)

// writeTemplate writes content to a fresh display template file and returns a HandlerOption
// pointing the handler at it with no local fallback.
func writeTemplate(t *testing.T, content []byte) display.HandlerOption {
	t.Helper()
	path := filepath.Join(t.TempDir(), "display_react.html")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}
	return display.WithTemplatePath(path)
}

func displayString(s string) func() string { return func() string { return s } }
func displayBool(b bool) func() bool       { return func() bool { return b } }

func displayVideo(v *display.HeaderVideoTemplateData) func() *display.HeaderVideoTemplateData {
	return func() *display.HeaderVideoTemplateData { return v }
}

func displayWave(v *display.VoiceWaveformTemplateData) func() *display.VoiceWaveformTemplateData {
	return func() *display.VoiceWaveformTemplateData { return v }
}

func TestGetDisplay_RoutingAndResolution(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	primPath := filepath.Join(tempDir, "display_react.html")
	localPath := filepath.Join(tempDir, "local.html")
	malformedPath := filepath.Join(tempDir, "malformed.html")
	_ = os.WriteFile(primPath, []byte("<h1>primary</h1>"), 0o644)
	_ = os.WriteFile(localPath, []byte("<h1>local</h1>"), 0o644)
	_ = os.WriteFile(malformedPath, []byte("<div>{{ bad"), 0o644)
	primary := []display.HandlerOption{display.WithTemplatePath(primPath), display.WithLocalTemplatePath("")}
	missing := []display.HandlerOption{display.WithTemplatePath("/no/such/file"), display.WithLocalTemplatePath("/no/such/local")}

	tests := []struct {
		name     string
		method   string
		opts     []display.HandlerOption
		wantCode int
		wantSub  string
		wantHead bool
	}{
		{"options", http.MethodOptions, nil, http.StatusNoContent, "", false},
		{"post_not_allowed", http.MethodPost, nil, http.StatusMethodNotAllowed, `"status":"error"`, false},
		{"head_ok", http.MethodHead, primary, http.StatusOK, "", true},
		{"head_missing", http.MethodHead, missing, http.StatusNotFound, "", false},
		{"primary_path", http.MethodGet, primary, http.StatusOK, "<h1>primary</h1>", false},
		{"local_fallback", http.MethodGet, []display.HandlerOption{display.WithTemplatePath("/no/such/file"), display.WithLocalTemplatePath(localPath)}, http.StatusOK, "<h1>local</h1>", false},
		{"malformed_syntax", http.MethodGet, []display.HandlerOption{display.WithTemplatePath(malformedPath), display.WithLocalTemplatePath("")}, http.StatusOK, "<div>{{ bad", false},
		{"not_found", http.MethodGet, missing, http.StatusNotFound, "display template not found", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := display.NewHandler(tc.opts...)
			rec := httptest.NewRecorder()
			h.GetDisplay(rec, httptest.NewRequest(tc.method, "/display", http.NoBody))
			if rec.Code != tc.wantCode {
				t.Fatalf("code: got %d, want %d", rec.Code, tc.wantCode)
			}
			if tc.wantHead && rec.Body.Len() != 0 {
				t.Fatalf("head body: got %d bytes, want 0", rec.Body.Len())
			}
			if tc.wantSub != "" && !strings.Contains(rec.Body.String(), tc.wantSub) {
				t.Fatalf("body: got %q, want substring %q", rec.Body.String(), tc.wantSub)
			}
			if tc.method == http.MethodOptions && rec.Header().Get("Access-Control-Allow-Origin") != "*" {
				t.Fatalf("cors header missing: %v", rec.Header())
			}
		})
	}
}

type displayGetters struct {
	tz, remote, theme, grid string
	hide                    bool
	video                   *display.HeaderVideoTemplateData
	wave                    *display.VoiceWaveformTemplateData
}

func TestHandler_Getters(t *testing.T) {
	t.Parallel()
	video := &display.HeaderVideoTemplateData{Enabled: true, StreamID: "cam1"}
	wave := &display.VoiceWaveformTemplateData{Enabled: true, LevelsURL: "http://127.0.0.1:8765/levels", Gain: 2.2}
	defaults := displayGetters{tz: "UTC", theme: "day", grid: "6x2"}

	tests := []struct {
		name string
		opts []display.HandlerOption
		want displayGetters
	}{
		{"no_providers", nil, defaults},
		{"empty_or_invalid_provider_values", []display.HandlerOption{
			display.WithTimezoneProvider(displayString("")), display.WithRemoteURLProvider(displayString("")), display.WithThemeProvider(displayString("")),
			display.WithGridProvider(displayString("4x4")), display.WithHideCursorProvider(displayBool(false)),
		}, defaults},
		{"provider_values", []display.HandlerOption{
			display.WithTimezoneProvider(displayString("America/Los_Angeles")), display.WithRemoteURLProvider(displayString("http://remote.example.com")),
			display.WithThemeProvider(displayString("night")), display.WithGridProvider(displayString("3x6")), display.WithHideCursorProvider(displayBool(true)),
			display.WithHeaderVideoProvider(displayVideo(video)), display.WithVoiceWaveformProvider(displayWave(wave)),
		}, displayGetters{tz: "America/Los_Angeles", remote: "http://remote.example.com", theme: "night", grid: "3x6", hide: true, video: video, wave: wave}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := display.NewHandler(tc.opts...)
			got := displayGetters{tz: h.Timezone(), remote: h.RemoteURL(), theme: h.Theme(), grid: h.Grid(), hide: h.HideCursor(), video: h.HeaderVideo(), wave: h.VoiceWaveform()}
			if got != tc.want {
				t.Fatalf("getters = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestGetDisplay_TemplateDataAndProviders(t *testing.T) {
	t.Parallel()
	tmpl := []byte(`<body data-tz="{{ .Timezone }}" class="{{ if .HideCursor }}cursor-hidden{{ end }}" data-remote="{{ .RemoteURL }}" data-theme="{{ .Theme }}" data-grid="{{ .Grid }}">{{ if .HeaderVideo }}<div id="video" data-id="{{ .HeaderVideo.StreamID }}" data-url="{{ .HeaderVideo.StreamURL }}" data-ctrl="{{ .HeaderVideo.ControlURL }}" data-type="{{ .HeaderVideo.Type }}"></div>{{ end }}</body>`)
	video := &display.HeaderVideoTemplateData{Enabled: true, StreamID: "cam1", StreamURL: "http://cam.example.com", ControlURL: "http://ctrl.example.com", Type: "webrtc"}

	tests := []struct {
		name, path, wantSub, wantNotSub string
		opts                            []display.HandlerOption
	}{
		{name: "default_values", path: "/display", wantSub: `<body data-tz="UTC" class="" data-remote="" data-theme="day" data-grid="6x2"></body>`},
		{name: "timezone", path: "/display", wantSub: `data-tz="America/Los_Angeles"`, opts: []display.HandlerOption{display.WithTimezoneProvider(displayString("America/Los_Angeles"))}},
		{name: "hide_cursor", path: "/display", wantSub: `class="cursor-hidden"`, opts: []display.HandlerOption{display.WithHideCursorProvider(displayBool(true))}},
		{name: "query_hide_true", path: "/display?hide_cursor=true", wantSub: `class="cursor-hidden"`, opts: []display.HandlerOption{display.WithHideCursorProvider(displayBool(false))}},
		{name: "query_hide_false", path: "/display?hide_cursor=false", wantNotSub: "cursor-hidden", opts: []display.HandlerOption{display.WithHideCursorProvider(displayBool(true))}},
		{name: "remote_url", path: "/display", wantSub: `data-remote="http://remote.example.com"`, opts: []display.HandlerOption{display.WithRemoteURLProvider(displayString("http://remote.example.com"))}},
		{
			name: "header_video", path: "/display", wantSub: `<div id="video" data-id="cam1" data-url="http://cam.example.com" data-ctrl="http://ctrl.example.com" data-type="webrtc">`,
			opts: []display.HandlerOption{display.WithHeaderVideoProvider(displayVideo(video))},
		},
		{name: "theme", path: "/display", wantSub: `data-theme="night"`, opts: []display.HandlerOption{display.WithThemeProvider(displayString("night"))}},
		{name: "grid_3x6", path: "/display", wantSub: `data-grid="3x6"`, opts: []display.HandlerOption{display.WithGridProvider(displayString("3x6"))}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := display.NewHandler(append([]display.HandlerOption{writeTemplate(t, tmpl)}, tc.opts...)...)
			rec := httptest.NewRecorder()
			h.GetDisplay(rec, httptest.NewRequest(http.MethodGet, tc.path, http.NoBody))
			if tc.wantSub != "" && !strings.Contains(rec.Body.String(), tc.wantSub) {
				t.Fatalf("body missing %q: %s", tc.wantSub, rec.Body.String())
			}
			if tc.wantNotSub != "" && strings.Contains(rec.Body.String(), tc.wantNotSub) {
				t.Fatalf("body contains unwanted %q: %s", tc.wantNotSub, rec.Body.String())
			}
		})
	}
}

func TestGetStatic(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	localDir := t.TempDir()

	files := map[string][]byte{
		"app.js":      []byte("console.log('primary');"),
		"test.css":    []byte(".a { color: red; }"),
		"test.js":     []byte("const x = 1;"),
		"test.svg":    []byte("<svg></svg>"),
		"test.png":    {0x89, 'P', 'N', 'G'},
		"test.jpg":    {0xFF, 0xD8, 0xFF},
		"test.woff2":  []byte("wOF2"),
		"test.html":   []byte("<html>test</html>"),
		"test.bin":    []byte("binary data"),
		"test.custom": []byte("custom data"),
	}
	for relPath, data := range files {
		_ = os.WriteFile(filepath.Join(tempDir, relPath), data, 0o644)
	}
	_ = os.WriteFile(filepath.Join(localDir, "theme.css"), []byte("body { background: #000; }"), 0o644)

	h := display.NewHandler(display.WithStaticDir(tempDir), display.WithLocalStaticDir(localDir))
	hFallback := display.NewHandler(display.WithStaticDir(""), display.WithLocalStaticDir(localDir))

	tests := []struct {
		name     string
		method   string
		path     string
		useEmpty bool
		wantCode int
		wantMime string
		wantSub  string
		wantHead bool
	}{
		{"options", http.MethodOptions, "app.js", false, http.StatusNoContent, "", "", false},
		{"post_not_allowed", http.MethodPost, "app.js", false, http.StatusMethodNotAllowed, "", `"status":"error"`, false},
		{"traversal_dotdot", http.MethodGet, "..", false, http.StatusBadRequest, "", "", false},
		{"traversal_dotdot_file", http.MethodGet, "../secret.txt", false, http.StatusBadRequest, "", "", false},
		{"traversal_escape", http.MethodGet, "../../etc/passwd", false, http.StatusBadRequest, "", "", false},
		{"traversal_abs", http.MethodGet, "/etc/passwd", false, http.StatusBadRequest, "", "", false},
		{"traversal_win", http.MethodGet, `\windows\system32`, false, http.StatusBadRequest, "", "", false},
		{"traversal_dot", http.MethodGet, ".", false, http.StatusBadRequest, "", "", false},
		{"traversal_dot_rel", http.MethodGet, "./something", false, http.StatusBadRequest, "", "", false},
		{"primary_static", http.MethodGet, "app.js", false, http.StatusOK, "", "primary", false},
		{"local_fallback", http.MethodGet, "theme.css", true, http.StatusOK, "", "background: #000", false},
		{"head_method", http.MethodHead, "app.js", false, http.StatusOK, "", "", true},
		{"missing_file", http.MethodGet, "missing.txt", false, http.StatusNotFound, "", `"status":"error"`, false},
		{"mime_css", http.MethodGet, "test.css", false, http.StatusOK, "text/css; charset=utf-8", "", false},
		{"mime_js", http.MethodGet, "test.js", false, http.StatusOK, "text/javascript; charset=utf-8", "", false},
		{"mime_svg", http.MethodGet, "test.svg", false, http.StatusOK, "image/svg+xml", "", false},
		{"mime_png", http.MethodGet, "test.png", false, http.StatusOK, "image/png", "", false},
		{"mime_jpg", http.MethodGet, "test.jpg", false, http.StatusOK, "image/jpeg", "", false},
		{"mime_woff2", http.MethodGet, "test.woff2", false, http.StatusOK, "font/woff2", "", false},
		{"mime_html", http.MethodGet, "test.html", false, http.StatusOK, "text/html; charset=utf-8", "", false},
		{"mime_bin", http.MethodGet, "test.bin", false, http.StatusOK, "application/octet-stream", "", false},
		{"mime_custom", http.MethodGet, "test.custom", false, http.StatusOK, "application/octet-stream", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			targetH := h
			if tc.useEmpty {
				targetH = hFallback
			}
			rec := httptest.NewRecorder()
			targetH.GetStatic(rec, httptest.NewRequest(tc.method, "/static/"+tc.path, http.NoBody), tc.path)
			if rec.Code != tc.wantCode {
				t.Fatalf("code: got %d, want %d", rec.Code, tc.wantCode)
			}
			if tc.wantHead && rec.Body.Len() != 0 {
				t.Fatalf("head body: got %d bytes, want 0", rec.Body.Len())
			}
			if tc.wantMime != "" && rec.Header().Get("Content-Type") != tc.wantMime {
				t.Fatalf("mime: got %q, want %q", rec.Header().Get("Content-Type"), tc.wantMime)
			}
			if tc.wantSub != "" && !strings.Contains(rec.Body.String(), tc.wantSub) {
				t.Fatalf("body: got %q, want substring %q", rec.Body.String(), tc.wantSub)
			}
		})
	}
}

func TestVoiceWaveformProvider(t *testing.T) {
	t.Parallel()
	tmpl := []byte(`<body>{{ if and .VoiceWaveform .VoiceWaveform.Enabled }}<wave url="{{ .VoiceWaveform.LevelsURL }}" gain="{{ .VoiceWaveform.Gain }}"></wave>{{ end }}</body>`)
	tmplOpt := writeTemplate(t, tmpl)

	h := display.NewHandler(tmplOpt)
	if h.VoiceWaveform() != nil {
		t.Fatal("voice waveform without provider should be nil")
	}
	rec := httptest.NewRecorder()
	h.GetDisplay(rec, httptest.NewRequest(http.MethodGet, "/display", http.NoBody))
	if strings.Contains(rec.Body.String(), "<wave") {
		t.Fatalf("waveform rendered without provider: %s", rec.Body.String())
	}

	wave := &display.VoiceWaveformTemplateData{Enabled: true, LevelsURL: "http://127.0.0.1:8765/levels", Gain: 2.2}
	h = display.NewHandler(tmplOpt, display.WithVoiceWaveformProvider(func() *display.VoiceWaveformTemplateData { return wave }))
	if h.VoiceWaveform() != wave {
		t.Fatal("voice waveform getter did not return provider value")
	}
	rec = httptest.NewRecorder()
	h.GetDisplay(rec, httptest.NewRequest(http.MethodGet, "/display", http.NoBody))
	body := rec.Body.String()
	if !strings.Contains(body, `url="http://127.0.0.1:8765/levels"`) || !strings.Contains(body, `gain="2.2"`) {
		t.Fatalf("waveform data missing from template: %s", body)
	}

	disabled := &display.VoiceWaveformTemplateData{Enabled: false, LevelsURL: "http://127.0.0.1:8765/levels", Gain: 2.2}
	h = display.NewHandler(tmplOpt, display.WithVoiceWaveformProvider(func() *display.VoiceWaveformTemplateData { return disabled }))
	rec = httptest.NewRecorder()
	h.GetDisplay(rec, httptest.NewRequest(http.MethodGet, "/display", http.NoBody))
	if strings.Contains(rec.Body.String(), "<wave") {
		t.Fatalf("disabled waveform rendered: %s", rec.Body.String())
	}
}

// TestGetDisplay_RepoTemplate checks the shipped display_react.html: provider data lands on the
// body, and the perf instrumentation loads ahead of the React bundle only for ?perf=1 (#779).
// wantSubs must appear in order.
func TestGetDisplay_RepoTemplate(t *testing.T) {
	t.Parallel()
	const perfScript = `<script src="static/js/perf.js"></script>`
	const bundle = `<script type="module" src="static/ui/app.js"></script>`
	providers := []display.HandlerOption{
		display.WithHideCursorProvider(displayBool(true)), display.WithRemoteURLProvider(displayString("http://remote.example/display")),
	}
	tests := []struct {
		name, path           string
		opts                 []display.HandlerOption
		wantSubs, wantNoSubs []string
	}{
		{name: "shell_with_providers", path: "/display", opts: providers, wantSubs: []string{
			`<base href="./">`, `data-hide-cursor="true"`, `class="mm-touch-display"`, `data-remote-url="http://remote.example/display"`, `<div id="root"></div>`, bundle,
		}, wantNoSubs: []string{perfScript}},
		{name: "perf_zero", path: "/display?perf=0", wantSubs: []string{bundle}, wantNoSubs: []string{perfScript}},
		{name: "perf_one", path: "/display?perf=1", wantSubs: []string{perfScript, bundle}},
		{name: "perf_one_with_other_params", path: "/display?hide_cursor=true&perf=1", wantSubs: []string{perfScript, bundle}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			opts := append([]display.HandlerOption{display.WithTemplatePath("../../web/templates/display_react.html"), display.WithLocalTemplatePath("")}, tc.opts...)
			rec := httptest.NewRecorder()
			display.NewHandler(opts...).GetDisplay(rec, httptest.NewRequest(http.MethodGet, tc.path, http.NoBody))
			if rec.Code != http.StatusOK {
				t.Fatalf("code: got %d, want 200", rec.Code)
			}
			rest := rec.Body.String()
			for _, want := range tc.wantSubs {
				i := strings.Index(rest, want)
				if i < 0 {
					t.Fatalf("body missing %q after the previous match: %s", want, rec.Body.String())
				}
				rest = rest[i+len(want):]
			}
			for _, unwanted := range tc.wantNoSubs {
				if strings.Contains(rec.Body.String(), unwanted) {
					t.Fatalf("body contains %q", unwanted)
				}
			}
		})
	}
}
