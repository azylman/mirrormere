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

// Config encapsulates configuration for the HTTP server.
type Config struct {
	Host              string
	Port              int
	ReadTimeout       time.Duration
	ReadHeaderTimeout time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
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
	cfg        Config
	mux        *http.ServeMux
	httpServer *http.Server
	ready      chan struct{}
	readyOnce  sync.Once
	mu         sync.RWMutex
	addr       string
}

// New constructs a configured Server instance.
func New(cfg Config) *Server {
	cfg.ApplyDefaults()

	s := &Server{
		cfg:   cfg,
		mux:   http.NewServeMux(),
		ready: make(chan struct{}),
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

func (s *Server) setupRoutes() {
	s.mux.HandleFunc("/healthz", s.handleHealth)
	s.mux.HandleFunc("/health", s.handleHealth)
	s.mux.HandleFunc("/", s.handleRoot)
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
