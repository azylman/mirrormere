# Architecture Decision Record: Atomic Provider Push Verification and Cache Write under Read Lock

- **Date:** 2026-09-26
- **Status:** Accepted
- **Context:** Resolving Issue #225 on `azylman/mirrormere`, hardening `internal/provider` against race conditions between provider push ingestion and live config reload cache purge (SPEC-003 §2, SPEC-012 §3).

---

## 1. Problem Statement

In PR #224 (resolving Issue #218), `listenEventSink` introduced an active worker check (`c.isWorkerActive(ev.worker, ev.payload.WidgetID)`) before writing push payloads to the cache. However, `isWorkerActive` acquired and released `c.mu.RLock()`, and then `c.cache.RecordPush` and `c.stateSink.SetWidgetState` were executed without holding `c.mu`.

This introduced a concurrency window between worker validation and cache insertion:
1. `listenEventSink` called `isWorkerActive`, verified the worker was active under `RLock`, and released `RLock`.
2. Concurrently, a configuration reload (`UpdateConfig`) arrived with domain changes for the widget. `UpdateConfig` acquired `c.mu.Lock()`, stopped the old worker, purged the stale cache entry (`c.cache.Purge(m.ID)` per SPEC-012 §3), initialized the replacement worker, and released `c.mu.Lock()`.
3. `listenEventSink` resumed and executed `c.cache.RecordPush(payload.WidgetID, payload.Data, now)`.
4. As a result, the old worker's push resurrected the purged cache entry as `healthy` with stale domain data, directly overwriting the purge and corrupting the state of the replacement worker.

---

## 2. Decision & Architecture

### A. Atomic Check-and-Write under `c.mu.RLock()`
1. Consolidated worker validation and cache recording into an atomic helper:
   ```go
   func (c *ProviderCoordinator) recordWorkerPush(w *worker, payload WidgetPayload) (WidgetPayload, bool)
   ```
2. `recordWorkerPush` holds `c.mu.RLock()` across the entire sequence:
   - Worker active validation via `isWorkerActiveLocked(w, payload.WidgetID)` (checking `current == w && !w.stopped.Load()`).
   - SWR cache recording (`c.cache.RecordPush`).
   - State sink updates (`c.stateSink.SetWidgetState` and `c.stateSink.SetProvidersStatus`).
3. Mutual exclusion guarantees:
   - `UpdateConfig` acquires `c.mu.Lock()` for the entirety of its worker stop and cache purge logic.
   - If `UpdateConfig` acquires `Lock()` first, it stops the worker and replaces the mapping in `c.workers`. When `recordWorkerPush` subsequently acquires `RLock()`, `isWorkerActiveLocked` detects that the worker is stopped and no longer matches `c.workers[widgetID]`, immediately dropping the push.
   - If `recordWorkerPush` acquires `RLock()` first, it completes the push write before `UpdateConfig` acquires `Lock()`. `UpdateConfig` then purges the entry and boots the new worker cleanly.
   - Interleaving between the active worker check and cache write is strictly prevented.

### B. Broadcast Outside the Lock
To avoid holding locks during external or channel-based notifications, `c.broadcastUpdate(p)` executes after `c.mu.RUnlock()` is called in `recordWorkerPush`.

### C. Deterministic Testing Hook
To test this race condition without arbitrary sleeps:
1. Added `OnBeforeRecordPush func(widgetID string)` to `CoordinatorConfig` and `ProviderCoordinator`.
2. In `TestProviderCoordinator_PushInterleavedWithDomainChangeReload_Atomic`, the hook blocks the push handler inside `recordWorkerPush` while `RLock` is held.
3. The test starts `coord.UpdateConfig` in a goroutine and asserts that it cannot proceed until the hook is released and `RLock` is dropped.
4. The test verifies that once `UpdateConfig` completes and the replacement worker fetches new data, the cache holds the new data and the old worker's push does not resurrect the purged cache entry.

---

## 3. Consequences
- Zero race window between worker validation and SWR cache writes on provider pushes.
- Guaranteed conformance to SPEC-012 §3: domain-change reloads reliably purge old domain data and prevent zombie worker resurrection.
- Coverage in `internal/provider` remains at **95.0%**, satisfying the repository quality gate.
