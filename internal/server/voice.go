package server

import (
	"encoding/json"
	"net/http"

	"github.com/azylman/mirrormere/internal/api"
	"github.com/azylman/mirrormere/internal/events"
	"github.com/azylman/mirrormere/internal/voice"
)

// VoiceCoordinator abstracts voice coordinator state manipulation.
type VoiceCoordinator interface {
	GetState() events.VoiceStateData
	SetState(state string, transcript, reply, ttsEngine *string) (events.VoiceStateData, error)
}

// DefaultVoiceHandler serves OpenAPI /api/voice/state endpoint.
type DefaultVoiceHandler struct {
	coord VoiceCoordinator
}

// NewDefaultVoiceHandler constructs a DefaultVoiceHandler backed by coord.
func NewDefaultVoiceHandler(coord VoiceCoordinator) *DefaultVoiceHandler {
	return &DefaultVoiceHandler{coord: coord}
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
