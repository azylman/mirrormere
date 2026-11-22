package provider_test

import (
	"context"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/provider"
	"github.com/azylman/mirrormere/internal/testutil"
)

func icsDoc(events ...string) string {
	return "BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:-//Mirrormere//Test//EN\n" + strings.Join(events, "\n") + "\nEND:VCALENDAR"
}

// newICSServer serves body as text/calendar at the server root.
func newICSServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv, _ := testutil.RouteServer(t, map[string]testutil.Route{"/": {Body: body, ContentType: "text/calendar"}})
	return srv
}

// calendarWantEvent is the comparable slice of a CalendarEvent the parsing
// table pins (ID, calendar name, color and description are not asserted).
type calendarWantEvent struct {
	Title, Start, End string
	AllDay            bool
	Location, Image   string
}

func calendarGotEvents(events []provider.CalendarEvent) []calendarWantEvent {
	got := make([]calendarWantEvent, len(events))
	for i, ev := range events {
		got[i] = calendarWantEvent{Title: ev.Title, Start: ev.Start, End: ev.End, AllDay: ev.AllDay, Location: ev.Location, Image: ev.ImageURL}
	}
	return got
}

func calendarTitles(events []provider.CalendarEvent) []string {
	titles := make([]string, len(events))
	for i, ev := range events {
		titles[i] = ev.Title
	}
	return titles
}

func TestCalendarProvider_Init_Validations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		cfg        map[string]any
		opts       provider.InitOptions
		wantErrSub string
	}{
		{"missing calendars", map[string]any{}, provider.InitOptions{}, "requires at least one calendar"},
		{"empty calendars list", map[string]any{"calendars": []any{}}, provider.InitOptions{}, "requires at least one calendar"},
		{"calendar item not a map", map[string]any{"calendars": []any{"invalid"}}, provider.InitOptions{}, "not a map"},
		{"missing name", map[string]any{"calendars": []any{map[string]any{"url": "https://example.com/cal.ics"}}}, provider.InitOptions{}, "requires non-empty 'name'"},
		{"missing url and url_env", map[string]any{"calendars": []any{map[string]any{"name": "Cal1"}}}, provider.InitOptions{}, "requires either 'url' or 'url_env'"},
		{"both url and url_env provided", map[string]any{"calendars": []any{map[string]any{"name": "Cal1", "url": "https://x", "url_env": "E"}}}, provider.InitOptions{}, "cannot specify both 'url' and 'url_env'"},
		{"url_env unset in environment and secrets", map[string]any{"calendars": []any{map[string]any{"name": "Cal1", "url_env": "UNSET_123"}}}, provider.InitOptions{}, "environment variable \"UNSET_123\" is not set"},
		{"url_env resolved via secrets", map[string]any{"calendars": []any{map[string]any{"name": "Cal1", "url_env": "CAL_URL"}}}, provider.InitOptions{Secrets: map[string]string{"CAL_URL": "https://x"}}, ""},
		{"url_env resolved via structured secret path", map[string]any{"calendars": []any{map[string]any{"name": "Cal1", "url_env": "F"}}}, provider.InitOptions{Secrets: map[string]string{"calendars[0].url": "https://x"}}, ""},
		{"valid calendars with view", map[string]any{"view": "week", "calendars": []map[string]any{{"name": "Work", "url": "https://x", "color": "#10b981", "enabled": true}}}, provider.InitOptions{}, ""},
		{"valid calendars with strip_recurring and exclude_recurring", map[string]any{"view": "week", "strip_recurring": true, "calendars": []map[string]any{{"name": "W", "url": "https://x", "strip_recurring": false}, {"name": "P", "url": "https://y", "exclude_recurring": true}}}, provider.InitOptions{}, ""},
		{"valid calendars with exclude_recurring at root", map[string]any{"exclude_recurring": true, "calendars": []any{map[string]any{"name": "C", "url": "https://x"}}}, provider.InitOptions{}, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := provider.NewCalendarProvider()
			err := p.Init(context.Background(), tc.cfg, tc.opts)
			if tc.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Fatalf("expected error containing %q, got: %v", tc.wantErrSub, err)
				}
			} else if err != nil {
				t.Fatalf("unexpected init error: %v", err)
			}
		})
	}
}

var (
	calendarSep25    = time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	calendarSep20    = time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	calendarStripICS = icsDoc(
		"BEGIN:VEVENT\nUID:dentist\nDTSTART:20260921T140000Z\nDTEND:20260921T150000Z\nSUMMARY:Dentist\nEND:VEVENT",
		"BEGIN:VEVENT\nUID:standup\nDTSTART:20260921T090000Z\nDTEND:20260921T093000Z\nRRULE:FREQ=DAILY;COUNT=3\nSUMMARY:Standup\nEND:VEVENT",
		"BEGIN:VEVENT\nUID:standup\nRECURRENCE-ID:20260922T090000Z\nDTSTART:20260922T100000Z\nDTEND:20260922T103000Z\nSUMMARY:Standup (Rescheduled)\nEND:VEVENT",
		"BEGIN:VEVENT\nUID:rdate\nDTSTART:20260921T110000Z\nDTEND:20260921T120000Z\nRDATE:20260923T110000Z\nSUMMARY:Ad-hoc\nEND:VEVENT",
		"BEGIN:VEVENT\nUID:dinner\nDTSTART:20260922T180000Z\nDTEND:20260922T200000Z\nSUMMARY:Dinner\nEND:VEVENT",
	)
	calendarStandup21 = calendarWantEvent{Title: "Standup", Start: "2026-09-21T09:00:00Z", End: "2026-09-21T09:30:00Z"}
	calendarAdhoc     = calendarWantEvent{Title: "Ad-hoc", Start: "2026-09-21T11:00:00Z", End: "2026-09-21T12:00:00Z"}
	calendarDentist   = calendarWantEvent{Title: "Dentist", Start: "2026-09-21T14:00:00Z", End: "2026-09-21T15:00:00Z"}
	calendarResched   = calendarWantEvent{Title: "Standup (Rescheduled)", Start: "2026-09-22T10:00:00Z", End: "2026-09-22T10:30:00Z"}
	calendarDinner    = calendarWantEvent{Title: "Dinner", Start: "2026-09-22T18:00:00Z", End: "2026-09-22T20:00:00Z"}
	calendarStandup23 = calendarWantEvent{Title: "Standup", Start: "2026-09-23T09:00:00Z", End: "2026-09-23T09:30:00Z"}
	calendarAllStrip  = []calendarWantEvent{calendarStandup21, calendarAdhoc, calendarDentist, calendarResched, calendarDinner, calendarStandup23}
	calendarOneOffs   = []calendarWantEvent{calendarDentist, calendarDinner}
)

// calendarParsingTests drives TestCalendarProvider_FetchParsing.
var calendarParsingTests = []struct {
	name       string
	ics        string
	ref        time.Time
	cfg        map[string]any   // root config keys besides calendars
	sources    []map[string]any // per-source keys; url is filled in, nil means one plain source
	tz         string
	wantEvents []calendarWantEvent
}{
	{
		name: "timed all day multi day and duration inside window",
		ics: icsDoc(
			"BEGIN:VEVENT\nUID:evt-timed\nDTSTART:20260925T140000Z\nDTEND:20260925T150000Z\nSUMMARY:Soccer Practice\nLOCATION:Field 2\nEND:VEVENT",
			"BEGIN:VEVENT\nUID:evt-allday\nDTSTART;VALUE=DATE:20260926\nDTEND;VALUE=DATE:20260927\nSUMMARY:Trash & Recycling\nEND:VEVENT",
			"BEGIN:VEVENT\nUID:evt-multiday\nDTSTART;VALUE=DATE:20260927\nDTEND;VALUE=DATE:20260930\nSUMMARY:Camping Trip\nLOCATION:Yosemite\nEND:VEVENT",
			"BEGIN:VEVENT\nUID:evt-past\nDTSTART:20260501T100000Z\nDTEND:20260501T110000Z\nSUMMARY:Old Meeting\nEND:VEVENT",
			"BEGIN:VEVENT\nUID:evt-future\nDTSTART:20270501T100000Z\nDTEND:20270501T110000Z\nSUMMARY:Far Future Meeting\nEND:VEVENT",
			"BEGIN:VEVENT\nUID:evt-dur\nDTSTART:20260925T160000Z\nDURATION:PT1H30M\nSUMMARY:Duration Event\nEND:VEVENT",
		),
		ref: calendarSep25,
		wantEvents: []calendarWantEvent{
			{Title: "Soccer Practice", Start: "2026-09-25T14:00:00Z", End: "2026-09-25T15:00:00Z", Location: "Field 2"},
			{Title: "Duration Event", Start: "2026-09-25T16:00:00Z", End: "2026-09-25T17:30:00Z"},
			{Title: "Trash & Recycling", Start: "2026-09-26", End: "2026-09-26", AllDay: true},
			{Title: "Camping Trip", Start: "2026-09-27", End: "2026-09-29", AllDay: true, Location: "Yosemite"},
		},
	},
	{
		name: "recipe image from each property for single and recurring events",
		ics: icsDoc(
			"BEGIN:VEVENT\nUID:img-prop\nDTSTART;VALUE=DATE:20260925\nDTEND;VALUE=DATE:20260926\nSUMMARY:Mealie Image Prop\nIMAGE:/hearth-api/mealie/image/recipe-1\nEND:VEVENT",
			"BEGIN:VEVENT\nUID:attach-prop\nDTSTART;VALUE=DATE:20260925\nDTEND;VALUE=DATE:20260926\nSUMMARY:Mealie Attach Prop\nATTACH:https://mealie.local/api/media/recipes/abc/images/min-original.webp\nEND:VEVENT",
			"BEGIN:VEVENT\nUID:xmealie-prop\nDTSTART;VALUE=DATE:20260925\nDTEND;VALUE=DATE:20260926\nSUMMARY:Mealie X-Prop\nX-MEALIE-IMAGE:data:image/webp;base64,AAAA\nEND:VEVENT",
			"BEGIN:VEVENT\nUID:url-prop\nDTSTART;VALUE=DATE:20260925\nDTEND;VALUE=DATE:20260926\nSUMMARY:Mealie URL Prop\nURL:/api/media/recipes/def/images/original.webp\nEND:VEVENT",
			"BEGIN:VEVENT\nUID:loc-prop\nDTSTART;VALUE=DATE:20260925\nDTEND;VALUE=DATE:20260926\nSUMMARY:Mealie Location Fallback\nURL:https://example.com/not-an-image\nLOCATION:/hearth-api/mealie/image/recipe-loc\nEND:VEVENT",
			"BEGIN:VEVENT\nUID:recur-img\nDTSTART;VALUE=DATE:20260926\nDTEND;VALUE=DATE:20260927\nRRULE:FREQ=DAILY;COUNT=2\nSUMMARY:Recurring Taco Night\nX-MEALIE-IMAGE:/hearth-api/mealie/image/tacos\nEND:VEVENT",
		),
		ref: calendarSep25,
		wantEvents: []calendarWantEvent{
			{Title: "Mealie Attach Prop", Start: "2026-09-25", End: "2026-09-25", AllDay: true, Image: "https://mealie.local/api/media/recipes/abc/images/min-original.webp"},
			{Title: "Mealie Image Prop", Start: "2026-09-25", End: "2026-09-25", AllDay: true, Image: "/hearth-api/mealie/image/recipe-1"},
			{Title: "Mealie Location Fallback", Start: "2026-09-25", End: "2026-09-25", AllDay: true, Location: "/hearth-api/mealie/image/recipe-loc", Image: "/hearth-api/mealie/image/recipe-loc"},
			{Title: "Mealie URL Prop", Start: "2026-09-25", End: "2026-09-25", AllDay: true, Image: "/api/media/recipes/def/images/original.webp"},
			{Title: "Mealie X-Prop", Start: "2026-09-25", End: "2026-09-25", AllDay: true, Image: "data:image/webp;base64,AAAA"},
			{Title: "Recurring Taco Night", Start: "2026-09-26", End: "2026-09-26", AllDay: true, Image: "/hearth-api/mealie/image/tacos"},
			{Title: "Recurring Taco Night", Start: "2026-09-27", End: "2026-09-27", AllDay: true, Image: "/hearth-api/mealie/image/tacos"},
		},
	},
	{
		name: "exdates and recurrence-id overrides in utc and tzid",
		ics: icsDoc(
			"BEGIN:VEVENT\nUID:daily\nDTSTART:20260921T090000Z\nDTEND:20260921T093000Z\nRRULE:FREQ=DAILY;COUNT=5\nEXDATE:20260923T090000Z\nSUMMARY:Team Standup\nEND:VEVENT",
			"BEGIN:VEVENT\nUID:daily\nRECURRENCE-ID:20260924T090000Z\nDTSTART:20260924T100000Z\nDTEND:20260924T104500Z\nSUMMARY:Team Standup (Delayed)\nLOCATION:Zoom Room A\nEND:VEVENT",
			"BEGIN:VEVENT\nUID:tz-daily\nDTSTART;TZID=America/Los_Angeles:20260921T090000\nDTEND;TZID=America/Los_Angeles:20260921T093000\nRRULE:FREQ=DAILY;COUNT=4\nEXDATE;TZID=America/Los_Angeles:20260922T090000,,20260923,20260923T090000\nSUMMARY:LA Sync\nEND:VEVENT",
			"BEGIN:VEVENT\nUID:tz-daily\nRECURRENCE-ID;TZID=America/Los_Angeles:20260924T090000\nDTSTART;TZID=America/Los_Angeles:20260924T110000\nDTEND;TZID=America/Los_Angeles:20260924T113000\nSUMMARY:LA Sync (Moved)\nEND:VEVENT",
		),
		ref: calendarSep20,
		wantEvents: []calendarWantEvent{
			{Title: "Team Standup", Start: "2026-09-21T09:00:00Z", End: "2026-09-21T09:30:00Z"},
			{Title: "LA Sync", Start: "2026-09-21T09:00:00-07:00", End: "2026-09-21T09:30:00-07:00"},
			{Title: "Team Standup", Start: "2026-09-22T09:00:00Z", End: "2026-09-22T09:30:00Z"},
			{Title: "Team Standup (Delayed)", Start: "2026-09-24T10:00:00Z", End: "2026-09-24T10:45:00Z", Location: "Zoom Room A"},
			{Title: "LA Sync (Moved)", Start: "2026-09-24T11:00:00-07:00", End: "2026-09-24T11:30:00-07:00"},
			{Title: "Team Standup", Start: "2026-09-25T09:00:00Z", End: "2026-09-25T09:30:00Z"},
		},
	},
	{
		name: "tzid durations default length and skipped invalid starts",
		ics: icsDoc(
			"BEGIN:VEVENT\nUID:tz1\nDTSTART;TZID=America/New_York:20260925T090000\nDURATION:P1D\nSUMMARY:NY Full Day\nEND:VEVENT",
			"BEGIN:VEVENT\nUID:tz2\nDTSTART:20260925T140000Z\nDURATION:PT45S\nSUMMARY:Short\nEND:VEVENT",
			"BEGIN:VEVENT\nUID:inv\nDTSTART:INVALID_DATE\nSUMMARY:Inv\nEND:VEVENT",
			"BEGIN:VEVENT\nSUMMARY:No DTSTART\nEND:VEVENT",
			"BEGIN:VEVENT\nUID:long-uid-exceeding-thirty-two-chars-with-special-chars-@!#$%\nDTSTART:20260925T140000Z\nSUMMARY:Same Time A\nEND:VEVENT",
			"BEGIN:VEVENT\nUID:same-b\nDTSTART:20260925T140000Z\nSUMMARY:Same Time B\nEND:VEVENT",
			"BEGIN:VEVENT\nUID:\nDTSTART:20260925T140000Z\nSUMMARY:Empty UID\nEND:VEVENT",
		),
		ref: calendarSep25,
		wantEvents: []calendarWantEvent{
			{Title: "NY Full Day", Start: "2026-09-25T09:00:00-04:00", End: "2026-09-26T09:00:00-04:00"},
			{Title: "Empty UID", Start: "2026-09-25T14:00:00Z", End: "2026-09-25T15:00:00Z"},
			{Title: "Same Time A", Start: "2026-09-25T14:00:00Z", End: "2026-09-25T15:00:00Z"},
			{Title: "Same Time B", Start: "2026-09-25T14:00:00Z", End: "2026-09-25T15:00:00Z"},
			{Title: "Short", Start: "2026-09-25T14:00:00Z", End: "2026-09-25T14:00:45Z"},
		},
	},
	{
		name: "invalid rrule and invalid recurring start are dropped",
		ics: icsDoc(
			"BEGIN:VEVENT\nUID:bad-rrule\nDTSTART:20260925T100000Z\nRRULE:INVALID_RRULE\nSUMMARY:Bad\nEND:VEVENT",
			"BEGIN:VEVENT\nUID:recur-dur\nDTSTART:20260925T110000Z\nDURATION:PT2H\nRRULE:FREQ=DAILY;COUNT=2\nSUMMARY:Recurring with Duration\nEND:VEVENT",
			"BEGIN:VEVENT\nUID:recur-bad-start\nDTSTART:INVALID\nRRULE:FREQ=DAILY;COUNT=2\nSUMMARY:Bad Start\nEND:VEVENT",
		),
		ref: calendarSep25,
		wantEvents: []calendarWantEvent{
			{Title: "Recurring with Duration", Start: "2026-09-25T11:00:00Z", End: "2026-09-25T13:00:00Z"},
			{Title: "Recurring with Duration", Start: "2026-09-26T11:00:00Z", End: "2026-09-26T13:00:00Z"},
		},
	},
	{
		name: "secondly and minutely recurrence ignored",
		ics: icsDoc(
			"BEGIN:VEVENT\nUID:sec\nDTSTART:20260925T100000Z\nRRULE:FREQ=SECONDLY;INTERVAL=1\nSUMMARY:Sec\nEND:VEVENT",
			"BEGIN:VEVENT\nUID:min\nDTSTART:20260925T100000Z\nRRULE:FREQ=MINUTELY;INTERVAL=5\nSUMMARY:Min\nEND:VEVENT",
		),
		ref:        calendarSep25,
		wantEvents: []calendarWantEvent{},
	},
	{name: "strip recurring off by default", ics: calendarStripICS, ref: calendarSep20, wantEvents: calendarAllStrip},
	{name: "root strip_recurring", ics: calendarStripICS, ref: calendarSep20, cfg: map[string]any{"strip_recurring": true}, wantEvents: calendarOneOffs},
	{name: "root exclude_recurring alias", ics: calendarStripICS, ref: calendarSep20, cfg: map[string]any{"exclude_recurring": true}, wantEvents: calendarOneOffs},
	{
		name: "per-source strip_recurring overrides root",
		ics:  calendarStripICS, ref: calendarSep20, cfg: map[string]any{"strip_recurring": true},
		sources:    []map[string]any{{"name": "S", "strip_recurring": true}, {"name": "I", "strip_recurring": false}},
		wantEvents: []calendarWantEvent{calendarStandup21, calendarAdhoc, calendarDentist, calendarDentist, calendarResched, calendarDinner, calendarDinner, calendarStandup23},
	},
	{
		name: "per-source exclude_recurring alias", ics: calendarStripICS, ref: calendarSep20, cfg: map[string]any{"strip_recurring": false},
		sources: []map[string]any{{"name": "S", "exclude_recurring": true}}, wantEvents: calendarOneOffs,
	},
	{
		name: "sorted by local date in the configured timezone",
		ics: icsDoc(
			"BEGIN:VEVENT\nUID:e3\nDTSTART;VALUE=DATE:20261015\nDTEND;VALUE=DATE:20261016\nSUMMARY:Weekend Party\nEND:VEVENT",
			"BEGIN:VEVENT\nUID:e2\nDTSTART:20261015T020000Z\nDTEND:20261015T040000Z\nSUMMARY:Wednesday Game Night\nEND:VEVENT",
			"BEGIN:VEVENT\nUID:e1\nDTSTART:20261013T020000Z\nDTEND:20261013T030000Z\nSUMMARY:Monday Evening Dinner\nEND:VEVENT",
			"BEGIN:VEVENT\nUID:e4\nDTSTART:20261015T170000Z\nDTEND:20261015T183000Z\nSUMMARY:Saturday Brunch\nEND:VEVENT",
		),
		ref: time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC), tz: "America/Los_Angeles",
		wantEvents: []calendarWantEvent{
			{Title: "Monday Evening Dinner", Start: "2026-10-13T02:00:00Z", End: "2026-10-13T03:00:00Z"},
			{Title: "Wednesday Game Night", Start: "2026-10-15T02:00:00Z", End: "2026-10-15T04:00:00Z"},
			{Title: "Weekend Party", Start: "2026-10-15", End: "2026-10-15", AllDay: true},
			{Title: "Saturday Brunch", Start: "2026-10-15T17:00:00Z", End: "2026-10-15T18:30:00Z"},
		},
	},
}

func TestCalendarProvider_FetchParsing(t *testing.T) {
	t.Parallel()
	for _, tc := range calendarParsingTests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := newICSServer(t, tc.ics)
			sources := tc.sources
			if sources == nil {
				sources = []map[string]any{{"name": "Cal", "color": "#3b82f6"}}
			}
			calendars := make([]any, len(sources))
			for i, src := range sources {
				m := maps.Clone(src)
				m["url"] = srv.URL
				calendars[i] = m
			}
			cfg := maps.Clone(tc.cfg)
			if cfg == nil {
				cfg = map[string]any{}
			}
			cfg["calendars"] = calendars
			p := provider.NewCalendarProviderWithClient(srv.Client(), func() time.Time { return tc.ref })
			if err := p.Init(context.Background(), cfg, provider.InitOptions{Timezone: tc.tz}); err != nil {
				t.Fatalf("Init: %v", err)
			}
			res, err := p.Fetch(context.Background())
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			snap := testutil.As[provider.CalendarSnapshot](t, res)
			if snap.SyncStatus != "ok" {
				t.Errorf("SyncStatus = %q, want ok", snap.SyncStatus)
			}
			if got := calendarGotEvents(snap.Events); !slices.Equal(got, tc.wantEvents) {
				t.Errorf("events =\n%+v\nwant\n%+v", got, tc.wantEvents)
			}
		})
	}
}

func TestCalendarProvider_FetchErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		status     int
		body       string
		bodyBytes  int    // body of this many 'A's instead of body
		scheme     string // replaces the http scheme of the server URL
		rawURL     string // used instead of the server URL
		skipInit   bool
		wantErrSub string
	}{
		{name: "fetch before init", skipInit: true, wantErrSub: "calendar provider not initialized"},
		{name: "non-2xx status", status: http.StatusServiceUnavailable, wantErrSub: "all calendar feeds failed: F: http status 503"},
		{name: "malformed ics", body: "NOT A VALID ICS", wantErrSub: "F: parsing ics calendar"},
		{name: "webcal rewritten to https", body: icsDoc(), scheme: "webcal", wantErrSub: "http request failed: Get \"https://127.0.0.1:"},
		{name: "invalid request url", rawURL: "http:// invalid url with spaces", wantErrSub: "F: invalid request URL"},
		{name: "body over size limit", bodyBytes: 2*1024*1024 + 8, wantErrSub: "F: calendar feed exceeds size limit of 2097152 bytes"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body := tc.body
			if tc.bodyBytes > 0 {
				body = strings.Repeat("A", tc.bodyBytes)
			}
			srv, _ := testutil.RouteServer(t, map[string]testutil.Route{"/cal.ics": {Status: tc.status, Body: body, ContentType: "text/calendar"}})
			url := srv.URL + "/cal.ics"
			if tc.scheme != "" {
				url = tc.scheme + strings.TrimPrefix(url, "http")
			}
			if tc.rawURL != "" {
				url = tc.rawURL
			}
			p := provider.NewCalendarProviderWithClient(srv.Client(), nil)
			if !tc.skipInit {
				cfg := map[string]any{"calendars": []any{map[string]any{"name": "F", "url": url}}}
				if err := p.Init(context.Background(), cfg, provider.InitOptions{}); err != nil {
					t.Fatalf("Init: %v", err)
				}
			}
			_, err := p.Fetch(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Fatalf("Fetch error = %v, want containing %q", err, tc.wantErrSub)
			}
		})
	}
}

// calendarFeed is one configured source of a MultiFeedPartial row; its URL is
// the row's route server plus path.
type calendarFeed struct {
	name, typ, path string
	disabled        bool
}

func TestCalendarProvider_MultiFeedPartial(t *testing.T) {
	t.Parallel()
	workICS := icsDoc("BEGIN:VEVENT\nUID:w1\nDTSTART:20260925T170000Z\nDTEND:20260925T180000Z\nSUMMARY:Work Meeting\nEND:VEVENT")
	davCollection := msXML("/cal/", "HTTP/1.1 200 OK", "<d:resourcetype><d:collection/><c:calendar/></d:resourcetype>")
	davReport := msXML("/cal/1.ics", "HTTP/1.1 200 OK", "<c:calendar-data>"+icsDoc("BEGIN:VEVENT\nUID:c1\nDTSTART:20260925T140000Z\nDTEND:20260925T150000Z\nSUMMARY:CalDAV Event\nEND:VEVENT")+"</c:calendar-data>")
	tests := []struct {
		name       string
		feeds      []calendarFeed
		routes     map[string]testutil.Route
		wantSync   string
		wantTitles []string
	}{
		{
			name:       "ics feeds with a disabled source all succeed",
			feeds:      []calendarFeed{{name: "Work", typ: "ical", path: "/work.ics"}, {name: "Disabled", path: "/disabled.ics", disabled: true}},
			routes:     map[string]testutil.Route{"/work.ics": {Body: workICS, ContentType: "text/calendar"}},
			wantSync:   "ok",
			wantTitles: []string{"Work Meeting"},
		},
		{
			name:       "ics feed failure keeps the healthy feed",
			feeds:      []calendarFeed{{name: "Work", path: "/work.ics"}, {name: "Broken", path: "/broken.ics"}},
			routes:     map[string]testutil.Route{"/work.ics": {Body: workICS, ContentType: "text/calendar"}, "/broken.ics": {Status: http.StatusInternalServerError}},
			wantSync:   "partial",
			wantTitles: []string{"Work Meeting"},
		},
		{
			name:  "caldav and ics merged with a failing caldav source",
			feeds: []calendarFeed{{name: "Work", typ: "ical", path: "/work.ics"}, {name: "Personal", typ: "caldav", path: "/cal/"}, {name: "Broken", typ: "caldav", path: "/fail/"}},
			routes: map[string]testutil.Route{
				"/work.ics": {Body: workICS, ContentType: "text/calendar"},
				"/cal/":     {Status: http.StatusMultiStatus, Body: davCollection, ContentType: "application/xml", Next: []testutil.Route{{Status: http.StatusMultiStatus, Body: davReport, ContentType: "application/xml"}}},
				"/fail/":    {Status: http.StatusUnauthorized},
			},
			wantSync:   "partial",
			wantTitles: []string{"CalDAV Event", "Work Meeting"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, _ := testutil.RouteServer(t, tc.routes)
			calendars := make([]any, len(tc.feeds))
			for i, f := range tc.feeds {
				calendars[i] = map[string]any{"name": f.name, "type": f.typ, "url": srv.URL + f.path, "username": "u", "password": "p", "enabled": !f.disabled}
			}
			p := provider.NewCalendarProviderWithClient(srv.Client(), func() time.Time { return time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC) })
			if err := p.Init(context.Background(), map[string]any{"calendars": calendars}, provider.InitOptions{}); err != nil {
				t.Fatalf("Init: %v", err)
			}
			res, err := p.Fetch(context.Background())
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			snap := testutil.As[provider.CalendarSnapshot](t, res)
			if snap.SyncStatus != tc.wantSync {
				t.Errorf("SyncStatus = %q, want %q", snap.SyncStatus, tc.wantSync)
			}
			if got := calendarTitles(snap.Events); !slices.Equal(got, tc.wantTitles) {
				t.Errorf("titles = %v, want %v", got, tc.wantTitles)
			}
		})
	}
}

func TestSortCalendarEvents(t *testing.T) {
	t.Parallel()
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	tests := []struct {
		name   string
		events []provider.CalendarEvent
		loc    *time.Location
		want   []string
	}{
		{
			name: "local date in loc groups timed events with all-day ones",
			events: []provider.CalendarEvent{
				{Title: "Weekend Party", Start: "2026-10-15", End: "2026-10-15", AllDay: true},
				{Title: "Wednesday Game Night", Start: "2026-10-15T02:00:00Z", End: "2026-10-15T04:00:00Z"},
				{Title: "Monday Evening Dinner", Start: "2026-10-13T02:00:00Z", End: "2026-10-13T03:00:00Z"},
				{Title: "Saturday Brunch", Start: "2026-10-15T17:00:00Z", End: "2026-10-15T18:30:00Z"},
			},
			loc:  la,
			want: []string{"Monday Evening Dinner", "Wednesday Game Night", "Weekend Party", "Saturday Brunch"},
		},
		{
			name: "nil loc falls back to utc and raw strings sort unparsable starts",
			events: []provider.CalendarEvent{
				{Title: "Timed B", Start: "2026-11-02T12:00:00Z", End: "2026-11-02T13:00:00Z"},
				{Title: "AllDay A", Start: "20261102", End: "20261103", AllDay: true},
				{Title: "Timed A", Start: "20260102T150405", End: "20260102T160405"},
				{Title: "Invalid", Start: "not-a-date", End: "not-a-date"},
				{Title: "Empty", Start: "", End: ""},
			},
			want: []string{"Empty", "Timed A", "AllDay A", "Timed B", "Invalid"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			events := slices.Clone(tc.events)
			provider.SortCalendarEvents(events, tc.loc)
			if got := calendarTitles(events); !slices.Equal(got, tc.want) {
				t.Errorf("order = %v, want %v", got, tc.want)
			}
		})
	}
}
