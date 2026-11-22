package testutil

import (
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// Route is one scripted response of a RouteServer. Status 0 means 200 and an
// empty ContentType means application/json. When Next is set, the first hit
// serves this route and then replaces it with Next[0], keeping Next[1:] queued.
type Route struct {
	Status      int
	Body        string
	ContentType string
	Next        []Route
}

// Request is one request a RouteServer received.
type Request struct {
	Method, Path, RawQuery string
	Header                 http.Header
	Body                   string
}

// Captured records the requests a RouteServer received.
type Captured struct {
	mu       sync.Mutex
	requests []Request
	errs     []error
}

// Err joins every error the server hit reading a request body or writing a
// response; nil when serving went cleanly.
func (c *Captured) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return errors.Join(c.errs...)
}

// Requests returns a copy of the requests received so far, in arrival order.
func (c *Captured) Requests() []Request {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Request(nil), c.requests...)
}

// RouteServer starts an httptest server that answers each request with the
// route keyed by its RequestURI, falling back to its Path, and 404 when
// neither matches. The server is closed through t.Cleanup.
func RouteServer(t testing.TB, routes map[string]Route) (*httptest.Server, *Captured) {
	t.Helper()
	captured := &Captured{}
	table := maps.Clone(routes)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(r.Body)
		captured.mu.Lock()
		defer captured.mu.Unlock()
		captured.errs = append(captured.errs, readErr)
		captured.requests = append(captured.requests, Request{
			Method: r.Method, Path: r.URL.Path, RawQuery: r.URL.RawQuery, Header: r.Header.Clone(), Body: string(body),
		})
		key := r.URL.RequestURI()
		route, ok := table[key]
		if !ok {
			key = r.URL.Path
			route, ok = table[key]
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		if len(route.Next) > 0 {
			next := route.Next[0]
			next.Next = route.Next[1:]
			table[key] = next
		}
		status, contentType := route.Status, route.ContentType
		if status == 0 {
			status = http.StatusOK
		}
		if contentType == "" {
			contentType = "application/json"
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, writeErr := io.WriteString(w, route.Body)
		captured.errs = append(captured.errs, writeErr)
	}))
	t.Cleanup(srv.Close)
	return srv, captured
}
