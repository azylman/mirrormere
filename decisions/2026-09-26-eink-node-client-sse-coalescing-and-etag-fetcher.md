# Architecture Decision Record: E-Ink Node Client SSE Coalescing and ETag Fetcher

- **Date:** 2026-09-26
- **Status:** Accepted
- **Context:** Implementing Issue #246 on `azylman/mirrormere` (SPEC-013 Phase 5, Chunk 5.2), fulfilling SPEC-009 §1-§3 and SPEC-006 §1-§3.

---

## 1. Problem Statement & Motivation
Electronic paper (e-paper / E-Ink) displays possess unique physical and electrical characteristics:
1. **Thermal & Voltage Stress on Rapid Refreshes**: Frequent panel writes (whether full or partial) cause irreversible physical wear, ghosting, and voltage degradation. A 60-second minimum write floor (`min_refresh_seconds`) is essential to prevent hardware burnout.
2. **High-Frequency Upstream Event Churn**: Mirrormere Core broadcasts real-time Server-Sent Events (`GET /api/events`) for widget updates, rotations, weather polling, and audio/voice transitions. Multiple events often arrive in rapid millisecond bursts (e.g. initial connection hydration or simultaneous calendar/weather updates).
3. **Bandwidth and Display Node Compute Conservation**: Electronic paper is bistable and retains images indefinitely without power. Redundant downloading and rendering of unchanged 800×480 frames wastes network bandwidth and CPU cycles. HTTP cache validation using cryptographic ETags (`304 Not Modified`) and byte-level diffing is mandatory.
4. **Hermetic Testability**: Display node logic must be 100% testable in automated CI and pre-flight pipelines without requiring physical SPI buses, Raspberry Pi GPIO headers, or live browser sidecars.

---

## 2. Decision & Architecture

Mirrormere establishes the Ambient E-Ink Display Node Client at `clients/eink-node/` with modular, decoupled components:

### A. SSE Stream Listener (`clients/eink-node/sse.py`)
- **Connection & Protocol**: Establishes a persistent streaming connection to `GET /api/events` with `Accept: text/event-stream` and `Cache-Control: no-cache`.
- **Resumption via `Last-Event-ID`**: Tracks incoming message IDs (`id: <id>`) and supplies `Last-Event-ID: <id>` on reconnects, allowing Core to replay missed events from its ring buffer without unneeded full-state flushes.
- **Reconnection with Exponential Backoff**: On socket drops or network errors, automatically reconnects using exponential backoff starting at 1.0s and doubling up to 60.0s max (`1s -> 2s -> 4s -> 8s -> 16s -> 32s -> 60s`). Backoff resets to 1.0s immediately upon a successful connection.
- **Strict Event Filtering**:
  - **Dirty Triggers**: Marks the screen dirty on `widget.update`, `header.update`, `screen.rotate`, `widget.reload`, and `style.reload`.
  - **Non-Dirty Ignored Events**: Explicitly ignores `system.status`, `voice.state`, `audio.state`, `video.state`, and SSE comment lines (`: ping <timestamp>`). On Profile B, voice state is handled by the reSpeaker hardware LED ring, and system telemetry is logged without causing disruptive panel flashes.
  - **Comment-Only Block Discard**: Blocks containing only comments (`: ping`) are recognized by `parse_sse_block` and discarded without emitting spurious `"message"` events.

### B. Refresh Coalescing Engine (`clients/eink-node/coalesce.py`)
- **Debounce Window (`coalesce_seconds: 5`)**: When a dirty event arrives, the engine starts or resets a trailing-edge debounce timer. A burst of 5 events arriving over 3 seconds collapses into a single refresh scheduled 5 seconds after the final event.
- **Panel Write Floor (`min_refresh_seconds: 60`)**: Enforces a strict 60-second floor between consecutive physical panel writes. If a dirty event arrives 10 seconds after a panel write, the debounce window (5s) expires at second 15, but the engine delays execution until second 60.
- **Debounce Precedence**: If a new event arrives near the end of the write floor (e.g. at second 58 of 60), the 5-second debounce takes precedence (scheduling execution for second 63) to prevent splitting ongoing bursts.
- **Initial Boot Fast-Path**: On daemon startup, `last_write_time` is initialized to `-1.0` (unwritten). The initial SSE hydration batch is coalesced for 5 seconds, but does not incur an artificial 60-second cold-boot delay before rendering.
- **Safety Fetch (`max_idle_seconds: 900`)**: If no events arrive for 15 minutes, a safety fetch runs against `GET /eink.png` to synchronize against any dropped events.

### C. ETag Fetcher & Double-Guard Skipping (`clients/eink-node/fetcher.py`)
- **Cache Validation**: Queries `GET /eink.png` with `If-None-Match: <last_etag>`.
- **HTTP 304 Handling**: In Python `urllib.request`, HTTP 304 is raised as `urllib.error.HTTPError`. The fetcher intercepts status 304, records `not_modified = True`, clears the dirty flag, and skips panel writing.
- **Payload Byte Verification**: If the server returns 200 OK but the binary PNG bytes are identical to the cached frame buffer, the panel write is skipped (`skipped_identical`), eliminating unnecessary hardware refresh cycles.
- **Error Resilience**: If the sidecar returns HTTP 503 (e.g. during cold-boot snapshot generation) or times out, the dirty flag remains set (`is_dirty = True`) to retry on the next cycle, and panel writes are avoided.

### D. Background Clock Refresh & Client Orchestration (`clients/eink-node/client.py`)
- **Header Clock Sync**: A background timer thread ticks every `clock_refresh_seconds` (default 300s), marking the screen dirty (`clock_timer`) to keep the top-banner clock accurate without high-frequency tick spam from Core.
- **Thread Management**: Clean thread lifecycle (`SSEListenerThread`, `ClockTimerThread`, `RefreshWorkerThread`) with thread-safe `threading.Condition` notification and interruptible shutdown on `SIGINT` / `SIGTERM`.

### E. Configuration & Hermetic Test Architecture (`clients/eink-node/config.py`)
- **Pure-Python Standard Library Core**: Core networking and coalescing logic rely solely on standard library modules (`urllib.request`, `threading`, `time`, `dataclasses`, `argparse`). PyYAML is used when available, with a built-in fallback parser to guarantee zero runtime failures when running in minimal environments.
- **Simulated Clock Test Doubles**: All timing tests use a simulated `MockClock` with zero arbitrary sleeps (`time.sleep`), enabling the entire 24-test suite to run deterministically in < 250ms.

---

## 3. References
- `specs/009-eink-display-node.md` (§1-§3: Trigger Model, Refresh Lifecycle, Architecture)
- `specs/006-realtime-comms-and-mutations.md` (§1-§3: SSE Stream Endpoint, Event Schemas, Hydration)
- `specs/013-implementation-roadmap.md` (Phase 5, Task 5.2)
- `decisions/2026-09-26-headless-eink-snapshot-and-selective-dithering.md` (Chunk 5.1 Snapshot Sidecar)
