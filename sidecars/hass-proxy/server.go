package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Server encapsulates the HTTP handler and dependencies for the Home Assistant Fast-Path Proxy.
type Server struct {
	client HAClient
	logger *slog.Logger
	mux    *http.ServeMux
}

// NewServer initializes a new Server with routing.
func NewServer(client HAClient, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}

	s := &Server{
		client: client,
		logger: logger,
		mux:    http.NewServeMux(),
	}

	s.routes()
	return s
}

// routes configures the HTTP routes.
func (s *Server) routes() {
	s.mux.HandleFunc("/healthz", s.handleHealthz)
	s.mux.HandleFunc("/intent", s.handleIntent)
}

// ServeHTTP delegates to the internal ServeMux.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// handleHealthz handles Docker and Nomad health probes.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("OK\n")); err != nil {
		s.logger.WarnContext(r.Context(), "failed to write healthz response", "error", err)
	}
}

// handleIntent processes the inbound voice intent request.
func (s *Server) handleIntent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 64*1024)) // 64KB max request
	if err != nil {
		s.logger.WarnContext(r.Context(), "failed to read request body", "error", err)
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var req IntentRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		s.logger.DebugContext(r.Context(), "malformed intent request JSON", "error", err)
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	// Empty text fast-fail: return 204 immediately
	if strings.TrimSpace(req.Text) == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	intentResp, matched, err := s.client.ProcessIntent(r.Context(), req)
	if err != nil {
		if errors.Is(r.Context().Err(), context.Canceled) {
			s.logger.DebugContext(r.Context(), "client canceled request before completion")
			return
		}
		// Treat errors as no-match per LAMMAS contract
		s.logger.WarnContext(r.Context(), "intent processing encountered error, treating as no match", "error", err)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if !matched || intentResp == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Successful match: stream SSE only per LAMMAS contract
	s.writeSSE(w, r, req.TurnID, intentResp.Speech)
}

// writeSSE streams the matched intent response using Server-Sent Events.
func (s *Server) writeSSE(w http.ResponseWriter, r *http.Request, turnID string, speech string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)

	writeEvent := func(eventType string, data any) bool {
		if r.Context().Err() != nil {
			return false
		}
		jsonData, err := json.Marshal(data)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, jsonData); err != nil {
			return false
		}
		if ok && flusher != nil {
			flusher.Flush()
		}
		return true
	}

	// Emit turn, sentence, reply, done events per LAMMAS contract
	if !writeEvent("turn", map[string]string{"turn_id": turnID}) {
		return
	}
	if !writeEvent("sentence", map[string]string{"text": speech}) {
		return
	}
	if !writeEvent("reply", map[string]string{"reply": speech, "tts_engine": "none"}) {
		return
	}
	_ = writeEvent("done", map[string]string{"turn_id": turnID})
}

// BuildHTTPServer constructs an http.Server with configured timeouts.
func BuildHTTPServer(cfg ServerConfig, handler http.Handler) *http.Server {
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	readTimeout := time.Duration(cfg.ReadTimeoutSeconds) * time.Second
	writeTimeout := time.Duration(cfg.WriteTimeoutSeconds) * time.Second

	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       60 * time.Second,
	}
}
