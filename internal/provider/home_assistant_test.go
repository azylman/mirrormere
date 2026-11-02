package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestHomeAssistant_ProviderInterface(t *testing.T) {
	t.Parallel()
	var p Provider = NewHomeAssistantProvider()
	if p == nil {
		t.Fatal("expected non-nil Provider instance")
	}
}

func TestHomeAssistant_SupportedDomains(t *testing.T) {
	t.Parallel()
	domains := GetSupportedDomains()
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

func TestHomeAssistant_Init(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("valid configuration", func(t *testing.T) {
		p := NewHomeAssistantProvider()
		err := p.Init(ctx, map[string]any{
			"url":       "http://198.51.100.10:8123/",
			"token":     "secret-token-123",
			"dashboard": "lovelace-tablet",
			"view":      "living-room",
			"title":     "Main Dashboard",
			"domains":   []any{"light", "switch"},
			"entities":  []any{"light.living_room", "switch.fan"},
		}, InitOptions{})

		if err != nil {
			t.Fatalf("unexpected Init error: %v", err)
		}
		if p.baseURL != "http://198.51.100.10:8123" {
			t.Errorf("expected trimmed baseURL, got %q", p.baseURL)
		}
		if p.token != "secret-token-123" {
			t.Errorf("expected token secret-token-123, got %q", p.token)
		}
		if p.cfg.Dashboard != "lovelace-tablet" {
			t.Errorf("expected dashboard lovelace-tablet, got %q", p.cfg.Dashboard)
		}
		if p.cfg.View != "living-room" {
			t.Errorf("expected view living-room, got %q", p.cfg.View)
		}
		if p.cfg.Title != "Main Dashboard" {
			t.Errorf("expected title 'Main Dashboard', got %q", p.cfg.Title)
		}
		if len(p.cfg.Domains) != 2 || p.cfg.Domains[0] != "light" || p.cfg.Domains[1] != "switch" {
			t.Errorf("unexpected domains: %v", p.cfg.Domains)
		}
		if len(p.cfg.Entities) != 2 || p.cfg.Entities[0] != "light.living_room" {
			t.Errorf("unexpected entities: %v", p.cfg.Entities)
		}
	})

	t.Run("missing URL error", func(t *testing.T) {
		p := NewHomeAssistantProvider()
		err := p.Init(ctx, map[string]any{
			"token": "secret-token-123",
		}, InitOptions{})

		if err == nil || !strings.Contains(err.Error(), "requires a valid URL") {
			t.Fatalf("expected missing URL error, got %v", err)
		}
	})

	t.Run("missing token error", func(t *testing.T) {
		p := NewHomeAssistantProvider()
		err := p.Init(ctx, map[string]any{
			"url": "http://198.51.100.10:8123",
		}, InitOptions{})

		if err == nil || !strings.Contains(err.Error(), "requires a valid access token") {
			t.Fatalf("expected missing token error, got %v", err)
		}
	})

	t.Run("resolve from InitOptions and secrets", func(t *testing.T) {
		p := NewHomeAssistantProvider()
		opts := InitOptions{
			Endpoint: "http://198.51.100.20:8123",
			Token:    "opt-token",
			Secrets: map[string]string{
				"CUSTOM_TOKEN": "custom-secret-token",
			},
		}

		err := p.Init(ctx, map[string]any{
			"token_env": "CUSTOM_TOKEN",
		}, opts)

		if err != nil {
			t.Fatalf("unexpected Init error: %v", err)
		}
		if p.baseURL != "http://198.51.100.20:8123" {
			t.Errorf("expected baseURL from opts.Endpoint, got %q", p.baseURL)
		}
		if p.token != "custom-secret-token" {
			t.Errorf("expected token from secret CUSTOM_TOKEN, got %q", p.token)
		}
	})

	t.Run("resolve from OS env vars", func(t *testing.T) {
		os.Setenv("TEST_HA_URL", "http://198.51.100.30:8123")
		os.Setenv("TEST_HA_TOKEN", "env-secret-token")
		defer os.Unsetenv("TEST_HA_URL")
		defer os.Unsetenv("TEST_HA_TOKEN")

		p := NewHomeAssistantProvider()
		err := p.Init(ctx, map[string]any{
			"url_env":   "TEST_HA_URL",
			"token_env": "TEST_HA_TOKEN",
		}, InitOptions{})

		if err != nil {
			t.Fatalf("unexpected Init error: %v", err)
		}
		if p.baseURL != "http://198.51.100.30:8123" {
			t.Errorf("expected baseURL from env, got %q", p.baseURL)
		}
		if p.token != "env-secret-token" {
			t.Errorf("expected token from env, got %q", p.token)
		}
	})
}

func TestHomeAssistant_LovelaceLayoutDiscovery(t *testing.T) {
	t.Parallel()

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
		t.Fatalf("failed to unmarshal test Lovelace config: %v", err)
	}

	p := NewHomeAssistantProvider()

	t.Run("discover first view cards and entities (overview)", func(t *testing.T) {
		layout := p.DiscoverLayout(&cfg, "0")
		if layout.Title != "Home Dashboard" {
			t.Errorf("expected title 'Home Dashboard', got %q", layout.Title)
		}
		if layout.View.Path != "overview" {
			t.Errorf("expected view path 'overview', got %q", layout.View.Path)
		}
		if len(layout.Cards) != 4 {
			t.Fatalf("expected 4 cards in overview, got %d", len(layout.Cards))
		}

		// Verify card types: tile, entities, grid, picture-entity
		cardTypes := []string{layout.Cards[0].Type, layout.Cards[1].Type, layout.Cards[2].Type, layout.Cards[3].Type}
		expectedTypes := []string{"tile", "entities", "grid", "picture-entity"}
		if !reflect.DeepEqual(cardTypes, expectedTypes) {
			t.Errorf("expected card types %v, got %v", expectedTypes, cardTypes)
		}

		// Verify entities extracted:
		// light.living_room_ceiling (tile)
		// switch.coffee_maker (entities string)
		// switch.porch_light (entities map)
		// script.good_night (grid child button)
		// climate.hallway_nest (grid child thermostat)
		// camera.front_doorbell (picture-entity)
		expectedEntities := []string{
			"light.living_room_ceiling",
			"switch.coffee_maker",
			"switch.porch_light",
			"script.good_night",
			"climate.hallway_nest",
			"camera.front_doorbell",
		}

		for _, exp := range expectedEntities {
			found := false
			for _, act := range layout.DiscoveredEntities {
				if act == exp {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected entity %q in discovered entities %v", exp, layout.DiscoveredEntities)
			}
		}
	})

	t.Run("discover sections view (sections layout)", func(t *testing.T) {
		layout := p.DiscoverLayout(&cfg, "climate-view")
		if layout.View.Path != "climate-view" {
			t.Errorf("expected view path 'climate-view', got %q", layout.View.Path)
		}
		if layout.View.Type != "sections" {
			t.Errorf("expected view type 'sections', got %q", layout.View.Type)
		}
		if len(layout.View.Sections) != 2 {
			t.Fatalf("expected 2 sections in climate-view, got %d", len(layout.View.Sections))
		}
		// Flattened cards should contain all 4 cards from the 2 sections
		if len(layout.Cards) != 4 {
			t.Fatalf("expected 4 cards collected across sections, got %d", len(layout.Cards))
		}

		expectedEntities := []string{
			"climate.living_room_ac",
			"fan.ceiling_fan",
			"lock.front_door",
			"cover.garage_door",
		}
		for _, exp := range expectedEntities {
			found := false
			for _, act := range layout.DiscoveredEntities {
				if act == exp {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected entity %q in discovered entities %v", exp, layout.DiscoveredEntities)
			}
		}
	})

	t.Run("select view by title", func(t *testing.T) {
		layout := p.DiscoverLayout(&cfg, "Overview")
		if layout.View.Path != "overview" {
			t.Errorf("expected view path 'overview', got %q", layout.View.Path)
		}
	})

	t.Run("fallback to view 0 when selector not found", func(t *testing.T) {
		layout := p.DiscoverLayout(&cfg, "non-existent-view")
		if layout.View.Path != "overview" {
			t.Errorf("expected view path 'overview' on fallback, got %q", layout.View.Path)
		}
	})
}

func TestHomeAssistant_HydrateStates(t *testing.T) {
	t.Parallel()

	mockStates := []EntityState{
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
				"media_title":  "Hey Jude",
				"volume_level": 0.4,
			},
		},
		{
			EntityID: "camera.driveway",
			State:    "idle",
			Attributes: map[string]any{
				"access_token": "cam-token-xyz",
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
		// Unsupported domains that should be filtered out
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
		json.NewEncoder(w).Encode(mockStates)
	}))
	defer server.Close()

	ctx := context.Background()

	t.Run("hydrates all 12 supported domains and filters others", func(t *testing.T) {
		p := NewHomeAssistantProviderWithClient(server.Client(), server.URL, "test-ha-token")
		err := p.Init(ctx, map[string]any{
			"url":   server.URL,
			"token": "test-ha-token",
		}, InitOptions{})
		if err != nil {
			t.Fatalf("Init failed: %v", err)
		}

		states, err := p.HydrateStates(ctx)
		if err != nil {
			t.Fatalf("HydrateStates failed: %v", err)
		}

		if len(states) != 12 {
			t.Fatalf("expected 12 hydrated states across the 12 domains, got %d", len(states))
		}

		// Verify all 12 domains exist
		expectedDomains := []string{
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
		}

		for _, entityID := range expectedDomains {
			if _, ok := states[entityID]; !ok {
				t.Errorf("expected entity %q to be hydrated", entityID)
			}
		}

		// Verify filtered out
		if _, ok := states["automation.motion_lighting"]; ok {
			t.Error("automation domain should have been filtered out")
		}
		if _, ok := states["zone.home"]; ok {
			t.Error("zone domain should have been filtered out")
		}
	})

	t.Run("filters by configured domains", func(t *testing.T) {
		p := NewHomeAssistantProviderWithClient(server.Client(), server.URL, "test-ha-token")
		err := p.Init(ctx, map[string]any{
			"url":     server.URL,
			"token":   "test-ha-token",
			"domains": []any{"light", "climate"},
		}, InitOptions{})
		if err != nil {
			t.Fatalf("Init failed: %v", err)
		}

		states, err := p.HydrateStates(ctx)
		if err != nil {
			t.Fatalf("HydrateStates failed: %v", err)
		}

		if len(states) != 2 {
			t.Fatalf("expected 2 states for light & climate, got %d", len(states))
		}
		if _, ok := states["light.kitchen"]; !ok {
			t.Error("expected light.kitchen to be present")
		}
		if _, ok := states["climate.hallway"]; !ok {
			t.Error("expected climate.hallway to be present")
		}
	})
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
			json.NewEncoder(w).Encode(lovelaceData)
		case "/api/states":
			json.NewEncoder(w).Encode(statesData)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ctx := context.Background()
	p := NewHomeAssistantProviderWithClient(server.Client(), server.URL, "token-123")
	err := p.Init(ctx, map[string]any{
		"url":   server.URL,
		"token": "token-123",
	}, InitOptions{})
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
			json.NewEncoder(w).Encode(statesData)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ctx := context.Background()
	p := NewHomeAssistantProviderWithClient(server.Client(), server.URL, "token-123")
	err := p.Init(ctx, map[string]any{
		"url":   server.URL,
		"token": "token-123",
	}, InitOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	res, err := p.Fetch(ctx)
	if err != nil {
		t.Fatalf("Fetch should succeed even when Lovelace 404s: %v", err)
	}

	snapshot := res.(HomeAssistantSnapshot)
	if len(snapshot.Cards) != 3 {
		t.Fatalf("expected 3 generated fallback cards, got %d", len(snapshot.Cards))
	}
	if len(snapshot.Entities) != 3 {
		t.Fatalf("expected 3 hydrated entities, got %d", len(snapshot.Entities))
	}

	// Verify generated card types matching domain heuristics
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

func TestHomeAssistant_CallService(t *testing.T) {
	t.Parallel()

	var receivedMethod string
	var receivedPath string
	var receivedAuth string
	var receivedBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedPath = r.URL.Path
		receivedAuth = r.Header.Get("Authorization")

		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &receivedBody)

		if strings.HasPrefix(r.URL.Path, "/api/services/light/turn_on") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[{"entity_id": "light.living_room", "state": "on"}]`))
			return
		}

		if strings.HasPrefix(r.URL.Path, "/api/services/light/not_found") {
			http.Error(w, "Service not found", http.StatusNotFound)
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()

	ctx := context.Background()
	p := NewHomeAssistantProviderWithClient(server.Client(), server.URL, "bearer-test-token")
	err := p.Init(ctx, map[string]any{
		"url":   server.URL,
		"token": "bearer-test-token",
	}, InitOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	t.Run("successful service call with data", func(t *testing.T) {
		serviceData := map[string]any{
			"entity_id":  "light.living_room",
			"brightness": 255,
		}

		err := p.CallService(ctx, "light", "turn_on", serviceData)
		if err != nil {
			t.Fatalf("CallService failed: %v", err)
		}

		if receivedMethod != http.MethodPost {
			t.Errorf("expected POST method, got %q", receivedMethod)
		}
		if receivedPath != "/api/services/light/turn_on" {
			t.Errorf("expected path /api/services/light/turn_on, got %q", receivedPath)
		}
		if receivedAuth != "Bearer bearer-test-token" {
			t.Errorf("expected Authorization header 'Bearer bearer-test-token', got %q", receivedAuth)
		}
		if receivedBody["entity_id"] != "light.living_room" {
			t.Errorf("expected entity_id in body, got %v", receivedBody)
		}
	})

	t.Run("successful service call with response states", func(t *testing.T) {
		states, err := p.CallServiceWithResponse(ctx, "light", "turn_on", map[string]any{
			"entity_id": "light.living_room",
		})
		if err != nil {
			t.Fatalf("CallServiceWithResponse failed: %v", err)
		}
		if len(states) != 1 || states[0].EntityID != "light.living_room" || states[0].State != "on" {
			t.Fatalf("unexpected states returned: %v", states)
		}
	})

	t.Run("service call with empty parameters returns error", func(t *testing.T) {
		err := p.CallService(ctx, "", "turn_on", nil)
		if err == nil {
			t.Fatal("expected error on empty domain")
		}
		err = p.CallService(ctx, "light", "", nil)
		if err == nil {
			t.Fatal("expected error on empty service")
		}
	})

	t.Run("upstream HTTP error is propagated", func(t *testing.T) {
		err := p.CallService(ctx, "light", "not_found", nil)
		if err == nil || !strings.Contains(err.Error(), "404") {
			t.Fatalf("expected 404 error propagated, got %v", err)
		}
	})
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
