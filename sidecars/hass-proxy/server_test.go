package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type mockHAClient struct {
	resp    *IntentResponse
	matched bool
	err     error
}

func (m *mockHAClient) ProcessIntent(context.Context, IntentRequest) (*IntentResponse, bool, error) {
	return m.resp, m.matched, m.err
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read error simulation") }

type nonFlusher struct {
	header http.Header
	code   int
	body   []byte
}

func (n *nonFlusher) Header() http.Header        { if n.header == nil { n.header = make(http.Header) }; return n.header }
func (n *nonFlusher) Write(b []byte) (int, error) { n.body = append(n.body, b...); return len(b), nil }
func (n *nonFlusher) WriteHeader(c int)           { n.code = c }

type errFlusher struct{ nonFlusher }

func (e *errFlusher) Write([]byte) (int, error) { return 0, errors.New("simulated write error") }
func (e *errFlusher) Flush()                    {}

func TestServer_Intent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, method, body, acceptHdr, writerType string
		readErr, cancelCtx, midCancel, clientMatch bool
		clientResp                                *IntentResponse
		clientErr                                 error
		wantCode                                  int
		wantSub                                   []string
	}{
		{name: "method_not_allowed", method: http.MethodGet, wantCode: http.StatusMethodNotAllowed},
		{name: "read_body_error", method: http.MethodPost, readErr: true, wantCode: http.StatusBadRequest},
		{name: "malformed_json", method: http.MethodPost, body: "{bad json", wantCode: http.StatusBadRequest},
		{name: "empty_text", method: http.MethodPost, body: `{"text":"  "}`, wantCode: http.StatusNoContent},
		{name: "no_match", method: http.MethodPost, body: `{"text":"unknown"}`, wantCode: http.StatusNoContent},
		{name: "client_error", method: http.MethodPost, body: `{"text":"lamp"}`, clientErr: errors.New("upstream reset"), wantCode: http.StatusNoContent},
		{name: "canceled_context", method: http.MethodPost, body: `{"text":"lamp"}`, cancelCtx: true, clientErr: context.Canceled},
		{name: "sse_match_no_accept", method: http.MethodPost, body: `{"text":"kitchen light","turn_id":"t-100"}`, clientResp: &IntentResponse{Intent: "action_done", Speech: "Turned on"}, clientMatch: true, wantCode: http.StatusOK, wantSub: []string{"event: turn", "t-100", "event: reply", "Turned on", "tts_engine"}},
		{name: "sse_match_accept_stream", method: http.MethodPost, body: `{"text":"thermostat","turn_id":"t-777"}`, acceptHdr: "text/event-stream", clientResp: &IntentResponse{Intent: "action_done", Speech: "Set 72"}, clientMatch: true, wantCode: http.StatusOK, wantSub: []string{"event: turn", "t-777", "event: sentence", "event: done"}},
		{name: "non_flusher", method: http.MethodPost, body: `{"text":"test","turn_id":"t-non"}`, acceptHdr: "text/event-stream", writerType: "non_flusher", clientResp: &IntentResponse{Intent: "action_done", Speech: "Fallback"}, clientMatch: true, wantCode: http.StatusOK, wantSub: []string{"event: reply", "Fallback"}},
		{name: "write_error", method: http.MethodPost, body: `{"text":"test","turn_id":"t-err"}`, acceptHdr: "text/event-stream", writerType: "err_flusher", clientResp: &IntentResponse{Intent: "action_done", Speech: "Fallback"}, clientMatch: true},
		{name: "mid_stream_context_cancel", method: http.MethodPost, body: `{"text":"cancel","turn_id":"t-cancel"}`, acceptHdr: "text/event-stream", midCancel: true, clientResp: &IntentResponse{Intent: "action_done", Speech: "Quick"}, clientMatch: true, wantCode: http.StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancelCtx {
				cancel()
			}
			srv := NewServer(&mockHAClient{resp: tc.clientResp, matched: tc.clientMatch, err: tc.clientErr}, nil)
			var rBody io.Reader = strings.NewReader(tc.body)
			if tc.readErr {
				rBody = errReader{}
			}
			req := httptest.NewRequest(tc.method, "/intent", rBody).WithContext(ctx)
			if tc.acceptHdr != "" {
				req.Header.Set("Accept", tc.acceptHdr)
			}
			if tc.midCancel {
				cancel()
			}
			var code int
			var bodyStr string
			switch tc.writerType {
			case "non_flusher":
				w := &nonFlusher{}
				srv.ServeHTTP(w, req)
				code, bodyStr = w.code, string(w.body)
			case "err_flusher":
				w := &errFlusher{}
				srv.ServeHTTP(w, req)
				code = w.code
			default:
				rec := httptest.NewRecorder()
				srv.ServeHTTP(rec, req)
				code, bodyStr = rec.Code, rec.Body.String()
			}
			if tc.wantCode != 0 && code != tc.wantCode {
				t.Fatalf("code=%d, want %d", code, tc.wantCode)
			}
			for _, sub := range tc.wantSub {
				if !strings.Contains(bodyStr, sub) {
					t.Fatalf("body %q missing %q", bodyStr, sub)
				}
			}
		})
	}
}

func TestServer_HealthzAndBuild(t *testing.T) {
	srv := NewServer(&mockHAClient{}, nil)
	for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodPost} {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(m, "/healthz", nil))
		want := http.StatusOK
		if m == http.MethodPost {
			want = http.StatusMethodNotAllowed
		}
		if rec.Code != want {
			t.Errorf("%s /healthz got %d, want %d", m, rec.Code, want)
		}
	}
	s := BuildHTTPServer(ServerConfig{Host: "127.0.0.1", Port: 9099, ReadTimeoutSeconds: 12, WriteTimeoutSeconds: 14}, http.NotFoundHandler())
	if s.Addr != "127.0.0.1:9099" || s.ReadTimeout != 12*time.Second || s.WriteTimeout != 14*time.Second {
		t.Errorf("unexpected build config: %+v", s)
	}
}

func TestCoverageFlushEndpoint(t *testing.T) {
	tests := []struct {
		name, method, coverDir, wantSub string
		simTime                         time.Time
		writeErr                        error
		repeatCall                      bool
		wantCode                        int
	}{
		{name: "method_not_allowed", method: http.MethodDelete, wantCode: http.StatusMethodNotAllowed},
		{name: "unset_gocoverdir", method: http.MethodGet, wantCode: http.StatusOK, wantSub: `"status":"ok"`},
		{name: "valid_flush_and_debounce", method: http.MethodPost, coverDir: "temp", simTime: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC), repeatCall: true, wantCode: http.StatusOK, wantSub: `"status":"ok"`},
		{name: "write_error_500", method: http.MethodGet, coverDir: "temp", simTime: time.Date(2026, 10, 4, 13, 0, 0, 0, time.UTC), writeErr: errors.New("err"), wantCode: http.StatusInternalServerError, wantSub: "err"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.simTime.IsZero() {
				defer SetNowFnForTest(func() time.Time { return tc.simTime })()
			}
			dir := tc.coverDir
			if dir == "temp" {
				dir = t.TempDir()
			}
			t.Setenv("GOCOVERDIR", dir)
			defer SetWriteCountersDirForTest(func(string) error { return tc.writeErr })()
			srv := NewServer(&mockHAClient{}, nil)
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, httptest.NewRequest(tc.method, "/debug/coverage/flush", nil))
			if rec.Code != tc.wantCode || (tc.wantSub != "" && !strings.Contains(rec.Body.String(), tc.wantSub)) {
				t.Fatalf("code=%d want %d, body=%q want %q", rec.Code, tc.wantCode, rec.Body.String(), tc.wantSub)
			}
			if tc.repeatCall {
				rec2 := httptest.NewRecorder()
				srv.ServeHTTP(rec2, httptest.NewRequest(tc.method, "/debug/coverage/flush", nil))
				if rec2.Code != http.StatusOK || !strings.Contains(rec2.Body.String(), `"status":"debounced"`) {
					t.Fatalf("repeat code=%d body=%q", rec2.Code, rec2.Body.String())
				}
			}
		})
	}
}
