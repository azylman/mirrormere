package display

import (
	"bytes"
	"encoding/json"
	"html/template"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/azylman/mirrormere/internal/api"
)

const (
	// DefaultContainerTemplatePath is the standard container path for the dashboard HTML shell.
	DefaultContainerTemplatePath = "/app/web/templates/display.html"
	// DefaultContainerStaticDir is the standard container path for static web assets.
	DefaultContainerStaticDir = "/app/web/static"

	// LocalFallbackTemplatePath is the relative development path for display.html.
	LocalFallbackTemplatePath = "web/templates/display.html"
	// LocalFallbackStaticDir is the relative development path for static web assets.
	LocalFallbackStaticDir = "web/static"
)

// HandlerOption configures Handler instances.
type HandlerOption func(*Handler)

// WithTemplatePath sets the primary container template path for display.html.
func WithTemplatePath(path string) HandlerOption {
	return func(h *Handler) {
		h.templatePath = path
	}
}

// WithLocalTemplatePath sets the local development fallback template path.
func WithLocalTemplatePath(path string) HandlerOption {
	return func(h *Handler) {
		h.localTemplatePath = path
	}
}

// WithStaticDir sets the primary container directory for static assets.
func WithStaticDir(dir string) HandlerOption {
	return func(h *Handler) {
		h.staticDir = dir
	}
}

// WithLocalStaticDir sets the local development fallback directory for static assets.
func WithLocalStaticDir(dir string) HandlerOption {
	return func(h *Handler) {
		h.localStaticDir = dir
	}
}

// WithTemplateContent sets an in-memory template override directly (primarily for testing).
func WithTemplateContent(content []byte) HandlerOption {
	return func(h *Handler) {
		h.templateContent = content
	}
}

// WithEmbeddedTemplate is an alias for WithTemplateContent for backward compatibility.
func WithEmbeddedTemplate(content []byte) HandlerOption {
	return WithTemplateContent(content)
}

// WithTimezone sets the static fallback household IANA timezone.
func WithTimezone(tz string) HandlerOption {
	return func(h *Handler) {
		h.timezone = tz
	}
}

// WithTimezoneProvider sets a dynamic function returning the current household IANA timezone.
func WithTimezoneProvider(provider func() string) HandlerOption {
	return func(h *Handler) {
		h.timezoneProvider = provider
	}
}

// Handler serves the web display HUD shell and static web runtime assets.
type Handler struct {
	templatePath      string
	localTemplatePath string
	staticDir         string
	localStaticDir    string
	templateContent   []byte
	mu                sync.RWMutex
	timezone          string
	timezoneProvider  func() string
}

// SetTimezone updates the household timezone dynamically.
func (h *Handler) SetTimezone(tz string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.timezone = tz
}

// Timezone returns the effective household timezone, defaulting to "UTC".
func (h *Handler) Timezone() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.timezoneProvider != nil {
		if tz := h.timezoneProvider(); tz != "" {
			return tz
		}
	}
	if h.timezone != "" {
		return h.timezone
	}
	return "UTC"
}

// DisplayTemplateData holds context parameters passed to display.html template.
type DisplayTemplateData struct {
	Timezone string
}

// NewHandler constructs a Handler with default filesystem paths.
func NewHandler(opts ...HandlerOption) *Handler {
	h := &Handler{
		templatePath:      DefaultContainerTemplatePath,
		localTemplatePath: LocalFallbackTemplatePath,
		staticDir:         DefaultContainerStaticDir,
		localStaticDir:    LocalFallbackStaticDir,
	}

	for _, opt := range opts {
		opt(h)
	}

	return h
}

// GetDisplay handles GET /display requests, serving display.html with household timezone context.
func (h *Handler) GetDisplay(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD, OPTIONS")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")

	var raw []byte
	var found bool

	// 1. Check primary container template path on disk
	if h.templatePath != "" {
		if fi, err := os.Stat(h.templatePath); err == nil && !fi.IsDir() {
			if data, err := os.ReadFile(h.templatePath); err == nil {
				raw = data
				found = true
			}
		}
	}

	// 2. Explicit template content override (primarily unit tests)
	if !found && len(h.templateContent) > 0 {
		raw = h.templateContent
		found = true
	}

	// 3. Check local repo development template path on disk
	if !found && h.localTemplatePath != "" {
		candidates := []string{
			h.localTemplatePath,
			filepath.Join("..", "..", h.localTemplatePath),
		}
		for _, c := range candidates {
			if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
				if data, err := os.ReadFile(c); err == nil {
					raw = data
					found = true
					break
				}
			}
		}
	}

	// 4. Not found
	if !found {
		writeJSONError(w, http.StatusNotFound, "display template not found")
		return
	}

	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}

	out := raw
	tmpl, err := template.New("display.html").Parse(string(raw))
	if err == nil {
		var buf bytes.Buffer
		data := DisplayTemplateData{
			Timezone: h.Timezone(),
		}
		if err := tmpl.Execute(&buf, data); err == nil {
			out = buf.Bytes()
		}
	}

	if _, err := w.Write(out); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// GetStatic handles GET /static/{path...} requests with strict path traversal defenses.
func (h *Handler) GetStatic(w http.ResponseWriter, r *http.Request, assetPath string) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD, OPTIONS")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// 1. Strict path traversal defense
	if assetPath == "" || strings.Contains(assetPath, "..") || strings.HasPrefix(assetPath, ".") || filepath.IsAbs(assetPath) {
		writeJSONError(w, http.StatusBadRequest, "invalid asset path: path traversal detected")
		return
	}

	clean := filepath.Clean(assetPath)
	if clean == "." || strings.HasPrefix(clean, "..") || strings.HasPrefix(clean, "/") || strings.HasPrefix(clean, "\\") {
		writeJSONError(w, http.StatusBadRequest, "invalid asset path: path traversal detected")
		return
	}

	// 2. Check primary container static dir on disk
	if h.staticDir != "" {
		fullPath := filepath.Join(h.staticDir, clean)
		rel, err := filepath.Rel(h.staticDir, fullPath)
		if err == nil && !strings.HasPrefix(rel, "..") {
			if fi, err := os.Stat(fullPath); err == nil && !fi.IsDir() {
				w.Header().Set("Content-Type", detectContentType(clean))
				w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
				http.ServeFile(w, r, fullPath)
				return
			}
		}
	}

	// 3. Check local development fallback static dir on disk
	if h.localStaticDir != "" {
		candidates := []string{
			h.localStaticDir,
			filepath.Join("..", "..", h.localStaticDir),
		}
		for _, baseDir := range candidates {
			fullPath := filepath.Join(baseDir, clean)
			rel, err := filepath.Rel(baseDir, fullPath)
			if err == nil && !strings.HasPrefix(rel, "..") {
				if fi, err := os.Stat(fullPath); err == nil && !fi.IsDir() {
					w.Header().Set("Content-Type", detectContentType(clean))
					w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
					http.ServeFile(w, r, fullPath)
					return
				}
			}
		}
	}

	// 4. Asset not found
	writeJSONError(w, http.StatusNotFound, "static asset not found")
}

func detectContentType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".js":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".json":
		return "application/json; charset=utf-8"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".html":
		return "text/html; charset=utf-8"
	default:
		if t := mime.TypeByExtension(ext); t != "" {
			return t
		}
		return "application/octet-stream"
	}
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(api.ErrorResponse{
		Status: "error",
		Error:  msg,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
