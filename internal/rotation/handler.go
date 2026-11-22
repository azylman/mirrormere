package rotation

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/azylman/mirrormere/internal/api"
	"github.com/azylman/mirrormere/internal/events"
)

// ScreenController abstracts operations needed by the screen rotation HTTP handler.
type ScreenController interface {
	SelectScreen(index int) (events.ScreenRotateData, error)
	AdvanceScreen(direction string) (events.ScreenRotateData, error)
	PauseRotation(duration time.Duration) (int, error)
	ResumeRotation() (int, error)
	ScreenLayout() ScreenLayoutSnapshot
}

// Handler serves HTTP endpoints for screen navigation.
type Handler struct {
	controller ScreenController
}

// NewHandler constructs a Handler wrapping a ScreenController.
func NewHandler(controller ScreenController) *Handler {
	return &Handler{controller: controller}
}

func (h *Handler) checkPostAndController(w http.ResponseWriter, r *http.Request) bool {
	if !api.AllowMethods(w, r, http.MethodPost) {
		return false
	}
	if h.controller == nil {
		api.WriteError(w, http.StatusInternalServerError, "screen controller not configured")
		return false
	}
	return true
}

func (h *Handler) PostScreenSelect(w http.ResponseWriter, r *http.Request) {
	if !h.checkPostAndController(w, r) {
		return
	}
	var req api.ScreenSelectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	data, err := h.controller.SelectScreen(req.ScreenIndex)
	if err != nil {
		api.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	api.WriteJSON(w, http.StatusOK, api.ScreenNavigationResponse{Status: "ok", CurrentScreen: data.CurrentScreen, TotalScreens: data.TotalScreens})
}

func (h *Handler) PostScreenAdvance(w http.ResponseWriter, r *http.Request) {
	if !h.checkPostAndController(w, r) {
		return
	}
	direction := "next"
	if r.Body != nil {
		var req api.ScreenAdvanceRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			api.WriteError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		} else if req.Direction != "" {
			direction = req.Direction
		}
	}
	data, err := h.controller.AdvanceScreen(direction)
	if err != nil {
		api.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	api.WriteJSON(w, http.StatusOK, api.ScreenNavigationResponse{Status: "ok", CurrentScreen: data.CurrentScreen, TotalScreens: data.TotalScreens})
}

func (h *Handler) PostScreenPause(w http.ResponseWriter, r *http.Request) {
	if !h.checkPostAndController(w, r) {
		return
	}
	var req api.ScreenPauseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Paused == nil {
		api.WriteError(w, http.StatusBadRequest, "invalid payload: paused must be a boolean")
		return
	}
	if req.DurationSeconds != nil && *req.DurationSeconds < 0 {
		api.WriteError(w, http.StatusBadRequest, "duration_seconds must be >= 0")
		return
	}

	var currentScreen int
	var err error
	if *req.Paused {
		dur := time.Duration(-1)
		if req.DurationSeconds != nil {
			dur = time.Duration(*req.DurationSeconds) * time.Second
		}
		currentScreen, err = h.controller.PauseRotation(dur)
	} else {
		currentScreen, err = h.controller.ResumeRotation()
	}
	if err != nil {
		api.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	api.WriteJSON(w, http.StatusOK, api.ScreenPauseResponse{Status: "ok", Paused: *req.Paused, CurrentScreen: currentScreen})
}

// GetScreens handles GET /api/screens requests, returning the solved layout of every screen so the
// client can keep adjacent screens mounted.
func (h *Handler) GetScreens(w http.ResponseWriter, r *http.Request) {
	if !api.AllowMethods(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	if h.controller == nil {
		api.WriteError(w, http.StatusInternalServerError, "screen controller not configured")
		return
	}

	snap := h.controller.ScreenLayout()
	resp := api.ScreensResponse{
		CurrentScreen:   snap.CurrentScreen,
		IntervalSeconds: snap.IntervalSeconds,
		Screens:         make([]api.ScreenLayout, 0, len(snap.Screens)),
	}
	for i, screen := range snap.Screens {
		widgets := make([]api.WidgetPlacement, 0, len(screen.Widgets))
		for _, pw := range screen.Widgets {
			widgets = append(widgets, api.WidgetPlacement{
				WidgetID:   pw.WidgetID,
				Type:       pw.Type,
				Origin:     []int{pw.Origin[0], pw.Origin[1]},
				Dimensions: []int{pw.Dimensions.Cols, pw.Dimensions.Rows},
			})
		}
		resp.Screens = append(resp.Screens, api.ScreenLayout{Index: i, Widgets: widgets})
	}
	api.WriteJSON(w, http.StatusOK, resp)
}
