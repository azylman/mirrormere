package testutil_test

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/azylman/mirrormere/internal/domain"
	"github.com/azylman/mirrormere/internal/testutil"
)

type routeHit struct {
	method, target, body string
	wantStatus           int
	wantBody, wantType   string
}

// doRouteHit sends one request and checks the scripted response.
func doRouteHit(t *testing.T, srv *httptest.Server, hit routeHit) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), hit.method, srv.URL+hit.target, strings.NewReader(hit.body))
	if err != nil {
		t.Fatalf("NewRequest(%s): %v", hit.target, err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", hit.method, hit.target, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != hit.wantStatus || string(body) != hit.wantBody || resp.Header.Get("Content-Type") != hit.wantType {
		t.Fatalf("%s %s = %d %q (%s) %v, want %d %q (%s)", hit.method, hit.target, resp.StatusCode, body, resp.Header.Get("Content-Type"), err, hit.wantStatus, hit.wantBody, hit.wantType)
	}
}

func TestRouteServer(t *testing.T) {
	t.Parallel()
	routes := map[string]testutil.Route{
		"/items":            {Body: `{"page":1}`},
		"/items?page=2":     {Status: http.StatusAccepted, Body: "two", ContentType: "text/plain"},
		"/flaky":            {Status: http.StatusUnauthorized, Next: []testutil.Route{{Status: http.StatusTeapot}, {Body: "steady"}}},
		"/flaky-single":     {Status: http.StatusBadGateway, Next: []testutil.Route{{Body: "recovered"}}},
		"/unused-reference": {Body: "never"},
	}
	tests := []struct {
		name         string
		hits         []routeHit
		wantRequests []testutil.Request
	}{
		{
			name: "request uri then path then not found",
			hits: []routeHit{
				{http.MethodGet, "/items?page=2", "", http.StatusAccepted, "two", "text/plain"},
				{http.MethodPost, "/items?page=9", "payload", http.StatusOK, `{"page":1}`, "application/json"},
				{http.MethodGet, "/missing", "", http.StatusNotFound, "404 page not found\n", "text/plain; charset=utf-8"},
			},
			wantRequests: []testutil.Request{
				{Method: http.MethodGet, Path: "/items", RawQuery: "page=2"},
				{Method: http.MethodPost, Path: "/items", RawQuery: "page=9", Body: "payload"},
				{Method: http.MethodGet, Path: "/missing"},
			},
		},
		{
			name: "next routes advance and the last one sticks",
			hits: []routeHit{
				{http.MethodGet, "/flaky", "", http.StatusUnauthorized, "", "application/json"},
				{http.MethodGet, "/flaky", "", http.StatusTeapot, "", "application/json"},
				{http.MethodGet, "/flaky", "", http.StatusOK, "steady", "application/json"},
				{http.MethodGet, "/flaky", "", http.StatusOK, "steady", "application/json"},
				{http.MethodGet, "/flaky-single", "", http.StatusBadGateway, "", "application/json"},
				{http.MethodGet, "/flaky-single", "", http.StatusOK, "recovered", "application/json"},
			},
			wantRequests: []testutil.Request{
				{Method: http.MethodGet, Path: "/flaky"},
				{Method: http.MethodGet, Path: "/flaky"},
				{Method: http.MethodGet, Path: "/flaky"},
				{Method: http.MethodGet, Path: "/flaky"},
				{Method: http.MethodGet, Path: "/flaky-single"},
				{Method: http.MethodGet, Path: "/flaky-single"},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, captured := testutil.RouteServer(t, routes)
			for _, hit := range tc.hits {
				doRouteHit(t, srv, hit)
			}
			got := captured.Requests()
			if err := captured.Err(); err != nil {
				t.Fatalf("captured.Err() = %v, want nil", err)
			}
			if len(got) != len(tc.wantRequests) {
				t.Fatalf("captured %d requests, want %d", len(got), len(tc.wantRequests))
			}
			for i, want := range tc.wantRequests {
				g := got[i]
				if g.Method != want.Method || g.Path != want.Path || g.RawQuery != want.RawQuery || g.Body != want.Body || g.Header.Get("User-Agent") == "" {
					t.Errorf("request %d = %+v, want %+v", i, g, want)
				}
			}
		})
	}
	if routes["/flaky"].Status != http.StatusUnauthorized {
		t.Fatal("RouteServer mutated the caller's routes map")
	}
}

// TestRouteServer_TruncatedBody sends a body shorter than its Content-Length
// and half-closes, so the server records what arrived plus the read error.
func TestRouteServer_TruncatedBody(t *testing.T) {
	t.Parallel()
	srv, captured := testutil.RouteServer(t, map[string]testutil.Route{"/items": {}})
	conn, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, "POST /items HTTP/1.1\r\nHost: example.com\r\nContent-Length: 10\r\n\r\nabc"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := testutil.As[*net.TCPConn](t, conn).CloseWrite(); err != nil {
		t.Fatalf("close write: %v", err)
	}
	if _, err := io.ReadAll(conn); err != nil {
		t.Fatalf("read response: %v", err)
	}
	reqs := captured.Requests()
	if len(reqs) != 1 || reqs[0].Body != "abc" || !errors.Is(captured.Err(), io.ErrUnexpectedEOF) {
		t.Fatalf("requests %+v err %v, want one with body abc and unexpected EOF", reqs, captured.Err())
	}
}

func TestNonFlusher(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		codes    []int
		writes   []string
		wantCode int
		wantBody string
	}{
		{"first status wins", []int{http.StatusAccepted, http.StatusTeapot}, []string{"a", "b"}, http.StatusAccepted, "ab"},
		{"write implies 200", nil, []string{"body"}, http.StatusOK, "body"},
		{"no writes", nil, nil, 0, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := &testutil.NonFlusher{}
			var _ http.ResponseWriter = w
			if _, ok := any(w).(http.Flusher); ok {
				t.Fatal("NonFlusher must not implement http.Flusher")
			}
			w.Header().Set("X-Room", "kitchen")
			for _, c := range tc.codes {
				w.WriteHeader(c)
			}
			for _, s := range tc.writes {
				if n, err := w.Write([]byte(s)); n != len(s) || err != nil {
					t.Fatalf("Write(%q) = %d, %v", s, n, err)
				}
			}
			if w.Code != tc.wantCode || w.Body.String() != tc.wantBody || w.Header().Get("X-Room") != "kitchen" {
				t.Fatalf("got %d %q %v, want %d %q", w.Code, w.Body.String(), w.Header(), tc.wantCode, tc.wantBody)
			}
		})
	}
}

func TestFailingFlusher(t *testing.T) {
	t.Parallel()
	w := &testutil.FailingFlusher{}
	var _ http.Flusher = w
	w.WriteHeader(http.StatusOK)
	w.Flush()
	if n, err := w.Write([]byte("lost")); n != 0 || !errors.Is(err, testutil.ErrWrite) || w.Code != http.StatusOK || w.Body.Len() != 0 {
		t.Fatalf("Write = %d, %v; code %d body %q", n, err, w.Code, w.Body.String())
	}
}

func TestErrReader(t *testing.T) {
	t.Parallel()
	errBoom := errors.New("boom")
	tests := []struct {
		name    string
		r       testutil.ErrReader
		wantErr error
	}{
		{"configured error", testutil.ErrReader{Err: errBoom}, errBoom},
		{"zero value", testutil.ErrReader{}, io.ErrUnexpectedEOF},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if n, err := tc.r.Read(make([]byte, 4)); n != 0 || !errors.Is(err, tc.wantErr) {
				t.Fatalf("Read = %d, %v; want 0, %v", n, err, tc.wantErr)
			}
		})
	}
}

func TestPackages(t *testing.T) {
	t.Parallel()
	clock := &domain.Package{Type: "clock"}
	errBoom := errors.New("loader down")
	tests := []struct {
		name     string
		p        testutil.Packages
		typ      string
		want     *domain.Package
		wantErrs []string
	}{
		{"found", testutil.Packages{ByType: map[string]*domain.Package{"clock": clock}}, "clock", clock, nil},
		{"missing", testutil.Packages{ByType: map[string]*domain.Package{"clock": clock}}, "weather", nil, []string{"package not found: weather"}},
		{"nil map", testutil.Packages{}, "clock", nil, []string{"package not found: clock"}},
		{"forced error", testutil.Packages{ByType: map[string]*domain.Package{"clock": clock}, Err: errBoom}, "clock", nil, []string{"loader down"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := tc.p.LoadPackage(tc.typ)
			var gotErrs []string
			if err != nil {
				gotErrs = []string{err.Error()}
			}
			if got != tc.want || !slices.Equal(gotErrs, tc.wantErrs) {
				t.Fatalf("LoadPackage(%q) = %v, %v; want %v, %v", tc.typ, got, err, tc.want, tc.wantErrs)
			}
		})
	}
}
