package provider_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/config"
	"github.com/azylman/mirrormere/internal/events"
	"github.com/azylman/mirrormere/internal/provider"
)

func intPtr(i int) *int {
	return &i
}

func boolPtr(b bool) *bool {
	return &b
}

type mockHeaderSink struct {
	mu      sync.Mutex
	weather *events.HeaderWeather
}

func (s *mockHeaderSink) SetHeaderWeather(weather *events.HeaderWeather) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.weather = weather
}

func (s *mockHeaderSink) GetWeather() *events.HeaderWeather {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.weather
}

func sampleHeaderOpenMeteoJSON() string {
	return `{
		"current": {
			"temperature_2m": 72.4,
			"weather_code": 1
		}
	}`
}

func TestNewHeaderWeatherPoller_Defaults(t *testing.T) {
	t.Parallel()

	poller := provider.NewHeaderWeatherPoller(provider.HeaderWeatherPollerConfig{})
	if poller == nil {
		t.Fatal("expected non-nil poller")
	}

	// Calling Stop before Start is a no-op and should not panic
	poller.Stop()
}

func TestHeaderWeatherPoller_FetchOnce_Imperial(t *testing.T) {
	t.Parallel()

	var receivedQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sampleHeaderOpenMeteoJSON()))
	}))
	defer ts.Close()

	sink := &mockHeaderSink{}
	broadcaster := &mockBroadcaster{}
	fixedTime := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)

	poller := provider.NewHeaderWeatherPoller(provider.HeaderWeatherPollerConfig{
		Client:      ts.Client(),
		BaseURL:     ts.URL,
		Broadcaster: broadcaster,
		StateSink:   sink,
		NowFunc:     func() time.Time { return fixedTime },
	})

	weatherCfg := &config.HeaderWeatherConfig{
		Latitude:               37.8044,
		Longitude:              -122.2712,
		Units:                  "imperial",
		RefreshIntervalSeconds: intPtr(900),
	}

	poller.Start(context.Background(), weatherCfg, "America/Los_Angeles")
	defer poller.Stop()

	// Immediate fetch should have completed
	hw, err := poller.FetchOnce(context.Background())
	if err != nil {
		t.Fatalf("FetchOnce failed: %v", err)
	}

	if hw.Temperature != 72.4 {
		t.Errorf("expected temp 72.4, got %f", hw.Temperature)
	}
	if hw.Units != "F" {
		t.Errorf("expected units 'F', got %q", hw.Units)
	}
	if hw.WeatherCode != 1 {
		t.Errorf("expected weather_code 1, got %d", hw.WeatherCode)
	}
	if hw.Icon != "weather-partly-cloudy" {
		t.Errorf("expected icon 'weather-partly-cloudy', got %q", hw.Icon)
	}

	// Check sink
	saved := sink.GetWeather()
	if saved == nil || saved.Temperature != 72.4 {
		t.Errorf("expected sink to have weather, got %+v", saved)
	}

	// Check broadcast
	eventsList := broadcaster.getEvents()
	if len(eventsList) == 0 {
		t.Fatal("expected broadcaster to receive event")
	}
	lastEvt := eventsList[len(eventsList)-1]
	if lastEvt.Type != events.EventHeaderUpdate {
		t.Errorf("expected event %s, got %s", events.EventHeaderUpdate, lastEvt.Type)
	}

	var data events.HeaderUpdateData
	if err := json.Unmarshal(lastEvt.Data, &data); err != nil {
		t.Fatalf("failed to unmarshal broadcast data: %v", err)
	}
	if data.Timezone != "America/Los_Angeles" {
		t.Errorf("expected timezone America/Los_Angeles, got %s", data.Timezone)
	}
	if data.Weather == nil || data.Weather.Temperature != 72.4 {
		t.Errorf("unexpected broadcast weather: %+v", data.Weather)
	}

	// Check query string
	if !strings.Contains(receivedQuery, "temperature_unit=fahrenheit") {
		t.Errorf("expected temperature_unit=fahrenheit, got %s", receivedQuery)
	}
	if !strings.Contains(receivedQuery, "latitude=37.8044") {
		t.Errorf("expected latitude=37.8044, got %s", receivedQuery)
	}
}

func TestHeaderWeatherPoller_FetchOnce_Metric(t *testing.T) {
	t.Parallel()

	var receivedQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sampleHeaderOpenMeteoJSON()))
	}))
	defer ts.Close()

	poller := provider.NewHeaderWeatherPoller(provider.HeaderWeatherPollerConfig{
		Client:  ts.Client(),
		BaseURL: ts.URL,
	})

	weatherCfg := &config.HeaderWeatherConfig{
		Latitude:               51.5074,
		Longitude:              -0.1278,
		Units:                  "metric",
		RefreshIntervalSeconds: intPtr(900),
	}

	poller.Start(context.Background(), weatherCfg, "Europe/London")
	defer poller.Stop()

	hw, err := poller.FetchOnce(context.Background())
	if err != nil {
		t.Fatalf("FetchOnce failed: %v", err)
	}

	if hw.Units != "C" {
		t.Errorf("expected metric units 'C', got %q", hw.Units)
	}
	if !strings.Contains(receivedQuery, "temperature_unit=celsius") {
		t.Errorf("expected temperature_unit=celsius, got %s", receivedQuery)
	}
}

func TestHeaderWeatherPoller_FetchOnce_Errors(t *testing.T) {
	t.Parallel()

	t.Run("nil config returns nil", func(t *testing.T) {
		t.Parallel()
		poller := provider.NewHeaderWeatherPoller(provider.HeaderWeatherPollerConfig{})
		hw, err := poller.FetchOnce(context.Background())
		if err != nil || hw != nil {
			t.Errorf("expected (nil, nil), got (%v, %v)", hw, err)
		}
	})

	t.Run("HTTP 500 error", func(t *testing.T) {
		t.Parallel()
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("upstream server failure"))
		}))
		defer ts.Close()

		poller := provider.NewHeaderWeatherPoller(provider.HeaderWeatherPollerConfig{
			Client:  ts.Client(),
			BaseURL: ts.URL,
		})

		weatherCfg := &config.HeaderWeatherConfig{
			Latitude:  37.8,
			Longitude: -122.2,
		}
		poller.Start(context.Background(), weatherCfg, "UTC")
		defer poller.Stop()

		_, err := poller.FetchOnce(context.Background())
		if err == nil {
			t.Fatal("expected error on HTTP 500, got nil")
		}
		if !strings.Contains(err.Error(), "HTTP 500") {
			t.Errorf("expected HTTP 500 in error, got %q", err.Error())
		}
	})

	t.Run("malformed JSON", func(t *testing.T) {
		t.Parallel()
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("not valid json"))
		}))
		defer ts.Close()

		poller := provider.NewHeaderWeatherPoller(provider.HeaderWeatherPollerConfig{
			Client:  ts.Client(),
			BaseURL: ts.URL,
		})

		weatherCfg := &config.HeaderWeatherConfig{Latitude: 37.8, Longitude: -122.2}
		poller.Start(context.Background(), weatherCfg, "UTC")
		defer poller.Stop()

		_, err := poller.FetchOnce(context.Background())
		if err == nil {
			t.Fatal("expected error on malformed JSON, got nil")
		}
		if !strings.Contains(err.Error(), "failed to decode header Open-Meteo response") {
			t.Errorf("expected decode error, got %q", err.Error())
		}
	})

	t.Run("invalid baseURL", func(t *testing.T) {
		t.Parallel()
		poller := provider.NewHeaderWeatherPoller(provider.HeaderWeatherPollerConfig{
			BaseURL: "http://invalid-host-%-escape",
		})
		weatherCfg := &config.HeaderWeatherConfig{Latitude: 37.8, Longitude: -122.2}
		poller.Start(context.Background(), weatherCfg, "UTC")
		defer poller.Stop()

		_, err := poller.FetchOnce(context.Background())
		if err == nil {
			t.Fatal("expected error on invalid baseURL, got nil")
		}
		if !strings.Contains(err.Error(), "invalid header weather baseURL") {
			t.Errorf("expected invalid baseURL error, got %q", err.Error())
		}
	})
}

func TestHeaderWeatherPoller_StartStopUpdate(t *testing.T) {
	t.Parallel()

	var fetchCount int
	var mu sync.Mutex
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		fetchCount++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sampleHeaderOpenMeteoJSON()))
	}))
	defer ts.Close()

	poller := provider.NewHeaderWeatherPoller(provider.HeaderWeatherPollerConfig{
		Client:  ts.Client(),
		BaseURL: ts.URL,
	})

	// Start with nil does nothing
	poller.Start(context.Background(), nil, "UTC")

	weatherCfg := &config.HeaderWeatherConfig{
		Latitude:               37.8,
		Longitude:              -122.2,
		RefreshIntervalSeconds: intPtr(1), // 1 second for fast test
	}

	poller.Start(context.Background(), weatherCfg, "America/Los_Angeles")

	// Wait briefly for at least initial fetch
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	count1 := fetchCount
	mu.Unlock()
	if count1 == 0 {
		t.Error("expected at least 1 fetch after start")
	}

	// Update configuration
	newCfg := &config.HeaderWeatherConfig{
		Latitude:               51.5,
		Longitude:              -0.1,
		RefreshIntervalSeconds: intPtr(900),
	}
	poller.UpdateConfig(context.Background(), newCfg, "Europe/London")

	// Stop cleanly
	poller.Stop()

	// Double stop is safe
	poller.Stop()
}

func TestProviderCoordinator_HeaderWeatherPoller_Lifecycle(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sampleHeaderOpenMeteoJSON()))
	}))
	defer ts.Close()

	sink := &mockHeaderSink{}
	broadcaster := &mockBroadcaster{}

	snap := &config.Snapshot{
		Config: &config.Config{
			Timezone: "America/Los_Angeles",
			Display: config.DisplayConfig{
				Header: config.HeaderConfig{
					Enabled: boolPtr(true),
					Weather: &config.HeaderWeatherConfig{
						Latitude:               37.8044,
						Longitude:              -122.2712,
						Units:                  "imperial",
						RefreshIntervalSeconds: intPtr(900),
					},
				},
				Widgets: []config.WidgetConfig{},
			},
		},
	}

	coord := provider.NewCoordinator(provider.CoordinatorConfig{
		Broadcaster:         broadcaster,
		HeaderWeatherSink:   sink,
		HeaderPollerClient:  ts.Client(),
		HeaderPollerBaseURL: ts.URL,
	}, snap)

	hp := coord.HeaderPoller()
	if hp == nil {
		t.Fatal("expected non-nil HeaderPoller on coordinator")
	}

	// Give initial fetch time to execute
	time.Sleep(50 * time.Millisecond)

	saved := sink.GetWeather()
	if saved == nil || saved.Temperature != 72.4 {
		t.Errorf("expected initial header weather in sink, got %+v", saved)
	}

	// UpdateConfig with new weather
	newSnap := &config.Snapshot{
		Config: &config.Config{
			Timezone: "Europe/London",
			Display: config.DisplayConfig{
				Header: config.HeaderConfig{
					Enabled: boolPtr(true),
					Weather: &config.HeaderWeatherConfig{
						Latitude:               51.5074,
						Longitude:              -0.1278,
						Units:                  "metric",
						RefreshIntervalSeconds: intPtr(600),
					},
				},
				Widgets: []config.WidgetConfig{},
			},
		},
	}
	if err := coord.UpdateConfig(newSnap); err != nil {
		t.Fatalf("UpdateConfig failed: %v", err)
	}

	// UpdateConfig removing weather stops the poller
	noWeatherSnap := &config.Snapshot{
		Config: &config.Config{
			Timezone: "UTC",
			Display: config.DisplayConfig{
				Header: config.HeaderConfig{
					Enabled: boolPtr(true),
					Weather: nil,
				},
				Widgets: []config.WidgetConfig{},
			},
		},
	}
	if err := coord.UpdateConfig(noWeatherSnap); err != nil {
		t.Fatalf("UpdateConfig removing weather failed: %v", err)
	}

	// Shutdown coordinator cleanly
	if err := coord.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown failed: %v", err)
	}
}

func TestHeaderWeatherPoller_EmptyTimezoneAndZeroInterval(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sampleHeaderOpenMeteoJSON()))
	}))
	defer ts.Close()

	poller := provider.NewHeaderWeatherPoller(provider.HeaderWeatherPollerConfig{
		Client:  ts.Client(),
		BaseURL: ts.URL,
	})

	// Pass empty timezone and zero RefreshIntervalSeconds
	weatherCfg := &config.HeaderWeatherConfig{
		Latitude:               37.8,
		Longitude:              -122.2,
		RefreshIntervalSeconds: intPtr(0),
	}
	poller.Start(context.Background(), weatherCfg, "")
	defer poller.Stop()

	hw, err := poller.FetchOnce(context.Background())
	if err != nil || hw == nil {
		t.Fatalf("FetchOnce failed: %v", err)
	}
}
