package provider_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/provider"
)

func TestCalDAV_REPORT_DirectCollection(t *testing.T) {
	t.Parallel()

	refTime := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	windowStart := refTime.AddDate(0, 0, -1)
	windowEnd := refTime.AddDate(0, 0, 14)

	calData1 := `BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:evt-1@mirrormere
DTSTART:20260925T140000Z
DTEND:20260925T150000Z
SUMMARY:CalDAV Timed Event
LOCATION:Conference Room A
END:VEVENT
END:VCALENDAR`

	calData2 := `BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:evt-2@mirrormere
DTSTART:20260926T100000Z
DTEND:20260926T110000Z
RRULE:FREQ=DAILY;COUNT=3
SUMMARY:CalDAV Recurring Standup
END:VEVENT
END:VCALENDAR`

	reportResponseXML := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8" ?>
<d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
  <d:response>
    <d:href>/caldav/calendars/user/events/evt1.ics</d:href>
    <d:propstat>
      <d:prop>
        <d:getetag>"tag1"</d:getetag>
        <c:calendar-data>%s</c:calendar-data>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
  <d:response>
    <d:href>/caldav/calendars/user/events/evt2.ics</d:href>
    <d:propstat>
      <d:prop>
        <d:getetag>"tag2"</d:getetag>
        <c:calendar-data>%s</c:calendar-data>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
</d:multistatus>`, calData1, calData2)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "PROPFIND":
			// Fast-path: declare this endpoint is a calendar collection
			w.Header().Set("Content-Type", "application/xml; charset=utf-8")
			w.WriteHeader(http.StatusMultiStatus)
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8" ?>
<d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
  <d:response>
    <d:href>/caldav/calendars/user/</d:href>
    <d:propstat>
      <d:prop>
        <d:resourcetype>
          <d:collection/>
          <c:calendar/>
        </d:resourcetype>
        <d:displayname>Test Calendar</d:displayname>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
</d:multistatus>`))
		case "REPORT":
			// Verify CalDAV REPORT protocol invariants
			if r.Header.Get("Depth") != "1" {
				t.Errorf("expected Depth: 1, got %q", r.Header.Get("Depth"))
			}
			user, pass, ok := r.BasicAuth()
			if !ok || user != "caluser" || pass != "calpass" {
				t.Errorf("expected Basic Auth caluser:calpass, got ok=%v user=%q", ok, user)
			}
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "calendar-query") {
				t.Errorf("expected calendar-query in body, got: %s", string(body))
			}
			// Verify UTC format: YYYYMMDD'T'HHMMSS'Z'
			if !strings.Contains(string(body), "20260924T120000Z") {
				t.Errorf("expected UTC start time in query, got: %s", string(body))
			}

			w.Header().Set("Content-Type", "application/xml; charset=utf-8")
			w.WriteHeader(http.StatusMultiStatus)
			_, _ = w.Write([]byte(reportResponseXML))
		default:
			t.Errorf("unexpected method: %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()

	client := provider.NewCalDAVClient(srv.Client(), nil)
	source := provider.CalendarSource{
		Name:     "Test Calendar",
		Type:     "caldav",
		URL:      srv.URL + "/caldav/calendars/user/",
		Username: "caluser",
		Password: "calpass",
		Color:    "#3b82f6",
		Enabled:  true,
	}

	events, err := client.FetchCalendarEvents(context.Background(), source, windowStart, windowEnd)
	if err != nil {
		t.Fatalf("FetchCalendarEvents failed: %v", err)
	}

	// 1 single event + 3 recurring standup occurrences = 4 events
	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(events))
	}

	if events[0].Title != "CalDAV Timed Event" || events[0].Location != "Conference Room A" {
		t.Errorf("unexpected event 0: %+v", events[0])
	}
	if events[1].Title != "CalDAV Recurring Standup" {
		t.Errorf("unexpected event 1: %+v", events[1])
	}
}

func TestCalDAV_REPORT_BearerTokenAuth(t *testing.T) {
	t.Parallel()

	refTime := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	windowStart := refTime.AddDate(0, 0, -1)
	windowEnd := refTime.AddDate(0, 0, 14)

	calData := `BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:bearer-evt@mirrormere
DTSTART:20260925T160000Z
DTEND:20260925T170000Z
SUMMARY:Bearer Authenticated Event
END:VEVENT
END:VCALENDAR`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer secret-bearer-token-123" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("Unauthorized"))
			return
		}

		if r.Method == "PROPFIND" {
			w.Header().Set("Content-Type", "application/xml; charset=utf-8")
			w.WriteHeader(http.StatusMultiStatus)
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8" ?>
<d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
  <d:response>
    <d:href>/caldav/</d:href>
    <d:propstat>
      <d:prop>
        <d:resourcetype><d:collection/><c:calendar/></d:resourcetype>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
</d:multistatus>`))
			return
		}

		if r.Method == "REPORT" {
			w.Header().Set("Content-Type", "application/xml; charset=utf-8")
			w.WriteHeader(http.StatusMultiStatus)
			_, _ = w.Write([]byte(fmt.Sprintf(`<?xml version="1.0" encoding="utf-8" ?>
<d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
  <d:response>
    <d:href>/caldav/events/1.ics</d:href>
    <d:propstat>
      <d:prop>
        <c:calendar-data>%s</c:calendar-data>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
</d:multistatus>`, calData)))
			return
		}

		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer srv.Close()

	client := provider.NewCalDAVClient(srv.Client(), nil)
	source := provider.CalendarSource{
		Name:    "Bearer Calendar",
		Type:    "caldav",
		URL:     srv.URL + "/caldav/",
		Token:   "secret-bearer-token-123",
		Enabled: true,
	}

	events, err := client.FetchCalendarEvents(context.Background(), source, windowStart, windowEnd)
	if err != nil {
		t.Fatalf("FetchCalendarEvents with Bearer auth failed: %v", err)
	}

	if len(events) != 1 || events[0].Title != "Bearer Authenticated Event" {
		t.Fatalf("expected 1 event, got %+v", events)
	}
}

func TestCalDAV_PROPFIND_DiscoveryFlow(t *testing.T) {
	t.Parallel()

	refTime := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	windowStart := refTime.AddDate(0, 0, -1)
	windowEnd := refTime.AddDate(0, 0, 14)

	calData := `BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:discovered-evt@mirrormere
DTSTART:20260925T180000Z
DTEND:20260925T190000Z
SUMMARY:Discovered Calendar Event
END:VEVENT
END:VCALENDAR`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")

		switch {
		case r.Method == "PROPFIND" && r.URL.Path == "/remote.php/dav/":
			// Step 1: Endpoint returns current-user-principal
			w.WriteHeader(http.StatusMultiStatus)
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8" ?>
<d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
  <d:response>
    <d:href>/remote.php/dav/</d:href>
    <d:propstat>
      <d:prop>
        <d:current-user-principal>
          <d:href>/remote.php/dav/principals/users/alex/</d:href>
        </d:current-user-principal>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
</d:multistatus>`))

		case r.Method == "PROPFIND" && r.URL.Path == "/remote.php/dav/principals/users/alex/":
			// Step 2: Principal returns calendar-home-set
			w.WriteHeader(http.StatusMultiStatus)
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8" ?>
<d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
  <d:response>
    <d:href>/remote.php/dav/principals/users/alex/</d:href>
    <d:propstat>
      <d:prop>
        <c:calendar-home-set>
          <d:href>/remote.php/dav/calendars/alex/</d:href>
        </c:calendar-home-set>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
</d:multistatus>`))

		case r.Method == "PROPFIND" && r.URL.Path == "/remote.php/dav/calendars/alex/":
			// Step 3: Calendar home-set lists child calendar collections
			w.WriteHeader(http.StatusMultiStatus)
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8" ?>
<d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
  <d:response>
    <d:href>/remote.php/dav/calendars/alex/</d:href>
    <d:propstat>
      <d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
  <d:response>
    <d:href>/remote.php/dav/calendars/alex/work/</d:href>
    <d:propstat>
      <d:prop>
        <d:resourcetype><d:collection/><c:calendar/></d:resourcetype>
        <d:displayname>Work</d:displayname>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
  <d:response>
    <d:href>/remote.php/dav/calendars/alex/family/</d:href>
    <d:propstat>
      <d:prop>
        <d:resourcetype><d:collection/><c:calendar/></d:resourcetype>
        <d:displayname>Family</d:displayname>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
</d:multistatus>`))

		case r.Method == "REPORT" && r.URL.Path == "/remote.php/dav/calendars/alex/family/":
			// Step 4: REPORT on discovered "Family" calendar
			w.WriteHeader(http.StatusMultiStatus)
			_, _ = w.Write([]byte(fmt.Sprintf(`<?xml version="1.0" encoding="utf-8" ?>
<d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
  <d:response>
    <d:href>/remote.php/dav/calendars/alex/family/evt1.ics</d:href>
    <d:propstat>
      <d:prop>
        <c:calendar-data>%s</c:calendar-data>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
</d:multistatus>`, calData)))

		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := provider.NewCalDAVClient(srv.Client(), nil)
	source := provider.CalendarSource{
		Name:     "Family",
		Type:     "caldav",
		URL:      srv.URL + "/remote.php/dav/",
		Username: "alex",
		Password: "password",
		Enabled:  true,
	}

	events, err := client.FetchCalendarEvents(context.Background(), source, windowStart, windowEnd)
	if err != nil {
		t.Fatalf("discovery and fetch failed: %v", err)
	}

	if len(events) != 1 || events[0].Title != "Discovered Calendar Event" {
		t.Fatalf("unexpected events: %+v", events)
	}

	// Verify discovery cache is populated and subsequent fetch hits cache directly
	cachedEvents, err := client.FetchCalendarEvents(context.Background(), source, windowStart, windowEnd)
	if err != nil {
		t.Fatalf("cached fetch failed: %v", err)
	}
	if len(cachedEvents) != 1 {
		t.Fatalf("expected 1 cached event, got %d", len(cachedEvents))
	}
}

func TestCalDAV_AuthFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statusCode int
		wantError  string
	}{
		{
			name:       "401 Unauthorized",
			statusCode: http.StatusUnauthorized,
			wantError:  "caldav authentication failed (HTTP 401)",
		},
		{
			name:       "403 Forbidden",
			statusCode: http.StatusForbidden,
			wantError:  "caldav authentication failed (HTTP 403)",
		},
		{
			name:       "404 Not Found",
			statusCode: http.StatusNotFound,
			wantError:  "caldav calendar collection not found (HTTP 404)",
		},
		{
			name:       "500 Server Error",
			statusCode: http.StatusInternalServerError,
			wantError:  "caldav report failed with HTTP 500",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
			}))
			defer srv.Close()

			client := provider.NewCalDAVClient(srv.Client(), nil)
			source := provider.CalendarSource{
				Name:     "Fail Cal",
				Type:     "caldav",
				URL:      srv.URL + "/caldav/",
				Username: "user",
				Password: "badpassword",
				Enabled:  true,
			}

			now := time.Now()
			_, err := client.FetchCalendarEvents(context.Background(), source, now, now.Add(24*time.Hour))
			if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("expected error containing %q, got: %v", tc.wantError, err)
			}
		})
	}
}

func TestCalDAV_EdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("Slowloris 2MB ceiling exceeded", func(t *testing.T) {
		t.Parallel()

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/xml; charset=utf-8")
			w.WriteHeader(http.StatusMultiStatus)
			chunk := make([]byte, 1024*1024)
			for i := range chunk {
				chunk[i] = ' '
			}
			_, _ = w.Write(chunk)
			_, _ = w.Write(chunk)
			_, _ = w.Write([]byte("EXCESS_BYTES"))
		}))
		defer srv.Close()

		client := provider.NewCalDAVClient(srv.Client(), nil)
		source := provider.CalendarSource{
			Name:    "Huge CalDAV",
			Type:    "caldav",
			URL:     srv.URL + "/huge/",
			Enabled: true,
		}

		now := time.Now()
		_, err := client.FetchCalendarEvents(context.Background(), source, now, now.Add(24*time.Hour))
		if err == nil || !strings.Contains(err.Error(), "exceeds size limit") {
			t.Fatalf("expected size limit error, got: %v", err)
		}
	})

	t.Run("Empty multi-status returns empty slice without error", func(t *testing.T) {
		t.Parallel()

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/xml; charset=utf-8")
			w.WriteHeader(http.StatusMultiStatus)
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8" ?>
<d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
</d:multistatus>`))
		}))
		defer srv.Close()

		client := provider.NewCalDAVClient(srv.Client(), nil)
		source := provider.CalendarSource{
			Name:    "Empty Range",
			Type:    "caldav",
			URL:     srv.URL + "/empty/",
			Enabled: true,
		}

		now := time.Now()
		events, err := client.FetchCalendarEvents(context.Background(), source, now, now.Add(24*time.Hour))
		if err != nil {
			t.Fatalf("unexpected error on empty multi-status: %v", err)
		}
		if len(events) != 0 {
			t.Fatalf("expected 0 events, got %d", len(events))
		}
	})

	t.Run("Fallback to raw iCalendar text when non-XML returned", func(t *testing.T) {
		t.Parallel()

		rawICS := `BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:raw-fallback@mirrormere
DTSTART:20260925T120000Z
DTEND:20260925T130000Z
SUMMARY:Raw Fallback Event
END:VEVENT
END:VCALENDAR`

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/calendar")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(rawICS))
		}))
		defer srv.Close()

		client := provider.NewCalDAVClient(srv.Client(), nil)
		source := provider.CalendarSource{
			Name:    "Raw Fallback",
			Type:    "caldav",
			URL:     srv.URL + "/raw/",
			Enabled: true,
		}

		now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
		events, err := client.FetchCalendarEvents(context.Background(), source, now.Add(-time.Hour), now.Add(24*time.Hour))
		if err != nil {
			t.Fatalf("raw fallback failed: %v", err)
		}
		if len(events) != 1 || events[0].Title != "Raw Fallback Event" {
			t.Fatalf("expected 1 raw fallback event, got %+v", events)
		}
	})

	t.Run("Skip empty calendar-data and invalid ICS", func(t *testing.T) {
		t.Parallel()

		validICS := `BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:valid-one@mirrormere
DTSTART:20260925T120000Z
DTEND:20260925T130000Z
SUMMARY:Valid Event
END:VEVENT
END:VCALENDAR`

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/xml; charset=utf-8")
			w.WriteHeader(http.StatusMultiStatus)
			_, _ = w.Write([]byte(fmt.Sprintf(`<?xml version="1.0" encoding="utf-8" ?>
<d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
  <d:response>
    <d:href>/caldav/empty/</d:href>
    <d:propstat>
      <d:prop><c:calendar-data></c:calendar-data></d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
  <d:response>
    <d:href>/caldav/notfound/</d:href>
    <d:propstat>
      <d:prop><c:calendar-data>NOT_FOUND_DATA</c:calendar-data></d:prop>
      <d:status>HTTP/1.1 404 Not Found</d:status>
    </d:propstat>
  </d:response>
  <d:response>
    <d:href>/caldav/broken/</d:href>
    <d:propstat>
      <d:prop><c:calendar-data>INVALID_ICS_GARBAGE</c:calendar-data></d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
  <d:response>
    <d:href>/caldav/valid/</d:href>
    <d:propstat>
      <d:prop><c:calendar-data>%s</c:calendar-data></d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
</d:multistatus>`, validICS)))
		}))
		defer srv.Close()

		client := provider.NewCalDAVClient(srv.Client(), nil)
		source := provider.CalendarSource{
			Name:    "Mixed Responses",
			Type:    "caldav",
			URL:     srv.URL + "/mixed/",
			Enabled: true,
		}

		now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
		events, err := client.FetchCalendarEvents(context.Background(), source, now.Add(-time.Hour), now.Add(24*time.Hour))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(events) != 1 || events[0].Title != "Valid Event" {
			t.Fatalf("expected 1 valid event filtered out of junk, got %+v", events)
		}
	})
}

func TestCalendarProvider_Init_CalDAV_Validations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		cfg       map[string]any
		opts      provider.InitOptions
		wantError string
	}{
		{
			name: "invalid calendar type",
			cfg: map[string]any{
				"calendars": []any{
					map[string]any{
						"name": "Bad Type",
						"type": "unsupported-protocol",
						"url":  "https://example.com/cal",
					},
				},
			},
			wantError: "invalid type \"unsupported-protocol\"",
		},
		{
			name: "username and username_env both provided",
			cfg: map[string]any{
				"calendars": []any{
					map[string]any{
						"name":         "Double User",
						"type":         "caldav",
						"url":          "https://example.com/caldav/",
						"username":     "user",
						"username_env": "USER_ENV",
					},
				},
			},
			wantError: "cannot specify both 'username' and 'username_env'",
		},
		{
			name: "password and password_env both provided",
			cfg: map[string]any{
				"calendars": []any{
					map[string]any{
						"name":         "Double Pass",
						"type":         "caldav",
						"url":          "https://example.com/caldav/",
						"username":     "user",
						"password":     "pass",
						"password_env": "PASS_ENV",
					},
				},
			},
			wantError: "cannot specify both 'password' and 'password_env'",
		},
		{
			name: "token and token_env both provided",
			cfg: map[string]any{
				"calendars": []any{
					map[string]any{
						"name":      "Double Token",
						"type":      "caldav",
						"url":       "https://example.com/caldav/",
						"token":     "tok",
						"token_env": "TOK_ENV",
					},
				},
			},
			wantError: "cannot specify both 'token' and 'token_env'",
		},
		{
			name: "both basic auth and bearer token provided",
			cfg: map[string]any{
				"calendars": []any{
					map[string]any{
						"name":     "Both Auth Modes",
						"type":     "caldav",
						"url":      "https://example.com/caldav/",
						"username": "user",
						"password": "pass",
						"token":    "tok",
					},
				},
			},
			wantError: "cannot specify both Basic auth",
		},
		{
			name: "username without password",
			cfg: map[string]any{
				"calendars": []any{
					map[string]any{
						"name":     "User Only",
						"type":     "caldav",
						"url":      "https://example.com/caldav/",
						"username": "user",
					},
				},
			},
			wantError: "username provided without password",
		},
		{
			name: "password without username",
			cfg: map[string]any{
				"calendars": []any{
					map[string]any{
						"name":     "Pass Only",
						"type":     "caldav",
						"url":      "https://example.com/caldav/",
						"password": "pass",
					},
				},
			},
			wantError: "password provided without username",
		},
		{
			name: "username_env unset in environment and secrets",
			cfg: map[string]any{
				"calendars": []any{
					map[string]any{
						"name":         "Unset User Env",
						"type":         "caldav",
						"url":          "https://example.com/caldav/",
						"username_env": "UNSET_CALDAV_USER_VAR",
					},
				},
			},
			wantError: "environment variable \"UNSET_CALDAV_USER_VAR\" is not set",
		},
		{
			name: "password_env unset in environment and secrets",
			cfg: map[string]any{
				"calendars": []any{
					map[string]any{
						"name":         "Unset Pass Env",
						"type":         "caldav",
						"url":          "https://example.com/caldav/",
						"username":     "user",
						"password_env": "UNSET_CALDAV_PASS_VAR",
					},
				},
			},
			wantError: "environment variable \"UNSET_CALDAV_PASS_VAR\" is not set",
		},
		{
			name: "token_env unset in environment and secrets",
			cfg: map[string]any{
				"calendars": []any{
					map[string]any{
						"name":      "Unset Token Env",
						"type":      "caldav",
						"url":       "https://example.com/caldav/",
						"token_env": "UNSET_CALDAV_TOKEN_VAR",
					},
				},
			},
			wantError: "environment variable \"UNSET_CALDAV_TOKEN_VAR\" is not set",
		},
		{
			name: "credentials resolved via structured secret paths",
			cfg: map[string]any{
				"calendars": []any{
					map[string]any{
						"name":         "Structured CalDAV",
						"type":         "caldav",
						"url_env":      "IGNORED_URL_VAR",
						"username_env": "IGNORED_USER_VAR",
						"password_env": "IGNORED_PASS_VAR",
					},
				},
			},
			opts: provider.InitOptions{
				Secrets: map[string]string{
					"calendars[0].url":      "https://example.com/caldav/",
					"calendars[0].username": "caluser",
					"calendars[0].password": "calpass",
				},
			},
		},
		{
			name: "token resolved via structured secret paths",
			cfg: map[string]any{
				"calendars": []any{
					map[string]any{
						"name":      "Structured Token CalDAV",
						"type":      "caldav",
						"url":       "https://example.com/caldav/",
						"token_env": "IGNORED_TOKEN_VAR",
					},
				},
			},
			opts: provider.InitOptions{
				Secrets: map[string]string{
					"calendars[0].token": "secret-bearer-tok",
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

func TestCalendarProvider_Fetch_MultiCalendarWithCalDAV(t *testing.T) {
	t.Parallel()

	refTime := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

	// Server 1: Standard iCal feed
	icsData := `BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:ical-evt@mirrormere
DTSTART:20260925T100000Z
DTEND:20260925T110000Z
SUMMARY:iCal Standard Feed Event
END:VEVENT
END:VCALENDAR`

	srvICal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/calendar")
		_, _ = w.Write([]byte(icsData))
	}))
	defer srvICal.Close()

	// Server 2: CalDAV server
	caldavData := `BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:caldav-evt@mirrormere
DTSTART:20260925T140000Z
DTEND:20260925T150000Z
SUMMARY:CalDAV Synchronized Event
END:VEVENT
END:VCALENDAR`

	srvCalDAV := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.WriteHeader(http.StatusMultiStatus)
		if r.Method == "PROPFIND" {
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8" ?>
<d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
  <d:response>
    <d:href>/cal/</d:href>
    <d:propstat>
      <d:prop><d:resourcetype><d:collection/><c:calendar/></d:resourcetype></d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
</d:multistatus>`))
			return
		}
		if r.Method == "REPORT" {
			_, _ = w.Write([]byte(fmt.Sprintf(`<?xml version="1.0" encoding="utf-8" ?>
<d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
  <d:response>
    <d:href>/cal/1.ics</d:href>
    <d:propstat>
      <d:prop><c:calendar-data>%s</c:calendar-data></d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
</d:multistatus>`, caldavData)))
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer srvCalDAV.Close()

	// Server 3: Failing CalDAV server (401 Unauthorized) to test partial status
	srvFailing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srvFailing.Close()

	p := provider.NewCalendarProviderWithClient(srvICal.Client(), func() time.Time {
		return refTime
	})

	cfg := map[string]any{
		"calendars": []any{
			map[string]any{
				"name":    "Work iCal",
				"type":    "ical",
				"url":     srvICal.URL,
				"color":   "#10b981",
				"enabled": true,
			},
			map[string]any{
				"name":     "Personal CalDAV",
				"type":     "caldav",
				"url":      srvCalDAV.URL + "/cal/",
				"username": "user",
				"password": "pass",
				"color":    "#3b82f6",
				"enabled":  true,
			},
			map[string]any{
				"name":     "Broken CalDAV",
				"type":     "caldav",
				"url":      srvFailing.URL + "/fail/",
				"username": "baduser",
				"password": "badpass",
				"color":    "#ef4444",
				"enabled":  true,
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
	// Partial success: 1 from iCal + 1 from CalDAV = 2 events
	if snap.SyncStatus != "partial" {
		t.Errorf("expected sync_status 'partial', got %q", snap.SyncStatus)
	}
	if len(snap.Events) != 2 {
		t.Fatalf("expected 2 merged events, got %d", len(snap.Events))
	}
	if snap.Events[0].Title != "iCal Standard Feed Event" {
		t.Errorf("expected event 0 to be iCal event, got %q", snap.Events[0].Title)
	}
	if snap.Events[1].Title != "CalDAV Synchronized Event" {
		t.Errorf("expected event 1 to be CalDAV event, got %q", snap.Events[1].Title)
	}
}

func TestCalDAV_DiscoveryEdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("first calendar collection fallback when name does not match", func(t *testing.T) {
		t.Parallel()

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/xml; charset=utf-8")
			w.WriteHeader(http.StatusMultiStatus)
			if r.Method == "PROPFIND" && r.URL.Path == "/home/" {
				_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8" ?>
<d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
  <d:response>
    <d:href>/home/</d:href>
    <d:propstat>
      <d:prop><c:calendar-home-set><d:href>/home/calendars/</d:href></c:calendar-home-set></d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
</d:multistatus>`))
				return
			}
			if r.Method == "PROPFIND" && r.URL.Path == "/home/calendars/" {
				_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8" ?>
<d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
  <d:response>
    <d:href>/home/calendars/default/</d:href>
    <d:propstat>
      <d:prop>
        <d:resourcetype><d:collection/><c:calendar/></d:resourcetype>
        <d:displayname>Unmatched Name</d:displayname>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
</d:multistatus>`))
				return
			}
			if r.Method == "REPORT" {
				_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8" ?>
<d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
  <d:response>
    <d:href>/home/calendars/default/1.ics</d:href>
    <d:propstat>
      <d:prop>
        <c:calendar-data>BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:fallback-first@mirrormere
DTSTART:20260925T120000Z
DTEND:20260925T130000Z
SUMMARY:Fallback First Calendar
END:VEVENT
END:VCALENDAR</c:calendar-data>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
</d:multistatus>`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		client := provider.NewCalDAVClient(srv.Client(), nil)
		source := provider.CalendarSource{
			Name:    "Wanted Different Name",
			Type:    "caldav",
			URL:     srv.URL + "/home/",
			Enabled: true,
		}

		now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
		events, err := client.FetchCalendarEvents(context.Background(), source, now.Add(-time.Hour), now.Add(24*time.Hour))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(events) != 1 || events[0].Title != "Fallback First Calendar" {
			t.Fatalf("expected 1 event from fallback calendar, got %+v", events)
		}
	})

	t.Run("PROPFIND failure falls back to configured URL", func(t *testing.T) {
		t.Parallel()

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "PROPFIND" {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if r.Method == "REPORT" {
				w.Header().Set("Content-Type", "application/xml; charset=utf-8")
				w.WriteHeader(http.StatusMultiStatus)
				_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8" ?>
<d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
  <d:response>
    <d:href>/cal/1.ics</d:href>
    <d:propstat>
      <d:prop>
        <c:calendar-data>BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:no-propfind@mirrormere
DTSTART:20260925T120000Z
DTEND:20260925T130000Z
SUMMARY:No PROPFIND Direct Event
END:VEVENT
END:VCALENDAR</c:calendar-data>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
</d:multistatus>`))
				return
			}
		}))
		defer srv.Close()

		client := provider.NewCalDAVClient(srv.Client(), nil)
		source := provider.CalendarSource{
			Name:    "Direct Only",
			Type:    "caldav",
			URL:     srv.URL + "/cal/",
			Enabled: true,
		}

		now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
		events, err := client.FetchCalendarEvents(context.Background(), source, now.Add(-time.Hour), now.Add(24*time.Hour))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(events) != 1 || events[0].Title != "No PROPFIND Direct Event" {
			t.Fatalf("expected 1 direct event, got %+v", events)
		}
	})

	t.Run("webcal scheme converted on PROPFIND discovery", func(t *testing.T) {
		t.Parallel()

		client := provider.NewCalDAVClient(&http.Client{Timeout: 50 * time.Millisecond}, nil)
		source := provider.CalendarSource{
			Name: "Webcal Test",
			Type: "caldav",
			URL:  "webcal://127.0.0.1:54321/cal/",
		}
		discovered, err := client.DiscoverCalendarURL(context.Background(), source)
		if err != nil {
			t.Fatalf("unexpected discovery error: %v", err)
		}
		if !strings.HasPrefix(discovered, "https://") {
			t.Errorf("expected https prefix, got %q", discovered)
		}
	})
}

func TestCalDAV_NetworkAndParsingErrors(t *testing.T) {
	t.Parallel()

	t.Run("network error on REPORT", func(t *testing.T) {
		t.Parallel()

		client := provider.NewCalDAVClient(&http.Client{Timeout: 50 * time.Millisecond}, nil)
		source := provider.CalendarSource{
			Name: "Dead CalDAV",
			Type: "caldav",
			URL:  "http://127.0.0.1:59999/cal/",
		}
		now := time.Now()
		_, err := client.FetchCalendarEvents(context.Background(), source, now, now.Add(time.Hour))
		if err == nil {
			t.Fatal("expected network error on unreachable server")
		}
	})

	t.Run("invalid request URL", func(t *testing.T) {
		t.Parallel()

		client := provider.NewCalDAVClient(nil, nil)
		source := provider.CalendarSource{
			Name: "Invalid URL",
			Type: "caldav",
			URL:  "http:// invalid host with spaces",
		}
		now := time.Now()
		_, err := client.FetchCalendarEvents(context.Background(), source, now, now.Add(time.Hour))
		if err == nil {
			t.Fatal("expected error on invalid URL")
		}
	})

	t.Run("PROPFIND 401 unauthorized in discovery", func(t *testing.T) {
		t.Parallel()

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer srv.Close()

		client := provider.NewCalDAVClient(srv.Client(), nil)
		source := provider.CalendarSource{
			Name: "Auth Discovery Fail",
			Type: "caldav",
			URL:  srv.URL + "/dav/",
		}
		now := time.Now()
		_, err := client.FetchCalendarEvents(context.Background(), source, now, now.Add(time.Hour))
		if err == nil || !strings.Contains(err.Error(), "authentication failed") {
			t.Fatalf("expected authentication failure in discovery, got: %v", err)
		}
	})
}

