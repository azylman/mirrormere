package server_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/azylman/mirrormere/internal/server"
)

// These tests share the process-wide default registry that /metrics exposes, so they
// assert deltas on the histograms rather than absolute counts and do not run in parallel.

func doPerfReq(srv *server.Server, method, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/api/perf", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	return rec
}

// perfHistogramCount returns the current _count of a display timing histogram series, parsed
// from the /metrics exposition, or 0 if the series has not been observed yet.
func perfHistogramCount(t *testing.T, srv *server.Server, series string) float64 {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", http.NoBody))
	for line := range strings.SplitSeq(rec.Body.String(), "\n") {
		if !strings.HasPrefix(line, series+" ") {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimPrefix(line, series+" "), 64)
		if err != nil {
			t.Fatalf("parse %q: %v", line, err)
		}
		return v
	}
	return 0
}

func TestPerf_Post(t *testing.T) {
	srv := server.New(server.Config{})
	tests := []struct {
		name       string
		method     string
		body       string
		wantStatus int
		wantAllow  string
		wantDeltas map[string]float64
	}{
		{
			name: "valid_batch_observes_histograms", method: http.MethodPost, wantStatus: http.StatusNoContent,
			body:       `{"samples":[{"metric":"swipe_settle","value_ms":42.5,"ui":"legacy"},{"metric":"frame","value_ms":16.7,"ui":"react"}]}`,
			wantDeltas: map[string]float64{`mirrormere_display_swipe_settle_ms_count{ui="legacy"}`: 1, `mirrormere_display_frame_ms_count{ui="react"}`: 1},
		},
		{
			// Upper bounds are inclusive, so 16.7 lands in the le="16.7" bucket.
			name: "bucket_includes_boundary", method: http.MethodPost, wantStatus: http.StatusNoContent,
			body:       `{"samples":[{"metric":"screen_ready","value_ms":16.7,"ui":"legacy"}]}`,
			wantDeltas: map[string]float64{`mirrormere_display_screen_ready_ms_bucket{ui="legacy",le="16.7"}`: 1},
		},
		{
			name: "rejected_batch_observes_nothing", method: http.MethodPost, wantStatus: http.StatusBadRequest,
			body:       `{"samples":[{"metric":"long_task","value_ms":80,"ui":"legacy"},{"metric":"bogus","value_ms":1,"ui":"legacy"}]}`,
			wantDeltas: map[string]float64{`mirrormere_display_long_task_ms_count{ui="legacy"}`: 0},
		},
		{
			name: "max_batch_accepted", method: http.MethodPost, body: fullBatch(), wantStatus: http.StatusNoContent,
			wantDeltas: map[string]float64{`mirrormere_display_interaction_ms_count{ui="legacy"}`: 200},
		},
		{name: "wrong_method", method: http.MethodGet, wantStatus: http.StatusMethodNotAllowed, wantAllow: http.MethodPost},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := make(map[string]float64, len(tc.wantDeltas))
			for series := range tc.wantDeltas {
				before[series] = perfHistogramCount(t, srv, series)
			}
			rec := doPerfReq(srv, tc.method, tc.body)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if got := rec.Header().Get("Allow"); got != tc.wantAllow {
				t.Errorf("Allow = %q, want %q", got, tc.wantAllow)
			}
			for series, want := range tc.wantDeltas {
				if got := perfHistogramCount(t, srv, series) - before[series]; got != want {
					t.Errorf("%s delta = %v, want %v", series, got, want)
				}
			}
		})
	}
}

func TestPerf_Validation(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantCode int
		wantSub  string
	}{
		{"unknown_metric", `{"samples":[{"metric":"nope","value_ms":1,"ui":"legacy"}]}`, http.StatusBadRequest, "unknown metric"},
		{"bad_ui", `{"samples":[{"metric":"frame","value_ms":1,"ui":"web"}]}`, http.StatusBadRequest, "ui must be"},
		{"negative_value", `{"samples":[{"metric":"frame","value_ms":-1,"ui":"legacy"}]}`, http.StatusBadRequest, "value_ms"},
		{"empty_samples", `{"samples":[]}`, http.StatusBadRequest, "between 1 and 200"},
		{"missing_samples", `{}`, http.StatusBadRequest, "between 1 and 200"},
		{"malformed_json", `{"samples":`, http.StatusBadRequest, "invalid JSON body"},
		{"oversize_batch", oversizeBatch(), http.StatusBadRequest, "between 1 and 200"},
		{"oversize_body", `{"samples":[{"metric":"frame","value_ms":1,"ui":"legacy"}],"pad":"` + strings.Repeat("x", 70<<10) + `"}`, http.StatusBadRequest, "invalid JSON body"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := server.New(server.Config{})
			rec := doPerfReq(srv, http.MethodPost, tc.body)
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.wantCode, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantSub) {
				t.Errorf("body = %q, want substring %q", rec.Body.String(), tc.wantSub)
			}
		})
	}
}

func oversizeBatch() string {
	var b strings.Builder
	b.WriteString(`{"samples":[`)
	for i := range 201 {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"metric":"frame","value_ms":16,"ui":"legacy"}`)
	}
	b.WriteString(`]}`)
	return b.String()
}

func fullBatch() string {
	var b strings.Builder
	b.WriteString(`{"samples":[`)
	for i := range 200 {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"metric":"interaction","value_ms":%d,"ui":"legacy"}`, i)
	}
	b.WriteString(`]}`)
	return b.String()
}
