// Package httpx holds the bounded HTTP fetch shared by mirrormere providers.
package httpx

import (
	"context"
	"io"
	"net/http"
)

// GetBounded sends req (bound to ctx) through client, reads at most maxBytes of
// the response body, closes it and returns the body with the status code.
//
// A non-2xx status is not an error: callers decide. Errors are returned
// unwrapped so callers keep their own wording. The status is 0 exactly when
// the request itself failed; a read failure returns the response status, which
// lets callers word transport and read failures differently.
func GetBounded(ctx context.Context, client *http.Client, req *http.Request, maxBytes int64) ([]byte, int, error) {
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req.WithContext(ctx))
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}
