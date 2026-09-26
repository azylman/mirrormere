# ADR: HTTP Provider Upstream Error Classification & Backoff Scheduling (Issue #236)

## Status
Accepted

## Context & Problem Statement
SPEC-003 §3.2 and the Chunk 3.1 architecture define retry policies for out-of-process HTTP providers: transient upstream faults (such as network drops, timeouts, HTTP 5xx server errors, and HTTP 429 rate limits) must schedule retries with exponential backoff rather than waiting for the normal polling interval. Permanent client errors (such as 400 Bad Request, 401 Unauthorized, 403 Forbidden, 404 Not Found) or schema validation failures retain the configured `refresh_interval_seconds` because repeating an invalid request without configuration changes cannot succeed.

In Issue #236, on non-2xx responses `HTTPProvider.Fetch` was returning untyped errors created via `fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)`. Because this untyped error did not implement `provider.HTTPStatusCoder`, and the text format did not match the legacy substring patterns in `IsNetworkOrServerError` (which looked for `"status 503"` or `"503 service unavailable"`), all 5xx and 429 responses were misclassified as permanent errors. As a result, `ComputeRetryDelay` returned the full interval cap (e.g. 900 seconds / 15 minutes) instead of backing off (e.g. 5 seconds), causing widgets to remain degraded for long periods following transient sidecar restarts.

## Decisions

### 1. Introduce Typed `HTTPStatusError`
We introduce a concrete error type in `internal/provider/http.go`:
- `type HTTPStatusError struct { Code int; Body string }`
- Implements `error` via `Error() string` (formatting as `HTTP <code>: <body>`, or `HTTP <code>` if body is empty).
- Implements `provider.HTTPStatusCoder` via `StatusCode() int`.

When `HTTPProvider.Fetch` receives an upstream response with status `< 200` or `>= 300`, it returns `&HTTPStatusError{Code: resp.StatusCode, Body: strings.TrimSpace(string(bodyBytes))}`.

### 2. Status Coder Evaluation & Pattern Hardening
In `internal/provider/backoff.go`:
- `IsNetworkOrServerError` inspects `HTTPStatusCoder` via `errors.As`. For status codes `>= 500` or `== 429`, it returns `true` (transient fault), triggering exponential backoff.
- For client error status codes (`400..499` excluding `429`), it returns `false` (permanent fault), retaining the normal polling interval.
- As a defensive fallback, `IsNetworkOrServerError` also includes `"http 500"`, `"http 502"`, `"http 503"`, `"http 504"`, and `"http 429"` string patterns.

## Technical Rationale
- **Spec Adherence**: Satisfies SPEC-003 §3.2 and restores intended exponential backoff during sidecar restarts and network glitches.
- **Fast Self-Healing**: Transient 502/503 errors during container rollout or sidecar restarts trigger retry within ~5s (with jitter), restoring widgets to healthy state in seconds rather than 15 minutes.
- **Zero Allocations & Clean Type Assertions**: Using `errors.As` with `HTTPStatusCoder` avoids brittle string parsing while preserving full error message context in logs.

## Verification
- Unit tests in `internal/provider/http_test.go` verifying that non-2xx responses return `*HTTPStatusError` implementing `HTTPStatusCoder`.
- Tests verifying that 503 responses trigger backoff (< 7s vs 900s interval cap) while 400 responses retain the 900s interval.
- Table-driven unit tests in `internal/provider/backoff_test.go` covering `HTTPStatusError` and string patterns.
