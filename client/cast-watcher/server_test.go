package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type mockMediaActionSender struct {
	mu               sync.Mutex
	sendErr, wakeErr error
	seekErr          error
	status           StatusSnapshot
	lastAction       string
	lastSeek         float64
	woken            bool
}

func (m *mockMediaActionSender) SendMediaAction(ctx context.Context, action string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastAction = action
	return m.sendErr
}

func (m *mockMediaActionSender) Seek(ctx context.Context, targetTime float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastSeek = targetTime
	return m.seekErr
}

func (m *mockMediaActionSender) Wake(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.woken = true
	return m.wakeErr
}

func (m *mockMediaActionSender) GetStatus() StatusSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

// SetNowFnForTest overrides the time provider for testing debounce without sleeps.
func SetNowFnForTest(fn func() time.Time) func() {
	flushMu.Lock()
	defer flushMu.Unlock()
	prev := nowFn
	nowFn = fn
	return func() {
		flushMu.Lock()
		defer flushMu.Unlock()
		nowFn = prev
	}
}

func SetWriteCountersDirForTest(fn func(string) error) func() {
	writeCountersDirMu.Lock()
	defer writeCountersDirMu.Unlock()
	prev := writeCountersDirFn
	writeCountersDirFn = fn
	return func() {
		writeCountersDirMu.Lock()
		defer writeCountersDirMu.Unlock()
		writeCountersDirFn = prev
	}
}

func TestActionServer_HandleAction(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		method     string
		body       string
		sendErr    error
		wakeErr    error
		seekErr    error
		wantCode   int
		wantAction string
		wantSeek   float64
		wantWake   bool
	}{
		{"play", "POST", `{"id":"chromecast","action":"play"}`, nil, nil, nil, http.StatusOK, "play", 0, false},
		{"pause", "POST", `{"id":"chromecast","action":"pause"}`, nil, nil, nil, http.StatusOK, "pause", 0, false},
		{"toggle", "POST", `{"id":"chromecast","action":"toggle_playback"}`, nil, nil, nil, http.StatusOK, "toggle_playback", 0, false},
		{"wake_ok", "POST", `{"id":"chromecast","action":"wake"}`, nil, nil, nil, http.StatusOK, "", 0, true},
		{"wake_not_connected", "POST", `{"id":"chromecast","action":"wake"}`, nil, ErrNotConnected, nil, http.StatusBadGateway, "", 0, true},
		{"wake_internal_err", "POST", `{"id":"chromecast","action":"wake"}`, nil, errors.New("timeout"), nil, http.StatusInternalServerError, "", 0, true},
		{"options_preflight", "OPTIONS", "", nil, nil, nil, http.StatusOK, "", 0, false},
		{"get_method_not_allowed", "GET", "", nil, nil, nil, http.StatusMethodNotAllowed, "", 0, false},
		{"malformed_json", "POST", `{invalid`, nil, nil, nil, http.StatusBadRequest, "", 0, false},
		{"empty_id", "POST", `{"id":"","action":"play"}`, nil, nil, nil, http.StatusBadRequest, "", 0, false},
		{"empty_action", "POST", `{"id":"chromecast","action":""}`, nil, nil, nil, http.StatusBadRequest, "", 0, false},
		{"wrong_id", "POST", `{"id":"appletv","action":"play"}`, nil, nil, nil, http.StatusNotFound, "", 0, false},
		{"action_invalid", "POST", `{"id":"chromecast","action":"bad"}`, ErrInvalidAction, nil, nil, http.StatusBadRequest, "bad", 0, false},
		{"action_not_connected", "POST", `{"id":"chromecast","action":"play"}`, ErrNotConnected, nil, nil, http.StatusBadGateway, "play", 0, false},
		{"action_no_session", "POST", `{"id":"chromecast","action":"play"}`, ErrNoActiveMediaSession, nil, nil, http.StatusBadGateway, "play", 0, false},
		{"action_internal_err", "POST", `{"id":"chromecast","action":"play"}`, errors.New("internal"), nil, nil, http.StatusInternalServerError, "play", 0, false},
		{"seek_float", "POST", `{"id":"chromecast","action":"seek","value":42.5}`, nil, nil, nil, http.StatusOK, "", 42.5, false},
		{"seek_string", "POST", `{"id":"chromecast","action":"seek","value":"10.5"}`, nil, nil, nil, http.StatusOK, "", 10.5, false},
		{"seek_int", "POST", `{"id":"chromecast","action":"seek","value":10}`, nil, nil, nil, http.StatusOK, "", 10.0, false},
		{"seek_negative", "POST", `{"id":"chromecast","action":"seek","value":-5}`, nil, nil, nil, http.StatusBadRequest, "", 0, false},
		{"seek_bad_string", "POST", `{"id":"chromecast","action":"seek","value":"abc"}`, nil, nil, nil, http.StatusBadRequest, "", 0, false},
		{"seek_missing_val", "POST", `{"id":"chromecast","action":"seek"}`, nil, nil, nil, http.StatusBadRequest, "", 0, false},
		{"seek_invalid_action", "POST", `{"id":"chromecast","action":"seek","value":10}`, nil, nil, ErrInvalidAction, http.StatusBadRequest, "", 10.0, false},
		{"seek_not_connected", "POST", `{"id":"chromecast","action":"seek","value":10}`, nil, nil, ErrNotConnected, http.StatusBadGateway, "", 10.0, false},
		{"seek_no_session", "POST", `{"id":"chromecast","action":"seek","value":10}`, nil, nil, ErrNoActiveMediaSession, http.StatusBadGateway, "", 10.0, false},
		{"seek_internal_err", "POST", `{"id":"chromecast","action":"seek","value":10}`, nil, nil, errors.New("seek failed"), http.StatusInternalServerError, "", 10.0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mock := &mockMediaActionSender{sendErr: tc.sendErr, wakeErr: tc.wakeErr, seekErr: tc.seekErr}
			server := NewActionServer(ServerConfig{Port: 0}, mock)
			req := httptest.NewRequest(tc.method, "/action", strings.NewReader(tc.body))
			rec := httptest.NewRecorder()
			server.mux.ServeHTTP(rec, req)

			if rec.Code != tc.wantCode {
				t.Fatalf("got code %d, want %d", rec.Code, tc.wantCode)
			}
			if tc.wantAction != "" && mock.lastAction != tc.wantAction {
				t.Fatalf("got action %q, want %q", mock.lastAction, tc.wantAction)
			}
			if tc.wantSeek > 0 && mock.lastSeek != tc.wantSeek {
				t.Fatalf("got seek %f, want %f", mock.lastSeek, tc.wantSeek)
			}
			if tc.wantWake && !mock.woken {
				t.Fatalf("expected Wake to be called")
			}
		})
	}
}

func TestActionServer_HandleHealthz(t *testing.T) {
	t.Parallel()
	mock := &mockMediaActionSender{status: StatusSnapshot{Connected: true, AppID: "YouTube", PlayerState: "playing"}}
	server := NewActionServer(ServerConfig{Port: 0}, mock)
	for _, tc := range []struct {
		method   string
		wantCode int
		hasBody  bool
	}{
		{http.MethodGet, http.StatusOK, true},
		{http.MethodHead, http.StatusOK, false},
		{http.MethodPost, http.StatusMethodNotAllowed, false},
	} {
		req := httptest.NewRequest(tc.method, "/healthz", nil)
		rec := httptest.NewRecorder()
		server.mux.ServeHTTP(rec, req)
		if rec.Code != tc.wantCode || (tc.hasBody && !strings.Contains(rec.Body.String(), "YouTube")) || (!tc.hasBody && rec.Body.Len() > 0) {
			t.Fatalf("%s /healthz: code %d, body %q", tc.method, rec.Code, rec.Body.String())
		}
	}
}

type errResponseWriter struct{ header http.Header }

func (e *errResponseWriter) Header() http.Header        { return e.header }
func (e *errResponseWriter) Write([]byte) (int, error)  { return 0, errors.New("simulated write error") }
func (e *errResponseWriter) WriteHeader(statusCode int) {}

func TestWriteJSON_Error(t *testing.T) {
	t.Parallel()
	writeJSON(&errResponseWriter{header: make(http.Header)}, http.StatusOK, map[string]string{"foo": "bar"})
}

func TestActionServer_HandleCoverageFlush(t *testing.T) {
	server := NewActionServer(ServerConfig{Port: 0}, &mockMediaActionSender{})

	req := httptest.NewRequest(http.MethodDelete, "/debug/coverage/flush", nil)
	rec := httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}

	t.Setenv("GOCOVERDIR", "")
	req = httptest.NewRequest(http.MethodPost, "/debug/coverage/flush", nil)
	rec = httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	t.Setenv("GOCOVERDIR", t.TempDir())
	restoreCounters := SetWriteCountersDirForTest(func(dir string) error { return nil })
	defer restoreCounters()
	simTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	restoreNow := SetNowFnForTest(func() time.Time { return simTime })
	defer restoreNow()

	req = httptest.NewRequest(http.MethodGet, "/debug/coverage/flush", nil)
	rec = httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected ok status, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "debounced") {
		t.Errorf("expected debounced, got %d: %s", rec.Code, rec.Body.String())
	}

	simTime = simTime.Add(2 * time.Second)
	SetWriteCountersDirForTest(func(dir string) error { return errors.New("write failed") })
	rec = httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 error, got %d", rec.Code)
	}
}

func TestExtractTargetTime(t *testing.T) {
	t.Parallel()
	nestedVal := any(42.5)
	var ifaceNil *interface{}

	for _, tc := range []struct {
		input   any
		wantVal float64
		wantErr bool
	}{
		{nil, 0, true},
		{float64(42.5), 42.5, false},
		{float32(42.5), 42.5, false},
		{int(42), 42.0, false},
		{int64(42), 42.0, false},
		{int32(42), 42.0, false},
		{"42.5", 42.5, false},
		{"  42.5  ", 42.5, false},
		{"not_a_number", 0, true},
		{&nestedVal, 42.5, false},
		{ifaceNil, 0, true},
		{true, 0, true},
	} {
		got, err := extractTargetTime(tc.input)
		if (err != nil) != tc.wantErr || (!tc.wantErr && got != tc.wantVal) {
			t.Fatalf("extractTargetTime(%v) = (%f, %v), want (%f, err=%v)", tc.input, got, err, tc.wantVal, tc.wantErr)
		}
	}
}
