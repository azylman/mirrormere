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

	"github.com/azylman/mirrormere/internal/testutil"
)

type mockHAClient struct {
	resp    *IntentResponse
	matched bool
	err     error
}

func (m *mockHAClient) ProcessIntent(context.Context, IntentRequest) (*IntentResponse, bool, error) {
	return m.resp, m.matched, m.err
}

// intentResult reads the status and body back from whichever writer a row used.
func intentResult(t *testing.T, w http.ResponseWriter) (int, string) {
	t.Helper()
	switch w := w.(type) {
	case *testutil.NonFlusher:
		return w.Code, w.Body.String()
	case *testutil.FailingFlusher:
		return w.Code, w.Body.String()
	}
	rec := testutil.As[*httptest.ResponseRecorder](t, w)
	return rec.Code, rec.Body.String()
}

func TestServer_Intent(t *testing.T) {
	t.Parallel()
	errRead := errors.New("read error simulation")
	tests := []struct {
		name, method, acceptHdr string
		body                    io.Reader
		w                       http.ResponseWriter
		cancelCtx, midCancel    bool
		clientMatch             bool
		clientResp              *IntentResponse
		clientErr               error
		wantCode                int
		wantSub                 []string
	}{
		{name: "method_not_allowed", method: http.MethodGet, wantCode: http.StatusMethodNotAllowed},
		{name: "read_body_error", method: http.MethodPost, body: testutil.ErrReader{Err: errRead}, wantCode: http.StatusBadRequest},
		{name: "malformed_json", method: http.MethodPost, body: strings.NewReader("{bad json"), wantCode: http.StatusBadRequest},
		{name: "empty_text", method: http.MethodPost, body: strings.NewReader(`{"text":"  "}`), wantCode: http.StatusNoContent},
		{name: "no_match", method: http.MethodPost, body: strings.NewReader(`{"text":"unknown"}`), wantCode: http.StatusNoContent},
		{name: "client_error", method: http.MethodPost, body: strings.NewReader(`{"text":"lamp"}`), clientErr: errors.New("upstream reset"), wantCode: http.StatusNoContent},
		{name: "canceled_context", method: http.MethodPost, body: strings.NewReader(`{"text":"lamp"}`), w: &testutil.NonFlusher{}, cancelCtx: true, clientErr: context.Canceled, wantCode: 0},
		{
			name: "sse_match_no_accept", method: http.MethodPost, body: strings.NewReader(`{"text":"kitchen light","turn_id":"t-100"}`),
			clientResp: &IntentResponse{Intent: "action_done", Speech: "Turned on"}, clientMatch: true,
			wantCode: http.StatusOK, wantSub: []string{"event: turn", "t-100", "event: reply", "Turned on", "tts_engine"},
		},
		{
			name: "sse_match_accept_stream", method: http.MethodPost, body: strings.NewReader(`{"text":"thermostat","turn_id":"t-777"}`), acceptHdr: "text/event-stream",
			clientResp: &IntentResponse{Intent: "action_done", Speech: "Set 72"}, clientMatch: true,
			wantCode: http.StatusOK, wantSub: []string{"event: turn", "t-777", "event: sentence", "event: done"},
		},
		{
			name: "non_flusher", method: http.MethodPost, body: strings.NewReader(`{"text":"test","turn_id":"t-non"}`), acceptHdr: "text/event-stream", w: &testutil.NonFlusher{},
			clientResp: &IntentResponse{Intent: "action_done", Speech: "Fallback"}, clientMatch: true, wantCode: http.StatusOK, wantSub: []string{"event: reply", "Fallback"},
		},
		{
			name: "write_error", method: http.MethodPost, body: strings.NewReader(`{"text":"test","turn_id":"t-err"}`), acceptHdr: "text/event-stream", w: &testutil.FailingFlusher{},
			clientResp: &IntentResponse{Intent: "action_done", Speech: "Fallback"}, clientMatch: true, wantCode: http.StatusOK,
		},
		{
			name: "mid_stream_context_cancel", method: http.MethodPost, body: strings.NewReader(`{"text":"cancel","turn_id":"t-cancel"}`), acceptHdr: "text/event-stream", midCancel: true,
			clientResp: &IntentResponse{Intent: "action_done", Speech: "Quick"}, clientMatch: true, wantCode: http.StatusOK,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.cancelCtx {
				cancel()
			}
			srv := NewServer(&mockHAClient{resp: tc.clientResp, matched: tc.clientMatch, err: tc.clientErr}, nil)
			body := tc.body
			if body == nil {
				body = http.NoBody
			}
			req := httptest.NewRequest(tc.method, "/intent", body).WithContext(ctx)
			if tc.acceptHdr != "" {
				req.Header.Set("Accept", tc.acceptHdr)
			}
			if tc.midCancel {
				cancel()
			}
			w := tc.w
			if w == nil {
				w = httptest.NewRecorder()
			}
			srv.ServeHTTP(w, req)
			code, bodyStr := intentResult(t, w)
			if code != tc.wantCode {
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
		srv.ServeHTTP(rec, httptest.NewRequest(m, "/healthz", http.NoBody))
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
