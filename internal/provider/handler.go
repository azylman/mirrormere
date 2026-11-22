package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/azylman/mirrormere/internal/api"
)

// WidgetPushCaller abstracts the push data and photo ingestion methods implemented by ProviderCoordinator.
type WidgetPushCaller interface {
	PushWidgetData(ctx context.Context, widgetID string, data map[string]any) (WidgetPayload, error)
	PushWidgetPhotos(ctx context.Context, widgetID string, photos []string, albumName string) (WidgetPayload, error)
}

// WidgetPusher is an alias for WidgetPushCaller for backwards compatibility.
type WidgetPusher = WidgetPushCaller

// PushHandler serves the POST /api/widgets/{widget_id}/push and /api/widgets/{widget_id}/photos webhook endpoints.
type PushHandler struct {
	pusher WidgetPushCaller
}

// NewPushHandler constructs an initialized PushHandler.
func NewPushHandler(pusher WidgetPushCaller) *PushHandler {
	return &PushHandler{pusher: pusher}
}

// PostWidgetPush handles POST /api/widgets/{widget_id}/push requests.
func (h *PushHandler) PostWidgetPush(w http.ResponseWriter, r *http.Request, widgetID string) {
	if !h.checkPostAndPusher(w, r) {
		return
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, MaxHTTPResponseBodySize))
	if err != nil {
		api.WriteError(w, http.StatusBadRequest, "failed to read request body: "+err.Error())
		return
	}

	var rawDoc any
	if err = json.Unmarshal(bodyBytes, &rawDoc); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid JSON payload: "+err.Error())
		return
	}

	obj, ok := rawDoc.(map[string]any)
	if !ok {
		api.WriteError(w, http.StatusBadRequest, "payload must be a JSON object")
		return
	}

	payload, err := h.pusher.PushWidgetData(r.Context(), widgetID, obj)
	if err != nil {
		if errors.Is(err, ErrWidgetNotFound) {
			api.WriteError(w, http.StatusNotFound, fmt.Sprintf("widget '%s' not found in active configuration", widgetID))
			return
		}
		if errors.Is(err, ErrListWidgetPushForbidden) {
			api.WriteError(w, http.StatusConflict, fmt.Sprintf("cannot push state to list-backed widget '%s'; task lists are read-only and ingested from configured upstream providers", widgetID))
			return
		}
		if errors.Is(err, ErrNonHTTPWidgetPushForbidden) {
			api.WriteError(w, http.StatusConflict, fmt.Sprintf("cannot push state to widget '%s'; push webhook is strictly limited to widgets using the http provider", widgetID))
			return
		}
		if errors.Is(err, ErrSchemaValidation) {
			api.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		api.WriteError(w, http.StatusInternalServerError, "push ingestion error: "+err.Error())
		return
	}

	api.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "widget_id": widgetID, "updated_at": payload.Timestamp})
}

// PostWidgetPhotos handles POST /api/widgets/{widget_id}/photos requests.
func (h *PushHandler) PostWidgetPhotos(w http.ResponseWriter, r *http.Request, widgetID string) {
	if !h.checkPostAndPusher(w, r) {
		return
	}

	var req struct {
		Photos    []string `json:"photos"`
		AlbumName string   `json:"album_name"`
	}
	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, MaxHTTPResponseBodySize))
	if err != nil {
		api.WriteError(w, http.StatusBadRequest, "failed to read request body: "+err.Error())
		return
	}
	if err = json.Unmarshal(bodyBytes, &req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid JSON payload: "+err.Error())
		return
	}
	if req.Photos == nil {
		api.WriteError(w, http.StatusBadRequest, "photos field is required")
		return
	}

	payload, err := h.pusher.PushWidgetPhotos(r.Context(), widgetID, req.Photos, req.AlbumName)
	if err != nil {
		writePhotoPushError(w, widgetID, err)
		return
	}

	api.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "widget_id": widgetID, "updated_at": payload.Timestamp})
}

func writePhotoPushError(w http.ResponseWriter, widgetID string, err error) {
	switch {
	case errors.Is(err, ErrWidgetNotFound):
		api.WriteError(w, http.StatusNotFound, fmt.Sprintf("widget '%s' not found in active configuration", widgetID))
	case errors.Is(err, ErrPhotoPushNotSupported):
		api.WriteError(w, http.StatusConflict, fmt.Sprintf("target widget '%s' does not support photo push updates: %s", widgetID, err.Error()))
	case errors.Is(err, ErrInvalidPhotoPushPayload):
		api.WriteError(w, http.StatusBadRequest, err.Error())
	default:
		api.WriteError(w, http.StatusInternalServerError, "photo push error: "+err.Error())
	}
}

func (h *PushHandler) checkPostAndPusher(w http.ResponseWriter, r *http.Request) bool {
	if !api.AllowMethods(w, r, http.MethodPost) {
		return false
	}
	if h.pusher == nil {
		api.WriteError(w, http.StatusInternalServerError, "pusher not configured")
		return false
	}
	return true
}
