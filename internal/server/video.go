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
	SetPlayerState(id string, playerState string) (video.VideoState, error)
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

// GetVideoState handles GET /api/video/state.
func (h *DefaultVideoHandler) GetVideoState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD, OPTIONS")
		writeVideoError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if h.coord == nil {
		writeVideoError(w, http.StatusInternalServerError, "video coordinator not configured")
		return
	}

	state := h.coord.GetState()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}

	if err := json.NewEncoder(w).Encode(toAPIVideoStateResponse(state)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// PostVideoTrigger handles POST /api/video/trigger.
func (h *DefaultVideoHandler) PostVideoTrigger(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, OPTIONS")
		writeVideoError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if h.coord == nil {
		writeVideoError(w, http.StatusInternalServerError, "video coordinator not configured")
		return
	}

	var req api.VideoTriggerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeVideoError(w, http.StatusBadRequest, "invalid video trigger request: malformed JSON")
		return
	}

	if strings.TrimSpace(req.Id) == "" || strings.TrimSpace(req.StreamUrl) == "" {
		writeVideoError(w, http.StatusBadRequest, "invalid video trigger request: id and stream_url are required")
		return
	}

	vType := video.TypeWebRTC
	if req.Type != nil && string(*req.Type) != "" {
		vType = string(*req.Type)
	}
	vPriority := video.PriorityPersistent
	if req.Priority != nil && string(*req.Priority) != "" {
		vPriority = string(*req.Priority)
	}
	vTimeout := 0
	if req.TimeoutSeconds != nil {
		vTimeout = *req.TimeoutSeconds
	}
	vControllable := false
	if req.Controllable != nil {
		vControllable = *req.Controllable
	}
	vControlURL := ""
	if req.ControlUrl != nil {
		vControlURL = *req.ControlUrl
	}
	vPlayerState := video.PlayerStatePlaying
	if req.PlayerState != nil && string(*req.PlayerState) != "" {
		vPlayerState = string(*req.PlayerState)
	}

	stream := video.VideoStream{
		ID:             req.Id,
		StreamURL:      req.StreamUrl,
		Type:           vType,
		Priority:       vPriority,
		TimeoutSeconds: vTimeout,
		Controllable:   vControllable,
		ControlURL:     vControlURL,
		PlayerState:    vPlayerState,
	}

	newState, err := h.coord.Trigger(stream)
	if err != nil {
		writeVideoError(w, http.StatusBadRequest, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(toAPIVideoStateResponse(newState)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// PostVideoDismiss handles POST /api/video/dismiss.
func (h *DefaultVideoHandler) PostVideoDismiss(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, OPTIONS")
		writeVideoError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if h.coord == nil {
		writeVideoError(w, http.StatusInternalServerError, "video coordinator not configured")
		return
	}

	var req api.VideoDismissRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeVideoError(w, http.StatusBadRequest, "invalid video dismiss request: malformed JSON")
		return
	}

	if strings.TrimSpace(req.Id) == "" {
		writeVideoError(w, http.StatusBadRequest, "invalid video dismiss request: id is required")
		return
	}

	newState, err := h.coord.Dismiss(req.Id)
	if err != nil {
		if errors.Is(err, video.ErrStreamNotFound) {
			writeVideoError(w, http.StatusNotFound, "stream not active")
			return
		}
		writeVideoError(w, http.StatusBadRequest, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(toAPIVideoStateResponse(newState)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// PostVideoAction handles POST /api/video/action.
func (h *DefaultVideoHandler) PostVideoAction(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, OPTIONS")
		writeVideoError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if h.coord == nil {
		writeVideoError(w, http.StatusInternalServerError, "video coordinator not configured")
		return
	}

	var req api.VideoActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeVideoError(w, http.StatusBadRequest, "invalid video action request: malformed JSON")
		return
	}

	if strings.TrimSpace(req.Id) == "" || string(req.Action) == "" {
		writeVideoError(w, http.StatusBadRequest, "invalid video action request: id and action are required")
		return
	}

	err := h.coord.ForwardAction(r.Context(), req.Id, string(req.Action), req.Value)
	if err != nil {
		if errors.Is(err, video.ErrStreamNotFound) {
			writeVideoError(w, http.StatusNotFound, "stream not active")
			return
		}
		if errors.Is(err, video.ErrNotControllable) {
			writeVideoError(w, http.StatusUnprocessableEntity, "stream is not controllable")
			return
		}
		if errors.Is(err, video.ErrControllerUnreachable) {
			writeVideoError(w, http.StatusBadGateway, "stream controller unreachable")
			return
		}
		writeVideoError(w, http.StatusBadRequest, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(api.ActionResponse{Status: "ok"}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// PostVideoState handles POST /api/video/state.
func (h *DefaultVideoHandler) PostVideoState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, OPTIONS")
		writeVideoError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if h.coord == nil {
		writeVideoError(w, http.StatusInternalServerError, "video coordinator not configured")
		return
	}

	var req api.VideoPlayerStateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeVideoError(w, http.StatusBadRequest, "invalid video player state request: malformed JSON")
		return
	}

	if strings.TrimSpace(req.Id) == "" || string(req.PlayerState) == "" {
		writeVideoError(w, http.StatusBadRequest, "invalid video player state request: id and player_state are required")
		return
	}

	_, err := h.coord.SetPlayerState(req.Id, string(req.PlayerState))
	if err != nil {
		if errors.Is(err, video.ErrStreamNotFound) {
			writeVideoError(w, http.StatusNotFound, "stream not active")
			return
		}
		writeVideoError(w, http.StatusBadRequest, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(api.VideoPlayerStateResponse{
		Status:      "ok",
		Id:          req.Id,
		PlayerState: api.VideoPlayerStateResponsePlayerState(req.PlayerState),
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func toAPIVideoStream(s *video.VideoStream) *api.VideoStream {
	if s == nil {
		return nil
	}
	apiStream := &api.VideoStream{
		Id:        s.ID,
		StreamUrl: s.StreamURL,
		Type:      api.VideoStreamType(s.Type),
	}
	if s.Priority != "" {
		p := api.VideoStreamPriority(s.Priority)
		apiStream.Priority = &p
	}
	if s.TimeoutSeconds > 0 {
		t := s.TimeoutSeconds
		apiStream.TimeoutSeconds = &t
	}
	if s.Controllable {
		c := true
		apiStream.Controllable = &c
	}
	if s.ControlURL != "" {
		u := s.ControlURL
		apiStream.ControlUrl = &u
	}
	if s.PlayerState != "" {
		ps := api.VideoStreamPlayerState(s.PlayerState)
		apiStream.PlayerState = &ps
	}
	m := s.Muted
	apiStream.Muted = &m
	return apiStream
}

func toAPIVideoStateResponse(state video.VideoState) api.VideoStateResponse {
	mode := api.Widgets
	if state.Mode == video.ModeVideo {
		mode = api.Video
	}
	return api.VideoStateResponse{
		Status:  "ok",
		Mode:    mode,
		Primary: toAPIVideoStream(state.Primary),
		Pip:     toAPIVideoStream(state.Pip),
	}
}

func writeVideoError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(api.ErrorResponse{
		Status: "error",
		Error:  msg,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
