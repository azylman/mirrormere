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
	CurrentScreen() int
	TotalScreens() int
	IsPaused() bool
}

// Handler serves HTTP endpoints for screen navigation matching OpenAPI specification.
type Handler struct {
	controller ScreenController
}

// NewHandler constructs a Handler wrapping a ScreenController.
func NewHandler(controller ScreenController) *Handler {
	return &Handler{controller: controller}
}

// PostScreenSelect handles POST /api/screen/select requests.
func (h *Handler) PostScreenSelect(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, OPTIONS")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if h.controller == nil {
		writeError(w, http.StatusInternalServerError, "screen controller not configured")
		return
	}

	var req api.ScreenSelectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	data, err := h.controller.SelectScreen(req.ScreenIndex)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, api.ScreenNavigationResponse{
		Status:        "ok",
		CurrentScreen: data.CurrentScreen,
		TotalScreens:  data.TotalScreens,
	})
}

// PostScreenAdvance handles POST /api/screen/advance requests.
func (h *Handler) PostScreenAdvance(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, OPTIONS")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if h.controller == nil {
		writeError(w, http.StatusInternalServerError, "screen controller not configured")
		return
	}

	direction := "next"
	if r.Body != nil {
		var req api.ScreenAdvanceRequest
		err := json.NewDecoder(r.Body).Decode(&req)
		if err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		if req.Direction != nil && *req.Direction != "" {
			direction = string(*req.Direction)
		}
	}

	data, err := h.controller.AdvanceScreen(direction)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, api.ScreenNavigationResponse{
		Status:        "ok",
		CurrentScreen: data.CurrentScreen,
		TotalScreens:  data.TotalScreens,
	})
}

// PostScreenPause handles POST /api/screen/pause requests.
func (h *Handler) PostScreenPause(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, OPTIONS")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if h.controller == nil {
		writeError(w, http.StatusInternalServerError, "screen controller not configured")
		return
	}

	var req struct {
		Paused          *bool `json:"paused"`
		DurationSeconds *int  `json:"duration_seconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid payload: paused must be a boolean")
		return
	}

	if req.Paused == nil {
		writeError(w, http.StatusBadRequest, "invalid payload: paused must be a boolean")
		return
	}

	if req.DurationSeconds != nil && *req.DurationSeconds < 0 {
		writeError(w, http.StatusBadRequest, "duration_seconds must be >= 0")
		return
	}

	var currentScreen int
	var err error

	if *req.Paused {
		var dur time.Duration
		if req.DurationSeconds == nil {
			dur = -1
		} else {
			dur = time.Duration(*req.DurationSeconds) * time.Second
		}
		currentScreen, err = h.controller.PauseRotation(dur)
	} else {
		currentScreen, err = h.controller.ResumeRotation()
	}

	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, api.ScreenPauseResponse{
		Status:        "ok",
		Paused:        *req.Paused,
		CurrentScreen: currentScreen,
	})
}

// HandleSelect is an alias for PostScreenSelect.
func (h *Handler) HandleSelect(w http.ResponseWriter, r *http.Request) {
	h.PostScreenSelect(w, r)
}

// HandleAdvance is an alias for PostScreenAdvance.
func (h *Handler) HandleAdvance(w http.ResponseWriter, r *http.Request) {
	h.PostScreenAdvance(w, r)
}

// HandlePause is an alias for PostScreenPause.
func (h *Handler) HandlePause(w http.ResponseWriter, r *http.Request) {
	h.PostScreenPause(w, r)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(api.ErrorResponse{
		Status: "error",
		Error:  msg,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
