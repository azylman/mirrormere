# Architecture Decision Record: Provider Polling Coordinator & SWR Cache

- **Date:** 2026-09-25
- **Status:** Accepted
- **Context:** Implementing Chunk 3.1 of Phase 3 (Issue #210, SPEC-003 §1–3, SPEC-006 §2.A, §3.2, SPEC-012 §3, SPEC-013 Chunk 3.1).

---

## 1. Problem Statement
Mirrormere compiles into a static, zero-CGO binary (`CGO_ENABLED=0`) and decouples physical display clients from upstream data providers. Display clients must never blank, unmount, or crash when upstream services experience network timeouts, transient 5xx errors, or rate limits.

Chunk 3.1 mandates:
1. A pluggable in-process `Provider` lifecycle interface per SPEC-003: `Init`, `Fetch`, `Subscribe`, and `Shutdown`.
2. An in-memory Stale-While-Revalidate (SWR) cache that:
   - Preserves Last-Known-Good (LKG) data on fetch errors, transitioning the widget state to `degraded`.
   - Freezes and preserves the timestamp of the last successful data fetch during degraded state so clients can measure staleness.
   - Sets cold-boot fetch failures (without LKG) to `error` state with empty fallback data.
3. Exponential backoff with jitter on transient network refusal (`ECONNREFUSED`, timeouts) or upstream 5xx/429 errors.
4. A `ProviderCoordinator` managing polling sync loops for all active widget instances declared in `config.yaml`.
5. Event bus dispatch emitting `widget.update` SSE events when fresh payloads arrive or operational states transition, while suppressing redundant event spam on repeated degraded retries.
6. Zero-overhead handling of native `spacer` widgets per SPEC-005 without spinning polling tickers.
7. Hermetic unit tests achieving `>= 95.0%` statement test coverage floor across all modified and new packages.

---

## 2. Decision & Architecture

### A. Provider Lifecycle Interface (`internal/provider/provider.go`)
- `Provider` defines the lifecycle contract:
  - `Init(ctx context.Context, config map[string]any) error`: validates configuration and prepares connection handles.
  - `Fetch(ctx context.Context) (any, error)`: executes a single pull cycle and returns the raw domain data object. Returning `(any, error)` rather than the wrapped payload isolates providers from envelope management, RFC 3339 timestamps, and SWR state logic.
  - `Subscribe(ctx context.Context, eventSink chan<- WidgetPayload) error`: provides push-capable or streaming providers a channel sink for unsolicited updates. For polling-only providers, this is a clean no-op returning `nil`.
  - `Shutdown(ctx context.Context) error`: releases active network connections, timers, or background goroutines.
- `WidgetPayload` defines the canonical JSON envelope matching SPEC-003 §2 and SPEC-006 §2.A:
  `widget_id`, `timestamp` (RFC 3339), `state` (`healthy` | `degraded` | `error`), and `data` (domain object).
- `Registry` provides a thread-safe factory registry mapping provider names (e.g. `spacer`, and later `http`, `calendar-agenda`, etc.) to provider constructors.

### B. In-Memory SWR Cache (`internal/provider/cache.go`)
- `SWRCache` maintains thread-safe (`sync.RWMutex`) entries for each widget instance:
  - **Cold Boot Failure**: If a widget fails its initial fetch and has no LKG data, state transitions to `error`, data is set to empty fallback `map[string]any{}`, timestamp is set to `now`, and `transitioned` is `true`.
  - **Successful Fetch**: State transitions to `healthy`, LKG data is updated with fresh domain data, timestamp is updated to `now`, consecutive failure count is reset to 0, and `transitioned` is `true` if previous state was not `healthy`.
  - **Degraded Fetch Failure**: If a fetch fails but LKG data exists, state transitions to `degraded`. Crucially, the cached `timestamp` **retains the timestamp of the last successful fetch** per SPEC-003 §2. LKG data remains frozen and untouched.
  - **Event Deduplication**: `RecordFailure` returns `(WidgetPayload, transitioned bool)`. The coordinator only broadcasts `widget.update` when `transitioned == true`. Consecutive retries that remain in `degraded` or `error` update internal failure counts and logs without flooding the SSE bus.
  - **Cache Purge on Domain Change**: Per SPEC-012 §3, if an instance's domain parameters or type change during a live config reload, cached data is purged so old target data is never displayed under new instance configurations.

### C. Exponential Backoff with Jitter & Error Classification (`internal/provider/backoff.go`)
- `BackoffPolicy` calculates delay using `baseDelay * (multiplier ^ (failures - 1))` with proportional jitter (default ±20%).
- **Backoff Clamping**: Delay is clamped to `min(maxDelay, refresh_interval_seconds)` ensuring retries never exceed the regular polling interval.
- **Error Classification**:
  - `IsNetworkOrServerError(err)` identifies transient faults: `syscall.ECONNREFUSED`, `net.Error` (timeouts), `net.OpError`, HTTP 5xx, and HTTP 429.
  - Transient faults trigger exponential backoff.
  - Permanent client/configuration errors (HTTP 400, 401, 403, 404, or schema invalidation) wait the standard polling interval to prevent hammering remote endpoints or thrashing server logs.

### D. Provider Coordinator (`internal/provider/coordinator.go`)
- `ProviderCoordinator` manages worker goroutines for all active widgets in the configuration snapshot:
  - Maps widget instances to providers via package manifests (`pkg.Manifest.Provider`), falling back to `widget.Type`.
  - For `type: spacer`, initializes the cache once with empty data and `healthy` state, strictly skipping the creation of polling timers or goroutines per SPEC-005.
  - For polling widgets, spawns isolated worker goroutines running under cancelable contexts.
  - Timer management avoids `time.After` leaks by using managed `time.NewTimer` instances with deterministic channel draining on shutdown.
  - Manual on-demand refresh via `RefreshWidget(widgetID)` is serialized through non-blocking trigger channels into the worker loops.
  - Out-of-band push updates (`PushUpdate`) immediately update the SWR cache, state sink, and broadcast `widget.update`.
- Updates `StateSink` (`SetWidgetState`, `SetProvidersStatus`) so `render.Engine` and `events.BuildHydrationBatch` immediately reflect live state.

### E. Initial Hydration Timestamp Alignment (`internal/events/hydration.go`)
- Updated `InMemoryStateProvider` to record and expose the widget's last successful fetch timestamp alongside data and state.
- `BuildHydrationBatch` now preserves the true LKG timestamp for degraded widgets during reconnect hydration bursts rather than overwriting with `now`.
