package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestHomeAssistant_ProviderInterface(t *testing.T) {
	t.Parallel()
	var _ Provider = NewHomeAssistantProvider()
}

func TestHomeAssistant_SupportedDomains(t *testing.T) {
	t.Parallel()
	domains := SupportedDomains
	expected := []string{
		"light",
		"switch",
		"fan",
		"cover",
		"lock",
		"climate",
		"media_player",
		"camera",
		"scene",
		"script",
		"sensor",
		"binary_sensor",
	}

	if len(domains) != len(expected) {
		t.Fatalf("expected %d domains, got %d", len(expected), len(domains))
	}

	for _, d := range expected {
		if !IsSupportedDomain(d) {
			t.Errorf("expected domain %q to be supported", d)
		}
	}

	if IsSupportedDomain("automation") {
		t.Error("automation domain should not be in supported list")
	}
	if IsSupportedDomain("zone") {
		t.Error("zone domain should not be in supported list")
	}
}

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
	_ = json.Unmarshal([]byte(lovelaceJSON), &cfg)
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

type haHydrateTestCase struct {
	name            string
	domains         []any
	wantCount       int
	wantPresent     []string
	wantAbsent      []string
	wantDrivewayPic string
	wantMediaPic    string
}

func assertHaHydrateStates(t *testing.T, states map[string]EntityState, tc haHydrateTestCase) {
	t.Helper()
	if len(states) != tc.wantCount {
		t.Fatalf("expected %d hydrated states, got %d", tc.wantCount, len(states))
	}
	for _, entityID := range tc.wantPresent {
		if _, ok := states[entityID]; !ok {
			t.Errorf("expected entity %q to be hydrated", entityID)
		}
	}
	for _, entityID := range tc.wantAbsent {
		if _, ok := states[entityID]; ok {
			t.Errorf("expected entity %q to be filtered out", entityID)
		}
	}
}

func assertHaHydratePictures(t *testing.T, states map[string]EntityState, tc haHydrateTestCase) {
	t.Helper()
	if tc.wantDrivewayPic != "" {
		gotPic, _ := states["camera.driveway"].Attributes["entity_picture"].(string)
		if gotPic != tc.wantDrivewayPic {
			t.Errorf("expected camera entity_picture %q, got %q", tc.wantDrivewayPic, gotPic)
		}
	}
	if tc.wantMediaPic != "" {
		gotMediaPic, _ := states["media_player.living_room_speaker"].Attributes["entity_picture"].(string)
		if gotMediaPic != tc.wantMediaPic {
			t.Errorf("expected media_player entity_picture %q, got %q", tc.wantMediaPic, gotMediaPic)
		}
	}
}

func runHaHydrateCase(t *testing.T, server *httptest.Server, tc haHydrateTestCase) {
	t.Helper()
	ctx := context.Background()
	p := NewHomeAssistantProviderWithClient(server.Client(), server.URL, "test-ha-token")
	cfgMap := map[string]any{
		"url": server.URL,
	}
	if tc.domains != nil {
		cfgMap["domains"] = tc.domains
	}
	if err := p.Init(ctx, cfgMap, InitOptions{Token: "test-ha-token"}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	states, err := p.HydrateStates(ctx)
	if err != nil {
		t.Fatalf("HydrateStates failed: %v", err)
	}
	assertHaHydrateStates(t, states, tc)
	assertHaHydratePictures(t, states, tc)
}

func mockHomeAssistantStates() []EntityState {
	return []EntityState{
		{
			EntityID: "light.kitchen",
			State:    "on",
			Attributes: map[string]any{
				"friendly_name": "Kitchen Light",
				"brightness":    200,
			},
		},
		{
			EntityID: "switch.coffee",
			State:    "off",
			Attributes: map[string]any{
				"friendly_name": "Coffee Maker",
			},
		},
		{
			EntityID: "fan.living_room",
			State:    "on",
			Attributes: map[string]any{
				"percentage": 50,
			},
		},
		{
			EntityID: "cover.garage",
			State:    "closed",
			Attributes: map[string]any{
				"current_position": 0,
			},
		},
		{
			EntityID: "lock.front_door",
			State:    "locked",
			Attributes: map[string]any{
				"friendly_name": "Front Door",
			},
		},
		{
			EntityID: "climate.hallway",
			State:    "heat",
			Attributes: map[string]any{
				"current_temperature": 68.5,
				"temperature":         70.0,
			},
		},
		{
			EntityID: "media_player.living_room_speaker",
			State:    "playing",
			Attributes: map[string]any{
				"media_title":    "Hey Jude",
				"volume_level":   0.4,
				"entity_picture": "https://cdn.example.com/album.jpg",
			},
		},
		{
			EntityID: "camera.driveway",
			State:    "idle",
			Attributes: map[string]any{
				"access_token":   "cam-token-xyz",
				"entity_picture": "/api/camera_proxy/camera.driveway?token=cam-token-xyz",
			},
		},
		{
			EntityID: "scene.evening",
			State:    "scening",
			Attributes: map[string]any{
				"friendly_name": "Evening Scene",
			},
		},
		{
			EntityID: "script.movie_night",
			State:    "off",
			Attributes: map[string]any{
				"friendly_name": "Movie Night",
			},
		},
		{
			EntityID: "sensor.outdoor_temperature",
			State:    "72.4",
			Attributes: map[string]any{
				"unit_of_measurement": "°F",
				"device_class":        "temperature",
			},
		},
		{
			EntityID: "binary_sensor.front_motion",
			State:    "on",
			Attributes: map[string]any{
				"device_class": "motion",
			},
		},
		{
			EntityID: "automation.motion_lighting",
			State:    "on",
			Attributes: map[string]any{
				"friendly_name": "Motion Lighting Rule",
			},
		},
		{
			EntityID: "zone.home",
			State:    "zoning",
			Attributes: map[string]any{
				"radius": 100,
			},
		},
	}
}

func setupHomeAssistantHydrateServer(t *testing.T) *httptest.Server {
	t.Helper()
	mockStates := mockHomeAssistantStates()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/states" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-ha-token" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mockStates)
	}))
	t.Cleanup(server.Close)
	return server
}

func haHydrateTestCases(serverURL string) []haHydrateTestCase {
	return []haHydrateTestCase{
		{
			name:      "hydrates all 12 supported domains and rewrites root-relative entity_picture",
			wantCount: 12,
			wantPresent: []string{
				"light.kitchen",
				"switch.coffee",
				"fan.living_room",
				"cover.garage",
				"lock.front_door",
				"climate.hallway",
				"media_player.living_room_speaker",
				"camera.driveway",
				"scene.evening",
				"script.movie_night",
				"sensor.outdoor_temperature",
				"binary_sensor.front_motion",
			},
			wantAbsent:      []string{"automation.motion_lighting", "zone.home"},
			wantDrivewayPic: serverURL + "/api/camera_proxy/camera.driveway?token=cam-token-xyz",
			wantMediaPic:    "https://cdn.example.com/album.jpg",
		},
		{
			name:        "filters by configured domains",
			domains:     []any{"light", "climate"},
			wantCount:   2,
			wantPresent: []string{"light.kitchen", "climate.hallway"},
			wantAbsent:  []string{"switch.coffee", "camera.driveway"},
		},
	}
}

func TestHomeAssistant_HydrateStates(t *testing.T) {
	t.Parallel()

	server := setupHomeAssistantHydrateServer(t)
	tests := haHydrateTestCases(server.URL)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runHaHydrateCase(t, server, tc)
		})
	}
}

func TestHomeAssistant_Fetch(t *testing.T) {
	t.Parallel()

	lovelaceData := LovelaceConfig{
		Title: "My Lovelace Home",
		Views: []LovelaceView{
			{
				Title: "Living Room",
				Path:  "living_room",
				Cards: []LovelaceCard{
					{
						Type:   "tile",
						Entity: "light.living_room",
					},
					{
						Type:   "thermostat",
						Entity: "climate.hallway",
					},
				},
			},
		},
	}

	statesData := []EntityState{
		{
			EntityID: "light.living_room",
			State:    "on",
			Attributes: map[string]any{
				"friendly_name": "Living Room Light",
			},
		},
		{
			EntityID: "climate.hallway",
			State:    "cool",
			Attributes: map[string]any{
				"current_temperature": 71.0,
			},
		},
		{
			EntityID: "switch.tv",
			State:    "off",
			Attributes: map[string]any{
				"friendly_name": "TV Power",
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/lovelace/config":
			_ = json.NewEncoder(w).Encode(lovelaceData)
		case "/api/states":
			_ = json.NewEncoder(w).Encode(statesData)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	ctx := context.Background()
	p := NewHomeAssistantProviderWithClient(server.Client(), server.URL, "token-123")
	err := p.Init(ctx, map[string]any{
		"url": server.URL,
	}, InitOptions{Token: "token-123"})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	res, err := p.Fetch(ctx)
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}

	snapshot, ok := res.(HomeAssistantSnapshot)
	if !ok {
		t.Fatalf("expected HomeAssistantSnapshot, got %T", res)
	}

	if snapshot.Title != "My Lovelace Home" {
		t.Errorf("expected title 'My Lovelace Home', got %q", snapshot.Title)
	}
	if len(snapshot.Cards) != 2 {
		t.Fatalf("expected 2 discovered cards, got %d", len(snapshot.Cards))
	}
	if len(snapshot.Entities) != 3 {
		t.Fatalf("expected 3 hydrated entities, got %d", len(snapshot.Entities))
	}
	if len(snapshot.DiscoveredEntities) != 2 {
		t.Fatalf("expected 2 discovered entities, got %d", len(snapshot.DiscoveredEntities))
	}
	if snapshot.LastUpdated == "" {
		t.Error("expected non-empty LastUpdated timestamp")
	}
}

func TestHomeAssistant_Fetch_FallbackWhenLovelace404(t *testing.T) {
	t.Parallel()

	statesData := []EntityState{
		{
			EntityID: "climate.thermostat",
			State:    "heat",
			Attributes: map[string]any{
				"current_temperature": 69,
			},
		},
		{
			EntityID: "camera.front_door",
			State:    "idle",
		},
		{
			EntityID: "light.porch",
			State:    "on",
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/lovelace/config":
			http.NotFound(w, r)
		case "/api/states":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(statesData)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	ctx := context.Background()
	p := NewHomeAssistantProviderWithClient(server.Client(), server.URL, "token-123")
	err := p.Init(ctx, map[string]any{
		"url": server.URL,
	}, InitOptions{Token: "token-123"})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	res, err := p.Fetch(ctx)
	if err != nil {
		t.Fatalf("Fetch should succeed even when Lovelace 404s: %v", err)
	}

	snapshot, ok := res.(HomeAssistantSnapshot)
	if !ok {
		t.Fatalf("expected HomeAssistantSnapshot, got %T", res)
	}
	if len(snapshot.Cards) != 3 {
		t.Fatalf("expected 3 generated fallback cards, got %d", len(snapshot.Cards))
	}
	if len(snapshot.Entities) != 3 {
		t.Fatalf("expected 3 hydrated entities, got %d", len(snapshot.Entities))
	}

	typeMap := make(map[string]string)
	for _, c := range snapshot.Cards {
		typeMap[c.Entity] = c.Type
	}

	if typeMap["climate.thermostat"] != "thermostat" {
		t.Errorf("expected climate fallback card to be thermostat, got %q", typeMap["climate.thermostat"])
	}
	if typeMap["camera.front_door"] != "picture-entity" {
		t.Errorf("expected camera fallback card to be picture-entity, got %q", typeMap["camera.front_door"])
	}
	if typeMap["light.porch"] != "tile" {
		t.Errorf("expected light fallback card to be tile, got %q", typeMap["light.porch"])
	}
}

func TestHomeAssistant_Registry(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	p, err := reg.Create("home-assistant")
	if err != nil {
		t.Fatalf("expected home-assistant to be registered in NewRegistry(): %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil provider instance")
	}
	if _, ok := p.(*HomeAssistantProvider); !ok {
		t.Fatalf("expected *HomeAssistantProvider, got %T", p)
	}
}

func TestHomeAssistant_IsValidIdentifier(t *testing.T) {
	t.Parallel()
	valid := []string{"light", "switch", "turn_on", "toggle", "binary_sensor", "media_player", "v1_service"}
	for _, s := range valid {
		if !IsValidIdentifier(s) {
			t.Errorf("expected IsValidIdentifier(%q) to be true", s)
		}
	}

	invalid := []string{"", "Light", "light-switch", "light.turn_on", "light/turn_on", "../..", "light\n", "turn_on "}
	for _, s := range invalid {
		if IsValidIdentifier(s) {
			t.Errorf("expected IsValidIdentifier(%q) to be false", s)
		}
	}
}

func TestHomeAssistant_SubscribeAndShutdown(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p := NewHomeAssistantProvider()

	if err := p.Subscribe(ctx, nil); err != nil {
		t.Errorf("expected nil from Subscribe, got %v", err)
	}

	if err := p.Shutdown(ctx); err != nil {
		t.Errorf("expected nil from Shutdown, got %v", err)
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

func TestHomeAssistant_LovelaceCustomDashboardAndFallbacks(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/lovelace/config/custom":
			w.WriteHeader(http.StatusNotFound)
		case "/api/lovelace/config":
			_ = json.NewEncoder(w).Encode(LovelaceConfig{
				Title: "Default Dash",
			})
		case "/api/states":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`invalid json`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	ctx := context.Background()
	p := NewHomeAssistantProviderWithClient(server.Client(), server.URL, "tok")

	cfg, err := p.FetchLovelaceConfig(ctx, "custom")
	if err != nil {
		t.Fatalf("expected fallback to succeed, got %v", err)
	}
	if cfg.Title != "Default Dash" {
		t.Errorf("expected title 'Default Dash', got %q", cfg.Title)
	}

	if _, err := p.HydrateStates(ctx); err == nil {
		t.Error("expected decode error on invalid json states")
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

func TestHomeAssistant_Fetch_HydrationErrorAndEntityMerging(t *testing.T) {
	t.Parallel()

	serverErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/api/lovelace/config") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"title":"Dash","views":[{"title":"V1","cards":[{"type":"tile","entity":"light.one"}]}]}`))
			return
		}
		if strings.Contains(r.URL.Path, "/api/states") {
			http.Error(w, "states error", http.StatusInternalServerError)
			return
		}
		http.NotFound(w, r)
	}))
	defer serverErr.Close()

	ctx := context.Background()
	pErr := NewHomeAssistantProviderWithClient(serverErr.Client(), serverErr.URL, "tok")
	_ = pErr.Init(ctx, map[string]any{"url": serverErr.URL}, InitOptions{Token: "tok"})

	_, err := pErr.Fetch(ctx)
	if err == nil || !strings.Contains(err.Error(), "home-assistant hydration failed") {
		t.Fatalf("expected hydration failed error, got: %v", err)
	}

	serverOK := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/api/lovelace/config") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"title":"Lovelace Dash","views":[{"title":"V1","sections":[{"title":"S1","cards":[{"type":"tile","entity":"light.one","image":"/local/img.png"}]}]}]}`))
			return
		}
		if strings.Contains(r.URL.Path, "/api/states") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"entity_id":"light.one","state":"on"},{"entity_id":"switch.extra","state":"off"}]`))
			return
		}
		http.NotFound(w, r)
	}))
	defer serverOK.Close()

	pOK := NewHomeAssistantProviderWithClient(serverOK.Client(), serverOK.URL, "tok")
	_ = pOK.Init(ctx, map[string]any{
		"url":      serverOK.URL,
		"entities": []any{"switch.extra"},
	}, InitOptions{Token: "tok"})

	snapRaw, err := pOK.Fetch(ctx)
	if err != nil {
		t.Fatalf("unexpected Fetch error: %v", err)
	}
	snap, ok := snapRaw.(HomeAssistantSnapshot)
	if !ok {
		t.Fatalf("unexpected snapshot type: %T", snapRaw)
	}
	if snap.Title != "Lovelace Dash" {
		t.Errorf("expected Title 'Lovelace Dash', got %q", snap.Title)
	}
	if !slices.Contains(snap.DiscoveredEntities, "switch.extra") {
		t.Errorf("expected DiscoveredEntities to contain 'switch.extra', got %v", snap.DiscoveredEntities)
	}
	if len(snap.Cards) != 1 || snap.Cards[0].Image != serverOK.URL+"/local/img.png" {
		t.Errorf("expected section card image normalized, got %+v", snap.Cards)
	}

	serverNoTitle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/api/lovelace/config") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"views":[{"cards":[]}]}`))
			return
		}
		if strings.Contains(r.URL.Path, "/api/states") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
			return
		}
		http.NotFound(w, r)
	}))
	defer serverNoTitle.Close()

	pNoTitle := NewHomeAssistantProviderWithClient(serverNoTitle.Client(), serverNoTitle.URL, "tok")
	_ = pNoTitle.Init(ctx, map[string]any{"url": serverNoTitle.URL}, InitOptions{Token: "tok"})
	snapNoTitleRaw, err := pNoTitle.Fetch(ctx)
	if err != nil {
		t.Fatalf("unexpected Fetch error: %v", err)
	}
	snapNoTitle, ok := snapNoTitleRaw.(HomeAssistantSnapshot)
	if !ok {
		t.Fatalf("unexpected snapshot type: %T", snapNoTitleRaw)
	}
	if snapNoTitle.Title != "Home Assistant" {
		t.Errorf("expected default title 'Home Assistant', got %q", snapNoTitle.Title)
	}
}
