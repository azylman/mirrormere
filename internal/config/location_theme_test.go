package config_test

import (
	"strings"
	"testing"

	"github.com/azylman/mirrormere/internal/config"
)

func TestLocationAndTheme_Propagation(t *testing.T) {
	t.Parallel()

	raw := []byte(`
timezone: America/Los_Angeles
location:
  name: "Oakland Hills"
  latitude: 37.8044
  longitude: -122.2712

display:
  theme:
    mode: "auto"
  header:
    weather:
      units: imperial
  widgets:
    - id: forecast-1
      type: weather-forecast
      dimensions: [2, 1]
`)

	cfg, err := config.ParseWithEnv(raw, nil)
	if err != nil {
		t.Fatalf("unexpected error parsing valid config: %v", err)
	}

	// 1. LocationConfig populated
	loc := cfg.GetLocationConfig()
	if loc == nil {
		t.Fatal("expected GetLocationConfig to return non-nil")
	}
	if loc.Name != "Oakland Hills" || *loc.Latitude != 37.8044 || *loc.Longitude != -122.2712 {
		t.Errorf("unexpected location config: %+v", loc)
	}

	// 2. Header Weather inherited location and coordinates
	hw := cfg.Display.Header.Weather
	if hw == nil {
		t.Fatal("expected header weather to be non-nil")
	}
	if hw.Location != "Oakland Hills" {
		t.Errorf("expected header weather location 'Oakland Hills', got '%s'", hw.Location)
	}
	if hw.Latitude != 37.8044 || hw.Longitude != -122.2712 {
		t.Errorf("expected header weather coordinates (37.8044, -122.2712), got (%f, %f)", hw.Latitude, hw.Longitude)
	}

	// 3. Widget inherited location and coordinates
	if len(cfg.Display.Widgets) != 1 {
		t.Fatalf("expected 1 widget, got %d", len(cfg.Display.Widgets))
	}
	w := cfg.Display.Widgets[0]
	if w.Config["location_name"] != "Oakland Hills" {
		t.Errorf("expected widget location_name 'Oakland Hills', got '%v'", w.Config["location_name"])
	}
	if w.Config["latitude"] != 37.8044 || w.Config["longitude"] != -122.2712 {
		t.Errorf("expected widget coordinates (37.8044, -122.2712), got (%v, %v)", w.Config["latitude"], w.Config["longitude"])
	}

	// 4. Theme mode
	if cfg.Display.Theme == nil || cfg.Display.Theme.GetMode() != "auto" {
		t.Errorf("expected theme mode 'auto', got '%v'", cfg.Display.Theme)
	}
}

func TestTheme_Validation(t *testing.T) {
	t.Parallel()

	// Invalid mode
	rawInvalidMode := []byte(`
timezone: America/Los_Angeles
display:
  theme:
    mode: "cyberpunk"
  widgets:
    - id: w1
      type: spacer
      dimensions: [6, 2]
`)
	_, err := config.ParseWithEnv(rawInvalidMode, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid theme mode 'cyberpunk'") {
		t.Fatalf("expected invalid theme mode error, got %v", err)
	}

	// Auto mode without coordinates
	rawAutoWithoutCoords := []byte(`
timezone: America/Los_Angeles
display:
  theme:
    mode: "auto"
  widgets:
    - id: w1
      type: spacer
      dimensions: [6, 2]
`)
	_, err = config.ParseWithEnv(rawAutoWithoutCoords, nil)
	if err == nil || !strings.Contains(err.Error(), "theme mode 'auto' requires location coordinates") {
		t.Fatalf("expected error for auto mode without coordinates, got %v", err)
	}

	// Backwards compatibility: auto mode with coordinates defined in display.header.weather
	rawAutoWithHeaderCoords := []byte(`
timezone: America/Los_Angeles
display:
  theme:
    mode: "auto"
  header:
    weather:
      latitude: 37.8
      longitude: -122.2
      units: imperial
  widgets:
    - id: w1
      type: spacer
      dimensions: [6, 2]
`)
	cfg, err := config.ParseWithEnv(rawAutoWithHeaderCoords, nil)
	if err != nil {
		t.Fatalf("expected auto mode with header weather coords to pass, got: %v", err)
	}
	if cfg.GetLocationConfig() == nil || *cfg.GetLocationConfig().Latitude != 37.8 {
		t.Errorf("expected GetLocationConfig to resolve from header weather, got %+v", cfg.GetLocationConfig())
	}
}

func TestLocation_BoundsValidation(t *testing.T) {
	t.Parallel()

	// Latitude out of bounds
	rawBadLat := []byte(`
timezone: America/Los_Angeles
location:
  latitude: 95.0
  longitude: -122.2
display:
  widgets:
    - id: w1
      type: spacer
      dimensions: [6, 2]
`)
	_, err := config.ParseWithEnv(rawBadLat, nil)
	if err == nil || !strings.Contains(err.Error(), "location latitude must be between -90 and 90") {
		t.Fatalf("expected latitude bounds error, got %v", err)
	}

	// Longitude out of bounds
	rawBadLon := []byte(`
timezone: America/Los_Angeles
location:
  latitude: 37.8
  longitude: -195.0
display:
  widgets:
    - id: w1
      type: spacer
      dimensions: [6, 2]
`)
	_, err = config.ParseWithEnv(rawBadLon, nil)
	if err == nil || !strings.Contains(err.Error(), "location longitude must be between -180 and 180") {
		t.Fatalf("expected longitude bounds error, got %v", err)
	}
}
