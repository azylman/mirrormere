package provider

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/testutil"
)

type haInitTestCase struct {
	name          string
	envURL        string
	envToken      string
	config        map[string]any
	opts          InitOptions
	wantErrSubstr string
	wantBaseURL   string
	wantToken     string
	wantDashboard string
	wantView      string
	wantTitle     string
	wantDomains   []string
	wantEntities  []string
}

func assertHaInitResult(t *testing.T, p *HomeAssistantProvider, tc haInitTestCase) {
	t.Helper()
	if p.baseURL != tc.wantBaseURL {
		t.Errorf("expected baseURL %q, got %q", tc.wantBaseURL, p.baseURL)
	}
	if p.token != tc.wantToken {
		t.Errorf("expected token %q, got %q", tc.wantToken, p.token)
	}
	if p.cfg.Dashboard != tc.wantDashboard {
		t.Errorf("expected dashboard %q, got %q", tc.wantDashboard, p.cfg.Dashboard)
	}
	if p.cfg.View != tc.wantView {
		t.Errorf("expected view %q, got %q", tc.wantView, p.cfg.View)
	}
	if p.cfg.Title != tc.wantTitle {
		t.Errorf("expected title %q, got %q", tc.wantTitle, p.cfg.Title)
	}
	if !reflect.DeepEqual(p.cfg.Domains, tc.wantDomains) {
		t.Errorf("expected domains %v, got %v", tc.wantDomains, p.cfg.Domains)
	}
	if !reflect.DeepEqual(p.cfg.Entities, tc.wantEntities) {
		t.Errorf("expected entities %v, got %v", tc.wantEntities, p.cfg.Entities)
	}
}

func runHaInitCase(ctx context.Context, t *testing.T, tc haInitTestCase) {
	t.Helper()
	if tc.envURL != "" {
		t.Setenv("TEST_HA_URL", tc.envURL)
	}
	if tc.envToken != "" {
		t.Setenv("TEST_HA_TOKEN", tc.envToken)
	}

	p := NewHomeAssistantProvider()
	err := p.Init(ctx, tc.config, tc.opts)
	if tc.wantErrSubstr != "" {
		if err == nil || !strings.Contains(err.Error(), tc.wantErrSubstr) {
			t.Fatalf("expected error containing %q, got %v", tc.wantErrSubstr, err)
		}
		return
	}
	if err != nil {
		t.Fatalf("unexpected Init error: %v", err)
	}
	assertHaInitResult(t, p, tc)
}

func TestHomeAssistant_Init(t *testing.T) {
	ctx := context.Background()

	tests := []haInitTestCase{
		{
			name: "valid configuration via token_env and opts",
			config: map[string]any{
				"url":       "http://198.51.100.10:8123/",
				"token_env": "HA_TOKEN",
				"dashboard": "lovelace-tablet",
				"view":      "living-room",
				"title":     "Main Dashboard",
				"domains":   []any{"light", "switch"},
				"entities":  []any{"light.living_room", "switch.fan"},
			},
			opts: InitOptions{
				Secrets: map[string]string{
					"HA_TOKEN": "secret-token-123",
				},
			},
			wantBaseURL:   "http://198.51.100.10:8123",
			wantToken:     "secret-token-123",
			wantDashboard: "lovelace-tablet",
			wantView:      "living-room",
			wantTitle:     "Main Dashboard",
			wantDomains:   []string{"light", "switch"},
			wantEntities:  []string{"light.living_room", "switch.fan"},
		},
		{
			name: "missing URL error",
			config: map[string]any{
				"token_env": "HA_TOKEN",
			},
			opts: InitOptions{
				Token: "secret-token-123",
			},
			wantErrSubstr: "requires a valid URL",
		},
		{
			name: "missing token error even when plaintext token is present in config",
			config: map[string]any{
				"url":   "http://198.51.100.10:8123",
				"token": "plaintext-token-must-be-ignored",
			},
			wantErrSubstr: "requires a valid access token",
		},
		{
			name: "resolve from InitOptions and secrets",
			config: map[string]any{
				"token_env": "CUSTOM_TOKEN",
				"name":      "Fallback Name Title",
			},
			opts: InitOptions{
				Endpoint: "http://198.51.100.20:8123",
				Token:    "opt-token",
				Secrets: map[string]string{
					"CUSTOM_TOKEN": "custom-secret-token",
				},
			},
			wantBaseURL: "http://198.51.100.20:8123",
			wantToken:   "custom-secret-token",
			wantTitle:   "Fallback Name Title",
		},
		{
			name:     "resolve from OS env vars",
			envURL:   "http://198.51.100.30:8123",
			envToken: "env-secret-token",
			config: map[string]any{
				"url_env":   "TEST_HA_URL",
				"token_env": "TEST_HA_TOKEN",
			},
			wantBaseURL: "http://198.51.100.30:8123",
			wantToken:   "env-secret-token",
		},
		{
			name: "resolve token from ha.token secret",
			config: map[string]any{
				"base_url": "http://198.51.100.40:8123",
			},
			opts: InitOptions{
				Secrets: map[string]string{
					"ha.token": "ha-prefixed-secret",
				},
			},
			wantBaseURL: "http://198.51.100.40:8123",
			wantToken:   "ha-prefixed-secret",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runHaInitCase(ctx, t, tc)
		})
	}
}

type haDiscoveryTestCase struct {
	name             string
	baseURL          string
	inputCfg         *LovelaceConfig
	selector         string
	wantTitle        string
	wantViewPath     string
	wantViewType     string
	wantSectionCount int
	wantCardTypes    []string
	wantEntities     []string
	wantFourthImage  string
}

func assertHaLayoutDetails(t *testing.T, layout *LovelaceLayout, tc haDiscoveryTestCase) {
	t.Helper()
	if layout.Title != tc.wantTitle {
		t.Errorf("expected title %q, got %q", tc.wantTitle, layout.Title)
	}
	if layout.View.Path != tc.wantViewPath {
		t.Errorf("expected view path %q, got %q", tc.wantViewPath, layout.View.Path)
	}
	if tc.wantViewType != "" && layout.View.Type != tc.wantViewType {
		t.Errorf("expected view type %q, got %q", tc.wantViewType, layout.View.Type)
	}
	if tc.wantSectionCount > 0 && len(layout.View.Sections) != tc.wantSectionCount {
		t.Fatalf("expected %d sections, got %d", tc.wantSectionCount, len(layout.View.Sections))
	}
}

func assertHaLayoutCardsAndEntities(t *testing.T, layout *LovelaceLayout, tc haDiscoveryTestCase) {
	t.Helper()
	if len(layout.Cards) != len(tc.wantCardTypes) {
		t.Fatalf("expected %d cards, got %d", len(tc.wantCardTypes), len(layout.Cards))
	}
	for i, wantType := range tc.wantCardTypes {
		if layout.Cards[i].Type != wantType {
			t.Errorf("card %d: expected type %q, got %q", i, wantType, layout.Cards[i].Type)
		}
	}
	if tc.wantFourthImage != "" && len(layout.Cards) >= 4 {
		if layout.Cards[3].Image != tc.wantFourthImage {
			t.Errorf("expected card[3].Image %q, got %q", tc.wantFourthImage, layout.Cards[3].Image)
		}
	}
	for _, exp := range tc.wantEntities {
		if !slices.Contains(layout.DiscoveredEntities, exp) {
			t.Errorf("expected entity %q in discovered entities %v", exp, layout.DiscoveredEntities)
		}
	}
}

func runHaDiscoveryCase(t *testing.T, tc haDiscoveryTestCase) {
	t.Helper()
	p := NewHomeAssistantProviderWithClient(nil, tc.baseURL, "")
	layout := p.DiscoverLayout(tc.inputCfg, tc.selector)
	assertHaLayoutDetails(t, layout, tc)
	assertHaLayoutCardsAndEntities(t, layout, tc)
}

func testLovelaceConfig() LovelaceConfig {
	lovelaceJSON := `{
		"title": "Home Dashboard",
		"views": [
			{
				"title": "Overview",
				"path": "overview",
				"cards": [
					{
						"type": "tile",
						"entity": "light.living_room_ceiling",
						"name": "Ceiling Light",
						"icon": "mdi:ceiling-light"
					},
					{
						"type": "entities",
						"title": "Quick Switches",
						"entities": [
							"switch.coffee_maker",
							{
								"entity": "switch.porch_light",
								"name": "Porch"
							},
							{
								"type": "divider"
							}
						]
					},
					{
						"type": "grid",
						"columns": 2,
						"square": false,
						"cards": [
							{
								"type": "button",
								"entity": "script.good_night",
								"name": "Good Night",
								"tap_action": {"action": "call-service"}
							},
							{
								"type": "thermostat",
								"entity": "climate.hallway_nest",
								"name": "Thermostat"
							}
						]
					},
					{
						"type": "picture-entity",
						"entity": "camera.front_doorbell",
						"image": "/local/camera.jpg",
						"camera_view": "live"
					}
				]
			},
			{
				"title": "Climate & Sections",
				"path": "climate-view",
				"type": "sections",
				"sections": [
					{
						"title": "Main Floor Climate",
						"cards": [
							{
								"type": "thermostat",
								"entity": "climate.living_room_ac"
							},
							{
								"type": "tile",
								"entity": "fan.ceiling_fan"
							}
						]
					},
					{
						"title": "Security",
						"cards": [
							{
								"type": "tile",
								"entity": "lock.front_door"
							},
							{
								"type": "tile",
								"entity": "cover.garage_door"
							}
						]
					}
				]
			}
		]
	}`
	var cfg LovelaceConfig
	if err := json.Unmarshal([]byte(lovelaceJSON), &cfg); err != nil {
		panic(err)
	}
	return cfg
}

func haDiscoveryTestCases(cfg *LovelaceConfig) []haDiscoveryTestCase {
	return []haDiscoveryTestCase{
		{
			name:            "discover first view cards and entities with relative image rewrite",
			baseURL:         "http://198.51.100.10:8123",
			inputCfg:        cfg,
			selector:        "0",
			wantTitle:       "Home Dashboard",
			wantViewPath:    "overview",
			wantCardTypes:   []string{"tile", "entities", "grid", "picture-entity"},
			wantFourthImage: "http://198.51.100.10:8123/local/camera.jpg",
			wantEntities: []string{
				"light.living_room_ceiling",
				"switch.coffee_maker",
				"switch.porch_light",
				"script.good_night",
				"climate.hallway_nest",
				"camera.front_doorbell",
			},
		},
		{
			name:             "discover sections view",
			baseURL:          "http://198.51.100.10:8123",
			inputCfg:         cfg,
			selector:         "climate-view",
			wantTitle:        "Home Dashboard",
			wantViewPath:     "climate-view",
			wantViewType:     "sections",
			wantSectionCount: 2,
			wantCardTypes:    []string{"thermostat", "tile", "tile", "tile"},
			wantEntities: []string{
				"climate.living_room_ac",
				"fan.ceiling_fan",
				"lock.front_door",
				"cover.garage_door",
			},
		},
		{
			name:            "select view by title",
			inputCfg:        cfg,
			selector:        "Overview",
			wantTitle:       "Home Dashboard",
			wantViewPath:    "overview",
			wantCardTypes:   []string{"tile", "entities", "grid", "picture-entity"},
			wantFourthImage: "/local/camera.jpg",
		},
		{
			name:            "fallback to view 0 when selector not found",
			inputCfg:        cfg,
			selector:        "non-existent-view",
			wantTitle:       "Home Dashboard",
			wantViewPath:    "overview",
			wantCardTypes:   []string{"tile", "entities", "grid", "picture-entity"},
			wantFourthImage: "/local/camera.jpg",
		},
		{
			name:      "nil lovelace config returns empty layout",
			inputCfg:  nil,
			selector:  "view1",
			wantTitle: "",
		},
		{
			name:      "empty views slice returns layout without cards",
			inputCfg:  &LovelaceConfig{Title: "Empty Views"},
			selector:  "view1",
			wantTitle: "Empty Views",
		},
	}
}

func TestHomeAssistant_LovelaceLayoutDiscovery(t *testing.T) {
	t.Parallel()

	cfg := testLovelaceConfig()
	tests := haDiscoveryTestCases(&cfg)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runHaDiscoveryCase(t, tc)
		})
	}
}

// newHAServer serves a scripted Home Assistant: the default Lovelace config
// route and the /api/states route.
func newHAServer(t *testing.T, lovelace, states testutil.Route) (*httptest.Server, *testutil.Captured) {
	t.Helper()
	return testutil.RouteServer(t, map[string]testutil.Route{"/api/lovelace/config": lovelace, "/api/states": states})
}

// haJSON marshals v for a scripted Home Assistant response body.
func haJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// haCheckErr fails the test unless err is nil when wantSub is empty, or
// non-nil with text containing wantSub otherwise.
func haCheckErr(t *testing.T, err error, wantSub string) {
	t.Helper()
	got := ""
	if err != nil {
		got = err.Error()
	}
	if (wantSub == "") != (got == "") || !strings.Contains(got, wantSub) {
		t.Fatalf("error = %q, want substring %q", got, wantSub)
	}
}

// haCardSummary renders a card as "type entity image" with the server base
// URL replaced by BASE, so rows can state normalized image URLs as literals.
func haCardSummary(c LovelaceCard, base string) string {
	return strings.TrimSpace(strings.Join([]string{c.Type, c.Entity, strings.Replace(c.Image, base, "BASE", 1)}, " "))
}

func mockHomeAssistantStates() []EntityState {
	return []EntityState{
		{EntityID: "light.kitchen", State: "on", Attributes: map[string]any{"friendly_name": "Kitchen Light", "brightness": 200}},
		{EntityID: "switch.coffee", State: "off", Attributes: map[string]any{"friendly_name": "Coffee Maker"}},
		{EntityID: "fan.living_room", State: "on", Attributes: map[string]any{"percentage": 50}},
		{EntityID: "cover.garage", State: "closed", Attributes: map[string]any{"current_position": 0}},
		{EntityID: "lock.front_door", State: "locked", Attributes: map[string]any{"friendly_name": "Front Door"}},
		{EntityID: "climate.hallway", State: "heat", Attributes: map[string]any{"current_temperature": 68.5, "temperature": 70.0}},
		{EntityID: "media_player.living_room_speaker", State: "playing", Attributes: map[string]any{
			"media_title": "Hey Jude", "volume_level": 0.4, "entity_picture": "https://cdn.example.com/album.jpg",
		}},
		{EntityID: "camera.driveway", State: "idle", Attributes: map[string]any{
			"access_token": "cam-token-xyz", "entity_picture": "/api/camera_proxy/camera.driveway?token=cam-token-xyz",
		}},
		{EntityID: "scene.evening", State: "scening", Attributes: map[string]any{"friendly_name": "Evening Scene"}},
		{EntityID: "script.movie_night", State: "off", Attributes: map[string]any{"friendly_name": "Movie Night"}},
		{EntityID: "sensor.outdoor_temperature", State: "72.4", Attributes: map[string]any{"unit_of_measurement": "°F", "device_class": "temperature"}},
		{EntityID: "binary_sensor.front_motion", State: "on", Attributes: map[string]any{"device_class": "motion"}},
		{EntityID: "automation.motion_lighting", State: "on", Attributes: map[string]any{"friendly_name": "Motion Lighting Rule"}},
		{EntityID: "zone.home", State: "zoning", Attributes: map[string]any{"radius": 100}},
	}
}

func TestHomeAssistant_HydrateStates(t *testing.T) {
	t.Parallel()
	allStates := haJSON(t, mockHomeAssistantStates())
	tests := []struct {
		name         string
		states       testutil.Route
		domains      []any
		canceledCtx  bool
		wantErrSub   string
		wantIDs      []string
		wantPictures map[string]string
	}{
		{
			name:   "all 12 supported domains with root-relative entity_picture rewritten",
			states: testutil.Route{Body: allStates},
			wantIDs: []string{
				"binary_sensor.front_motion", "camera.driveway", "climate.hallway", "cover.garage", "fan.living_room", "light.kitchen",
				"lock.front_door", "media_player.living_room_speaker", "scene.evening", "script.movie_night", "sensor.outdoor_temperature", "switch.coffee",
			},
			wantPictures: map[string]string{
				"camera.driveway":                  "BASE/api/camera_proxy/camera.driveway?token=cam-token-xyz",
				"media_player.living_room_speaker": "https://cdn.example.com/album.jpg",
			},
		},
		{name: "configured domains filter", states: testutil.Route{Body: allStates}, domains: []any{"light", "climate"}, wantIDs: []string{"climate.hallway", "light.kitchen"}},
		{name: "upstream 500", states: testutil.Route{Status: http.StatusInternalServerError, Body: "internal crash"}, wantErrSub: "states request returned status 500: internal crash"},
		{name: "invalid json", states: testutil.Route{Body: "invalid json"}, wantErrSub: "failed to decode states"},
		{name: "canceled context", states: testutil.Route{Body: allStates}, canceledCtx: true, wantErrSub: "states request failed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, captured := newHAServer(t, testutil.Route{}, tc.states)
			p := NewHomeAssistantProviderWithClient(nil, srv.URL, "")
			p.client = nil // exercise the http.DefaultClient fallback
			cfg := map[string]any{"url": srv.URL}
			if tc.domains != nil {
				cfg["domains"] = tc.domains
			}
			if err := p.Init(context.Background(), cfg, InitOptions{Token: "test-ha-token"}); err != nil {
				t.Fatalf("Init: %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.canceledCtx {
				cancel()
			}
			states, err := p.HydrateStates(ctx)
			haCheckErr(t, err, tc.wantErrSub)
			for _, r := range captured.Requests() {
				if got := r.Header.Get("Authorization"); got != "Bearer test-ha-token" {
					t.Errorf("Authorization = %q, want bearer token", got)
				}
			}
			if gotIDs := slices.Sorted(maps.Keys(states)); !slices.Equal(gotIDs, tc.wantIDs) {
				t.Errorf("hydrated IDs = %v, want %v", gotIDs, tc.wantIDs)
			}
			for id, want := range tc.wantPictures {
				pic, _ := states[id].Attributes["entity_picture"].(string)
				if got := strings.Replace(pic, srv.URL, "BASE", 1); got != want {
					t.Errorf("%s entity_picture = %q, want %q", id, got, want)
				}
			}
		})
	}
}

func TestHomeAssistant_Fetch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		config         map[string]any
		lovelace       testutil.Route
		states         testutil.Route
		wantErrSub     string
		wantTitle      string
		wantCards      []string
		wantEntities   []string
		wantDiscovered []string
	}{
		{
			name:           "lovelace layout plus a hydrated non-card entity",
			lovelace:       testutil.Route{Body: `{"title":"My Lovelace Home","views":[{"title":"Living Room","path":"living_room","cards":[{"type":"tile","entity":"light.living_room"},{"type":"thermostat","entity":"climate.hallway"}]}]}`},
			states:         testutil.Route{Body: `[{"entity_id":"light.living_room","state":"on"},{"entity_id":"climate.hallway","state":"cool"},{"entity_id":"switch.tv","state":"off"}]`},
			wantTitle:      "My Lovelace Home",
			wantCards:      []string{"tile light.living_room", "thermostat climate.hallway"},
			wantEntities:   []string{"climate.hallway", "light.living_room", "switch.tv"},
			wantDiscovered: []string{"light.living_room", "climate.hallway"},
		},
		{
			name:           "lovelace 404 generates sorted fallback cards from states",
			lovelace:       testutil.Route{Status: http.StatusNotFound},
			states:         testutil.Route{Body: `[{"entity_id":"climate.thermostat","state":"heat"},{"entity_id":"camera.front_door","state":"idle"},{"entity_id":"light.porch","state":"on"}]`},
			wantTitle:      "Home Assistant",
			wantCards:      []string{"picture-entity camera.front_door", "thermostat climate.thermostat", "tile light.porch"},
			wantEntities:   []string{"camera.front_door", "climate.thermostat", "light.porch"},
			wantDiscovered: []string{"camera.front_door", "climate.thermostat", "light.porch"},
		},
		{
			name:       "states failure fails the fetch",
			lovelace:   testutil.Route{Body: `{"title":"Dash","views":[{"title":"V1","cards":[{"type":"tile","entity":"light.one"}]}]}`},
			states:     testutil.Route{Status: http.StatusInternalServerError, Body: "states error"},
			wantErrSub: "home-assistant hydration failed: states request returned status 500: states error",
		},
		{
			name:           "configured entity joins discovered and section image is normalized",
			config:         map[string]any{"entities": []any{"switch.extra"}},
			lovelace:       testutil.Route{Body: `{"title":"Lovelace Dash","views":[{"title":"V1","sections":[{"title":"S1","cards":[{"type":"tile","entity":"light.one","image":"/local/img.png"}]}]}]}`},
			states:         testutil.Route{Body: `[{"entity_id":"light.one","state":"on"},{"entity_id":"switch.extra","state":"off"}]`},
			wantTitle:      "Lovelace Dash",
			wantCards:      []string{"tile light.one BASE/local/img.png"},
			wantEntities:   []string{"light.one", "switch.extra"},
			wantDiscovered: []string{"light.one", "switch.extra"},
		},
		{name: "untitled empty dashboard defaults the title", lovelace: testutil.Route{Body: `{"views":[{"cards":[]}]}`}, states: testutil.Route{Body: `[]`}, wantTitle: "Home Assistant"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, captured := newHAServer(t, tc.lovelace, tc.states)
			_ = captured
			p := NewHomeAssistantProviderWithClient(srv.Client(), srv.URL, "")
			cfg := map[string]any{"url": srv.URL}
			maps.Copy(cfg, tc.config)
			if err := p.Init(context.Background(), cfg, InitOptions{Token: "token-123"}); err != nil {
				t.Fatalf("Init: %v", err)
			}
			res, err := p.Fetch(context.Background())
			haCheckErr(t, err, tc.wantErrSub)
			if err != nil {
				return
			}
			snap := testutil.As[HomeAssistantSnapshot](t, res)
			cards := make([]string, 0, len(snap.Cards))
			for _, c := range snap.Cards {
				cards = append(cards, haCardSummary(c, srv.URL))
			}
			if snap.Title != tc.wantTitle || !slices.Equal(cards, tc.wantCards) {
				t.Errorf("title, cards = %q, %v; want %q, %v", snap.Title, cards, tc.wantTitle, tc.wantCards)
			}
			if got := slices.Sorted(maps.Keys(snap.Entities)); !slices.Equal(got, tc.wantEntities) {
				t.Errorf("entities = %v, want %v", got, tc.wantEntities)
			}
			if !slices.Equal(snap.DiscoveredEntities, tc.wantDiscovered) {
				t.Errorf("discovered = %v, want %v", snap.DiscoveredEntities, tc.wantDiscovered)
			}
			if _, err := time.Parse(time.RFC3339, snap.LastUpdated); err != nil {
				t.Errorf("LastUpdated %q is not RFC3339: %v", snap.LastUpdated, err)
			}
		})
	}
}

func TestHomeAssistant_IsValidIdentifier(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want bool
	}{
		{"light", true},
		{"switch", true},
		{"turn_on", true},
		{"toggle", true},
		{"binary_sensor", true},
		{"media_player", true},
		{"v1_service", true},
		{"", false},
		{"Light", false},
		{"light-switch", false},
		{"light.turn_on", false},
		{"light/turn_on", false},
		{"../..", false},
		{"light\n", false},
		{"turn_on ", false},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			if got := IsValidIdentifier(tc.in); got != tc.want {
				t.Errorf("IsValidIdentifier(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestLovelace_UnmarshalJSON_Errors(t *testing.T) {
	t.Parallel()
	var v LovelaceView
	if err := v.UnmarshalJSON([]byte("invalid json")); err == nil {
		t.Error("expected error unmarshaling invalid json for LovelaceView")
	}
	var s LovelaceSection
	if err := s.UnmarshalJSON([]byte("invalid json")); err == nil {
		t.Error("expected error unmarshaling invalid json for LovelaceSection")
	}
	var c LovelaceCard
	if err := c.UnmarshalJSON([]byte("invalid json")); err == nil {
		t.Error("expected error unmarshaling invalid json for LovelaceCard")
	}
}

func TestHomeAssistant_ToStringSlice(t *testing.T) {
	t.Parallel()
	if res := toStringSlice(nil); res != nil {
		t.Errorf("expected nil for nil input, got %v", res)
	}
	if res := toStringSlice([]string{"a", "b"}); len(res) != 2 {
		t.Errorf("expected 2 elements for []string, got %v", res)
	}
	if res := toStringSlice([]any{"a", 123, "b", ""}); len(res) != 2 || res[0] != "a" || res[1] != "b" {
		t.Errorf("unexpected result for []any: %v", res)
	}
	if res := toStringSlice("foo, bar, , baz"); len(res) != 3 || res[0] != "foo" || res[1] != "bar" || res[2] != "baz" {
		t.Errorf("unexpected result for csv string: %v", res)
	}
	if res := toStringSlice(12345); res != nil {
		t.Errorf("expected nil for non-string/slice, got %v", res)
	}
}

func TestHomeAssistant_GenerateFallbackCards(t *testing.T) {
	t.Parallel()
	p := NewHomeAssistantProvider()

	explicit := []string{"  ", "climate.living_room", "camera.front_door", "scene.night_mode", "script.notify", "switch.fan"}
	cards := p.generateFallbackCards(nil, explicit)
	if len(cards) != 5 {
		t.Fatalf("expected 5 cards, got %d", len(cards))
	}
	if cards[0].Type != "thermostat" || cards[1].Type != "picture-entity" || cards[2].Type != "button" || cards[3].Type != "button" || cards[4].Type != "tile" {
		t.Errorf("unexpected card types: %+v", cards)
	}

	states := map[string]EntityState{
		"climate.living_room": {EntityID: "climate.living_room"},
		"camera.doorbell":     {EntityID: "camera.doorbell"},
		"scene.bedtime":       {EntityID: "scene.bedtime"},
		"script.cleanup":      {EntityID: "script.cleanup"},
		"light.desk":          {EntityID: "light.desk"},
	}
	cards2 := p.generateFallbackCards(states, nil)
	if len(cards2) != 5 {
		t.Fatalf("expected 5 cards from states, got %d", len(cards2))
	}
}

func TestHomeAssistant_ResolveConfigField(t *testing.T) {
	t.Parallel()
	mockSecrets := map[string]string{
		"secret_key": "val1",
		"ha.url":     "http://203.0.113.10:8123",
		"token":      "val2",
	}
	opts := InitOptions{
		Secrets: mockSecrets,
	}

	if val := resolveToken(map[string]any{}, opts, ""); val != "val2" {
		t.Errorf("expected val2, got %q", val)
	}

	if val := resolveConfigField(map[string]any{}, opts, "url"); val != "http://203.0.113.10:8123" {
		t.Errorf("expected ha.url resolved, got %q", val)
	}

	if val := resolveToken(map[string]any{"token_env": "secret_key"}, opts, ""); val != "val1" {
		t.Errorf("expected val1 from token_env, got %q", val)
	}

	if val := resolveConfigField(map[string]any{}, opts, "unknown"); val != "" {
		t.Errorf("expected empty string, got %q", val)
	}
}

func TestHomeAssistant_UnconfiguredErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	p := NewHomeAssistantProvider()

	if _, err := p.CallServiceWithResponse(ctx, "light", "turn_on", nil); !errors.Is(err, ErrUpstreamUnavailable) {
		t.Errorf("expected ErrUpstreamUnavailable for unconfigured baseURL in CallServiceWithResponse, got %v", err)
	}

	if _, err := p.FetchLovelaceConfig(ctx, ""); err == nil {
		t.Error("expected error for unconfigured baseURL in FetchLovelaceConfig")
	}

	if _, err := p.HydrateStates(ctx); err == nil {
		t.Error("expected error for unconfigured baseURL in HydrateStates")
	}
}

func TestHomeAssistant_LovelaceCustomDashboardFallsBackToDefault(t *testing.T) {
	t.Parallel()
	srv, captured := testutil.RouteServer(t, map[string]testutil.Route{
		"/api/lovelace/config/custom": {Status: http.StatusNotFound},
		"/api/lovelace/config":        {Body: `{"title":"Default Dash"}`},
	})
	p := NewHomeAssistantProviderWithClient(srv.Client(), srv.URL, "tok")
	cfg, err := p.FetchLovelaceConfig(context.Background(), "custom")
	if err != nil || cfg.Title != "Default Dash" {
		t.Fatalf("FetchLovelaceConfig = %+v, %v; want Default Dash fallback", cfg, err)
	}
	reqs := captured.Requests()
	paths := make([]string, 0, len(reqs))
	for _, r := range reqs {
		paths = append(paths, r.Path)
	}
	if want := []string{"/api/lovelace/config/custom", "/api/lovelace/config"}; !slices.Equal(paths, want) {
		t.Errorf("request paths = %v, want %v", paths, want)
	}
}

func TestHomeAssistant_ExtractEntitiesAndSelectView(t *testing.T) {
	t.Parallel()

	if v := selectView(nil, ""); v.Title != "" {
		t.Errorf("expected empty view for nil views")
	}

	views := []LovelaceView{
		{Title: "Home", Path: "home"},
		{Title: "Upstairs", Path: "upstairs"},
	}

	if v := selectView(views, "99"); v.Title != "Home" {
		t.Errorf("expected fallback to Home, got %q", v.Title)
	}
	if v := selectView(views, "-1"); v.Title != "Home" {
		t.Errorf("expected fallback to Home, got %q", v.Title)
	}
	if v := selectView(views, "Unknown"); v.Title != "Home" {
		t.Errorf("expected fallback to Home, got %q", v.Title)
	}
	if v := selectView(views, "Upstairs"); v.Path != "upstairs" {
		t.Errorf("expected Upstairs view by title, got %q", v.Path)
	}

	card := LovelaceCard{
		Entity:      "light.desk",
		CameraImage: "camera.driveway",
		Image:       "/local/desk.png",
		Entities: []any{
			"light.desk",
			map[string]any{"entity": "switch.socket"},
			map[string]any{"entity": "light.desk"},
			12345,
		},
		Cards: []LovelaceCard{
			{
				Entity: "fan.office",
				Image:  "/local/fan.png",
				Sections: []LovelaceSection{
					{
						Cards: []LovelaceCard{
							{Entity: "sensor.office_temp", Image: "/local/temp.png"},
						},
					},
				},
			},
		},
		Sections: []LovelaceSection{
			{
				Cards: []LovelaceCard{
					{Entity: "lock.front_door", Image: "/local/lock.png"},
				},
			},
		},
	}
	ents := ExtractEntitiesFromCard(card)
	expected := []string{"light.desk", "camera.driveway", "switch.socket", "fan.office", "sensor.office_temp", "lock.front_door"}
	if !reflect.DeepEqual(ents, expected) {
		t.Errorf("expected %v, got %v", expected, ents)
	}

	normalized := normalizeCardImage(card, "http://198.51.100.10:8123")
	if normalized.Image != "http://198.51.100.10:8123/local/desk.png" {
		t.Errorf("expected normalized root card image, got %q", normalized.Image)
	}
	if normalized.Cards[0].Image != "http://198.51.100.10:8123/local/fan.png" {
		t.Errorf("expected normalized nested card image, got %q", normalized.Cards[0].Image)
	}
	if normalized.Cards[0].Sections[0].Cards[0].Image != "http://198.51.100.10:8123/local/temp.png" {
		t.Errorf("expected normalized nested section card image, got %q", normalized.Cards[0].Sections[0].Cards[0].Image)
	}
	if normalized.Sections[0].Cards[0].Image != "http://198.51.100.10:8123/local/lock.png" {
		t.Errorf("expected normalized section card image, got %q", normalized.Sections[0].Cards[0].Image)
	}

	deduped := deduplicateStrings([]string{"a", "", "b", "a", "c", "b", " "})
	expectedDeduped := []string{"a", "b", "c"}
	if !reflect.DeepEqual(deduped, expectedDeduped) {
		t.Errorf("expected %v, got %v", expectedDeduped, deduped)
	}
}

func TestHomeAssistant_EntityState_Domain(t *testing.T) {
	t.Parallel()

	cases := []struct {
		entityID string
		want     string
	}{
		{"light.kitchen", "light"},
		{"sensor.temp", "sensor"},
		{"nodomain", "nodomain"},
		{"", ""},
	}
	for _, tc := range cases {
		es := EntityState{EntityID: tc.entityID}
		if got := es.Domain(); got != tc.want {
			t.Errorf("Domain(%q) = %q, want %q", tc.entityID, got, tc.want)
		}
	}
}
