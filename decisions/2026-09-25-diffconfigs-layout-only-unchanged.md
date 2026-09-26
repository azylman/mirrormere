# Architecture Decision Record: DiffConfigs Layout-Only Edits Classified as Unchanged

- **Date:** 2026-09-25
- **Status:** Accepted
- **Context:** Resolving Issue #183 on `azylman/mirrormere` (`fix(config): DiffConfigs treats resize/pin changes as Modified, restarting workers on layout-only edits`).

---

## 1. Context & Inconsistency

Under SPEC-012 §3 (Instance Diffing & Worker Lifecycle Management):
- **Unchanged criteria**: Identical `id`, `type`, `config`, `refresh_interval_seconds`, `endpoint`. Action: "Zero interruption: Goroutine continues running; cache remains valid."
- **Modified criteria**: Same `id` and `type`, but `config`, interval, or transport settings changed. Action: "In-Place Worker Restart: Cancel existing worker context, re-instantiate provider with updated settings, start new sync loop."
- **Layout Solver Recalculation (SPEC-012 §4)**: Layout changes (widget dimensions, pinning, screen placements) are handled downstream by the 6×2 bin-packing layout solver and broadcast via `screen.rotate`. They do not alter provider ingestion settings or worker state.

In Chunk 1.6 (PR #181), `internal/config/lkgc.go`'s `DiffConfigs` included `Dimensions` and `Pinned` inside `transportChanged`. Resizing a widget or changing its pinned flag erroneously classified the instance as `ChangeModified`, unnecessarily canceling and restarting that widget's background sync worker.

---

## 2. Decision

1. **Decouple Layout Edits from Worker Lifecycle**:
   - Removed `Dimensions` and `Pinned` comparisons from `transportChanged`.
   - Widgets whose `config`, `endpoint`, `method`, `token_env`, and `refresh_interval_seconds` are unchanged remain classified as `Unchanged`, guaranteeing zero worker interruption.
2. **Dedicated `LayoutModified` Diff Tracking**:
   - Added `LayoutModified []WidgetConfig` to `ConfigDiff`.
   - When a widget's dimensions or pinned status change without transport or domain modifications, it is added to `diff.Unchanged` for worker lifecycle AND recorded in `diff.LayoutModified` for callers that need to track layout changes explicitly.
3. **Modified Preservation**:
   - Any widget with actual domain configuration changes (`Config`) or transport modifications (`endpoint`, `method`, `token_env`, `refresh_interval_seconds`) continues to be classified as `Modified` with appropriate in-place worker restarts.

---

## 3. Verification & Test Evidence

- Updated `TestLKGC_DiffConfigs` in `internal/config/lkgc_test.go`:
  - Resized widget (`[2, 1]` -> `[4, 1]`) is classified as `Unchanged` and recorded in `LayoutModified`.
  - Pinned toggle (`pinned: false` -> `pinned: true`) is classified as `Unchanged` and recorded in `LayoutModified`.
  - Neither layout-only edit is classified as `Modified`.
  - Modified widgets (`mod-transport`, `mod-domain`, `replaced-1`) remain accurately classified.
- All unit tests pass cleanly with zero regressions.
