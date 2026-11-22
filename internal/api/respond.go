package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// WriteJSON writes v as a JSON response body with the given status code. Encoding failures happen after the
// status line is sent, so they are logged rather than reported to the client.
func WriteJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("failed to encode json response", "error", err)
	}
}

// WriteError writes the standard ErrorResponse body {"status":"error","error":msg} with the given status code.
func WriteError(w http.ResponseWriter, code int, msg string) {
	WriteJSON(w, code, ErrorResponse{Status: "error", Error: msg})
}

// AllowMethods sets the shared CORS headers and reports whether r.Method is one of methods. An OPTIONS
// preflight is answered with 204 and any other method with 405, an Allow header and an ErrorResponse; both
// return false so the caller stops handling the request.
func AllowMethods(w http.ResponseWriter, r *http.Request, methods ...string) bool {
	allow := strings.Join(append(slices.Clone(methods), http.MethodOptions), ", ")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", allow)
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return false
	}
	if slices.Contains(methods, r.Method) {
		return true
	}
	w.Header().Set("Allow", allow)
	WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
	return false
}

// UpstreamHTTPError is a non-2xx response from an upstream HTTP service. URL and Body are optional context;
// the URL is printed without credentials or query string.
type UpstreamHTTPError struct {
	Code int
	URL  string
	Body string
}

func (e UpstreamHTTPError) Error() string {
	msg := fmt.Sprintf("upstream returned http status %d", e.Code)
	if e.URL != "" {
		msg += " for " + SanitizeURL(e.URL)
	}
	if e.Body != "" {
		msg += ": " + e.Body
	}
	return msg
}

// StatusCode returns the upstream HTTP status code.
func (e UpstreamHTTPError) StatusCode() int { return e.Code }

// SanitizeURL strips user info and the query string from rawURL so it is safe to log. Unparsable input is
// returned unchanged.
func SanitizeURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u == nil {
		return rawURL
	}
	u.User, u.RawQuery = nil, ""
	return u.String()
}
