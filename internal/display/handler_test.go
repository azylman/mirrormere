package display_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/azylman/mirrormere/internal/api"
	"github.com/azylman/mirrormere/internal/display"
)

func TestGetDisplay_MethodsAndHeaders(t *testing.T) {
	t.Parallel()

	h := display.NewHandler()

	// 1. OPTIONS method
	req := httptest.NewRequest(http.MethodOptions, "/display", nil)
	rec := httptest.NewRecorder()
	h.GetDisplay(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected status 204 for OPTIONS, got %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("expected CORS origin '*', got %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}

	// 2. Disallowed method (POST)
	req = httptest.NewRequest(http.MethodPost, "/display", nil)
	rec = httptest.NewRecorder()
	h.GetDisplay(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405 for POST, got %d", rec.Code)
	}
	var errResp api.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil || errResp.Status != "error" {
		t.Errorf("expected JSON error response for 405, got %v", rec.Body.String())
	}

	// 3. HEAD method
	req = httptest.NewRequest(http.MethodHead, "/display", nil)
	rec = httptest.NewRecorder()
	h.GetDisplay(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200 for HEAD, got %d", rec.Code)
	}
	if rec.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Errorf("expected Content-Type text/html, got %q", rec.Header().Get("Content-Type"))
	}
	if rec.Body.Len() != 0 {
		t.Errorf("expected empty body for HEAD, got %d bytes", rec.Body.Len())
	}
}

func TestGetDisplay_Resolutions(t *testing.T) {
	t.Parallel()

	t.Run("PrimaryTemplatePath", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		tmplPath := filepath.Join(tempDir, "display.html")
		if err := os.WriteFile(tmplPath, []byte("<html>primary</html>"), 0644); err != nil {
			t.Fatal(err)
		}

		h := display.NewHandler(
			display.WithTemplatePath(tmplPath),
			display.WithLocalTemplatePath(""),
			display.WithEmbeddedFS(nil),
		)

		req := httptest.NewRequest(http.MethodGet, "/display", nil)
		rec := httptest.NewRecorder()
		h.GetDisplay(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "<html>primary</html>") {
			t.Errorf("unexpected body: %s", rec.Body.String())
		}
	})

	t.Run("LocalFallbackTemplatePath", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		tmplPath := filepath.Join(tempDir, "local_display.html")
		if err := os.WriteFile(tmplPath, []byte("<html>local</html>"), 0644); err != nil {
			t.Fatal(err)
		}

		h := display.NewHandler(
			display.WithTemplatePath("/nonexistent/display.html"),
			display.WithLocalTemplatePath(tmplPath),
			display.WithEmbeddedFS(nil),
		)

		req := httptest.NewRequest(http.MethodGet, "/display", nil)
		rec := httptest.NewRecorder()
		h.GetDisplay(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "<html>local</html>") {
			t.Errorf("unexpected body: %s", rec.Body.String())
		}
	})

	t.Run("EmbeddedTemplateOverride", func(t *testing.T) {
		t.Parallel()
		h := display.NewHandler(
			display.WithTemplatePath("/nonexistent/path"),
			display.WithLocalTemplatePath("/nonexistent/local"),
			display.WithEmbeddedTemplate([]byte("<html>embedded-override</html>")),
		)

		req := httptest.NewRequest(http.MethodGet, "/display", nil)
		rec := httptest.NewRecorder()
		h.GetDisplay(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "<html>embedded-override</html>") {
			t.Errorf("unexpected body: %s", rec.Body.String())
		}
	})

	t.Run("EmbeddedFS", func(t *testing.T) {
		t.Parallel()
		mockFS := fstest.MapFS{
			"templates/display.html": &fstest.MapFile{
				Data: []byte("<html>mock-fs</html>"),
			},
		}

		h := display.NewHandler(
			display.WithTemplatePath("/nonexistent/path"),
			display.WithLocalTemplatePath("/nonexistent/local"),
			display.WithEmbeddedFS(mockFS),
		)

		req := httptest.NewRequest(http.MethodGet, "/display", nil)
		rec := httptest.NewRecorder()
		h.GetDisplay(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "<html>mock-fs</html>") {
			t.Errorf("unexpected body: %s", rec.Body.String())
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		t.Parallel()
		h := display.NewHandler(
			display.WithTemplatePath("/nonexistent/path"),
			display.WithLocalTemplatePath("/nonexistent/local"),
			display.WithEmbeddedFS(fstest.MapFS{}),
		)

		req := httptest.NewRequest(http.MethodGet, "/display", nil)
		rec := httptest.NewRecorder()
		h.GetDisplay(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected status 404, got %d", rec.Code)
		}
		var errResp api.ErrorResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil || errResp.Status != "error" {
			t.Errorf("expected JSON error response for 404, got %v", rec.Body.String())
		}
	})
}

func TestGetStatic_MethodsAndTraversal(t *testing.T) {
	t.Parallel()

	h := display.NewHandler()

	// 1. OPTIONS method
	req := httptest.NewRequest(http.MethodOptions, "/static/js/sse.js", nil)
	rec := httptest.NewRecorder()
	h.GetStatic(rec, req, "js/sse.js")

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected status 204 for OPTIONS, got %d", rec.Code)
	}

	// 2. Disallowed method
	req = httptest.NewRequest(http.MethodPost, "/static/js/sse.js", nil)
	rec = httptest.NewRecorder()
	h.GetStatic(rec, req, "js/sse.js")

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405 for POST, got %d", rec.Code)
	}

	// 3. Path traversal attacks
	traversalPaths := []string{
		"",
		"..",
		"../secret.txt",
		"../../etc/passwd",
		"/etc/passwd",
		"\\windows\\system32",
		".",
		"./something",
	}

	for _, badPath := range traversalPaths {
		t.Run("Traversal_"+badPath, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, "/static/"+badPath, nil)
			rec := httptest.NewRecorder()
			h.GetStatic(rec, req, badPath)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("expected 400 Bad Request for path %q, got %d", badPath, rec.Code)
			}
			var errResp api.ErrorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil || errResp.Status != "error" {
				t.Errorf("expected JSON error response, got %v", rec.Body.String())
			}
		})
	}
}

func TestGetStatic_Resolutions(t *testing.T) {
	t.Parallel()

	t.Run("PrimaryStaticDir", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		jsPath := filepath.Join(tempDir, "app.js")
		if err := os.WriteFile(jsPath, []byte("console.log('primary');"), 0644); err != nil {
			t.Fatal(err)
		}

		h := display.NewHandler(
			display.WithStaticDir(tempDir),
			display.WithLocalStaticDir(""),
			display.WithEmbeddedFS(nil),
		)

		req := httptest.NewRequest(http.MethodGet, "/static/app.js", nil)
		rec := httptest.NewRecorder()
		h.GetStatic(rec, req, "app.js")

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "console.log('primary');") {
			t.Errorf("unexpected body: %s", rec.Body.String())
		}
	})

	t.Run("LocalStaticDirFallback", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		cssPath := filepath.Join(tempDir, "theme.css")
		if err := os.WriteFile(cssPath, []byte("body { background: #000; }"), 0644); err != nil {
			t.Fatal(err)
		}

		h := display.NewHandler(
			display.WithStaticDir("/nonexistent/static"),
			display.WithLocalStaticDir(tempDir),
			display.WithEmbeddedFS(nil),
		)

		req := httptest.NewRequest(http.MethodGet, "/static/theme.css", nil)
		rec := httptest.NewRecorder()
		h.GetStatic(rec, req, "theme.css")

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "body { background: #000; }") {
			t.Errorf("unexpected body: %s", rec.Body.String())
		}
	})

	t.Run("EmbeddedFSMimeTypes", func(t *testing.T) {
		t.Parallel()
		mockFS := fstest.MapFS{
			"static/test.js": &fstest.MapFile{
				Data: []byte("const x = 1;"),
			},
			"static/test.css": &fstest.MapFile{
				Data: []byte(".a { color: red; }"),
			},
			"static/test.svg": &fstest.MapFile{
				Data: []byte("<svg></svg>"),
			},
			"static/test.json": &fstest.MapFile{
				Data: []byte(`{"status":"ok"}`),
			},
			"static/test.png": &fstest.MapFile{
				Data: []byte{0x89, 'P', 'N', 'G'},
			},
			"static/test.webp": &fstest.MapFile{
				Data: []byte("RIFF....WEBP"),
			},
			"static/test.html": &fstest.MapFile{
				Data: []byte("<html>test</html>"),
			},
			"static/test.unknown": &fstest.MapFile{
				Data: []byte("binary data"),
			},
		}

		h := display.NewHandler(
			display.WithStaticDir("/nonexistent/static"),
			display.WithLocalStaticDir("/nonexistent/local"),
			display.WithEmbeddedFS(mockFS),
		)

		testCases := []struct {
			path         string
			expectedMime string
		}{
			{"test.js", "text/javascript; charset=utf-8"},
			{"test.css", "text/css; charset=utf-8"},
			{"test.svg", "image/svg+xml"},
			{"test.json", "application/json; charset=utf-8"},
			{"test.png", "image/png"},
			{"test.webp", "image/webp"},
			{"test.html", "text/html; charset=utf-8"},
			{"test.unknown", "application/octet-stream"},
		}

		for _, tc := range testCases {
			t.Run(tc.path, func(t *testing.T) {
				t.Parallel()
				req := httptest.NewRequest(http.MethodGet, "/static/"+tc.path, nil)
				rec := httptest.NewRecorder()
				h.GetStatic(rec, req, tc.path)

				if rec.Code != http.StatusOK {
					t.Fatalf("expected status 200, got %d", rec.Code)
				}
				if rec.Header().Get("Content-Type") != tc.expectedMime {
					t.Errorf("expected MIME %q, got %q", tc.expectedMime, rec.Header().Get("Content-Type"))
				}
			})
		}
	})

	t.Run("HEADMethod", func(t *testing.T) {
		t.Parallel()
		mockFS := fstest.MapFS{
			"static/test.js": &fstest.MapFile{
				Data: []byte("const x = 1;"),
			},
		}

		h := display.NewHandler(
			display.WithStaticDir("/nonexistent/static"),
			display.WithLocalStaticDir("/nonexistent/local"),
			display.WithEmbeddedFS(mockFS),
		)

		req := httptest.NewRequest(http.MethodHead, "/static/test.js", nil)
		rec := httptest.NewRecorder()
		h.GetStatic(rec, req, "test.js")

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
		if rec.Body.Len() != 0 {
			t.Errorf("expected empty body for HEAD, got %d bytes", rec.Body.Len())
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		t.Parallel()
		h := display.NewHandler(
			display.WithStaticDir("/nonexistent/static"),
			display.WithLocalStaticDir("/nonexistent/local"),
			display.WithEmbeddedFS(fstest.MapFS{}),
		)

		req := httptest.NewRequest(http.MethodGet, "/static/missing.js", nil)
		rec := httptest.NewRecorder()
		h.GetStatic(rec, req, "missing.js")

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected status 404, got %d", rec.Code)
		}
		var errResp api.ErrorResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil || errResp.Status != "error" {
			t.Errorf("expected JSON error response for 404, got %v", rec.Body.String())
		}
	})
}
