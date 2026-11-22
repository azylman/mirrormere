package httpx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fixedTransport struct {
	resp *http.Response
	err  error
}

func (f fixedTransport) RoundTrip(*http.Request) (*http.Response, error) { return f.resp, f.err }

type failingBody struct{}

func (failingBody) Read([]byte) (int, error) { return 0, errors.New("body reset") }
func (failingBody) Close() error             { return nil }

func respond(status int, body string) *http.Client {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	return &http.Client{Transport: rewriteTo{srv: srv}}
}

type rewriteTo struct{ srv *httptest.Server }

func (r rewriteTo) RoundTrip(req *http.Request) (*http.Response, error) {
	out := req.Clone(req.Context())
	out.URL.Scheme = "http"
	out.URL.Host = strings.TrimPrefix(r.srv.URL, "http://")
	return r.srv.Client().Transport.RoundTrip(out)
}

func TestGetBounded(t *testing.T) {
	t.Parallel()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name       string
		ctx        context.Context
		client     *http.Client
		maxBytes   int64
		wantBody   string
		wantStatus int
		wantErr    string
	}{
		{"ok body under limit", context.Background(), respond(200, "hello"), 16, "hello", 200, ""},
		{"body truncated at limit", context.Background(), respond(200, "abcdefgh"), 3, "abc", 200, ""},
		{"non-2xx returns body without error", context.Background(), respond(503, "down"), 16, "down", 503, ""},
		{"transport error has zero status", context.Background(), &http.Client{Transport: fixedTransport{err: errors.New("dial refused")}}, 16, "", 0, "dial refused"},
		{"read error keeps status", context.Background(), &http.Client{Transport: fixedTransport{resp: &http.Response{StatusCode: http.StatusBadGateway, Body: failingBody{}}}}, 16, "", 502, "body reset"},
		{"canceled ctx fails request", canceled, respond(200, "x"), 16, "", 0, "context canceled"},
		{"nil client falls back to default", context.Background(), nil, 16, "", 0, "unsupported protocol scheme"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			target := "http://upstream.invalid/data"
			if tc.client == nil {
				target = "nope://upstream.invalid/data"
			}
			req, err := http.NewRequest(http.MethodGet, target, http.NoBody)
			if err != nil {
				t.Fatal(err)
			}
			body, status, err := GetBounded(tc.ctx, tc.client, req, tc.maxBytes)
			if status != tc.wantStatus {
				t.Errorf("status = %d, want %d", status, tc.wantStatus)
			}
			if string(body) != tc.wantBody {
				t.Errorf("body = %q, want %q", body, tc.wantBody)
			}
			gotErr := ""
			if err != nil {
				gotErr = err.Error()
			}
			if (tc.wantErr == "") != (gotErr == "") || !strings.Contains(gotErr, tc.wantErr) {
				t.Errorf("err = %q, want containing %q", gotErr, tc.wantErr)
			}
		})
	}
}
