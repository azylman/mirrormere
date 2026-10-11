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

func TestLocationAndTheme_Getters(t *testing.T) {
	t.Parallel()

	// 1. Nil LocationConfig
	var nilLoc *config.LocationConfig
	if nilLoc.GetLatitude() != 0 || nilLoc.GetLongitude() != 0 {
		t.Errorf("expected 0 for nil LocationConfig, got %f, %f", nilLoc.GetLatitude(), nilLoc.GetLongitude())
	}

	// 2. Non-nil LocationConfig with nil fields
	partialLoc := &config.LocationConfig{Name: "Nowhere"}
	if partialLoc.GetLatitude() != 0 || partialLoc.GetLongitude() != 0 {
		t.Errorf("expected 0 for partial LocationConfig, got %f, %f", partialLoc.GetLatitude(), partialLoc.GetLongitude())
	}

	// 3. Valid LocationConfig
	lat := 37.8044
	lon := -122.2712
	validLoc := &config.LocationConfig{Latitude: &lat, Longitude: &lon}
	if validLoc.GetLatitude() != 37.8044 || validLoc.GetLongitude() != -122.2712 {
		t.Errorf("unexpected coordinates: %f, %f", validLoc.GetLatitude(), validLoc.GetLongitude())
	}

	// 4. ThemeConfig GetMode edge cases
	var nilTheme *config.ThemeConfig
	if nilTheme.GetMode() != "day" {
		t.Errorf("expected 'day' for nil ThemeConfig, got %q", nilTheme.GetMode())
	}
	emptyTheme := &config.ThemeConfig{Mode: "   "}
	if emptyTheme.GetMode() != "day" {
		t.Errorf("expected 'day' for whitespace ThemeConfig, got %q", emptyTheme.GetMode())
	}
	nightTheme := &config.ThemeConfig{Mode: "NIGHT"}
	if nightTheme.GetMode() != "night" {
		t.Errorf("expected 'night' for uppercase ThemeConfig, got %q", nightTheme.GetMode())
	}

	// 5. Config GetLocationConfig nil receiver
	var nilCfg *config.Config
	if nilCfg.GetLocationConfig() != nil {
		t.Errorf("expected nil for nil Config.GetLocationConfig")
	}

	// 6. Config without location or header weather
	emptyCfg := &config.Config{}
	if emptyCfg.GetLocationConfig() != nil {
		t.Errorf("expected nil for empty Config.GetLocationConfig")
	}
}

