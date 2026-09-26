package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/azylman/mirrormere/internal/api"
)

// AudioCoordinator abstracts master audio coordinator state manipulation.
type AudioCoordinator interface {
	GetState() (int, bool)
	SetVolume(vol int) (int, bool, error)
	SetMute(muted *bool) (int, bool, error)
	ToggleMute() (int, bool, error)
}

// DefaultAudioHandler serves OpenAPI /api/audio, /api/audio/volume, and /api/audio/mute endpoints.
type DefaultAudioHandler struct {
	coord AudioCoordinator
}

// NewDefaultAudioHandler constructs a DefaultAudioHandler backed by coord.
func NewDefaultAudioHandler(coord AudioCoordinator) *DefaultAudioHandler {
	return &DefaultAudioHandler{coord: coord}
}

// GetAudio handles GET /api/audio.
func (h *DefaultAudioHandler) GetAudio(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD, OPTIONS")
		writeAudioError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if h.coord == nil {
		writeAudioError(w, http.StatusInternalServerError, "audio coordinator not configured")
		return
	}

	vol, muted := h.coord.GetState()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}

	if err := json.NewEncoder(w).Encode(api.AudioStateResponse{
		Status: "ok",
		Volume: vol,
		Muted:  muted,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// PostAudioVolume handles POST /api/audio/volume.
func (h *DefaultAudioHandler) PostAudioVolume(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, OPTIONS")
		writeAudioError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if h.coord == nil {
		writeAudioError(w, http.StatusInternalServerError, "audio coordinator not configured")
		return
	}

	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeAudioError(w, http.StatusBadRequest, "invalid volume: must be an integer between 0 and 100")
		return
	}

	rawVol, exists := raw["volume"]
	if !exists {
		writeAudioError(w, http.StatusBadRequest, "invalid volume: must be an integer between 0 and 100")
		return
	}

	var floatVol float64
	if err := json.Unmarshal(rawVol, &floatVol); err != nil {
		writeAudioError(w, http.StatusBadRequest, "invalid volume: must be an integer between 0 and 100")
		return
	}

	if floatVol != float64(int(floatVol)) || floatVol < 0 || floatVol > 100 {
		writeAudioError(w, http.StatusBadRequest, "invalid volume: must be an integer between 0 and 100")
		return
	}

	intVol := int(floatVol)
	newVol, newMuted, err := h.coord.SetVolume(intVol)
	if err != nil {
		writeAudioError(w, http.StatusBadRequest, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(api.AudioStateResponse{
		Status: "ok",
		Volume: newVol,
		Muted:  newMuted,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// PostAudioMute handles POST /api/audio/mute.
func (h *DefaultAudioHandler) PostAudioMute(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, OPTIONS")
		writeAudioError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if h.coord == nil {
		writeAudioError(w, http.StatusInternalServerError, "audio coordinator not configured")
		return
	}

	var raw map[string]json.RawMessage
	err := json.NewDecoder(r.Body).Decode(&raw)
	if err != nil && (r.ContentLength == 0 || err.Error() == "EOF") {
		raw = nil
	} else if err != nil {
		writeAudioError(w, http.StatusBadRequest, "invalid payload: muted must be a boolean")
		return
	}

	var targetMuted *bool
	if raw != nil {
		if rawMuted, exists := raw["muted"]; exists {
			trimmed := strings.TrimSpace(string(rawMuted))
			if trimmed != "true" && trimmed != "false" {
				writeAudioError(w, http.StatusBadRequest, "invalid payload: muted must be a boolean")
				return
			}
			b := (trimmed == "true")
			targetMuted = &b
		}
	}

	newVol, newMuted, err := h.coord.SetMute(targetMuted)
	if err != nil {
		writeAudioError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(api.AudioStateResponse{
		Status: "ok",
		Volume: newVol,
		Muted:  newMuted,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func writeAudioError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(api.ErrorResponse{
		Status: "error",
		Error:  msg,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
