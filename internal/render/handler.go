package render

import (
	_ "embed"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/azylman/mirrormere/internal/api"
)

//go:embed default_hud.css
var defaultEmbeddedHUDCSS []byte

const (
	// DefaultCustomCSSPath is the standard volume mount location for custom CSS.
	DefaultCustomCSSPath = "/config/custom.css"
	// DefaultCoreCSSPath is the standard container location for default hud.css.
	DefaultCoreCSSPath = "/app/web/static/css/hud.css"
)

// HandlerOption configures Handler instances.
type HandlerOption func(*Handler)

// WithCustomCSSPath overrides the custom stylesheet disk path.
func WithCustomCSSPath(path string) HandlerOption {
	return func(h *Handler) {
		h.customCSSPath = path
	}
}

// WithDefaultCSSPath overrides the default core stylesheet disk path.
func WithDefaultCSSPath(path string) HandlerOption {
	return func(h *Handler) {
		h.defaultCSSPath = path
	}
}

// WithDefaultCSSContent overrides the fallback embedded CSS content.
func WithDefaultCSSContent(content []byte) HandlerOption {
	return func(h *Handler) {
		h.defaultCSS = content
	}
}

// Handler serves HTTP endpoints for widget rendering, static package assets, and stylesheets.
type Handler struct {
	engine         *Engine
	resolver       PackageResolver
	customCSSPath  string
	defaultCSSPath string
	defaultCSS     []byte
}

// NewHandler constructs a Handler.
func NewHandler(engine *Engine, resolver PackageResolver, opts ...HandlerOption) *Handler {
	h := &Handler{
		engine:         engine,
		resolver:       resolver,
		customCSSPath:  DefaultCustomCSSPath,
		defaultCSSPath: DefaultCoreCSSPath,
		defaultCSS:     defaultEmbeddedHUDCSS,
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// GetWidgetRender handles GET /api/widgets/{widget_id}/render requests.
func (h *Handler) GetWidgetRender(w http.ResponseWriter, r *http.Request, widgetID string) {
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

	if h.engine == nil {
		writeJSONError(w, http.StatusInternalServerError, "rendering engine not configured")
		return
	}

	htmlBytes, err := h.engine.RenderWidget(r.Context(), widgetID)
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

// ServeStyle handles GET /style.css requests, serving custom.css if present or hud.css fallback.
func (h *Handler) ServeStyle(w http.ResponseWriter, r *http.Request) {
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

	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")

	// 1. Check volume-mounted custom stylesheet
	if h.customCSSPath != "" {
		if fi, err := os.Stat(h.customCSSPath); err == nil && !fi.IsDir() {
			http.ServeFile(w, r, h.customCSSPath)
			return
		}
	}

	// 2. Check default core stylesheet on disk
	if h.defaultCSSPath != "" {
		if fi, err := os.Stat(h.defaultCSSPath); err == nil && !fi.IsDir() {
			http.ServeFile(w, r, h.defaultCSSPath)
			return
		}
	}

	// 3. Serve embedded default CSS
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		if _, err := w.Write(h.defaultCSS); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
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
