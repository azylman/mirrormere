package provider_test

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azylman/mirrormere/internal/provider"
	"github.com/azylman/mirrormere/internal/testutil"
)

func TestFloorplan3d_DemoMode(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		cfg  map[string]any
	}{
		{name: "nil_config", cfg: nil},
		{name: "layout_url_only", cfg: map[string]any{"title": "Studio Demo", "layout_url": "/layouts/demo.json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := provider.NewFloorplan3dProvider()
			if err := p.Init(ctx, tc.cfg, provider.InitOptions{}); err != nil {
				t.Fatalf("Init(%v) failed: %v", tc.cfg, err)
			}
			data, err := p.Fetch(ctx)
			if err != nil {
				t.Fatalf("Fetch() failed: %v", err)
			}
			got := testutil.As[map[string]any](t, data)
			if len(got) != 0 {
				t.Fatalf("expected empty map in demo mode, got %v", got)
			}
			if err := p.Subscribe(ctx, make(chan provider.WidgetPayload, 1)); err != nil {
				t.Fatalf("Subscribe failed: %v", err)
			}
			if err := p.Shutdown(ctx); err != nil {
				t.Fatalf("Shutdown failed: %v", err)
			}
		})
	}
}

func TestFloorplan3d_HydratesHAEntities(t *testing.T) {
	t.Setenv("TEST_FLOORPLAN_HA_TOKEN", "floorplan-test-token")

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/states" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]provider.EntityState{
			{EntityID: "light.studio_main", State: "on", Attributes: map[string]any{"brightness": float64(200)}},
			{EntityID: "vacuum.main_floor", State: "docked", Attributes: map[string]any{"battery_level": float64(100)}},
			{EntityID: "automation.ignored_rule", State: "on"},
		})
	}))
	defer srv.Close()

	cfg := map[string]any{
		"url":       srv.URL,
		"token_env": "TEST_FLOORPLAN_HA_TOKEN",
		"haBindings": []any{
			map[string]any{"id": "studio-light", "entityId": "light.studio_main"},
		},
		"layout": map[string]any{
			"haBindings": []any{
				map[string]any{"id": "main-vac", "entityId": "vacuum.main_floor"},
			},
		},
	}

	p := provider.NewFloorplan3dProviderWithClient(srv.Client())
	ctx := context.Background()
	if err := p.Init(ctx, cfg, provider.InitOptions{}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	if _, mutated := cfg["domains"]; mutated {
		t.Fatalf("Init mutated caller config by adding domains key")
	}

	raw, err := p.Fetch(ctx)
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}
	if gotAuth != "Bearer floorplan-test-token" {
		t.Fatalf("expected Bearer auth header, got %q", gotAuth)
	}

	payload := testutil.As[map[string]any](t, raw)
	entities := testutil.As[map[string]provider.EntityState](t, payload["entities"])
	if entities["light.studio_main"].State != "on" {
		t.Fatalf("expected light.studio_main state 'on', got %+v", entities["light.studio_main"])
	}
	if entities["vacuum.main_floor"].State != "docked" {
		t.Fatalf("expected custom haBindings domain vacuum.main_floor state 'docked', got %+v", entities["vacuum.main_floor"])
	}
	if _, exists := entities["automation.ignored_rule"]; exists {
		t.Fatalf("unexpected unbound unsupported domain entity automation.ignored_rule in result")
	}
}

func TestFloorplan3d_ExplicitDomainsAndBindingVariants(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]provider.EntityState{
			{EntityID: "light.room_a", State: "off"},
			{EntityID: "vacuum.main_floor", State: "cleaning"},
			{EntityID: "automation.ignored_rule", State: "on"},
		})
	}))
	defer srv.Close()

	ctx := context.Background()
	opts := provider.InitOptions{Secrets: map[string]string{"TEST_FP_TOKEN": "sec-token"}}

	// Explicit domains filter should be respected rather than overwritten.
	pExplicit := provider.NewFloorplan3dProviderWithClient(srv.Client())
	if err := pExplicit.Init(ctx, map[string]any{
		"base_url":  srv.URL,
		"token_env": "TEST_FP_TOKEN",
		"domains":   []string{"vacuum"},
	}, opts); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	raw, err := pExplicit.Fetch(ctx)
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}
	entities := testutil.As[map[string]provider.EntityState](t, testutil.As[map[string]any](t, raw)["entities"])
	if len(entities) != 1 || entities["vacuum.main_floor"].State != "cleaning" {
		t.Fatalf("expected only vacuum entity with explicit domains, got %v", entities)
	}

	// Typed []map[string]any haBindings with entity_id fallback should also work.
	pMapSlice := provider.NewFloorplan3dProviderWithClient(srv.Client())
	if initErr := pMapSlice.Init(ctx, map[string]any{
		"url":       srv.URL,
		"token_env": "TEST_FP_TOKEN",
		"haBindings": []map[string]any{
			{"id": "vac", "entity_id": "vacuum.main_floor"},
		},
	}, opts); initErr != nil {
		t.Fatalf("Init failed: %v", initErr)
	}
	raw2, err := pMapSlice.Fetch(ctx)
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}
	entities2 := testutil.As[map[string]provider.EntityState](t, testutil.As[map[string]any](t, raw2)["entities"])
	if entities2["vacuum.main_floor"].State != "cleaning" || entities2["light.room_a"].State != "off" {
		t.Fatalf("unexpected entities from []map[string]any bindings: %v", entities2)
	}
}

func TestFloorplan3d_JSONStringAndMapKeyedBindings(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]provider.EntityState{
			{EntityID: "light.room_a", State: "on"},
			{EntityID: "vacuum.main_floor", State: "docked"},
			{EntityID: "automation.ignored_rule", State: "on"},
		})
	}))
	t.Cleanup(srv.Close)

	ctx := context.Background()
	opts := provider.InitOptions{Secrets: map[string]string{"TEST_FP_TOKEN": "sec-token"}}

	cases := []struct {
		name string
		cfg  map[string]any
	}{
		{
			name: "json_string_layout_with_vacuum_binding",
			cfg: map[string]any{
				"url":       srv.URL,
				"token_env": "TEST_FP_TOKEN",
				"layout":    `{"title":"Studio","haBindings":[{"id":"vac","entityId":"vacuum.main_floor"}]}`,
			},
		},
		{
			name: "json_string_haBindings",
			cfg: map[string]any{
				"url":        srv.URL,
				"token_env":  "TEST_FP_TOKEN",
				"haBindings": `[{"id":"vac","entity_id":"vacuum.main_floor"}]`,
			},
		},
		{
			name: "map_keyed_haBindings_with_entityId_and_key_fallback",
			cfg: map[string]any{
				"url":       srv.URL,
				"token_env": "TEST_FP_TOKEN",
				"haBindings": map[string]any{
					"vacuum.main_floor": map[string]any{"label": "Floor Vacuum"},
					"studio_light":      map[string]any{"entityId": "light.room_a"},
					"invalid_key":       "not-a-map",
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := provider.NewFloorplan3dProviderWithClient(srv.Client())
			if err := p.Init(ctx, tc.cfg, opts); err != nil {
				t.Fatalf("Init failed: %v", err)
			}
			raw, err := p.Fetch(ctx)
			if err != nil {
				t.Fatalf("Fetch failed: %v", err)
			}
			entities := testutil.As[map[string]provider.EntityState](t, testutil.As[map[string]any](t, raw)["entities"])
			if entities["vacuum.main_floor"].State != "docked" {
				t.Fatalf("expected vacuum.main_floor to be hydrated, got %v", entities)
			}
			if entities["light.room_a"].State != "on" {
				t.Fatalf("expected light.room_a to be hydrated, got %v", entities)
			}
			if _, exists := entities["automation.ignored_rule"]; exists {
				t.Fatalf("unexpected unbound automation.ignored_rule in hydrated entities")
			}
		})
	}
}

func newFloorplanStatesTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/states" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer layout-url-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]provider.EntityState{
			{EntityID: "light.room_a", State: "on"},
			{EntityID: "vacuum.main_floor", State: "cleaning", Attributes: map[string]any{"entity_picture": "/local/vacuum.png"}},
			{EntityID: "automation.evening_routine", State: "on"},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestFloorplan3d_LayoutURLHydratesAllDomains(t *testing.T) {
	t.Parallel()

	srv := newFloorplanStatesTestServer(t)
	ctx := context.Background()
	opts := provider.InitOptions{Secrets: map[string]string{"TEST_FP_TOKEN": "layout-url-token"}}

	for _, tc := range []struct {
		name   string
		urlKey string
		extra  map[string]any
	}{
		{name: "layout_url", urlKey: "layout_url"},
		{name: "layoutUrl_empty_domains", urlKey: "layoutUrl", extra: map[string]any{"domains": []string{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := map[string]any{
				"url":       srv.URL,
				"token_env": "TEST_FP_TOKEN",
				tc.urlKey:   "/layouts/home.json",
			}
			maps.Copy(cfg, tc.extra)

			p := provider.NewFloorplan3dProviderWithClient(srv.Client())
			if err := p.Init(ctx, cfg, opts); err != nil {
				t.Fatalf("Init failed: %v", err)
			}
			raw, err := p.Fetch(ctx)
			if err != nil {
				t.Fatalf("Fetch failed: %v", err)
			}
			entities := testutil.As[map[string]provider.EntityState](t, testutil.As[map[string]any](t, raw)["entities"])
			if len(entities) != 3 {
				t.Fatalf("expected all 3 entities hydrated for %s, got %v", tc.urlKey, entities)
			}
			vac := entities["vacuum.main_floor"]
			if vac.State != "cleaning" {
				t.Fatalf("expected vacuum.main_floor state 'cleaning', got %+v", vac)
			}
			if gotPic, _ := vac.Attributes["entity_picture"].(string); gotPic != srv.URL+"/local/vacuum.png" {
				t.Fatalf("expected normalized entity_picture %q, got %q", srv.URL+"/local/vacuum.png", gotPic)
			}
			if entities["automation.evening_routine"].State != "on" {
				t.Fatalf("expected automation.evening_routine to be hydrated, got %v", entities)
			}
		})
	}
}

func TestFloorplan3d_ExplicitDomainsPreservedWithLayouts(t *testing.T) {
	t.Parallel()

	srv := newFloorplanStatesTestServer(t)
	ctx := context.Background()
	opts := provider.InitOptions{Secrets: map[string]string{"TEST_FP_TOKEN": "layout-url-token"}}

	explicitCases := []struct {
		name string
		cfg  map[string]any
		want string
	}{
		{
			name: "layout_url_with_explicit_domains",
			cfg: map[string]any{
				"url":        srv.URL,
				"token_env":  "TEST_FP_TOKEN",
				"layout_url": "/layouts/home.json",
				"domains":    []string{"vacuum"},
			},
			want: "vacuum.main_floor",
		},
		{
			name: "json_layout_with_explicit_domains",
			cfg: map[string]any{
				"url":       srv.URL,
				"token_env": "TEST_FP_TOKEN",
				"layout":    `{"haBindings":[{"entityId":"vacuum.main_floor"}]}`,
				"domains":   []string{"light"},
			},
			want: "light.room_a",
		},
	}
	for _, tc := range explicitCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := provider.NewFloorplan3dProviderWithClient(srv.Client())
			if err := p.Init(ctx, tc.cfg, opts); err != nil {
				t.Fatalf("Init failed: %v", err)
			}
			raw, err := p.Fetch(ctx)
			if err != nil {
				t.Fatalf("Fetch failed: %v", err)
			}
			entities := testutil.As[map[string]provider.EntityState](t, testutil.As[map[string]any](t, raw)["entities"])
			if len(entities) != 1 {
				t.Fatalf("expected exactly 1 filtered entity for %s, got %v", tc.name, entities)
			}
			if _, ok := entities[tc.want]; !ok {
				t.Fatalf("expected %q in filtered entities, got %v", tc.want, entities)
			}
		})
	}
}

func TestFloorplan3d_PartialAndErrorCases(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	cases := []struct {
		name          string
		cfg           map[string]any
		opts          provider.InitOptions
		wantErrSubstr string
	}{
		{
			name:          "url_without_token",
			cfg:           map[string]any{"url": "http://198.51.100.10:8123"},
			wantErrSubstr: "access token",
		},
		{
			name:          "token_without_url",
			cfg:           map[string]any{"token_env": "TEST_FP_TOK"},
			opts:          provider.InitOptions{Secrets: map[string]string{"TEST_FP_TOK": "secret"}},
			wantErrSubstr: "valid URL",
		},
		{
			name:          "endpoint_without_token",
			cfg:           nil,
			opts:          provider.InitOptions{Endpoint: "http://198.51.100.10:8123"},
			wantErrSubstr: "access token",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := provider.NewFloorplan3dProvider()
			err := p.Init(ctx, tc.cfg, tc.opts)
			if err == nil || !strings.Contains(err.Error(), tc.wantErrSubstr) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErrSubstr, err)
			}
		})
	}

	// Upstream HTTP error during Fetch should propagate (both standard and layout_url modes).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "upstream down", http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)

	opts := provider.InitOptions{Secrets: map[string]string{"TEST_FP_TOKEN": "tok"}}

	p := provider.NewFloorplan3dProviderWithClient(srv.Client())
	if err := p.Init(ctx, map[string]any{
		"url":       srv.URL,
		"token_env": "TEST_FP_TOKEN",
	}, opts); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	if _, err := p.Fetch(ctx); err == nil {
		t.Fatal("expected Fetch error on HTTP 502")
	}

	// Layout URL mode should also propagate HTTP 502 and malformed JSON errors.
	pLayout := provider.NewFloorplan3dProviderWithClient(srv.Client())
	if err := pLayout.Init(ctx, map[string]any{
		"url":        srv.URL,
		"token_env":  "TEST_FP_TOKEN",
		"layout_url": "/layouts/demo.json",
	}, opts); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	if _, err := pLayout.Fetch(ctx); err == nil {
		t.Fatal("expected Fetch error on HTTP 502 in layout_url mode")
	}

	badJSONSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{not-json"))
	}))
	t.Cleanup(badJSONSrv.Close)

	pBadJSON := provider.NewFloorplan3dProviderWithClient(badJSONSrv.Client())
	if err := pBadJSON.Init(ctx, map[string]any{
		"url":        badJSONSrv.URL,
		"token_env":  "TEST_FP_TOKEN",
		"layout_url": "/layouts/demo.json",
	}, opts); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	if _, err := pBadJSON.Fetch(ctx); err == nil {
		t.Fatal("expected Fetch error on malformed JSON in layout_url mode")
	}
}
