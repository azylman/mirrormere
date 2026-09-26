# Architecture Decision Record: High-Performance SSE Event Bus & Initial Hydration

**Date:** 2026-09-25  
**Status:** Approved  
**Context:** SPEC-003, SPEC-006, SPEC-007, SPEC-012, SPEC-013 (Chunk 2.1, Issue #193)  

---

## 1. SSE Wire Protocol, Streaming Buffering & Write Deadlines

### Description of Issue / Inconsistency
Server-Sent Events (SSE) connections are long-lived unidirectional HTTP streams (`GET /api/events`). In standard Go HTTP servers (`http.Server`), `WriteTimeout` (configured to 10s by default in Mirrormere) forcibly closes any open HTTP connection after the deadline expires, terminating persistent event streams. In addition, reverse proxies (Caddy, Nginx) buffer response chunks unless explicitly instructed, delaying or batching realtime notifications.

### Decision Made
- Stream events under `Content-Type: text/event-stream; charset=utf-8` with explicit caching and buffering headers: `Cache-Control: no-cache, no-transform`, `Connection: keep-alive`, `X-Accel-Buffering: no`, and CORS headers (`Access-Control-Allow-Origin: *`).
- In Go 1.20+, use `http.NewResponseController(w).SetWriteDeadline(time.Time{})` immediately after header dispatch to disable per-request write deadlines for the lifetime of the stream, ignoring `http.ErrNotSupported` in unit tests using `httptest.ResponseRecorder`.
- Emit SSE comment heartbeats (`: ping <unix>\n\n`) at 15-second intervals to satisfy proxy and NAT keep-alive requirements without polluting client event streams.

### Technical Rationale & References
- Eliminates 10-second connection drops under Go's default `WriteTimeout`.
- References: `specs/006-realtime-comms-and-mutations.md` §1, Go 1.20 `net/http` `ResponseController` specification.

---

## 2. In-Memory Ring Buffer, Replay Window & Monotonic Event IDs

### Description of Issue / Inconsistency
Transient network interruptions (e.g. Wi-Fi re-association or display sleep/wake cycles) temporarily disconnect display clients. Without an event replay log, clients either miss intermediate state mutations or must re-fetch full page states, causing layout recalculations and flashes.

### Decision Made
- Maintain an in-memory circular ring buffer with a default capacity of 1,000 events and a 5-minute Time-To-Live (TTL) eviction policy.
- Guard the ring buffer with `sync.RWMutex` to guarantee safe concurrent appends, snapshots, and replays.
- Generate monotonic, chronologically sortable event identifiers matching `evt_<unix_sec>_<zero_padded_counter>`.
- On incoming client connection, check for `Last-Event-ID` (from request header or `?last_event_id` query parameter).
- If `Last-Event-ID` is present and found within the ring buffer's active window, replay all missed events in strict chronological order before streaming live events.
- If `Last-Event-ID` is expired or not found, fall back safely to authoritative 7-stage initial state hydration.

### Technical Rationale & References
- Provides seamless reconnect recovery for brief network blips while keeping memory consumption bounded.
- References: `specs/006-realtime-comms-and-mutations.md` §1–2, W3C Server-Sent Events specification.

---

## 3. Authoritative 7-Stage Initial State Hydration Ordering

### Description of Issue / Inconsistency
When a display client connects on cold boot or after an expired session, receiving state events out of order causes layout thrashing, flashes of unstyled content, or orphaned widget markup. For example, rendering widget markup before the grid screen dimensions and rotation intervals are established causes layout reflow.

### Decision Made
- Enforce strict sequential ordering for initial state hydration per SPEC-006 §3:
  1. `screen.rotate`: Active screen configuration, screen count ($K$), and rotation interval seconds.
  2. `widget.update`: Current rendered markup and cached state for all active widgets, ordered deterministically by widget ID ascending.
  3. `header.update`: Date/time format, custom title, and greeting state.
  4. `video.state`: Media playback state, video stream URL, and mute status.
  5. `audio.state`: Background audio track state and volume.
  6. `voice.state`: Microphone listening mode and wake word state.
  7. `system.status`: Overall LKGC status, validation errors, and provider health.
- Abstract state retrieval behind the `StateProvider` interface, decoupling the events subsystem from the concrete configuration and runtime state managers.

### Technical Rationale & References
- Guarantees display clients establish the grid layout canvas first, inject widget slots cleanly, and set header/peripheral status without blank flashes or visual jitter.
- References: `specs/006-realtime-comms-and-mutations.md` §3.

---

## 4. Bounded Subscriber Channels & Slow Consumer Isolation

### Description of Issue / Inconsistency
If one connected display client encounters network stalls or high event processing latency, a synchronous event bus would block event delivery to all other connected displays, stalling the entire Mirrormere runtime.

### Decision Made
- Buffer subscriber channels with a capacity of 64 events.
- Dispatch events via non-blocking select: if a subscriber's channel buffer is full, log a structured warning (`slow SSE consumer detected; dropping event`) and drop the event for that consumer rather than blocking the hub.
- Support clean subscriber unregistration when client context cancels (`r.Context().Done()`) or when the hub shuts down (`hub.Close()`).
- Implement the `watcher.Dispatcher` interface (`DispatchConfigReload`, `DispatchStyleReload`, `DispatchWidgetReload`, `DispatchStatus`) directly on `Hub` to route filesystem watcher events onto the realtime SSE event bus seamlessly.

### Technical Rationale & References
- Guarantees fault isolation: slow or hung clients cannot impact the responsiveness or throughput of healthy clients or the core runtime.
- References: `specs/006-realtime-comms-and-mutations.md` §1–2, `specs/013-phase-1-walking-skeleton.md`.
