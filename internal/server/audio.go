package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/azylman/mirrormere/internal/api"
)

// AudioCoordinator abstracts master audio coordinator state manipulation.
type AudioCoordinator interface {
	GetState() (int, bool)
	SetVolume(vol int) (int, bool, error)
	SetMute(muted *bool) (int, bool, error)
}

// DefaultAudioHandler serves /api/audio, /api/audio/volume, and /api/audio/mute endpoints.
type DefaultAudioHandler struct {
	coord AudioCoordinator
}

// NewDefaultAudioHandler constructs a DefaultAudioHandler backed by coord.
func NewDefaultAudioHandler(coord AudioCoordinator) *DefaultAudioHandler {
	return &DefaultAudioHandler{coord: coord}
}

// GetAudio handles GET /api/audio.
func (h *DefaultAudioHandler) GetAudio(w http.ResponseWriter, r *http.Request) {
	if !api.AllowMethods(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	if h.coord == nil {
		api.WriteError(w, http.StatusInternalServerError, "audio coordinator not configured")
		return
	}
	vol, muted := h.coord.GetState()
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		return
	}
	api.WriteJSON(w, http.StatusOK, api.AudioStateResponse{Status: "ok", Volume: vol, Muted: muted})
}

// PostAudioVolume handles POST /api/audio/volume.
func (h *DefaultAudioHandler) PostAudioVolume(w http.ResponseWriter, r *http.Request) {
	if !api.AllowMethods(w, r, http.MethodPost) {
		return
	}
	if h.coord == nil {
		api.WriteError(w, http.StatusInternalServerError, "audio coordinator not configured")
		return
	}
	var req api.AudioVolumeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Volume == nil || *req.Volume < 0 || *req.Volume > 100 {
		api.WriteError(w, http.StatusBadRequest, "invalid volume: must be an integer between 0 and 100")
		return
	}
	newVol, newMuted, err := h.coord.SetVolume(*req.Volume)
	if err != nil {
		api.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	api.WriteJSON(w, http.StatusOK, api.AudioStateResponse{Status: "ok", Volume: newVol, Muted: newMuted})
}

// PostAudioMute handles POST /api/audio/mute.
func (h *DefaultAudioHandler) PostAudioMute(w http.ResponseWriter, r *http.Request) {
	if !api.AllowMethods(w, r, http.MethodPost) {
		return
	}
	if h.coord == nil {
		api.WriteError(w, http.StatusInternalServerError, "audio coordinator not configured")
		return
	}
	var req api.AudioMuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		api.WriteError(w, http.StatusBadRequest, "invalid payload: muted must be a boolean")
		return
	}
	var targetMuted *bool
	if len(req.Muted) > 0 {
		var b bool
		if bytes.Equal(bytes.TrimSpace(req.Muted), []byte("null")) || json.Unmarshal(req.Muted, &b) != nil {
			api.WriteError(w, http.StatusBadRequest, "invalid payload: muted must be a boolean")
			return
		}
		targetMuted = &b
	}
	newVol, newMuted, err := h.coord.SetMute(targetMuted)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	api.WriteJSON(w, http.StatusOK, api.AudioStateResponse{Status: "ok", Volume: newVol, Muted: newMuted})
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
