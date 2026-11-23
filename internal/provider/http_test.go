package provider_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azylman/mirrormere/internal/provider"
)

func sampleResponseSchema() map[string]any {
	return map[string]any{
		"type":       "object",
		"required":   []any{"temperature", "humidity"},
		"properties": map[string]any{"temperature": map[string]any{"type": "number"}, "humidity": map[string]any{"type": "number"}, "status": map[string]any{"type": "string"}},
	}
}

func httpOpts(endpoint, method, token string, dims []int, secrets map[string]string, schema map[string]any) provider.InitOptions {
	return provider.InitOptions{ID: "w1", Type: "sensor-card", Endpoint: endpoint, Method: method, Token: token, Dimensions: dims, Secrets: secrets, ResponseSchema: schema}
}

func TestHTTPProvider_Init(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		opts       provider.InitOptions
		wantErrSub string
	}{
		{"empty_endpoint", httpOpts("", "GET", "", nil, nil, sampleResponseSchema()), "requires non-empty endpoint"},
		{"unsupported_method", httpOpts("http://x", "DELETE", "", nil, nil, sampleResponseSchema()), "unsupported HTTP method"},
		{"empty_schema", httpOpts("http://x", "GET", "", nil, nil, nil), "requires non-empty response_schema"},
		{"invalid_schema", httpOpts("http://x", "GET", "", nil, nil, map[string]any{"type": 123}), "failed to compile response_schema"},
		{"marshal_schema_err", httpOpts("http://x", "GET", "", nil, nil, map[string]any{"bad": make(chan int)}), "failed to marshal response_schema"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := provider.NewHTTPProviderWithClient(nil, nil) // nil client and logger take the defaults
			err := p.Init(context.Background(), nil, tc.opts)
			if err == nil || !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Fatalf("Init() error = %v, want substring %q", err, tc.wantErrSub)
			}
		})
	}
}

type httpFetchTestCase struct {
	name           string
	method         string
	status         int
	respBody       string
	token          string
	secrets        map[string]string
	dims           []int
	cfg            map[string]any
	cancelCtx      bool
	badURL         bool
	wantVal        float64
	wantMethod     string
	wantDims       string
	wantAuth       string
	wantBodySub    string
	wantErrSub     string
	wantStatusCode int
}

type httpCapturedRequest struct{ method, dims, auth, body string }

func assertHTTPFetchError(t *testing.T, err error, tc httpFetchTestCase) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), tc.wantErrSub) {
		t.Fatalf("Fetch() error = %v, want substring %q", err, tc.wantErrSub)
	}
	if tc.wantStatusCode != 0 {
		var sc provider.HTTPStatusCoder
		if !errors.As(err, &sc) || sc.StatusCode() != tc.wantStatusCode {
			t.Fatalf("want status code %d, got %v", tc.wantStatusCode, err)
		}
	}
}

func assertHTTPFetchSuccess(t *testing.T, data any, tc httpFetchTestCase, got httpCapturedRequest) {
	t.Helper()
	resMap, ok := data.(map[string]any)
	if !ok || resMap["temperature"] != tc.wantVal {
		t.Fatalf("unexpected data: %+v", data)
	}
	if got.method != tc.wantMethod || got.dims != tc.wantDims || got.auth != tc.wantAuth || !strings.Contains(got.body, tc.wantBodySub) {
		t.Fatalf("request method=%q dims=%q auth=%q body=%q; want method=%q dims=%q auth=%q body containing %q",
			got.method, got.dims, got.auth, got.body, tc.wantMethod, tc.wantDims, tc.wantAuth, tc.wantBodySub)
	}
}

func runHTTPFetchCase(t *testing.T, tc httpFetchTestCase) {
	t.Helper()
	var got httpCapturedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tc.cancelCtx {
			<-r.Context().Done()
			return
		}
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("io.ReadAll: %v", err)
		}
		got = httpCapturedRequest{method: r.Method, dims: r.Header.Get("X-Widget-Dimensions"), auth: r.Header.Get("Authorization"), body: string(b)}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(tc.status)
		_, _ = w.Write([]byte(tc.respBody))
	}))
	defer srv.Close()

	endpoint := srv.URL
	if tc.badURL {
		endpoint = "://invalid-url"
	}
	p := provider.NewHTTPProviderWithClient(srv.Client(), nil)
	if err := p.Init(context.Background(), tc.cfg, httpOpts(endpoint, tc.method, tc.token, tc.dims, tc.secrets, sampleResponseSchema())); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	ctx := context.Background()
	if tc.cancelCtx {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		cancel()
	}

	data, err := p.Fetch(ctx)
	if tc.wantErrSub != "" {
		assertHTTPFetchError(t, err, tc)
		return
	}
	if err != nil {
		t.Fatalf("Fetch() unexpected error: %v", err)
	}
	assertHTTPFetchSuccess(t, data, tc, got)
}

func TestHTTPProvider_Fetch(t *testing.T) {
	t.Parallel()
	tests := []httpFetchTestCase{
		{
			name: "fetch_post_ok", method: "POST", status: 200, respBody: `{"temperature":72.5,"humidity":45.0,"status":"good"}`, token: "secret-token-123",
			dims: []int{2, 1}, cfg: map[string]any{"entity_id": "sensor.lr", "unit": "F"},
			wantVal: 72.5, wantMethod: "POST", wantDims: "2x1", wantAuth: "Bearer secret-token-123", wantBodySub: `"entity_id":"sensor.lr"`,
		},
		{
			name: "fetch_get_ok", method: "GET", status: 200, respBody: `{"temperature":68.0,"humidity":50.0}`, secrets: map[string]string{"token_env": "env-token-456"},
			wantVal: 68.0, wantMethod: "GET", wantAuth: "Bearer env-token-456",
		},
		{
			name: "token_env_dims", method: "POST", status: 200, respBody: `{"temperature":70.0,"humidity":40.0}`, secrets: map[string]string{"token_env": "secret-from-env"},
			dims: []int{3, 2}, wantVal: 70.0, wantMethod: "POST", wantDims: "3x2", wantAuth: "Bearer secret-from-env",
		},
		{name: "status_503", method: "GET", status: 503, respBody: "upstream unavailable", wantErrSub: "upstream returned http status 503: upstream unavailable", wantStatusCode: 503},
		{name: "status_400", method: "GET", status: 400, respBody: "bad request", wantErrSub: "upstream returned http status 400: bad request", wantStatusCode: 400},
		{name: "invalid_json", method: "GET", status: 200, respBody: "not valid json", wantErrSub: "invalid response JSON"},
		{name: "non_object_json", method: "GET", status: 200, respBody: "[1, 2, 3]", wantErrSub: "response body must be a JSON object"},
		{name: "schema_fail", method: "GET", status: 200, respBody: `{"temperature":75.0}`, wantErrSub: "response_schema validation failed"},
		{name: "cancelled_context", method: "GET", status: 200, respBody: "{}", cancelCtx: true, wantErrSub: "context"},
		{name: "marshal_cfg_err", method: "POST", status: 200, respBody: "{}", cfg: map[string]any{"bad": make(chan int)}, wantErrSub: "failed to marshal domain config"},
		{name: "bad_request_url", method: "GET", status: 200, respBody: "{}", badURL: true, wantErrSub: "failed to create HTTP request"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runHTTPFetchCase(t, tc)
		})
	}
}
