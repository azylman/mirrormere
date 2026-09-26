package rotation

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/api"
	"github.com/azylman/mirrormere/internal/events"
)

type mockController struct {
	selectFunc  func(index int) (events.ScreenRotateData, error)
	advanceFunc func(direction string) (events.ScreenRotateData, error)
	pauseFunc   func(duration time.Duration) (int, error)
	resumeFunc  func() (int, error)
	curScreen   int
	totalScreen int
	paused      bool
}

func (m *mockController) SelectScreen(index int) (events.ScreenRotateData, error) {
	if m.selectFunc != nil {
		return m.selectFunc(index)
	}
	return events.ScreenRotateData{CurrentScreen: index, TotalScreens: m.totalScreen}, nil
}

func (m *mockController) AdvanceScreen(direction string) (events.ScreenRotateData, error) {
	if m.advanceFunc != nil {
		return m.advanceFunc(direction)
	}
	return events.ScreenRotateData{CurrentScreen: 1, TotalScreens: m.totalScreen}, nil
}

func (m *mockController) PauseRotation(duration time.Duration) (int, error) {
	if m.pauseFunc != nil {
		return m.pauseFunc(duration)
	}
	return m.curScreen, nil
}

func (m *mockController) ResumeRotation() (int, error) {
	if m.resumeFunc != nil {
		return m.resumeFunc()
	}
	return m.curScreen, nil
}

func (m *mockController) CurrentScreen() int { return m.curScreen }
func (m *mockController) TotalScreens() int  { return m.totalScreen }
func (m *mockController) IsPaused() bool     { return m.paused }

func TestHandler_PostScreenSelect(t *testing.T) {
	t.Parallel()

	t.Run("OPTIONS CORS preflight", func(t *testing.T) {
		t.Parallel()
		h := NewHandler(&mockController{})
		req := httptest.NewRequest(http.MethodOptions, "/api/screen/select", nil)
		rec := httptest.NewRecorder()

		h.PostScreenSelect(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected 204, got %d", rec.Code)
		}
		if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Errorf("expected CORS origin '*', got %q", rec.Header().Get("Access-Control-Allow-Origin"))
		}
	})

	t.Run("Method not allowed", func(t *testing.T) {
		t.Parallel()
		h := NewHandler(&mockController{})
		req := httptest.NewRequest(http.MethodGet, "/api/screen/select", nil)
		rec := httptest.NewRecorder()

		h.PostScreenSelect(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405, got %d", rec.Code)
		}
		if rec.Header().Get("Allow") != "POST, OPTIONS" {
			t.Errorf("expected Allow 'POST, OPTIONS', got %q", rec.Header().Get("Allow"))
		}
	})

	t.Run("Nil controller error", func(t *testing.T) {
		t.Parallel()
		h := NewHandler(nil)
		req := httptest.NewRequest(http.MethodPost, "/api/screen/select", strings.NewReader(`{"screen_index":0}`))
		rec := httptest.NewRecorder()

		h.PostScreenSelect(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d", rec.Code)
		}
	})

	t.Run("Invalid JSON body", func(t *testing.T) {
		t.Parallel()
		h := NewHandler(&mockController{})
		req := httptest.NewRequest(http.MethodPost, "/api/screen/select", strings.NewReader(`{invalid`))
		rec := httptest.NewRecorder()

		h.PostScreenSelect(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
		var errResp api.ErrorResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &errResp)
		if !strings.Contains(errResp.Error, "invalid request body") {
			t.Errorf("expected 'invalid request body', got %q", errResp.Error)
		}
	})

	t.Run("Controller select error", func(t *testing.T) {
		t.Parallel()
		ctrl := &mockController{
			selectFunc: func(index int) (events.ScreenRotateData, error) {
				return events.ScreenRotateData{}, ErrInvalidScreenIndex
			},
		}
		h := NewHandler(ctrl)
		req := httptest.NewRequest(http.MethodPost, "/api/screen/select", strings.NewReader(`{"screen_index":99}`))
		rec := httptest.NewRecorder()

		h.HandleSelect(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
		var errResp api.ErrorResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &errResp)
		if errResp.Error != ErrInvalidScreenIndex.Error() {
			t.Errorf("expected %q, got %q", ErrInvalidScreenIndex.Error(), errResp.Error)
		}
	})

	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		ctrl := &mockController{
			selectFunc: func(index int) (events.ScreenRotateData, error) {
				return events.ScreenRotateData{CurrentScreen: index, TotalScreens: 3}, nil
			},
		}
		h := NewHandler(ctrl)
		req := httptest.NewRequest(http.MethodPost, "/api/screen/select", strings.NewReader(`{"screen_index":2}`))
		rec := httptest.NewRecorder()

		h.PostScreenSelect(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		var navResp api.ScreenNavigationResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &navResp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if navResp.Status != "ok" || navResp.CurrentScreen != 2 || navResp.TotalScreens != 3 {
			t.Errorf("unexpected response: %+v", navResp)
		}
	})
}

func TestHandler_PostScreenAdvance(t *testing.T) {
	t.Parallel()

	t.Run("OPTIONS CORS preflight", func(t *testing.T) {
		t.Parallel()
		h := NewHandler(&mockController{})
		req := httptest.NewRequest(http.MethodOptions, "/api/screen/advance", nil)
		rec := httptest.NewRecorder()

		h.PostScreenAdvance(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected 204, got %d", rec.Code)
		}
	})

	t.Run("Method not allowed", func(t *testing.T) {
		t.Parallel()
		h := NewHandler(&mockController{})
		req := httptest.NewRequest(http.MethodPut, "/api/screen/advance", nil)
		rec := httptest.NewRecorder()

		h.PostScreenAdvance(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405, got %d", rec.Code)
		}
	})

	t.Run("Nil controller error", func(t *testing.T) {
		t.Parallel()
		h := NewHandler(nil)
		req := httptest.NewRequest(http.MethodPost, "/api/screen/advance", nil)
		rec := httptest.NewRecorder()

		h.PostScreenAdvance(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d", rec.Code)
		}
	})

	t.Run("Empty or nil body defaults to next", func(t *testing.T) {
		t.Parallel()
		var receivedDir string
		ctrl := &mockController{
			advanceFunc: func(direction string) (events.ScreenRotateData, error) {
				receivedDir = direction
				return events.ScreenRotateData{CurrentScreen: 1, TotalScreens: 2}, nil
			},
		}
		h := NewHandler(ctrl)

		// 1. Nil body
		req1 := httptest.NewRequest(http.MethodPost, "/api/screen/advance", nil)
		rec1 := httptest.NewRecorder()
		h.PostScreenAdvance(rec1, req1)
		if rec1.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec1.Code)
		}
		if receivedDir != "next" {
			t.Errorf("expected 'next', got %q", receivedDir)
		}

		// 2. Empty body string
		req2 := httptest.NewRequest(http.MethodPost, "/api/screen/advance", strings.NewReader(""))
		rec2 := httptest.NewRecorder()
		h.HandleAdvance(rec2, req2)
		if rec2.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec2.Code)
		}
		if receivedDir != "next" {
			t.Errorf("expected 'next', got %q", receivedDir)
		}
	})

	t.Run("Explicit prev direction", func(t *testing.T) {
		t.Parallel()
		var receivedDir string
		ctrl := &mockController{
			advanceFunc: func(direction string) (events.ScreenRotateData, error) {
				receivedDir = direction
				return events.ScreenRotateData{CurrentScreen: 0, TotalScreens: 2}, nil
			},
		}
		h := NewHandler(ctrl)
		req := httptest.NewRequest(http.MethodPost, "/api/screen/advance", strings.NewReader(`{"direction":"prev"}`))
		rec := httptest.NewRecorder()

		h.PostScreenAdvance(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if receivedDir != "prev" {
			t.Errorf("expected 'prev', got %q", receivedDir)
		}
	})

	t.Run("Invalid JSON body", func(t *testing.T) {
		t.Parallel()
		h := NewHandler(&mockController{})
		req := httptest.NewRequest(http.MethodPost, "/api/screen/advance", strings.NewReader(`{broken`))
		rec := httptest.NewRecorder()

		h.PostScreenAdvance(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("Controller advance error", func(t *testing.T) {
		t.Parallel()
		ctrl := &mockController{
			advanceFunc: func(direction string) (events.ScreenRotateData, error) {
				return events.ScreenRotateData{}, ErrInvalidDirection
			},
		}
		h := NewHandler(ctrl)
		req := httptest.NewRequest(http.MethodPost, "/api/screen/advance", strings.NewReader(`{"direction":"bad"}`))
		rec := httptest.NewRecorder()

		h.PostScreenAdvance(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
		var errResp api.ErrorResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &errResp)
		if errResp.Error != ErrInvalidDirection.Error() {
			t.Errorf("expected %q, got %q", ErrInvalidDirection.Error(), errResp.Error)
		}
	})
}

func TestHandler_PostScreenPause(t *testing.T) {
	t.Parallel()

	t.Run("OPTIONS CORS preflight", func(t *testing.T) {
		t.Parallel()
		h := NewHandler(&mockController{})
		req := httptest.NewRequest(http.MethodOptions, "/api/screen/pause", nil)
		rec := httptest.NewRecorder()

		h.PostScreenPause(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected 204, got %d", rec.Code)
		}
	})

	t.Run("Method not allowed", func(t *testing.T) {
		t.Parallel()
		h := NewHandler(&mockController{})
		req := httptest.NewRequest(http.MethodDelete, "/api/screen/pause", nil)
		rec := httptest.NewRecorder()

		h.PostScreenPause(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405, got %d", rec.Code)
		}
	})

	t.Run("Nil controller error", func(t *testing.T) {
		t.Parallel()
		h := NewHandler(nil)
		req := httptest.NewRequest(http.MethodPost, "/api/screen/pause", strings.NewReader(`{"paused":true}`))
		rec := httptest.NewRecorder()

		h.PostScreenPause(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d", rec.Code)
		}
	})

	t.Run("Invalid JSON body", func(t *testing.T) {
		t.Parallel()
		h := NewHandler(&mockController{})
		req := httptest.NewRequest(http.MethodPost, "/api/screen/pause", strings.NewReader(`{invalid`))
		rec := httptest.NewRecorder()

		h.PostScreenPause(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("Negative duration_seconds", func(t *testing.T) {
		t.Parallel()
		h := NewHandler(&mockController{})
		req := httptest.NewRequest(http.MethodPost, "/api/screen/pause", strings.NewReader(`{"paused":true,"duration_seconds":-5}`))
		rec := httptest.NewRecorder()

		h.PostScreenPause(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
		var errResp api.ErrorResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &errResp)
		if !strings.Contains(errResp.Error, "duration_seconds must be >= 0") {
			t.Errorf("unexpected error: %q", errResp.Error)
		}
	})

	t.Run("Pause with omitted duration defaults to -1", func(t *testing.T) {
		t.Parallel()
		var receivedDur time.Duration
		ctrl := &mockController{
			pauseFunc: func(duration time.Duration) (int, error) {
				receivedDur = duration
				return 0, nil
			},
		}
		h := NewHandler(ctrl)
		req := httptest.NewRequest(http.MethodPost, "/api/screen/pause", strings.NewReader(`{"paused":true}`))
		rec := httptest.NewRecorder()

		h.HandlePause(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if receivedDur != -1 {
			t.Errorf("expected duration -1, got %v", receivedDur)
		}
		var pauseResp api.ScreenPauseResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &pauseResp)
		if !pauseResp.Paused || pauseResp.CurrentScreen != 0 || pauseResp.Status != "ok" {
			t.Errorf("unexpected response: %+v", pauseResp)
		}
	})

	t.Run("Pause with explicit duration", func(t *testing.T) {
		t.Parallel()
		var receivedDur time.Duration
		ctrl := &mockController{
			pauseFunc: func(duration time.Duration) (int, error) {
				receivedDur = duration
				return 1, nil
			},
		}
		h := NewHandler(ctrl)
		req := httptest.NewRequest(http.MethodPost, "/api/screen/pause", strings.NewReader(`{"paused":true,"duration_seconds":60}`))
		rec := httptest.NewRecorder()

		h.PostScreenPause(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if receivedDur != 60*time.Second {
			t.Errorf("expected 60s, got %v", receivedDur)
		}
		var pauseResp api.ScreenPauseResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &pauseResp)
		if !pauseResp.Paused || pauseResp.CurrentScreen != 1 {
			t.Errorf("unexpected response: %+v", pauseResp)
		}
	})

	t.Run("Resume rotation", func(t *testing.T) {
		t.Parallel()
		resumed := false
		ctrl := &mockController{
			resumeFunc: func() (int, error) {
				resumed = true
				return 2, nil
			},
		}
		h := NewHandler(ctrl)
		req := httptest.NewRequest(http.MethodPost, "/api/screen/pause", strings.NewReader(`{"paused":false}`))
		rec := httptest.NewRecorder()

		h.PostScreenPause(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if !resumed {
			t.Errorf("expected resumeFunc called")
		}
		var pauseResp api.ScreenPauseResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &pauseResp)
		if pauseResp.Paused || pauseResp.CurrentScreen != 2 {
			t.Errorf("unexpected response: %+v", pauseResp)
		}
	})

	t.Run("Controller pause error", func(t *testing.T) {
		t.Parallel()
		ctrl := &mockController{
			pauseFunc: func(duration time.Duration) (int, error) {
				return 0, errors.New("coordinator stopped")
			},
		}
		h := NewHandler(ctrl)
		req := httptest.NewRequest(http.MethodPost, "/api/screen/pause", strings.NewReader(`{"paused":true}`))
		rec := httptest.NewRecorder()

		h.PostScreenPause(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("Controller resume error", func(t *testing.T) {
		t.Parallel()
		ctrl := &mockController{
			resumeFunc: func() (int, error) {
				return 0, errors.New("coordinator stopped")
			},
		}
		h := NewHandler(ctrl)
		req := httptest.NewRequest(http.MethodPost, "/api/screen/pause", strings.NewReader(`{"paused":false}`))
		rec := httptest.NewRecorder()

		h.PostScreenPause(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("Missing paused field in empty body returns 400", func(t *testing.T) {
		t.Parallel()
		h := NewHandler(&mockController{})
		req := httptest.NewRequest(http.MethodPost, "/api/screen/pause", strings.NewReader(`{}`))
		rec := httptest.NewRecorder()

		h.PostScreenPause(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
		var errResp api.ErrorResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &errResp)
		if errResp.Error != "invalid payload: paused must be a boolean" {
			t.Errorf("expected %q, got %q", "invalid payload: paused must be a boolean", errResp.Error)
		}
	})

	t.Run("Missing paused field with duration returns 400", func(t *testing.T) {
		t.Parallel()
		h := NewHandler(&mockController{})
		req := httptest.NewRequest(http.MethodPost, "/api/screen/pause", strings.NewReader(`{"duration_seconds":120}`))
		rec := httptest.NewRecorder()

		h.PostScreenPause(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
		var errResp api.ErrorResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &errResp)
		if errResp.Error != "invalid payload: paused must be a boolean" {
			t.Errorf("expected %q, got %q", "invalid payload: paused must be a boolean", errResp.Error)
		}
	})

	t.Run("Non-boolean paused field returns 400", func(t *testing.T) {
		t.Parallel()
		h := NewHandler(&mockController{})
		req := httptest.NewRequest(http.MethodPost, "/api/screen/pause", strings.NewReader(`{"paused":"true"}`))
		rec := httptest.NewRecorder()

		h.PostScreenPause(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
		var errResp api.ErrorResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &errResp)
		if errResp.Error != "invalid payload: paused must be a boolean" {
			t.Errorf("expected %q, got %q", "invalid payload: paused must be a boolean", errResp.Error)
		}
	})
}

func TestWriteJSONAndErrorHelpers(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	writeError(rec, http.StatusBadRequest, "sample error")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	if rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", rec.Header().Get("Content-Type"))
	}

	rec2 := httptest.NewRecorder()
	var buf bytes.Buffer
	buf.WriteString("hello")
	writeJSON(rec2, map[string]string{"result": "ok"})
	if rec2.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec2.Code)
	}
}
