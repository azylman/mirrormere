# Architecture Decision Record: fsnotify Directory Watcher & Reload Dispatcher

**Date:** 2026-09-25  
**Status:** Approved  
**Context:** SPEC-003, SPEC-006, SPEC-012, SPEC-013 (Chunk 1.7, Issue #168)  

---

## 1. Directory-Descriptor Watching and Inode Detachment Prevention

### Description of Issue / Inconsistency
Under Linux, modern editors (VS Code, Vim, JetBrains, Emacs) execute atomic file saves by writing to temporary files and renaming them over the target (`config.yaml` or `custom.css`). Because Linux inotify binds watches directly to filesystem inodes rather than file path names, watching individual files causes the watch descriptor to remain attached to the unlinked inode after an atomic rename, permanently dropping future file change notifications.

### Decision Made
- Watch directory descriptors exclusively: the parent configuration directory (`/config`), built-in widgets directory (`/app/widgets`), and custom widgets directory (`/config/widgets`).
- Recursively register watch descriptors on all existing subdirectories under `/app/widgets` and `/config/widgets`.
- Dynamically attach watch descriptors to newly created subdirectories at runtime via `fsnotify.Create` on directory paths (`fi.IsDir()`).
- Handle directory removal gracefully by ignoring `os.ErrNotExist` / `syscall.ENOENT`.

### Technical Rationale & References
- Guarantees zero missed file updates on atomic renames without requiring external polling.
- References: `specs/012-live-config-reload-and-lkgc.md` §1.

---

## 2. Event Debouncing, Starvation Ceiling & Temporary File Filtering

### Description of Issue / Inconsistency
Single file saves emit rapid clusters of filesystem events within milliseconds (create, write, rename, chmod). Naive event handling triggers excessive reload cycles, while continuous event bursts can starve reload evaluations indefinitely if debounce timers are repeatedly reset without an upper bound.

### Decision Made
- Debounce events with a default `DebounceDuration` of 100ms.
- Enforce a `MaxDebounceDuration` ceiling (default 1000ms) to ensure event batches flush even under sustained rapid filesystem activity.
- Filter out temporary and editor-specific file patterns (`*.tmp`, `*.swp`, `*~`, `4913`, `.goutputstream-*`, `.#*`, `.DS_Store`) immediately upon arrival.
- Discarded temporary events neither mark dirty state nor reset the debounce timer.
- Confine all debounce timer operations, channel selects, and dirty state mutations to a single coordinator event-loop goroutine, eliminating data races.

### Technical Rationale & References
- Prevents reload thrashing while guaranteeing bounded latency for live configuration updates.
- References: `specs/012-live-config-reload-and-lkgc.md` §1.

---

## 3. Package Completeness Gating & Schema Compliance

### Description of Issue / Inconsistency
When a custom widget package is created or updated at runtime, its files (`manifest.yaml` and `views/widget.html`) may be written sequentially across multiple file operations. Emitting `widget.reload` before all required package files are present would cause the web client or SSR engine to attempt to load half-written packages, generating transient errors.

### Decision Made
- Before dispatching a `widget.reload` event for a modified or newly created widget type, verify package completeness and manifest validity via `PackageLoader`.
- If the package is incomplete or missing required files, log a structured warning (`[widget.watcher] warning="widget package '%s' is incomplete; waiting for manifest.yaml and views/widget.html"`) and suppress the reload event until the package is complete.
- Format `style.reload` events matching `api/schemas/style.reload.json` (`file` and ISO 8601 / RFC 3339 UTC `timestamp`).
- Format `widget.reload` events matching `api/schemas/widget.reload.json` (`type`).

### Technical Rationale & References
- Upholds SPEC-003 §1 and SPEC-012 §2 Stage 3: incomplete packages created at runtime are never selected, preserving active display stability.
- References: `specs/003-widget-contract.md` §1, `specs/006-realtime-comms-and-mutations.md` §2.H–I, `specs/012-live-config-reload-and-lkgc.md` §2.
