package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/azylman/mirrormere/internal/provider"
	"github.com/azylman/mirrormere/internal/server"
	"github.com/azylman/mirrormere/internal/testutil"
)

// actionCall is one call a fake WidgetActionCaller received.
type actionCall struct {
	WidgetID, Domain, Service string
	ServiceData               map[string]any
}

// recordingActionCaller is a WidgetActionCaller fake that reports the calls it received.
type recordingActionCaller interface {
	server.WidgetActionCaller
	calls() []actionCall
}

// actionCaller is a plain WidgetActionCaller fake returning err.
type actionCaller struct {
	err      error
	mu       sync.Mutex
	recorded []actionCall
}

func (c *actionCaller) record(widgetID, domain, service string, serviceData map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.recorded = append(c.recorded, actionCall{widgetID, domain, service, serviceData})
}

func (c *actionCaller) calls() []actionCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]actionCall(nil), c.recorded...)
}

func (c *actionCaller) CallWidgetAction(_ context.Context, widgetID, domain, service string, serviceData map[string]any) error {
	c.record(widgetID, domain, service, serviceData)
	return c.err
}

// responseActionCaller also implements WidgetActionResponseCaller, returning resp and err.
type responseActionCaller struct {
	actionCaller

	resp any
}

func (c *responseActionCaller) CallWidgetActionWithResponse(_ context.Context, widgetID, domain, service string, serviceData map[string]any) (any, error) {
	c.record(widgetID, domain, service, serviceData)
	return c.resp, c.err
}

// decodeActionResponse decodes a 200 action response body; other statuses yield nil.
func decodeActionResponse(t *testing.T, rec *httptest.ResponseRecorder) *server.WidgetActionResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		return nil
	}
	var resp server.WidgetActionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	return &resp
}

// newWidgetActionServer mounts a DefaultWidgetActionHandler using caller (none when nil), or no
// handler at all when noHandler is set.
func newWidgetActionServer(caller recordingActionCaller, noHandler bool) *server.Server {
	if noHandler {
		return server.New(server.Config{})
	}
	var opts []server.WidgetActionOption
	if caller != nil {
		opts = append(opts, server.WithWidgetActionCaller(caller))
	}
	return server.New(server.Config{WidgetActionHandler: server.NewDefaultWidgetActionHandler(opts...)})
}

// widgetActionCalls returns the calls caller received; nil when there is no caller.
func widgetActionCalls(caller recordingActionCaller) []actionCall {
	if caller == nil {
		return nil
	}
	return caller.calls()
}

func TestWidgetAction_PostWidgetAction(t *testing.T) {
	t.Parallel()
	const turnOn = `{"domain":"light","service":"turn_on"}`
	const kitchenOn = `{"domain":"light","service":"turn_on","entity_id":"light.kitchen"}`
	cors := map[string]string{"Access-Control-Allow-Origin": "*", "Access-Control-Allow-Methods": "POST, OPTIONS"}
	notAllowed := map[string]string{"Allow": "POST, OPTIONS", "Access-Control-Allow-Origin": "*"}
	kitchenCall := []actionCall{{"ha-lights", "light", "turn_on", map[string]any{"entity_id": "light.kitchen"}}}
	tests := []struct {
		name        string
		method      string // "" means POST
		widgetID    string // url.PathEscape-d into the route
		body        string
		caller      recordingActionCaller // nil: handler with neither caller nor direct proxy
		noHandler   bool                  // server built without any WidgetActionHandler
		wantStatus  int
		wantHeaders map[string]string
		wantBodySub string
		wantResp    *server.WidgetActionResponse
		wantCalls   []actionCall
	}{
		{name: "no_handler_mounted", widgetID: "light-1", body: turnOn, noHandler: true, wantStatus: http.StatusNotFound},
		{name: "get_not_allowed", method: http.MethodGet, widgetID: "light-1", caller: &actionCaller{}, wantStatus: http.StatusMethodNotAllowed, wantHeaders: notAllowed},
		{name: "put_not_allowed", method: http.MethodPut, widgetID: "light-1", caller: &actionCaller{}, wantStatus: http.StatusMethodNotAllowed, wantHeaders: notAllowed},
		{name: "delete_not_allowed", method: http.MethodDelete, widgetID: "light-1", caller: &actionCaller{}, wantStatus: http.StatusMethodNotAllowed, wantHeaders: notAllowed},
		{name: "patch_not_allowed", method: http.MethodPatch, widgetID: "light-1", caller: &actionCaller{}, wantStatus: http.StatusMethodNotAllowed, wantHeaders: notAllowed},
		{name: "options_preflight", method: http.MethodOptions, widgetID: "light-1", caller: &actionCaller{}, wantStatus: http.StatusNoContent, wantHeaders: cors},
		{name: "blank_widget_id", widgetID: "  ", body: turnOn, caller: &actionCaller{}, wantStatus: http.StatusBadRequest, wantBodySub: "invalid widget_id"},
		{name: "dotdot_widget_id", widgetID: "../escape", body: turnOn, caller: &actionCaller{}, wantStatus: http.StatusBadRequest, wantBodySub: "invalid widget_id"},
		{name: "slash_widget_id", widgetID: "nested/slash", body: turnOn, caller: &actionCaller{}, wantStatus: http.StatusBadRequest, wantBodySub: "invalid widget_id"},
		{name: "malformed_json", widgetID: "ha-1", body: `{invalid-json`, caller: &actionCaller{}, wantStatus: http.StatusBadRequest, wantBodySub: "invalid request payload"},
		{name: "empty_body", widgetID: "ha-1", body: ``, caller: &actionCaller{}, wantStatus: http.StatusBadRequest, wantBodySub: "invalid request payload"},
		{name: "json_array_body", widgetID: "ha-1", body: `["not", "an", "object"]`, caller: &actionCaller{}, wantStatus: http.StatusBadRequest, wantBodySub: "invalid request payload"},
		{name: "empty_object", widgetID: "ha-1", body: `{}`, caller: &actionCaller{}, wantStatus: http.StatusBadRequest, wantBodySub: "domain and service are required"},
		{name: "missing_service", widgetID: "ha-1", body: `{"domain":"light"}`, caller: &actionCaller{}, wantStatus: http.StatusBadRequest, wantBodySub: "domain and service are required"},
		{name: "missing_domain", widgetID: "ha-1", body: `{"service":"turn_on"}`, caller: &actionCaller{}, wantStatus: http.StatusBadRequest, wantBodySub: "domain and service are required"},
		{name: "empty_domain_and_service", widgetID: "ha-1", body: `{"domain":"","service":""}`, caller: &actionCaller{}, wantStatus: http.StatusBadRequest, wantBodySub: "domain and service are required"},
		{name: "domain_dotdot", widgetID: "w-test", body: `{"domain":"../..","service":"turn_on"}`, caller: &actionCaller{}, wantStatus: http.StatusBadRequest, wantBodySub: "invalid domain"},
		{name: "domain_dotdot_api", widgetID: "w-test", body: `{"domain":"../../api","service":"turn_on"}`, caller: &actionCaller{}, wantStatus: http.StatusBadRequest, wantBodySub: "invalid domain"},
		{name: "domain_parent_light", widgetID: "w-test", body: `{"domain":"../light","service":"turn_on"}`, caller: &actionCaller{}, wantStatus: http.StatusBadRequest, wantBodySub: "invalid domain"},
		{name: "domain_slash", widgetID: "w-test", body: `{"domain":"light/admin","service":"turn_on"}`, caller: &actionCaller{}, wantStatus: http.StatusBadRequest, wantBodySub: "invalid domain"},
		{name: "domain_abs_path", widgetID: "w-test", body: `{"domain":"/etc/passwd","service":"turn_on"}`, caller: &actionCaller{}, wantStatus: http.StatusBadRequest, wantBodySub: "invalid domain"},
		{name: "domain_double_dot", widgetID: "w-test", body: `{"domain":"light..turn_on","service":"turn_on"}`, caller: &actionCaller{}, wantStatus: http.StatusBadRequest, wantBodySub: "invalid domain"},
		{name: "domain_semicolon", widgetID: "w-test", body: `{"domain":"light;drop","service":"turn_on"}`, caller: &actionCaller{}, wantStatus: http.StatusBadRequest, wantBodySub: "invalid domain"},
		{name: "domain_space", widgetID: "w-test", body: `{"domain":"light turn_on","service":"turn_on"}`, caller: &actionCaller{}, wantStatus: http.StatusBadRequest, wantBodySub: "invalid domain"},
		{name: "domain_query", widgetID: "w-test", body: `{"domain":"light?admin=true","service":"turn_on"}`, caller: &actionCaller{}, wantStatus: http.StatusBadRequest, wantBodySub: "invalid domain"},
		{name: "domain_uppercase", widgetID: "w-test", body: `{"domain":"LIGHT","service":"turn_on"}`, caller: &actionCaller{}, wantStatus: http.StatusBadRequest, wantBodySub: "invalid domain"},
		{name: "unsupported_automation", widgetID: "w-test", body: `{"domain":"automation","service":"turn_on"}`, caller: &actionCaller{}, wantStatus: http.StatusBadRequest, wantBodySub: "unsupported domain"},
		{name: "unsupported_zone", widgetID: "w-test", body: `{"domain":"zone","service":"turn_on"}`, caller: &actionCaller{}, wantStatus: http.StatusBadRequest, wantBodySub: "unsupported domain"},
		{name: "unsupported_shell_command", widgetID: "w-test", body: `{"domain":"shell_command","service":"turn_on"}`, caller: &actionCaller{}, wantStatus: http.StatusBadRequest, wantBodySub: "unsupported domain"},
		{name: "unsupported_hassio", widgetID: "w-test", body: `{"domain":"hassio","service":"turn_on"}`, caller: &actionCaller{}, wantStatus: http.StatusBadRequest, wantBodySub: "unsupported domain"},
		{name: "unsupported_input_boolean", widgetID: "w-test", body: `{"domain":"input_boolean","service":"turn_on"}`, caller: &actionCaller{}, wantStatus: http.StatusBadRequest, wantBodySub: "unsupported domain"},
		{name: "unsupported_input_number", widgetID: "w-test", body: `{"domain":"input_number","service":"turn_on"}`, caller: &actionCaller{}, wantStatus: http.StatusBadRequest, wantBodySub: "unsupported domain"},
		{name: "service_dotdot", widgetID: "w-test", body: `{"domain":"light","service":"../.."}`, caller: &actionCaller{}, wantStatus: http.StatusBadRequest, wantBodySub: "invalid service"},
		{name: "service_traversal", widgetID: "w-test", body: `{"domain":"light","service":"turn_on/../admin"}`, caller: &actionCaller{}, wantStatus: http.StatusBadRequest, wantBodySub: "invalid service"},
		{name: "service_api_config", widgetID: "w-test", body: `{"domain":"light","service":"api/config"}`, caller: &actionCaller{}, wantStatus: http.StatusBadRequest, wantBodySub: "invalid service"},
		{name: "service_api_template", widgetID: "w-test", body: `{"domain":"light","service":"../../../api/template"}`, caller: &actionCaller{}, wantStatus: http.StatusBadRequest, wantBodySub: "invalid service"},
		{name: "service_query", widgetID: "w-test", body: `{"domain":"light","service":"turn_on?evil=1"}`, caller: &actionCaller{}, wantStatus: http.StatusBadRequest, wantBodySub: "invalid service"},
		{name: "service_fragment", widgetID: "w-test", body: `{"domain":"light","service":"turn_on#frag"}`, caller: &actionCaller{}, wantStatus: http.StatusBadRequest, wantBodySub: "invalid service"},
		{name: "service_space", widgetID: "w-test", body: `{"domain":"light","service":"turn on"}`, caller: &actionCaller{}, wantStatus: http.StatusBadRequest, wantBodySub: "invalid service"},
		{name: "service_dash", widgetID: "w-test", body: `{"domain":"light","service":"turn-on"}`, caller: &actionCaller{}, wantStatus: http.StatusBadRequest, wantBodySub: "invalid service"},
		{name: "service_uppercase", widgetID: "w-test", body: `{"domain":"light","service":"TURN_ON"}`, caller: &actionCaller{}, wantStatus: http.StatusBadRequest, wantBodySub: "invalid service"},
		{name: "service_trailing_dotdot", widgetID: "w-test", body: `{"domain":"light","service":"turn_on/.."}`, caller: &actionCaller{}, wantStatus: http.StatusBadRequest, wantBodySub: "invalid service"},
		{
			name: "caller_success", widgetID: "living-room-ha", caller: &actionCaller{}, wantStatus: http.StatusOK,
			body:      `{"domain":"light","service":"turn_on","service_data":{"entity_id":"light.kitchen","brightness":200}}`,
			wantResp:  &server.WidgetActionResponse{Status: "ok", WidgetID: "living-room-ha"},
			wantCalls: []actionCall{{"living-room-ha", "light", "turn_on", map[string]any{"entity_id": "light.kitchen", "brightness": float64(200)}}},
		},
		{
			name: "dot_service_format", widgetID: "lights-widget", body: `{"service":"light.toggle","entity_id":"light.porch"}`, caller: &actionCaller{},
			wantStatus: http.StatusOK, wantResp: &server.WidgetActionResponse{Status: "ok", WidgetID: "lights-widget"},
			wantCalls: []actionCall{{"lights-widget", "light", "toggle", map[string]any{"entity_id": "light.porch"}}},
		},
		{
			name: "slash_service_format", widgetID: "switch-widget", body: `{"service":"switch/turn_off","data":{"entity_id":"switch.bedroom_fan"}}`, caller: &actionCaller{},
			wantStatus: http.StatusOK, wantResp: &server.WidgetActionResponse{Status: "ok", WidgetID: "switch-widget"},
			wantCalls: []actionCall{{"switch-widget", "switch", "turn_off", map[string]any{"entity_id": "switch.bedroom_fan"}}},
		},
		{
			name: "action_field_flattens_target", widgetID: "climate-widget", caller: &actionCaller{}, wantStatus: http.StatusOK,
			body:      `{"action":"climate.set_temperature","target":{"entity_id":"climate.living_room"},"data":{"temperature":71}}`,
			wantResp:  &server.WidgetActionResponse{Status: "ok", WidgetID: "climate-widget"},
			wantCalls: []actionCall{{"climate-widget", "climate", "set_temperature", map[string]any{"entity_id": "climate.living_room", "temperature": float64(71)}}},
		},
		{
			name: "response_caller_returns_data", widgetID: "ha-lights", body: kitchenOn, wantStatus: http.StatusOK,
			caller:    &responseActionCaller{resp: []map[string]any{{"entity_id": "light.kitchen", "state": "on", "brightness": 255}}},
			wantResp:  &server.WidgetActionResponse{Status: "ok", WidgetID: "ha-lights", Data: []any{map[string]any{"entity_id": "light.kitchen", "state": "on", "brightness": float64(255)}}},
			wantCalls: kitchenCall,
		},
		{
			name: "response_caller_error_redundant_prefix", widgetID: "ha-lights", body: `{"domain":"light","service":"light.turn_on"}`,
			caller: &responseActionCaller{err: provider.ErrWidgetNotFound}, wantStatus: http.StatusNotFound,
			wantCalls: []actionCall{{"ha-lights", "light", "turn_on", map[string]any{}}},
		},
		{name: "err_sentinel_widget_not_found", widgetID: "ha-lights", body: kitchenOn, caller: &actionCaller{err: fmt.Errorf("%w: widget-99", provider.ErrWidgetNotFound)}, wantStatus: http.StatusNotFound, wantCalls: kitchenCall},
		{name: "err_sentinel_upstream_not_found", widgetID: "ha-lights", body: kitchenOn, caller: &actionCaller{err: fmt.Errorf("%w: light.missing", provider.ErrUpstreamNotFound)}, wantStatus: http.StatusNotFound, wantCalls: kitchenCall},
		{name: "err_sentinel_unauthorized", widgetID: "ha-lights", body: kitchenOn, caller: &actionCaller{err: fmt.Errorf("%w: bad token", provider.ErrUpstreamUnauthorized)}, wantStatus: http.StatusUnauthorized, wantCalls: kitchenCall},
		{name: "err_sentinel_validation", widgetID: "ha-lights", body: kitchenOn, caller: &actionCaller{err: fmt.Errorf("%w: bad domain", provider.ErrActionValidation)}, wantStatus: http.StatusBadRequest, wantCalls: kitchenCall},
		{name: "err_sentinel_unavailable", widgetID: "ha-lights", body: kitchenOn, caller: &actionCaller{err: fmt.Errorf("%w: dial tcp", provider.ErrUpstreamUnavailable)}, wantStatus: http.StatusBadGateway, wantCalls: kitchenCall},
		{name: "err_sentinel_upstream_status", widgetID: "ha-lights", body: kitchenOn, caller: &actionCaller{err: fmt.Errorf("%w: 500", provider.ErrUpstreamStatus)}, wantStatus: http.StatusBadGateway, wantCalls: kitchenCall},
		{name: "err_text_not_found", widgetID: "ha-lights", body: kitchenOn, caller: &actionCaller{err: errors.New("widget not found: widget-99")}, wantStatus: http.StatusNotFound, wantCalls: kitchenCall},
		{name: "err_text_unauthorized", widgetID: "ha-lights", body: kitchenOn, caller: &actionCaller{err: errors.New("upstream service unauthorized")}, wantStatus: http.StatusUnauthorized, wantCalls: kitchenCall},
		{name: "err_text_invalid", widgetID: "ha-lights", body: kitchenOn, caller: &actionCaller{err: errors.New("invalid entity_id specified")}, wantStatus: http.StatusBadRequest, wantCalls: kitchenCall},
		{name: "err_text_refused", widgetID: "ha-lights", body: kitchenOn, caller: &actionCaller{err: errors.New("upstream server connection refused")}, wantStatus: http.StatusBadGateway, wantCalls: kitchenCall},
		{name: "err_text_generic", widgetID: "ha-lights", body: kitchenOn, caller: &actionCaller{err: errors.New("something unexpected went wrong")}, wantStatus: http.StatusInternalServerError, wantBodySub: "something unexpected went wrong", wantCalls: kitchenCall},
		{name: "not_configured", widgetID: "ha-lights", body: kitchenOn, wantStatus: http.StatusInternalServerError, wantBodySub: "widget action caller not configured"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			method := tc.method
			if method == "" {
				method = http.MethodPost
			}
			req := httptest.NewRequest(method, "/api/widgets/"+url.PathEscape(tc.widgetID)+"/action", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			newWidgetActionServer(tc.caller, tc.noHandler).Routes().ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			for k, want := range tc.wantHeaders {
				if got := rec.Header().Get(k); got != want {
					t.Errorf("header %s = %q, want %q", k, got, want)
				}
			}
			if !strings.Contains(rec.Body.String(), tc.wantBodySub) {
				t.Errorf("body = %q, want substring %q", rec.Body.String(), tc.wantBodySub)
			}
			if got := decodeActionResponse(t, rec); !reflect.DeepEqual(got, tc.wantResp) {
				t.Errorf("response = %+v, want %+v", got, tc.wantResp)
			}
			if gotCalls := widgetActionCalls(tc.caller); !reflect.DeepEqual(gotCalls, tc.wantCalls) {
				t.Errorf("calls = %+v, want %+v", gotCalls, tc.wantCalls)
			}
		})
	}
}

func TestHAAction_DirectProxy(t *testing.T) {
	t.Parallel()
	const haToken = "ha-token-must-not-leak"
	const toggle = `{"domain":"light","service":"toggle","entity_id":"light.one"}`
	tests := []struct {
		name         string
		body         string
		upstream     testutil.Route
		upstreamDown bool // upstream is a started-then-closed listener
		wantStatus   int
		wantUpstream []string // "METHOD path body" per upstream request
		wantBodySub  string
		wantResp     *server.WidgetActionResponse
	}{
		{
			name: "success_forwards_service_data", body: `{"domain":"light","service":"turn_on","service_data":{"entity_id":"light.couch"}}`,
			upstream: testutil.Route{Body: `[{"entity_id":"light.couch","state":"on"}]`}, wantStatus: http.StatusOK,
			wantUpstream: []string{`POST /api/services/light/turn_on {"entity_id":"light.couch"}`},
			wantResp:     &server.WidgetActionResponse{Status: "ok", WidgetID: "couch-widget", Data: []any{map[string]any{"entity_id": "light.couch", "state": "on"}}},
		},
		{
			name: "nil_service_data_sends_empty_object", body: `{"domain":"light","service":"turn_on"}`, wantStatus: http.StatusOK,
			wantUpstream: []string{`POST /api/services/light/turn_on {}`}, wantResp: &server.WidgetActionResponse{Status: "ok", WidgetID: "couch-widget"},
		},
		{
			name: "non_json_upstream_body_passed_as_string", body: toggle, upstream: testutil.Route{Body: "OK", ContentType: "text/plain"}, wantStatus: http.StatusOK,
			wantUpstream: []string{`POST /api/services/light/toggle {"entity_id":"light.one"}`}, wantResp: &server.WidgetActionResponse{Status: "ok", WidgetID: "couch-widget", Data: "OK"},
		},
		{name: "upstream_unauthorized", body: toggle, upstream: testutil.Route{Status: http.StatusUnauthorized, Body: "401: Unauthorized"}, wantStatus: http.StatusUnauthorized, wantUpstream: []string{`POST /api/services/light/toggle {"entity_id":"light.one"}`}, wantBodySub: "upstream unauthorized"},
		{name: "upstream_not_found", body: toggle, upstream: testutil.Route{Status: http.StatusNotFound, Body: "Service not found"}, wantStatus: http.StatusNotFound, wantUpstream: []string{`POST /api/services/light/toggle {"entity_id":"light.one"}`}, wantBodySub: "upstream service or entity not found"},
		{name: "upstream_bad_request", body: toggle, upstream: testutil.Route{Status: http.StatusBadRequest, Body: "Invalid entity"}, wantStatus: http.StatusBadRequest, wantUpstream: []string{`POST /api/services/light/toggle {"entity_id":"light.one"}`}, wantBodySub: "Invalid entity"},
		{name: "upstream_server_error", body: toggle, upstream: testutil.Route{Status: http.StatusInternalServerError, Body: "Internal failure"}, wantStatus: http.StatusBadGateway, wantUpstream: []string{`POST /api/services/light/toggle {"entity_id":"light.one"}`}, wantBodySub: "upstream returned status 500: Internal failure"},
		{name: "connection_refused", body: toggle, upstreamDown: true, wantStatus: http.StatusBadGateway, wantBodySub: "upstream service call failed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			upstream, captured := testutil.RouteServer(t, map[string]testutil.Route{"/api/services/light/turn_on": tc.upstream, "/api/services/light/toggle": tc.upstream})
			if tc.upstreamDown {
				upstream.Close()
			}
			handler := server.NewDefaultWidgetActionHandler(
				server.WithHAActionClient(upstream.Client(), upstream.URL, haToken),
				server.WithWidgetActionLogger(slog.New(slog.DiscardHandler)),
			)
			req := httptest.NewRequest(http.MethodPost, "/api/widgets/couch-widget/action", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			server.New(server.Config{WidgetActionHandler: handler}).Routes().ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), haToken) {
				t.Fatalf("token leaked in client response: %s", rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantBodySub) {
				t.Errorf("body = %q, want substring %q", rec.Body.String(), tc.wantBodySub)
			}
			if got := decodeActionResponse(t, rec); !reflect.DeepEqual(got, tc.wantResp) {
				t.Errorf("response = %+v, want %+v", got, tc.wantResp)
			}
			var gotUpstream []string
			for _, r := range captured.Requests() {
				if auth := r.Header.Get("Authorization"); auth != "Bearer "+haToken {
					t.Errorf("Authorization = %q, want bearer token", auth)
				}
				gotUpstream = append(gotUpstream, r.Method+" "+r.Path+" "+strings.TrimSpace(r.Body))
			}
			if !reflect.DeepEqual(gotUpstream, tc.wantUpstream) {
				t.Errorf("upstream requests = %q, want %q", gotUpstream, tc.wantUpstream)
			}
		})
	}
}
