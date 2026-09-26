package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultOpenMeteoBaseURL = "https://api.open-meteo.com/v1/forecast"
	defaultWeatherTimeout   = 10 * time.Second
)

// MapWMOCode maps a WMO 4501 weather code to human-readable condition text and vector icon token.
// Conforms to SPEC-007 §2.
func MapWMOCode(code int) (string, string) {
	switch code {
	case 0:
		return "Clear Sky", "weather-sunny"
	case 1:
		return "Mainly Clear", "weather-partly-cloudy"
	case 2:
		return "Partly Cloudy", "weather-partly-cloudy"
	case 3:
		return "Overcast", "weather-cloudy"
	case 45, 48:
		return "Fog", "weather-fog"
	case 51, 53, 55:
		return "Drizzle", "weather-rainy"
	case 56, 57:
		return "Freezing Drizzle", "weather-snowy-rainy"
	case 61, 63, 65:
		return "Rain", "weather-rainy"
	case 66, 67:
		return "Freezing Rain", "weather-snowy-rainy"
	case 71, 73, 75:
		return "Snow Fall", "weather-snowy"
	case 77:
		return "Snow Grains", "weather-snowy"
	case 80, 81, 82:
		return "Rain Showers", "weather-pouring"
	case 85, 86:
		return "Snow Showers", "weather-snowy"
	case 95:
		return "Thunderstorm", "weather-lightning"
	case 96, 99:
		return "Thunderstorm with Hail", "weather-lightning-rainy"
	default:
		return "Unknown", "weather-cloudy"
	}
}

// WeatherSnapshot matches the normalized internal weather schema in SPEC-007 §2.
type WeatherSnapshot struct {
	Location string          `json:"location,omitempty"`
	Current  WeatherCurrent  `json:"current"`
	Hourly   []WeatherHourly `json:"hourly"`
	Daily    []WeatherDaily  `json:"daily"`
}

// WeatherCurrent encapsulates current ambient observations.
type WeatherCurrent struct {
	Temperature   float64      `json:"temperature"`
	FeelsLike     float64      `json:"feels_like"`
	Humidity      int          `json:"humidity"`
	WindSpeed     float64      `json:"wind_speed"`
	Units         WeatherUnits `json:"units"`
	ConditionCode int          `json:"condition_code"`
	ConditionText string       `json:"condition_text"`
	Icon          string       `json:"icon"`
}

// WeatherUnits represents measurement unit labels.
type WeatherUnits struct {
	Temperature string `json:"temperature"`
	WindSpeed   string `json:"wind_speed"`
}

// WeatherHourly represents hourly forecasted data.
type WeatherHourly struct {
	Time       string  `json:"time"`
	Temp       float64 `json:"temp"`
	PrecipProb int     `json:"precip_prob"`
	Icon       string  `json:"icon"`
}

// WeatherDaily represents daily forecasted summary.
type WeatherDaily struct {
	Date          string  `json:"date"`
	TempMax       float64 `json:"temp_max"`
	TempMin       float64 `json:"temp_min"`
	PrecipProbMax int     `json:"precip_prob_max"`
	ConditionText string  `json:"condition_text"`
	Icon          string  `json:"icon"`
}

type openMeteoResponse struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Timezone  string  `json:"timezone"`
	Current   struct {
		Time                string  `json:"time"`
		Temperature2m       float64 `json:"temperature_2m"`
		RelativeHumidity2m  int     `json:"relative_humidity_2m"`
		ApparentTemperature float64 `json:"apparent_temperature"`
		Precipitation       float64 `json:"precipitation"`
		WeatherCode         int     `json:"weather_code"`
		WindSpeed10m        float64 `json:"wind_speed_10m"`
	} `json:"current"`
	Hourly struct {
		Time                     []string  `json:"time"`
		Temperature2m            []float64 `json:"temperature_2m"`
		PrecipitationProbability []int     `json:"precipitation_probability"`
		WeatherCode              []int     `json:"weather_code"`
	} `json:"hourly"`
	Daily struct {
		Time                        []string  `json:"time"`
		WeatherCode                 []int     `json:"weather_code"`
		Temperature2mMax            []float64 `json:"temperature_2m_max"`
		Temperature2mMin            []float64 `json:"temperature_2m_min"`
		PrecipitationProbabilityMax []int     `json:"precipitation_probability_max"`
	} `json:"daily"`
}

// WeatherProvider ingests weather data from Open-Meteo per SPEC-007 §2.
type WeatherProvider struct {
	client    *http.Client
	baseURL   string
	latitude  float64
	longitude float64
	units     string
	timezone  string
	location  string
	logger    *slog.Logger
}

// NewWeatherProvider constructs a standard WeatherProvider instance.
func NewWeatherProvider() Provider {
	return NewWeatherProviderWithClient(nil, "")
}

// NewWeatherProviderWithClient constructs a WeatherProvider with injected HTTP client and optional base URL override.
func NewWeatherProviderWithClient(client *http.Client, baseURL string) *WeatherProvider {
	if client == nil {
		client = &http.Client{Timeout: defaultWeatherTimeout}
	}
	if baseURL == "" {
		baseURL = defaultOpenMeteoBaseURL
	}
	return &WeatherProvider{
		client:  client,
		baseURL: baseURL,
		units:   "imperial",
		logger:  slog.Default(),
	}
}

// Init initializes the provider with instance domain config and framework options.
func (p *WeatherProvider) Init(ctx context.Context, config map[string]any, opts InitOptions) error {
	latVal, hasLat := parseCoordinate(config["latitude"])
	lonVal, hasLon := parseCoordinate(config["longitude"])

	if !hasLat || !hasLon {
		return errors.New("weather provider requires valid latitude and longitude")
	}

	if latVal < -90 || latVal > 90 {
		return fmt.Errorf("latitude %.4f out of range [-90, 90]", latVal)
	}
	if lonVal < -180 || lonVal > 180 {
		return fmt.Errorf("longitude %.4f out of range [-180, 180]", lonVal)
	}

	units := "imperial"
	if u, ok := config["units"].(string); ok && strings.TrimSpace(u) != "" {
		lower := strings.ToLower(strings.TrimSpace(u))
		if lower == "metric" || lower == "imperial" {
			units = lower
		} else {
			return fmt.Errorf("invalid units %q: must be 'imperial' or 'metric'", u)
		}
	}

	tz := "UTC"
	if strings.TrimSpace(opts.Timezone) != "" {
		tz = strings.TrimSpace(opts.Timezone)
	}

	if opts.Endpoint != "" {
		p.baseURL = opts.Endpoint
	}

	location := ""
	if n, ok := config["name"].(string); ok && strings.TrimSpace(n) != "" {
		location = strings.TrimSpace(n)
	} else if loc, ok := config["location_name"].(string); ok && strings.TrimSpace(loc) != "" {
		location = strings.TrimSpace(loc)
	}

	p.latitude = latVal
	p.longitude = lonVal
	p.units = units
	p.timezone = tz
	p.location = location

	return nil
}

// Fetch queries Open-Meteo and returns normalized WeatherSnapshot domain data.
func (p *WeatherProvider) Fetch(ctx context.Context) (any, error) {
	tempUnit := "fahrenheit"
	windUnit := "mph"
	precipUnit := "inch"
	tempLabel := "°F"
	windLabel := "mph"

	if p.units == "metric" {
		tempUnit = "celsius"
		windUnit = "kmh"
		precipUnit = "mm"
		tempLabel = "°C"
		windLabel = "km/h"
	}

	parsedURL, err := url.Parse(p.baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid weather baseURL: %w", err)
	}

	q := parsedURL.Query()
	q.Set("latitude", fmt.Sprintf("%.4f", p.latitude))
	q.Set("longitude", fmt.Sprintf("%.4f", p.longitude))
	q.Set("current", "temperature_2m,relative_humidity_2m,apparent_temperature,precipitation,weather_code,wind_speed_10m")
	q.Set("hourly", "temperature_2m,precipitation_probability,weather_code")
	q.Set("daily", "weather_code,temperature_2m_max,temperature_2m_min,precipitation_probability_max")
	q.Set("temperature_unit", tempUnit)
	q.Set("wind_speed_unit", windUnit)
	q.Set("precipitation_unit", precipUnit)
	q.Set("timezone", p.timezone)
	parsedURL.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsedURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create weather request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, MaxHTTPResponseBodySize))
	if err != nil {
		return nil, fmt.Errorf("failed to read weather response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}

	var data openMeteoResponse
	if err := json.Unmarshal(bodyBytes, &data); err != nil {
		return nil, fmt.Errorf("failed to decode Open-Meteo response: %w", err)
	}

	condText, iconToken := MapWMOCode(data.Current.WeatherCode)

	snapshot := WeatherSnapshot{
		Location: p.location,
		Current: WeatherCurrent{
			Temperature:   data.Current.Temperature2m,
			FeelsLike:     data.Current.ApparentTemperature,
			Humidity:      data.Current.RelativeHumidity2m,
			WindSpeed:     data.Current.WindSpeed10m,
			Units:         WeatherUnits{Temperature: tempLabel, WindSpeed: windLabel},
			ConditionCode: data.Current.WeatherCode,
			ConditionText: condText,
			Icon:          iconToken,
		},
		Hourly: make([]WeatherHourly, 0, len(data.Hourly.Time)),
		Daily:  make([]WeatherDaily, 0, len(data.Daily.Time)),
	}

	hourlyCount := len(data.Hourly.Time)
	if len(data.Hourly.Temperature2m) < hourlyCount {
		hourlyCount = len(data.Hourly.Temperature2m)
	}
	if len(data.Hourly.WeatherCode) < hourlyCount {
		hourlyCount = len(data.Hourly.WeatherCode)
	}
	if len(data.Hourly.PrecipitationProbability) < hourlyCount {
		hourlyCount = len(data.Hourly.PrecipitationProbability)
	}
	// Limit hourly timeline to next 24 entries per SPEC-007 §2
	if hourlyCount > 24 {
		hourlyCount = 24
	}

	for i := 0; i < hourlyCount; i++ {
		_, hIcon := MapWMOCode(data.Hourly.WeatherCode[i])
		snapshot.Hourly = append(snapshot.Hourly, WeatherHourly{
			Time:       data.Hourly.Time[i],
			Temp:       data.Hourly.Temperature2m[i],
			PrecipProb: data.Hourly.PrecipitationProbability[i],
			Icon:       hIcon,
		})
	}

	dailyCount := len(data.Daily.Time)
	if len(data.Daily.Temperature2mMax) < dailyCount {
		dailyCount = len(data.Daily.Temperature2mMax)
	}
	if len(data.Daily.Temperature2mMin) < dailyCount {
		dailyCount = len(data.Daily.Temperature2mMin)
	}
	if len(data.Daily.WeatherCode) < dailyCount {
		dailyCount = len(data.Daily.WeatherCode)
	}
	if len(data.Daily.PrecipitationProbabilityMax) < dailyCount {
		dailyCount = len(data.Daily.PrecipitationProbabilityMax)
	}
	// Limit daily timeline to 7 days per SPEC-007 §2
	if dailyCount > 7 {
		dailyCount = 7
	}

	for i := 0; i < dailyCount; i++ {
		dText, dIcon := MapWMOCode(data.Daily.WeatherCode[i])
		snapshot.Daily = append(snapshot.Daily, WeatherDaily{
			Date:          data.Daily.Time[i],
			TempMax:       data.Daily.Temperature2mMax[i],
			TempMin:       data.Daily.Temperature2mMin[i],
			PrecipProbMax: data.Daily.PrecipitationProbabilityMax[i],
			ConditionText: dText,
			Icon:          dIcon,
		})
	}

	return snapshot, nil
}

// Subscribe returns nil as Open-Meteo polling does not maintain persistent push connections.
func (p *WeatherProvider) Subscribe(ctx context.Context, eventSink chan<- WidgetPayload) error {
	return nil
}

// Shutdown cleanly stops the provider.
func (p *WeatherProvider) Shutdown(ctx context.Context) error {
	return nil
}

func parseCoordinate(val any) (float64, bool) {
	if val == nil {
		return 0, false
	}
	switch v := val.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err == nil {
			return f, true
		}
	}
	return 0, false
}
