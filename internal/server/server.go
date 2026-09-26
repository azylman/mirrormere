package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	// DefaultPort is the standard HTTP port for mirrormere daemon.
	DefaultPort = 8080
	// DefaultHost is the standard bind host.
	DefaultHost = "0.0.0.0"
	// DefaultReadTimeout covers request body reading.
	DefaultReadTimeout = 5 * time.Second
	// DefaultReadHeaderTimeout mitigates Slowloris attacks.
	DefaultReadHeaderTimeout = 3 * time.Second
	// DefaultWriteTimeout covers response transmission.
	DefaultWriteTimeout = 10 * time.Second
	// DefaultIdleTimeout handles keep-alive connections.
	DefaultIdleTimeout = 120 * time.Second

	// Version of the mirrormere service.
	Version = "0.1.0"
)

// ScreenHandler handles screen navigation and pause endpoints matching OpenAPI specifications.
type ScreenHandler interface {
	PostScreenAdvance(w http.ResponseWriter, r *http.Request)
	PostScreenPause(w http.ResponseWriter, r *http.Request)
	PostScreenSelect(w http.ResponseWriter, r *http.Request)
}

// RenderHandler handles widget rendering, package static assets, and stylesheet endpoints.
type RenderHandler interface {
	GetWidgetRender(w http.ResponseWriter, r *http.Request, widgetID string)
	GetWidgetAsset(w http.ResponseWriter, r *http.Request, widgetType string, assetPath string)
	ServeStyle(w http.ResponseWriter, r *http.Request)
}

// PushHandler handles realtime widget push webhooks matching OpenAPI specifications.
type PushHandler interface {
	PostWidgetPush(w http.ResponseWriter, r *http.Request, widgetID string)
}

// DisplayHandler handles display UI and static web runtime asset endpoints.
type DisplayHandler interface {
	GetDisplay(w http.ResponseWriter, r *http.Request)
	GetStatic(w http.ResponseWriter, r *http.Request, path string)
}

// Config encapsulates configuration for the HTTP server.
type Config struct {
	Host              string
	Port              int
	ReadTimeout       time.Duration
	ReadHeaderTimeout time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	EventsHandler     http.Handler
	ScreenHandler     ScreenHandler
	RenderHandler     RenderHandler
	PushHandler       PushHandler
	DisplayHandler    DisplayHandler
}

// ApplyDefaults sets fallback values for any unspecified configuration fields.
// If Port is negative (e.g. -1), it is mapped to 0 (ephemeral port).
// If Port is 0, it defaults to DefaultPort (8080).
func (c *Config) ApplyDefaults() {
	if c.Host == "" {
		c.Host = DefaultHost
	}
	if c.Port < 0 {
		c.Port = 0
	} else if c.Port == 0 {
		c.Port = DefaultPort
	}
	if c.ReadTimeout <= 0 {
		c.ReadTimeout = DefaultReadTimeout
	}
	if c.ReadHeaderTimeout <= 0 {
		c.ReadHeaderTimeout = DefaultReadHeaderTimeout
	}
	if c.WriteTimeout <= 0 {
		c.WriteTimeout = DefaultWriteTimeout
	}
	if c.IdleTimeout <= 0 {
		c.IdleTimeout = DefaultIdleTimeout
	}
}

// HealthResponse represents the payload returned by healthcheck probes.
type HealthResponse struct {
	Status string `json:"status"`
}

// RootResponse represents the service identification payload.
type RootResponse struct {
	Service string `json:"service"`
	Status  string `json:"status"`
	Version string `json:"version"`
}

// Server wraps http.Server with lifecycle signaling and healthcheck routing.
type Server struct {
	cfg            Config
	mux            *http.ServeMux
	httpServer     *http.Server
	ready          chan struct{}
	readyOnce      sync.Once
	mu             sync.RWMutex
	addr           string
	eventsHandler  http.Handler
	screenHandler  ScreenHandler
	renderHandler  RenderHandler
	pushHandler    PushHandler
	displayHandler DisplayHandler
}

// New constructs a configured Server instance.
func New(cfg Config) *Server {
	cfg.ApplyDefaults()

	s := &Server{
		cfg:            cfg,
		mux:            http.NewServeMux(),
		ready:          make(chan struct{}),
		eventsHandler:  cfg.EventsHandler,
		screenHandler:  cfg.ScreenHandler,
		renderHandler:  cfg.RenderHandler,
		pushHandler:    cfg.PushHandler,
		displayHandler: cfg.DisplayHandler,
	}

	s.setupRoutes()

	s.httpServer = &http.Server{
		Handler:           s.mux,
		ReadTimeout:       s.cfg.ReadTimeout,
		ReadHeaderTimeout: s.cfg.ReadHeaderTimeout,
		WriteTimeout:      s.cfg.WriteTimeout,
		IdleTimeout:       s.cfg.IdleTimeout,
	}

	return s
}

// Ready returns a channel closed as soon as the server listener is bound.
func (s *Server) Ready() <-chan struct{} {
	return s.ready
}

// Addr returns the network address the server is actively listening on.
func (s *Server) Addr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.addr
}

// Routes returns the configured http.Handler for in-memory testing.
func (s *Server) Routes() http.Handler {
	return s.mux
}

// RegisterEventsHandler dynamically registers or replaces the /api/events HTTP handler.
func (s *Server) RegisterEventsHandler(h http.Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.eventsHandler = h
}

// EventsHandler returns the currently registered events handler.
func (s *Server) EventsHandler() http.Handler {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.eventsHandler
}

// RegisterScreenHandler dynamically registers or replaces the screen navigation handler.
func (s *Server) RegisterScreenHandler(h ScreenHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.screenHandler = h
}

// ScreenHandler returns the currently registered screen handler.
func (s *Server) ScreenHandler() ScreenHandler {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.screenHandler
}

// RegisterRenderHandler dynamically registers or replaces the render and asset handler.
func (s *Server) RegisterRenderHandler(h RenderHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.renderHandler = h
}

// RenderHandler returns the currently registered render handler.
func (s *Server) RenderHandler() RenderHandler {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.renderHandler
}

// RegisterPushHandler dynamically registers or replaces the widget push webhook handler.
func (s *Server) RegisterPushHandler(h PushHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pushHandler = h
}

// PushHandler returns the currently registered push handler.
func (s *Server) PushHandler() PushHandler {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pushHandler
}

// RegisterDisplayHandler dynamically registers or replaces the display and static asset handler.
func (s *Server) RegisterDisplayHandler(h DisplayHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.displayHandler = h
}

// DisplayHandler returns the currently registered display handler.
func (s *Server) DisplayHandler() DisplayHandler {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.displayHandler
}

func (s *Server) setupRoutes() {
	s.mux.HandleFunc("/healthz", s.handleHealth)
	s.mux.HandleFunc("/health", s.handleHealth)
	s.mux.HandleFunc("/api/events", s.handleEvents)
	s.mux.HandleFunc("/api/screen/select", s.handleScreenSelect)
	s.mux.HandleFunc("/api/screen/advance", s.handleScreenAdvance)
	s.mux.HandleFunc("/api/screen/pause", s.handleScreenPause)
	s.mux.HandleFunc("/api/widgets/{widget_id}/render", s.handleWidgetRender)
	s.mux.HandleFunc("/api/widgets/{widget_id}/push", s.handleWidgetPush)
	s.mux.HandleFunc("/widget-types/{type}/assets/{path...}", s.handleWidgetAsset)
	s.mux.HandleFunc("/style.css", s.handleStyle)
	s.mux.HandleFunc("/display", s.handleDisplay)
	s.mux.HandleFunc("/static/{path...}", s.handleStatic)
	s.mux.HandleFunc("/", s.handleRoot)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	h := s.eventsHandler
	s.mu.RUnlock()
	if h == nil {
		http.NotFound(w, r)
		return
	}
	h.ServeHTTP(w, r)
}

func (s *Server) handleScreenSelect(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	h := s.screenHandler
	s.mu.RUnlock()
	if h == nil {
		http.NotFound(w, r)
		return
	}
	h.PostScreenSelect(w, r)
}

func (s *Server) handleScreenAdvance(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	h := s.screenHandler
	s.mu.RUnlock()
	if h == nil {
		http.NotFound(w, r)
		return
	}
	h.PostScreenAdvance(w, r)
}

func (s *Server) handleScreenPause(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	h := s.screenHandler
	s.mu.RUnlock()
	if h == nil {
		http.NotFound(w, r)
		return
	}
	h.PostScreenPause(w, r)
}

func (s *Server) handleWidgetRender(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	h := s.renderHandler
	s.mu.RUnlock()
	if h == nil {
		http.NotFound(w, r)
		return
	}
	widgetID := r.PathValue("widget_id")
	h.GetWidgetRender(w, r, widgetID)
}

func (s *Server) handleWidgetPush(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	h := s.pushHandler
	s.mu.RUnlock()
	if h == nil {
		http.NotFound(w, r)
		return
	}
	widgetID := r.PathValue("widget_id")
	h.PostWidgetPush(w, r, widgetID)
}

func (s *Server) handleWidgetAsset(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	h := s.renderHandler
	s.mu.RUnlock()
	if h == nil {
		http.NotFound(w, r)
		return
	}
	pType := r.PathValue("type")
	path := r.PathValue("path")
	h.GetWidgetAsset(w, r, pType, path)
}

func (s *Server) handleStyle(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	h := s.renderHandler
	s.mu.RUnlock()
	if h == nil {
		http.NotFound(w, r)
		return
	}
	h.ServeStyle(w, r)
}

func (s *Server) handleDisplay(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	h := s.displayHandler
	s.mu.RUnlock()
	if h == nil {
		http.NotFound(w, r)
		return
	}
	h.GetDisplay(w, r)
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	h := s.displayHandler
	s.mu.RUnlock()
	if h == nil {
		http.NotFound(w, r)
		return
	}
	path := r.PathValue("path")
	h.GetStatic(w, r, path)
}


func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	// CORS Headers for client probes & autoheal
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "*")

	switch r.Method {
	case http.MethodOptions:
		w.WriteHeader(http.StatusNoContent)
	case http.MethodHead:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(HealthResponse{Status: "ok"}); err != nil {
			http.Error(w, "failed to encode response", http.StatusInternalServerError)
		}
	default:
		w.Header().Set("Allow", "GET, HEAD, OPTIONS")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD, OPTIONS")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		if err := json.NewEncoder(w).Encode(RootResponse{
			Service: "mirrormere",
			Status:  "running",
			Version: Version,
		}); err != nil {
			http.Error(w, "failed to encode response", http.StatusInternalServerError)
		}
	}
}

// Serve accepts connections on the given net.Listener.
func (s *Server) Serve(l net.Listener) error {
	s.mu.Lock()
	s.addr = l.Addr().String()
	s.mu.Unlock()

	s.readyOnce.Do(func() {
		close(s.ready)
	})

	err := s.httpServer.Serve(l)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// ListenAndServe binds to cfg.Host:cfg.Port and serves requests.
func (s *Server) ListenAndServe() error {
	bindAddr := fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port)
	l, err := net.Listen("tcp", bindAddr)
	if err != nil {
		return fmt.Errorf("failed to bind address %s: %w", bindAddr, err)
	}
	return s.Serve(l)
}

// Shutdown gracefully stops the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}
