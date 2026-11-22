package provider_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/provider"
	"github.com/azylman/mirrormere/internal/testutil"
)

func msXML(href, status, innerProp string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?><d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:response><d:href>%s</d:href><d:propstat><d:prop>%s</d:prop><d:status>%s</d:status></d:propstat></d:response></d:multistatus>`, href, innerProp, status)
}

// davRoute is a 207 multistatus XML route; next queues the responses served on
// later hits of the same path (PROPFIND first, then REPORT).
func davRoute(body string, next ...testutil.Route) testutil.Route {
	return testutil.Route{Status: http.StatusMultiStatus, Body: body, ContentType: "application/xml; charset=utf-8", Next: next}
}

func davCalData(href, ics string) string {
	return msXML(href, "HTTP/1.1 200 OK", "<c:calendar-data>"+ics+"</c:calendar-data>")
}

func davEvent(uid, start, end, summary string) string {
	return "BEGIN:VEVENT\nUID:" + uid + "\nDTSTART:" + start + "\nDTEND:" + end + "\nSUMMARY:" + summary + "\nEND:VEVENT"
}

// caldavRequestLog lists "METHOD path" for every captured request and returns
// the last REPORT (zero value when none was sent).
func caldavRequestLog(reqs []testutil.Request) ([]string, testutil.Request) {
	log := make([]string, 0, len(reqs))
	var report testutil.Request
	for _, req := range reqs {
		log = append(log, req.Method+" "+req.Path)
		if req.Method == "REPORT" {
			report = req
		}
	}
	return log, report
}

func TestCalDAVClient_FetchCalendarEvents(t *testing.T) {
	t.Parallel()
	ref := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	deadURL := closedServerURL(t)
	collection := msXML("/cal/", "HTTP/1.1 200 OK", "<d:resourcetype><d:collection/><c:calendar/></d:resourcetype><d:displayname>Test Calendar</d:displayname>")
	directReport := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?><d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">`+
		`<d:response><d:href>/cal/e1.ics</d:href><d:propstat><d:prop><c:calendar-data>%s</c:calendar-data></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`+
		`<d:response><d:href>/cal/e2.ics</d:href><d:propstat><d:prop><c:calendar-data>%s</c:calendar-data></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response></d:multistatus>`,
		icsDoc("BEGIN:VEVENT\nUID:e1\nDTSTART:20260925T140000Z\nDTEND:20260925T150000Z\nSUMMARY:CalDAV Timed Event\nLOCATION:Room A\nEND:VEVENT"),
		icsDoc("BEGIN:VEVENT\nUID:e2\nDTSTART:20260926T100000Z\nDTEND:20260926T110000Z\nRRULE:FREQ=DAILY;COUNT=3\nSUMMARY:CalDAV Recurring Standup\nEND:VEVENT"))
	homeList := `<?xml version="1.0" encoding="utf-8"?><d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">` +
		`<d:response><d:href>/dav/calendars/u/</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>` +
		`<d:response><d:href>/dav/calendars/u/family/</d:href><d:propstat><d:prop><d:resourcetype><d:collection/><c:calendar/></d:resourcetype><d:displayname>Family</d:displayname></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response></d:multistatus>`
	skipReport := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?><d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">`+
		`<d:response><d:href>/1</d:href><d:propstat><d:prop><c:calendar-data></c:calendar-data></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`+
		`<d:response><d:href>/2</d:href><d:propstat><d:prop><c:calendar-data>NOT_FOUND</c:calendar-data></d:prop><d:status>HTTP/1.1 404 Not Found</d:status></d:propstat></d:response>`+
		`<d:response><d:href>/3</d:href><d:propstat><d:prop><c:calendar-data>INVALID_GARBAGE</c:calendar-data></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`+
		`<d:response><d:href>/4</d:href><d:propstat><d:prop><c:calendar-data>%s</c:calendar-data></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response></d:multistatus>`,
		icsDoc(davEvent("v1", "20260925T120000Z", "20260925T130000Z", "Valid")))
	timeRange := `<c:time-range start="20260924T120000Z" end="20261009T120000Z"/>`

	tests := []struct {
		name              string
		routes            map[string]testutil.Route
		path              string // appended to the route server URL
		rawURL            string // used instead of the route server URL
		calName           string
		user, pass, token string
		fetches           int // 0 means 1; later fetches reuse the cached discovery
		wantRequests      []string
		wantAuth          string // Authorization header of the last REPORT
		wantDepth         string // Depth header of the last REPORT
		wantReportBodySub string
		wantTitles        []string
		wantErrSub        string
	}{
		{
			name: "direct collection with basic auth", routes: map[string]testutil.Route{"/cal/": davRoute(collection, davRoute(directReport))},
			path: "/cal/", calName: "Test Calendar", user: "user", pass: "pass",
			wantRequests: []string{"PROPFIND /cal/", "REPORT /cal/"}, wantAuth: "Basic dXNlcjpwYXNz", wantDepth: "1", wantReportBodySub: timeRange,
			wantTitles: []string{"CalDAV Timed Event", "CalDAV Recurring Standup", "CalDAV Recurring Standup", "CalDAV Recurring Standup"},
		},
		{
			name:   "bearer token auth",
			routes: map[string]testutil.Route{"/cal/": davRoute(collection, davRoute(davCalData("/cal/1.ics", icsDoc(davEvent("b1", "20260925T160000Z", "20260925T170000Z", "Bearer Event")))))},
			path:   "/cal/", calName: "Bearer Cal", token: "secret-tok-123",
			wantRequests: []string{"PROPFIND /cal/", "REPORT /cal/"}, wantAuth: "Bearer secret-tok-123", wantDepth: "1", wantReportBodySub: timeRange, wantTitles: []string{"Bearer Event"},
		},
		{
			name: "principal then home-set discovery is cached for the next fetch",
			routes: map[string]testutil.Route{
				"/dav/":                    davRoute(msXML("/dav/", "HTTP/1.1 200 OK", "<d:current-user-principal><d:href>/dav/principals/u/</d:href></d:current-user-principal>")),
				"/dav/principals/u/":       davRoute(msXML("/dav/principals/u/", "HTTP/1.1 200 OK", "<c:calendar-home-set><d:href>/dav/calendars/u/</d:href></c:calendar-home-set>")),
				"/dav/calendars/u/":        davRoute(homeList),
				"/dav/calendars/u/family/": davRoute(davCalData("/dav/calendars/u/family/1.ics", icsDoc(davEvent("disc", "20260925T180000Z", "20260925T190000Z", "Discovered Event")))),
			},
			path: "/dav/", calName: "Family", user: "alpha", pass: "pass", fetches: 2,
			wantRequests: []string{"PROPFIND /dav/", "PROPFIND /dav/principals/u/", "PROPFIND /dav/calendars/u/", "REPORT /dav/calendars/u/family/", "REPORT /dav/calendars/u/family/"},
			wantAuth:     "Basic YWxwaGE6cGFzcw==", wantDepth: "1", wantReportBodySub: timeRange, wantTitles: []string{"Discovered Event"},
		},
		{
			name: "home-set falls back to the first calendar when no name matches",
			routes: map[string]testutil.Route{
				"/home/":       davRoute(msXML("/home/", "HTTP/1.1 200 OK", "<c:calendar-home-set><d:href>/home/c/</d:href></c:calendar-home-set>")),
				"/home/c/":     davRoute(msXML("/home/c/def/", "HTTP/1.1 200 OK", "<d:resourcetype><d:collection/><c:calendar/></d:resourcetype><d:displayname>Unmatched</d:displayname>")),
				"/home/c/def/": davRoute(davCalData("/home/c/def/1.ics", icsDoc(davEvent("f1", "20260925T120000Z", "20260925T130000Z", "Fallback First")))),
			},
			path: "/home/", calName: "Wanted Other",
			wantRequests: []string{"PROPFIND /home/", "PROPFIND /home/c/", "REPORT /home/c/def/"}, wantDepth: "1", wantReportBodySub: timeRange, wantTitles: []string{"Fallback First"},
		},
		{
			name:   "propfind refusal falls back to the configured url",
			routes: map[string]testutil.Route{"/cal/": {Status: http.StatusMethodNotAllowed, Next: []testutil.Route{davRoute(davCalData("/cal/1.ics", icsDoc(davEvent("d1", "20260925T120000Z", "20260925T130000Z", "Direct"))))}}},
			path:   "/cal/", calName: "Direct",
			wantRequests: []string{"PROPFIND /cal/", "REPORT /cal/"}, wantDepth: "1", wantReportBodySub: timeRange, wantTitles: []string{"Direct"},
		},
		{
			name:   "raw icalendar report body",
			routes: map[string]testutil.Route{"/raw/": {Body: icsDoc(davEvent("r1", "20260925T120000Z", "20260925T130000Z", "Raw Fallback")), ContentType: "text/calendar"}},
			path:   "/raw/", calName: "R",
			wantRequests: []string{"PROPFIND /raw/", "REPORT /raw/"}, wantDepth: "1", wantReportBodySub: timeRange, wantTitles: []string{"Raw Fallback"},
		},
		{
			name: "empty, non-200 and unparsable calendar-data are skipped", routes: map[string]testutil.Route{"/m/": davRoute(skipReport)}, path: "/m/", calName: "M",
			wantRequests: []string{"PROPFIND /m/", "REPORT /m/"}, wantDepth: "1", wantReportBodySub: timeRange, wantTitles: []string{"Valid"},
		},
		{
			name:   "empty multistatus yields no events",
			routes: map[string]testutil.Route{"/empty/": davRoute(`<?xml version="1.0" encoding="utf-8"?><d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"></d:multistatus>`)},
			path:   "/empty/", calName: "E",
			wantRequests: []string{"PROPFIND /empty/", "REPORT /empty/"}, wantDepth: "1", wantReportBodySub: timeRange, wantTitles: []string{},
		},
		{
			name: "discovery 401", routes: map[string]testutil.Route{"/cal/": {Status: http.StatusUnauthorized}}, path: "/cal/", calName: "F", user: "u", pass: "p",
			wantRequests: []string{"PROPFIND /cal/"}, wantErrSub: `caldav discovery failed for "F": caldav authentication failed (HTTP 401)`,
		},
		{
			name: "discovery 403", routes: map[string]testutil.Route{"/cal/": {Status: http.StatusForbidden}}, path: "/cal/", calName: "F", user: "u", pass: "p",
			wantRequests: []string{"PROPFIND /cal/"}, wantErrSub: "caldav authentication failed (HTTP 403)",
		},
		{
			name: "report 401 after discovery", routes: map[string]testutil.Route{"/cal/": davRoute(collection, testutil.Route{Status: http.StatusUnauthorized})}, path: "/cal/", calName: "F",
			wantRequests: []string{"PROPFIND /cal/", "REPORT /cal/"}, wantDepth: "1", wantReportBodySub: timeRange, wantErrSub: "caldav authentication failed (HTTP 401): invalid credentials for http://127.0.0.1:",
		},
		{
			name: "report 404", routes: map[string]testutil.Route{"/cal/": {Status: http.StatusNotFound}}, path: "/cal/", calName: "F", user: "u", pass: "p",
			wantRequests: []string{"PROPFIND /cal/", "REPORT /cal/"}, wantAuth: "Basic dTpw", wantDepth: "1", wantReportBodySub: timeRange, wantErrSub: "caldav calendar collection not found (HTTP 404)",
		},
		{
			name: "report 500", routes: map[string]testutil.Route{"/cal/": {Status: http.StatusInternalServerError}}, path: "/cal/", calName: "F", user: "u", pass: "p",
			wantRequests: []string{"PROPFIND /cal/", "REPORT /cal/"}, wantAuth: "Basic dTpw", wantDepth: "1", wantReportBodySub: timeRange, wantErrSub: "caldav report failed with HTTP 500",
		},
		{
			name: "report body over size limit", routes: map[string]testutil.Route{"/huge/": davRoute(strings.Repeat(" ", 2*1024*1024) + "EXCESS")}, path: "/huge/", calName: "H",
			wantRequests: []string{"PROPFIND /huge/", "REPORT /huge/"}, wantDepth: "1", wantReportBodySub: timeRange, wantErrSub: "caldav payload exceeds size limit of 2097152 bytes",
		},
		{name: "connection refused", rawURL: deadURL + "/cal/", calName: "Dead", wantErrSub: "caldav http request failed: Report \"http://127.0.0.1:"},
		{name: "webcal rewritten to https", rawURL: "webcal" + strings.TrimPrefix(deadURL, "http") + "/cal/", calName: "W", wantErrSub: "caldav http request failed: Report \"https://127.0.0.1:"},
		{name: "invalid url", rawURL: "http:// invalid url with spaces", calName: "Inv", wantErrSub: "invalid caldav request URL"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, captured := testutil.RouteServer(t, tc.routes)
			url := srv.URL + tc.path
			if tc.rawURL != "" {
				url = tc.rawURL
			}
			client := provider.NewCalDAVClient(srv.Client(), nil)
			source := provider.CalendarSource{Name: tc.calName, Type: "caldav", URL: url, Username: tc.user, Password: tc.pass, Token: tc.token, Enabled: true}
			var events []provider.CalendarEvent
			var err error
			for range max(tc.fetches, 1) {
				events, err = client.FetchCalendarEvents(context.Background(), source, ref.AddDate(0, 0, -1), ref.AddDate(0, 0, 14))
			}
			if (err == nil) != (tc.wantErrSub == "") || !strings.Contains(fmt.Sprint(err), tc.wantErrSub) {
				t.Fatalf("FetchCalendarEvents error = %v, want containing %q", err, tc.wantErrSub)
			}
			gotRequests, report := caldavRequestLog(captured.Requests())
			if !slices.Equal(gotRequests, tc.wantRequests) {
				t.Errorf("requests = %q, want %q", gotRequests, tc.wantRequests)
			}
			if got := [2]string{report.Header.Get("Authorization"), report.Header.Get("Depth")}; got != [2]string{tc.wantAuth, tc.wantDepth} {
				t.Errorf("REPORT Authorization, Depth = %q, want %q", got, [2]string{tc.wantAuth, tc.wantDepth})
			}
			if !strings.Contains(report.Body, tc.wantReportBodySub) {
				t.Errorf("REPORT body = %q, want containing %q", report.Body, tc.wantReportBodySub)
			}
			if got := calendarTitles(events); !slices.Equal(got, tc.wantTitles) {
				t.Errorf("titles = %q, want %q", got, tc.wantTitles)
			}
		})
	}
}

func TestCalendarProvider_Init_CalDAV_Validations(t *testing.T) {
	t.Parallel()

	mkCfg := func(name, typ, url, user, uEnv, pass, pEnv, tok, tEnv string) map[string]any {
		m := map[string]any{"name": name, "type": typ, "url": url}
		for _, kv := range [][2]string{{"username", user}, {"username_env", uEnv}, {"password", pass}, {"password_env", pEnv}, {"token", tok}, {"token_env", tEnv}} {
			if kv[1] != "" {
				m[kv[0]] = kv[1]
			}
		}
		return map[string]any{"calendars": []any{m}}
	}

	tests := []struct {
		name       string
		cfg        map[string]any
		opts       provider.InitOptions
		wantErrSub string
	}{
		{"invalid_calendar_type", mkCfg("Bad", "unsupported-protocol", "https://x", "", "", "", "", "", ""), provider.InitOptions{}, "invalid type"},
		{"username_and_username_env_both_provided", mkCfg("DU", "caldav", "https://x", "u", "U_ENV", "", "", "", ""), provider.InitOptions{}, "cannot specify both 'username' and 'username_env'"},
		{"password_and_password_env_both_provided", mkCfg("DP", "caldav", "https://x", "u", "", "p", "P_ENV", "", ""), provider.InitOptions{}, "cannot specify both 'password' and 'password_env'"},
		{"token_and_token_env_both_provided", mkCfg("DT", "caldav", "https://x", "", "", "", "", "t", "T_ENV"), provider.InitOptions{}, "cannot specify both 'token' and 'token_env'"},
		{"both_basic_auth_and_bearer_token_provided", mkCfg("Both", "caldav", "https://x", "u", "", "p", "", "t", ""), provider.InitOptions{}, "cannot specify both Basic auth"},
		{"username_without_password", mkCfg("UO", "caldav", "https://x", "u", "", "", "", "", ""), provider.InitOptions{}, "username provided without password"},
		{"password_without_username", mkCfg("PO", "caldav", "https://x", "", "", "p", "", "", ""), provider.InitOptions{}, "password provided without username"},
		{"username_env_unset_in_environment_and_secrets", mkCfg("UnsetUser", "caldav", "https://x", "", "UNSET_CALDAV_USER_VAR", "", "", "", ""), provider.InitOptions{}, "environment variable \"UNSET_CALDAV_USER_VAR\" is not set"},
		{"password_env_unset_in_environment_and_secrets", mkCfg("UnsetPass", "caldav", "https://x", "u", "", "", "UNSET_CALDAV_PASS_VAR", "", ""), provider.InitOptions{}, "environment variable \"UNSET_CALDAV_PASS_VAR\" is not set"},
		{"token_env_unset_in_environment_and_secrets", mkCfg("UnsetTok", "caldav", "https://x", "", "", "", "", "", "UNSET_CALDAV_TOKEN_VAR"), provider.InitOptions{}, "environment variable \"UNSET_CALDAV_TOKEN_VAR\" is not set"},
		{"credentials_resolved_via_structured_secret_paths", map[string]any{"calendars": []any{map[string]any{"name": "S", "type": "caldav", "url_env": "U", "username_env": "UN", "password_env": "PW"}}}, provider.InitOptions{Secrets: map[string]string{"calendars[0].url": "https://x", "calendars[0].username": "u", "calendars[0].password": "p"}}, ""},
		{"token_resolved_via_structured_secret_paths", map[string]any{"calendars": []any{map[string]any{"name": "S", "type": "caldav", "url": "https://x", "token_env": "T"}}}, provider.InitOptions{Secrets: map[string]string{"calendars[0].token": "tok"}}, ""},
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
