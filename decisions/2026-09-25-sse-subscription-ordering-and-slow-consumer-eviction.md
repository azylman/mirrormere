# Architecture Decision Record: SSE Subscription Ordering, Replay Deduplication & Slow Consumer Eviction

- **Date:** 2026-09-25
- **Status:** Accepted
- **Context:** Resolving Issue #199 on `azylman/mirrormere`:
  - `fix(events): events published during hydration/replay are lost; subscribe happens after the initial batch`

---

## 1. Context & Inconsistency

Under SPEC-006 §2.D and §3:
- Cold-booting, waking, or reconnecting display clients must never render a blank canvas or stale layout.
- Resumption with `Last-Event-ID` guarantees that "the server replays only the missed events in sequence."
- System status transitions (e.g. `config_status: "error"` on failed config reloads) must be authoritatively delivered so that reconnecting kiosks never miss diagnostic warnings.

Prior to this change:
1. **Subscription Window State Loss:**
   In `internal/events/handler.go`, `GET /api/events` prepared and flushed the initial batch (via either `hub.BuildHydration()` or `hub.ReplaySince(lastEventID)`) *before* invoking `hub.Subscribe(r.Context())`. Any event published between the snapshot/ring query and the registration of the subscriber was dropped permanently for that client. On a busy host or slow link, this race window was wide enough to cause kiosks reconnecting during a configuration reload or layout change to display obsolete layouts until the next rotation cycle, or permanently if rotation interval was disabled (`0`).
2. **Silent State Loss on Slow Consumers:**
   In `internal/events/hub.go`, when a subscriber channel's buffer filled (e.g. due to network backpressure or slow client rendering), `PublishEvent` dropped the event and logged a warning while keeping the SSE stream open. The client silently lost state transitions and never resynced.

---

## 2. Decision

1. **Pre-Snapshot Subscription Ordering (`internal/events/handler.go`)**:
   - Invoke `hub.Subscribe(r.Context())` immediately after completing protocol handshakes and setting headers, *before* querying `ReplaySince()` or constructing `BuildHydration()`.
   - Any live event broadcast by the system while initial batch serialization and socket writing take place is buffered into the subscriber's channel, eliminating the state loss window entirely.

2. **Replay Deduplication & Channel Draining (`internal/events/handler.go`)**:
   - For replay requests (`Last-Event-ID`), construct a set of already-delivered event IDs (`seenReplayIDs`) containing `lastEventID` and all IDs in the `replayed` batch.
   - When draining the subscriber channel (`drainLoop`) or streaming live events, drop any event whose ID exists in `seenReplayIDs`. Upon encountering the first event not in `seenReplayIDs`, discard `seenReplayIDs` (set to `nil`) because events arrive in strict monotonic FIFO sequence.
   - For full hydration batches (`BuildHydration`), deliver all queued events in `drainLoop`. Duplicate idempotent state events are safe and ensure any mutation occurring during snapshotting immediately overrides stale snapshot data.

3. **Slow Consumer Disconnection & Resync Enforcement (`internal/events/hub.go`)**:
   - Replace event-dropping with channel termination: when a subscriber's channel buffer overflows (`sendBufferFull`), log a structured warning and call `hub.Unsubscribe(sub.id)`, closing the subscriber's channel.
   - In `internal/events/hub.go`, protect subscriber channel sends and closures with a dedicated per-subscriber mutex (`sub.mu`), ensuring non-blocking sends and eliminating any risk of `panic: send on closed channel`.
   - When the channel closes, `Handler.ServeHTTP` terminates the response stream cleanly. Display clients (via standard WHATWG `EventSource` auto-reconnect) immediately reconnect with their latest processed `Last-Event-ID` and receive an up-to-date replay or fresh hydration batch.

---

## 3. Verification & Test Evidence

- Added unit tests in `internal/events/handler_test.go`:
  - `TestHandler_EventPublishedDuringHydrationDelivered`: Verifies that an event published while `BuildHydration` is executing is delivered to the client without state loss.
  - `TestHandler_ReplayDeduplication`: Verifies that events present in both the replay batch and live subscriber channel are delivered exactly once.
  - `TestHandler_ReplayUpToDateWithLiveEvent`: Verifies that clients reconnecting with the latest event ID receive subsequent live events cleanly without dropped messages.
  - `TestHandler_SlowConsumerClosesConnection`: Simulates client write backpressure, verifies subscriber eviction on buffer overflow, and confirms clean handler stream termination.
- Updated unit test in `internal/events/hub_test.go`:
  - `TestHub_SlowConsumerDrop`: Asserts that buffer overflow terminates the subscriber, evicts it from `SubscriberCount()`, and closes the channel.
- Ran full monorepo verification (`./scripts/verify.sh --staged`): all packages passed with 100% green tests and **97.19%** monorepo test coverage (`internal/events` at **96.33%**).
