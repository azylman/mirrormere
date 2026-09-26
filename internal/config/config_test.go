package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azylman/mirrormere/internal/config"
)

func mockGetenv(vars map[string]string) func(string) string {
	return func(key string) string {
		return vars[key]
	}
}

func TestLoad_ExampleConfig(t *testing.T) {
	t.Parallel()

	examplePath := filepath.Join("..", "..", "deploy", "examples", "config.yaml")

	// 1. Without FAMILY_CALENDAR_URL set, validation must fail per SPEC-012 §6
	_, err := config.LoadWithEnv(examplePath, mockGetenv(nil))
	if err == nil {
		t.Fatal("expected error loading example config without FAMILY_CALENDAR_URL, got nil")
	}
	if !strings.Contains(err.Error(), "FAMILY_CALENDAR_URL") {
		t.Fatalf("expected error mentioning FAMILY_CALENDAR_URL, got: %v", err)
	}

	// 2. With FAMILY_CALENDAR_URL set, parsing succeeds end-to-end
	mockEnv := map[string]string{
		"FAMILY_CALENDAR_URL": "https://calendar.google.com/calendar/ical/example%40gmail.com/private-token/basic.ics",
	}
	cfg, err := config.LoadWithEnv(examplePath, mockGetenv(mockEnv))
	if err != nil {
		t.Fatalf("unexpected error loading example config: %v", err)
	}

	// Validate top-level timezone
	if cfg.Timezone != "America/Los_Angeles" {
		t.Errorf("expected timezone America/Los_Angeles, got %s", cfg.Timezone)
	}
	if cfg.Location() == nil || cfg.Location().String() != "America/Los_Angeles" {
		t.Errorf("expected location America/Los_Angeles, got %v", cfg.Location())
	}

	// Validate rotation
	if cfg.Display.Rotation.GetIntervalSeconds() != 30 {
		t.Errorf("expected rotation interval 30, got %d", cfg.Display.Rotation.GetIntervalSeconds())
	}
	if cfg.Display.Rotation.GetTransition() != "slide" {
		t.Errorf("expected transition slide, got %s", cfg.Display.Rotation.GetTransition())
	}
	if !cfg.Display.Rotation.GetPauseOnTouch() {
		t.Error("expected pause_on_touch to be true")
	}
	if cfg.Display.Rotation.GetPauseDurationSeconds() != 120 {
		t.Errorf("expected pause_duration_seconds 120, got %d", cfg.Display.Rotation.GetPauseDurationSeconds())
	}

	// Validate fixed header
	if !cfg.Display.Header.IsEnabled() {
		t.Error("expected header to be enabled")
	}
	if len(cfg.Display.Header.Elements) != 4 {
		t.Errorf("expected 4 header elements, got %d", len(cfg.Display.Header.Elements))
	}
	if cfg.Display.Header.Weather == nil {
		t.Fatal("expected header weather to be configured")
	}
	if cfg.Display.Header.Weather.GetRefreshIntervalSeconds() != 900 {
		t.Errorf("expected header weather interval 900, got %d", cfg.Display.Header.Weather.GetRefreshIntervalSeconds())
	}
	if cfg.Display.Header.Weather.Units != "imperial" {
		t.Errorf("expected units imperial, got %s", cfg.Display.Header.Weather.Units)
	}

	// Validate widgets
	if len(cfg.Display.Widgets) != 5 {
		t.Fatalf("expected 5 widgets, got %d", len(cfg.Display.Widgets))
	}

	calWidget := cfg.Display.Widgets[0]
	if calWidget.ID != "family-calendar" || calWidget.Type != "calendar-agenda" {
		t.Errorf("unexpected first widget: %+v", calWidget)
	}
	calendars, ok := calWidget.Config["calendars"].([]any)
	if !ok || len(calendars) == 0 {
		t.Fatalf("expected calendars slice in widget config, got %+v", calWidget.Config)
	}
	firstCal, ok := calendars[0].(map[string]any)
	if !ok {
		t.Fatalf("expected calendar map, got %+v", calendars[0])
	}
	if _, exists := firstCal["url"]; exists {
		t.Errorf("expected secret key 'url' NOT to exist in Config map, got %v", firstCal["url"])
	}
	if calWidget.Secrets["calendars[0].url"] != mockEnv["FAMILY_CALENDAR_URL"] {
		t.Errorf("expected resolved secret %s, got %v", mockEnv["FAMILY_CALENDAR_URL"], calWidget.Secrets["calendars[0].url"])
	}
}

func TestLoad_DefaultConfigPath(t *testing.T) {
	t.Parallel()

	// Calling Load("") targets /config/config.yaml which should fail cleanly if nonexistent
	_, err := config.Load("")
	if err == nil {
		t.Fatal("expected error reading non-existent default config path")
	}
	if !strings.Contains(err.Error(), "/config/config.yaml") {
		t.Fatalf("expected error mentioning /config/config.yaml, got %v", err)
	}
}

func TestLoad_NonExistentFile(t *testing.T) {
	t.Parallel()

	_, err := config.LoadWithEnv("/non/existent/path/config.yaml", mockGetenv(nil))
	if err == nil {
		t.Fatal("expected error loading non-existent file, got nil")
	}
}

func TestParse_Empty(t *testing.T) {
	t.Parallel()

	cases := [][]byte{
		nil,
		{},
		[]byte("   \n\t  "),
	}

	for _, c := range cases {
		_, err := config.ParseWithEnv(c, mockGetenv(nil))
		if err == nil {
			t.Errorf("expected error parsing empty bytes, got nil")
		}
	}
}

func TestParse_InvalidYAML(t *testing.T) {
	t.Parallel()

	raw := []byte(`
timezone: America/Los_Angeles
display:
  widgets: [unclosed bracket
`)
	_, err := config.ParseWithEnv(raw, mockGetenv(nil))
	if err == nil {
		t.Fatal("expected yaml syntax error, got nil")
	}
	if !strings.Contains(err.Error(), "yaml syntax error") {
		t.Fatalf("expected yaml syntax error prefix, got %v", err)
	}
}

func TestParse_RootNotMapping(t *testing.T) {
	t.Parallel()

	raw := []byte(`
- item1
- item2
`)
	_, err := config.ParseWithEnv(raw, mockGetenv(nil))
	if err == nil {
		t.Fatal("expected error when root is not a mapping, got nil")
	}
	if !strings.Contains(err.Error(), "YAML mapping") {
		t.Fatalf("expected YAML mapping error, got %v", err)
	}
}

func TestParse_DisallowedScreens_TopLevel(t *testing.T) {
	t.Parallel()

	raw := []byte(`
timezone: America/Los_Angeles
screens:
  - id: screen-1
display:
  widgets:
    - id: test
      type: spacer
`)
	_, err := config.ParseWithEnv(raw, mockGetenv(nil))
	if err == nil {
		t.Fatal("expected error for top-level screens, got nil")
	}
	if !strings.Contains(err.Error(), "disallowed top-level key 'screens'") {
		t.Fatalf("expected disallowed top-level key 'screens' error, got %v", err)
	}
}

func TestParse_DisallowedProviders_TopLevel(t *testing.T) {
	t.Parallel()

	raw := []byte(`
timezone: America/Los_Angeles
providers:
  weather:
    api: open-meteo
display:
  widgets:
    - id: test
      type: spacer
`)
	_, err := config.ParseWithEnv(raw, mockGetenv(nil))
	if err == nil {
		t.Fatal("expected error for top-level providers, got nil")
	}
	if !strings.Contains(err.Error(), "disallowed top-level key 'providers'") {
		t.Fatalf("expected disallowed top-level key 'providers' error, got %v", err)
	}
}

func TestParse_DisallowedScreens_Display(t *testing.T) {
	t.Parallel()

	raw := []byte(`
timezone: America/Los_Angeles
display:
  screens:
    - id: screen-1
  widgets:
    - id: test
      type: spacer
`)
	_, err := config.ParseWithEnv(raw, mockGetenv(nil))
	if err == nil {
		t.Fatal("expected error for display.screens, got nil")
	}
	if !strings.Contains(err.Error(), "display contains disallowed key 'screens'") {
		t.Fatalf("expected disallowed display screens error, got %v", err)
	}
}

func TestParse_DisallowedProviders_Display(t *testing.T) {
	t.Parallel()

	raw := []byte(`
timezone: America/Los_Angeles
display:
  providers:
    calendar: google
  widgets:
    - id: test
      type: spacer
`)
	_, err := config.ParseWithEnv(raw, mockGetenv(nil))
	if err == nil {
		t.Fatal("expected error for display.providers, got nil")
	}
	if !strings.Contains(err.Error(), "display contains disallowed key 'providers'") {
		t.Fatalf("expected disallowed display providers error, got %v", err)
	}
}

func TestParse_UnknownTopLevelKey(t *testing.T) {
	t.Parallel()

	raw := []byte(`
timezone: America/Los_Angeles
theme: purple-cyberpunk
display:
  widgets:
    - id: test
      type: spacer
`)
	_, err := config.ParseWithEnv(raw, mockGetenv(nil))
	if err == nil {
		t.Fatal("expected error for unknown top-level key, got nil")
	}
	if !strings.Contains(err.Error(), "unknown or disallowed top-level key 'theme'") {
		t.Fatalf("expected unknown top-level key error, got %v", err)
	}
}

func TestParse_UnknownDisplayKey(t *testing.T) {
	t.Parallel()

	raw := []byte(`
timezone: America/Los_Angeles
display:
  unrecognized_key: value
  widgets:
    - id: test
      type: spacer
`)
	_, err := config.ParseWithEnv(raw, mockGetenv(nil))
	if err == nil {
		t.Fatal("expected error for unknown display key, got nil")
	}
	if !strings.Contains(err.Error(), "unknown key 'unrecognized_key' under display") {
		t.Fatalf("expected unknown display key error, got %v", err)
	}
}

func TestParse_InterpolationProhibited(t *testing.T) {
	t.Parallel()

	raw := []byte(`
timezone: America/Los_Angeles
display:
  widgets:
    - id: test-widget
      type: sensor-card
      endpoint: "http://${SERVER_HOST}:8080/api"
`)
	_, err := config.ParseWithEnv(raw, mockGetenv(nil))
	if err == nil {
		t.Fatal("expected error for ${VAR} interpolation, got nil")
	}
	if !strings.Contains(err.Error(), "arbitrary '${...}' string interpolation is prohibited") {
		t.Fatalf("expected interpolation prohibited error, got %v", err)
	}
}

func TestParse_InterpolationInCommentAllowed(t *testing.T) {
	t.Parallel()

	raw := []byte(`
# This comment mentions ${VAR} but should not trigger an error
timezone: America/Los_Angeles
display:
  widgets:
    - id: test-widget
      type: spacer
`)
	cfg, err := config.ParseWithEnv(raw, mockGetenv(nil))
	if err != nil {
		t.Fatalf("unexpected error with comment interpolation: %v", err)
	}
	if cfg.Timezone != "America/Los_Angeles" {
		t.Errorf("expected timezone America/Los_Angeles, got %s", cfg.Timezone)
	}
}

func TestValidate_MissingTimezone(t *testing.T) {
	t.Parallel()

	raw := []byte(`
display:
  widgets:
    - id: test
      type: spacer
`)
	_, err := config.ParseWithEnv(raw, mockGetenv(nil))
	if err == nil {
		t.Fatal("expected error for missing timezone, got nil")
	}
	if !strings.Contains(err.Error(), "missing required configuration field 'timezone'") {
		t.Fatalf("expected missing timezone error, got %v", err)
	}
}

func TestValidate_InvalidTimezone(t *testing.T) {
	t.Parallel()

	raw := []byte(`
timezone: "Mars/Olympus_Mons"
display:
  widgets:
    - id: test
      type: spacer
`)
	_, err := config.ParseWithEnv(raw, mockGetenv(nil))
	if err == nil {
		t.Fatal("expected error for invalid timezone, got nil")
	}
	if !strings.Contains(err.Error(), "invalid timezone") {
		t.Fatalf("expected invalid timezone error, got %v", err)
	}
}

func TestValidate_GridDimensions(t *testing.T) {
	t.Parallel()

	// Invalid columns
	rawCols := []byte(`
timezone: UTC
display:
  grid:
    columns: 4
    rows: 2
  widgets:
    - id: test
      type: spacer
`)
	_, err := config.ParseWithEnv(rawCols, mockGetenv(nil))
	if err == nil || !strings.Contains(err.Error(), "grid columns must be 6") {
		t.Fatalf("expected grid columns error, got %v", err)
	}

	// Invalid rows
	rawRows := []byte(`
timezone: UTC
display:
  grid:
    columns: 6
    rows: 3
  widgets:
    - id: test
      type: spacer
`)
	_, err = config.ParseWithEnv(rawRows, mockGetenv(nil))
	if err == nil || !strings.Contains(err.Error(), "grid rows must be 2") {
		t.Fatalf("expected grid rows error, got %v", err)
	}
}

func TestValidate_Rotation(t *testing.T) {
	t.Parallel()

	// Negative interval
	rawNegInterval := []byte(`
timezone: UTC
display:
  rotation:
    interval_seconds: -5
  widgets:
    - id: test
      type: spacer
`)
	_, err := config.ParseWithEnv(rawNegInterval, mockGetenv(nil))
	if err == nil || !strings.Contains(err.Error(), "rotation interval_seconds must be non-negative") {
		t.Fatalf("expected negative interval error, got %v", err)
	}

	// Negative pause duration
	rawNegPause := []byte(`
timezone: UTC
display:
  rotation:
    pause_duration_seconds: -10
  widgets:
    - id: test
      type: spacer
`)
	_, err = config.ParseWithEnv(rawNegPause, mockGetenv(nil))
	if err == nil || !strings.Contains(err.Error(), "rotation pause_duration_seconds must be non-negative") {
		t.Fatalf("expected negative pause duration error, got %v", err)
	}

	// Invalid transition
	rawTransition := []byte(`
timezone: UTC
display:
  rotation:
    transition: "zoom-in"
  widgets:
    - id: test
      type: spacer
`)
	_, err = config.ParseWithEnv(rawTransition, mockGetenv(nil))
	if err == nil || !strings.Contains(err.Error(), "invalid rotation transition 'zoom-in'") {
		t.Fatalf("expected invalid transition error, got %v", err)
	}

	// Valid transitions
	for _, tr := range []string{"slide", "fade", "instant", "none"} {
		raw := []byte(`
timezone: UTC
display:
  rotation:
    transition: "` + tr + `"
  widgets:
    - id: test
      type: spacer
`)
		cfg, err := config.ParseWithEnv(raw, mockGetenv(nil))
		if err != nil {
			t.Fatalf("unexpected error for transition %s: %v", tr, err)
		}
		if cfg.Display.Rotation.GetTransition() != tr {
			t.Errorf("expected transition %s, got %s", tr, cfg.Display.Rotation.GetTransition())
		}
	}
}

func TestValidate_HeaderWeather(t *testing.T) {
	t.Parallel()

	// Non-positive refresh interval
	rawInterval := []byte(`
timezone: UTC
display:
  header:
    weather:
      refresh_interval_seconds: 0
      latitude: 37.8
      longitude: -122.2
  widgets:
    - id: test
      type: spacer
`)
	_, err := config.ParseWithEnv(rawInterval, mockGetenv(nil))
	if err == nil || !strings.Contains(err.Error(), "header weather refresh_interval_seconds must be positive") {
		t.Fatalf("expected non-positive interval error, got %v", err)
	}

	// Latitude out of bounds
	rawLat := []byte(`
timezone: UTC
display:
  header:
    weather:
      refresh_interval_seconds: 300
      latitude: 95.0
      longitude: -122.2
  widgets:
    - id: test
      type: spacer
`)
	_, err = config.ParseWithEnv(rawLat, mockGetenv(nil))
	if err == nil || !strings.Contains(err.Error(), "latitude must be between -90 and 90") {
		t.Fatalf("expected latitude bounds error, got %v", err)
	}

	// Longitude out of bounds
	rawLon := []byte(`
timezone: UTC
display:
  header:
    weather:
      refresh_interval_seconds: 300
      latitude: 37.8
      longitude: -190.0
  widgets:
    - id: test
      type: spacer
`)
	_, err = config.ParseWithEnv(rawLon, mockGetenv(nil))
	if err == nil || !strings.Contains(err.Error(), "longitude must be between -180 and 180") {
		t.Fatalf("expected longitude bounds error, got %v", err)
	}

	// Invalid units
	rawUnits := []byte(`
timezone: UTC
display:
  header:
    weather:
      refresh_interval_seconds: 300
      latitude: 37.8
      longitude: -122.2
      units: kelvin
  widgets:
    - id: test
      type: spacer
`)
	_, err = config.ParseWithEnv(rawUnits, mockGetenv(nil))
	if err == nil || !strings.Contains(err.Error(), "invalid header weather units 'kelvin'") {
		t.Fatalf("expected units error, got %v", err)
	}

	// Metric units valid
	rawMetric := []byte(`
timezone: UTC
display:
  header:
    weather:
      refresh_interval_seconds: 300
      latitude: 37.8
      longitude: -122.2
      units: metric
  widgets:
    - id: test
      type: spacer
`)
	cfg, err := config.ParseWithEnv(rawMetric, mockGetenv(nil))
	if err != nil {
		t.Fatalf("unexpected error for metric units: %v", err)
	}
	if cfg.Display.Header.Weather.Units != "metric" {
		t.Errorf("expected units metric, got %s", cfg.Display.Header.Weather.Units)
	}
}

func TestValidate_DisallowedRootHeader(t *testing.T) {
	t.Parallel()

	raw := []byte(`
timezone: UTC
header:
  enabled: true
  elements:
    - clock
    - date
display:
  widgets:
    - id: test
      type: spacer
`)
	_, err := config.ParseWithEnv(raw, mockGetenv(nil))
	if err == nil || !strings.Contains(err.Error(), "configuration contains disallowed top-level key 'header': header must be configured under display.header") {
		t.Fatalf("expected disallowed root header error, got %v", err)
	}
}

func TestValidate_Widgets(t *testing.T) {
	t.Parallel()

	// Missing ID
	rawMissingID := []byte(`
timezone: UTC
display:
  widgets:
    - type: spacer
`)
	_, err := config.ParseWithEnv(rawMissingID, mockGetenv(nil))
	if err == nil || !strings.Contains(err.Error(), "missing required field 'id'") {
		t.Fatalf("expected missing id error, got %v", err)
	}

	// Duplicate ID
	rawDuplicateID := []byte(`
timezone: UTC
display:
  widgets:
    - id: my-widget
      type: spacer
    - id: my-widget
      type: spacer
`)
	_, err = config.ParseWithEnv(rawDuplicateID, mockGetenv(nil))
	if err == nil || !strings.Contains(err.Error(), "duplicate widget id 'my-widget'") {
		t.Fatalf("expected duplicate id error, got %v", err)
	}

	// Missing Type
	rawMissingType := []byte(`
timezone: UTC
display:
  widgets:
    - id: my-widget
`)
	_, err = config.ParseWithEnv(rawMissingType, mockGetenv(nil))
	if err == nil || !strings.Contains(err.Error(), "missing required field 'type'") {
		t.Fatalf("expected missing type error, got %v", err)
	}

	// Invalid dimensions length
	rawDimLen := []byte(`
timezone: UTC
display:
  widgets:
    - id: my-widget
      type: spacer
      dimensions: [4, 2, 1]
`)
	_, err = config.ParseWithEnv(rawDimLen, mockGetenv(nil))
	if err == nil || !strings.Contains(err.Error(), "dimensions must contain exactly 2 integers") {
		t.Fatalf("expected dimensions length error, got %v", err)
	}

	// Dimensions out of bounds (cols > 6)
	rawDimCols := []byte(`
timezone: UTC
display:
  widgets:
    - id: my-widget
      type: spacer
      dimensions: [7, 2]
`)
	_, err = config.ParseWithEnv(rawDimCols, mockGetenv(nil))
	if err == nil || !strings.Contains(err.Error(), "cols must be 1..6") {
		t.Fatalf("expected cols bound error, got %v", err)
	}

	// Dimensions out of bounds (rows > 2)
	rawDimRows := []byte(`
timezone: UTC
display:
  widgets:
    - id: my-widget
      type: spacer
      dimensions: [4, 3]
`)
	_, err = config.ParseWithEnv(rawDimRows, mockGetenv(nil))
	if err == nil || !strings.Contains(err.Error(), "rows must be 1..2") {
		t.Fatalf("expected rows bound error, got %v", err)
	}

	// Dimensions out of bounds (cols < 1)
	rawDimZero := []byte(`
timezone: UTC
display:
  widgets:
    - id: my-widget
      type: spacer
      dimensions: [0, 1]
`)
	_, err = config.ParseWithEnv(rawDimZero, mockGetenv(nil))
	if err == nil || !strings.Contains(err.Error(), "cols must be 1..6") {
		t.Fatalf("expected zero col bound error, got %v", err)
	}

	// Non-positive refresh interval
	rawRefresh := []byte(`
timezone: UTC
display:
  widgets:
    - id: my-widget
      type: spacer
      refresh_interval_seconds: 0
`)
	_, err = config.ParseWithEnv(rawRefresh, mockGetenv(nil))
	if err == nil || !strings.Contains(err.Error(), "refresh_interval_seconds must be positive") {
		t.Fatalf("expected non-positive refresh error, got %v", err)
	}

	// Invalid HTTP method
	rawMethod := []byte(`
timezone: UTC
display:
  widgets:
    - id: my-widget
      type: sensor-card
      endpoint: "http://ha-bridge:8095/data"
      method: "DELETE"
`)
	_, err = config.ParseWithEnv(rawMethod, mockGetenv(nil))
	if err == nil || !strings.Contains(err.Error(), "invalid http method 'DELETE'") {
		t.Fatalf("expected invalid method error, got %v", err)
	}

	// HTTP method normalization and default POST
	rawDefaultMethod := []byte(`
timezone: UTC
display:
  widgets:
    - id: widget-1
      type: sensor-card
      endpoint: "http://ha-bridge:8095/data"
    - id: widget-2
      type: sensor-card
      endpoint: "http://ha-bridge:8095/data"
      method: "get"
`)
	cfg, err := config.ParseWithEnv(rawDefaultMethod, mockGetenv(nil))
	if err != nil {
		t.Fatalf("unexpected error parsing methods: %v", err)
	}
	if cfg.Display.Widgets[0].Method != "POST" {
		t.Errorf("expected default method POST, got %s", cfg.Display.Widgets[0].Method)
	}
	if cfg.Display.Widgets[1].Method != "GET" {
		t.Errorf("expected normalized method GET, got %s", cfg.Display.Widgets[1].Method)
	}
}

func TestResolveEnv_TokenEnv(t *testing.T) {
	t.Parallel()

	raw := []byte(`
timezone: UTC
display:
  widgets:
    - id: living-room-temp
      type: sensor-card
      endpoint: "http://ha-bridge:8095/data"
      token_env: SENSOR_HUD_TOKEN
`)

	// Missing env var
	_, err := config.ParseWithEnv(raw, mockGetenv(nil))
	if err == nil {
		t.Fatal("expected error for unset token_env, got nil")
	}
	expected := "widget 'living-room-temp': environment variable 'SENSOR_HUD_TOKEN' defined in token_env is unset or empty"
	if err.Error() != expected {
		t.Fatalf("expected exact error %q, got %q", expected, err.Error())
	}

	// Present env var
	env := map[string]string{
		"SENSOR_HUD_TOKEN": "secret-token-12345",
	}
	cfg, err := config.ParseWithEnv(raw, mockGetenv(env))
	if err != nil {
		t.Fatalf("unexpected error resolving token_env: %v", err)
	}
	if cfg.Display.Widgets[0].Token != "secret-token-12345" {
		t.Errorf("expected token secret-token-12345, got %s", cfg.Display.Widgets[0].Token)
	}
}

func TestResolveEnv_NestedConfig(t *testing.T) {
	t.Parallel()

	raw := []byte(`
timezone: UTC
display:
  widgets:
    - id: calendar
      type: calendar-agenda
      config:
        calendars:
          - name: "Personal"
            url_env: PERSONAL_CAL_URL
`)

	// Missing nested env var
	_, err := config.ParseWithEnv(raw, mockGetenv(nil))
	if err == nil {
		t.Fatal("expected error for unset url_env, got nil")
	}
	if !strings.Contains(err.Error(), "PERSONAL_CAL_URL") {
		t.Fatalf("expected error mentioning PERSONAL_CAL_URL, got %v", err)
	}

	// Present nested env var
	env := map[string]string{
		"PERSONAL_CAL_URL": "https://calendar.google.com/feed.ics",
	}
	cfg, err := config.ParseWithEnv(raw, mockGetenv(env))
	if err != nil {
		t.Fatalf("unexpected error resolving nested url_env: %v", err)
	}
	cals := cfg.Display.Widgets[0].Config["calendars"].([]any)
	calMap := cals[0].(map[string]any)
	if _, exists := calMap["url"]; exists {
		t.Errorf("expected secret key 'url' NOT to be written into Config map, got %v", calMap["url"])
	}
	if calMap["url_env"] != "PERSONAL_CAL_URL" {
		t.Errorf("expected 'url_env' to remain in Config map, got %v", calMap["url_env"])
	}
	expectedSecret := "https://calendar.google.com/feed.ics"
	if cfg.Display.Widgets[0].Secrets["calendars[0].url"] != expectedSecret {
		t.Errorf("expected secret at 'calendars[0].url' to be %q, got %q", expectedSecret, cfg.Display.Widgets[0].Secrets["calendars[0].url"])
	}

	// Invalid: non-string value for *_env
	rawNonString := []byte(`
timezone: UTC
display:
  widgets:
    - id: calendar
      type: calendar-agenda
      config:
        url_env: 12345
`)
	_, err = config.ParseWithEnv(rawNonString, mockGetenv(nil))
	if err == nil || !strings.Contains(err.Error(), "must specify a non-empty environment variable name") {
		t.Fatalf("expected error for non-string env var name, got %v", err)
	}

	// Invalid: both base key and *_env key specified
	rawBoth := []byte(`
timezone: UTC
display:
  widgets:
    - id: calendar
      type: calendar-agenda
      config:
        url: "http://public.feed"
        url_env: PERSONAL_CAL_URL
`)
	_, err = config.ParseWithEnv(rawBoth, mockGetenv(env))
	if err == nil || !strings.Contains(err.Error(), "cannot specify both 'url' and 'url_env'") {
		t.Fatalf("expected cannot specify both error, got %v", err)
	}
}

func TestConfig_AccessorsAndDefaults(t *testing.T) {
	t.Parallel()

	// Default rotation and header elements
	raw := []byte(`
timezone: America/New_York
display:
  widgets:
    - id: spacer-1
      type: spacer
`)
	cfg, err := config.ParseWithEnv(raw, mockGetenv(nil))
	if err != nil {
		t.Fatalf("unexpected error parsing minimal config: %v", err)
	}

	if cfg.Display.Rotation.GetIntervalSeconds() != 30 {
		t.Errorf("expected default interval 30, got %d", cfg.Display.Rotation.GetIntervalSeconds())
	}
	if cfg.Display.Rotation.GetTransition() != "slide" {
		t.Errorf("expected default transition slide, got %s", cfg.Display.Rotation.GetTransition())
	}
	if !cfg.Display.Rotation.GetPauseOnTouch() {
		t.Error("expected default pause_on_touch true")
	}
	if cfg.Display.Rotation.GetPauseDurationSeconds() != 120 {
		t.Errorf("expected default pause_duration_seconds 120, got %d", cfg.Display.Rotation.GetPauseDurationSeconds())
	}
	if !cfg.Display.Header.IsEnabled() {
		t.Error("expected default header enabled true")
	}
	defaultElements := config.DefaultHeaderElements()
	if len(defaultElements) != 4 || defaultElements[0] != "clock" {
		t.Errorf("unexpected default elements: %v", defaultElements)
	}

	// Custom rotation with interval_seconds: 0 (disabled rotation)
	zero := 0
	pause := false
	pauseDur := 60
	enabled := false
	customRot := config.Config{
		Timezone: "UTC",
		Display: config.DisplayConfig{
			Grid: config.GridConfig{Columns: 6, Rows: 2},
			Rotation: config.RotationConfig{
				IntervalSeconds:      &zero,
				Transition:           "fade",
				PauseOnTouch:         &pause,
				PauseDurationSeconds: &pauseDur,
			},
			Header: config.HeaderConfig{
				Enabled: &enabled,
			},
		},
	}
	if customRot.Display.Rotation.GetIntervalSeconds() != 0 {
		t.Errorf("expected explicit interval 0, got %d", customRot.Display.Rotation.GetIntervalSeconds())
	}
	if customRot.Display.Rotation.GetTransition() != "fade" {
		t.Errorf("expected explicit transition fade, got %s", customRot.Display.Rotation.GetTransition())
	}
	if customRot.Display.Rotation.GetPauseOnTouch() {
		t.Error("expected explicit pause_on_touch false")
	}
	if customRot.Display.Rotation.GetPauseDurationSeconds() != 60 {
		t.Errorf("expected explicit pause_duration_seconds 60, got %d", customRot.Display.Rotation.GetPauseDurationSeconds())
	}
	if customRot.Display.Header.IsEnabled() {
		t.Error("expected explicit header enabled false")
	}
}

func TestConfig_LocationFallback(t *testing.T) {
	t.Parallel()

	// Location when invalid timezone manually injected
	badCfg := config.Config{Timezone: "Invalid/Zone"}
	loc := badCfg.Location()
	if loc.String() != "UTC" {
		t.Errorf("expected fallback to UTC on invalid timezone, got %v", loc)
	}
}

func TestParse_SystemGetenv(t *testing.T) {
	t.Setenv("TEST_WIDGET_TOKEN", "system-token-xyz")

	raw := []byte(`
timezone: UTC
display:
  widgets:
    - id: test-widget
      type: sensor-card
      endpoint: "http://test:8080"
      token_env: TEST_WIDGET_TOKEN
`)
	cfg, err := config.Parse(raw)
	if err != nil {
		t.Fatalf("unexpected error parsing with system env: %v", err)
	}
	if cfg.Display.Widgets[0].Token != "system-token-xyz" {
		t.Errorf("expected token system-token-xyz, got %s", cfg.Display.Widgets[0].Token)
	}
}

func TestLoad_SystemGetenv(t *testing.T) {
	t.Setenv("SYSTEM_CAL_URL", "https://system.calendar.url")

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")

	content := `
timezone: UTC
display:
  widgets:
    - id: test-cal
      type: calendar-agenda
      config:
        url_env: SYSTEM_CAL_URL
`
	if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("unexpected error with Load: %v", err)
	}
	if _, exists := cfg.Display.Widgets[0].Config["url"]; exists {
		t.Errorf("expected 'url' NOT to exist in Config map, got %v", cfg.Display.Widgets[0].Config["url"])
	}
	if cfg.Display.Widgets[0].Secrets["url"] != "https://system.calendar.url" {
		t.Errorf("expected resolved secret in Secrets map, got %v", cfg.Display.Widgets[0].Secrets["url"])
	}
}

func TestResolveEnv_NestedStructuresAndErrors(t *testing.T) {
	t.Parallel()

	// 1. Error in nested map
	rawNestedMapErr := []byte(`
timezone: UTC
display:
  widgets:
    - id: test-widget
      type: tasks
      config:
        nested:
          secret_env: MISSING_SECRET
`)
	_, err := config.ParseWithEnv(rawNestedMapErr, mockGetenv(nil))
	if err == nil || !strings.Contains(err.Error(), "MISSING_SECRET") {
		t.Fatalf("expected error for missing secret in nested map, got %v", err)
	}

	// 2. Error in nested slice of maps
	rawNestedSliceErr := []byte(`
timezone: UTC
display:
  widgets:
    - id: test-widget
      type: tasks
      config:
        items:
          - secret_env: MISSING_IN_SLICE
`)
	_, err = config.ParseWithEnv(rawNestedSliceErr, mockGetenv(nil))
	if err == nil || !strings.Contains(err.Error(), "MISSING_IN_SLICE") {
		t.Fatalf("expected error for missing secret in slice of maps, got %v", err)
	}

	// 3. Nested slice of slices
	rawSliceOfSlices := []byte(`
timezone: UTC
display:
  widgets:
    - id: test-widget
      type: tasks
      config:
        matrix:
          - - secret_env: NESTED_SLICE_SECRET
`)
	_, err = config.ParseWithEnv(rawSliceOfSlices, mockGetenv(nil))
	if err == nil || !strings.Contains(err.Error(), "NESTED_SLICE_SECRET") {
		t.Fatalf("expected error for missing secret in slice of slices, got %v", err)
	}

	// 4. Valid nested map and slice of slices resolution
	env := map[string]string{
		"NESTED_SECRET": "nested-value",
		"MATRIX_SECRET": "matrix-value",
	}
	rawSuccess := []byte(`
timezone: UTC
display:
  widgets:
    - id: test-widget
      type: tasks
      config:
        sub:
          nested_secret_env: NESTED_SECRET
        matrix:
          - - matrix_secret_env: MATRIX_SECRET
`)
	cfg, err := config.ParseWithEnv(rawSuccess, mockGetenv(env))
	if err != nil {
		t.Fatalf("unexpected error resolving nested secrets: %v", err)
	}
	subMap := cfg.Display.Widgets[0].Config["sub"].(map[string]any)
	if _, exists := subMap["nested_secret"]; exists {
		t.Errorf("expected nested_secret NOT to be in Config, got %v", subMap["nested_secret"])
	}
	if cfg.Display.Widgets[0].Secrets["sub.nested_secret"] != "nested-value" {
		t.Errorf("expected Secrets[sub.nested_secret] == 'nested-value', got %q", cfg.Display.Widgets[0].Secrets["sub.nested_secret"])
	}
	if cfg.Display.Widgets[0].Secrets["matrix[0][0].matrix_secret"] != "matrix-value" {
		t.Errorf("expected Secrets[matrix[0][0].matrix_secret] == 'matrix-value', got %q", cfg.Display.Widgets[0].Secrets["matrix[0][0].matrix_secret"])
	}

	// 5. Handling map[any]any
	customCfg := config.Config{
		Timezone: "UTC",
		Display: config.DisplayConfig{
			Grid: config.GridConfig{Columns: 6, Rows: 2},
			Widgets: []config.WidgetConfig{
				{
					ID:   "any-map-widget",
					Type: "custom",
					Config: map[string]any{
						"sub": map[any]any{
							"token_env": "MAP_ANY_TOKEN",
						},
						"slice": []any{
							map[any]any{
								"url_env": "SLICE_ANY_URL",
							},
						},
					},
				},
			},
		},
	}
	anyEnv := map[string]string{
		"MAP_ANY_TOKEN": "token-123",
		"SLICE_ANY_URL": "http://example.org",
	}
	if err := customCfg.ResolveEnv(mockGetenv(anyEnv)); err != nil {
		t.Fatalf("unexpected error resolving map[any]any: %v", err)
	}
	if customCfg.Display.Widgets[0].Secrets["sub.token"] != "token-123" {
		t.Errorf("expected Secrets[sub.token] == 'token-123', got %q", customCfg.Display.Widgets[0].Secrets["sub.token"])
	}
	if customCfg.Display.Widgets[0].Secrets["slice[0].url"] != "http://example.org" {
		t.Errorf("expected Secrets[slice[0].url] == 'http://example.org', got %q", customCfg.Display.Widgets[0].Secrets["slice[0].url"])
	}

	// 6. Error handling in map[any]any
	badAnyCfg := config.Config{
		Timezone: "UTC",
		Display: config.DisplayConfig{
			Grid: config.GridConfig{Columns: 6, Rows: 2},
			Widgets: []config.WidgetConfig{
				{
					ID:   "bad-any-widget",
					Type: "custom",
					Config: map[string]any{
						"sub": map[any]any{
							"token_env": "UNSET_VAR",
						},
					},
				},
			},
		},
	}
	if err := badAnyCfg.ResolveEnv(mockGetenv(nil)); err == nil {
		t.Fatal("expected error resolving unset var in map[any]any, got nil")
	}

	badSliceAnyCfg := config.Config{
		Timezone: "UTC",
		Display: config.DisplayConfig{
			Grid: config.GridConfig{Columns: 6, Rows: 2},
			Widgets: []config.WidgetConfig{
				{
					ID:   "bad-slice-any-widget",
					Type: "custom",
					Config: map[string]any{
						"slice": []any{
							map[any]any{
								"url_env": "UNSET_SLICE_VAR",
							},
						},
					},
				},
			},
		},
	}
	if err := badSliceAnyCfg.ResolveEnv(mockGetenv(nil)); err == nil {
		t.Fatal("expected error resolving unset var in slice map[any]any, got nil")
	}
}

func TestConfig_LocationCached(t *testing.T) {
	t.Parallel()

	raw := []byte(`
timezone: America/Chicago
display:
  widgets:
    - id: test
      type: spacer
`)
	cfg, err := config.ParseWithEnv(raw, mockGetenv(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	loc1 := cfg.Location()
	loc2 := cfg.Location()
	if loc1 != loc2 {
		t.Errorf("expected cached location pointer, got %p and %p", loc1, loc2)
	}
}

func TestHeaderWeatherConfig_NilHelpers(t *testing.T) {
	t.Parallel()

	var hw *config.HeaderWeatherConfig
	if hw.GetRefreshIntervalSeconds() != 900 {
		t.Errorf("expected 900 for nil HeaderWeatherConfig, got %d", hw.GetRefreshIntervalSeconds())
	}

	hwEmpty := &config.HeaderWeatherConfig{}
	if hwEmpty.GetRefreshIntervalSeconds() != 900 {
		t.Errorf("expected 900 for empty HeaderWeatherConfig, got %d", hwEmpty.GetRefreshIntervalSeconds())
	}
}

func TestParseWithEnv_DecodeError(t *testing.T) {
	t.Parallel()

	raw := []byte(`
timezone: UTC
display: "should be a mapping not string"
`)
	_, err := config.ParseWithEnv(raw, mockGetenv(nil))
	if err == nil {
		t.Fatal("expected decode error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to decode configuration") {
		t.Fatalf("expected decode error prefix, got %v", err)
	}
}

func TestParseWithEnv_CommentsOnly(t *testing.T) {
	t.Parallel()

	raw := []byte("# Just a comment\n# Another comment\n")
	_, err := config.ParseWithEnv(raw, mockGetenv(nil))
	if err == nil {
		t.Fatal("expected error parsing comments-only config, got nil")
	}
	if !strings.Contains(err.Error(), "configuration is empty") {
		t.Fatalf("expected empty configuration error, got %v", err)
	}
}

func TestResolveEnv_NilGetenvDefaults(t *testing.T) {
	cfg := &config.Config{
		Display: config.DisplayConfig{
			Widgets: []config.WidgetConfig{
				{
					ID:   "w1",
					Type: "spacer",
				},
			},
		},
	}
	// ResolveEnv with nil should default to os.Getenv without crashing
	if err := cfg.ResolveEnv(nil); err != nil {
		t.Fatalf("unexpected error with nil getenv: %v", err)
	}
}

func TestConfig_LocationUnvalidatedLoad(t *testing.T) {
	t.Parallel()

	unvalCfg := config.Config{Timezone: "America/Denver"}
	loc := unvalCfg.Location()
	if loc.String() != "America/Denver" {
		t.Errorf("expected America/Denver, got %v", loc)
	}
}

func TestRotationConfig_EmptyTransition(t *testing.T) {
	t.Parallel()

	r := config.RotationConfig{}
	if r.GetTransition() != "slide" {
		t.Errorf("expected slide, got %s", r.GetTransition())
	}
}

func TestParseWithEnv_NilGetenv(t *testing.T) {
	raw := []byte(`
timezone: UTC
display:
  widgets:
    - id: test
      type: spacer
`)
	cfg, err := config.ParseWithEnv(raw, nil)
	if err != nil {
		t.Fatalf("unexpected error with nil getenv in ParseWithEnv: %v", err)
	}
	if cfg.Timezone != "UTC" {
		t.Errorf("expected UTC, got %s", cfg.Timezone)
	}
}

func TestHeaderWeatherConfig_OmittedRefreshInterval(t *testing.T) {
	t.Parallel()

	raw := []byte(`
timezone: UTC
display:
  header:
    weather:
      latitude: 37.8
      longitude: -122.2
  widgets:
    - id: test
      type: spacer
`)
	cfg, err := config.ParseWithEnv(raw, mockGetenv(nil))
	if err != nil {
		t.Fatalf("unexpected error with omitted weather interval: %v", err)
	}
	if cfg.Display.Header.Weather.GetRefreshIntervalSeconds() != 900 {
		t.Errorf("expected default weather interval 900, got %d", cfg.Display.Header.Weather.GetRefreshIntervalSeconds())
	}
}


