package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/azylman/mirrormere/internal/api"
)

// AudioCoordinator abstracts master audio coordinator state manipulation.
type AudioCoordinator interface {
	GetState() (int, bool)
	SetVolume(vol int) (int, bool, error)
	SetMute(muted *bool) (int, bool, error)
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
	if !requireCORSAndMethod(w, r, "GET, HEAD, OPTIONS", http.MethodGet, http.MethodHead) {
		return
	}
	if h.coord == nil {
		writeAPIError(w, http.StatusInternalServerError, "audio coordinator not configured")
		return
	}
	vol, muted := h.coord.GetState()
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		return
	}
	writeJSON(w, http.StatusOK, api.AudioStateResponse{Status: "ok", Volume: vol, Muted: muted})
}

// PostAudioVolume handles POST /api/audio/volume.
func (h *DefaultAudioHandler) PostAudioVolume(w http.ResponseWriter, r *http.Request) {
	if !requireCORSAndMethod(w, r, "POST, OPTIONS", http.MethodPost) {
		return
	}
	if h.coord == nil {
		writeAPIError(w, http.StatusInternalServerError, "audio coordinator not configured")
		return
	}
	var raw map[string]json.RawMessage
	var floatVol float64
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil || json.Unmarshal(raw["volume"], &floatVol) != nil || floatVol != float64(int(floatVol)) || floatVol < 0 || floatVol > 100 {
		writeAPIError(w, http.StatusBadRequest, "invalid volume: must be an integer between 0 and 100")
		return
	}
	newVol, newMuted, err := h.coord.SetVolume(int(floatVol))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, api.AudioStateResponse{Status: "ok", Volume: newVol, Muted: newMuted})
}

// PostAudioMute handles POST /api/audio/mute.
func (h *DefaultAudioHandler) PostAudioMute(w http.ResponseWriter, r *http.Request) {
	if !requireCORSAndMethod(w, r, "POST, OPTIONS", http.MethodPost) {
		return
	}
	if h.coord == nil {
		writeAPIError(w, http.StatusInternalServerError, "audio coordinator not configured")
		return
	}
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil && r.ContentLength != 0 && err.Error() != "EOF" {
		writeAPIError(w, http.StatusBadRequest, "invalid payload: muted must be a boolean")
		return
	}
	var targetMuted *bool
	if raw != nil {
		if rawMuted, exists := raw["muted"]; exists {
			trimmed := strings.TrimSpace(string(rawMuted))
			if trimmed != "true" && trimmed != "false" {
				writeAPIError(w, http.StatusBadRequest, "invalid payload: muted must be a boolean")
				return
			}
			b := (trimmed == "true")
			targetMuted = &b
		}
	}
	newVol, newMuted, err := h.coord.SetMute(targetMuted)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, api.AudioStateResponse{Status: "ok", Volume: newVol, Muted: newMuted})
}

func requireCORSAndMethod(w http.ResponseWriter, r *http.Request, allowHeader string, allowed ...string) bool {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", allowHeader)
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return false
	}
	for _, m := range allowed {
		if r.Method == m {
			return true
		}
	}
	w.Header().Set("Allow", allowHeader)
	writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
	return false
}

func writeJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		slog.Error("failed to encode json response", "error", err)
	}
}

func writeAPIError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, api.ErrorResponse{Status: "error", Error: msg})
}

func setSSEHeaders(w http.ResponseWriter, rc *http.ResponseController) {
	for k, v := range map[string]string{"Content-Type": "text/event-stream", "Cache-Control": "no-cache", "Connection": "keep-alive", "X-Accel-Buffering": "no"} {
		w.Header().Set(k, v)
	}
	if err := rc.SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		slog.Debug("failed to clear write deadline", "error", err)
	}
	w.WriteHeader(http.StatusOK)
}

func clearDeadlines(rc *http.ResponseController) {
	if err := rc.SetReadDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		slog.Debug("failed to clear read deadline", "error", err)
	}
	if err := rc.SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		slog.Debug("failed to clear write deadline", "error", err)
	}
}
