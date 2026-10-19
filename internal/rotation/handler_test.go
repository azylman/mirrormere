package rotation

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/events"
)

type mockController struct {
	ctrlErr     error
	totalScreen int
}

func doReq(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	var r io.Reader
	if body != "<nil>" {
		r = strings.NewReader(body)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, r))
	return rec
}

func (m *mockController) SelectScreen(index int) (events.ScreenRotateData, error) {
	if m.ctrlErr != nil {
		return events.ScreenRotateData{}, m.ctrlErr
	}
	return events.ScreenRotateData{CurrentScreen: index, TotalScreens: m.totalScreen}, nil
}
func (m *mockController) AdvanceScreen(dir string) (events.ScreenRotateData, error) {
	if m.ctrlErr != nil {
		return events.ScreenRotateData{}, m.ctrlErr
	}
	return events.ScreenRotateData{CurrentScreen: 1, TotalScreens: m.totalScreen}, nil
}
func (m *mockController) PauseRotation(d time.Duration) (int, error) { return 0, m.ctrlErr }
func (m *mockController) ResumeRotation() (int, error)               { return 0, m.ctrlErr }
func (m *mockController) CurrentScreen() int                         { return 0 }
func (m *mockController) TotalScreens() int                          { return m.totalScreen }
func (m *mockController) IsPaused() bool                             { return false }

func TestHandler_PostScreenSelect(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		method   string
		body     string
		nilCtrl  bool
		ctrlErr  error
		useAlias bool
		wantCode int
		wantSub  string
	}{
		{"valid_index_2", http.MethodPost, `{"screen_index":2}`, false, nil, false, http.StatusOK, `"current_screen":2`},
		{"alias_handle_select", http.MethodPost, `{"screen_index":1}`, false, nil, true, http.StatusOK, `"current_screen":1`},
		{"options_cors", http.MethodOptions, "", false, nil, false, http.StatusNoContent, ""},
		{"get_not_allowed", http.MethodGet, "", false, nil, false, http.StatusMethodNotAllowed, ""},
		{"nil_controller", http.MethodPost, `{"screen_index":1}`, true, nil, false, http.StatusInternalServerError, "not configured"},
		{"invalid_json", http.MethodPost, `{bad`, false, nil, false, http.StatusBadRequest, "invalid request body"},
		{"ctrl_err", http.MethodPost, `{"screen_index":9}`, false, ErrInvalidScreenIndex, false, http.StatusBadRequest, ErrInvalidScreenIndex.Error()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var ctrl ScreenController
			if !tc.nilCtrl {
				ctrl = &mockController{totalScreen: 3, ctrlErr: tc.ctrlErr}
			}
			h := NewHandler(ctrl)
			fn := h.PostScreenSelect
			if tc.useAlias {
				fn = h.HandleSelect
			}
			rec := doReq(http.HandlerFunc(fn), tc.method, "/api/screen/select", tc.body)
			if rec.Code != tc.wantCode || (tc.wantSub != "" && !strings.Contains(rec.Body.String(), tc.wantSub)) {
				t.Fatalf("got (%d, %q), want (%d, sub %q)", rec.Code, rec.Body.String(), tc.wantCode, tc.wantSub)
			}
		})
	}
}

func TestHandler_PostScreenAdvance(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		method   string
		body     string
		nilCtrl  bool
		ctrlErr  error
		useAlias bool
		wantCode int
		wantSub  string
	}{
		{"default_next", http.MethodPost, "", false, nil, false, http.StatusOK, `"current_screen":1`},
		{"nil_body", http.MethodPost, "<nil>", false, nil, false, http.StatusOK, `"current_screen":1`},
		{"alias_handle_advance", http.MethodPost, "", false, nil, true, http.StatusOK, `"current_screen":1`},
		{"explicit_prev", http.MethodPost, `{"direction":"prev"}`, false, nil, false, http.StatusOK, `"current_screen":1`},
		{"options_cors", http.MethodOptions, "", false, nil, false, http.StatusNoContent, ""},
		{"method_not_allowed", http.MethodPut, "", false, nil, false, http.StatusMethodNotAllowed, ""},
		{"nil_controller", http.MethodPost, "", true, nil, false, http.StatusInternalServerError, "not configured"},
		{"invalid_json", http.MethodPost, `{broken`, false, nil, false, http.StatusBadRequest, "invalid request body"},
		{"ctrl_err", http.MethodPost, `{"direction":"bad"}`, false, ErrInvalidDirection, false, http.StatusBadRequest, ErrInvalidDirection.Error()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var ctrl ScreenController
			if !tc.nilCtrl {
				ctrl = &mockController{totalScreen: 2, ctrlErr: tc.ctrlErr}
			}
			h := NewHandler(ctrl)
			fn := h.PostScreenAdvance
			if tc.useAlias {
				fn = h.HandleAdvance
			}
			rec := doReq(http.HandlerFunc(fn), tc.method, "/api/screen/advance", tc.body)
			if rec.Code != tc.wantCode || (tc.wantSub != "" && !strings.Contains(rec.Body.String(), tc.wantSub)) {
				t.Fatalf("got (%d, %q), want (%d, sub %q)", rec.Code, rec.Body.String(), tc.wantCode, tc.wantSub)
			}
		})
	}
}

func TestHandler_PostScreenPause(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		method   string
		body     string
		nilCtrl  bool
		ctrlErr  error
		useAlias bool
		wantCode int
		wantSub  string
	}{
		{"pause_default", http.MethodPost, `{"paused":true}`, false, nil, false, http.StatusOK, `"paused":true`},
		{"pause_explicit", http.MethodPost, `{"paused":true,"duration_seconds":60}`, false, nil, false, http.StatusOK, `"paused":true`},
		{"resume", http.MethodPost, `{"paused":false}`, false, nil, false, http.StatusOK, `"paused":false`},
		{"alias_handle_pause", http.MethodPost, `{"paused":true}`, false, nil, true, http.StatusOK, `"paused":true`},
		{"options_cors", http.MethodOptions, "", false, nil, false, http.StatusNoContent, ""},
		{"method_not_allowed", http.MethodDelete, "", false, nil, false, http.StatusMethodNotAllowed, ""},
		{"nil_controller", http.MethodPost, `{"paused":true}`, true, nil, false, http.StatusInternalServerError, "not configured"},
		{"invalid_json", http.MethodPost, `{bad`, false, nil, false, http.StatusBadRequest, "invalid payload"},
		{"missing_paused", http.MethodPost, `{}`, false, nil, false, http.StatusBadRequest, "invalid payload"},
		{"missing_paused_dur", http.MethodPost, `{"duration_seconds":120}`, false, nil, false, http.StatusBadRequest, "invalid payload"},
		{"non_bool_paused", http.MethodPost, `{"paused":"true"}`, false, nil, false, http.StatusBadRequest, "invalid payload"},
		{"negative_dur", http.MethodPost, `{"paused":true,"duration_seconds":-5}`, false, nil, false, http.StatusBadRequest, "duration_seconds"},
		{"pause_err", http.MethodPost, `{"paused":true}`, false, errors.New("pause err"), false, http.StatusBadRequest, "pause err"},
		{"resume_err", http.MethodPost, `{"paused":false}`, false, errors.New("resume err"), false, http.StatusBadRequest, "resume err"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var ctrl ScreenController
			if !tc.nilCtrl {
				ctrl = &mockController{ctrlErr: tc.ctrlErr}
			}
			h := NewHandler(ctrl)
			fn := h.PostScreenPause
			if tc.useAlias {
				fn = h.HandlePause
			}
			rec := doReq(http.HandlerFunc(fn), tc.method, "/api/screen/pause", tc.body)
			if rec.Code != tc.wantCode || (tc.wantSub != "" && !strings.Contains(rec.Body.String(), tc.wantSub)) {
				t.Fatalf("got (%d, %q), want (%d, sub %q)", rec.Code, rec.Body.String(), tc.wantCode, tc.wantSub)
			}
		})
	}
}

func TestWriteJSONAndErrorHelpers(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	writeError(rec, http.StatusBadRequest, "sample error")
	if rec.Code != http.StatusBadRequest || rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("unexpected writeError")
	}

	rec2 := httptest.NewRecorder()
	writeJSON(rec2, map[string]string{"result": "ok"})
	if rec2.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec2.Code)
	}
}
