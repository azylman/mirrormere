package provider_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/provider"
)

func TestMapWMOCode(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		code         int
		expectedText string
		expectedIcon string
	}{
		{0, "Clear Sky", "weather-sunny"},
		{1, "Mainly Clear", "weather-partly-cloudy"},
		{2, "Partly Cloudy", "weather-partly-cloudy"},
		{3, "Overcast", "weather-cloudy"},
		{45, "Fog", "weather-fog"},
		{48, "Fog", "weather-fog"},
		{51, "Drizzle", "weather-rainy"},
		{53, "Drizzle", "weather-rainy"},
		{55, "Drizzle", "weather-rainy"},
		{56, "Freezing Drizzle", "weather-snowy-rainy"},
		{57, "Freezing Drizzle", "weather-snowy-rainy"},
		{61, "Rain", "weather-rainy"},
		{63, "Rain", "weather-rainy"},
		{65, "Rain", "weather-rainy"},
		{66, "Freezing Rain", "weather-snowy-rainy"},
		{67, "Freezing Rain", "weather-snowy-rainy"},
		{71, "Snow Fall", "weather-snowy"},
		{73, "Snow Fall", "weather-snowy"},
		{75, "Snow Fall", "weather-snowy"},
		{77, "Snow Grains", "weather-snowy"},
		{80, "Rain Showers", "weather-pouring"},
		{81, "Rain Showers", "weather-pouring"},
		{82, "Rain Showers", "weather-pouring"},
		{85, "Snow Showers", "weather-snowy"},
		{86, "Snow Showers", "weather-snowy"},
		{95, "Thunderstorm", "weather-lightning"},
		{96, "Thunderstorm with Hail", "weather-lightning-rainy"},
		{99, "Thunderstorm with Hail", "weather-lightning-rainy"},
		{999, "Unknown", "weather-cloudy"},
	}

	for _, tc := range testCases {
		text, icon := provider.MapWMOCode(tc.code)
		if text != tc.expectedText || icon != tc.expectedIcon {
			t.Errorf("code %d: expected (%q, %q), got (%q, %q)", tc.code, tc.expectedText, tc.expectedIcon, text, icon)
		}
	}
}

func TestWeatherProvider_Init_Validation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		cfg         map[string]any
		opts        provider.InitOptions
		expectError bool
		errorSubstr string
	}{
		{
			name: "valid imperial with float coordinates and timezone in opts",
			cfg: map[string]any{
				"latitude":  37.8044,
				"longitude": -122.2712,
				"units":     "imperial",
			},
			opts: provider.InitOptions{
				Timezone: "America/Los_Angeles",
			},
			expectError: false,
		},
		{
			name: "valid metric with string coordinates and legacy config timezone ignored",
			cfg: map[string]any{
				"latitude":  "51.5074",
				"longitude": "-0.1278",
				"units":     "metric",
				"timezone":  "America/Chicago",
			},
			opts: provider.InitOptions{
				Timezone: "Europe/London",
			},
			expectError: false,
		},
		{
			name: "valid metric with string coordinates",
			cfg: map[string]any{
				"latitude":  "51.5074",
				"longitude": "-0.1278",
				"units":     "metric",
			},
			expectError: false,
		},
		{
			name: "valid with int coordinates",
			cfg: map[string]any{
				"latitude":  37,
				"longitude": -122,
			},
			expectError: false,
		},
		{
			name: "custom endpoint override in opts",
			cfg: map[string]any{
				"latitude":  37.8044,
				"longitude": -122.2712,
			},
			opts: provider.InitOptions{
				Endpoint: "https://custom.open-meteo.internal/v1/forecast",
			},
			expectError: false,
		},
		{
			name: "missing latitude",
			cfg: map[string]any{
				"longitude": -122.2712,
			},
			expectError: true,
			errorSubstr: "valid latitude and longitude",
		},
		{
			name: "missing longitude",
			cfg: map[string]any{
				"latitude": 37.8044,
			},
			expectError: true,
			errorSubstr: "valid latitude and longitude",
		},
		{
			name: "unparseable string coordinate",
			cfg: map[string]any{
				"latitude":  "not-a-number",
				"longitude": -122.2712,
			},
			expectError: true,
			errorSubstr: "valid latitude and longitude",
		},
		{
			name: "invalid coordinate type",
			cfg: map[string]any{
				"latitude":  []string{"37.8"},
				"longitude": -122.2712,
			},
			expectError: true,
			errorSubstr: "valid latitude and longitude",
		},
		{
			name: "latitude out of bounds high",
			cfg: map[string]any{
				"latitude":  95.0,
				"longitude": -122.2712,
			},
			expectError: true,
			errorSubstr: "latitude 95.0000 out of range",
		},
		{
			name: "latitude out of bounds low",
			cfg: map[string]any{
				"latitude":  -95.0,
				"longitude": -122.2712,
			},
			expectError: true,
			errorSubstr: "latitude -95.0000 out of range",
		},
		{
			name: "longitude out of bounds high",
			cfg: map[string]any{
				"latitude":  37.0,
				"longitude": 185.0,
			},
			expectError: true,
			errorSubstr: "longitude 185.0000 out of range",
		},
		{
			name: "longitude out of bounds low",
			cfg: map[string]any{
				"latitude":  37.0,
				"longitude": -185.0,
			},
			expectError: true,
			errorSubstr: "longitude -185.0000 out of range",
		},
		{
			name: "invalid units string",
			cfg: map[string]any{
				"latitude":  37.0,
				"longitude": -122.0,
				"units":     "kelvin",
			},
			expectError: true,
			errorSubstr: "invalid units \"kelvin\"",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := provider.NewWeatherProvider()
			err := p.Init(context.Background(), tc.cfg, tc.opts)
			if tc.expectError {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.errorSubstr)
				}
				if !strings.Contains(err.Error(), tc.errorSubstr) {
					t.Fatalf("expected error containing %q, got %q", tc.errorSubstr, err.Error())
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

func sampleOpenMeteoJSON() string {
	return `{
		"latitude": 37.8,
		"longitude": -122.27,
		"timezone": "America/Los_Angeles",
		"current": {
			"time": "2026-09-26T12:00",
			"temperature_2m": 68.4,
			"relative_humidity_2m": 58,
			"apparent_temperature": 67.8,
			"precipitation": 0.0,
			"weather_code": 1,
			"wind_speed_10m": 7.2
		},
		"hourly": {
			"time": ["2026-09-26T12:00", "2026-09-26T13:00", "2026-09-26T14:00"],
			"temperature_2m": [68.4, 70.1, 71.5],
			"precipitation_probability": [0, 5, 10],
			"weather_code": [1, 2, 2]
		},
		"daily": {
			"time": ["2026-09-26", "2026-09-27"],
			"weather_code": [1, 2],
			"temperature_2m_max": [72.5, 74.0],
			"temperature_2m_min": [55.0, 56.5],
			"precipitation_probability_max": [10, 20]
		}
	}`
}

func TestWeatherProvider_Fetch_Imperial(t *testing.T) {
	t.Parallel()

	var receivedQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sampleOpenMeteoJSON()))
	}))
	defer ts.Close()

	p := provider.NewWeatherProviderWithClient(ts.Client(), ts.URL)
	err := p.Init(context.Background(), map[string]any{
		"latitude":  37.8044,
		"longitude": -122.2712,
		"units":     "imperial",
	}, provider.InitOptions{
		Timezone: "America/Los_Angeles",
	})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	data, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}

	snap, ok := data.(provider.WeatherSnapshot)
	if !ok {
		t.Fatalf("expected WeatherSnapshot return type, got %T", data)
	}

	// Verify current observations
	if snap.Current.Temperature != 68.4 {
		t.Errorf("expected temp 68.4, got %f", snap.Current.Temperature)
	}
	if snap.Current.FeelsLike != 67.8 {
		t.Errorf("expected feels_like 67.8, got %f", snap.Current.FeelsLike)
	}
	if snap.Current.Humidity != 58 {
		t.Errorf("expected humidity 58, got %d", snap.Current.Humidity)
	}
	if snap.Current.WindSpeed != 7.2 {
		t.Errorf("expected wind_speed 7.2, got %f", snap.Current.WindSpeed)
	}
	if snap.Current.ConditionCode != 1 {
		t.Errorf("expected condition_code 1, got %d", snap.Current.ConditionCode)
	}
	if snap.Current.ConditionText != "Mainly Clear" {
		t.Errorf("expected 'Mainly Clear', got %q", snap.Current.ConditionText)
	}
	if snap.Current.Icon != "weather-partly-cloudy" {
		t.Errorf("expected 'weather-partly-cloudy', got %q", snap.Current.Icon)
	}
	if snap.Current.Units.Temperature != "°F" || snap.Current.Units.WindSpeed != "mph" {
		t.Errorf("unexpected imperial units: %+v", snap.Current.Units)
	}

	// Verify hourly
	if len(snap.Hourly) != 3 {
		t.Fatalf("expected 3 hourly entries, got %d", len(snap.Hourly))
	}
	if snap.Hourly[0].Temp != 68.4 || snap.Hourly[0].PrecipProb != 0 || snap.Hourly[0].Icon != "weather-partly-cloudy" {
		t.Errorf("unexpected hourly[0]: %+v", snap.Hourly[0])
	}

	// Verify daily
	if len(snap.Daily) != 2 {
		t.Fatalf("expected 2 daily entries, got %d", len(snap.Daily))
	}
	if snap.Daily[0].TempMax != 72.5 || snap.Daily[0].TempMin != 55.0 || snap.Daily[0].PrecipProbMax != 10 {
		t.Errorf("unexpected daily[0]: %+v", snap.Daily[0])
	}
	if snap.Daily[0].ConditionText != "Mainly Clear" {
		t.Errorf("unexpected daily[0] condition_text: %q", snap.Daily[0].ConditionText)
	}

	// Verify query params sent to Open-Meteo
	if !strings.Contains(receivedQuery, "temperature_unit=fahrenheit") {
		t.Errorf("expected temperature_unit=fahrenheit in query, got %q", receivedQuery)
	}
	if !strings.Contains(receivedQuery, "wind_speed_unit=mph") {
		t.Errorf("expected wind_speed_unit=mph in query, got %q", receivedQuery)
	}
	if !strings.Contains(receivedQuery, "precipitation_unit=inch") {
		t.Errorf("expected precipitation_unit=inch in query, got %q", receivedQuery)
	}
	if !strings.Contains(receivedQuery, "timezone=America%2FLos_Angeles") {
		t.Errorf("expected timezone=America%%2FLos_Angeles in query, got %q", receivedQuery)
	}
}

func TestWeatherProvider_Fetch_Metric(t *testing.T) {
	t.Parallel()

	var receivedQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sampleOpenMeteoJSON()))
	}))
	defer ts.Close()

	p := provider.NewWeatherProviderWithClient(ts.Client(), ts.URL)
	err := p.Init(context.Background(), map[string]any{
		"latitude":  51.5074,
		"longitude": -0.1278,
		"units":     "metric",
	}, provider.InitOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	data, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}

	snap := data.(provider.WeatherSnapshot)
	if snap.Current.Units.Temperature != "°C" || snap.Current.Units.WindSpeed != "km/h" {
		t.Errorf("unexpected metric units: %+v", snap.Current.Units)
	}

	if !strings.Contains(receivedQuery, "temperature_unit=celsius") {
		t.Errorf("expected temperature_unit=celsius in query, got %q", receivedQuery)
	}
	if !strings.Contains(receivedQuery, "wind_speed_unit=kmh") {
		t.Errorf("expected wind_speed_unit=kmh in query, got %q", receivedQuery)
	}
	if !strings.Contains(receivedQuery, "precipitation_unit=mm") {
		t.Errorf("expected precipitation_unit=mm in query, got %q", receivedQuery)
	}
	if !strings.Contains(receivedQuery, "timezone=UTC") {
		t.Errorf("expected default timezone=UTC in query, got %q", receivedQuery)
	}
}

func TestWeatherProvider_Fetch_Errors(t *testing.T) {
	t.Parallel()

	t.Run("HTTP 500 error", func(t *testing.T) {
		t.Parallel()
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("internal upstream failure"))
		}))
		defer ts.Close()

		p := provider.NewWeatherProviderWithClient(ts.Client(), ts.URL)
		_ = p.Init(context.Background(), map[string]any{"latitude": 37.0, "longitude": -122.0}, provider.InitOptions{})

		_, err := p.Fetch(context.Background())
		if err == nil {
			t.Fatal("expected error on HTTP 500, got nil")
		}
		if !strings.Contains(err.Error(), "HTTP 500") {
			t.Errorf("expected HTTP 500 in error, got %q", err.Error())
		}
	})

	t.Run("Malformed JSON error", func(t *testing.T) {
		t.Parallel()
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("not valid json"))
		}))
		defer ts.Close()

		p := provider.NewWeatherProviderWithClient(ts.Client(), ts.URL)
		_ = p.Init(context.Background(), map[string]any{"latitude": 37.0, "longitude": -122.0}, provider.InitOptions{})

		_, err := p.Fetch(context.Background())
		if err == nil {
			t.Fatal("expected error on malformed JSON, got nil")
		}
		if !strings.Contains(err.Error(), "failed to decode Open-Meteo response") {
			t.Errorf("expected decode error, got %q", err.Error())
		}
	})

	t.Run("Slowloris 1MB body limit exceeded", func(t *testing.T) {
		t.Parallel()
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			// Stream 2MB of padding
			padding := strings.Repeat("A", 1024*1024*2)
			_, _ = fmt.Fprintf(w, `{"current":{"temperature_2m": 60},"padding":"%s"}`, padding)
		}))
		defer ts.Close()

		p := provider.NewWeatherProviderWithClient(ts.Client(), ts.URL)
		_ = p.Init(context.Background(), map[string]any{"latitude": 37.0, "longitude": -122.0}, provider.InitOptions{})

		// When reading is capped at 1MB, the JSON will be truncated and unmarshal will fail
		_, err := p.Fetch(context.Background())
		if err == nil {
			t.Fatal("expected error on truncated oversized payload, got nil")
		}
	})

	t.Run("Invalid baseURL error", func(t *testing.T) {
		t.Parallel()
		p := provider.NewWeatherProviderWithClient(nil, "http://invalid-host-%-escape")
		_ = p.Init(context.Background(), map[string]any{"latitude": 37.0, "longitude": -122.0}, provider.InitOptions{})

		_, err := p.Fetch(context.Background())
		if err == nil {
			t.Fatal("expected error on invalid baseURL, got nil")
		}
		if !strings.Contains(err.Error(), "invalid weather baseURL") {
			t.Errorf("expected invalid baseURL error, got %q", err.Error())
		}
	})

	t.Run("Context cancelled", func(t *testing.T) {
		t.Parallel()
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(100 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		}))
		defer ts.Close()

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		p := provider.NewWeatherProviderWithClient(ts.Client(), ts.URL)
		_ = p.Init(context.Background(), map[string]any{"latitude": 37.0, "longitude": -122.0}, provider.InitOptions{})

		_, err := p.Fetch(ctx)
		if err == nil {
			t.Fatal("expected error on cancelled context, got nil")
		}
	})
}

func TestWeatherProvider_SubscribeAndShutdown(t *testing.T) {
	t.Parallel()

	p := provider.NewWeatherProvider()
	ch := make(chan provider.WidgetPayload, 1)

	// Subscribe on polling provider returns nil without errors
	if err := p.Subscribe(context.Background(), ch); err != nil {
		t.Fatalf("unexpected error on Subscribe: %v", err)
	}

	// Shutdown on polling provider returns nil without errors
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatalf("unexpected error on Shutdown: %v", err)
	}
}

func TestWeatherProvider_DefaultRegistry(t *testing.T) {
	t.Parallel()

	reg := provider.NewRegistry()
	if !reg.Has("weather-forecast") {
		t.Error("expected registry to have 'weather-forecast'")
	}
	if !reg.Has("weather") {
		t.Error("expected registry to have 'weather'")
	}

	p1, err := reg.Create("weather-forecast")
	if err != nil || p1 == nil {
		t.Fatalf("failed to create 'weather-forecast': %v", err)
	}

	p2, err := reg.Create("weather")
	if err != nil || p2 == nil {
		t.Fatalf("failed to create 'weather': %v", err)
	}
}

func TestWeatherSnapshot_JSONMarshal(t *testing.T) {
	t.Parallel()

	snap := provider.WeatherSnapshot{
		Current: provider.WeatherCurrent{
			Temperature:   72.4,
			FeelsLike:     70.5,
			Humidity:      45,
			WindSpeed:     5.5,
			Units:         provider.WeatherUnits{Temperature: "°F", WindSpeed: "mph"},
			ConditionCode: 0,
			ConditionText: "Clear Sky",
			Icon:          "weather-sunny",
		},
		Hourly: []provider.WeatherHourly{
			{Time: "2026-09-26T12:00", Temp: 72.4, PrecipProb: 0, Icon: "weather-sunny"},
		},
		Daily: []provider.WeatherDaily{
			{Date: "2026-09-26", TempMax: 75.0, TempMin: 55.0, PrecipProbMax: 0, ConditionText: "Clear Sky", Icon: "weather-sunny"},
		},
	}

	b, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("failed to marshal WeatherSnapshot: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatalf("failed to unmarshal WeatherSnapshot JSON: %v", err)
	}

	current, ok := parsed["current"].(map[string]any)
	if !ok || current["temperature"] != 72.4 {
		t.Errorf("unexpected current in JSON: %+v", current)
	}
}

func TestWeatherProvider_Init_CoordinateTypes(t *testing.T) {
	t.Parallel()

	p := provider.NewWeatherProvider()
	err := p.Init(context.Background(), map[string]any{
		"latitude":  float32(37.5),
		"longitude": int64(-122),
	}, provider.InitOptions{})
	if err != nil {
		t.Fatalf("Init failed with float32 and int64 coordinates: %v", err)
	}
}

func TestWeatherProvider_Fetch_HourlyAndDailyLimits(t *testing.T) {
	t.Parallel()

	// Build JSON with 30 hourly items and 10 daily items, plus mismatched slices
	hours := make([]string, 30)
	temps := make([]float64, 28) // Mismatched length (28 < 30)
	probs := make([]int, 27)     // Mismatched length (27 < 28)
	codes := make([]int, 26)     // Mismatched length (26 < 27)
	for i := 0; i < 30; i++ {
		hours[i] = fmt.Sprintf("2026-09-26T%02d:00", i)
		if i < 28 {
			temps[i] = 65.0 + float64(i)
		}
		if i < 27 {
			probs[i] = i
		}
		if i < 26 {
			codes[i] = 1
		}
	}

	days := make([]string, 10)
	dMax := make([]float64, 9) // Mismatched
	dMin := make([]float64, 8) // Mismatched
	dCodes := make([]int, 8)
	dProbs := make([]int, 8)
	for i := 0; i < 10; i++ {
		days[i] = fmt.Sprintf("2026-09-%02d", i+1)
		if i < 9 {
			dMax[i] = 70.0 + float64(i)
		}
		if i < 8 {
			dMin[i] = 50.0 + float64(i)
			dCodes[i] = 0
			dProbs[i] = 5
		}
	}

	jsonMap := map[string]any{
		"current": map[string]any{
			"temperature_2m":       70.0,
			"relative_humidity_2m": 50,
			"apparent_temperature": 69.0,
			"precipitation":        0.0,
			"weather_code":         0,
			"wind_speed_10m":       5.0,
		},
		"hourly": map[string]any{
			"time":                      hours,
			"temperature_2m":            temps,
			"precipitation_probability": probs,
			"weather_code":              codes,
		},
		"daily": map[string]any{
			"time":                         days,
			"temperature_2m_max":           dMax,
			"temperature_2m_min":           dMin,
			"weather_code":                 dCodes,
			"precipitation_probability_max": dProbs,
		},
	}

	payload, err := json.Marshal(jsonMap)
	if err != nil {
		t.Fatalf("failed to marshal test json: %v", err)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	defer ts.Close()

	p := provider.NewWeatherProviderWithClient(ts.Client(), ts.URL)
	_ = p.Init(context.Background(), map[string]any{
		"latitude":  37.0,
		"longitude": -122.0,
	}, provider.InitOptions{})

	res, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}

	snap := res.(provider.WeatherSnapshot)
	// Hourly should be capped at min(26, 24) = 24
	if len(snap.Hourly) != 24 {
		t.Errorf("expected 24 hourly items, got %d", len(snap.Hourly))
	}

	// Daily should be capped at min(8, 7) = 7
	if len(snap.Daily) != 7 {
		t.Errorf("expected 7 daily items, got %d", len(snap.Daily))
	}
}

func TestWeatherProvider_Init_LocationName(t *testing.T) {
	t.Parallel()

	jsonMap := map[string]any{
		"latitude":  37.83,
		"longitude": -122.28,
		"current": map[string]any{
			"temperature_2m": 68.0,
			"weather_code":   1,
		},
		"hourly": map[string]any{
			"time":           []string{},
			"temperature_2m": []float64{},
		},
		"daily": map[string]any{
			"time":         []string{},
			"weather_code": []int{},
		},
	}
	payload, _ := json.Marshal(jsonMap)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	defer ts.Close()

	// 1. With "name"
	p1 := provider.NewWeatherProviderWithClient(ts.Client(), ts.URL)
	if err := p1.Init(context.Background(), map[string]any{
		"latitude":  37.83,
		"longitude": -122.28,
		"name":      "Home",
	}, provider.InitOptions{}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	res1, err := p1.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}
	snap1 := res1.(provider.WeatherSnapshot)
	if snap1.Location != "Home" {
		t.Errorf("expected Location 'Home', got %q", snap1.Location)
	}

	// 2. With "location_name"
	p2 := provider.NewWeatherProviderWithClient(ts.Client(), ts.URL)
	if err := p2.Init(context.Background(), map[string]any{
		"latitude":      37.83,
		"longitude":     -122.28,
		"location_name": "Ayda's School",
	}, provider.InitOptions{}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	res2, err := p2.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}
	snap2 := res2.(provider.WeatherSnapshot)
	if snap2.Location != "Ayda's School" {
		t.Errorf("expected Location 'Ayda's School', got %q", snap2.Location)
	}

	// 3. JSON marshal with location
	b, err := json.Marshal(snap1)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	if parsed["location"] != "Home" {
		t.Errorf("expected location 'Home' in JSON, got %v", parsed["location"])
	}
}

func TestWeatherProvider_Fetch_CurrentHourAlignment(t *testing.T) {
	t.Parallel()

	// 48 hours of forecast
	hours := make([]string, 48)
	temps := make([]float64, 48)
	probs := make([]int, 48)
	codes := make([]int, 48)
	for i := 0; i < 48; i++ {
		hours[i] = fmt.Sprintf("2026-09-26T%02d:00", i)
		temps[i] = 60.0 + float64(i)
		probs[i] = i
		codes[i] = 1
	}

	// 1. Current.Time at 14:00 -> should return 24 entries starting at 14:00
	json1 := map[string]any{
		"current": map[string]any{
			"time":           "2026-09-26T14:15",
			"temperature_2m": 72.0,
			"weather_code":   1,
		},
		"hourly": map[string]any{
			"time":                      hours,
			"temperature_2m":            temps,
			"precipitation_probability": probs,
			"weather_code":              codes,
		},
	}
	payload1, _ := json.Marshal(json1)

	ts1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload1)
	}))
	defer ts1.Close()

	p1 := provider.NewWeatherProviderWithClient(ts1.Client(), ts1.URL)
	_ = p1.Init(context.Background(), map[string]any{"latitude": 37.0, "longitude": -122.0}, provider.InitOptions{})
	res1, err := p1.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}
	snap1 := res1.(provider.WeatherSnapshot)
	if len(snap1.Hourly) != 24 {
		t.Fatalf("expected 24 hourly entries, got %d", len(snap1.Hourly))
	}
	if snap1.Hourly[0].Time != "2026-09-26T14:00" {
		t.Errorf("expected first hourly item to be 2026-09-26T14:00, got %s", snap1.Hourly[0].Time)
	}

	// 2. Current.Time near end (40:00) with 48 items -> should safely return 8 items
	json2 := map[string]any{
		"current": map[string]any{
			"time":           "2026-09-26T40:00",
			"temperature_2m": 75.0,
			"weather_code":   1,
		},
		"hourly": map[string]any{
			"time":                      hours,
			"temperature_2m":            temps,
			"precipitation_probability": probs,
			"weather_code":              codes,
		},
	}
	payload2, _ := json.Marshal(json2)

	ts2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload2)
	}))
	defer ts2.Close()

	p2 := provider.NewWeatherProviderWithClient(ts2.Client(), ts2.URL)
	_ = p2.Init(context.Background(), map[string]any{"latitude": 37.0, "longitude": -122.0}, provider.InitOptions{})
	res2, err := p2.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}
	snap2 := res2.(provider.WeatherSnapshot)
	if len(snap2.Hourly) != 8 {
		t.Errorf("expected 8 hourly items near end, got %d", len(snap2.Hourly))
	}

	// 3. Short / unparseable Current.Time -> defaults to 0 without panic
	json3 := map[string]any{
		"current": map[string]any{
			"time":           "bad",
			"temperature_2m": 65.0,
			"weather_code":   0,
		},
		"hourly": map[string]any{
			"time":                      hours[:10],
			"temperature_2m":            temps[:10],
			"precipitation_probability": probs[:10],
			"weather_code":              codes[:10],
		},
	}
	payload3, _ := json.Marshal(json3)

	ts3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload3)
	}))
	defer ts3.Close()

	p3 := provider.NewWeatherProviderWithClient(ts3.Client(), ts3.URL)
	_ = p3.Init(context.Background(), map[string]any{"latitude": 37.0, "longitude": -122.0}, provider.InitOptions{})
	res3, err := p3.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}
	snap3 := res3.(provider.WeatherSnapshot)
	if len(snap3.Hourly) != 10 {
		t.Errorf("expected 10 hourly items for short time fallback, got %d", len(snap3.Hourly))
	}
}
