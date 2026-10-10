package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type mockHAClient struct {
	processFunc func(ctx context.Context, req IntentRequest) (*IntentResponse, bool, error)
}

func (m *mockHAClient) ProcessIntent(ctx context.Context, req IntentRequest) (*IntentResponse, bool, error) {
	if m.processFunc != nil {
		return m.processFunc(ctx, req)
	}
	return nil, false, nil
}

type errReader struct{}

func (errReader) Read(p []byte) (n int, err error) {
	return 0, errors.New("read error simulation")
}

// nonFlusherResponseWriter simulates a ResponseWriter that does not implement http.Flusher.
type nonFlusherResponseWriter struct {
	header http.Header
	code   int
	body   []byte
}

func (n *nonFlusherResponseWriter) Header() http.Header {
	if n.header == nil {
		n.header = make(http.Header)
	}
	return n.header
}

func (n *nonFlusherResponseWriter) Write(b []byte) (int, error) {
	n.body = append(n.body, b...)
	return len(b), nil
}

func (n *nonFlusherResponseWriter) WriteHeader(statusCode int) {
	n.code = statusCode
}

// errFlusherResponseWriter simulates a ResponseWriter that implements Flusher but fails on Write.
type errFlusherResponseWriter struct {
	header http.Header
}

func (e *errFlusherResponseWriter) Header() http.Header {
	if e.header == nil {
		e.header = make(http.Header)
	}
	return e.header
}

func (e *errFlusherResponseWriter) Write(b []byte) (int, error) {
	return 0, errors.New("simulated write error")
}

func (e *errFlusherResponseWriter) WriteHeader(statusCode int) {}

func (e *errFlusherResponseWriter) Flush() {}

func TestServer_Healthz(t *testing.T) {
	srv := NewServer(&mockHAClient{}, nil)

	// GET request
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != "OK\n" {
		t.Errorf("expected OK\\n, got %q", rec.Body.String())
	}

	// HEAD request
	reqHead := httptest.NewRequest(http.MethodHead, "/healthz", nil)
	recHead := httptest.NewRecorder()
	srv.ServeHTTP(recHead, reqHead)
	if recHead.Code != http.StatusOK {
		t.Errorf("expected 200 for HEAD, got %d", recHead.Code)
	}

	// POST request (not allowed)
	reqPost := httptest.NewRequest(http.MethodPost, "/healthz", nil)
	recPost := httptest.NewRecorder()
	srv.ServeHTTP(recPost, reqPost)
	if recPost.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 Method Not Allowed, got %d", recPost.Code)
	}
}

func TestServer_Intent_MethodNotAllowed(t *testing.T) {
	srv := NewServer(&mockHAClient{}, nil)

	req := httptest.NewRequest(http.MethodGet, "/intent", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 Method Not Allowed, got %d", rec.Code)
	}
}

func TestServer_Intent_ReadBodyError(t *testing.T) {
	srv := NewServer(&mockHAClient{}, nil)

	req := httptest.NewRequest(http.MethodPost, "/intent", io.NopCloser(errReader{}))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request on read error, got %d", rec.Code)
	}
}

func TestServer_Intent_MalformedJSON(t *testing.T) {
	srv := NewServer(&mockHAClient{}, nil)

	req := httptest.NewRequest(http.MethodPost, "/intent", strings.NewReader("{invalid json"))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", rec.Code)
	}
}

func TestServer_Intent_EmptyText_204(t *testing.T) {
	srv := NewServer(&mockHAClient{}, nil)

	payload := `{"text": "   ", "speaker": "alex"}`
	req := httptest.NewRequest(http.MethodPost, "/intent", strings.NewReader(payload))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204 No Content for empty text, got %d", rec.Code)
	}
}

func TestServer_Intent_NoMatch_204(t *testing.T) {
	mock := &mockHAClient{
		processFunc: func(ctx context.Context, req IntentRequest) (*IntentResponse, bool, error) {
			return nil, false, nil
		},
	}
	srv := NewServer(mock, nil)

	payload := `{"text": "unknown command", "speaker": "alex"}`
	req := httptest.NewRequest(http.MethodPost, "/intent", strings.NewReader(payload))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204 No Content on no match, got %d", rec.Code)
	}
}

func TestServer_Intent_ClientError_204(t *testing.T) {
	mock := &mockHAClient{
		processFunc: func(ctx context.Context, req IntentRequest) (*IntentResponse, bool, error) {
			return nil, false, errors.New("upstream connection reset")
		},
	}
	srv := NewServer(mock, nil)

	payload := `{"text": "turn on lamp"}`
	req := httptest.NewRequest(http.MethodPost, "/intent", strings.NewReader(payload))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204 No Content on upstream error, got %d", rec.Code)
	}
}

func TestServer_Intent_ClientCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-canceled

	mock := &mockHAClient{
		processFunc: func(c context.Context, req IntentRequest) (*IntentResponse, bool, error) {
			return nil, false, c.Err()
		},
	}
	srv := NewServer(mock, nil)

	payload := `{"text": "turn on lamp"}`
	req := httptest.NewRequest(http.MethodPost, "/intent", strings.NewReader(payload)).WithContext(ctx)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	// Since canceled, response body should not be written
	if rec.Body.Len() > 0 {
		t.Errorf("expected empty body when client context canceled, got %s", rec.Body.String())
	}
}

func TestServer_Intent_JSON_Match(t *testing.T) {
	mock := &mockHAClient{
		processFunc: func(ctx context.Context, req IntentRequest) (*IntentResponse, bool, error) {
			return &IntentResponse{
				Intent: "action_done",
				Speech: "Turned on the kitchen light",
			}, true, nil
		},
	}
	srv := NewServer(mock, nil)

	payload := `{"text": "turn on kitchen light", "turn_id": "t-100"}`
	req := httptest.NewRequest(http.MethodPost, "/intent", strings.NewReader(payload))
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
		t.Errorf("expected application/json content type, got %q", rec.Header().Get("Content-Type"))
	}

	var resp IntentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode JSON response: %v", err)
	}
	if resp.Intent != "action_done" || resp.Speech != "Turned on the kitchen light" {
		t.Errorf("unexpected response content: %+v", resp)
	}
}

func TestServer_Intent_SSE_Match(t *testing.T) {
	mock := &mockHAClient{
		processFunc: func(ctx context.Context, req IntentRequest) (*IntentResponse, bool, error) {
			return &IntentResponse{
				Intent: "action_done",
				Speech: "Set thermostat to 72 degrees.",
			}, true, nil
		},
	}
	srv := NewServer(mock, nil)

	payload := `{"text": "set thermostat to 72", "turn_id": "turn-777"}`
	req := httptest.NewRequest(http.MethodPost, "/intent", strings.NewReader(payload))
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "text/event-stream") {
		t.Errorf("expected text/event-stream content type, got %q", rec.Header().Get("Content-Type"))
	}

	body := rec.Body.String()
	if !strings.Contains(body, "event: turn") || !strings.Contains(body, `"turn_id":"turn-777"`) {
		t.Errorf("missing turn event in SSE body: %s", body)
	}
	if !strings.Contains(body, "event: sentence") || !strings.Contains(body, `"text":"Set thermostat to 72 degrees."`) {
		t.Errorf("missing sentence event in SSE body: %s", body)
	}
	if !strings.Contains(body, "event: reply") || !strings.Contains(body, `"reply":"Set thermostat to 72 degrees."`) {
		t.Errorf("missing reply event in SSE body: %s", body)
	}
	if !strings.Contains(body, "event: done") {
		t.Errorf("missing done event in SSE body: %s", body)
	}
}

func TestServer_Intent_SSE_NonFlusher(t *testing.T) {
	mock := &mockHAClient{
		processFunc: func(ctx context.Context, req IntentRequest) (*IntentResponse, bool, error) {
			return &IntentResponse{
				Intent: "action_done",
				Speech: "Fallback response",
			}, true, nil
		},
	}
	srv := NewServer(mock, nil)

	payload := `{"text": "test fallback", "turn_id": "turn-nonflush"}`
	req := httptest.NewRequest(http.MethodPost, "/intent", strings.NewReader(payload))
	req.Header.Set("Accept", "text/event-stream")

	writer := &nonFlusherResponseWriter{}
	srv.ServeHTTP(writer, req)

	if writer.code != http.StatusOK {
		t.Errorf("expected 200, got %d", writer.code)
	}
	if !strings.Contains(string(writer.body), "Fallback response") {
		t.Errorf("expected JSON fallback body, got %s", string(writer.body))
	}
}

func TestServer_Intent_SSE_WriteError(t *testing.T) {
	mock := &mockHAClient{
		processFunc: func(ctx context.Context, req IntentRequest) (*IntentResponse, bool, error) {
			return &IntentResponse{
				Intent: "action_done",
				Speech: "Fallback response",
			}, true, nil
		},
	}
	srv := NewServer(mock, nil)

	payload := `{"text": "test write error", "turn_id": "turn-err"}`
	req := httptest.NewRequest(http.MethodPost, "/intent", strings.NewReader(payload))
	req.Header.Set("Accept", "text/event-stream")

	writer := &errFlusherResponseWriter{}
	srv.ServeHTTP(writer, req)
}

func TestServer_Intent_SSE_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	mock := &mockHAClient{
		processFunc: func(c context.Context, req IntentRequest) (*IntentResponse, bool, error) {
			cancel() // Cancel before SSE flush finishes
			return &IntentResponse{
				Intent: "action_done",
				Speech: "Quick reply",
			}, true, nil
		},
	}
	srv := NewServer(mock, nil)

	payload := `{"text": "cancel test", "turn_id": "turn-cancel"}`
	req := httptest.NewRequest(http.MethodPost, "/intent", strings.NewReader(payload)).WithContext(ctx)
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 code header set, got %d", rec.Code)
	}
}

func TestBuildHTTPServer(t *testing.T) {
	cfg := ServerConfig{
		Host:                "127.0.0.1",
		Port:                9099,
		ReadTimeoutSeconds:  12,
		WriteTimeoutSeconds: 14,
	}

	s := BuildHTTPServer(cfg, http.NotFoundHandler())
	if s.Addr != "127.0.0.1:9099" {
		t.Errorf("expected 127.0.0.1:9099, got %q", s.Addr)
	}
	if s.ReadTimeout != 12*time.Second {
		t.Errorf("expected read timeout 12s, got %v", s.ReadTimeout)
	}
	if s.WriteTimeout != 14*time.Second {
		t.Errorf("expected write timeout 14s, got %v", s.WriteTimeout)
	}
}
