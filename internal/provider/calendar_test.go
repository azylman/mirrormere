package provider_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/provider"
)

func TestCalendarProvider_Init_Validations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		cfg       map[string]any
		opts      provider.InitOptions
		envKey    string
		envVal    string
		wantError string
	}{
		{
			name:      "missing calendars",
			cfg:       map[string]any{},
			wantError: "requires at least one calendar",
		},
		{
			name: "empty calendars list",
			cfg: map[string]any{
				"calendars": []any{},
			},
			wantError: "requires at least one calendar",
		},
		{
			name: "calendar item not a map",
			cfg: map[string]any{
				"calendars": []any{"invalid"},
			},
			wantError: "not a map",
		},
		{
			name: "missing name",
			cfg: map[string]any{
				"calendars": []any{
					map[string]any{"url": "https://example.com/cal.ics"},
				},
			},
			wantError: "requires non-empty 'name'",
		},
		{
			name: "missing url and url_env",
			cfg: map[string]any{
				"calendars": []any{
					map[string]any{"name": "Cal1"},
				},
			},
			wantError: "requires either 'url' or 'url_env'",
		},
		{
			name: "both url and url_env provided",
			cfg: map[string]any{
				"calendars": []any{
					map[string]any{
						"name":    "Cal1",
						"url":     "https://example.com/cal.ics",
						"url_env": "CAL_URL",
					},
				},
			},
			wantError: "cannot specify both 'url' and 'url_env'",
		},
		{
			name: "url_env unset in environment and secrets",
			cfg: map[string]any{
				"calendars": []any{
					map[string]any{
						"name":    "Secret Cal",
						"url_env": "UNSET_CAL_VAR_12345",
					},
				},
			},
			wantError: "environment variable \"UNSET_CAL_VAR_12345\" is not set",
		},
		{
			name: "url_env resolved via secrets",
			cfg: map[string]any{
				"calendars": []any{
					map[string]any{
						"name":    "Secret Cal",
						"url_env": "SECRET_CAL_URL",
					},
				},
			},
			opts: provider.InitOptions{
				Secrets: map[string]string{
					"SECRET_CAL_URL": "https://example.com/secret.ics",
				},
			},
		},
		{
			name: "url_env resolved via structured secret path",
			cfg: map[string]any{
				"calendars": []any{
					map[string]any{
						"name":    "Structured Secret Cal",
						"url_env": "FALLBACK_NAME",
					},
				},
			},
			opts: provider.InitOptions{
				Secrets: map[string]string{
					"calendars[0].url": "https://example.com/structured.ics",
				},
			},
		},
		{
			name: "valid calendars with custom window and view",
			cfg: map[string]any{
				"window_days_past":   2,
				"window_days_future": 21,
				"view":               "week",
				"calendars": []map[string]any{
					{
						"name":    "Work",
						"url":     "https://example.com/work.ics",
						"color":   "#10b981",
						"enabled": true,
					},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := provider.NewCalendarProvider()
			err := p.Init(context.Background(), tc.cfg, tc.opts)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("expected error containing %q, got: %v", tc.wantError, err)
				}
			} else if err != nil {
				t.Fatalf("unexpected init error: %v", err)
			}
		})
	}
}

func TestCalendarProvider_Fetch_Uninitialized(t *testing.T) {
	t.Parallel()

	p := provider.NewCalendarProvider()
	_, err := p.Fetch(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not initialized") {
		t.Fatalf("expected not initialized error, got: %v", err)
	}
}

func TestCalendarProvider_Fetch_SingleAndAllDayEvents(t *testing.T) {
	t.Parallel()

	// Fixed reference instant: 2026-09-25 12:00:00 UTC
	refTime := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

	icsPayload := `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Mirrormere//Test Cal//EN
BEGIN:VEVENT
UID:evt-timed-1@mirrormere
DTSTART:20260925T140000Z
DTEND:20260925T150000Z
SUMMARY:Soccer Practice
LOCATION:Community Park Field 2
DESCRIPTION:Bring water bottle
END:VEVENT
BEGIN:VEVENT
UID:evt-allday-1@mirrormere
DTSTART;VALUE=DATE:20260926
DTEND;VALUE=DATE:20260927
SUMMARY:Trash & Recycling
END:VEVENT
BEGIN:VEVENT
UID:evt-multiday-allday@mirrormere
DTSTART;VALUE=DATE:20260927
DTEND;VALUE=DATE:20260930
SUMMARY:Camping Trip
LOCATION:Yosemite
END:VEVENT
BEGIN:VEVENT
UID:evt-outside-past@mirrormere
DTSTART:20260910T100000Z
DTEND:20260910T110000Z
SUMMARY:Old Meeting
END:VEVENT
BEGIN:VEVENT
UID:evt-duration-only@mirrormere
DTSTART:20260925T160000Z
DURATION:PT1H30M
SUMMARY:Duration Event
END:VEVENT
END:VCALENDAR`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/calendar")
		_, _ = w.Write([]byte(icsPayload))
	}))
	defer server.Close()

	p := provider.NewCalendarProviderWithClient(server.Client(), func() time.Time {
		return refTime
	})

	cfg := map[string]any{
		"window_days_past":   1,
		"window_days_future": 7,
		"calendars": []any{
			map[string]any{
				"name":  "Family Calendar",
				"url":   server.URL,
				"color": "#3b82f6",
			},
		},
	}

	if err := p.Init(context.Background(), cfg, provider.InitOptions{}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	res, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}

	snap, ok := res.(provider.CalendarSnapshot)
	if !ok {
		t.Fatalf("expected CalendarSnapshot, got %T", res)
	}

	if snap.SyncStatus != "ok" {
		t.Errorf("expected sync_status 'ok', got %q", snap.SyncStatus)
	}

	// Should contain: Soccer Practice (timed), Duration Event (timed), Trash & Recycling (all-day), Camping Trip (all-day)
	// Old Meeting should be excluded
	if len(snap.Events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(snap.Events))
	}

	// Verify events are chronologically sorted
	if snap.Events[0].Title != "Soccer Practice" {
		t.Errorf("expected first event 'Soccer Practice', got %q", snap.Events[0].Title)
	}
	if snap.Events[0].AllDay {
		t.Errorf("expected 'Soccer Practice' to not be all-day")
	}
	if snap.Events[0].Location != "Community Park Field 2" {
		t.Errorf("expected location 'Community Park Field 2', got %q", snap.Events[0].Location)
	}

	if snap.Events[1].Title != "Duration Event" {
		t.Errorf("expected second event 'Duration Event', got %q", snap.Events[1].Title)
	}

	// All-day single day check: 2026-09-26
	if snap.Events[2].Title != "Trash & Recycling" {
		t.Errorf("expected third event 'Trash & Recycling', got %q", snap.Events[2].Title)
	}
	if !snap.Events[2].AllDay {
		t.Errorf("expected 'Trash & Recycling' to be all-day")
	}
	if snap.Events[2].Start != "2026-09-26" || snap.Events[2].End != "2026-09-26" {
		t.Errorf("expected 2026-09-26 for start and end, got start=%q, end=%q", snap.Events[2].Start, snap.Events[2].End)
	}

	// Multi-day all-day check: 2026-09-27 to 2026-09-29 inclusive
	if snap.Events[3].Title != "Camping Trip" {
		t.Errorf("expected fourth event 'Camping Trip', got %q", snap.Events[3].Title)
	}
	if snap.Events[3].Start != "2026-09-27" || snap.Events[3].End != "2026-09-29" {
		t.Errorf("expected 2026-09-27 to 2026-09-29, got start=%q, end=%q", snap.Events[3].Start, snap.Events[3].End)
	}
}

func TestCalendarProvider_Fetch_RecurrenceAndExdatesAndOverrides(t *testing.T) {
	t.Parallel()

	// Fixed reference instant: 2026-09-20 00:00:00 UTC (Sunday)
	refTime := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)

	icsPayload := `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Mirrormere//Recurring Cal//EN
BEGIN:VEVENT
UID:daily-standup@mirrormere
DTSTART:20260921T090000Z
DTEND:20260921T093000Z
RRULE:FREQ=DAILY;COUNT=5
EXDATE:20260923T090000Z
SUMMARY:Team Standup
END:VEVENT
BEGIN:VEVENT
UID:daily-standup@mirrormere
RECURRENCE-ID:20260924T090000Z
DTSTART:20260924T100000Z
DTEND:20260924T104500Z
SUMMARY:Team Standup (Delayed & Extended)
LOCATION:Zoom Room A
END:VEVENT
END:VCALENDAR`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/calendar")
		_, _ = w.Write([]byte(icsPayload))
	}))
	defer server.Close()

	p := provider.NewCalendarProviderWithClient(server.Client(), func() time.Time {
		return refTime
	})

	cfg := map[string]any{
		"window_days_past":   1,
		"window_days_future": 7,
		"calendars": []any{
			map[string]any{
				"name": "Work",
				"url":  server.URL,
			},
		},
	}

	if err := p.Init(context.Background(), cfg, provider.InitOptions{}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	res, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}

	snap := res.(provider.CalendarSnapshot)

	// Daily for 5 days: Mon Sep 21, Tue Sep 22, Wed Sep 23 (EXDATE skipped), Thu Sep 24 (RECURRENCE-ID replaced), Fri Sep 25
	// Total occurrences: 4 (Sep 21, Sep 22, Sep 24 modified, Sep 25)
	if len(snap.Events) != 4 {
		t.Fatalf("expected 4 events after exdate and override, got %d: %+v", len(snap.Events), snap.Events)
	}

	// Verify Wednesday Sep 23 is NOT in events
	for _, ev := range snap.Events {
		if strings.Contains(ev.Start, "2026-09-23") {
			t.Fatalf("found excluded event on 2026-09-23: %+v", ev)
		}
	}

	// Verify Thursday Sep 24 is replaced with overridden summary and location
	foundOverride := false
	for _, ev := range snap.Events {
		if strings.Contains(ev.Start, "2026-09-24") {
			foundOverride = true
			if ev.Title != "Team Standup (Delayed & Extended)" {
				t.Errorf("expected overridden title, got %q", ev.Title)
			}
			if ev.Location != "Zoom Room A" {
				t.Errorf("expected location 'Zoom Room A', got %q", ev.Location)
			}
		}
	}
	if !foundOverride {
		t.Errorf("overridden occurrence for Sep 24 was not found")
	}
}

func TestCalendarProvider_Fetch_MultiCalendarMergeAndPartialFailure(t *testing.T) {
	t.Parallel()

	refTime := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

	cal1Payload := `BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:work-1@mirrormere
DTSTART:20260925T170000Z
DTEND:20260925T180000Z
SUMMARY:Work Meeting
END:VEVENT
END:VCALENDAR`

	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/calendar")
		_, _ = w.Write([]byte(cal1Payload))
	}))
	defer srv1.Close()

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer srv2.Close()

	p := provider.NewCalendarProviderWithClient(srv1.Client(), func() time.Time {
		return refTime
	})

	cfg := map[string]any{
		"calendars": []any{
			map[string]any{
				"name":    "Work",
				"url":     srv1.URL,
				"color":   "#10b981",
				"enabled": true,
			},
			map[string]any{
				"name":    "Broken",
				"url":     srv2.URL,
				"color":   "#ef4444",
				"enabled": true,
			},
			map[string]any{
				"name":    "Disabled",
				"url":     "http://invalid.local/disabled.ics",
				"enabled": false,
			},
		},
	}

	if err := p.Init(context.Background(), cfg, provider.InitOptions{}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	res, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}

	snap := res.(provider.CalendarSnapshot)
	if snap.SyncStatus != "partial" {
		t.Errorf("expected 'partial' sync status due to srv2 failure, got %q", snap.SyncStatus)
	}

	if len(snap.Events) != 1 {
		t.Fatalf("expected 1 event from Work calendar, got %d", len(snap.Events))
	}
	if snap.Events[0].Title != "Work Meeting" || snap.Events[0].Color != "#10b981" {
		t.Errorf("unexpected event: %+v", snap.Events[0])
	}
}

func TestCalendarProvider_Fetch_AllCalendarsFail(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	p := provider.NewCalendarProviderWithClient(srv.Client(), nil)

	cfg := map[string]any{
		"calendars": []any{
			map[string]any{
				"name": "Failing",
				"url":  srv.URL,
			},
		},
	}

	if err := p.Init(context.Background(), cfg, provider.InitOptions{}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	_, err := p.Fetch(context.Background())
	if err == nil || !strings.Contains(err.Error(), "all calendar feeds failed") {
		t.Fatalf("expected all calendar feeds failed error, got: %v", err)
	}
}

func TestCalendarProvider_Fetch_WebcalAndInvalidICS(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("NOT A VALID ICS FILE"))
	}))
	defer srv.Close()

	p := provider.NewCalendarProviderWithClient(srv.Client(), nil)

	cfg := map[string]any{
		"calendars": []any{
			map[string]any{
				"name": "Webcal Feed",
				"url":  strings.Replace(srv.URL, "http://", "webcal://", 1),
			},
		},
	}

	if err := p.Init(context.Background(), cfg, provider.InitOptions{}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	_, err := p.Fetch(context.Background())
	if err == nil {
		t.Fatalf("expected error on malformed ics, got nil")
	}
}

func TestCalendarProvider_RegistryAndLifecycle(t *testing.T) {
	t.Parallel()

	reg := provider.NewRegistry()
	if !reg.Has("calendar-agenda") {
		t.Error("expected registry to have 'calendar-agenda'")
	}
	if !reg.Has("calendar") {
		t.Error("expected registry to have 'calendar'")
	}

	p, err := reg.Create("calendar-agenda")
	if err != nil {
		t.Fatalf("failed to create 'calendar-agenda': %v", err)
	}

	if err := p.Subscribe(context.Background(), nil); err != nil {
		t.Errorf("Subscribe returned error: %v", err)
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown returned error: %v", err)
	}
}

func TestCalendarProvider_Fetch_TimezonesAndDurations(t *testing.T) {
	t.Parallel()

	refTime := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

	icsPayload := `BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:tz-event-1@mirrormere
DTSTART;TZID=America/New_York:20260925T090000
DURATION:P1D
SUMMARY:NY Full Day Event
END:VEVENT
BEGIN:VEVENT
UID:tz-event-2@mirrormere
DTSTART:20260925T140000Z
DURATION:PT45S
SUMMARY:Short Event
END:VEVENT
BEGIN:VEVENT
UID:invalid-date-event@mirrormere
DTSTART:INVALID_DATE_VALUE
SUMMARY:Invalid Date
END:VEVENT
BEGIN:VEVENT
SUMMARY:No DTSTART Event
END:VEVENT
BEGIN:VEVENT
UID:very-long-uid-exceeding-thirty-two-characters-with-special-chars-@!#$%
DTSTART:20260925T140000Z
SUMMARY:Same Time Event A
END:VEVENT
BEGIN:VEVENT
UID:same-time-b
DTSTART:20260925T140000Z
SUMMARY:Same Time Event B
END:VEVENT
BEGIN:VEVENT
UID:
DTSTART:20260925T140000Z
SUMMARY:Empty UID Event
END:VEVENT
END:VCALENDAR`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/calendar")
		_, _ = w.Write([]byte(icsPayload))
	}))
	defer srv.Close()

	p := provider.NewCalendarProviderWithClient(srv.Client(), func() time.Time {
		return refTime
	})

	cfg := map[string]any{
		"window_days_past":   float64(2),
		"window_days_future": float64(10),
		"calendars": []any{
			map[string]any{
				"name": "TZ Calendar",
				"url":  srv.URL,
			},
		},
	}

	if err := p.Init(context.Background(), cfg, provider.InitOptions{}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	res, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}

	snap := res.(provider.CalendarSnapshot)
	if len(snap.Events) < 3 {
		t.Fatalf("expected at least 3 valid events, got %d", len(snap.Events))
	}
}

func TestCalendarProvider_Fetch_InvalidRequestURL(t *testing.T) {
	t.Parallel()

	p := provider.NewCalendarProviderWithClient(nil, nil)
	cfg := map[string]any{
		"calendars": []any{
			map[string]any{
				"name": "Bad URL",
				"url":  "http:// invalid url with spaces",
			},
		},
	}

	if err := p.Init(context.Background(), cfg, provider.InitOptions{}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	_, err := p.Fetch(context.Background())
	if err == nil {
		t.Fatalf("expected error on invalid request URL, got nil")
	}
}

func TestCalendarProvider_Fetch_RecurrenceEdgeCases(t *testing.T) {
	t.Parallel()

	refTime := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

	icsPayload := `BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:bad-rrule@mirrormere
DTSTART:20260925T100000Z
RRULE:INVALID_RRULE_SYNTAX
SUMMARY:Bad RRULE
END:VEVENT
BEGIN:VEVENT
UID:recur-duration@mirrormere
DTSTART:20260925T110000Z
DURATION:PT2H
RRULE:FREQ=DAILY;COUNT=2
SUMMARY:Recurring with Duration
END:VEVENT
BEGIN:VEVENT
UID:recur-bad-dtstart@mirrormere
DTSTART:INVALID_TIME
RRULE:FREQ=DAILY;COUNT=2
SUMMARY:Bad DTSTART
END:VEVENT
END:VCALENDAR`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/calendar")
		_, _ = w.Write([]byte(icsPayload))
	}))
	defer srv.Close()

	p := provider.NewCalendarProviderWithClient(srv.Client(), func() time.Time {
		return refTime
	})

	cfg := map[string]any{
		"calendars": []any{
			map[string]any{
				"name": "Edge Recurrence",
				"url":  srv.URL,
			},
		},
	}

	if err := p.Init(context.Background(), cfg, provider.InitOptions{}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	res, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}

	snap := res.(provider.CalendarSnapshot)
	if len(snap.Events) != 2 {
		t.Fatalf("expected 2 recurring occurrences from recur-duration, got %d", len(snap.Events))
	}
}

func TestCalendarProvider_Fetch_SlowlorisSizeLimit(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/calendar")
		chunk := make([]byte, 1024*1024)
		for i := range chunk {
			chunk[i] = 'A'
		}
		_, _ = w.Write(chunk)
		_, _ = w.Write(chunk)
		_, _ = w.Write([]byte("OVERFLOW_BYTES"))
	}))
	defer srv.Close()

	p := provider.NewCalendarProviderWithClient(srv.Client(), nil)
	cfg := map[string]any{
		"calendars": []any{
			map[string]any{
				"name": "Huge Calendar",
				"url":  srv.URL,
			},
		},
	}

	if err := p.Init(context.Background(), cfg, provider.InitOptions{}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	_, err := p.Fetch(context.Background())
	if err == nil || !strings.Contains(err.Error(), "exceeds size limit") {
		t.Fatalf("expected size limit error, got: %v", err)
	}
}

func TestCalendarProvider_Fetch_HighFrequencyRecurrenceIgnored(t *testing.T) {
	t.Parallel()

	refTime := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	icsPayload := `BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:secondly-recur@mirrormere
DTSTART:20260925T100000Z
RRULE:FREQ=SECONDLY;INTERVAL=1
SUMMARY:Secondly Runaway
END:VEVENT
BEGIN:VEVENT
UID:minutely-recur@mirrormere
DTSTART:20260925T100000Z
RRULE:FREQ=MINUTELY;INTERVAL=5
SUMMARY:Minutely Runaway
END:VEVENT
END:VCALENDAR`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/calendar")
		_, _ = w.Write([]byte(icsPayload))
	}))
	defer srv.Close()

	p := provider.NewCalendarProviderWithClient(srv.Client(), func() time.Time {
		return refTime
	})

	cfg := map[string]any{
		"calendars": []any{
			map[string]any{
				"name": "High Frequency",
				"url":  srv.URL,
			},
		},
	}

	if err := p.Init(context.Background(), cfg, provider.InitOptions{}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	res, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}

	snap := res.(provider.CalendarSnapshot)
	if len(snap.Events) != 0 {
		t.Fatalf("expected 0 events because high-frequency RRULEs are ignored, got %d", len(snap.Events))
	}
}



