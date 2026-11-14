package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type haServiceCapturedReq struct {
	method string
	path   string
	auth   string
	body   map[string]any
}

type haServiceTestCase struct {
	name           string
	domain         string
	service        string
	serviceData    map[string]any
	wantErrIs      error
	wantErrSubstr  string
	wantStateCount int
	wantPath       string
}

func setupHomeAssistantServiceServer(t *testing.T, recorded map[string]haServiceCapturedReq, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		_ = json.Unmarshal(body, &parsed)

		mu.Lock()
		recorded[r.URL.Path] = haServiceCapturedReq{
			method: r.Method,
			path:   r.URL.Path,
			auth:   r.Header.Get("Authorization"),
			body:   parsed,
		}
		mu.Unlock()

		switch {
		case strings.HasPrefix(r.URL.Path, "/api/services/light/turn_on"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"entity_id": "light.living_room", "state": "on", "attributes": {"entity_picture": "/api/camera_proxy/light.living_room"}}]`))
		case strings.HasPrefix(r.URL.Path, "/api/services/light/not_found"):
			http.Error(w, "Service not found", http.StatusNotFound)
		case strings.HasPrefix(r.URL.Path, "/api/services/light/unauthorized"):
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func assertHaServiceError(t *testing.T, err error, tc haServiceTestCase) {
	t.Helper()
	if !errors.Is(err, tc.wantErrIs) {
		t.Fatalf("expected error wrapping %v, got %v", tc.wantErrIs, err)
	}
	if tc.wantErrSubstr != "" && !strings.Contains(err.Error(), tc.wantErrSubstr) {
		t.Fatalf("expected error containing %q, got %v", tc.wantErrSubstr, err)
	}
	if errors.Is(tc.wantErrIs, ErrUpstreamNotFound) || errors.Is(tc.wantErrIs, ErrUpstreamUnauthorized) {
		if !errors.Is(err, ErrUpstreamStatus) {
			t.Errorf("expected error to also wrap ErrUpstreamStatus, got %v", err)
		}
	}
}

func assertHaServiceSuccess(t *testing.T, states []EntityState, serverURL string, tc haServiceTestCase) {
	t.Helper()
	if len(states) != tc.wantStateCount {
		t.Fatalf("expected %d states, got %d", tc.wantStateCount, len(states))
	}
	if len(states) > 0 {
		gotPic, _ := states[0].Attributes["entity_picture"].(string)
		wantPic := serverURL + "/api/camera_proxy/light.living_room"
		if gotPic != wantPic {
			t.Errorf("expected normalized entity_picture %q, got %q", wantPic, gotPic)
		}
	}
}

func assertHaServiceCapturedReq(ctx context.Context, t *testing.T, p *HomeAssistantProvider, tc haServiceTestCase, recorded map[string]haServiceCapturedReq, mu *sync.Mutex) {
	t.Helper()
	if err := p.CallService(ctx, tc.domain, tc.service, tc.serviceData); err != nil {
		t.Fatalf("CallService failed: %v", err)
	}
	mu.Lock()
	capReq := recorded[tc.wantPath]
	mu.Unlock()
	if capReq.method != http.MethodPost {
		t.Errorf("expected POST, got %q", capReq.method)
	}
	if capReq.path != tc.wantPath {
		t.Errorf("expected path %q, got %q", tc.wantPath, capReq.path)
	}
	if capReq.auth != "Bearer bearer-test-token" {
		t.Errorf("expected Bearer header, got %q", capReq.auth)
	}
	if capReq.body["entity_id"] != "light.living_room" {
		t.Errorf("expected entity_id light.living_room, got %v", capReq.body["entity_id"])
	}
}

func runHaCallServiceCase(ctx context.Context, t *testing.T, p *HomeAssistantProvider, tc haServiceTestCase, serverURL string, recorded map[string]haServiceCapturedReq, mu *sync.Mutex) {
	t.Helper()
	states, err := p.CallServiceWithResponse(ctx, tc.domain, tc.service, tc.serviceData)
	if tc.wantErrIs != nil {
		assertHaServiceError(t, err, tc)
		return
	}
	if err != nil {
		t.Fatalf("CallServiceWithResponse failed: %v", err)
	}
	assertHaServiceSuccess(t, states, serverURL, tc)
	if tc.wantPath != "" {
		assertHaServiceCapturedReq(ctx, t, p, tc, recorded, mu)
	}
}

func TestHomeAssistant_CallService(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	recorded := make(map[string]haServiceCapturedReq)
	server := setupHomeAssistantServiceServer(t, recorded, &mu)

	ctx := context.Background()
	p := NewHomeAssistantProviderWithClient(server.Client(), server.URL, "bearer-test-token")
	if err := p.Init(ctx, map[string]any{"url": server.URL}, InitOptions{Token: "bearer-test-token"}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	tests := []haServiceTestCase{
		{
			name:    "successful service call with data and relative entity_picture normalization",
			domain:  "light",
			service: "turn_on",
			serviceData: map[string]any{
				"entity_id":  "light.living_room",
				"brightness": 255,
			},
			wantStateCount: 1,
			wantPath:       "/api/services/light/turn_on",
		},
		{
			name:          "empty domain returns ErrActionValidation",
			domain:        "",
			service:       "turn_on",
			wantErrIs:     ErrActionValidation,
			wantErrSubstr: "domain and service must not be empty",
		},
		{
			name:          "empty service returns ErrActionValidation",
			domain:        "light",
			service:       "",
			wantErrIs:     ErrActionValidation,
			wantErrSubstr: "domain and service must not be empty",
		},
		{
			name:          "path traversal domain rejected with ErrActionValidation",
			domain:        "../../api",
			service:       "turn_on",
			wantErrIs:     ErrActionValidation,
			wantErrSubstr: "invalid domain",
		},
		{
			name:          "uppercase domain rejected with ErrActionValidation",
			domain:        "LIGHT",
			service:       "turn_on",
			wantErrIs:     ErrActionValidation,
			wantErrSubstr: "invalid domain",
		},
		{
			name:          "unsupported domain rejected with ErrActionValidation",
			domain:        "automation",
			service:       "trigger",
			wantErrIs:     ErrActionValidation,
			wantErrSubstr: "unsupported domain",
		},
		{
			name:          "path traversal service rejected with ErrActionValidation",
			domain:        "light",
			service:       "turn_on/../admin",
			wantErrIs:     ErrActionValidation,
			wantErrSubstr: "invalid service",
		},
		{
			name:          "upstream 404 wraps ErrUpstreamStatus and ErrUpstreamNotFound",
			domain:        "light",
			service:       "not_found",
			wantErrIs:     ErrUpstreamNotFound,
			wantErrSubstr: "404",
		},
		{
			name:          "upstream 401 wraps ErrUpstreamStatus and ErrUpstreamUnauthorized",
			domain:        "light",
			service:       "unauthorized",
			wantErrIs:     ErrUpstreamUnauthorized,
			wantErrSubstr: "401",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runHaCallServiceCase(ctx, t, p, tc, server.URL, recorded, &mu)
		})
	}
}

func setupCallServiceWithResponseServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-tok" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch {
		case strings.Contains(r.URL.Path, "empty_status_error"):
			w.WriteHeader(http.StatusBadGateway)
		case strings.Contains(r.URL.Path, "status_error"):
			http.Error(w, "server boom", http.StatusInternalServerError)
		case strings.Contains(r.URL.Path, "empty_unauth"):
			w.WriteHeader(http.StatusForbidden)
		case strings.Contains(r.URL.Path, "empty_notfound"):
			w.WriteHeader(http.StatusNotFound)
		case strings.Contains(r.URL.Path, "invalid_json"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("not-json"))
		case strings.Contains(r.URL.Path, "empty_resp"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(""))
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"entity_id":"light.desk","state":"on"}]`))
		}
	}))
}

func assertCallServiceStatusErrors(ctx context.Context, t *testing.T, p *HomeAssistantProvider) {
	t.Helper()
	_, err := p.CallServiceWithResponse(ctx, "light", "status_error", nil)
	if !errors.Is(err, ErrUpstreamStatus) || !strings.Contains(err.Error(), "returned status 500") {
		t.Fatalf("expected status 500 ErrUpstreamStatus error, got: %v", err)
	}

	_, err = p.CallServiceWithResponse(ctx, "light", "empty_status_error", nil)
	if !errors.Is(err, ErrUpstreamStatus) || !strings.Contains(err.Error(), "returned status 502") {
		t.Fatalf("expected status 502 ErrUpstreamStatus error, got: %v", err)
	}

	_, err = p.CallServiceWithResponse(ctx, "light", "empty_unauth", nil)
	if !errors.Is(err, ErrUpstreamUnauthorized) || !errors.Is(err, ErrUpstreamStatus) {
		t.Fatalf("expected status 403 ErrUpstreamUnauthorized error, got: %v", err)
	}

	_, err = p.CallServiceWithResponse(ctx, "light", "empty_notfound", nil)
	if !errors.Is(err, ErrUpstreamNotFound) || !errors.Is(err, ErrUpstreamStatus) {
		t.Fatalf("expected status 404 ErrUpstreamNotFound error, got: %v", err)
	}
}

func assertCallServicePayloadAndContext(ctx context.Context, t *testing.T, p *HomeAssistantProvider) {
	t.Helper()
	states, err := p.CallServiceWithResponse(ctx, "light", "invalid_json", nil)
	if err != nil {
		t.Fatalf("unexpected error on invalid json: %v", err)
	}
	if states != nil {
		t.Errorf("expected nil states on invalid json, got: %v", states)
	}

	states, err = p.CallServiceWithResponse(ctx, "light", "empty_resp", nil)
	if err != nil {
		t.Fatalf("unexpected error on empty response: %v", err)
	}
	if len(states) != 0 {
		t.Errorf("expected 0 states for empty response, got %d", len(states))
	}

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = p.CallServiceWithResponse(canceledCtx, "light", "turn_on", nil)
	if !errors.Is(err, ErrUpstreamUnavailable) || !strings.Contains(err.Error(), "service call failed") {
		t.Fatalf("expected ErrUpstreamUnavailable service call failed error, got: %v", err)
	}
}

func TestHomeAssistant_CallServiceWithResponse_ErrorsAndDefaults(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p := NewHomeAssistantProviderWithClient(http.DefaultClient, "http://198.51.100.10:8123", "tok")
	_, err := p.CallServiceWithResponse(ctx, "light", "turn_on", map[string]any{
		"invalid_func": make(chan int),
	})
	if !errors.Is(err, ErrActionValidation) || !strings.Contains(err.Error(), "failed to marshal service data") {
		t.Fatalf("expected ErrActionValidation marshal error, got: %v", err)
	}

	ts := setupCallServiceWithResponseServer()
	defer ts.Close()

	pNilClient := NewHomeAssistantProvider()
	pNilClient.baseURL = ts.URL
	pNilClient.token = "test-tok"
	pNilClient.client = nil

	assertCallServiceStatusErrors(ctx, t, pNilClient)
	assertCallServicePayloadAndContext(ctx, t, pNilClient)
}

func TestHomeAssistant_FetchLovelaceConfig_ErrorsAndFallbacks(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "invalid_json") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{invalid"))
			return
		}
		http.Error(w, "internal crash", http.StatusInternalServerError)
	}))
	defer ts.Close()

	p := NewHomeAssistantProviderWithClient(ts.Client(), ts.URL, "tok")
	p.client = nil
	_, err := p.FetchLovelaceConfig(ctx, "")
	if err == nil || !strings.Contains(err.Error(), "lovelace config request returned status 500") {
		t.Fatalf("expected status 500 error, got: %v", err)
	}

	_, err = p.FetchLovelaceConfig(ctx, "invalid_json")
	if err == nil || !strings.Contains(err.Error(), "failed to decode lovelace config") {
		t.Fatalf("expected decode error, got: %v", err)
	}

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = p.FetchLovelaceConfig(canceledCtx, "")
	if err == nil || !strings.Contains(err.Error(), "lovelace config request failed") {
		t.Fatalf("expected fetch error on canceled ctx, got: %v", err)
	}
}

func TestHomeAssistant_HydrateStates_ErrorHandling(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal crash", http.StatusInternalServerError)
	}))
	defer ts.Close()

	p := NewHomeAssistantProviderWithClient(ts.Client(), ts.URL, "tok")
	p.client = nil
	_, err := p.HydrateStates(ctx)
	if err == nil || !strings.Contains(err.Error(), "states request returned status 500") {
		t.Fatalf("expected status 500 error, got: %v", err)
	}

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = p.HydrateStates(canceledCtx)
	if err == nil || !strings.Contains(err.Error(), "states request failed") {
		t.Fatalf("expected fetch error on canceled ctx, got: %v", err)
	}
}

func TestCoordinator_CallWidgetAction_RoutingAndRefresh(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/api/services/light/turn_on") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"entity_id":"light.living_room","state":"on"}]`))
			return
		}
		if strings.Contains(r.URL.Path, "/api/services/light/error_svc") {
			http.Error(w, "ha error", http.StatusInternalServerError)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	ctx := context.Background()
	hap := NewHomeAssistantProviderWithClient(ts.Client(), ts.URL, "test-tok")

	c := NewCoordinator(CoordinatorConfig{}, nil)
	defer func() {
		c.mu.Lock()
		c.workers = nil
		c.mu.Unlock()
		_ = c.Shutdown(ctx)
	}()

	_, err := c.CallWidgetActionWithResponse(ctx, "nonexistent-widget", "light", "turn_on", nil)
	if !errors.Is(err, ErrWidgetNotFound) {
		t.Fatalf("expected ErrWidgetNotFound error, got: %v", err)
	}

	c.mu.Lock()
	c.workers = map[string]*worker{
		"ha-widget": {
			widgetID:  "ha-widget",
			provider:  hap,
			refreshCh: make(chan struct{}, 1),
		},
	}
	c.mu.Unlock()

	_, err = c.CallWidgetActionWithResponse(ctx, "ha-widget", "light", "error_svc", nil)
	if !errors.Is(err, ErrUpstreamStatus) {
		t.Fatalf("expected ErrUpstreamStatus error, got %v", err)
	}

	res, err := c.CallWidgetActionWithResponse(ctx, "ha-widget", "light", "turn_on", map[string]any{"brightness": 255})
	if err != nil {
		t.Fatalf("unexpected action error: %v", err)
	}
	states, ok := res.([]EntityState)
	if !ok || len(states) != 1 || states[0].State != "on" {
		t.Fatalf("unexpected action response states: %+v", res)
	}

	c.mu.Lock()
	c.workers = map[string]*worker{
		"some-other-key": {
			widgetID:  "mismatched-key",
			provider:  hap,
			refreshCh: make(chan struct{}, 1),
		},
	}
	c.mu.Unlock()

	res, err = c.CallWidgetActionWithResponse(ctx, "mismatched-key", "light", "turn_on", nil)
	if err != nil {
		t.Fatalf("expected CallWidgetActionWithResponse to succeed even if refresh fails, got: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil response")
	}

	c.mu.Lock()
	c.workers = map[string]*worker{
		"ha-widget": {
			widgetID:  "ha-widget",
			provider:  hap,
			refreshCh: make(chan struct{}, 1),
		},
	}
	c.mu.Unlock()

	if err := c.CallWidgetAction(ctx, "ha-widget", "light", "turn_on", nil); err != nil {
		t.Fatalf("unexpected CallWidgetAction error: %v", err)
	}

	if err := c.CallWidgetAction(ctx, "nonexistent", "light", "turn_on", nil); !errors.Is(err, ErrWidgetNotFound) {
		t.Fatalf("expected ErrWidgetNotFound for nonexistent widget in CallWidgetAction, got %v", err)
	}
}
