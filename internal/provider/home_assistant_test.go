package provider

import (
	"context"
	"encoding/json"
	"io"
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

func TestHomeAssistant_Init(t *testing.T) {
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
		t.Setenv("TEST_HA_URL", "http://198.51.100.30:8123")
		t.Setenv("TEST_HA_TOKEN", "env-secret-token")

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
			if !slices.Contains(layout.DiscoveredEntities, exp) {
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
			if !slices.Contains(layout.DiscoveredEntities, exp) {
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

	t.Run("path traversal and invalid domain rejected", func(t *testing.T) {
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
			err := p.CallService(ctx, dom, "turn_on", nil)
			if err == nil {
				t.Errorf("expected error for invalid domain %q, got nil", dom)
			}
		}
	})

	t.Run("unsupported domain rejected even if identifier is valid", func(t *testing.T) {
		unsupportedDomains := []string{
			"automation",
			"zone",
			"shell_command",
			"hassio",
			"input_boolean",
			"input_number",
		}
		for _, dom := range unsupportedDomains {
			err := p.CallService(ctx, dom, "trigger", nil)
			if err == nil {
				t.Errorf("expected error for unsupported domain %q, got nil", dom)
			}
			if !strings.Contains(err.Error(), "unsupported domain") {
				t.Errorf("expected 'unsupported domain' error, got: %v", err)
			}
		}
	})

	t.Run("path traversal and invalid service rejected", func(t *testing.T) {
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
			err := p.CallService(ctx, "light", svc, nil)
			if err == nil {
				t.Errorf("expected error for invalid service %q, got nil", svc)
			}
		}
	})
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

	// Secret directly
	if val := resolveConfigField(map[string]any{}, opts, "token"); val != "val2" {
		t.Errorf("expected val2, got %q", val)
	}

	// ha. prefix secret
	if val := resolveConfigField(map[string]any{}, opts, "url"); val != "http://203.0.113.10:8123" {
		t.Errorf("expected ha.url resolved, got %q", val)
	}

	// env field in opts
	if val := resolveConfigField(map[string]any{"token_env": "secret_key"}, opts, "token"); val != "val1" {
		t.Errorf("expected val1 from token_env, got %q", val)
	}

	// not found
	if val := resolveConfigField(map[string]any{}, opts, "unknown"); val != "" {
		t.Errorf("expected empty string, got %q", val)
	}
}

func TestHomeAssistant_UnconfiguredErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	p := NewHomeAssistantProvider()

	if _, err := p.CallServiceWithResponse(ctx, "light", "turn_on", nil); err == nil {
		t.Error("expected error for unconfigured baseURL in CallServiceWithResponse")
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
			json.NewEncoder(w).Encode(LovelaceConfig{
				Title: "Default Dash",
			})
		case "/api/states":
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`invalid json`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ctx := context.Background()
	p := NewHomeAssistantProviderWithClient(server.Client(), server.URL, "tok")

	// Custom dash 404 should fall back to default
	cfg, err := p.FetchLovelaceConfig(ctx, "custom")
	if err != nil {
		t.Fatalf("expected fallback to succeed, got %v", err)
	}
	if cfg.Title != "Default Dash" {
		t.Errorf("expected title 'Default Dash', got %q", cfg.Title)
	}

	// States with invalid JSON should fail decoding
	if _, err := p.HydrateStates(ctx); err == nil {
		t.Error("expected decode error on invalid json states")
	}
}

func TestHomeAssistant_DiscoverLayout_EmptyConfigAndSectionViews(t *testing.T) {
	t.Parallel()
	p := NewHomeAssistantProvider()

	// nil config
	l1 := p.DiscoverLayout(nil, "view1")
	if len(l1.Cards) != 0 {
		t.Errorf("expected 0 cards for nil config, got %d", len(l1.Cards))
	}

	// empty views
	l2 := p.DiscoverLayout(&LovelaceConfig{}, "view1")
	if len(l2.Cards) != 0 {
		t.Errorf("expected 0 cards for empty views, got %d", len(l2.Cards))
	}

	// view with sections
	cfg := &LovelaceConfig{
		Views: []LovelaceView{
			{
				Title: "Sections View",
				Path:  "sections_path",
				Sections: []LovelaceSection{
					{
						Cards: []LovelaceCard{
							{Entity: "light.sec_light"},
						},
					},
				},
			},
		},
	}
	l3 := p.DiscoverLayout(cfg, "sections_path")
	if len(l3.Cards) != 1 || l3.Cards[0].Entity != "light.sec_light" {
		t.Errorf("expected 1 card from section, got %+v", l3.Cards)
	}
}

func TestHomeAssistant_ExtractEntitiesAndSelectView_FallbacksAndDeduplication(t *testing.T) {
	t.Parallel()

	// selectView empty views
	if v := selectView(nil, ""); v.Title != "" {
		t.Errorf("expected empty view for nil views")
	}

	views := []LovelaceView{
		{Title: "Home", Path: "home"},
		{Title: "Upstairs", Path: "upstairs"},
	}

	// selectView invalid index falls back to views[0]
	if v := selectView(views, "99"); v.Title != "Home" {
		t.Errorf("expected fallback to Home, got %q", v.Title)
	}
	// selectView negative index falls back to views[0]
	if v := selectView(views, "-1"); v.Title != "Home" {
		t.Errorf("expected fallback to Home, got %q", v.Title)
	}
	// selectView unknown title falls back to views[0]
	if v := selectView(views, "Unknown"); v.Title != "Home" {
		t.Errorf("expected fallback to Home, got %q", v.Title)
	}

	// ExtractEntitiesFromCard with CameraImage, nested sections, and duplicates
	card := LovelaceCard{
		Entity:      "light.desk",
		CameraImage: "camera.driveway",
		Entities: []any{
			"light.desk", // duplicate
			map[string]any{"entity": "switch.socket"},
			map[string]any{"entity": "light.desk"}, // duplicate
			12345,                                  // invalid type
		},
		Sections: []LovelaceSection{
			{
				Cards: []LovelaceCard{
					{Entity: "lock.front_door"},
				},
			},
		},
	}
	ents := ExtractEntitiesFromCard(card)
	expected := []string{"light.desk", "camera.driveway", "switch.socket", "lock.front_door"}
	if !reflect.DeepEqual(ents, expected) {
		t.Errorf("expected %v, got %v", expected, ents)
	}

	// deduplicateStrings with empty strings and duplicate strings
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

func TestHomeAssistant_Init_NameField(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	p := NewHomeAssistantProvider()
	err := p.Init(ctx, map[string]any{
		"url":   "http://198.51.100.10:8123",
		"token": "tok-123",
		"name":  "Fallback Name Title",
	}, InitOptions{})
	if err != nil {
		t.Fatalf("unexpected Init error: %v", err)
	}
	if p.cfg.Title != "Fallback Name Title" {
		t.Errorf("expected Title 'Fallback Name Title', got %q", p.cfg.Title)
	}
}

func TestHomeAssistant_Fetch_HydrationErrorAndEntityMerging(t *testing.T) {
	t.Parallel()

	// 1. Hydration failure during Fetch
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
	_ = pErr.Init(ctx, map[string]any{"url": serverErr.URL, "token": "tok"}, InitOptions{})

	_, err := pErr.Fetch(ctx)
	if err == nil || !strings.Contains(err.Error(), "home-assistant hydration failed") {
		t.Fatalf("expected hydration failed error, got: %v", err)
	}

	// 2. Fetch with configured entities merged into discovered entities when cards exist
	serverOK := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/api/lovelace/config") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"title":"Lovelace Dash","views":[{"title":"V1","cards":[{"type":"tile","entity":"light.one"}]}]}`))
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
		"token":    "tok",
		"entities": []any{"switch.extra"},
	}, InitOptions{})

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

	// 3. Title fallback to "Home Assistant" when both cfg.Title and lovelaceCfg.Title are empty
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
	_ = pNoTitle.Init(ctx, map[string]any{"url": serverNoTitle.URL, "token": "tok"}, InitOptions{})
	snapNoTitleRaw, err := pNoTitle.Fetch(ctx)
	if err != nil {
		t.Fatalf("unexpected Fetch error: %v", err)
	}
	snapNoTitle := snapNoTitleRaw.(HomeAssistantSnapshot)
	if snapNoTitle.Title != "Home Assistant" {
		t.Errorf("expected default title 'Home Assistant', got %q", snapNoTitle.Title)
	}
}

func TestHomeAssistant_CallServiceWithResponse_ErrorsAndDefaults(t *testing.T) {
	t.Parallel()

	// 1. Unmarshalable service data
	ctx := context.Background()
	p := NewHomeAssistantProviderWithClient(http.DefaultClient, "http://198.51.100.10:8123", "tok")
	_, err := p.CallServiceWithResponse(ctx, "light", "turn_on", map[string]any{
		"invalid_func": make(chan int),
	})
	if err == nil || !strings.Contains(err.Error(), "failed to marshal service data") {
		t.Fatalf("expected marshal service data error, got: %v", err)
	}

	// 2. Base URL empty
	pNoURL := NewHomeAssistantProvider()
	_, err = pNoURL.CallServiceWithResponse(ctx, "light", "turn_on", nil)
	if err == nil || !strings.Contains(err.Error(), "base URL not configured") {
		t.Fatalf("expected base URL not configured error, got: %v", err)
	}

	// 3. Client is nil defaults to http.DefaultClient + 500 status code
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-tok" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if strings.Contains(r.URL.Path, "status_error") {
			http.Error(w, "server boom", http.StatusInternalServerError)
			return
		}
		if strings.Contains(r.URL.Path, "invalid_json") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("not-json"))
			return
		}
		if strings.Contains(r.URL.Path, "empty_resp") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(""))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"entity_id":"light.desk","state":"on"}]`))
	}))
	defer ts.Close()

	pNilClient := NewHomeAssistantProvider()
	pNilClient.baseURL = ts.URL
	pNilClient.token = "test-tok"
	// client is intentionally nil to test fallback to http.DefaultClient

	// 500 status
	_, err = pNilClient.CallServiceWithResponse(ctx, "light", "status_error", nil)
	if err == nil || !strings.Contains(err.Error(), "returned status 500") {
		t.Fatalf("expected status 500 error, got: %v", err)
	}

	// invalid JSON returns nil states and nil error
	states, err := pNilClient.CallServiceWithResponse(ctx, "light", "invalid_json", nil)
	if err != nil {
		t.Fatalf("unexpected error on invalid json: %v", err)
	}
	if states != nil {
		t.Errorf("expected nil states on invalid json, got: %v", states)
	}

	// empty response body
	states, err = pNilClient.CallServiceWithResponse(ctx, "light", "empty_resp", nil)
	if err != nil {
		t.Fatalf("unexpected error on empty response: %v", err)
	}
	if len(states) != 0 {
		t.Errorf("expected 0 states for empty response, got %d", len(states))
	}

	// Network / context canceled error
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = pNilClient.CallServiceWithResponse(canceledCtx, "light", "turn_on", nil)
	if err == nil || !strings.Contains(err.Error(), "service call failed") {
		t.Fatalf("expected service call failed error, got: %v", err)
	}
}

func TestHomeAssistant_FetchLovelaceConfig_ErrorsAndFallbacks(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// 1. Base URL empty
	pNoURL := NewHomeAssistantProvider()
	_, err := pNoURL.FetchLovelaceConfig(ctx, "")
	if err == nil || !strings.Contains(err.Error(), "base URL not configured") {
		t.Fatalf("expected base URL error, got: %v", err)
	}

	// 2. Server 500
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
	_, err = p.FetchLovelaceConfig(ctx, "")
	if err == nil || !strings.Contains(err.Error(), "lovelace config request returned status 500") {
		t.Fatalf("expected status 500 error, got: %v", err)
	}

	// 3. Invalid JSON on custom dashboard path
	_, err = p.FetchLovelaceConfig(ctx, "invalid_json")
	if err == nil || !strings.Contains(err.Error(), "failed to decode lovelace config") {
		t.Fatalf("expected decode error, got: %v", err)
	}

	// 4. Context canceled
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

	// 1. Base URL empty
	pNoURL := NewHomeAssistantProvider()
	_, err := pNoURL.HydrateStates(ctx)
	if err == nil || !strings.Contains(err.Error(), "base URL not configured") {
		t.Fatalf("expected base URL error, got: %v", err)
	}

	// 2. Server 500
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal crash", http.StatusInternalServerError)
	}))
	defer ts.Close()

	p := NewHomeAssistantProviderWithClient(ts.Client(), ts.URL, "tok")
	_, err = p.HydrateStates(ctx)
	if err == nil || !strings.Contains(err.Error(), "states request returned status 500") {
		t.Fatalf("expected status 500 error, got: %v", err)
	}

	// 3. Context canceled
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = p.HydrateStates(canceledCtx)
	if err == nil || !strings.Contains(err.Error(), "states request failed") {
		t.Fatalf("expected fetch error on canceled ctx, got: %v", err)
	}
}

func TestHomeAssistant_SelectView_TitleMatch(t *testing.T) {
	t.Parallel()

	views := []LovelaceView{
		{Title: "Dashboard Main", Path: "main_path"},
		{Title: "Upstairs Bedrooms", Path: "upstairs_path"},
	}

	v := selectView(views, "Upstairs Bedrooms")
	if v.Path != "upstairs_path" {
		t.Errorf("expected view with path 'upstairs_path', got %q", v.Path)
	}
}

func TestHomeAssistant_GenerateFallbackCards_TypesAndFilter(t *testing.T) {
	t.Parallel()

	p := NewHomeAssistantProvider()

	// Explicit entities with blank entries, climate, camera, scene, and default
	explicit := []string{"", " ", "climate.hvac", "camera.front", "scene.night", "script.cleanup", "light.bulb"}
	cards := p.generateFallbackCards(nil, explicit)
	if len(cards) != 5 {
		t.Fatalf("expected 5 cards, got %d", len(cards))
	}
	if cards[0].Type != "thermostat" || cards[0].Entity != "climate.hvac" {
		t.Errorf("expected thermostat for climate.hvac, got %+v", cards[0])
	}
	if cards[1].Type != "picture-entity" || cards[1].Entity != "camera.front" {
		t.Errorf("expected picture-entity for camera.front, got %+v", cards[1])
	}
	if cards[2].Type != "button" || cards[2].Entity != "scene.night" {
		t.Errorf("expected button for scene.night, got %+v", cards[2])
	}
	if cards[3].Type != "button" || cards[3].Entity != "script.cleanup" {
		t.Errorf("expected button for script.cleanup, got %+v", cards[3])
	}
	if cards[4].Type != "tile" || cards[4].Entity != "light.bulb" {
		t.Errorf("expected tile for light.bulb, got %+v", cards[4])
	}

	// Fallback cards generated from states map with climate, camera, scene, script
	states := map[string]EntityState{
		"climate.living":  {EntityID: "climate.living"},
		"camera.backyard": {EntityID: "camera.backyard"},
		"scene.morning":   {EntityID: "scene.morning"},
		"script.bedtime":  {EntityID: "script.bedtime"},
	}
	stateCards := p.generateFallbackCards(states, nil)
	if len(stateCards) != 4 {
		t.Fatalf("expected 4 state cards, got %d", len(stateCards))
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

	// 1. Target provider not found (coordinator has no workers)
	c := NewCoordinator(CoordinatorConfig{}, nil)
	defer func() {
		c.mu.Lock()
		c.workers = nil
		c.mu.Unlock()
		_ = c.Shutdown(ctx)
	}()

	_, err := c.CallWidgetActionWithResponse(ctx, "nonexistent-widget", "light", "turn_on", nil)
	if err == nil || !strings.Contains(err.Error(), "widget provider not found") {
		t.Fatalf("expected widget provider not found error, got: %v", err)
	}

	// 2. Target provider exists, service call returns error
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
	if err == nil {
		t.Fatalf("expected service call error, got nil")
	}

	// 3. Successful CallWidgetActionWithResponse with RefreshWidget succeeding
	res, err := c.CallWidgetActionWithResponse(ctx, "ha-widget", "light", "turn_on", map[string]any{"brightness": 255})
	if err != nil {
		t.Fatalf("unexpected action error: %v", err)
	}
	states, ok := res.([]EntityState)
	if !ok || len(states) != 1 || states[0].State != "on" {
		t.Fatalf("unexpected action response states: %+v", res)
	}

	// 4. Successful CallWidgetActionWithResponse where RefreshWidget fails (refreshErr != nil)
	// Set targetWidgetID to "mismatched-key" so c.RefreshWidget("mismatched-key") is not found in c.workers
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

	// 5. Test CallWidgetAction wrapper
	c.mu.Lock()
	c.workers = map[string]*worker{
		"ha-widget": {
			widgetID:  "ha-widget",
			provider:  hap,
			refreshCh: make(chan struct{}, 1),
		},
	}
	c.mu.Unlock()

	err = c.CallWidgetAction(ctx, "ha-widget", "light", "turn_on", nil)
	if err != nil {
		t.Fatalf("unexpected CallWidgetAction error: %v", err)
	}

	err = c.CallWidgetAction(ctx, "nonexistent", "light", "turn_on", nil)
	if err == nil {
		t.Fatal("expected error for nonexistent widget in CallWidgetAction")
	}
}
