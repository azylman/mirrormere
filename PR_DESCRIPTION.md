## Executive Summary

This PR resolves Issues #189 and #190 by implementing manifest JIT reload, atomic snapshot swaps, and status telemetry dispatch across the `fsnotify` file watcher and LKGC configuration manager.

- **Manifest JIT Reload & Layout Re-solving (#189)**: Modifying a widget's `manifest.yaml` now re-runs `Manager.Reload` whenever the widget type is referenced in the running configuration. Changes to `default_dimensions`, `supported_dimensions`, and `config_schema` run through the full 6-stage validation pipeline, update the atomic running snapshot, re-solve the 6x2 grid layout, and dispatch `config.reload` alongside `widget.reload`. If manifest changes violate configuration rules, LKGC is retained, error status is reported to `system.status`, and `widget.reload` is aborted.
- **Dispatcher Status Telemetry Pathway (#190)**: Added `DispatchStatus(config.Status)` to the `Dispatcher` interface. Watcher events now dispatch status updates on configuration reload failures/recoveries, file read errors, runtime package incompleteness, and package completion.
- **Package Incompleteness Tracking in `config.Manager`**: Incomplete package errors are tracked via `SetPackageError` and `ClearPackageError` in `config.Manager`, ensuring initial client hydration and runtime SSE telemetry reflect package health per SPEC-003 and SPEC-006.

Closes #189
Closes #190

---

## Architectural & Implementation Details

- **`internal/watcher/events.go`**:
  - Added `DispatchStatus(status config.Status) error` to `Dispatcher` interface.
- **`internal/watcher/filter.go`**:
  - Added `IsManifest bool` to `TargetInfo` and populated it in `ClassifyPath` when modified files are `manifest.yaml`.
- **`internal/watcher/watcher.go`**:
  - Updated `run()` to accumulate `dirtyManifests` across debounce batches.
  - Updated `flush()` to dispatch `DispatchStatus` on config reload success/failure and file read errors.
  - Added package incompleteness status tracking via `SetPackageError` and `ClearPackageError`.
  - Added JIT manifest reload check: if an active widget has an updated manifest, re-runs `Manager.Reload(data)` once per debounce flush, dispatches `DispatchConfigReload`, `DispatchStatus`, and aborts `DispatchWidgetReload` if validation fails.
- **`internal/config/lkgc.go`**:
  - Added `configReloadErr error` and `packageErrors map[string]string` to `Manager`.
  - Added `SetPackageError`, `ClearPackageError`, `SetErrorStatus`, and `ClearErrorStatus` to atomically update telemetry status while preserving last success timestamps.
- **`decisions/2026-09-25-watcher-manifest-reload-and-status-dispatch.md`**:
  - Authored ADR detailing the architectural rationale, event sequences, and recovery mechanics.

---

## Verification & Test Evidence

1. **Unit Test Suite**:
   - `internal/config/lkgc_test.go`: Added `TestLKGC_PackageErrorsAndExplicitStatus`.
   - `internal/watcher/watcher_test.go`: Added `TestWatcher_ManifestDefaultDimensionsProducesNewLayoutAndConfigReload`, `TestWatcher_ManifestValidationFailureRetainsLKGCAndAbortsWidgetReload`, `TestWatcher_ConfigReloadFailureDispatchesErrorStatusAndRecoveryDispatchesOK`, `TestWatcher_IncompletePackageDispatchesErrorStatusAndRecoveryDispatchesOK`, and edge-case coverage for missing config files and dispatcher errors.
2. **Coverage Gating**:
   - Ran `./scripts/check-coverage.sh --check`:
     - `internal/watcher`: **96.73%** (266 / 275 statements, exceeding the >= 95.0% floor)
     - `internal/config`: **96.72%** (502 / 519 statements)
     - Repository Total: **97.59%** (1335 / 1368 statements)
3. **Pre-flight Verification**:
   - Verified via `./scripts/verify.sh --staged`.
