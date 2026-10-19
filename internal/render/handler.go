package render

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/azylman/mirrormere/internal/api"
)

const (
	// DefaultCSSPath is the standard filesystem path for the core HUD stylesheet.
	DefaultCSSPath = "web/static/css/hud.css"

	// DefaultCustomCSSPath is the standard volume mount location for custom CSS (for backward compatibility).
	DefaultCustomCSSPath = "/config/custom.css"
)

// HandlerOption configures Handler instances.
type HandlerOption func(*Handler)

// WithCSSPath sets the stylesheet disk path.
func WithCSSPath(path string) HandlerOption {
	return func(h *Handler) {
		h.cssPath = path
	}
}

// WithCustomCSSPath sets the optional custom override stylesheet path layered on top of the base stylesheet.
func WithCustomCSSPath(path string) HandlerOption {
	return func(h *Handler) {
		h.override = path
	}
}

// Handler serves HTTP endpoints for widget rendering, static package assets, and stylesheets.
type Handler struct {
	mu       sync.RWMutex
	engine   *Engine
	resolver PackageResolver
	cssPath  string
	override string
}

// NewHandler constructs a Handler.
func NewHandler(engine *Engine, resolver PackageResolver, opts ...HandlerOption) *Handler {
	h := &Handler{
		engine:   engine,
		resolver: resolver,
		cssPath:  DefaultCSSPath,
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// SetCSSPath dynamically updates the stylesheet path (e.g. on configuration reload).
func (h *Handler) SetCSSPath(path string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cssPath = path
}

// GetWidgetRender handles GET /api/widgets/{widget_id}/render requests. The optional
// "node" query parameter is the requesting display's voice node_id.
func (h *Handler) GetWidgetRender(w http.ResponseWriter, r *http.Request, widgetID string) {
	if !handleCORSAndMethod(w, r) {
		return
	}

	if h.engine == nil {
		writeJSONError(w, http.StatusInternalServerError, "rendering engine not configured")
		return
	}

	overrides := make(map[string]any)
	if dateParam := strings.TrimSpace(r.URL.Query().Get("date")); dateParam != "" {
		if _, err := time.Parse("2006-01-02", dateParam); err == nil {
			overrides["date"] = dateParam
		} else {
			writeJSONError(w, http.StatusBadRequest, "invalid date parameter: must be YYYY-MM-DD")
			return
		}
	}
	if viewParam := strings.TrimSpace(r.URL.Query().Get("view")); viewParam != "" {
		lowerView := strings.ToLower(viewParam)
		if lowerView == "day" || lowerView == "week" || lowerView == "month" {
			overrides["default_view"] = lowerView
		} else {
			writeJSONError(w, http.StatusBadRequest, "invalid view parameter: must be day, week, or month")
			return
		}
	}

	htmlBytes, err := h.engine.RenderWidgetWithOverrides(r.Context(), widgetID, r.URL.Query().Get("node"), overrides)
	if err != nil {
		var notFound WidgetNotFoundError
		if errors.As(err, &notFound) {
			writeJSONError(w, http.StatusNotFound, notFound.Error())
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "failed to render widget: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		if _, err := w.Write(htmlBytes); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// GetWidgetAsset handles GET /widget-types/{type}/assets/{path} requests with path traversal protection.
func (h *Handler) GetWidgetAsset(w http.ResponseWriter, r *http.Request, widgetType string, assetPath string) {
	if !handleCORSAndMethod(w, r) {
		return
	}

	// 1. Strict path traversal defense
	if assetPath == "" || strings.Contains(assetPath, "..") || filepath.IsAbs(assetPath) {
		writeJSONError(w, http.StatusBadRequest, "invalid asset path: path traversal detected")
		return
	}

	clean := filepath.Clean(assetPath)
	if clean == "." || strings.HasPrefix(clean, "..") || strings.HasPrefix(clean, "/") || strings.HasPrefix(clean, "\\") {
		writeJSONError(w, http.StatusBadRequest, "invalid asset path: path traversal detected")
		return
	}

	if h.resolver == nil {
		writeJSONError(w, http.StatusInternalServerError, "package resolver not configured")
		return
	}

	// 2. Resolve widget package
	pkg, err := h.resolver.LoadPackage(widgetType)
	if err != nil || pkg == nil || !pkg.HasAssets() {
		writeJSONError(w, http.StatusNotFound, "asset not found")
		return
	}

	// 3. Resolve and verify target file
	target := filepath.Join(pkg.AssetsDir, filepath.FromSlash(clean))
	rel, err := filepath.Rel(pkg.AssetsDir, target)
	if err != nil || strings.HasPrefix(rel, "..") || rel == "." {
		writeJSONError(w, http.StatusBadRequest, "invalid asset path: path traversal detected")
		return
	}

	fi, err := os.Stat(target)
	if err != nil || fi.IsDir() {
		writeJSONError(w, http.StatusNotFound, "asset not found")
		return
	}

	http.ServeFile(w, r, target)
}

// ServeStyle handles GET /style.css requests, serving the configured stylesheet from disk.
func (h *Handler) ServeStyle(w http.ResponseWriter, r *http.Request) {
	if !handleCORSAndMethod(w, r) {
		return
	}

	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")

	h.mu.RLock()
	cssPath, customCSSPath := h.cssPath, h.override
	h.mu.RUnlock()

	if cssPath == "" {
		writeJSONError(w, http.StatusNotFound, "stylesheet not configured")
		return
	}

	fi, err := os.Stat(cssPath)
	if err != nil || fi.IsDir() {
		writeJSONError(w, http.StatusNotFound, "stylesheet not found")
		return
	}

	if customFi, statErr := os.Stat(customCSSPath); customCSSPath != "" && statErr == nil && !customFi.IsDir() {
		if baseBytes, baseErr := os.ReadFile(cssPath); baseErr == nil {
			if customBytes, customErr := os.ReadFile(customCSSPath); customErr == nil {
				combined := append(append(baseBytes[:len(baseBytes):len(baseBytes)], '\n'), customBytes...)
				modTime := customFi.ModTime()
				if fi.ModTime().After(modTime) {
					modTime = fi.ModTime()
				}
				http.ServeContent(w, r, "style.css", modTime, bytes.NewReader(combined))
				return
			}
		}
	}

	http.ServeFile(w, r, cssPath)
}

func handleCORSAndMethod(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD, OPTIONS")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return false
	}
	return true
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
