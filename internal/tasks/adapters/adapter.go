package adapters

import (
	"context"
	"errors"
	"fmt"

	"github.com/azylman/mirrormere/internal/tasks"
)

// Standard constants and errors for list ingestion adapters.
const (
	// MaxPayloadBytes enforces a strict 2MB Slowloris ceiling on external payloads per SPEC-008.
	MaxPayloadBytes = 2 * 1024 * 1024
)

var (
	// ErrPayloadTooLarge is returned when an upstream payload exceeds MaxPayloadBytes.
	ErrPayloadTooLarge = errors.New("payload exceeds maximum permitted size of 2MB")
	// ErrUpstreamNotFound is returned when an upstream list or endpoint returns 404 Not Found.
	ErrUpstreamNotFound = errors.New("upstream task list not found")
	// ErrUnauthorized is returned when upstream returns 401 Unauthorized or 403 Forbidden (e.g. expired OAuth token).
	ErrUnauthorized = errors.New("upstream unauthorized: token expired or invalid")
)

// UpstreamHTTPError represents an HTTP error status returned by an upstream list provider.
// Implements provider.HTTPStatusCoder for integration with exponential backoff policies.
type UpstreamHTTPError struct {
	Code int
	URL  string
}

// Error formats the upstream HTTP status error with URL query sanitization.
func (e UpstreamHTTPError) Error() string {
	return fmt.Sprintf("upstream returned http status %d for %s", e.Code, sanitizeURL(e.URL))
}

// StatusCode returns the HTTP status code.
func (e UpstreamHTTPError) StatusCode() int {
	return e.Code
}

// Adapter defines the external task list ingestion contract per SPEC-008 §Source Adapters.
type Adapter interface {
	Name() string
	FetchList(ctx context.Context) (*tasks.List, []tasks.ListItem, error)
}
