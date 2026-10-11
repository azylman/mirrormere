package solar_test

import (
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/solar"
)

func TestCalculateSunriseSunset_Oakland(t *testing.T) {
	t.Parallel()

	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatalf("failed to load timezone: %v", err)
	}

	// Oct 10, 2026 in Oakland, CA
	date := time.Date(2026, 10, 10, 12, 0, 0, 0, loc)
	lat := 37.8044
	lon := -122.2712

	sunrise, sunset, err := solar.CalculateSunriseSunset(lat, lon, date)
	if err != nil {
		t.Fatalf("unexpected error calculating solar times: %v", err)
	}

	// Expected Oakland sunrise in early October is ~7:10 - 7:20 AM PDT
	// Sunset is ~6:35 - 6:45 PM PDT
	if sunrise.Hour() != 7 || (sunrise.Minute() < 10 || sunrise.Minute() > 20) {
		t.Errorf("expected sunrise between 7:10 AM and 7:20 AM PDT, got %s", sunrise.Format("15:04:05 MST"))
	}
	if sunset.Hour() != 18 || (sunset.Minute() < 35 || sunset.Minute() > 45) {
		t.Errorf("expected sunset between 6:35 PM and 6:45 PM PDT, got %s", sunset.Format("15:04:05 MST"))
	}
}

func TestIsNight(t *testing.T) {
	t.Parallel()

	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatalf("failed to load timezone: %v", err)
	}

	lat := 37.8044
	lon := -122.2712

	// Midday (12:00 PM) -> should be Day (not night)
	midday := time.Date(2026, 10, 10, 12, 0, 0, 0, loc)
	if solar.IsNight(lat, lon, midday) {
		t.Errorf("expected midday to be day, got night")
	}

	// Midnight (12:00 AM) -> should be Night
	midnight := time.Date(2026, 10, 10, 0, 0, 0, 0, loc)
	if !solar.IsNight(lat, lon, midnight) {
		t.Errorf("expected midnight to be night, got day")
	}

	// 10:00 PM -> should be Night
	lateNight := time.Date(2026, 10, 10, 22, 0, 0, 0, loc)
	if !solar.IsNight(lat, lon, lateNight) {
		t.Errorf("expected 10:00 PM to be night, got day")
	}

	// 5:00 AM -> before sunrise -> should be Night
	earlyMorning := time.Date(2026, 10, 10, 5, 0, 0, 0, loc)
	if !solar.IsNight(lat, lon, earlyMorning) {
		t.Errorf("expected 5:00 AM to be night, got day")
	}

	// 8:00 AM -> after sunrise -> should be Day
	afterSunrise := time.Date(2026, 10, 10, 8, 0, 0, 0, loc)
	if solar.IsNight(lat, lon, afterSunrise) {
		t.Errorf("expected 8:00 AM to be day, got night")
	}

	// Sunrise and Sunset exact boundaries
	sunrise, sunset, err := solar.CalculateSunriseSunset(lat, lon, midday)
	if err != nil {
		t.Fatalf("failed to calculate sunrise/sunset: %v", err)
	}

	// 1 minute before sunrise -> night
	if !solar.IsNight(lat, lon, sunrise.Add(-1*time.Minute)) {
		t.Errorf("expected 1 minute before sunrise (%s) to be night", sunrise.Add(-1*time.Minute).Format("15:04"))
	}
	// 1 minute after sunrise -> day
	if solar.IsNight(lat, lon, sunrise.Add(1*time.Minute)) {
		t.Errorf("expected 1 minute after sunrise (%s) to be day", sunrise.Add(1*time.Minute).Format("15:04"))
	}
	// 1 minute before sunset -> day
	if solar.IsNight(lat, lon, sunset.Add(-1*time.Minute)) {
		t.Errorf("expected 1 minute before sunset (%s) to be day", sunset.Add(-1*time.Minute).Format("15:04"))
	}
	// 1 minute after sunset -> night
	if !solar.IsNight(lat, lon, sunset.Add(1*time.Minute)) {
		t.Errorf("expected 1 minute after sunset (%s) to be night", sunset.Add(1*time.Minute).Format("15:04"))
	}
}

func TestCalculateSunriseSunset_SouthernHemisphere(t *testing.T) {
	t.Parallel()

	loc, err := time.LoadLocation("Australia/Sydney")
	if err != nil {
		t.Fatalf("failed to load timezone: %v", err)
	}

	// Sydney in October (Spring in Southern Hemisphere)
	date := time.Date(2026, 10, 10, 12, 0, 0, 0, loc)
	lat := -33.8688
	lon := 151.2093

	sunrise, sunset, err := solar.CalculateSunriseSunset(lat, lon, date)
	if err != nil {
		t.Fatalf("unexpected error calculating solar times: %v", err)
	}

	if sunrise.Hour() < 5 || sunrise.Hour() > 7 {
		t.Errorf("expected Sydney sunrise around 6 AM, got %s", sunrise.Format("15:04 MST"))
	}
	if sunset.Hour() < 18 || sunset.Hour() > 20 {
		t.Errorf("expected Sydney sunset around 7 PM, got %s", sunset.Format("15:04 MST"))
	}
}
