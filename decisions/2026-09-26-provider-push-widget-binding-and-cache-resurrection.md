# Architecture Decision Record: Provider Push Widget Binding and Cache Resurrection Resilience

- **Date:** 2026-09-26
- **Status:** Accepted
- **Context:** Resolving Issue #218 on `azylman/mirrormere`, aligning `internal/provider` with SPEC-003 §1–§2 and SPEC-012 §3.

---

## 1. Problem Statement
In commit `03b3422` (Phase 3 Chunk 3.1, PR #215), all providers shared a single `eventSink chan WidgetPayload` (buffer 64) in `ProviderCoordinator`, and `listenEventSink` recorded every payload under whatever `payload.WidgetID` the provider wrote.

This produced two critical defects:
1. **Unbound Provider Pushes**: A provider had no enforced binding to its widget ID in `Subscribe`. A provider that pushed had to guess or populate its own `WidgetID`. If empty or incorrect, the payload was recorded and broadcast under that invalid key.
2. **Post-Reload Cache Resurrection**: When a widget was removed or modified via a domain change (SPEC-012 §3), `stopWorkerLocked` stopped the worker, and `UpdateConfig` purged the cache entry (`c.cache.Purge(id)`). Any push already buffered in `eventSink` from the old worker was consumed subsequently by `listenEventSink`: `RecordPush` recreated the purged entry as `healthy` with stale data, `SetWidgetState` restored it in the state sink, and an obsolete `widget.update` was broadcast over SSE. For domain changes, this exposed old domain data under the new configuration (violating SPEC-012 §3); for removed widgets, it left phantom entries in the cache and provider status maps.

### Authoritative Specifications
- **SPEC-003 §2 (SWR Cache & Health Transitions)**: "Cache entries track the live operational state of each configured widget instance."
- **SPEC-012 §3 (Domain vs Non-Domain Reload Diffs)**: "If any domain keys change for an existing widget, Mirrormere terminates the old provider worker, purges the stale cache entry, broadcasts degraded operational state, and boots a clean worker instance."

---

## 2. Decision & Architecture

### A. Per-Worker Sink and Widget ID Binding
1. Each provider worker now receives its own dedicated `workerSink := make(chan WidgetPayload, 64)`.
2. A worker-scoped forwarder goroutine reads from `workerSink`, automatically stamps `payload.WidgetID = wrk.widgetID`, checks for worker context cancellation, and forwards `workerEvent{worker: wrk, payload: payload}` to the coordinator's internal `eventSink`.
3. If the worker's context is cancelled, the forwarder drops in-flight payloads and terminates cleanly.

### B. Active Worker Verification in `listenEventSink`
1. Introduced `isWorkerActive(w *worker, widgetID string) bool` on `ProviderCoordinator`.
2. `listenEventSink` verifies that:
   - The worker is not marked stopped (`!w.stopped.Load()`).
   - The current worker in `c.workers[widgetID]` matches the exact worker instance that generated the push event (`current == w`).
3. If a widget was removed, replaced with a new instance during a domain change, or stopped, any leftover buffered push from that worker is discarded without updating cache or broadcasting events.

### C. Worker Lifecycle and Purge Synchronization
1. Added `stopped atomic.Bool` to `worker`.
2. In `stopWorkerLocked`: `w.stopped.Store(true)` is set before `w.cancel()`, worker termination is awaited, and `w.subWg.Wait()` completes.
3. In `UpdateConfig`: `c.stopWorkerLocked(m.ID)` is called prior to `c.cache.Purge(m.ID)` on domain change, ensuring the old worker is fully stopped before cache reset.
4. When widgets are removed, `c.stateSink.SetProvidersStatus(c.cache.GetStatusMap())` is called immediately to synchronize the display state sink.

### D. Testing & Verification
1. In `internal/provider/coordinator_test.go`:
   - Updated `TestProviderCoordinator_ProviderPushSubscription` to verify that pushes with empty `WidgetID` are stamped with the correct widget ID and verified via event-driven synchronization (`broadcaster.waitForPublish`).
   - Added `TestProviderCoordinator_PushDoesNotResurrectPurgedCacheAfterDomainChange` to verify that an in-flight push emitted prior to a domain-change reload does not resurrect the purged cache entry with old domain data.
   - Added `TestProviderCoordinator_PushFromRemovedWidgetIgnored` to verify that pushes from removed widgets are dropped and do not leave phantom entries in cache or status maps.
2. Statement test coverage for `internal/provider` remains at **95.8%**, well above the >= 95.0% threshold.

---

## 3. Consequences
- Stale provider pushes can no longer resurrect purged cache entries after domain-change reloads or widget removals.
- Provider pushes are guaranteed to be bound and stamped to their configured widget ID.
- No phantom entries linger in display state or provider status maps.
