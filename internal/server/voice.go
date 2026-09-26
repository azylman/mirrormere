package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"

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
	Interact(ctx context.Context, audio io.Reader, nodeID, sessionID string, sink voice.SSEEventSink) error
}

// DefaultVoiceHandler serves OpenAPI /api/voice/state and /api/voice/interact endpoints.
type DefaultVoiceHandler struct {
	coord VoiceCoordinator
	hub   VoiceHub
}

// NewDefaultVoiceHandler constructs a DefaultVoiceHandler backed by coord and hub.
func NewDefaultVoiceHandler(coord VoiceCoordinator, hub VoiceHub) *DefaultVoiceHandler {
	return &DefaultVoiceHandler{coord: coord, hub: hub}
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
		http.Error(w, err.Error(), http.StatusInternalServerError)
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

	file, _, err := r.FormFile("audio")
	if err != nil {
		writeVoiceError(w, http.StatusBadRequest, "missing required 'audio' form field")
		return
	}
	defer file.Close()

	nodeID := r.FormValue("node_id")
	sessionID := r.FormValue("session_id")

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

	if err := h.hub.Interact(r.Context(), file, nodeID, sessionID, sink); err != nil {
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
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
