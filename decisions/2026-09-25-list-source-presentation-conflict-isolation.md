# Architecture Decision Record: List Source Presentation Conflict Isolation

- **Date:** 2026-09-25
- **Status:** Accepted
- **Context:** Resolving Issue #182 on `azylman/mirrormere` (`fix(config): list source conflict check compares whole config, rejecting primaries that differ only in presentation`).

---

## 1. Context & Inconsistency

Under SPEC-008 §Shared List State & Source Ownership Rules:
- **Rule 3 (Consumer Reference Widgets)**: Widgets sharing the same `list_id` may define their own presentation parameters (e.g. `show_completed`, `list_name`).
- **Rule 4 (Conflict Validation)**: "If multiple widgets declare `source:` for the same `list_id`, their source configurations must be identical; conflicting definitions are rejected at startup with an explicit validation error."

In Chunk 1.6 (PR #181), `internal/config/lkgc.go` implemented `validateDomainRulesStage` by comparing `reflect.DeepEqual(existing.config, w.Config)` across primary widgets sharing a `list_id`. Because `w.Config` contains both source settings (e.g. `source: "local"` or `gtasks:` block) and presentation parameters (e.g. `show_completed`), two primary widgets that configured the exact same source adapter but specified different presentation options (such as different completion display limits on separate screens) were incorrectly rejected with a false positive:
`[Mirrormere Config Error] conflicting source definitions for list_id '...' between widget '...' and widget '...'`

---

## 2. Decision

Refactored `validateDomainRulesStage` in `internal/config/lkgc.go` to compare only source-defining adapter configurations:
1. **Source Adapter Block Extraction**:
   - `source == "http"`: isolates `w.Config["http"]` (comparing `base_url`, `token_env`, etc.).
   - `source == "gtasks"`: isolates `w.Config["gtasks"]` (comparing `tasklist_id`, etc.).
   - `source == "local"`: source configuration is empty (`nil`), as local SQLite requires no adapter block.
2. **Conflict Comparison**:
   - Compares `existing.source == source` AND `reflect.DeepEqual(existing.sourceCfg, sourceCfg)`.
   - Ignores top-level presentation parameters under `config:` (`show_completed`, `list_name`, etc.).
3. **Validation Preservation**:
   - Genuine conflicts (mismatched adapter types like `local` vs `gtasks`, differing `gtasks.tasklist_id`, or differing `http.base_url`) continue to be rejected fast at startup with the canonical SPEC-008 diagnostic error.

---

## 3. Verification & Test Evidence

- Added unit tests in `internal/config/lkgc_test.go`:
  - Matching `local` source with differing `show_completed` values reloads cleanly.
  - Matching `gtasks` source with differing `show_completed` values reloads cleanly.
  - Conflicting `gtasks.tasklist_id` is rejected with `conflicting source definitions`.
  - Conflicting `http.base_url` is rejected with `conflicting source definitions`.
- 100% of tests in `internal/config` pass cleanly with zero regressions.
