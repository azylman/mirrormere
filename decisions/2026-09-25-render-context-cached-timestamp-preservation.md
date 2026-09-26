# Architecture Decision Record: Render Context Cached Timestamp Preservation

- **Date:** 2026-09-25
- **Status:** Accepted
- **Context:** Resolving Issue #211, aligning `internal/render` and `internal/events` with SPEC-003 §Server-Side Template Execution Context, §1 View Template, and §2 Fragment Rendering.

---

## 1. Problem Statement
In `internal/render/engine.go`, `buildContext` constructed the template execution context with `.Timestamp` set to `e.nowFunc().UTC().Format(time.RFC3339)` (the current render time) rather than the timestamp when the widget's domain data was actually fetched and cached.

`StateSnapshotProvider.GetWidgetState` returned only `(data any, state string, ok bool)`, dropping the cached envelope's timestamp. As a consequence:
1. Templates using relative date helpers (e.g. `{{ relDate .Timestamp }}` or "Updated 2h ago") and stale/degraded badges rendered "just now" on every initial page load or re-render, completely obscuring data staleness and violating the core premise of Stale-While-Revalidate (SWR).
2. Uncached widgets rendered the current render time instead of an empty string `""`.

Per SPEC-003:
- §Server-Side Template Execution Context: `.Timestamp` is the "ISO 8601 timestamp string of the latest update".
- §1 View Template: `.Timestamp` is the "ISO 8601 string of the payload timestamp".
- §2: Fragments are rendered from the "latest cached payload envelope (`data`, `state`, `timestamp`)".

---

## 2. Decision & Architecture

### A. Extended State Retrieval Contracts
1. Updated `render.StateSnapshotProvider`:
   ```go
   type StateSnapshotProvider interface {
       CurrentSnapshot() *config.Snapshot
       GetWidgetState(widgetID string) (data any, state string, timestamp string, ok bool)
       CurrentStatus() config.Status
   }
   ```
2. Aligned `events.StateProvider` and `events.InMemoryStateProvider`:
   ```go
   GetWidgetState(widgetID string) (data any, state string, timestamp string, ok bool)
   ```
3. Aligned `provider.ProviderCoordinator`:
   ```go
   func (c *ProviderCoordinator) GetWidgetState(widgetID string) (any, string, string, bool) {
       if p, ok := c.cache.Get(widgetID); ok {
           return p.Data, p.State, p.Timestamp, true
       }
       return nil, "", "", false
   }
   ```

### B. Context Timestamp Assignment
In `render.Engine.buildContext`:
- When cached state is available (`ok == true`), `.Timestamp` is populated directly from the cached envelope's timestamp `ts`.
- When no cached data exists (`ok == false` or `ts == ""`), `.Timestamp` defaults to an empty string `""` rather than the render time.
- `e.nowFunc()` remains reserved exclusively for template helper evaluation (such as `RelDate`) and time-sensitive display calculations.

### C. Testing & Verification
- Added `TestEngine_RenderWidget_CachedTimestampStaleness` in `internal/render/engine_test.go` verifying that:
  - A widget cached with a timestamp from 1 hour ago renders that exact timestamp in `.Timestamp`, NOT the current render time.
  - An uncached widget renders an empty string `""` in `.Timestamp`.
- Monorepo pre-flight test suite and coverage gate passed at 97.09% statement coverage with zero lint or deadcode violations.

---

## 3. Consequences
- Templates can accurately render relative staleness ("Updated 45m ago") and degraded indicator badges using `.Timestamp`.
- Complete consistency between initial SSR rendering context and SSE hydration batch timestamps.
