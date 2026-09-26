# Architecture Decision Record: SSE Client Reconnection Replay with Last-Event-ID

- **Date:** 2026-09-26
- **Status:** Accepted
- **Context:** Resolving Issue #214, aligning `web/static/js/sse.js`, `web/test/sse.test.js`, and `internal/events` with SPEC-006 §1 and §3.

---

## 1. Problem Statement
In commit `77e00cd` (Chunk 2.4, #208), the client-side SSE connection manager `MirrormereSSE` (`web/static/js/sse.js`) was introduced with automatic reconnection and exponential backoff.

However, when a connection dropped:
1. `this.eventSource.onerror` immediately invoked `this.scheduleReconnect()`, which closed the underlying `EventSource` instance.
2. When the reconnection timer fired, `this.connect()` constructed a brand-new `EventSource` instance pointing to `this.url` (`/api/events`).
3. Browser `EventSource` implementations only transmit the `Last-Event-ID` request header automatically during native browser-managed retries on open/reconnecting instances; creating a new `EventSource` does not send `Last-Event-ID` unless explicitly encoded in the URL.
4. `MirrormereSSE` never recorded `e.lastEventId` from incoming `MessageEvent` objects across standard event listeners or `onmessage`.

Because `Last-Event-ID` was omitted and no `lastEventId` query parameter was appended, the Mirrormere backend (`internal/events/handler.go`) was forced to treat every reconnection as a cold boot, executing full initial state hydration (`hub.BuildHydration()`). The server-side replay ring buffer (`hub.ReplaySince(lastEventID)`), query fallback, and subscriber replay deduplication logic (#199, PR #202) were never utilized by the display client.

### Authoritative Specifications
- **SPEC-006 §1 (Design Philosophy - Native Browser Resilience)**: "Transparent state resumption via the `Last-Event-ID` header."
- **SPEC-006 §3 (Connection Handshake & Initial State Hydration)**: "If the request includes a valid `Last-Event-ID` header and the missed events are present in the server's in-memory ring buffer (default retention: 5 minutes / 1,000 events), the server replays only the missed events in sequence."

---

## 2. Decision & Architecture

### A. Last-Event-ID Tracking in Client Runtime (`web/static/js/sse.js`)
1. **State Tracking**: Enhanced `MirrormereSSE` with `this.lastEventId` initialized to `options.lastEventId || null` or extracted from an initial URL query parameter.
2. **Event Stamping**: Implemented `_recordLastEventId(e, payload)` invoked on all incoming events across `standardEvents` (`screen.rotate`, `widget.update`, `widget.reload`, `style.reload`, `header.update`, `system.status`, `video.state`, `audio.state`, `voice.state`), dynamic event subscriptions, and default `onmessage`.
3. **URL Synthesis (`getConnectionURL`)**: On connection and reconnection, `getConnectionURL()` inspects `this.lastEventId`. If present, it sets or updates `lastEventId=<id>` in the query string using the `URL` API while preserving existing query parameters (`profile=touch`), relative vs. absolute paths, and hash fragments.
4. **Lifecycle & Testing Controls**:
   - `getLastEventId()`: Returns active event ID string.
   - `setLastEventId(id)`: Explicitly overrides or sets event ID.
   - `reset()`: Disconnects and resets `this.lastEventId = null` and `reconnectAttempts = 0`.
   - `this.jitter`: Configurable jitter factor (default 500ms) allowing deterministic 0ms jitter in unit tests.
   - Dual export: Exposes `window.MirrormereSSE` in browsers and `module.exports` in CommonJS/Node.js environments.

### B. Server-Side Query Parameter Resumption (`internal/events/handler.go`)
1. In `internal/events/handler.go`, verified and hardened the fallback check: if `Last-Event-ID` HTTP header is blank, inspect `r.URL.Query().Get("lastEventId")`, with secondary fallback to `last_event_id`.
2. When present, `hub.ReplaySince(lastEventID)` searches the in-memory ring buffer. If found, only the missed events are flushed to the client, deduplicated against any live events concurrently published during subscription handshake.

### C. Testing & Verification
1. Created `web/test/sse.test.js` using Node.js native test runner (`node --test`):
   - Verified URL construction across clean paths, existing query parameters, duplicate parameter replacement, hash fragments, and relative/absolute URLs.
   - Verified event ID stamping from standard events and default messages.
   - Verified reconnection creates a new `EventSource` with `?lastEventId=<id>`.
   - Verified dynamic event listener attachment and state resets.
2. Added `TestSSEClient_ReconnectionReplay_NodeRunner` in `web/web_test.go` ensuring tests run during `go test ./...`.
3. Added `TestHandler_QueryParamLastEventID_Replay` and `TestHandler_QueryParamSnakeCase_Fallback` in `internal/events/handler_test.go` asserting replayed event delivery over HTTP query parameter resumption.

---

## 3. Consequences
- Clients reconnecting after network blips replay only missed events, eliminating UI flicker and redundant network payloads.
- Ring buffer replay and deduplication paths are actively exercised in production.
- Monorepo statement test coverage remains strictly >= 95.0% across all packages.
