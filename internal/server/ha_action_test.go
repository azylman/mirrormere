package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/azylman/mirrormere/internal/server"
)

type mockWidgetActionCaller struct {
	mu           sync.Mutex
	calls        []mockActionCall
	errToReturn  error
	dataToReturn any
}

type mockActionCall struct {
	widgetID    string
	domain      string
	service     string
	serviceData map[string]any
}

func (m *mockWidgetActionCaller) CallWidgetAction(ctx context.Context, widgetID, domain, service string, serviceData map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, mockActionCall{
		widgetID:    widgetID,
		domain:      domain,
		service:     service,
		serviceData: serviceData,
	})
	return m.errToReturn
}

type mockWidgetActionResponseCaller struct {
	mockWidgetActionCaller
}

func (m *mockWidgetActionResponseCaller) CallWidgetActionWithResponse(ctx context.Context, widgetID, domain, service string, serviceData map[string]any) (any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, mockActionCall{
		widgetID:    widgetID,
		domain:      domain,
		service:     service,
		serviceData: serviceData,
	})
	return m.dataToReturn, m.errToReturn
}

func TestWidgetAction_NotFound(t *testing.T) {
	t.Parallel()
	srv := server.New(server.Config{})
	body := strings.NewReader(`{"domain":"light","service":"turn_on"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/widgets/light-1/action", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rec.Code)
	}
}

func TestWidgetAction_MethodNotAllowed(t *testing.T) {
	t.Parallel()
	caller := &mockWidgetActionCaller{}
	handler := server.NewDefaultWidgetActionHandler(server.WithWidgetActionCaller(caller))
	srv := server.New(server.Config{WidgetActionHandler: handler})

	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		req := httptest.NewRequest(method, "/api/widgets/light-1/action", nil)
		rec := httptest.NewRecorder()
		srv.Routes().ServeHTTP(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("method %s: expected status 405, got %d", method, rec.Code)
		}
		if allow := rec.Header().Get("Allow"); !strings.Contains(allow, "POST") {
			t.Errorf("method %s: expected Allow header to contain POST, got %q", method, allow)
		}
	}
}

func TestWidgetAction_CORS(t *testing.T) {
	t.Parallel()
	caller := &mockWidgetActionCaller{}
	handler := server.NewDefaultWidgetActionHandler(server.WithWidgetActionCaller(caller))
	srv := server.New(server.Config{WidgetActionHandler: handler})

	req := httptest.NewRequest(http.MethodOptions, "/api/widgets/light-1/action", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status 204 for OPTIONS preflight, got %d", rec.Code)
	}
	if origin := rec.Header().Get("Access-Control-Allow-Origin"); origin != "*" {
		t.Errorf("expected Access-Control-Allow-Origin '*', got %q", origin)
	}
	if methods := rec.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(methods, "POST") {
		t.Errorf("expected Access-Control-Allow-Methods to contain POST, got %q", methods)
	}
}

func TestWidgetAction_InvalidWidgetID(t *testing.T) {
	t.Parallel()
	caller := &mockWidgetActionCaller{}
	handler := server.NewDefaultWidgetActionHandler(server.WithWidgetActionCaller(caller))

	// Direct call to PostWidgetAction with invalid IDs
	invalidIDs := []string{"", "  ", "../escape", "nested/slash"}
	for _, id := range invalidIDs {
		body := strings.NewReader(`{"domain":"light","service":"turn_on"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/widgets/placeholder/action", body)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.PostWidgetAction(rec, req, id)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("id %q: expected 400 Bad Request, got %d", id, rec.Code)
		}
	}
}

func TestWidgetAction_InvalidJSON(t *testing.T) {
	t.Parallel()
	caller := &mockWidgetActionCaller{}
	handler := server.NewDefaultWidgetActionHandler(server.WithWidgetActionCaller(caller))
	srv := server.New(server.Config{WidgetActionHandler: handler})

	tests := []struct {
		name string
		body string
	}{
		{"malformed_json", `{invalid-json`},
		{"empty_body", ``},
		{"not_json_array", `["not", "an", "object"]`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodPost, "/api/widgets/ha-1/action", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			srv.Routes().ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 Bad Request, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestWidgetAction_MissingDomainOrService(t *testing.T) {
	t.Parallel()
	caller := &mockWidgetActionCaller{}
	handler := server.NewDefaultWidgetActionHandler(server.WithWidgetActionCaller(caller))
	srv := server.New(server.Config{WidgetActionHandler: handler})

	tests := []struct {
		name string
		body string
	}{
		{"empty_object", `{}`},
		{"missing_service", `{"domain":"light"}`},
		{"missing_domain", `{"service":"turn_on"}`},
		{"empty_domain_and_service", `{"domain":"","service":""}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodPost, "/api/widgets/ha-1/action", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			srv.Routes().ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 Bad Request, got %d: %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "domain and service are required") {
				t.Errorf("expected error message to mention domain and service, got: %s", rec.Body.String())
			}
		})
	}
}

func TestWidgetAction_CallerSuccess(t *testing.T) {
	t.Parallel()
	caller := &mockWidgetActionCaller{}
	handler := server.NewDefaultWidgetActionHandler(server.WithWidgetActionCaller(caller))
	srv := server.New(server.Config{WidgetActionHandler: handler})

	payload := `{
		"domain": "light",
		"service": "turn_on",
		"service_data": {
			"entity_id": "light.kitchen",
			"brightness": 200
		}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/widgets/living-room-ha/action", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp server.WidgetActionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response JSON: %v", err)
	}
	if resp.Status != "ok" || resp.WidgetID != "living-room-ha" {
		t.Errorf("unexpected response: %+v", resp)
	}

	caller.mu.Lock()
	defer caller.mu.Unlock()
	if len(caller.calls) != 1 {
		t.Fatalf("expected 1 caller call, got %d", len(caller.calls))
	}
	c := caller.calls[0]
	if c.widgetID != "living-room-ha" || c.domain != "light" || c.service != "turn_on" {
		t.Errorf("unexpected call arguments: %+v", c)
	}
	if c.serviceData["entity_id"] != "light.kitchen" {
		t.Errorf("expected entity_id 'light.kitchen', got %v", c.serviceData["entity_id"])
	}
	if b, ok := c.serviceData["brightness"].(float64); !ok || int(b) != 200 {
		t.Errorf("expected brightness 200, got %v", c.serviceData["brightness"])
	}
}

func TestWidgetAction_DotServiceFormat(t *testing.T) {
	t.Parallel()
	caller := &mockWidgetActionCaller{}
	handler := server.NewWidgetActionHandler(caller)
	srv := server.New(server.Config{WidgetActionHandler: handler})

	payload := `{
		"service": "light.toggle",
		"entity_id": "light.porch"
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/widgets/lights-widget/action", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	caller.mu.Lock()
	defer caller.mu.Unlock()
	if len(caller.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(caller.calls))
	}
	c := caller.calls[0]
	if c.domain != "light" || c.service != "toggle" {
		t.Errorf("expected light.toggle, got domain=%q service=%q", c.domain, c.service)
	}
	if c.serviceData["entity_id"] != "light.porch" {
		t.Errorf("expected entity_id light.porch, got %v", c.serviceData["entity_id"])
	}
}

func TestWidgetAction_SlashServiceFormat(t *testing.T) {
	t.Parallel()
	caller := &mockWidgetActionCaller{}
	handler := server.NewDefaultWidgetActionHandler(server.WithWidgetActionCaller(caller))
	srv := server.New(server.Config{WidgetActionHandler: handler})

	payload := `{
		"service": "switch/turn_off",
		"data": {
			"entity_id": "switch.bedroom_fan"
		}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/widgets/switch-widget/action", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	caller.mu.Lock()
	defer caller.mu.Unlock()
	if len(caller.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(caller.calls))
	}
	c := caller.calls[0]
	if c.domain != "switch" || c.service != "turn_off" {
		t.Errorf("expected switch.turn_off, got domain=%q service=%q", c.domain, c.service)
	}
	if c.serviceData["entity_id"] != "switch.bedroom_fan" {
		t.Errorf("expected entity_id switch.bedroom_fan, got %v", c.serviceData["entity_id"])
	}
}

func TestWidgetAction_ActionFieldFormat(t *testing.T) {
	t.Parallel()
	caller := &mockWidgetActionCaller{}
	handler := server.NewDefaultWidgetActionHandler(server.WithWidgetActionCaller(caller))
	srv := server.New(server.Config{WidgetActionHandler: handler})

	payload := `{
		"action": "climate.set_temperature",
		"target": {
			"entity_id": "climate.living_room"
		},
		"data": {
			"temperature": 71
		}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/widgets/climate-widget/action", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	caller.mu.Lock()
	defer caller.mu.Unlock()
	if len(caller.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(caller.calls))
	}
	c := caller.calls[0]
	if c.domain != "climate" || c.service != "set_temperature" {
		t.Errorf("expected climate.set_temperature, got domain=%q service=%q", c.domain, c.service)
	}
	if c.serviceData["entity_id"] != "climate.living_room" {
		t.Errorf("expected entity_id climate.living_room, got %v", c.serviceData["entity_id"])
	}
	if temp, ok := c.serviceData["temperature"].(float64); !ok || int(temp) != 71 {
		t.Errorf("expected temperature 71, got %v", c.serviceData["temperature"])
	}
}

func TestWidgetAction_CallerWithResponse(t *testing.T) {
	t.Parallel()
	respData := []map[string]any{
		{"entity_id": "light.kitchen", "state": "on", "brightness": 255},
	}
	caller := &mockWidgetActionResponseCaller{
		dataToReturn: respData,
	}
	handler := server.NewDefaultWidgetActionHandler(server.WithWidgetActionCaller(caller))
	srv := server.New(server.Config{WidgetActionHandler: handler})

	payload := `{"domain":"light","service":"turn_on","entity_id":"light.kitchen"}`
	req := httptest.NewRequest(http.MethodPost, "/api/widgets/ha-lights/action", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp server.WidgetActionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Data == nil {
		t.Fatal("expected non-nil response data")
	}
}

func TestWidgetAction_CallerErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"not_found", errors.New("widget not found: widget-99"), http.StatusNotFound},
		{"unauthorized", errors.New("upstream service unauthorized"), http.StatusUnauthorized},
		{"invalid_entity", errors.New("invalid entity_id specified"), http.StatusBadRequest},
		{"unreachable", errors.New("upstream server connection refused"), http.StatusBadGateway},
		{"generic_error", errors.New("something unexpected went wrong"), http.StatusInternalServerError},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			caller := &mockWidgetActionCaller{errToReturn: tc.err}
			handler := server.NewDefaultWidgetActionHandler(server.WithWidgetActionCaller(caller))
			srv := server.New(server.Config{WidgetActionHandler: handler})

			payload := `{"domain":"light","service":"turn_on","entity_id":"light.kitchen"}`
			req := httptest.NewRequest(http.MethodPost, "/api/widgets/ha-lights/action", strings.NewReader(payload))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			srv.Routes().ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d: %s", tc.wantStatus, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestWidgetAction_NotConfigured(t *testing.T) {
	t.Parallel()
	// Handler without caller or baseURL
	handler := server.NewDefaultWidgetActionHandler()
	srv := server.New(server.Config{WidgetActionHandler: handler})

	payload := `{"domain":"light","service":"turn_on","entity_id":"light.kitchen"}`
	req := httptest.NewRequest(http.MethodPost, "/api/widgets/ha-lights/action", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "widget action caller not configured") {
		t.Errorf("expected not configured error, got: %s", rec.Body.String())
	}
}

func TestHAAction_DirectProxy_Success(t *testing.T) {
	t.Parallel()
	const secretToken = "super-secret-ha-token-do-not-leak"
	var receivedAuth string
	var receivedPath string
	var receivedBody []byte

	haServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		receivedPath = r.URL.Path
		receivedBody, _ = io.ReadAll(r.Body)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"entity_id":"light.couch","state":"on"}]`))
	}))
	defer haServer.Close()

	handler := server.NewDefaultWidgetActionHandler(
		server.WithHAActionClient(haServer.Client(), haServer.URL, secretToken),
		server.WithWidgetActionLogger(slog.Default()),
	)
	srv := server.New(server.Config{WidgetActionHandler: handler})

	payload := `{
		"domain": "light",
		"service": "turn_on",
		"service_data": {
			"entity_id": "light.couch"
		}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/widgets/couch-widget/action", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	if receivedPath != "/api/services/light/turn_on" {
		t.Errorf("expected upstream path /api/services/light/turn_on, got %s", receivedPath)
	}
	if receivedAuth != "Bearer "+secretToken {
		t.Errorf("expected Authorization 'Bearer %s', got %q", secretToken, receivedAuth)
	}

	var upstreamPayload map[string]any
	if err := json.Unmarshal(receivedBody, &upstreamPayload); err != nil {
		t.Fatalf("failed to decode upstream body: %v", err)
	}
	if upstreamPayload["entity_id"] != "light.couch" {
		t.Errorf("expected entity_id light.couch, got %v", upstreamPayload["entity_id"])
	}

	// Verify token is NOT leaked in the response to the client
	respBody := rec.Body.String()
	if strings.Contains(respBody, secretToken) {
		t.Fatalf("SECURITY VIOLATION: secret token leaked in client response: %s", respBody)
	}

	var resp server.WidgetActionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response JSON: %v", err)
	}
	if resp.Status != "ok" || resp.WidgetID != "couch-widget" {
		t.Errorf("unexpected response: %+v", resp)
	}
}

func TestHAAction_DirectProxy_UpstreamErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		upstreamStatus int
		upstreamBody   string
		wantClientCode int
	}{
		{"ha_unauthorized", http.StatusUnauthorized, "401: Unauthorized", http.StatusUnauthorized},
		{"ha_not_found", http.StatusNotFound, "Service not found", http.StatusNotFound},
		{"ha_bad_request", http.StatusBadRequest, "Invalid entity", http.StatusBadRequest},
		{"ha_server_error", http.StatusInternalServerError, "Internal failure", http.StatusBadGateway},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			haServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.upstreamStatus)
				_, _ = w.Write([]byte(tc.upstreamBody))
			}))
			defer haServer.Close()

			handler := server.NewDefaultWidgetActionHandler(
				server.WithHAActionClient(haServer.Client(), haServer.URL, "token"),
			)
			srv := server.New(server.Config{WidgetActionHandler: handler})

			payload := `{"domain":"light","service":"toggle","entity_id":"light.1"}`
			req := httptest.NewRequest(http.MethodPost, "/api/widgets/test-w/action", strings.NewReader(payload))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			srv.Routes().ServeHTTP(rec, req)

			if rec.Code != tc.wantClientCode {
				t.Fatalf("expected client status %d, got %d: %s", tc.wantClientCode, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestHAAction_DirectProxy_ConnectionFailure(t *testing.T) {
	t.Parallel()
	// Create and immediately close server to ensure connection failure
	haServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	haServer.Close()

	handler := server.NewDefaultWidgetActionHandler(
		server.WithHAActionClient(haServer.Client(), haServer.URL, "token"),
	)
	srv := server.New(server.Config{WidgetActionHandler: handler})

	payload := `{"domain":"light","service":"toggle","entity_id":"light.1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/widgets/test-w/action", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 Bad Gateway on connection failure, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHAAction_ConfigFieldActionHandler(t *testing.T) {
	t.Parallel()
	caller := &mockWidgetActionCaller{}
	handler := server.NewWidgetActionHandler(caller)

	// Test with ActionHandler field instead of WidgetActionHandler
	srv := server.New(server.Config{ActionHandler: handler})

	payload := `{"domain":"light","service":"turn_on","entity_id":"light.1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/widgets/test-w/action", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK via ActionHandler field, got %d", rec.Code)
	}
	caller.mu.Lock()
	defer caller.mu.Unlock()
	if len(caller.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(caller.calls))
	}
}

func TestHAAction_DirectProxy_NilServiceData(t *testing.T) {
	t.Parallel()
	var receivedBody []byte
	haServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer haServer.Close()

	handler := server.NewDefaultWidgetActionHandler(
		server.WithHAActionClient(haServer.Client(), haServer.URL, "token"),
	)
	srv := server.New(server.Config{WidgetActionHandler: handler})

	// Service without any extra data
	payload := `{"domain":"light","service":"turn_on"}`
	req := httptest.NewRequest(http.MethodPost, "/api/widgets/test-w/action", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}
	if !bytes.Equal(bytes.TrimSpace(receivedBody), []byte("{}")) {
		t.Errorf("expected empty JSON object '{}', got %q", string(receivedBody))
	}
}

func TestWidgetAction_DomainAndServiceValidation(t *testing.T) {
	t.Parallel()
	caller := &mockWidgetActionCaller{}
	handler := server.NewDefaultWidgetActionHandler(server.WithWidgetActionCaller(caller))
	srv := server.New(server.Config{WidgetActionHandler: handler})

	t.Run("invalid domain format rejected", func(t *testing.T) {
		t.Parallel()
		invalidDomains := []string{
			"../..",
			"../../api",
			"../light",
			"light/admin",
			"/etc/passwd",
			"light..turn_on",
			"light;drop",
			"light turn_on",
			"light?admin=true",
			"LIGHT",
		}
		for _, dom := range invalidDomains {
			payload := fmt.Sprintf(`{"domain":%q,"service":"turn_on"}`, dom)
			req := httptest.NewRequest(http.MethodPost, "/api/widgets/w-test/action", strings.NewReader(payload))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			srv.Routes().ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("domain %q: expected 400 Bad Request, got %d: %s", dom, rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "invalid domain") {
				t.Errorf("domain %q: expected error mentioning invalid domain, got: %s", dom, rec.Body.String())
			}
		}
	})

	t.Run("unsupported domain rejected even if identifier is valid", func(t *testing.T) {
		t.Parallel()
		unsupportedDomains := []string{
			"automation",
			"zone",
			"shell_command",
			"hassio",
			"input_boolean",
			"input_number",
		}
		for _, dom := range unsupportedDomains {
			payload := fmt.Sprintf(`{"domain":%q,"service":"turn_on"}`, dom)
			req := httptest.NewRequest(http.MethodPost, "/api/widgets/w-test/action", strings.NewReader(payload))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			srv.Routes().ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("domain %q: expected 400 Bad Request, got %d: %s", dom, rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "unsupported domain") {
				t.Errorf("domain %q: expected error mentioning unsupported domain, got: %s", dom, rec.Body.String())
			}
		}
	})

	t.Run("invalid service format rejected", func(t *testing.T) {
		t.Parallel()
		invalidServices := []string{
			"../..",
			"turn_on/../admin",
			"api/config",
			"../../../api/template",
			"turn_on?evil=1",
			"turn_on#frag",
			"turn on",
			"turn-on",
			"TURN_ON",
			"turn_on/..",
		}
		for _, svc := range invalidServices {
			payload := fmt.Sprintf(`{"domain":"light","service":%q}`, svc)
			req := httptest.NewRequest(http.MethodPost, "/api/widgets/w-test/action", strings.NewReader(payload))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			srv.Routes().ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("service %q: expected 400 Bad Request, got %d: %s", svc, rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "invalid service") {
				t.Errorf("service %q: expected error mentioning invalid service, got: %s", svc, rec.Body.String())
			}
		}
	})
}

func TestWidgetAction_DirectProxyPathEscape(t *testing.T) {
	t.Parallel()
	var requestedPath string
	haServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success"}`))
	}))
	defer haServer.Close()

	handler := server.NewDefaultWidgetActionHandler(
		server.WithHAActionClient(haServer.Client(), haServer.URL, "token"),
	)
	srv := server.New(server.Config{WidgetActionHandler: handler})

	payload := `{"domain":"light","service":"turn_on"}`
	req := httptest.NewRequest(http.MethodPost, "/api/widgets/test-w/action", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}
	expectedPath := "/api/services/light/turn_on"
	if requestedPath != expectedPath {
		t.Errorf("expected path %q, got %q", expectedPath, requestedPath)
	}
}
