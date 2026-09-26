# Architecture Decision Record: Batch 1 Specification Alignment & Codebase Synchronization

- **Date:** 2026-09-26
- **Status:** Accepted
- **Context:** Resolving Issues #259, #260, #276, #282, and #283 on `azylman/mirrormere`, aligning SPEC-004, SPEC-006, SPEC-010, SPEC-013, deploy configs, and sidecar environment parsing with delivered implementation.

---

## 1. Problem Statement
During initial architectural code review sweeps by Mike Carmody (`@arbiter`) and Amos, several discrepancies between the architecture specifications and the shipped codebase were identified across five distinct areas:

1. **Issue #259 (SPEC-006 `header.update` Weather Optionality)**: SPEC-006 §2.B still documented `weather` as `(object, required)`, even though `api/schemas/header.update.json` and the Go backend (`HeaderUpdateEvent`) treat `weather` as optional (`omitempty`) when `display.header.weather` is unconfigured or not yet hydrated.
2. **Issue #260 (SPEC-013 Task 3.5 External Task Adapters)**: SPEC-013 Task 3.5 referenced Todoist and Home Assistant adapters, while SPEC-008 §4 canonically specifies `gtasks` and `http` list source adapters syncing into SQLite.
3. **Issue #276 (SPEC-004 Video HUD Auto-Fade Timeout)**: SPEC-004 §2 prescribed a 3-second auto-fade timeout for the touch HUD overlay, whereas SPEC-013, `web/static/js/video_hud.js`, unit tests, and the touch HUD ADR standardized on 5 seconds (5,000ms).
4. **Issue #282 (SPEC-010 Packaging & Systemd Bootstrap)**: SPEC-010 still prescribed a systemd `getty@tty1.service` auto-login drop-in override, which had been superseded by `mirrormere-kiosk.service` declaring `Conflicts=getty@tty1.service`, `PAMName=login`, `TTYPath=/dev/tty1`, and `loginctl enable-linger kiosk`.
5. **Issue #283 (SPEC-004 WebRTC Stream URL & Sidecar Control URL)**: SPEC-004 referenced `/cast` as the go2rtc WebRTC stream endpoint (which in go2rtc is `/api/webrtc?src=cast`) and referenced `CAST_WATCHER_CONTROL_URL` instead of `CONTROL_URL` used in `deploy/compose.yml` and `sidecars/cast-watcher/main.go`.

---

## 2. Decisions & Implemented Changes

### A. Issue #259: Realtime Comms Alignment (SPEC-006)
- **Section 2.B (`header.update`)**:
  - Updated `weather` property specification from `(object, required)` to `(object, optional)`.
  - Documented that `weather` is omitted when `display.header.weather` is unset in configuration or prior to the initial weather fetch.
  - Specified client behavior: render the top-banner weather pill when present, and preserve prior state or remain hidden when omitted.
- **Section 3 (Item 3 - Initial State Hydration)**:
  - Documented that `weather` is omitted from initial hydration `header.update` events when unconfigured or not yet hydrated.

### B. Issue #260: Implementation Roadmap Alignment (SPEC-013)
- **Task 3.5 (External Task List Ingest Adapters)**:
  - Replaced mentions of Todoist and Home Assistant with `gtasks` and `http` list source adapters syncing into SQLite, achieving 100% fidelity with SPEC-008 §4.

### C. Issue #276: Touch HUD Auto-Fade Inactivity Window (SPEC-004)
- **Section 2 (Touch HUD & Transport Controls)**:
  - Updated HUD overlay auto-fade inactivity timeout from 3 seconds to 5 seconds to align with SPEC-013 Milestone 4, `web/static/js/video_hud.js`, and test suites.

### D. Issue #282: Touch Kiosk Packaging & Bootstrap (SPEC-010)
- **Section 2 (Packaging & Systemd Bootstrap)**:
  - Removed outdated `getty@tty1.service.d/override.conf` auto-login recommendation.
  - Added `loginctl enable-linger kiosk` to System Prerequisites.
  - Documented `mirrormere-kiosk.service` architecture: direct TTY binding to `/dev/tty1`, `Conflicts=getty@tty1.service`, and `PAMName=login`.

### E. Issue #283: go2rtc WebRTC Endpoint & Cast Watcher Control URL (SPEC-004, deploy, sidecars)
- **SPEC-004 & SPEC-006**:
  - Updated all occurrences of `http://127.0.0.1:1984/cast` to `http://127.0.0.1:1984/api/webrtc?src=cast`.
  - Updated `sidecars/cast-watcher` control URL references to `CONTROL_URL` with backward-compatible fallback to `CAST_WATCHER_CONTROL_URL`.
- **`deploy/go2rtc.yaml`**:
  - Updated comments to reference `/api/webrtc?src=cast`.
- **`sidecars/cast-watcher/main.go`**:
  - Added fallback evaluation for `CAST_WATCHER_CONTROL_URL` if `CONTROL_URL` is empty.
- **`sidecars/cast-watcher/main_test.go`**:
  - Added unit test coverage for `CONTROL_URL` and `CAST_WATCHER_CONTROL_URL` fallback, ensuring 96.0% statement test coverage.

---

## 3. Consequences
- Authoritative documentation across SPEC-004, SPEC-006, SPEC-010, and SPEC-013 now matches the implemented code and schemas.
- Downstream deployment runbooks and host provisioning scripts have zero conflicting instructions regarding systemd service isolation or go2rtc endpoint paths.
- All pre-flight verification gates, linters, and unit test suites remain green.
