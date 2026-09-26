# Architecture Decision Record: Rotation Publish Synchronization & Pause Validation

- **Date:** 2026-09-25
- **Status:** Accepted
- **Context:** Resolving Issues #203 and #204 on `azylman/mirrormere`:
  - Issue #203: `fix(rotation): screen.rotate published outside the lock can go out of order, leaving displays and hydration on a stale screen`
  - Issue #204: `fix(rotation): /api/screen/pause treats missing 'paused' as false and resumes instead of returning 400`

---

## 1. Context & Inconsistency

Under SPEC-006 §2.C, §3, and §8:
- Each screen navigation action must broadcast a `screen.rotate` SSE event whose payload authoritatively matches the server's active screen.
- Initial hydration carries the active screen, which is cached by the event hub from published `screen.rotate` events.
- In `POST /api/screen/pause`, the `paused` field is strictly required (`paused` boolean, required). Omitted or non-boolean payloads must return HTTP 400 Bad Request with error `"invalid payload: paused must be a boolean"`.

Prior to this change:
1. **Out-of-Order Screen Rotation Broadcasts (Issue #203):**
   In `internal/rotation/coordinator.go`, mutator methods (`SelectScreen`, `AdvanceScreen`, `UpdateConfig`, `onRotateTimeout`) modified `currentScreen` while holding `c.mu.Lock()`, but unlocked `c.mu` *before* invoking `c.publishRotate(data)`. When two mutations occurred concurrently (e.g. periodic rotation timer timeout racing against manual button/API `/api/screen/advance` or configuration reload), their `screen.rotate` events could be broadcast in reverse order from their state mutations. Displays and hydration state were left stuck on the stale screen, with manual mode (`interval_seconds: 0`) unable to recover.
2. **Missing `paused` Field Validation in Pause Endpoint (Issue #204):**
   In `internal/rotation/handler.go`, `PostScreenPause` decoded JSON directly into `api.ScreenPauseRequest`, where `paused` was typed as a plain Go `bool`. When `paused` was omitted (e.g. `{}` or `{"duration_seconds": 300}`), Go's JSON decoder defaulted `paused` to `false`, causing the endpoint to resume rotation with HTTP 200 rather than rejecting the payload with HTTP 400.

---

## 2. Decision

1. **Synchronous Broadcast Inside State Lock (`internal/rotation/coordinator.go`)**:
   - Call `c.publishRotateLocked(data)` directly inside `c.mu.Lock()` before releasing the mutex in `SelectScreen`, `AdvanceScreen`, `UpdateConfig`, and `onRotateTimeout`.
   - `events.Hub.Publish` performs non-blocking channel fanout to subscribers and in-memory updates without re-entering `Coordinator`. Holding `c.mu` during publication is completely deadlock-free and guarantees strict serialization: state changes and event broadcasts are atomic with respect to each other, preventing race inversions.

2. **Pointer-Based `paused` Validation (`internal/rotation/handler.go`)**:
   - Decode request payload into a local struct with `Paused *bool`.
   - If JSON decoding fails (e.g. non-boolean string or malformed syntax) or `req.Paused == nil` (omitted field), return HTTP 400 Bad Request with error `"invalid payload: paused must be a boolean"`.
   - Pass validated boolean down to `PauseRotation` or `ResumeRotation`.

---

## 3. Verification & Test Evidence

- Added unit tests in `internal/rotation/handler_test.go`:
  - `Missing paused field in empty body returns 400`
  - `Missing paused field with duration returns 400`
  - `Non-boolean paused field returns 400`
- Added race test in `internal/rotation/coordinator_test.go`:
  - `TestCoordinator_ConcurrentTimerAndAdvanceScreen`: Concurrently runs periodic rotation timeouts, sequential screen advances, and direct selects, asserting that the last broadcast `screen.rotate` event strictly matches `Coordinator.CurrentScreen()`.
- Monorepo verification (`./scripts/verify.sh --staged`):
  - Codegen zero-drift verified
  - `go vet` and `golangci-lint` clean
  - Monorepo statement test coverage: **97.27%** (floor: 95.0%, `internal/rotation` at **97.82%**).
