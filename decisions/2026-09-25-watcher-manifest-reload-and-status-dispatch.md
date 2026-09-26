# Architecture Decision Record: Watcher Manifest JIT Reload & Telemetry Status Dispatch

- **Date:** 2026-09-25
- **Status:** Accepted
- **Context:** Resolving Issues #189 and #190 on `azylman/mirrormere`:
  - Issue #189: `fix(watcher): manifest.yaml changes dispatch widget.reload without updating the running snapshot or re-solving layout`
  - Issue #190: `fix(watcher): reload failures and incomplete packages never reach system.status; Dispatcher has no status path`

---

## 1. Context & Inconsistency

Under SPEC-003 §Live Reload:
- When `manifest.yaml` updates, the daemon must reload widget registry metadata JIT, validate schemas, and re-solve layout so clients pick up updated dimensions and metadata.
- In SPEC-012 §2, configuration candidate validation follows a 6-stage pipeline, and stages 3, 4, and 6 depend directly on widget manifests (`default_dimensions`, `supported_dimensions`, `config_schema`).
- In SPEC-012 §2 Item 3, validation failures must broadcast a `system.status` event (`config_status: "error"`, `config_error: "..."`), cleared on the next valid apply.
- In SPEC-003 §Runtime Package Creation, incomplete widget packages (e.g. missing `manifest.yaml` or `views/widget.html`) must report the incomplete package error in `system.status`, and completing the package must clear it.

Prior to this change:
1. When `manifest.yaml` changed, the watcher verified package completeness and immediately dispatched `widget.reload` without re-running `Manager.Reload`. Consequently:
   - Modified `default_dimensions` never propagated to running instances, and layout was never re-solved.
   - Breaking manifest changes (unsupported dimensions, new required `config_schema` properties) left the running configuration invalid against its own package without reporting errors.
2. `Dispatcher` lacked a status telemetry path (`DispatchStatus`), and failure paths (config syntax/validation errors, incomplete packages) were only logged to stdout/stderr. Neither runtime package errors nor config reload errors reached `system.status` or client hydration.

---

## 2. Decision

1. **Dispatcher Status Telemetry Pathway (`internal/watcher/events.go`)**:
   - Added `DispatchStatus(status config.Status) error` to the `Dispatcher` interface.
   - The watcher dispatches status updates on:
     - Configuration reload errors and successful recoveries.
     - Configuration file read failures.
     - Incomplete widget package detection.
     - Incomplete package completion/recovery.
     - Manifest JIT validation failures and recoveries.

2. **Package Incompleteness Tracking in `config.Manager` (`internal/config/lkgc.go`)**:
   - Added `SetPackageError(widgetType string, err error)` and `ClearPackageError(widgetType string) bool` to `config.Manager`.
   - Incomplete package errors are recorded in `Manager.Status()` so both live SSE status broadcasts and initial client hydration (SPEC-006 §3) reflect active package errors.
   - Restoring a package clears its specific error; status returns to `ConfigStatusOK` when all package and configuration errors are resolved.

3. **Manifest JIT Reload & Snapshot Swap (`internal/watcher/watcher.go`)**:
   - Updated `ClassifyPath` in `internal/watcher/filter.go` to flag manifest updates via `TargetInfo.IsManifest`.
   - When a modified manifest belongs to a widget referenced in `config.Manager.CurrentSnapshot()`:
     - The watcher re-executes `configManager.Reload(data)` against the current `config.yaml`.
     - This re-runs stages 3–6: reloading package metadata JIT, applying updated `default_dimensions`, validating schemas against the new manifest, re-solving the 6×2 layout, and atomically swapping the running snapshot.
     - If reload succeeds: dispatches `DispatchConfigReload(snap, diff)`, `DispatchStatus(status)`, and `DispatchWidgetReload(event)`.
     - If reload fails: retains running LKGC, dispatches error status via `DispatchStatus(status)`, logs structured diagnostics, and aborts `DispatchWidgetReload`.

---

## 3. Verification & Test Evidence

- Added unit tests in `internal/config/lkgc_test.go`:
  - `TestLKGC_PackageErrorsAndExplicitStatus`: Verifies package error recording, multi-package tracking, clearing, and precedence over config errors.
- Added unit tests in `internal/watcher/watcher_test.go`:
  - `TestWatcher_ManifestDefaultDimensionsProducesNewLayoutAndConfigReload`: Verifies that updating `default_dimensions` in `manifest.yaml` updates `CurrentLayout()`, dispatches `config.reload`, and dispatches `widget.reload`.
  - `TestWatcher_ManifestValidationFailureRetainsLKGCAndAbortsWidgetReload`: Verifies that an invalid manifest change retains LKGC, dispatches error status, and suppresses `widget.reload`.
  - `TestWatcher_ConfigReloadFailureDispatchesErrorStatusAndRecoveryDispatchesOK`: Verifies syntax error status dispatch and recovery to OK.
  - `TestWatcher_IncompletePackageDispatchesErrorStatusAndRecoveryDispatchesOK`: Verifies incomplete package error status dispatch and completion recovery.
- Verification passed with **96.73%** statement coverage in `internal/watcher` and **97.59%** overall, meeting the $\ge 95.0\%$ threshold.
