package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/config"
)

func mockGetenv(vars map[string]string) func(string) string {
	return func(k string) string {
		if vars == nil {
			return ""
		}
		return vars[k]
	}
}

func withBaseYAML(extra string) string {
	return "timezone: America/Los_Angeles\ndisplay:\n  widgets:\n    - id: w1\n      type: spacer\n" + extra
}

func mkWidgetConfig(tokenEnv string, cfg map[string]any) *config.Config {
	return &config.Config{
		Timezone: "UTC",
		Display: config.DisplayConfig{
			Grid: config.GridConfig{Columns: 6, Rows: 2},
			Widgets: []config.WidgetConfig{
				{
					ID:       "w1",
					Type:     "custom",
					TokenEnv: tokenEnv,
					Config:   cfg,
				},
			},
		},
	}
}

func TestLoad_ExampleConfig(t *testing.T) {
	t.Parallel()

	examplePath := filepath.Join("..", "..", "deploy", "examples", "config.yaml")

	// 1. Without FAMILY_CALENDAR_URL set, validation must fail
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

	if cfg.Timezone != "America/Los_Angeles" {
		t.Errorf("expected timezone America/Los_Angeles, got %s", cfg.Timezone)
	}
	if cfg.Location() == nil || cfg.Location().String() != "America/Los_Angeles" {
		t.Errorf("expected location America/Los_Angeles, got %v", cfg.Location())
	}
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

	if len(cfg.Display.Widgets) != 5 {
		t.Fatalf("expected 5 widgets, got %d", len(cfg.Display.Widgets))
	}
	calWidget := cfg.Display.Widgets[0]
	if calWidget.ID != "family-calendar" || (calWidget.Type != "calendar-feed" && calWidget.Type != "calendar-agenda") {
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

func TestParseWithEnv_ValidationErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{name: "empty input", yaml: "", wantErr: "configuration file is empty"},
		{name: "whitespace only", yaml: "   \n\t  \n", wantErr: "configuration file is empty"},
		{name: "comments only", yaml: "# Just a comment\n# Another comment\n", wantErr: "configuration file is empty"},
		{name: "syntax error", yaml: "display: [unclosed", wantErr: "YAML syntax error"},
		{name: "root not mapping", yaml: "- item 1\n- item 2\n", wantErr: "configuration root must be a YAML mapping"},
		{name: "decode error", yaml: "timezone: UTC\ndisplay:\n  rotation: \"scalar-not-mapping\"\n  widgets:\n    - id: w1\n      type: spacer\n", wantErr: "failed to decode configuration"},
		{name: "interpolation prohibited", yaml: "timezone: ${TZ}\ndisplay:\n  widgets:\n    - id: w1\n      type: spacer\n", wantErr: "arbitrary '${...}' string interpolation is prohibited"},
		{name: "disallowed screens top-level", yaml: "screens:\n  - id: s1\n" + withBaseYAML(""), wantErr: "disallowed top-level key 'screens'"},
		{name: "disallowed providers top-level", yaml: "providers:\n  p1: {}\n" + withBaseYAML(""), wantErr: "disallowed top-level key 'providers'"},
		{name: "disallowed header top-level", yaml: "header:\n  enabled: true\n" + withBaseYAML(""), wantErr: "disallowed top-level key 'header'"},
		{name: "unknown top-level key", yaml: "bogus_key: 123\n" + withBaseYAML(""), wantErr: "unknown or disallowed top-level key 'bogus_key'"},
		{name: "disallowed screens under display", yaml: "timezone: UTC\ndisplay:\n  screens: []\n  widgets:\n    - id: w1\n      type: spacer\n", wantErr: "display contains disallowed key 'screens'"},
		{name: "disallowed providers under display", yaml: "timezone: UTC\ndisplay:\n  providers: {}\n  widgets:\n    - id: w1\n      type: spacer\n", wantErr: "display contains disallowed key 'providers'"},
		{name: "unknown display key", yaml: "timezone: UTC\ndisplay:\n  bogus_display: 1\n  widgets:\n    - id: w1\n      type: spacer\n", wantErr: "unknown key 'bogus_display' under display"},
		{name: "missing timezone", yaml: "display:\n  widgets:\n    - id: w1\n      type: spacer\n", wantErr: "missing required configuration field 'timezone'"},
		{name: "invalid timezone", yaml: "timezone: Invalid/Zone_Name\ndisplay:\n  widgets:\n    - id: w1\n      type: spacer\n", wantErr: "invalid timezone 'Invalid/Zone_Name'"},
		{name: "grid columns invalid", yaml: withBaseYAML("  grid:\n    columns: 5\n    rows: 2\n"), wantErr: "grid columns must be 6"},
		{name: "grid rows invalid", yaml: withBaseYAML("  grid:\n    columns: 6\n    rows: 3\n"), wantErr: "grid rows must be 2"},
		{name: "negative rotation interval", yaml: withBaseYAML("  rotation:\n    interval_seconds: -5\n"), wantErr: "rotation interval_seconds must be non-negative"},
		{name: "negative rotation pause", yaml: withBaseYAML("  rotation:\n    pause_duration_seconds: -1\n"), wantErr: "rotation pause_duration_seconds must be non-negative"},
		{name: "invalid rotation transition", yaml: withBaseYAML("  rotation:\n    transition: zoom\n"), wantErr: "invalid rotation transition 'zoom'"},
		{name: "location lat out of bounds", yaml: withBaseYAML("location:\n  latitude: 95.0\n  longitude: -122.2\n"), wantErr: "location latitude must be between -90 and 90"},
		{name: "location lon out of bounds", yaml: withBaseYAML("location:\n  latitude: 37.8\n  longitude: -195.0\n"), wantErr: "location longitude must be between -180 and 180"},
		{name: "theme invalid mode", yaml: withBaseYAML("  theme:\n    mode: cyberpunk\n"), wantErr: "invalid theme mode 'cyberpunk'"},
		{name: "theme auto without coordinates", yaml: withBaseYAML("  theme:\n    mode: auto\n"), wantErr: "theme mode 'auto' requires location coordinates"},
		{name: "header weather non-positive interval", yaml: withBaseYAML("  header:\n    weather:\n      refresh_interval_seconds: 0\n      latitude: 37.0\n      longitude: -122.0\n      units: imperial\n"), wantErr: "header weather refresh_interval_seconds must be positive"},
		{name: "header weather lat out of bounds", yaml: "location:\n  latitude: 0\n  longitude: 0\n" + withBaseYAML("  header:\n    weather:\n      latitude: -95.0\n      longitude: -122.0\n      units: imperial\n"), wantErr: "header weather latitude must be between -90 and 90"},
		{name: "header weather lon out of bounds", yaml: "location:\n  latitude: 0\n  longitude: 0\n" + withBaseYAML("  header:\n    weather:\n      latitude: 37.0\n      longitude: 200.0\n      units: imperial\n"), wantErr: "header weather longitude must be between -180 and 180"},
		{name: "header weather invalid units", yaml: withBaseYAML("  header:\n    weather:\n      latitude: 37.0\n      longitude: -122.0\n      units: kelvin\n"), wantErr: "invalid header weather units 'kelvin'"},
		{name: "widget missing id", yaml: "timezone: UTC\ndisplay:\n  widgets:\n    - type: spacer\n", wantErr: "missing required field 'id'"},
		{name: "widget duplicate id", yaml: "timezone: UTC\ndisplay:\n  widgets:\n    - id: dup\n      type: spacer\n    - id: dup\n      type: spacer\n", wantErr: "duplicate widget id 'dup'"},
		{name: "widget missing type", yaml: "timezone: UTC\ndisplay:\n  widgets:\n    - id: w1\n", wantErr: "widget 'w1' is missing required field 'type'"},
		{name: "widget invalid dimensions count", yaml: "timezone: UTC\ndisplay:\n  widgets:\n    - id: w1\n      type: spacer\n      dimensions: [1, 2, 3]\n", wantErr: "dimensions must contain exactly 2 integers"},
		{name: "widget dimensions cols out of range", yaml: "timezone: UTC\ndisplay:\n  widgets:\n    - id: w1\n      type: spacer\n      dimensions: [7, 1]\n", wantErr: "cols must be 1..6, rows must be 1..2"},
		{name: "widget dimensions rows out of range", yaml: "timezone: UTC\ndisplay:\n  widgets:\n    - id: w1\n      type: spacer\n      dimensions: [1, 3]\n", wantErr: "cols must be 1..6, rows must be 1..2"},
		{name: "widget dimensions zero", yaml: "timezone: UTC\ndisplay:\n  widgets:\n    - id: w1\n      type: spacer\n      dimensions: [0, 1]\n", wantErr: "cols must be 1..6, rows must be 1..2"},
		{name: "widget non-positive refresh", yaml: "timezone: UTC\ndisplay:\n  widgets:\n    - id: w1\n      type: spacer\n      refresh_interval_seconds: 0\n", wantErr: "refresh_interval_seconds must be positive"},
		{name: "widget invalid http method", yaml: "timezone: UTC\ndisplay:\n  widgets:\n    - id: w1\n      type: spacer\n      method: DELETE\n", wantErr: "invalid http method 'DELETE'"},
		{name: "widget invalid endpoint url", yaml: "timezone: UTC\ndisplay:\n  widgets:\n    - id: w1\n      type: spacer\n      endpoint: ftp://example.com/api\n", wantErr: "invalid endpoint URL 'ftp://example.com/api'"},
		{name: "chat_log non-positive ttl", yaml: withBaseYAML("chat_log:\n  ttl_minutes: 0\n"), wantErr: "chat_log ttl_minutes must be positive"},
		{name: "chat_log non-positive max_messages", yaml: withBaseYAML("chat_log:\n  max_messages_per_node: -1\n"), wantErr: "chat_log max_messages_per_node must be positive"},
		{name: "voice_hub brain timeout non-positive", yaml: withBaseYAML("voice_hub:\n  enabled: true\n  brain_timeout_seconds: 0\n"), wantErr: "voice_hub brain_timeout_seconds must be positive"},
		{name: "voice_hub tts timeout non-positive", yaml: withBaseYAML("voice_hub:\n  enabled: true\n  tts_timeout_seconds: -2\n"), wantErr: "voice_hub tts_timeout_seconds must be positive"},
		{name: "fast_path timeout_ms non-positive", yaml: withBaseYAML("voice_hub:\n  enabled: true\n  fast_path:\n    url: http://127.0.0.1:8000/intent\n    timeout_ms: 0\n"), wantErr: "voice_hub fast_path timeout_ms must be positive"},
		{name: "fast_path invalid url", yaml: withBaseYAML("voice_hub:\n  enabled: true\n  fast_path:\n    url: not-a-valid-url\n"), wantErr: "voice_hub fast_path url must be an http(s) URL"},
		{name: "speaker_id missing embed_url", yaml: withBaseYAML("voice_hub:\n  enabled: true\n  speaker_id:\n    fingerprints_path: /path/to/fp.json\n"), wantErr: "voice_hub speaker_id requires both embed_url and fingerprints_path"},
		{name: "speaker_id missing fingerprints_path", yaml: withBaseYAML("voice_hub:\n  enabled: true\n  speaker_id:\n    embed_url: http://127.0.0.1:8080/embed\n"), wantErr: "voice_hub speaker_id requires both embed_url and fingerprints_path"},
		{name: "speaker_id threshold zero", yaml: withBaseYAML("voice_hub:\n  enabled: true\n  speaker_id:\n    embed_url: http://127.0.0.1:8080/embed\n    fingerprints_path: /path/to/fp.json\n    threshold: 0.0\n"), wantErr: "voice_hub speaker_id threshold must be in (0, 1]"},
		{name: "speaker_id threshold greater than 1", yaml: withBaseYAML("voice_hub:\n  enabled: true\n  speaker_id:\n    embed_url: http://127.0.0.1:8080/embed\n    fingerprints_path: /path/to/fp.json\n    threshold: 1.5\n"), wantErr: "voice_hub speaker_id threshold must be in (0, 1]"},
		{name: "speaker_id timeout_seconds non-positive", yaml: withBaseYAML("voice_hub:\n  enabled: true\n  speaker_id:\n    embed_url: http://127.0.0.1:8080/embed\n    fingerprints_path: /path/to/fp.json\n    timeout_seconds: -1\n"), wantErr: "voice_hub speaker_id timeout_seconds must be positive"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := config.ParseWithEnv([]byte(tc.yaml), mockGetenv(nil))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ParseWithEnv() err = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestParseWithEnv_ValidConfigsAndDefaults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                string
		yaml                string
		getenv              func(string) string
		wantTransition      string
		wantWeatherUnits    string
		wantWeatherInterval int
		wantMethod          string
		wantHideCursor      bool
		wantRemoteURL       string
		wantVoiceHub        bool
		wantSpeakerID       bool
		wantFastPath        bool
		wantVideoEnabled    bool
		wantVideoStream     string
		wantChatTTL         time.Duration
		wantChatMax         int
		wantLocName         string
		wantLat             float64
		wantThemeMode       string
		wantForecastLoc     string
	}{
		{
			name:           "comment interpolation allowed and default transition",
			yaml:           "# ${VAR} in comment is allowed\n" + withBaseYAML(""),
			wantTransition: "slide",
		},
		{
			name:           "rotation transition fade",
			yaml:           withBaseYAML("  rotation:\n    transition: fade\n"),
			wantTransition: "fade",
		},
		{
			name:           "rotation transition instant",
			yaml:           withBaseYAML("  rotation:\n    transition: instant\n"),
			wantTransition: "instant",
		},
		{
			name:           "rotation transition none",
			yaml:           withBaseYAML("  rotation:\n    transition: none\n"),
			wantTransition: "none",
		},
		{
			name:                "header weather metric and omitted refresh interval",
			yaml:                withBaseYAML("  header:\n    weather:\n      units: metric\n      latitude: 37.0\n      longitude: -122.0\n"),
			wantWeatherUnits:    "metric",
			wantWeatherInterval: 900,
		},
		{
			name:       "widget http method get normalization",
			yaml:       "timezone: UTC\ndisplay:\n  widgets:\n    - id: w1\n      type: spacer\n      method: get\n",
			wantMethod: "GET",
		},
		{
			name:       "widget default method POST with endpoint",
			yaml:       "timezone: UTC\ndisplay:\n  widgets:\n    - id: w1\n      type: spacer\n      endpoint: http://example.com/api\n",
			wantMethod: "POST",
		},
		{
			name:           "hide_cursor and remote_url",
			yaml:           withBaseYAML("  hide_cursor: true\n  remote_url: http://localhost:8080/display\n"),
			wantHideCursor: true,
			wantRemoteURL:  "http://localhost:8080/display",
		},
		{
			name:           "nil getenv fallback",
			yaml:           withBaseYAML(""),
			getenv:         nil,
			wantTransition: "slide",
		},
		{
			name: "voice hub speaker id and fast path valid",
			yaml: withBaseYAML("voice_hub:\n  enabled: true\n  fast_path:\n    url: http://127.0.0.1:8000/intent\n  speaker_id:\n    embed_url: http://127.0.0.1:8080/embed\n    fingerprints_path: /path/to/fp.json\n"),
			wantVoiceHub:  true,
			wantFastPath:   true,
			wantSpeakerID:  true,
		},
		{
			name:             "header video valid config",
			yaml:             withBaseYAML("  header:\n    video:\n      enabled: true\n      stream_id: frontdoor\n      type: webrtc\n"),
			wantVideoEnabled: true,
			wantVideoStream:  "frontdoor",
		},
		{
			name:        "chat log defaults when omitted",
			yaml:        withBaseYAML(""),
			wantChatTTL: 20 * time.Minute,
			wantChatMax: 50,
		},
		{
			name:        "chat log custom values",
			yaml:        withBaseYAML("chat_log:\n  ttl_minutes: 5\n  max_messages_per_node: 12\n"),
			wantChatTTL: 5 * time.Minute,
			wantChatMax: 12,
		},
		{
			name: "location and theme propagation",
			yaml: `timezone: America/Los_Angeles
location:
  name: Oakland Hills
  latitude: 37.8044
  longitude: -122.2712
display:
  theme:
    mode: auto
  header:
    weather:
      units: imperial
  widgets:
    - id: forecast-1
      type: weather-forecast
`,
			wantLocName:     "Oakland Hills",
			wantLat:         37.8044,
			wantThemeMode:   "auto",
			wantForecastLoc: "Oakland Hills",
		},
		{
			name: "legacy header weather to location propagation",
			yaml: `timezone: America/Los_Angeles
display:
  theme:
    mode: auto
  header:
    weather:
      location: Oakland
      latitude: 37.8
      longitude: -122.2
  widgets:
    - id: w1
      type: spacer
`,
			wantLocName:   "Oakland",
			wantLat:       37.8,
			wantThemeMode: "auto",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			getenv := tc.getenv
			if getenv == nil && tc.name != "nil getenv fallback" {
				getenv = mockGetenv(nil)
			}
			cfg, err := config.ParseWithEnv([]byte(tc.yaml), getenv)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantTransition != "" && cfg.Display.Rotation.GetTransition() != tc.wantTransition {
				t.Errorf("transition = %q, want %q", cfg.Display.Rotation.GetTransition(), tc.wantTransition)
			}
			if tc.wantWeatherUnits != "" && (cfg.Display.Header.Weather == nil || cfg.Display.Header.Weather.Units != tc.wantWeatherUnits) {
				t.Errorf("weather units = %v, want %q", cfg.Display.Header.Weather, tc.wantWeatherUnits)
			}
			if tc.wantWeatherInterval != 0 && (cfg.Display.Header.Weather == nil || cfg.Display.Header.Weather.GetRefreshIntervalSeconds() != tc.wantWeatherInterval) {
				t.Errorf("weather interval = %v, want %d", cfg.Display.Header.Weather, tc.wantWeatherInterval)
			}
			if tc.wantMethod != "" && (len(cfg.Display.Widgets) == 0 || cfg.Display.Widgets[0].Method != tc.wantMethod) {
				t.Errorf("widget method = %v, want %q", cfg.Display.Widgets, tc.wantMethod)
			}
			if tc.wantHideCursor && !cfg.Display.HideCursor {
				t.Errorf("hide_cursor = false, want true")
			}
			if tc.wantRemoteURL != "" && cfg.Display.RemoteURL != tc.wantRemoteURL {
				t.Errorf("remote_url = %q, want %q", cfg.Display.RemoteURL, tc.wantRemoteURL)
			}
			if tc.wantVoiceHub && !cfg.VoiceHub.IsEnabled() {
				t.Errorf("voice_hub is_enabled = false, want true")
			}
			if tc.wantFastPath && (cfg.VoiceHub == nil || !cfg.VoiceHub.FastPath.IsEnabled()) {
				t.Errorf("fast_path is_enabled = false, want true")
			}
			if tc.wantSpeakerID && (cfg.VoiceHub == nil || !cfg.VoiceHub.SpeakerID.IsEnabled()) {
				t.Errorf("speaker_id is_enabled = false, want true")
			}
			if tc.wantVideoEnabled && (cfg.Display.Header.Video == nil || !cfg.Display.Header.Video.IsEnabled()) {
				t.Errorf("header video is_enabled = false, want true")
			}
			if tc.wantVideoStream != "" && (cfg.Display.Header.Video == nil || cfg.Display.Header.Video.GetStreamID() != tc.wantVideoStream) {
				t.Errorf("header video stream = %v, want %q", cfg.Display.Header.Video, tc.wantVideoStream)
			}
			if tc.wantChatTTL != 0 && cfg.ChatLog.GetTTL() != tc.wantChatTTL {
				t.Errorf("chat_log TTL = %v, want %v", cfg.ChatLog.GetTTL(), tc.wantChatTTL)
			}
			if tc.wantChatMax != 0 && cfg.ChatLog.GetMaxMessagesPerNode() != tc.wantChatMax {
				t.Errorf("chat_log max = %v, want %d", cfg.ChatLog.GetMaxMessagesPerNode(), tc.wantChatMax)
			}
			if tc.wantLocName != "" && (cfg.GetLocationConfig() == nil || cfg.GetLocationConfig().Name != tc.wantLocName) {
				t.Errorf("location name = %v, want %q", cfg.GetLocationConfig(), tc.wantLocName)
			}
			if tc.wantLat != 0 && (cfg.GetLocationConfig() == nil || cfg.GetLocationConfig().GetLatitude() != tc.wantLat) {
				t.Errorf("latitude = %v, want %f", cfg.GetLocationConfig(), tc.wantLat)
			}
			if tc.wantThemeMode != "" && (cfg.Display.Theme == nil || cfg.Display.Theme.GetMode() != tc.wantThemeMode) {
				t.Errorf("theme mode = %v, want %q", cfg.Display.Theme, tc.wantThemeMode)
			}
			if tc.wantForecastLoc != "" {
				found := false
				for _, w := range cfg.Display.Widgets {
					if w.Type == "weather-forecast" {
						found = true
						if w.Config["location_name"] != tc.wantForecastLoc {
							t.Errorf("forecast location_name = %v, want %q", w.Config["location_name"], tc.wantForecastLoc)
						}
					}
				}
				if !found {
					t.Errorf("expected weather-forecast widget, none found")
				}
			}
		})
	}
}

func TestResolveEnv(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		cfg          *config.Config
		env          map[string]string
		useNilGetenv bool
		wantToken    string
		wantSecrets  map[string]string
		wantErr      string
	}{
		{
			name:        "token_env valid",
			cfg:         mkWidgetConfig("MY_TOKEN", nil),
			env:         map[string]string{"MY_TOKEN": "secret-123"},
			wantToken:   "secret-123",
			wantSecrets: map[string]string{"token": "secret-123"},
		},
		{
			name:    "token_env unset error",
			cfg:     mkWidgetConfig("UNSET_TOKEN", nil),
			wantErr: "environment variable 'UNSET_TOKEN' defined in token_env is unset or empty",
		},
		{
			name:        "nested map valid",
			cfg:         mkWidgetConfig("", map[string]any{"sub": map[string]any{"secret_env": "SUB_SECRET"}}),
			env:         map[string]string{"SUB_SECRET": "nested-val"},
			wantSecrets: map[string]string{"sub.secret": "nested-val"},
		},
		{
			name:        "nested slice valid",
			cfg:         mkWidgetConfig("", map[string]any{"items": []any{map[string]any{"url_env": "ITEM_URL"}}}),
			env:         map[string]string{"ITEM_URL": "http://api.example.com"},
			wantSecrets: map[string]string{"items[0].url": "http://api.example.com"},
		},
		{
			name:        "nested slice of slices matrix valid",
			cfg:         mkWidgetConfig("", map[string]any{"matrix": []any{[]any{map[string]any{"m_env": "M_VAL"}}}}),
			env:         map[string]string{"M_VAL": "matrix-ok"},
			wantSecrets: map[string]string{"matrix[0][0].m": "matrix-ok"},
		},
		{
			name: "map[any]any in map and slice valid",
			cfg: mkWidgetConfig("", map[string]any{
				"sub":   map[any]any{"token_env": "ANY_TOK"},
				"slice": []any{map[any]any{"url_env": "SLICE_URL"}},
			}),
			env: map[string]string{
				"ANY_TOK":   "token-123",
				"SLICE_URL": "http://example.org",
			},
			wantSecrets: map[string]string{
				"sub.token":    "token-123",
				"slice[0].url": "http://example.org",
			},
		},
		{
			name:    "error unset env in nested map",
			cfg:     mkWidgetConfig("", map[string]any{"sub": map[string]any{"secret_env": "MISSING_VAR"}}),
			wantErr: "environment variable 'MISSING_VAR' defined in secret_env is unset or empty",
		},
		{
			name:    "error non-string *_env",
			cfg:     mkWidgetConfig("", map[string]any{"sub_env": 123}),
			wantErr: "key 'sub_env' must specify a non-empty environment variable name",
		},
		{
			name:    "error conflicting key and key_env",
			cfg:     mkWidgetConfig("", map[string]any{"api_key": "val", "api_key_env": "ENV_VAR"}),
			wantErr: "cannot specify both 'api_key' and 'api_key_env'",
		},
		{
			name:    "error map[any]any unset var",
			cfg:     mkWidgetConfig("", map[string]any{"sub": map[any]any{"token_env": "UNSET_ANY"}}),
			wantErr: "environment variable 'UNSET_ANY' defined in token_env is unset or empty",
		},
		{
			name:    "error slice map[any]any unset var",
			cfg:     mkWidgetConfig("", map[string]any{"slice": []any{map[any]any{"token_env": "UNSET_SLICE"}}}),
			wantErr: "environment variable 'UNSET_SLICE' defined in token_env is unset or empty",
		},
		{
			name:         "ResolveEnv nil getenv defaults to os.Getenv",
			cfg:          mkWidgetConfig("", nil),
			useNilGetenv: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			getenv := mockGetenv(tc.env)
			if tc.useNilGetenv {
				getenv = nil
			}
			err := tc.cfg.ResolveEnv(getenv)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("ResolveEnv() err = %v, want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveEnv() unexpected error: %v", err)
			}
			w := tc.cfg.Display.Widgets[0]
			if tc.wantToken != "" && w.Token != tc.wantToken {
				t.Errorf("Token = %q, want %q", w.Token, tc.wantToken)
			}
			for k, wantV := range tc.wantSecrets {
				if gotV := w.Secrets[k]; gotV != wantV {
					t.Errorf("Secrets[%q] = %q, want %q", k, gotV, wantV)
				}
			}
		})
	}
}

func TestLoad(t *testing.T) {
	// 1. Load("") uses DefaultConfigPath and errors if not present
	if _, err := config.Load(""); err == nil {
		t.Fatal("expected error reading DefaultConfigPath, got nil")
	}

	// 2. LoadWithEnv nonexistent file error
	if _, err := config.LoadWithEnv("/nonexistent/path/never/exists.yaml", os.Getenv); err == nil {
		t.Fatal("expected error loading nonexistent file, got nil")
	}

	// 3. Parse with t.Setenv
	t.Setenv("MIRRORMERE_TEST_TOKEN", "sys-token-val")
	raw := []byte("timezone: UTC\ndisplay:\n  widgets:\n    - id: w1\n      type: spacer\n      token_env: MIRRORMERE_TEST_TOKEN\n")
	cfg, err := config.Parse(raw)
	if err != nil {
		t.Fatalf("Parse() unexpected error: %v", err)
	}
	if cfg.Display.Widgets[0].Token != "sys-token-val" {
		t.Errorf("Token = %q, want sys-token-val", cfg.Display.Widgets[0].Token)
	}
	if cfg.Display.Widgets[0].Secrets["token"] != "sys-token-val" {
		t.Errorf("Secrets[token] = %q, want sys-token-val", cfg.Display.Widgets[0].Secrets["token"])
	}

	// 4. Load from t.TempDir()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if loaded.Timezone != "UTC" {
		t.Errorf("Timezone = %q, want UTC", loaded.Timezone)
	}
}

func TestConfig_Accessors(t *testing.T) {
	t.Parallel()

	// 1. LocationConfig
	var nilLoc *config.LocationConfig
	if nilLoc.GetLatitude() != 0 || nilLoc.GetLongitude() != 0 {
		t.Errorf("expected 0 for nil LocationConfig")
	}
	zeroLoc := &config.LocationConfig{}
	if zeroLoc.GetLatitude() != 0 || zeroLoc.GetLongitude() != 0 {
		t.Errorf("expected 0 for zero LocationConfig")
	}
	lat, lon := 37.8, -122.2
	popLoc := &config.LocationConfig{Latitude: &lat, Longitude: &lon}
	if popLoc.GetLatitude() != 37.8 || popLoc.GetLongitude() != -122.2 {
		t.Errorf("unexpected coords for popLoc")
	}

	// 2. ThemeConfig
	var nilTheme *config.ThemeConfig
	if nilTheme.GetMode() != "day" {
		t.Errorf("expected day for nil ThemeConfig")
	}
	if (&config.ThemeConfig{}).GetMode() != "day" {
		t.Errorf("expected day for zero ThemeConfig")
	}
	if (&config.ThemeConfig{Mode: "   "}).GetMode() != "day" {
		t.Errorf("expected day for whitespace ThemeConfig")
	}
	if (&config.ThemeConfig{Mode: "NIGHT"}).GetMode() != "night" {
		t.Errorf("expected night for upper ThemeConfig")
	}

	// 3. HeaderVideoConfig
	var nilVideo *config.HeaderVideoConfig
	if nilVideo.IsEnabled() || nilVideo.GetStreamID() != "chromecast" || nilVideo.GetType() != "webrtc" {
		t.Errorf("unexpected nil VideoConfig")
	}
	zeroVideo := &config.HeaderVideoConfig{}
	if zeroVideo.IsEnabled() || zeroVideo.GetStreamID() != "chromecast" || zeroVideo.GetType() != "webrtc" {
		t.Errorf("unexpected zero VideoConfig")
	}
	trueVal := true
	popVideo := &config.HeaderVideoConfig{Enabled: &trueVal, StreamID: "cam", Type: "hls"}
	if !popVideo.IsEnabled() || popVideo.GetStreamID() != "cam" || popVideo.GetType() != "hls" {
		t.Errorf("unexpected pop VideoConfig")
	}

	// 4. HeaderWeatherConfig
	var nilWeather *config.HeaderWeatherConfig
	if nilWeather.GetRefreshIntervalSeconds() != 900 {
		t.Errorf("expected 900 for nil HeaderWeatherConfig")
	}
	if (&config.HeaderWeatherConfig{}).GetRefreshIntervalSeconds() != 900 {
		t.Errorf("expected 900 for zero HeaderWeatherConfig")
	}
	ten := 10
	if (&config.HeaderWeatherConfig{RefreshIntervalSeconds: &ten}).GetRefreshIntervalSeconds() != 10 {
		t.Errorf("expected 10 for pop HeaderWeatherConfig")
	}

	// 5. ChatLogConfig
	var nilChat *config.ChatLogConfig
	if nilChat.GetTTL() != 20*time.Minute || nilChat.GetMaxMessagesPerNode() != 50 {
		t.Errorf("unexpected nil ChatLogConfig")
	}
	zeroChat := &config.ChatLogConfig{}
	if zeroChat.GetTTL() != 20*time.Minute || zeroChat.GetMaxMessagesPerNode() != 50 {
		t.Errorf("unexpected zero ChatLogConfig")
	}
	zeroVal := 0
	negVal := -5
	nonPosChat := &config.ChatLogConfig{TTLMinutes: &zeroVal, MaxMessagesPerNode: &negVal}
	if nonPosChat.GetTTL() != 20*time.Minute || nonPosChat.GetMaxMessagesPerNode() != 50 {
		t.Errorf("unexpected nonPos ChatLogConfig")
	}
	five := 5
	popChat := &config.ChatLogConfig{TTLMinutes: &five, MaxMessagesPerNode: &ten}
	if popChat.GetTTL() != 5*time.Minute || popChat.GetMaxMessagesPerNode() != 10 {
		t.Errorf("unexpected pop ChatLogConfig")
	}

	// 6. FastPathConfig
	var nilFP *config.FastPathConfig
	if nilFP.IsEnabled() || nilFP.GetTimeoutMS() != 800 {
		t.Errorf("unexpected nil FastPathConfig")
	}
	zeroFP := &config.FastPathConfig{}
	if zeroFP.IsEnabled() || zeroFP.GetTimeoutMS() != 800 {
		t.Errorf("unexpected zero FastPathConfig")
	}
	nonPosFP := &config.FastPathConfig{URL: "http://intent", TimeoutMS: &negVal}
	if !nonPosFP.IsEnabled() || nonPosFP.GetTimeoutMS() != 800 {
		t.Errorf("unexpected nonPos FastPathConfig")
	}
	msVal := 500
	popFP := &config.FastPathConfig{URL: "http://intent", TimeoutMS: &msVal}
	if !popFP.IsEnabled() || popFP.GetTimeoutMS() != 500 {
		t.Errorf("unexpected pop FastPathConfig")
	}

	// 7. SpeakerIDConfig
	var nilSID *config.SpeakerIDConfig
	if nilSID.IsEnabled() || nilSID.GetThreshold() != 0.70 || nilSID.GetTimeoutSeconds() != 5 {
		t.Errorf("unexpected nil SpeakerIDConfig")
	}
	zeroSID := &config.SpeakerIDConfig{}
	if zeroSID.IsEnabled() || zeroSID.GetThreshold() != 0.70 || zeroSID.GetTimeoutSeconds() != 5 {
		t.Errorf("unexpected zero SpeakerIDConfig")
	}
	nonPosSID := &config.SpeakerIDConfig{TimeoutSeconds: &negVal}
	if nonPosSID.GetTimeoutSeconds() != 5 {
		t.Errorf("unexpected nonPos SpeakerIDConfig")
	}
	thresh := 0.85
	popSID := &config.SpeakerIDConfig{EmbedURL: "http://embed", FingerprintsPath: "/fp.json", Threshold: &thresh, TimeoutSeconds: &ten}
	if !popSID.IsEnabled() || popSID.GetThreshold() != 0.85 || popSID.GetTimeoutSeconds() != 10 {
		t.Errorf("unexpected pop SpeakerIDConfig")
	}

	// 8. VoiceHubConfig
	var nilVH *config.VoiceHubConfig
	if nilVH.IsEnabled() || nilVH.GetBrainTimeoutSeconds() != 30 || nilVH.GetTTSTimeoutSeconds() != 10 || nilVH.GetTTSVoice() != "af_bella" || nilVH.GetTTSModel() != "kokoro" {
		t.Errorf("unexpected nil VoiceHubConfig")
	}
	zeroVH := &config.VoiceHubConfig{}
	if zeroVH.IsEnabled() || zeroVH.GetBrainTimeoutSeconds() != 30 || zeroVH.GetTTSTimeoutSeconds() != 10 || zeroVH.GetTTSVoice() != "af_bella" || zeroVH.GetTTSModel() != "kokoro" {
		t.Errorf("unexpected zero VoiceHubConfig")
	}
	nonPosVH := &config.VoiceHubConfig{BrainTimeoutSeconds: &negVal, TTSTimeoutSeconds: &zeroVal}
	if nonPosVH.GetBrainTimeoutSeconds() != 30 || nonPosVH.GetTTSTimeoutSeconds() != 10 {
		t.Errorf("unexpected nonPos VoiceHubConfig")
	}
	sixty := 60
	twenty := 20
	popVH := &config.VoiceHubConfig{Enabled: true, BrainTimeoutSeconds: &sixty, TTSTimeoutSeconds: &twenty, TTSVoice: "custom_v", TTSModel: "custom_m"}
	if !popVH.IsEnabled() || popVH.GetBrainTimeoutSeconds() != 60 || popVH.GetTTSTimeoutSeconds() != 20 || popVH.GetTTSVoice() != "custom_v" || popVH.GetTTSModel() != "custom_m" {
		t.Errorf("unexpected pop VoiceHubConfig")
	}

	// 9. RotationConfig
	zeroRot := config.RotationConfig{}
	if zeroRot.GetIntervalSeconds() != 30 || zeroRot.GetTransition() != "slide" || !zeroRot.GetPauseOnTouch() || zeroRot.GetPauseDurationSeconds() != 120 {
		t.Errorf("unexpected zero RotationConfig")
	}
	falseVal := false
	popRot := config.RotationConfig{IntervalSeconds: &ten, Transition: "fade", PauseOnTouch: &falseVal, PauseDurationSeconds: &sixty}
	if popRot.GetIntervalSeconds() != 10 || popRot.GetTransition() != "fade" || popRot.GetPauseOnTouch() || popRot.GetPauseDurationSeconds() != 60 {
		t.Errorf("unexpected pop RotationConfig")
	}

	// 10. HeaderConfig
	zeroHdr := &config.HeaderConfig{}
	if !zeroHdr.IsEnabled() {
		t.Errorf("expected zero HeaderConfig to be enabled")
	}
	popHdr := &config.HeaderConfig{Enabled: &falseVal}
	if popHdr.IsEnabled() {
		t.Errorf("expected pop HeaderConfig to be disabled")
	}

	// 11. DefaultHeaderElements
	elems := config.DefaultHeaderElements()
	if len(elems) != 4 || elems[0] != "clock" {
		t.Errorf("unexpected DefaultHeaderElements: %v", elems)
	}

	// 12. (*Config).Location() cached, lazy, UTC fallback
	locCfg := &config.Config{Timezone: "America/Denver"}
	loc1 := locCfg.Location()
	loc2 := locCfg.Location()
	if loc1 != loc2 || loc1.String() != "America/Denver" {
		t.Errorf("unexpected cached Location: %v", loc1)
	}
	invalidLocCfg := &config.Config{Timezone: "Invalid/Zone_Name"}
	if invalidLocCfg.Location() != time.UTC {
		t.Errorf("expected UTC fallback for invalid location, got %v", invalidLocCfg.Location())
	}

	// 13. (*Config).GetLocationConfig()
	var nilCfg *config.Config
	if nilCfg.GetLocationConfig() != nil {
		t.Errorf("expected nil for nil Config.GetLocationConfig()")
	}
	emptyCfg := &config.Config{}
	if emptyCfg.GetLocationConfig() != nil {
		t.Errorf("expected nil for empty Config.GetLocationConfig()")
	}
	directLocCfg := &config.Config{LocationConfig: &config.LocationConfig{Name: "Direct"}}
	if dl := directLocCfg.GetLocationConfig(); dl == nil || dl.Name != "Direct" {
		t.Errorf("expected direct location, got %v", dl)
	}
	weatherFallbackCfg := &config.Config{
		Display: config.DisplayConfig{
			Header: config.HeaderConfig{
				Weather: &config.HeaderWeatherConfig{Location: "San Jose", Latitude: 37.33, Longitude: -121.88},
			},
		},
	}
	if fbLoc := weatherFallbackCfg.GetLocationConfig(); fbLoc == nil || fbLoc.Name != "San Jose" || *fbLoc.Latitude != 37.33 {
		t.Errorf("expected weather fallback location, got %v", fbLoc)
	}
}
