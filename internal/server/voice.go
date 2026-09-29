package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/azylman/mirrormere/internal/api"
	"github.com/azylman/mirrormere/internal/events"
	"github.com/azylman/mirrormere/internal/voice"
)

// VoiceCoordinator abstracts voice coordinator state manipulation.
type VoiceCoordinator interface {
	GetState() events.VoiceStateData
	SetState(state string, transcript, reply, ttsEngine *string) (events.VoiceStateData, error)
}

// VoiceHub abstracts voice interaction pipeline coordination.
type VoiceHub interface {
	IsEnabled() bool
	Interact(ctx context.Context, audio io.Reader, nodeID, sessionID string, sink voice.SSEEventSink, timings voice.EdgeTimings) error
}

// DefaultVoiceHandler serves OpenAPI /api/voice/state, /api/voice/interact, and /api/voice/heartbeat endpoints.
type DefaultVoiceHandler struct {
	coord   VoiceCoordinator
	hub     VoiceHub
	metrics *voice.Metrics
}

// DefaultVoiceHandlerOption configures DefaultVoiceHandler.
type DefaultVoiceHandlerOption func(*DefaultVoiceHandler)

// WithVoiceMetrics configures the Prometheus voice metrics collector for the handler.
func WithVoiceMetrics(m *voice.Metrics) DefaultVoiceHandlerOption {
	return func(h *DefaultVoiceHandler) {
		h.metrics = m
	}
}

// NewDefaultVoiceHandler constructs a DefaultVoiceHandler backed by coord and hub.
func NewDefaultVoiceHandler(coord VoiceCoordinator, hub VoiceHub, opts ...DefaultVoiceHandlerOption) *DefaultVoiceHandler {
	h := &DefaultVoiceHandler{coord: coord, hub: hub}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// VoiceHeartbeatRequest represents the payload from edge device heartbeats.
type VoiceHeartbeatRequest struct {
	NodeID          string   `json:"node_id"`
	AmbientRMSDBFS  *float64 `json:"ambient_rms_dbfs,omitempty"`
	FalseWakes      *int     `json:"false_wakes,omitempty"`
	LastPlaybackSec *float64 `json:"last_playback_sec,omitempty"`
}

// PostVoiceState handles POST /api/voice/state.
func (h *DefaultVoiceHandler) PostVoiceState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, OPTIONS")
		writeVoiceError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if h.coord == nil {
		writeVoiceError(w, http.StatusInternalServerError, "voice coordinator not configured")
		return
	}

	var req api.VoiceStateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeVoiceError(w, http.StatusBadRequest, "invalid voice state request payload: "+err.Error())
		return
	}

	stateStr := string(req.State)
	if !voice.IsValidState(stateStr) {
		writeVoiceError(w, http.StatusBadRequest, voice.ErrInvalidState.Error())
		return
	}

	if _, err := h.coord.SetState(stateStr, req.Transcript, req.Reply, req.TtsEngine); err != nil {
		writeVoiceError(w, http.StatusBadRequest, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(api.VoiceStateResponse{
		Status: "ok",
	}); err != nil {
		slog.Error("failed to encode voice state response", "error", err)
	}
}

// PostVoiceHeartbeat handles POST /api/voice/heartbeat.
func (h *DefaultVoiceHandler) PostVoiceHeartbeat(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, OPTIONS")
		writeVoiceError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req VoiceHeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeVoiceError(w, http.StatusBadRequest, "invalid heartbeat payload: "+err.Error())
		return
	}

	nodeID := strings.TrimSpace(req.NodeID)
	if nodeID == "" {
		writeVoiceError(w, http.StatusBadRequest, "missing required 'node_id' field")
		return
	}

	if h.metrics != nil {
		h.metrics.RecordEdgeHeartbeat(nodeID)
		if req.AmbientRMSDBFS != nil {
			h.metrics.RecordAmbientRMS(nodeID, *req.AmbientRMSDBFS)
		}
		if req.FalseWakes != nil && *req.FalseWakes > 0 {
			for i := 0; i < *req.FalseWakes; i++ {
				h.metrics.RecordFalseWake(nodeID)
			}
		}
		if req.LastPlaybackSec != nil && *req.LastPlaybackSec > 0 {
			h.metrics.RecordPlaybackDuration(nodeID, *req.LastPlaybackSec)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); err != nil {
		slog.Error("failed to encode heartbeat response", "error", err)
	}
}

// PostVoiceInteract handles POST /api/voice/interact.
func (h *DefaultVoiceHandler) PostVoiceInteract(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, OPTIONS")
		writeVoiceError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if h.hub == nil || !h.hub.IsEnabled() {
		writeVoiceError(w, http.StatusServiceUnavailable, "voice hub is disabled")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeVoiceError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	// Protect multipart audio upload with 60s read deadline
	rc := http.NewResponseController(w)
	if err := rc.SetReadDeadline(time.Now().Add(60 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		slog.Debug("failed to set upload read deadline", "error", err)
	}

	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)

	if err := r.ParseMultipartForm(10 << 20); err != nil {
		writeVoiceError(w, http.StatusBadRequest, "invalid multipart form: "+err.Error())
		return
	}
	if r.MultipartForm != nil {
		defer func() {
			if removeErr := r.MultipartForm.RemoveAll(); removeErr != nil {
				slog.Debug("failed to clean up multipart form", "error", removeErr)
			}
		}()
	}

	// Once body is read, disable deadlines completely for the persistent deliberation stream
	if err := rc.SetReadDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		slog.Debug("failed to clear read deadline", "error", err)
	}
	if err := rc.SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		slog.Debug("failed to clear write deadline", "error", err)
	}

	file, _, err := r.FormFile("audio")
	if err != nil {
		writeVoiceError(w, http.StatusBadRequest, "missing required 'audio' form field")
		return
	}
	defer file.Close()

	nodeID := strings.TrimSpace(r.FormValue("node_id"))
	sessionID := strings.TrimSpace(r.FormValue("session_id"))

	var timings voice.EdgeTimings
	if val := r.FormValue("wake_eval_ms"); val != "" {
		if ms, err := strconv.ParseFloat(val, 64); err == nil && !math.IsNaN(ms) && !math.IsInf(ms, 0) && ms >= 0 && ms <= 60000.0 {
			timings.WakeEvalSec = ms / 1000.0
		} else if err != nil {
			slog.Warn("malformed wake_eval_ms in interact form", "val", val, "error", err)
		}
	}
	if val := r.FormValue("speech_duration_ms"); val != "" {
		if ms, err := strconv.ParseFloat(val, 64); err == nil && !math.IsNaN(ms) && !math.IsInf(ms, 0) && ms >= 0 && ms <= 60000.0 {
			timings.UtteranceSpeechSec = ms / 1000.0
		} else if err != nil {
			slog.Warn("malformed speech_duration_ms in interact form", "val", val, "error", err)
		}
	}
	if val := r.FormValue("silence_duration_ms"); val != "" {
		if ms, err := strconv.ParseFloat(val, 64); err == nil && !math.IsNaN(ms) && !math.IsInf(ms, 0) && ms >= 0 && ms <= 60000.0 {
			timings.UtteranceSilenceSec = ms / 1000.0
		} else if err != nil {
			slog.Warn("malformed silence_duration_ms in interact form", "val", val, "error", err)
		}
	}

	var wroteHeader bool
	var sinkMu sync.Mutex

	sink := func(event string, data any) error {
		sinkMu.Lock()
		defer sinkMu.Unlock()

		if !wroteHeader {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			w.Header().Set("X-Accel-Buffering", "no")
			if err := rc.SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
				slog.Debug("failed to disable response write deadline in sink", "error", err)
			}
			w.WriteHeader(http.StatusOK)
			wroteHeader = true
		}

		payload, err := json.Marshal(data)
		if err != nil {
			return err
		}

		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}

	if err := h.hub.Interact(r.Context(), file, nodeID, sessionID, sink, timings); err != nil {
		sinkMu.Lock()
		alreadyWrote := wroteHeader
		sinkMu.Unlock()

		if !alreadyWrote {
			switch {
			case errors.Is(err, voice.ErrHubDisabled):
				writeVoiceError(w, http.StatusServiceUnavailable, err.Error())
			case errors.Is(err, voice.ErrInteractionBusy):
				writeVoiceError(w, http.StatusConflict, err.Error())
			case errors.Is(err, voice.ErrInvalidAudio):
				writeVoiceError(w, http.StatusBadRequest, err.Error())
			default:
				writeVoiceError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}
	}
}

func writeVoiceError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(api.ErrorResponse{
		Status: "error",
		Error:  msg,
	}); err != nil {
		slog.Error("failed to encode voice error response", "error", err)
	}
}
