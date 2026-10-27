package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/coverage"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/azylman/mirrormere/internal/covflush"
)

// MediaActionSender abstracts media transport execution for the HTTP server.
type MediaActionSender interface {
	SendMediaAction(ctx context.Context, action string) error
	Seek(ctx context.Context, targetTime float64) error
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
	writeCountersDirMu sync.RWMutex
	writeMetaDirFn     = coverage.WriteMetaDir
	writeCountersDirFn = coverage.WriteCountersDir
)

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
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	mux := http.NewServeMux()
	s := &ActionServer{cfg: cfg, client: client, mux: mux, logger: cfg.Logger}
	mux.HandleFunc("/action", s.handleAction)
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/debug/coverage/flush", s.handleCoverageFlush)
	s.server = &http.Server{Addr: fmt.Sprintf("%s:%d", cfg.Host, cfg.Port), Handler: mux, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 1 << 20}
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

func writeActionError(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	if errors.Is(err, ErrInvalidAction) {
		code = http.StatusBadRequest
	} else if errors.Is(err, ErrNotConnected) || errors.Is(err, ErrNoActiveMediaSession) {
		code = http.StatusBadGateway
	}
	writeJSONError(w, code, err.Error())
}

func (s *ActionServer) handleAction(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	h.Set("Access-Control-Allow-Headers", "Content-Type")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		h.Set("Allow", "POST, OPTIONS")
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
	var err error
	switch req.Action {
	case "wake":
		err = s.client.Wake(r.Context())
	case "seek":
		var targetTime float64
		if targetTime, err = extractTargetTime(req.Value); err != nil || targetTime < 0 {
			writeJSONError(w, http.StatusBadRequest, "invalid seek target time: must be a non-negative number")
			return
		}
		err = s.client.Seek(r.Context(), targetTime)
	default:
		err = s.client.SendMediaAction(r.Context(), req.Action)
	}
	if err != nil {
		writeActionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *ActionServer) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		return
	}
	st := s.client.GetStatus()
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "chromecast_connected": st.Connected, "app_id": st.AppID, "player_state": st.PlayerState})
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
	flushMu.Lock()
	defer flushMu.Unlock()

	writeCountersDirMu.RLock()
	metaFn, fn := writeMetaDirFn, writeCountersDirFn
	writeCountersDirMu.RUnlock()

	var buf bytes.Buffer
	if err := covflush.WriteArchive(&buf, metaFn, fn); err != nil {
		s.logger.Error("coverage flush failed", "error", err)
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/x-tar")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(buf.Bytes()); err != nil {
		return
	}
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"status": "error", "error": msg})
}

func extractTargetTime(val any) (float64, error) {
	if val == nil {
		return 0, errors.New("missing seek value")
	}
	if p, ok := val.(*interface{}); ok {
		if p == nil || *p == nil {
			return 0, errors.New("missing seek value")
		}
		return extractTargetTime(*p)
	}
	switch v := val.(type) {
	case float64:
		return v, nil
	case float32:
		return float64(v), nil
	case int, int32, int64:
		f, err := strconv.ParseFloat(fmt.Sprint(v), 64)
		return f, err
	case json.Number:
		return v.Float64()
	case string:
		return strconv.ParseFloat(strings.TrimSpace(v), 64)
	default:
		return 0, fmt.Errorf("unsupported seek value type: %T", val)
	}
}
