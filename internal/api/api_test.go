package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type mockServer struct {
	lastWidgetID string
	lastType     string
	lastPath     string
	healthCalls  int
	healthzCalls int
}

func (m *mockServer) GetHealth(w http.ResponseWriter, r *http.Request) {
	m.healthCalls++
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(HealthResponse{Status: "ok"})
}

func (m *mockServer) GetHealthz(w http.ResponseWriter, r *http.Request) {
	m.healthzCalls++
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(HealthResponse{Status: "ok"})
}

func (m *mockServer) GetWidgetRender(w http.ResponseWriter, r *http.Request, widgetID string) {
	m.lastWidgetID = widgetID
	if widgetID == "not-found" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(ErrorResponse{
			Status: "error",
			Error:  fmt.Sprintf("widget '%s' not found in active configuration", widgetID),
		})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(fmt.Sprintf("<div id=\"%s\">rendered</div>", widgetID)))
}

func (m *mockServer) GetWidgetAsset(w http.ResponseWriter, r *http.Request, pType string, path string) {
	m.lastType = pType
	m.lastPath = path
	if pType == "missing" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(ErrorResponse{
			Status: "error",
			Error:  "asset not found",
		})
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("<svg></svg>"))
}

func TestHandler_Endpoints(t *testing.T) {
	t.Parallel()

	t.Run("GET /healthz", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		handler := Handler(mock)

		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		var resp HealthResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if resp.Status != "ok" {
			t.Fatalf("expected status 'ok', got '%s'", resp.Status)
		}
		if mock.healthzCalls != 1 {
			t.Fatalf("expected 1 healthz call, got %d", mock.healthzCalls)
		}
	})

	t.Run("GET /health", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		handler := Handler(mock)

		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		var resp HealthResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if resp.Status != "ok" {
			t.Fatalf("expected status 'ok', got '%s'", resp.Status)
		}
		if mock.healthCalls != 1 {
			t.Fatalf("expected 1 health call, got %d", mock.healthCalls)
		}
	})

	t.Run("GET /api/widgets/{widget_id}/render success", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		handler := Handler(mock)

		req := httptest.NewRequest(http.MethodGet, "/api/widgets/clock-primary/render", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if mock.lastWidgetID != "clock-primary" {
			t.Fatalf("expected widget_id 'clock-primary', got '%s'", mock.lastWidgetID)
		}
		if !strings.Contains(rec.Body.String(), "clock-primary") {
			t.Fatalf("expected body to contain 'clock-primary', got %s", rec.Body.String())
		}
	})

	t.Run("GET /api/widgets/{widget_id}/render not found", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		handler := Handler(mock)

		req := httptest.NewRequest(http.MethodGet, "/api/widgets/not-found/render", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}
		var errResp ErrorResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
			t.Fatalf("failed to decode error response: %v", err)
		}
		if errResp.Status != "error" {
			t.Fatalf("expected status 'error', got '%s'", errResp.Status)
		}
	})

	t.Run("GET /widget-types/{type}/assets/{path} success", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		handler := Handler(mock)

		req := httptest.NewRequest(http.MethodGet, "/widget-types/weather/assets/sun.svg", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if mock.lastType != "weather" {
			t.Fatalf("expected type 'weather', got '%s'", mock.lastType)
		}
		if mock.lastPath != "sun.svg" {
			t.Fatalf("expected path 'sun.svg', got '%s'", mock.lastPath)
		}
	})

	t.Run("GET /widget-types/{type}/assets/{path} not found", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		handler := Handler(mock)

		req := httptest.NewRequest(http.MethodGet, "/widget-types/missing/assets/sun.svg", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}
	})
}

func TestHandlerWithOptions_CustomMuxAndBaseURL(t *testing.T) {
	t.Parallel()

	mock := &mockServer{}
	customMux := http.NewServeMux()

	mwCalls := make(map[string]int)
	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mwCalls[r.URL.Path]++
			next.ServeHTTP(w, r)
		})
	}

	handler := HandlerWithOptions(mock, StdHTTPServerOptions{
		BaseURL:     "/v1",
		BaseRouter:  customMux,
		Middlewares: []MiddlewareFunc{mw},
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "custom: "+err.Error(), http.StatusBadRequest)
		},
	})

	endpoints := []string{
		"/v1/healthz",
		"/v1/health",
		"/v1/api/widgets/widget-1/render",
		"/v1/widget-types/clock/assets/icon.svg",
	}

	for _, ep := range endpoints {
		req := httptest.NewRequest(http.MethodGet, ep, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("endpoint %s expected 200, got %d", ep, rec.Code)
		}
		if mwCalls[ep] != 1 {
			t.Fatalf("expected middleware call for %s, got %d", ep, mwCalls[ep])
		}
	}
}

func TestHandlerFromMux(t *testing.T) {
	t.Parallel()

	mock := &mockServer{}
	mux := http.NewServeMux()
	handler := HandlerFromMux(mock, mux)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestHandlerFromMuxWithBaseURL(t *testing.T) {
	t.Parallel()

	mock := &mockServer{}
	mux := http.NewServeMux()
	handler := HandlerFromMuxWithBaseURL(mock, mux, "/prefix")

	req := httptest.NewRequest(http.MethodGet, "/prefix/healthz", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestServerInterfaceWrapper_ParameterErrors(t *testing.T) {
	t.Parallel()

	t.Run("Default error handler triggers on missing widget_id", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		wrapper := ServerInterfaceWrapper{
			Handler: mock,
			ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
				http.Error(w, err.Error(), http.StatusBadRequest)
			},
		}

		req := httptest.NewRequest(http.MethodGet, "/api/widgets//render", nil)
		rec := httptest.NewRecorder()
		wrapper.GetWidgetRender(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("Default error handler triggers on missing asset type", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		wrapper := ServerInterfaceWrapper{
			Handler: mock,
			ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
				http.Error(w, err.Error(), http.StatusBadRequest)
			},
		}

		req := httptest.NewRequest(http.MethodGet, "/widget-types//assets/icon.svg", nil)
		rec := httptest.NewRecorder()
		wrapper.GetWidgetAsset(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("Default error handler triggers on missing asset path", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		wrapper := ServerInterfaceWrapper{
			Handler: mock,
			ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
				http.Error(w, err.Error(), http.StatusBadRequest)
			},
		}

		req := httptest.NewRequest(http.MethodGet, "/widget-types/clock/assets/", nil)
		req.SetPathValue("type", "clock")
		rec := httptest.NewRecorder()
		wrapper.GetWidgetAsset(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("Default error handler func in HandlerWithOptions", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		h := HandlerWithOptions(mock, StdHTTPServerOptions{})
		if h == nil {
			t.Fatalf("expected non-nil handler")
		}
	})
}

func TestErrorTypes(t *testing.T) {
	t.Parallel()

	rootErr := errors.New("underlying cause")

	t.Run("UnescapedCookieParamError", func(t *testing.T) {
		t.Parallel()
		err := &UnescapedCookieParamError{ParamName: "session", Err: rootErr}
		if !strings.Contains(err.Error(), "session") {
			t.Fatalf("expected error string to contain 'session', got %s", err.Error())
		}
		if !errors.Is(err, rootErr) {
			t.Fatalf("expected Unwrap to return rootErr")
		}
	})

	t.Run("UnmarshalingParamError", func(t *testing.T) {
		t.Parallel()
		err := &UnmarshalingParamError{ParamName: "filter", Err: rootErr}
		if !strings.Contains(err.Error(), "filter") {
			t.Fatalf("expected error string to contain 'filter', got %s", err.Error())
		}
		if !errors.Is(err, rootErr) {
			t.Fatalf("expected Unwrap to return rootErr")
		}
	})

	t.Run("RequiredParamError", func(t *testing.T) {
		t.Parallel()
		err := &RequiredParamError{ParamName: "id"}
		if !strings.Contains(err.Error(), "id") {
			t.Fatalf("expected error string to contain 'id', got %s", err.Error())
		}
	})

	t.Run("RequiredHeaderError", func(t *testing.T) {
		t.Parallel()
		err := &RequiredHeaderError{ParamName: "Authorization", Err: rootErr}
		if !strings.Contains(err.Error(), "Authorization") {
			t.Fatalf("expected error string to contain 'Authorization', got %s", err.Error())
		}
		if !errors.Is(err, rootErr) {
			t.Fatalf("expected Unwrap to return rootErr")
		}
	})

	t.Run("InvalidParamFormatError", func(t *testing.T) {
		t.Parallel()
		err := &InvalidParamFormatError{ParamName: "count", Err: rootErr}
		if !strings.Contains(err.Error(), "count") {
			t.Fatalf("expected error string to contain 'count', got %s", err.Error())
		}
		if !errors.Is(err, rootErr) {
			t.Fatalf("expected Unwrap to return rootErr")
		}
	})

	t.Run("TooManyValuesForParamError", func(t *testing.T) {
		t.Parallel()
		err := &TooManyValuesForParamError{ParamName: "tag", Count: 3}
		if !strings.Contains(err.Error(), "tag") || !strings.Contains(err.Error(), "3") {
			t.Fatalf("expected error string to contain 'tag' and '3', got %s", err.Error())
		}
	})
}
