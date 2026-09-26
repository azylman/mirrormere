# Architecture Decision Record: Unified Video Stream API, Role-Based Priority Stack, and Sidecar Action Forwarding

- **Date:** 2026-09-26
- **Status:** Accepted
- **Context:** Implementing Issue #251 on `azylman/mirrormere` (SPEC-013 Phase 4, Chunk 4.2), fulfilling SPEC-004 §1–§4 and SPEC-006 §4.

---

## 1. Problem Statement
Prior to Chunk 4.2:
1. **Multi-Feed Collisions & Priority Absence**: Smart displays manage multiple simultaneous video sources, including long-running persistent media (Chromecast) and high-priority temporary alerts (doorbell cameras). Without an authoritative priority stack, incoming alerts either abruptly terminate active media playback or fail to display entirely.
2. **Coupled Hardware Capture**: Direct browser-level capture APIs (`getUserMedia`) couple display clients to physical host dongles, require interactive browser permissions, and break remote testing. A hardware-agnostic stream abstraction feeding into a unified API was required.
3. **Transport Control Proxy Gap**: Display clients and touch HUD overlays require the ability to send transport commands (play, pause, toggle_playback) back to stream producer sidecars (e.g. `sidecars/cast-watcher`). Core needed an HTTP action forwarding proxy with strict timeouts, stream existence validation, and status classification.
4. **OpenAPI Contracts & State Hydration Missing**: Mirrormere lacked OpenAPI definitions for `/api/video/trigger`, `/api/video/dismiss`, `/api/video/action`, and `/api/video/state`. Furthermore, cold-booting and reconnecting kiosks lacked video presentation state hydration during initial connection handshakes (`evt_init_04`).

---

## 2. Decision & Architecture

### A. Role-Based Video Priority Stack Coordinator (`internal/video/coordinator.go`)
- Thread-Safe In-Memory Stack: `Coordinator` tracks active `primary` and `pip` streams and display `mode` ("widgets" vs "video") guarded by `sync.RWMutex`.
- Precedence Hierarchy:
  - `persistent` streams (e.g. Chromecast) have absolute priority over `temporary` alert streams.
  - The Primary slot commands fullscreen focus with unmuted audio (`Muted: false`).
  - The PiP slot is a floating corner overlay with audio strictly muted (`Muted: true`).
- State Transitions & Slot Management:
  - Idle -> Persistent: Transitions to `video` mode; persistent stream takes Primary fullscreen unmuted.
  - Idle -> Temporary: Transitions to `video` mode; temporary alert takes Primary fullscreen unmuted for `timeout_seconds` (default 45s).
  - Persistent Active + Temporary: Persistent stream remains in Primary unmuted; incoming alert enters PiP with `Muted: true`.
  - Temporary in Primary + Persistent Trigger: Existing temporary stream is demoted to PiP with `Muted: true`, retaining its active timer. Persistent stream takes Primary unmuted.
  - Persistent Dismissal with Active PiP: Alert stream in PiP is promoted to Primary fullscreen with audio unmuted (`Muted: false`), while its auto-dismiss timer continues running.
- Auto-Dismiss Timer Hygiene:
  - Timers are tracked per stream ID.
  - When temporary streams expire, they are cleanly unmounted from their slot. If no streams remain, mode returns to `widgets`.
  - Re-triggering or explicitly dismissing a stream cancels its previous timer immediately.
  - Evicting an occupied PiP slot cancels the evicted stream's timer.
- Lock Inversion Prevention & Hydration Sync:
  - State mutations execute under write lock, snapshot the state, release the lock, and publish `video.state` SSE events to `events.Hub`.
  - Synchronizes `VideoStateSink` directly upon cold boot (`{mode: "widgets", primary: nil, pip: nil}`) and every mutation, ensuring `InMemoryStateProvider` feeds fresh state to `evt_init_04`.

### B. Sidecar Transport Action Forwarding (`internal/video/coordinator.go`, `internal/server/video.go`)
- Stream Existence & Controllability Guard:
  - Verifies target stream ID is active in Primary or PiP; returns 404 Not Found if missing.
  - Verifies `controllable: true` and non-empty `control_url`; returns 422 Unprocessable Entity if non-controllable.
- HTTP Proxy Execution:
  - Releases coordinator lock prior to dispatching outbound HTTP requests.
  - Forwards action payload `{"id": id, "action": action, "value": value}` to `control_url` under a strict 2-second timeout context (`context.WithTimeout(ctx, 2*time.Second)`).
  - Returns 200 OK on 2xx response, or 502 Bad Gateway if the sidecar is unreachable, times out, or returns a non-2xx status.

### C. OpenAPI 3.1 & REST Server Architecture (`api/openapi.yaml`, `internal/server/video.go`)
- Schema Contracts:
  - `api/schemas/video.state.json`: Draft 2020-12 schema with required `["mode", "primary", "pip"]`, mode enum `["widgets", "video"]`, and nullable stream objects with `additionalProperties: false`.
  - OpenAPI Schemas: Added `VideoStream`, `VideoStateResponse`, `VideoTriggerRequest`, `VideoDismissRequest`, `VideoActionRequest`, `VideoPlayerStateRequest`, `VideoPlayerStateResponse`, and `ActionResponse`.
- Endpoints:
  - `POST /api/video/trigger`: Validates payload, mounts/updates stream in priority stack, and returns 200 OK with `VideoStateResponse`.
  - `POST /api/video/dismiss`: Dismisses stream by ID, handles promotion/idle return, and returns 200 OK or 404 Not Found.
  - `POST /api/video/action`: Forwards transport action to sidecar; maps errors to 400, 404, 422, or 502.
  - `GET /api/video/state`: Returns current presentation snapshot with 200 OK.
  - `POST /api/video/state`: Producer webhook updating `player_state` on active streams and broadcasting updated `video.state`.
- Multiplexed Routing:
  - Mounted `/api/video/state` to dispatch GET and HEAD requests to `GetVideoState` and POST requests to `PostVideoState`, handling OPTIONS preflight with 204 No Content.
- Server Integration:
  - Added `VideoHandler` interface to `internal/server/server.go` with `RegisterVideoHandler` dynamic configuration support.
  - Implemented `DefaultVideoHandler` in `internal/server/video.go` with CORS support and consistent `ErrorResponse` formatting.

---

## 3. Verification & Compliance
- **Hermetic Unit Testing**:
  - `internal/video/coordinator_test.go`: Verified defaults, input validation, priority stack transitions (idle -> cast, cast -> doorbell PiP, doorbell eviction, re-trigger update, cast stop with active PiP promotion, idle return), timer expirations, player state updates, action forwarding proxying (200, 404, 422, 502), concurrent stress testing, and schema conformance against `api/schemas/video.state.json`. Zero `time.Sleep` via mock `TimerFunc`. Statement coverage: 95.6%.
  - `internal/server/video_test.go`: Verified 200, 204, 400, 404, 405, 422, 500, and 502 status codes across all endpoints, CORS options preflight, HEAD requests, and server routing. Statement coverage: 97.0%.
  - `internal/api/api_test.go`: Regenerated Go contracts via `oapi-codegen` with zero drift and verified middleware execution. Statement coverage: 95.5%.
  - `internal/events/hub_test.go`: Verified `EventVideoState` event publishing synchronizes `InMemoryStateProvider`. Statement coverage: 96.0%.
- **Zero Invariant Violations**: Zero markdown tables, zero plaintext tokens, and statement coverage >= 95.0% maintained across all modified and new packages.
