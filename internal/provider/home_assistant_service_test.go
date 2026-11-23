package provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/azylman/mirrormere/internal/testutil"
)

// newHAServiceServer scripts Home Assistant service endpoints under
// /api/services/light/<service>.
func newHAServiceServer(t *testing.T) (*httptest.Server, *testutil.Captured) {
	t.Helper()
	return testutil.RouteServer(t, map[string]testutil.Route{
		"/api/services/light/turn_on":            {Body: `[{"entity_id":"light.living_room","state":"on","attributes":{"entity_picture":"/api/camera_proxy/light.living_room"}}]`},
		"/api/services/light/not_found":          {Status: http.StatusNotFound, Body: "Service not found"},
		"/api/services/light/unauthorized":       {Status: http.StatusUnauthorized, Body: "Unauthorized"},
		"/api/services/light/status_error":       {Status: http.StatusInternalServerError, Body: "server boom"},
		"/api/services/light/empty_status_error": {Status: http.StatusBadGateway},
		"/api/services/light/empty_unauth":       {Status: http.StatusForbidden},
		"/api/services/light/empty_notfound":     {Status: http.StatusNotFound},
		"/api/services/light/invalid_json":       {Body: "not-json"},
		"/api/services/light/empty_resp":         {Body: ""},
	})
}

func TestHomeAssistant_CallServiceWithResponse(t *testing.T) {
	t.Parallel()
	upstreamNotFound := []error{ErrUpstreamStatus, ErrUpstreamNotFound}
	upstreamUnauthorized := []error{ErrUpstreamStatus, ErrUpstreamUnauthorized}
	tests := []struct {
		name        string
		domain      string
		service     string
		serviceData map[string]any
		canceledCtx bool
		wantErrIs   []error
		wantErrSub  string
		wantStates  []string
		wantReqs    []string
	}{
		{
			name: "success posts data and normalizes entity_picture", domain: "light", service: "turn_on",
			serviceData: map[string]any{"entity_id": "light.living_room", "brightness": 255},
			wantStates:  []string{"light.living_room on BASE/api/camera_proxy/light.living_room"},
			wantReqs:    []string{`POST /api/services/light/turn_on Bearer tok {"brightness":255,"entity_id":"light.living_room"}`},
		},
		{
			name: "nil data posts an empty object", domain: " light ", service: "turn_on",
			wantStates: []string{"light.living_room on BASE/api/camera_proxy/light.living_room"},
			wantReqs:   []string{"POST /api/services/light/turn_on Bearer tok {}"},
		},
		{name: "empty domain", domain: "", service: "turn_on", wantErrIs: []error{ErrActionValidation}, wantErrSub: "domain and service must not be empty"},
		{name: "empty service", domain: "light", service: "", wantErrIs: []error{ErrActionValidation}, wantErrSub: "domain and service must not be empty"},
		{name: "path traversal domain", domain: "../../api", service: "turn_on", wantErrIs: []error{ErrActionValidation}, wantErrSub: "invalid domain"},
		{name: "uppercase domain", domain: "LIGHT", service: "turn_on", wantErrIs: []error{ErrActionValidation}, wantErrSub: "invalid domain"},
		{name: "unsupported domain", domain: "automation", service: "trigger", wantErrIs: []error{ErrActionValidation}, wantErrSub: "unsupported domain"},
		{name: "path traversal service", domain: "light", service: "turn_on/../admin", wantErrIs: []error{ErrActionValidation}, wantErrSub: "invalid service"},
		{
			name: "unmarshalable data", domain: "light", service: "turn_on", serviceData: map[string]any{"bad": make(chan int)},
			wantErrIs: []error{ErrActionValidation}, wantErrSub: "failed to marshal service data",
		},
		{
			name: "upstream 404 with body", domain: "light", service: "not_found", wantErrIs: upstreamNotFound,
			wantErrSub: "service call returned status 404: Service not found", wantReqs: []string{"POST /api/services/light/not_found Bearer tok {}"},
		},
		{
			name: "upstream 401 with body", domain: "light", service: "unauthorized", wantErrIs: upstreamUnauthorized,
			wantErrSub: "service call returned status 401: Unauthorized", wantReqs: []string{"POST /api/services/light/unauthorized Bearer tok {}"},
		},
		{
			name: "upstream 500 with body", domain: "light", service: "status_error", wantErrIs: []error{ErrUpstreamStatus},
			wantErrSub: "service call returned status 500: server boom", wantReqs: []string{"POST /api/services/light/status_error Bearer tok {}"},
		},
		{
			name: "upstream 502 without body", domain: "light", service: "empty_status_error", wantErrIs: []error{ErrUpstreamStatus},
			wantErrSub: "service call returned status 502", wantReqs: []string{"POST /api/services/light/empty_status_error Bearer tok {}"},
		},
		{
			name: "upstream 403 without body", domain: "light", service: "empty_unauth", wantErrIs: upstreamUnauthorized,
			wantErrSub: "service call returned status 403", wantReqs: []string{"POST /api/services/light/empty_unauth Bearer tok {}"},
		},
		{
			name: "upstream 404 without body", domain: "light", service: "empty_notfound", wantErrIs: upstreamNotFound,
			wantErrSub: "service call returned status 404", wantReqs: []string{"POST /api/services/light/empty_notfound Bearer tok {}"},
		},
		{name: "invalid json response yields no states", domain: "light", service: "invalid_json", wantReqs: []string{"POST /api/services/light/invalid_json Bearer tok {}"}},
		{name: "empty response yields no states", domain: "light", service: "empty_resp", wantReqs: []string{"POST /api/services/light/empty_resp Bearer tok {}"}},
		{
			name: "canceled context", domain: "light", service: "turn_on", canceledCtx: true,
			wantErrIs: []error{ErrUpstreamUnavailable}, wantErrSub: "service call failed",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, captured := newHAServiceServer(t)
			p := NewHomeAssistantProviderWithClient(nil, srv.URL, "tok")
			p.client = nil // exercise the http.DefaultClient fallback
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.canceledCtx {
				cancel()
			}
			states, err := p.CallServiceWithResponse(ctx, tc.domain, tc.service, tc.serviceData)
			haCheckErr(t, err, tc.wantErrSub)
			for _, target := range tc.wantErrIs {
				if !errors.Is(err, target) {
					t.Errorf("error %v does not wrap %v", err, target)
				}
			}
			gotStates := make([]string, 0, len(states))
			for _, s := range states {
				pic, _ := s.Attributes["entity_picture"].(string)
				gotStates = append(gotStates, s.EntityID+" "+s.State+" "+strings.Replace(pic, srv.URL, "BASE", 1))
			}
			if !slices.Equal(gotStates, tc.wantStates) {
				t.Errorf("states = %v, want %v", gotStates, tc.wantStates)
			}
			reqs := captured.Requests()
			gotReqs := make([]string, 0, len(reqs))
			for _, r := range reqs {
				gotReqs = append(gotReqs, strings.Join([]string{r.Method, r.Path, r.Header.Get("Authorization"), r.Body}, " "))
			}
			if !slices.Equal(gotReqs, tc.wantReqs) {
				t.Errorf("requests = %q, want %q", gotReqs, tc.wantReqs)
			}
		})
	}
}

func TestHomeAssistant_FetchLovelaceConfig_ErrorsAndFallbacks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	srv, captured := testutil.RouteServer(t, map[string]testutil.Route{
		"/api/lovelace/config":              {Status: http.StatusInternalServerError, Body: "internal crash"},
		"/api/lovelace/config/invalid_json": {Body: "{invalid"},
	})
	_ = captured

	p := NewHomeAssistantProviderWithClient(srv.Client(), srv.URL, "tok")
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

func TestCoordinator_CallWidgetAction_RoutingAndRefresh(t *testing.T) {
	t.Parallel()

	ts, captured := newHAServiceServer(t)
	_ = captured

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

	_, err = c.CallWidgetActionWithResponse(ctx, "ha-widget", "light", "status_error", nil)
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
