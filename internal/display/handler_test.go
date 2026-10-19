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

func TestGetDisplay_RoutingAndResolution(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	primPath := filepath.Join(tempDir, "display.html")
	localPath := filepath.Join(tempDir, "local.html")
	_ = os.WriteFile(primPath, []byte("<h1>primary</h1>"), 0644)
	_ = os.WriteFile(localPath, []byte("<h1>local</h1>"), 0644)

	tests := []struct {
		name     string
		method   string
		kind     string
		wantCode int
		wantSub  string
		wantHead bool
	}{
		{"options", http.MethodOptions, "default", http.StatusNoContent, "", false},
		{"post_not_allowed", http.MethodPost, "default", http.StatusMethodNotAllowed, `"status":"error"`, false},
		{"head_ok", http.MethodHead, "content", http.StatusOK, "", true},
		{"primary_path", http.MethodGet, "primary", http.StatusOK, "<h1>primary</h1>", false},
		{"local_fallback", http.MethodGet, "local", http.StatusOK, "<h1>local</h1>", false},
		{"template_content", http.MethodGet, "content", http.StatusOK, "<h1>content</h1>", false},
		{"embedded_alias", http.MethodGet, "embedded", http.StatusOK, "<h1>embedded</h1>", false},
		{"malformed_syntax", http.MethodGet, "malformed", http.StatusOK, "<div>{{ bad", false},
		{"not_found", http.MethodGet, "missing", http.StatusNotFound, `"status":"error"`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var h *display.Handler
			switch tc.kind {
			case "primary":
				h = display.NewHandler(display.WithTemplatePath(primPath), display.WithLocalTemplatePath(""))
			case "local":
				h = display.NewHandler(display.WithTemplatePath("/no/such/file"), display.WithLocalTemplatePath(localPath))
			case "content":
				h = display.NewHandler(display.WithTemplateContent([]byte("<h1>content</h1>")))
			case "embedded":
				h = display.NewHandler(display.WithEmbeddedTemplate([]byte("<h1>embedded</h1>")))
			case "malformed":
				h = display.NewHandler(display.WithTemplateContent([]byte("<div>{{ bad")))
			case "missing":
				h = display.NewHandler(display.WithTemplatePath("/no/such/file"), display.WithLocalTemplatePath("/no/such/local"))
			default:
				h = display.NewHandler()
			}
			rec := httptest.NewRecorder()
			h.GetDisplay(rec, httptest.NewRequest(tc.method, "/display", nil))
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

func TestGetDisplay_TemplateDataAndProviders(t *testing.T) {
	t.Parallel()
	tmpl := []byte(`<body data-tz="{{ .Timezone }}" class="{{ if .HideCursor }}cursor-hidden{{ end }}" data-remote="{{ .RemoteURL }}" data-theme="{{ .Theme }}">{{ if .HeaderVideo }}<div id="video" data-id="{{ .HeaderVideo.StreamID }}" data-url="{{ .HeaderVideo.StreamURL }}" data-ctrl="{{ .HeaderVideo.ControlURL }}" data-type="{{ .HeaderVideo.Type }}"></div>{{ end }}</body>`)

	tests := []struct {
		name       string
		mode       string
		path       string
		wantGetter string
		wantSub    string
		wantNotSub string
	}{
		{"default_values", "default", "/display", "UTC", `data-tz="UTC"`, "cursor-hidden"},
		{"static_timezone", "static_tz", "/display", "America/Los_Angeles", `data-tz="America/Los_Angeles"`, ""},
		{"dynamic_tz_setter", "dyn_tz", "/display", "Asia/Tokyo", `data-tz="Europe/London"`, ""},
		{"static_hide_cursor", "static_hide", "/display", "true", "cursor-hidden", ""},
		{"dynamic_hide_cursor", "dyn_hide", "/display", "true", "cursor-hidden", ""},
		{"setter_hide_cursor", "set_hide", "/display", "true", "cursor-hidden", ""},
		{"query_hide_true", "static_unhide", "/display?hide_cursor=true", "false", "cursor-hidden", ""},
		{"query_hide_false", "static_hide", "/display?hide_cursor=false", "true", "", "cursor-hidden"},
		{"static_remote_url", "static_remote", "/display", "http://remote.lan", `data-remote="http://remote.lan"`, ""},
		{"dynamic_remote_setter", "dyn_remote", "/display", "http://setter.lan", `data-remote="http://dyn.lan"`, ""},
		{"static_header_video", "static_video", "/display", "cam1", `data-id="cam1"`, ""},
		{"dynamic_header_video", "dyn_video", "/display", "live-cam", "", ""},
		{"dynamic_theme", "dyn_theme", "/display", "night", `data-theme="night"`, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var h *display.Handler
			switch tc.mode {
			case "static_tz":
				h = display.NewHandler(display.WithEmbeddedTemplate(tmpl), display.WithTimezone("America/Los_Angeles"))
				if h.Timezone() != tc.wantGetter {
					t.Fatalf("tz getter: got %s, want %s", h.Timezone(), tc.wantGetter)
				}
			case "dyn_tz":
				h = display.NewHandler(display.WithEmbeddedTemplate(tmpl), display.WithTimezoneProvider(func() string { return "Europe/London" }))
				h2 := display.NewHandler(display.WithEmbeddedTemplate(tmpl))
				h2.SetTimezone("Asia/Tokyo")
				if h2.Timezone() != tc.wantGetter {
					t.Fatalf("tz setter: got %s, want %s", h2.Timezone(), tc.wantGetter)
				}
			case "static_hide":
				h = display.NewHandler(display.WithEmbeddedTemplate(tmpl), display.WithHideCursor(true))
				if !h.HideCursor() {
					t.Fatalf("hide getter: expected true")
				}
			case "static_unhide":
				h = display.NewHandler(display.WithEmbeddedTemplate(tmpl), display.WithHideCursor(false))
				if h.HideCursor() {
					t.Fatalf("hide getter: expected false")
				}
			case "dyn_hide":
				val := false
				h = display.NewHandler(display.WithEmbeddedTemplate(tmpl), display.WithHideCursorProvider(func() bool { return val }))
				val = true
				if !h.HideCursor() {
					t.Fatalf("dyn hide getter: expected true")
				}
			case "set_hide":
				h = display.NewHandler(display.WithEmbeddedTemplate(tmpl))
				h.SetHideCursor(true)
				if !h.HideCursor() {
					t.Fatalf("set hide getter: expected true")
				}
			case "static_remote":
				h = display.NewHandler(display.WithEmbeddedTemplate(tmpl), display.WithRemoteURL("http://remote.lan"))
				if h.RemoteURL() != tc.wantGetter {
					t.Fatalf("remote getter: got %s, want %s", h.RemoteURL(), tc.wantGetter)
				}
			case "dyn_remote":
				h = display.NewHandler(display.WithEmbeddedTemplate(tmpl), display.WithRemoteURLProvider(func() string { return "http://dyn.lan" }))
				h2 := display.NewHandler(display.WithEmbeddedTemplate(tmpl))
				h2.SetRemoteURL("http://setter.lan")
				if h2.RemoteURL() != tc.wantGetter {
					t.Fatalf("remote setter: got %s, want %s", h2.RemoteURL(), tc.wantGetter)
				}
			case "static_video":
				vData := &display.HeaderVideoTemplateData{Enabled: true, StreamID: "cam1", StreamURL: "http://cam.lan", ControlURL: "http://ctrl.lan", Type: "webrtc"}
				h = display.NewHandler(display.WithEmbeddedTemplate(tmpl), display.WithHeaderVideoTemplateData(vData))
				if h.HeaderVideo() != vData {
					t.Fatalf("video getter mismatch")
				}
			case "dyn_video":
				vData := &display.HeaderVideoTemplateData{Enabled: true, StreamID: "live-cam"}
				h = display.NewHandler(display.WithEmbeddedTemplate(tmpl), display.WithHeaderVideoProvider(func() *display.HeaderVideoTemplateData { return vData }))
				if h.HeaderVideo().StreamID != tc.wantGetter {
					t.Fatalf("dyn video getter mismatch")
				}
			case "dyn_theme":
				th := "night"
				h = display.NewHandler(display.WithEmbeddedTemplate(tmpl), display.WithThemeProvider(func() string { return th }))
				if h.Theme() != tc.wantGetter {
					t.Fatalf("theme getter: got %s, want %s", h.Theme(), tc.wantGetter)
				}
			default:
				h = display.NewHandler(display.WithEmbeddedTemplate(tmpl))
				if h.Timezone() != "UTC" || h.HideCursor() || h.RemoteURL() != "" || h.Theme() != "day" || h.HeaderVideo() != nil {
					t.Fatalf("defaults mismatch")
				}
			}
			rec := httptest.NewRecorder()
			h.GetDisplay(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
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
		"app.js":                []byte("console.log('primary');"),
		"test.css":              []byte(".a { color: red; }"),
		"test.js":               []byte("const x = 1;"),
		"test.svg":              []byte("<svg></svg>"),
		"test.png":              []byte{0x89, 'P', 'N', 'G'},
		"test.jpg":              []byte{0xFF, 0xD8, 0xFF},
		"test.woff2":            []byte("wOF2"),
		"test.html":             []byte("<html>test</html>"),
		"test.bin":              []byte("binary data"),
		"test.custom":           []byte("custom data"),
		"js/calendar_family.js": []byte("console.log('calendar controller');"),
	}
	_ = os.MkdirAll(filepath.Join(tempDir, "js"), 0755)
	for relPath, data := range files {
		_ = os.WriteFile(filepath.Join(tempDir, relPath), data, 0644)
	}
	_ = os.WriteFile(filepath.Join(localDir, "theme.css"), []byte("body { background: #000; }"), 0644)

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
		{"calendar_alias", http.MethodGet, "js/calendar_grid.js", false, http.StatusOK, "", "calendar controller", false},
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
			targetH.GetStatic(rec, httptest.NewRequest(tc.method, "/static/"+tc.path, nil), tc.path)
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
