# Architecture Decision Record: Watcher Package Deletion Status Recovery & Builtin Fallback Re-validation

- **Date:** 2026-09-25
- **Status:** Accepted
- **Context:** Resolving Issue #194 on `azylman/mirrormere`:
  - `fix(watcher): deleting a custom package dir leaves a permanent config_status error and skips re-validation against the built-in fallback`

---

## 1. Context & Inconsistency

Under SPEC-003 §Live Reload and SPEC-006 §2.D:
- Whole-package precedence dictates that deleting a custom widget package override (`/config/widgets/<type>`) causes the widget registry to fall back seamlessly to the built-in package (`/app/widgets/<type>`).
- Live reload requires that package changes trigger schema re-validation and metadata reload. Removing an override must re-validate the running configuration against the built-in manifest's `default_dimensions`, `config_schema`, and `supported_dimensions`.
- `config_status` must return to `ok` as soon as a subsequent valid `config.yaml` or complete package is successfully validated and applied.

Prior to this change:
1. **Permanent Package Incompleteness Error on Directory Deletion:**
   When an unreferenced custom widget directory was created (e.g. `mkdir /config/widgets/foo`), the watcher detected missing required files (`manifest.yaml`, `views/widget.html`) and recorded a package error via `SetPackageError("foo", ...)`, causing `config_status` to transition to `error`. When the directory was subsequently deleted (`rmdir /config/widgets/foo`), `LoadPackage("foo")` failed with `"not found"`. The watcher treated this as incomplete, overwriting the error instead of clearing it. Because deleted packages never successfully load, the error remained permanently until daemon restart.
2. **Missing Re-Validation on Override Removal:**
   When removing a custom override directory (e.g. `rm -rf /config/widgets/calendar-agenda`), the remove event targeted the directory rather than `manifest.yaml`, leaving `IsManifest` false. The watcher skipped re-validation via `Manager.Reload`, preventing the running snapshot from re-validating against the newly effective built-in manifest and updating layout dimensions.
3. **Stale Package Errors after Config Reload:**
   `Manager.Reload` did not reset `m.packageErrors` upon successful validation and application of a new configuration, violating SPEC-006 §2.D.

---

## 2. Decision

1. **Package Directory Existence Check in Watcher (`internal/watcher/watcher.go`)**:
   - In `Watcher.flush()`, probe directory existence across `CustomWidgetsDir` and `BuiltinWidgetsDir` before calling `LoadPackage`.
   - If a widget package directory does not exist in either custom or builtin locations (`!packageDirExists`):
     - Mark the package as deleted and incomplete (preventing `widget.reload` dispatch for non-existent widgets).
     - Clear any existing package error for that type via `configManager.ClearPackageError(widgetType)`. If this clears the error and config is healthy, dispatch `ConfigStatusOK` immediately.
     - If the deleted widget type is referenced in the running configuration, flag `needsManifestReload = true` to trigger `configManager.Reload`, catching missing packages during Stage 3 validation, retaining LKGC, and reporting the missing package error.

2. **Package Change & Fallback Detection for Referenced Widgets (`internal/watcher/watcher.go`)**:
   - In `Watcher.flush()`, when evaluating complete packages referenced in the running configuration (`isWidgetTypeReferenced`):
     - Compare the freshly loaded package (`loadedPkg`) against the active snapshot (`currentSnap.Packages[widgetType]`).
     - If package source (`snapPkg.Source != loadedPkg.Source`, such as falling back from `"custom"` to `"builtin"` or overriding to `"custom"`), directory, or manifest content (`reflect.DeepEqual`) has changed, trigger `configManager.Reload`.
     - Re-running `Manager.Reload` validates stages 3–6 against the newly effective package manifest, recalculates layout default dimensions, atomically swaps the snapshot, and dispatches `config.reload` and `system.status`.
     - If re-validation fails (e.g. built-in package does not support configured dimensions), LKGC is retained, error status is dispatched, and `widget.reload` is aborted.

3. **Reset Package Errors on Successful Config Reload (`internal/config/lkgc.go`)**:
   - In `Manager.Reload()`, upon successful validation and atomic swap of candidate configuration:
     - Clear `m.packageErrors = make(map[string]string)`.
     - Stage 3 has verified that all referenced packages are complete and valid. Any unreferenced package errors no longer affect the active applied configuration, ensuring `config_status` reliably returns to `ok` per SPEC-006 §2.D.

---

## 3. Verification & Test Evidence

- Added unit tests in `internal/config/lkgc_test.go`:
  - `TestManager_ReloadClearsStalePackageErrors`: Verifies that a successful `Manager.Reload` clears prior package errors and returns status to `ConfigStatusOK`.
- Added unit tests in `internal/watcher/watcher_test.go`:
  - `TestWatcher_MkdirRmdirCustomPackageStatusReturnsOK`: Verifies that running `mkdir` followed by `rmdir` for an unreferenced custom widget transitions status to `error` and then immediately back to `ok` with error badge cleared.
  - `TestWatcher_DeleteCustomOverrideFallsBackToBuiltinAndRevalidates`: Verifies that removing a custom override falls back to the built-in package, re-validates the running configuration, updates snapshot source to `"builtin"`, and dispatches `config.reload` and `widget.reload`.
  - `TestWatcher_DeleteCustomOverrideValidationFailureRetainsLKGC`: Verifies that if falling back to the built-in package causes a validation failure (e.g. unsupported dimensions), LKGC is retained, status becomes `error`, and `widget.reload` is aborted.
