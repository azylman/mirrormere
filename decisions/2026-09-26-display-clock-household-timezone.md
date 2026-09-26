# Architecture Decision Record: Display Header Clock Household Timezone Alignment

- **Date:** 2026-09-26
- **Status:** Accepted
- **Context:** Resolving Issue #213, aligning `/display` HTML shell, `web/static/js/display.js`, and `internal/events` with SPEC-001 §4 and SPEC-012 §5.

---

## 1. Problem Statement
In commit `77e00cd` (Chunk 2.4, #208), the persistent header clock and date rendered in `web/static/js/display.js` using browser-local methods (`new Date()`, `getHours()`, and `toLocaleDateString(undefined, ...)`). The configured household `timezone` declared in `config.yaml` was completely omitted from the display presentation layer.

This caused several critical divergences:
1. **Container Divergence**: Chromium running in kiosk mode on Wayland cage or headless Chromium in `sidecars/eink-renderer` inside UTC Docker containers displayed UTC time rather than household time.
2. **Ignored Configuration**: Live updates to the top-level `timezone` key in `config.yaml` had no effect on the digital clock.
3. **Hardcoded Formatting**: The 12-hour AM/PM format was hardcoded with modulo arithmetic, preventing 24-hour clocks in locales that prefer 24-hour time.

### Conflicting & Authoritative Specifications
- **SPEC-001 §4 (Top-Level `timezone` Key)**: "All date formatting, header clocks, calendar event bounding intervals, and midnight chore rollovers execute against this house timezone, preventing container host timezone divergence."
- **SPEC-012 §5 (Live Settings vs. Restart-Required Settings)**: "Timezone: Changing `timezone` updates Go's `time.Local` / location pointer JIT, immediately adjusting digital clock formats, agenda relative times, and rollover timers."
- **SPEC-006 §2.B (`header.update`)**: Pushed when header weather poller runs or ambient header conditions update.

---

## 2. Decision & Architecture

### A. Server-Side Timezone Injection (`/display`)
1. In `internal/display/handler.go`, enhanced `Handler` with `WithTimezone(tz string)` and `WithTimezoneProvider(provider func() string)` options, dynamic `SetTimezone(tz string)` setter, and thread-safe `Timezone() string` getter (defaulting to `"UTC"` if unspecified).
2. Updated `GetDisplay` to parse and execute `web/templates/display.html` as an `html/template` template passing `DisplayTemplateData{ Timezone: h.Timezone() }`, with automatic fallback to raw bytes for non-templated content and proper HEAD request handling.
3. Updated `web/templates/display.html` with `data-timezone="{{ .Timezone }}"` on `<body>`, `<div id="mirrormere-app">`, and `<div id="header-clock">`.

### B. Client-Side `Intl.DateTimeFormat` Formatting
1. In `web/static/js/display.js`, implemented `getTimezone()` resolving active timezone from DOM dataset attributes (`appEl.dataset.timezone`, `document.body.dataset.timezone`, `clockEl.dataset.timezone`).
2. Implemented `isValidTimezone(tz)` verifying validity via `Intl.DateTimeFormat` and rejecting unexpanded template variables (`{{ ... }}`).
3. Formatted time using `Intl.DateTimeFormat(undefined, { timeZone: tz, hour: 'numeric', minute: '2-digit' })` and date using `Intl.DateTimeFormat(undefined, { timeZone: tz, weekday: 'long', month: 'short', day: 'numeric' })`. Omission of explicit `hour12` respects user/browser locale conventions (12-hour vs 24-hour) while strictly binding to the household timezone.
4. Exported public API to `window.MirrormereDisplay` and `module.exports` for programmatic testing and headless integration.
5. Unref'd `setInterval` timer in Node.js environments (`clockTimer.unref()`) and guarded auto-initialization when imported as a CommonJS test module.

### C. Live Config Reload Broadcast (`header.update`)
1. In `internal/config/lkgc.go`, added `TimezoneChanged bool` to `ConfigDiff`, computed during `DiffConfigs` when `oldCfg.Timezone != newCfg.Timezone`.
2. In `internal/events/hydration.go`, enhanced `HeaderUpdateData` with `Timezone string json:"timezone,omitempty"`. During initial connection hydration, `BuildHydrationBatch` populates `Timezone` from the active snapshot.
3. In `internal/events/dispatcher.go`, updated `DispatchConfigReload` to broadcast a `header.update` SSE event when `diff.TimezoneChanged` is true.
4. In `web/static/js/display.js`, updated `handleHeaderUpdate(data)` to inspect `data.timezone`, update `currentTimezone` and DOM datasets, and trigger immediate clock re-render upon receiving live reload events.
5. Authored `api/schemas/header.update.json` formalizing the SSE schema per SPEC-001 §2.1 and SPEC-006 §2.B.

### D. Testing & Verification
1. Created `web/test/clock.test.js` using Node.js native test runner (`node --test`), verifying fixed instant formatting (`2026-09-25T20:00:00Z` -> `1:00 PM` in `America/Los_Angeles` under `TZ=UTC`), dynamic timezone updates via `handleHeaderUpdate`, and graceful fallback.
2. Added `TestHeaderClock_HouseholdTimezone_NodeRunner` in `web/web_test.go` and `run_node_tests` in `scripts/verify.sh`.
3. Added `TestGetDisplay_Timezone` in `internal/display/handler_test.go` verifying default UTC, static option, dynamic provider, SetTimezone, and HEAD request behavior.
4. Added `TestHub_DispatchConfigReload_TimezoneChange` in `internal/events/dispatcher_test.go`.
5. Updated `TestLKGC_DiffConfigs` in `internal/config/lkgc_test.go` and `TestBuildHydrationBatch_OrderingAndPayloads` in `internal/events/hydration_test.go`.

---

## 3. Consequences
- Kiosks and headless renderers in UTC containers faithfully display the household's local time and date.
- Updating `timezone` in `config.yaml` propagates live over SSE without container or browser restarts.
- System test coverage remains strictly >= 95.0% across all packages.
