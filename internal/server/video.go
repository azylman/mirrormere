package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/azylman/mirrormere/internal/api"
	"github.com/azylman/mirrormere/internal/video"
)

// VideoCoordinator abstracts video priority stack and action forwarding coordination.
type VideoCoordinator interface {
	GetState() video.VideoState
	Trigger(stream video.VideoStream) (video.VideoState, error)
	Dismiss(id string) (video.VideoState, error)
	UpdatePlayerState(id string, update video.VideoStream) (video.VideoState, error)
	ForwardAction(ctx context.Context, id, action string, value any) error
}

// DefaultVideoHandler serves OpenAPI /api/video/* endpoints.
type DefaultVideoHandler struct {
	coord VideoCoordinator
}

// NewDefaultVideoHandler constructs a DefaultVideoHandler backed by coord.
func NewDefaultVideoHandler(coord VideoCoordinator) *DefaultVideoHandler {
	return &DefaultVideoHandler{coord: coord}
}

func (h *DefaultVideoHandler) ready(w http.ResponseWriter) bool {
	if h.coord == nil {
		writeAPIError(w, http.StatusInternalServerError, "video coordinator not configured")
		return false
	}
	return true
}

// GetVideoState handles GET /api/video/state.
func (h *DefaultVideoHandler) GetVideoState(w http.ResponseWriter, r *http.Request) {
	if !requireCORSAndMethod(w, r, "GET, HEAD, OPTIONS", http.MethodGet, http.MethodHead) || !h.ready(w) {
		return
	}
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		return
	}
	writeJSON(w, http.StatusOK, toAPIVideoStateResponse(h.coord.GetState()))
}

func setIf[T any](dst *T, src *T) {
	if src != nil {
		*dst = *src
	}
}

func setStr[T ~string](dst *string, src *T) {
	if src != nil && *src != "" {
		*dst = string(*src)
	}
}

func applyStreamMetadata(s *video.VideoStream, title, series, season, episode *string, pos, dur *float32, updatedAt *int64) {
	setIf(&s.Title, title)
	setIf(&s.Series, series)
	setIf(&s.Season, season)
	setIf(&s.Episode, episode)
	if pos != nil {
		s.Position = float64(*pos)
	}
	if dur != nil {
		s.Duration = float64(*dur)
	}
	setIf(&s.PositionUpdatedAt, updatedAt)
}

// PostVideoTrigger handles POST /api/video/trigger.
func (h *DefaultVideoHandler) PostVideoTrigger(w http.ResponseWriter, r *http.Request) {
	if !requireCORSAndMethod(w, r, "POST, OPTIONS", http.MethodPost) || !h.ready(w) {
		return
	}
	var req api.VideoTriggerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid video trigger request: malformed JSON")
		return
	}
	reqID := strings.TrimSpace(req.Id)
	if reqID == "" || strings.TrimSpace(req.StreamUrl) == "" || reqID == "all" || reqID == "*" {
		msg := "invalid video trigger request: id and stream_url are required"
		if reqID == "all" || reqID == "*" {
			msg = "invalid video trigger request: \x27all\x27 and \x27*\x27 are reserved sentinels"
		}
		writeAPIError(w, http.StatusBadRequest, msg)
		return
	}
	stream := video.VideoStream{
		ID: req.Id, StreamURL: req.StreamUrl, Type: video.TypeWebRTC, Priority: video.PriorityPersistent, PlayerState: video.PlayerStatePlaying,
	}
	setStr(&stream.Type, req.Type)
	setStr(&stream.Priority, req.Priority)
	setIf(&stream.TimeoutSeconds, req.TimeoutSeconds)
	setIf(&stream.Controllable, req.Controllable)
	setIf(&stream.ControlURL, req.ControlUrl)
	setStr(&stream.PlayerState, req.PlayerState)
	applyStreamMetadata(&stream, req.Title, req.Series, req.Season, req.Episode, req.Position, req.Duration, req.PositionUpdatedAt)

	newState, err := h.coord.Trigger(stream)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toAPIVideoStateResponse(newState))
}

// PostVideoDismiss handles POST /api/video/dismiss.
func (h *DefaultVideoHandler) PostVideoDismiss(w http.ResponseWriter, r *http.Request) {
	if !requireCORSAndMethod(w, r, "POST, OPTIONS", http.MethodPost) || !h.ready(w) {
		return
	}
	var req api.VideoDismissRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Id) == "" {
		msg := "invalid video dismiss request: malformed JSON"
		if err == nil {
			msg = "invalid video dismiss request: id is required"
		}
		writeAPIError(w, http.StatusBadRequest, msg)
		return
	}
	newState, err := h.coord.Dismiss(req.Id)
	if err != nil {
		if errors.Is(err, video.ErrStreamNotFound) {
			writeAPIError(w, http.StatusNotFound, "stream not active")
			return
		}
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toAPIVideoStateResponse(newState))
}

// PostVideoAction handles POST /api/video/action.
func (h *DefaultVideoHandler) PostVideoAction(w http.ResponseWriter, r *http.Request) {
	if !requireCORSAndMethod(w, r, "POST, OPTIONS", http.MethodPost) || !h.ready(w) {
		return
	}
	var req api.VideoActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Id) == "" || string(req.Action) == "" {
		msg := "invalid video action request: malformed JSON"
		if err == nil {
			msg = "invalid video action request: id and action are required"
		}
		writeAPIError(w, http.StatusBadRequest, msg)
		return
	}
	err := h.coord.ForwardAction(r.Context(), req.Id, string(req.Action), req.Value)
	if err != nil {
		switch {
		case errors.Is(err, video.ErrStreamNotFound):
			writeAPIError(w, http.StatusNotFound, "stream not active")
		case errors.Is(err, video.ErrNotControllable):
			writeAPIError(w, http.StatusUnprocessableEntity, "stream is not controllable")
		case errors.Is(err, video.ErrControllerUnreachable):
			writeAPIError(w, http.StatusBadGateway, "stream controller unreachable")
		default:
			writeAPIError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, api.ActionResponse{Status: "ok"})
}

// PostVideoState handles POST /api/video/state.
func (h *DefaultVideoHandler) PostVideoState(w http.ResponseWriter, r *http.Request) {
	if !requireCORSAndMethod(w, r, "POST, OPTIONS", http.MethodPost) || !h.ready(w) {
		return
	}
	var req api.VideoPlayerStateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Id) == "" || string(req.PlayerState) == "" {
		msg := "invalid video player state request: malformed JSON"
		if err == nil {
			msg = "invalid video player state request: id and player_state are required"
		}
		writeAPIError(w, http.StatusBadRequest, msg)
		return
	}
	update := video.VideoStream{ID: req.Id, PlayerState: string(req.PlayerState)}
	applyStreamMetadata(&update, req.Title, req.Series, req.Season, req.Episode, req.Position, req.Duration, req.PositionUpdatedAt)

	_, err := h.coord.UpdatePlayerState(req.Id, update)
	if err != nil {
		if errors.Is(err, video.ErrStreamNotFound) {
			writeAPIError(w, http.StatusNotFound, "stream not active")
			return
		}
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, api.VideoPlayerStateResponse{
		Status: "ok", Id: req.Id, PlayerState: api.VideoPlayerStateResponsePlayerState(req.PlayerState),
	})
}

func ptrWhen[T any](v T, cond bool) *T {
	if !cond {
		return nil
	}
	return &v
}

func toAPIVideoStream(s *video.VideoStream) *api.VideoStream {
	if s == nil {
		return nil
	}
	return &api.VideoStream{
		Id:                s.ID,
		StreamUrl:         s.StreamURL,
		Type:              api.VideoStreamType(s.Type),
		Priority:          ptrWhen(api.VideoStreamPriority(s.Priority), s.Priority != ""),
		TimeoutSeconds:    ptrWhen(s.TimeoutSeconds, s.TimeoutSeconds > 0),
		Controllable:      ptrWhen(true, s.Controllable),
		ControlUrl:        ptrWhen(s.ControlURL, s.ControlURL != ""),
		PlayerState:       ptrWhen(api.VideoStreamPlayerState(s.PlayerState), s.PlayerState != ""),
		Muted:             &s.Muted,
		Title:             ptrWhen(s.Title, s.Title != ""),
		Series:            ptrWhen(s.Series, s.Series != ""),
		Season:            ptrWhen(s.Season, s.Season != ""),
		Episode:           ptrWhen(s.Episode, s.Episode != ""),
		Position:          ptrWhen(float32(s.Position), s.Position > 0 || s.PositionUpdatedAt > 0),
		Duration:          ptrWhen(float32(s.Duration), s.Duration > 0),
		PositionUpdatedAt: ptrWhen(s.PositionUpdatedAt, s.PositionUpdatedAt > 0),
	}
}

func toAPIVideoStateResponse(state video.VideoState) api.VideoStateResponse {
	mode := api.Widgets
	if state.Mode == video.ModeVideo {
		mode = api.Video
	}
	return api.VideoStateResponse{Status: "ok", Mode: mode, Primary: toAPIVideoStream(state.Primary), Pip: toAPIVideoStream(state.Pip)}
}
