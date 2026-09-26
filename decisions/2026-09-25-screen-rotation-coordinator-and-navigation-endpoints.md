# Architecture Decision Record: Screen Rotation Coordinator & Navigation Endpoints

- **Date:** 2026-09-25
- **Status:** Accepted
- **Context:** Implementing Phase 2, Chunk 2.2 (#200) of SPEC-013 on `azylman/mirrormere`.

---

## 1. Monotonic Timer Generation Tokens & Deadlock Avoidance

### Description of Issue / Inconsistency
Periodic screen rotation timers (`time.AfterFunc` or `time.Timer.Reset`) can fire concurrently with manual navigation requests (`POST /api/screen/select` or `advance`) or pause toggles (`POST /api/screen/pause`). If an in-flight timer callback executes after a manual navigation event, it can trigger an unexpected phantom screen advance, corrupting the user's intended display state. Furthermore, invoking event hub broadcasts while holding the coordinator's state mutex creates potential AB-BA lock inversion deadlocks if SSE subscriber fan-out locks interact with coordinator locks.

### Decision Made
- Enforce monotonic timer generation tokens (`timerGen uint64` and `pauseGen uint64`) incremented under `Coordinator.mu.Lock()` on every state change, timer reset, pause, or resume.
- Timers are armed via `time.AfterFunc(interval, func() { c.onRotateTimeout(gen) })`. When the callback fires, it checks `c.timerGen == gen`; if mismatched, the callback silently drops without state mutation.
- When `total_screens <= 1`, the rotation timer remains dormant. Advancing on a single-screen layout is a safe no-op that preserves screen index 0.
- When `interval_seconds <= 0`, manual mode is engaged, and the rotation timer is disabled.
- Strictly separate state mutation from event publishing: `Coordinator` mutates state, builds the `events.ScreenRotateData` payload, and releases `c.mu.Unlock()` before calling `c.broadcaster.Publish(events.EventScreenRotate, bytes)`.

### Technical Rationale & References
- Eliminates timer races and phantom screen rotations.
- Prevents lock-order deadlocks between coordinator state locks and SSE client broadcast channels.
- References: `specs/005-screen-layout-and-rotation.md` §3–4, `specs/006-realtime-comms-and-mutations.md` §2.C.

---

## 2. Navigation HTTP Handlers, CORS & OpenAPI Contract

### Description of Issue / Inconsistency
Display HUD clients, physical pushbuttons, Home Assistant webhooks, and touch kiosk interactions require explicit navigation control endpoints to advance, select, and pause screen rotation. These endpoints must strictly adhere to the OpenAPI 3.1 contract (`api/openapi.yaml`) and support CORS preflight requests from browser HUDs.

### Decision Made
- Implemented `internal/rotation.Handler` exposing standard library HTTP endpoints:
  - `POST /api/screen/select`: Direct navigation by 0-indexed screen number (`ScreenSelectRequest`), validating screen bounds `[0, total_screens - 1]`.
  - `POST /api/screen/advance`: Sequential navigation forward (`"next"`) or backward (`"prev"`), defaulting to `"next"` if request body is empty or omitted.
  - `POST /api/screen/pause`: Suspends automatic rotation with support for optional `duration_seconds` auto-resume timer, indefinite pause (`duration_seconds: 0`), or fallback to configured pause duration (`duration_seconds: null`). Unpausing immediately re-arms periodic rotation.
- Configured CORS headers (`Access-Control-Allow-Origin: *`, `POST, OPTIONS`) with HTTP 204 `StatusNoContent` preflight response.
- Standardized error payloads using `api.ErrorResponse` (`{"status": "error", "error": "<msg>"}`) with HTTP 400 Bad Request on validation or controller errors, and HTTP 405 Method Not Allowed on non-POST methods.
- Registered endpoints in `internal/server.Server` via `ScreenHandler` interface with clean HTTP 404 fallback when unconfigured.

### Technical Rationale & References
- Fulfills the navigation contract specified in SPEC-006 §2.C and SPEC-013 Chunk 2.2.
- Decouples server route plumbing from concrete rotation logic via pure interface injection.
- References: `api/openapi.yaml`, `specs/001-architecture-overview.md` §2.

---

## 3. Authoritative Initial Hydration Alignment & Decoupled Config Reload

### Description of Issue / Inconsistency
On cold boot or client reconnect, `BuildHydrationBatch` in `internal/events` previously defaulted `screen.rotate` to screen 0. If the coordinator had already rotated to screen 1 or 2, a newly connecting client would initially render screen 0 and then abruptly jump to the active screen upon the next rotation event. Additionally, during dynamic configuration reloads, both `Hub.DispatchConfigReload` and `Coordinator.UpdateConfig` risked publishing redundant duplicate `screen.rotate` events.

### Decision Made
- Extended `events.StateProvider` interface with `GetScreenRotateData() *ScreenRotateData` and implemented cached rotation tracking on `InMemoryStateProvider`.
- Updated `Hub.PublishEvent` to automatically intercept `EventScreenRotate` events and update `InMemoryStateProvider.SetScreenRotateData`, ensuring subsequent initial hydration batches deliver the true active screen and placed widgets.
- Introduced `events.RotationCoordinator` interface (`UpdateConfig(snap *config.Snapshot) ScreenRotateData`) in `internal/events`, allowing `Hub.DispatchConfigReload` to delegate configuration reload to the registered coordinator, suppressing duplicate events and eliminating circular package dependencies.

### Technical Rationale & References
- Guarantees seamless initial state hydration matching active display state without visual jumps or stale screens.
- Maintains strict package decoupling: `internal/rotation` imports `internal/events`, while `internal/events` defines an interface satisfied by `internal/rotation.Coordinator`.
- References: `specs/006-realtime-comms-and-mutations.md` §3, `specs/012-live-config-reload-and-lkgc.md` §3.
