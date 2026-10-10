package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"runtime/coverage"
	"strings"
	"sync"
	"time"
)

// MediaActionSender abstracts media transport execution for the HTTP server.
type MediaActionSender interface {
	SendMediaAction(ctx context.Context, action string) error
	Wake(ctx context.Context) error
	GetStatus() StatusSnapshot
}

// ServerConfig configures the internal webhook HTTP server.
type ServerConfig struct {
	Host   string
	Port   int
	Logger *slog.Logger
}

var (
	flushMu            sync.Mutex
	lastFlushTime      time.Time
	nowFn              = time.Now
	writeCountersDirMu sync.RWMutex
	writeCountersDirFn = coverage.WriteCountersDir
)

// SetNowFnForTest overrides the time provider for testing debounce without sleeps.
func SetNowFnForTest(fn func() time.Time) func() {
	flushMu.Lock()
	prev := nowFn
	nowFn = fn
	flushMu.Unlock()
	return func() {
		flushMu.Lock()
		nowFn = prev
		flushMu.Unlock()
	}
}

func SetWriteCountersDirForTest(fn func(string) error) func() {
	writeCountersDirMu.Lock()
	prev := writeCountersDirFn
	writeCountersDirFn = fn
	writeCountersDirMu.Unlock()
	return func() {
		writeCountersDirMu.Lock()
		writeCountersDirFn = prev
		writeCountersDirMu.Unlock()
	}
}

// ActionServer handles incoming transport webhook actions and health probes.
type ActionServer struct {
	cfg    ServerConfig
	client MediaActionSender
	mux    *http.ServeMux
	server *http.Server
	logger *slog.Logger
}

// ActionWebhookRequest represents the payload forwarded by Core POST /api/video/action.
type ActionWebhookRequest struct {
	ID     string `json:"id"`
	Action string `json:"action"`
	Value  any    `json:"value,omitempty"`
}

// NewActionServer constructs an ActionServer backed by client.
func NewActionServer(cfg ServerConfig, client MediaActionSender) *ActionServer {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	mux := http.NewServeMux()
	s := &ActionServer{
		cfg:    cfg,
		client: client,
		mux:    mux,
		logger: logger,
	}

	mux.HandleFunc("/action", s.handleAction)
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/debug/coverage/flush", s.handleCoverageFlush)

	bindAddr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	s.server = &http.Server{
		Addr:              bindAddr,
		Handler:           mux,
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1MB Slowloris defense
	}

	return s
}

// Serve starts listening and serving HTTP requests on listener.
func (s *ActionServer) Serve(listener net.Listener) error {
	return s.server.Serve(listener)
}

// Shutdown gracefully stops the HTTP server.
func (s *ActionServer) Shutdown(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

func (s *ActionServer) handleAction(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, OPTIONS")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req ActionWebhookRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request payload: malformed JSON")
		return
	}

	if strings.TrimSpace(req.ID) == "" || strings.TrimSpace(req.Action) == "" {
		writeJSONError(w, http.StatusBadRequest, "id and action are required")
		return
	}

	if req.ID != "chromecast" {
		writeJSONError(w, http.StatusNotFound, "stream not active")
		return
	}

	if req.Action == "wake" {
		err := s.client.Wake(r.Context())
		if err != nil {
			if errors.Is(err, ErrNotConnected) {
				writeJSONError(w, http.StatusBadGateway, err.Error())
				return
			}
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{
			"status": "ok",
		})
		return
	}

	err := s.client.SendMediaAction(r.Context(), req.Action)
	if err != nil {
		if errors.Is(err, ErrInvalidAction) {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		if errors.Is(err, ErrNotConnected) || errors.Is(err, ErrNoActiveMediaSession) {
			writeJSONError(w, http.StatusBadGateway, err.Error())
			return
		}

		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
	})
}

func (s *ActionServer) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	status := s.client.GetStatus()
	resp := map[string]any{
		"status":               "ok",
		"chromecast_connected": status.Connected,
		"app_id":               status.AppID,
		"player_state":         status.PlayerState,
	}

	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *ActionServer) handleCoverageFlush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if dir := os.Getenv("GOCOVERDIR"); dir != "" {
		flushMu.Lock()
		now := nowFn()
		if now.Sub(lastFlushTime) < time.Second {
			flushMu.Unlock()
			writeJSON(w, http.StatusOK, map[string]string{"status": "debounced"})
			return
		}
		lastFlushTime = now
		writeCountersDirMu.RLock()
		fn := writeCountersDirFn
		writeCountersDirMu.RUnlock()
		err := fn(dir)
		flushMu.Unlock()

		if err != nil {
			s.logger.Error("coverage flush failed", "error", err)
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
	})
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{
		"status": "error",
		"error":  msg,
	})
}
