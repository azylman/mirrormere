package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime/coverage"
	"strings"
	"sync"
	"time"

	"github.com/azylman/mirrormere/internal/covflush"
)

var (
	flushMu            sync.Mutex
	writeCountersDirMu sync.RWMutex
	writeMetaDirFn     = coverage.WriteMetaDir
	writeCountersDirFn = coverage.WriteCountersDir
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
	s := &Server{client: client, logger: logger, mux: http.NewServeMux()}
	s.mux.HandleFunc("/healthz", s.handleHealthz)
	s.mux.HandleFunc("/intent", s.handleIntent)
	s.mux.HandleFunc("/debug/coverage/flush", s.handleCoverageFlush)
	return s
}

// ServeHTTP delegates to the internal ServeMux.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("OK\n")); err != nil {
		s.logger.DebugContext(r.Context(), "healthz write failed", "error", err)
	}
}

func (s *Server) handleCoverageFlush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	flushMu.Lock()
	defer flushMu.Unlock()

	writeCountersDirMu.RLock()
	metaFn, fn := writeMetaDirFn, writeCountersDirFn
	writeCountersDirMu.RUnlock()

	var buf bytes.Buffer
	if err := covflush.WriteArchive(&buf, metaFn, fn); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/x-tar")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(buf.Bytes()); err != nil {
		return
	}
}

func (s *Server) handleIntent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
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
		s.logger.WarnContext(r.Context(), "intent processing encountered error, treating as no match", "error", err)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !matched || intentResp == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.writeSSE(w, r, req.TurnID, intentResp.Speech)
}

func (s *Server) writeSSE(w http.ResponseWriter, r *http.Request, turnID, speech string) {
	var flusher http.Flusher
	if f, ok := w.(http.Flusher); ok {
		flusher = f
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

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
		if flusher != nil {
			flusher.Flush()
		}
		return true
	}

	if writeEvent("turn", map[string]string{"turn_id": turnID}) &&
		writeEvent("sentence", map[string]string{"text": speech}) &&
		writeEvent("reply", map[string]string{"reply": speech, "tts_engine": "none"}) {
		_ = writeEvent("done", map[string]string{"turn_id": turnID})
	}
}

// BuildHTTPServer constructs an http.Server with configured timeouts.
func BuildHTTPServer(cfg ServerConfig, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Handler:           handler,
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       time.Duration(cfg.ReadTimeoutSeconds) * time.Second,
		WriteTimeout:      time.Duration(cfg.WriteTimeoutSeconds) * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}
