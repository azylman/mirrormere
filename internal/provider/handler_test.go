package provider_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/provider"
)

type mockWidgetPusher struct {
	pushFunc func(ctx context.Context, widgetID string, data map[string]any) (provider.WidgetPayload, error)
}

func (m *mockWidgetPusher) PushWidgetData(ctx context.Context, widgetID string, data map[string]any) (provider.WidgetPayload, error) {
	if m.pushFunc != nil {
		return m.pushFunc(ctx, widgetID, data)
	}
	return provider.WidgetPayload{
		WidgetID:  widgetID,
		Data:      data,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		State:     "ok",
	}, nil
}

type errReader struct{}

func (errReader) Read(p []byte) (n int, err error) {
	return 0, errors.New("read error simulated")
}

func TestNewPushHandler(t *testing.T) {
	t.Parallel()

	hDefaultLog := provider.NewPushHandler(&mockWidgetPusher{}, nil)
	if hDefaultLog == nil {
		t.Fatal("expected non-nil PushHandler")
	}

	hCustomLog := provider.NewPushHandler(&mockWidgetPusher{}, slog.Default())
	if hCustomLog == nil {
		t.Fatal("expected non-nil PushHandler")
	}
}

func TestPushHandler_PostWidgetPush(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		method         string
		widgetID       string
		body           io.Reader
		pusher         provider.WidgetPusher
		wantStatusCode int
		wantAllowHdr   string
		checkResponse  func(t *testing.T, rec *httptest.ResponseRecorder)
	}{
		{
			name:           "options preflight returns 204 and CORS headers",
			method:         http.MethodOptions,
			widgetID:       "tile-1",
			body:           nil,
			pusher:         &mockWidgetPusher{},
			wantStatusCode: http.StatusNoContent,
			checkResponse: func(t *testing.T, rec *httptest.ResponseRecorder) {
				if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
					t.Errorf("Access-Control-Allow-Origin = %q, want *", got)
				}
				if got := rec.Header().Get("Access-Control-Allow-Methods"); got != "POST, OPTIONS" {
					t.Errorf("Access-Control-Allow-Methods = %q, want POST, OPTIONS", got)
				}
			},
		},
		{
			name:           "method not allowed returns 405",
			method:         http.MethodGet,
			widgetID:       "tile-1",
			body:           nil,
			pusher:         &mockWidgetPusher{},
			wantStatusCode: http.StatusMethodNotAllowed,
			wantAllowHdr:   "POST, OPTIONS",
			checkResponse: func(t *testing.T, rec *httptest.ResponseRecorder) {
				var res map[string]string
				if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
					t.Fatalf("failed to decode response: %v", err)
				}
				if res["status"] != "error" || !strings.Contains(res["error"], "method not allowed") {
					t.Errorf("unexpected body: %v", res)
				}
			},
		},
		{
			name:           "nil pusher returns 500",
			method:         http.MethodPost,
			widgetID:       "tile-1",
			body:           strings.NewReader(`{"hello":"world"}`),
			pusher:         nil,
			wantStatusCode: http.StatusInternalServerError,
			checkResponse: func(t *testing.T, rec *httptest.ResponseRecorder) {
				var res map[string]string
				if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
					t.Fatalf("failed to decode response: %v", err)
				}
				if res["error"] != "pusher not configured" {
					t.Errorf("expected 'pusher not configured', got %q", res["error"])
				}
			},
		},
		{
			name:           "read body error returns 400",
			method:         http.MethodPost,
			widgetID:       "tile-1",
			body:           errReader{},
			pusher:         &mockWidgetPusher{},
			wantStatusCode: http.StatusBadRequest,
			checkResponse: func(t *testing.T, rec *httptest.ResponseRecorder) {
				var res map[string]string
				if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
					t.Fatalf("failed to decode response: %v", err)
				}
				if !strings.Contains(res["error"], "failed to read request body") {
					t.Errorf("unexpected error msg: %q", res["error"])
				}
			},
		},
		{
			name:           "invalid json returns 400",
			method:         http.MethodPost,
			widgetID:       "tile-1",
			body:           strings.NewReader(`{invalid-json`),
			pusher:         &mockWidgetPusher{},
			wantStatusCode: http.StatusBadRequest,
			checkResponse: func(t *testing.T, rec *httptest.ResponseRecorder) {
				var res map[string]string
				if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
					t.Fatalf("failed to decode response: %v", err)
				}
				if !strings.Contains(res["error"], "invalid JSON payload") {
					t.Errorf("unexpected error msg: %q", res["error"])
				}
			},
		},
		{
			name:           "non-object json array returns 400",
			method:         http.MethodPost,
			widgetID:       "tile-1",
			body:           strings.NewReader(`[1, 2, 3]`),
			pusher:         &mockWidgetPusher{},
			wantStatusCode: http.StatusBadRequest,
			checkResponse: func(t *testing.T, rec *httptest.ResponseRecorder) {
				var res map[string]string
				if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
					t.Fatalf("failed to decode response: %v", err)
				}
				if res["error"] != "payload must be a JSON object" {
					t.Errorf("unexpected error msg: %q", res["error"])
				}
			},
		},
		{
			name:     "widget not found returns 404",
			method:   http.MethodPost,
			widgetID: "tile-missing",
			body:     strings.NewReader(`{"status":"ok"}`),
			pusher: &mockWidgetPusher{
				pushFunc: func(ctx context.Context, widgetID string, data map[string]any) (provider.WidgetPayload, error) {
					return provider.WidgetPayload{}, provider.ErrWidgetNotFound
				},
			},
			wantStatusCode: http.StatusNotFound,
			checkResponse: func(t *testing.T, rec *httptest.ResponseRecorder) {
				var res map[string]string
				if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
					t.Fatalf("failed to decode response: %v", err)
				}
				if !strings.Contains(res["error"], "not found in active configuration") {
					t.Errorf("unexpected error msg: %q", res["error"])
				}
			},
		},
		{
			name:     "push to list widget returns 409 conflict",
			method:   http.MethodPost,
			widgetID: "tile-tasks",
			body:     strings.NewReader(`{"status":"ok"}`),
			pusher: &mockWidgetPusher{
				pushFunc: func(ctx context.Context, widgetID string, data map[string]any) (provider.WidgetPayload, error) {
					return provider.WidgetPayload{}, provider.ErrListWidgetPushForbidden
				},
			},
			wantStatusCode: http.StatusConflict,
			checkResponse: func(t *testing.T, rec *httptest.ResponseRecorder) {
				var res map[string]string
				if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
					t.Fatalf("failed to decode response: %v", err)
				}
				if !strings.Contains(res["error"], "cannot push state to list-backed widget") {
					t.Errorf("unexpected error msg: %q", res["error"])
				}
			},
		},
		{
			name:     "push to non-http widget returns 409 conflict",
			method:   http.MethodPost,
			widgetID: "tile-calendar",
			body:     strings.NewReader(`{"status":"ok"}`),
			pusher: &mockWidgetPusher{
				pushFunc: func(ctx context.Context, widgetID string, data map[string]any) (provider.WidgetPayload, error) {
					return provider.WidgetPayload{}, provider.ErrNonHTTPWidgetPushForbidden
				},
			},
			wantStatusCode: http.StatusConflict,
			checkResponse: func(t *testing.T, rec *httptest.ResponseRecorder) {
				var res map[string]string
				if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
					t.Fatalf("failed to decode response: %v", err)
				}
				if !strings.Contains(res["error"], "push webhook is strictly limited to widgets using the http provider") {
					t.Errorf("unexpected error msg: %q", res["error"])
				}
			},
		},
		{
			name:     "schema validation error returns 400",
			method:   http.MethodPost,
			widgetID: "tile-weather",
			body:     strings.NewReader(`{"temp":"not a number"}`),
			pusher: &mockWidgetPusher{
				pushFunc: func(ctx context.Context, widgetID string, data map[string]any) (provider.WidgetPayload, error) {
					return provider.WidgetPayload{}, errors.Join(provider.ErrSchemaValidation, errors.New("type mismatch"))
				},
			},
			wantStatusCode: http.StatusBadRequest,
			checkResponse: func(t *testing.T, rec *httptest.ResponseRecorder) {
				var res map[string]string
				if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
					t.Fatalf("failed to decode response: %v", err)
				}
				if !strings.Contains(res["error"], "schema validation failed") && !strings.Contains(res["error"], "type mismatch") {
					t.Errorf("unexpected error msg: %q", res["error"])
				}
			},
		},
		{
			name:     "generic internal error returns 500",
			method:   http.MethodPost,
			widgetID: "tile-error",
			body:     strings.NewReader(`{"key":"value"}`),
			pusher: &mockWidgetPusher{
				pushFunc: func(ctx context.Context, widgetID string, data map[string]any) (provider.WidgetPayload, error) {
					return provider.WidgetPayload{}, errors.New("unexpected database error")
				},
			},
			wantStatusCode: http.StatusInternalServerError,
			checkResponse: func(t *testing.T, rec *httptest.ResponseRecorder) {
				var res map[string]string
				if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
					t.Fatalf("failed to decode response: %v", err)
				}
				if !strings.Contains(res["error"], "push ingestion error") {
					t.Errorf("unexpected error msg: %q", res["error"])
				}
			},
		},
		{
			name:     "valid push returns 200 with ok status",
			method:   http.MethodPost,
			widgetID: "tile-sensor",
			body:     strings.NewReader(`{"temperature":21.5,"humidity":45.2}`),
			pusher: &mockWidgetPusher{
				pushFunc: func(ctx context.Context, widgetID string, data map[string]any) (provider.WidgetPayload, error) {
					return provider.WidgetPayload{
						WidgetID:  widgetID,
						Data:      data,
						Timestamp: "2026-09-26T00:00:00Z",
						State:     "ok",
					}, nil
				},
			},
			wantStatusCode: http.StatusOK,
			checkResponse: func(t *testing.T, rec *httptest.ResponseRecorder) {
				var res map[string]any
				if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
					t.Fatalf("failed to decode response: %v", err)
				}
				if res["status"] != "ok" {
					t.Errorf("status = %v, want ok", res["status"])
				}
				if res["widget_id"] != "tile-sensor" {
					t.Errorf("widget_id = %v, want tile-sensor", res["widget_id"])
				}
				if res["updated_at"] != "2026-09-26T00:00:00Z" {
					t.Errorf("updated_at = %v, want 2026-09-26T00:00:00Z", res["updated_at"])
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler := provider.NewPushHandler(tc.pusher, nil)

			var bodyReader io.Reader = http.NoBody
			if tc.body != nil {
				bodyReader = tc.body
			}
			req := httptest.NewRequest(tc.method, "/api/widgets/"+tc.widgetID+"/push", bodyReader)
			rec := httptest.NewRecorder()

			handler.PostWidgetPush(rec, req, tc.widgetID)

			if rec.Code != tc.wantStatusCode {
				t.Fatalf("status code = %d, want %d; body: %s", rec.Code, tc.wantStatusCode, rec.Body.String())
			}
			if tc.wantAllowHdr != "" {
				if got := rec.Header().Get("Allow"); got != tc.wantAllowHdr {
					t.Errorf("Allow header = %q, want %q", got, tc.wantAllowHdr)
				}
			}
			if tc.checkResponse != nil {
				tc.checkResponse(t, rec)
			}
		})
	}
}
