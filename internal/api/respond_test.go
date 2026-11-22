package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteJSON(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		code     int
		v        any
		wantBody string
	}{
		{"struct_ok", http.StatusOK, ActionResponse{Status: "ok"}, "{\"status\":\"ok\"}\n"},
		{"map_keys_sorted", http.StatusCreated, map[string]any{"status": "ok", "widget_id": "w1", "updated_at": "t"}, "{\"status\":\"ok\",\"updated_at\":\"t\",\"widget_id\":\"w1\"}\n"},
		{"unencodable_logged", http.StatusOK, make(chan int), ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			WriteJSON(rec, tc.code, tc.v)
			if rec.Code != tc.code || rec.Header().Get("Content-Type") != "application/json" || rec.Body.String() != tc.wantBody {
				t.Fatalf("got %d %q %q, want %d application/json %q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String(), tc.code, tc.wantBody)
			}
		})
	}
}

func TestWriteError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		code     int
		msg      string
		wantBody string
	}{
		{"bad_request", http.StatusBadRequest, "invalid payload", "{\"status\":\"error\",\"error\":\"invalid payload\"}\n"},
		{"internal_empty", http.StatusInternalServerError, "", "{\"status\":\"error\",\"error\":\"\"}\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			WriteError(rec, tc.code, tc.msg)
			if rec.Body.String() != tc.wantBody {
				t.Fatalf("body = %q, want %q", rec.Body.String(), tc.wantBody)
			}
			var got ErrorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got != (ErrorResponse{Status: "error", Error: tc.msg}) || rec.Code != tc.code {
				t.Fatalf("decoded %+v (err %v) code %d, want error %q code %d", got, err, rec.Code, tc.msg, tc.code)
			}
		})
	}
}

func TestAllowMethods(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		method      string
		methods     []string
		wantOK      bool
		wantCode    int
		wantMethods string
		wantAllow   string
		wantBody    string
	}{
		{"get_allowed", http.MethodGet, []string{http.MethodGet, http.MethodHead}, true, http.StatusOK, "GET, HEAD, OPTIONS", "", ""},
		{"head_allowed", http.MethodHead, []string{http.MethodGet, http.MethodHead}, true, http.StatusOK, "GET, HEAD, OPTIONS", "", ""},
		{"preflight", http.MethodOptions, []string{http.MethodPost}, false, http.StatusNoContent, "POST, OPTIONS", "", ""},
		{"post_on_get_route", http.MethodPost, []string{http.MethodGet, http.MethodHead}, false, http.StatusMethodNotAllowed, "GET, HEAD, OPTIONS", "GET, HEAD, OPTIONS", "{\"status\":\"error\",\"error\":\"method not allowed\"}\n"},
		{"get_on_delete_route", http.MethodGet, []string{http.MethodDelete}, false, http.StatusMethodNotAllowed, "DELETE, OPTIONS", "DELETE, OPTIONS", "{\"status\":\"error\",\"error\":\"method not allowed\"}\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			ok := AllowMethods(rec, httptest.NewRequest(tc.method, "/x", http.NoBody), tc.methods...)
			h := rec.Header()
			if ok != tc.wantOK || rec.Code != tc.wantCode || rec.Body.String() != tc.wantBody {
				t.Fatalf("got ok=%v %d %q, want ok=%v %d %q", ok, rec.Code, rec.Body.String(), tc.wantOK, tc.wantCode, tc.wantBody)
			}
			if h.Get("Access-Control-Allow-Origin") != "*" || h.Get("Access-Control-Allow-Headers") != "Content-Type, Accept" ||
				h.Get("Access-Control-Allow-Methods") != tc.wantMethods || h.Get("Allow") != tc.wantAllow {
				t.Fatalf("unexpected headers %v", h)
			}
		})
	}
}

func TestUpstreamHTTPError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		err      UpstreamHTTPError
		wantMsg  string
		wantCode int
	}{
		{"code_only", UpstreamHTTPError{Code: 404}, "upstream returned http status 404", 404},
		{"url_sanitized", UpstreamHTTPError{Code: 503, URL: "https://user:pass@example.com/api?token=secret"}, "upstream returned http status 503 for https://example.com/api", 503},
		{"body", UpstreamHTTPError{Code: 500, Body: "boom"}, "upstream returned http status 500: boom", 500},
		{"url_and_body", UpstreamHTTPError{Code: 429, URL: "http://h/p", Body: "slow down"}, "upstream returned http status 429 for http://h/p: slow down", 429},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.err.Error(); got != tc.wantMsg {
				t.Fatalf("Error() = %q, want %q", got, tc.wantMsg)
			}
			if got := tc.err.StatusCode(); got != tc.wantCode {
				t.Fatalf("StatusCode() = %d, want %d", got, tc.wantCode)
			}
		})
	}
}

func TestSanitizeURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, in, want string
	}{
		{"strips_userinfo_and_query", "https://u:p@example.com/a/b?token=x", "https://example.com/a/b"},
		{"plain", "http://h/p", "http://h/p"},
		{"unparsable_unchanged", "http://[::1", "http://[::1"},
		{"empty", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := SanitizeURL(tc.in); got != tc.want {
				t.Fatalf("SanitizeURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
