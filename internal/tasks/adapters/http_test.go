package adapters_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/azylman/mirrormere/internal/tasks/adapters"
)

type mockRoute struct {
	status     int
	body       string
	nextStatus int
	nextBody   string
}

func newRouteServer(t *testing.T, routes map[string]mockRoute, authOut *string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	m := make(map[string]mockRoute, len(routes))
	for k, v := range routes {
		m[k] = v
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if authOut != nil {
			*authOut = r.Header.Get("Authorization")
		}
		route, ok := m[r.URL.RequestURI()]
		if !ok {
			route, ok = m[r.URL.Path]
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		status := route.status
		if status == 0 {
			status = http.StatusOK
		}
		body := route.body
		if route.nextStatus != 0 {
			route.status, route.body = route.nextStatus, route.nextBody
			route.nextStatus, route.nextBody = 0, ""
			m[r.URL.RequestURI()], m[r.URL.Path] = route, route
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return s
}

func TestNewHTTPAdapter_Validation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		url     string
		token   string
		wantErr string
	}{
		{"empty_url", "", "", "base_url is required"},
		{"unsupported_scheme", "ftp://example.com/tasks", "", "must be http:// or https://"},
		{"invalid_url_no_host", "http://", "", "must be http:// or https:// with host"},
		{"valid_config", "https://example.com/api/lists/family-todo", "secret-token", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ad, err := adapters.NewHTTPAdapter(adapters.HTTPAdapterConfig{BaseURL: tc.url, Token: tc.token})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ad.Name() != "http" {
				t.Errorf("expected Name() to be 'http', got %q", ad.Name())
			}
		})
	}
}

func TestHTTPAdapter_FetchList(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		baseSuffix string
		routes     map[string]mockRoute
		closeSrv   bool
		wantID     string
		wantItems  int
		wantErr    string
		wantErrIs  error
	}{
		{"combined_endpoint", "/api/todo", map[string]mockRoute{"/api/todo": {body: `{"updated_at":"2026-09-27T10:00:00Z","items":[{"title":"Dishes"},{"title":"Sweep"}]}`}}, false, "todo", 2, "", nil},
		{"split_endpoint", "/api/todo", map[string]mockRoute{"/api/todo": {body: `{"id":"todo-2","name":"Chores"}`}, "/api/todo/items": {body: `[{"id":"i2","title":"Sweep"}]`}}, false, "todo-2", 1, "", nil},
		{"split_endpoint_with_query", "/api/todo?acc=1", map[string]mockRoute{"/api/todo?acc=1": {body: `{"id":"todo-q"}`}, "/api/todo/items?acc=1": {body: `[{"id":"i3"}]`}}, false, "todo-q", 1, "", nil},
		{"direct_items_array", "/groceries", map[string]mockRoute{"/groceries": {body: `[{"id":"i4","title":"Milk"}]`}}, false, "groceries", 1, "", nil},
		{"derive_id_items_suffix", "/api/v1/groceries/items", map[string]mockRoute{"/api/v1/groceries/items": {body: `[{"id":"i5"}]`}}, false, "groceries", 1, "", nil},
		{"derive_id_root_path", "/", map[string]mockRoute{"/": {body: `[{"id":"i6"}]`}}, false, "http-list", 1, "", nil},
		{"error_401", "", map[string]mockRoute{"/": {status: 401}}, false, "", 0, "", adapters.ErrUnauthorized},
		{"error_404", "", map[string]mockRoute{"/": {status: 404}}, false, "", 0, "", adapters.ErrUpstreamNotFound},
		{"error_500", "", map[string]mockRoute{"/": {status: 500}}, false, "", 0, "status 500", nil},
		{"error_payload_too_large", "", map[string]mockRoute{"/": {body: strings.Repeat("x", adapters.MaxPayloadBytes+10)}}, false, "", 0, "", adapters.ErrPayloadTooLarge},
		{"error_malformed_base_json", "", map[string]mockRoute{"/": {body: `{bad-json`}}, false, "", 0, "unexpected json response structure", nil},
		{"error_secondary_items_500", "/list", map[string]mockRoute{"/list": {body: `{"id":"l1"}`}, "/list/items": {status: 500}}, false, "", 0, "failed to fetch list items", nil},
		{"error_secondary_items_bad_json", "/list", map[string]mockRoute{"/list": {body: `{"id":"l2"}`}, "/list/items": {body: `{bad`}}, false, "", 0, "failed to parse items response", nil},
		{"error_closed_server", "", nil, true, "", 0, "http request failed", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var authHeader string
			srv := newRouteServer(t, tc.routes, &authHeader)
			if tc.closeSrv {
				srv.Close()
			}
			ad, err := adapters.NewHTTPAdapter(adapters.HTTPAdapterConfig{
				BaseURL: srv.URL + tc.baseSuffix, Token: "test-tok", Client: srv.Client(),
			})
			if err != nil {
				t.Fatalf("failed to create adapter: %v", err)
			}
			list, items, err := ad.FetchList(context.Background())
			if tc.wantErrIs != nil {
				if !errors.Is(err, tc.wantErrIs) {
					t.Fatalf("expected error %v, got %v", tc.wantErrIs, err)
				}
				return
			}
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if list.ID != tc.wantID {
				t.Errorf("list ID = %q, want %q", list.ID, tc.wantID)
			}
			if len(items) != tc.wantItems {
				t.Errorf("items count = %d, want %d", len(items), tc.wantItems)
			}
			if authHeader != "Bearer test-tok" {
				t.Errorf("auth header = %q, want Bearer test-tok", authHeader)
			}
		})
	}
}
