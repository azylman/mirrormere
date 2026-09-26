# Architecture Decision Record: SPEC Alignment for `header.update` Timezone Field and Reload Trigger

- **Date:** 2026-09-26
- **Status:** Accepted
- **Context:** Resolving Issue #222 on `azylman/mirrormere`, aligning SPEC-006, SPEC-007, SPEC-012, and `api/schemas/header.update.json` with the implementation delivered in PR #220 (#213).

---

## 1. Problem Statement
In PR #220 (Issue #213), Mirrormere bound the persistent header digital clock and date to the household `timezone` configured in `config.yaml`. To support this on connected displays:
1. `header.update` SSE payloads gained a `timezone` property carrying the configured IANA timezone string.
2. A new live reload trigger was added: when a live config reload modifies `timezone`, Mirrormere immediately broadcasts a fresh `header.update` event over SSE so client clocks and dates rebind dynamically without requiring a container or browser restart.
3. The authoritative JSON Schema in `api/schemas/header.update.json` was updated to define `timezone`.

However, the architecture specifications had not been updated to reflect these enhancements:
- **SPEC-006 §2.B**: Described `header.update` as pushed only when the autonomous weather poller completes an ingestion cycle, and its example payload only contained `timestamp` and `weather`.
- **SPEC-006 §3 (Item 3)**: Described initial state hydration for `header.update` with the two-field payload omitting `timezone`.
- **SPEC-007 §4**: Example JSON for header weather delivery omitted `timezone`.
- **SPEC-012 §5**: Stated that a `timezone` change updates the server's location pointer JIT, but omitted the immediate `header.update` broadcast to connected displays.

Without these specification updates, client implementations developed against the specs (such as the ambient e-ink display node in SPEC-009 or kiosk profile in SPEC-010) would omit timezone parsing and drift to local host or UTC defaults.

---

## 2. Decision & Architecture

### A. SPEC-006 Realtime Communications Alignment
1. **Section 2.B (`header.update`)**:
   - Updated the definition and payload example to include `timezone` (`"America/Los_Angeles"`).
   - Documented `timezone` as a required string field carrying the authoritative IANA timezone.
   - Defined both triggers:
     1. Autonomous weather poller ingestion cycle (SPEC-007 §4).
     2. Live configuration reload updating `timezone` in `config.yaml` (SPEC-012 §5).
2. **Section 3 (Item 3 - Initial State Hydration)**:
   - Updated hydration batch documentation and example payload to include `timezone`.
   - Clarified that initial hydration primes display clocks and date formatters to the household timezone on first connection.

### B. SPEC-012 Live Configuration Reload Alignment
1. **Section 5 (Live Settings vs. Restart-Required Settings)**:
   - Updated `Timezone` live setting to explicitly state that in addition to updating Go's `time.Local` / location pointer JIT, it triggers an immediate `header.update` SSE event (SPEC-006 §2.B) to synchronize connected display clocks and date formatters without container restart.

### C. SPEC-007 Core Ingest Providers Alignment
1. **Section 4 (Header Weather Delivery)**:
   - Updated the JSON example payload to include `"timezone": "America/Los_Angeles"`.

### D. JSON Schema and Go Event Model Alignment
1. In `api/schemas/header.update.json`:
   - Added `"timezone"` to the `required` array (`["timestamp", "timezone", "weather"]`).
2. In `internal/events/hydration.go`:
   - Removed `omitempty` from `HeaderUpdateData.Timezone` struct tag, guaranteeing consistent JSON serialization across both live reloads and initial hydration batches.

---

## 3. Consequences
- Authoritative specs, JSON schemas, Go data structures, and display clients are in 100% alignment.
- Downstream display clients (touch kiosks and ambient e-ink nodes) have clear specifications for dynamic household timezone synchronization over SSE.
- All verification suites and quality gates remain green.
