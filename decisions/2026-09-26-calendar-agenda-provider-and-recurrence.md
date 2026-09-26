# Architecture Decision Record: Calendar-Agenda Provider and Recurrence Expansion

- **Date:** 2026-09-26
- **Status:** Accepted
- **Context:** Implementing Issue #231 on `azylman/mirrormere` (SPEC-013 Phase 3, Chunk 3.3B), fulfilling SPEC-007 §1, SPEC-003 §3, and SPEC-006 §2.B.

---

## 1. Problem Statement
Prior to Chunk 3.3B:
1. **Calendar Ingest Driver Absent**: No built-in driver existed for the `"calendar-agenda"` provider type to ingest standard RFC 5545 iCalendar (`.ics`, `webcal://`) feeds, resolve multiple calendar feeds, or output structured `CalendarSnapshot` domain models.
2. **Recurrence & Exception Evaluation Missing**: No mechanism existed to expand RFC 5545 `RRULE` patterns across arbitrary rolling date windows (`[now - window_days_past, now + window_days_future]`), filter out `EXDATE` exclusions, or patch overridden occurrences (`RECURRENCE-ID`).
3. **All-Day DTEND Exclusivity Pitfall**: In RFC 5545, `DTEND` for `VALUE=DATE` all-day events is non-inclusive (e.g. a single-day event on 2026-09-25 specifies `DTSTART:20260925` and `DTEND:20260926`). Naive renderers evaluate this as spanning two days (Sept 25–26).
4. **Slowloris & Concurrency Vulnerabilities**: Sequential HTTP fetching across multiple remote calendar feeds risked cascading 10-second timeouts. Unbounded response streams risked memory exhaustion or slowloris denial-of-service.
5. **Built-in Widget Package Missing**: No built-in `widgets/calendar-agenda` package existed with declarative `manifest.yaml` and Cyber HUD template `views/widget.html`.

---

## 2. Decision & Architecture

### A. RFC 5545 iCal Ingest Driver (`internal/provider/calendar.go`)
- Registered `"calendar-agenda"` and alias `"calendar"` in `DefaultRegistry`.
- Implements `Provider` interface: `Init`, `Fetch`, `Subscribe` (no-op), and `Shutdown`.
- Structured Secret Resolution: In `Init`, parses `calendars` configurations and resolves `url_env` via structured path `calendars[idx].url` in `InitOptions.Secrets`, falling back to `opts.GetSecret(urlEnv)` and `os.Getenv(urlEnv)`. Fails fast on startup if configured environment variable is unset or empty.
- Scheme Normalization & Slowloris Defense: Normalizes `webcal://` URLs to `https://`. Enforces Slowloris payload limit via `io.LimitReader(resp.Body, maxCalendarFeedBytes+1)` (2MB cap), returning an explicit error if the body exceeds 2MB rather than silently truncating.
- Parallel Ingestion & Partial Fault Tolerance: Fetches all enabled calendar sources concurrently using goroutines and `sync.WaitGroup`. If one feed fails while others succeed, the provider continues, aggregates available events, and marks `sync_status: "partial"`. Fails only if all enabled calendars fail.

### B. Two-Pass Recurrence Engine & RFC 5545 Normalization
- Integrates `github.com/arran4/golang-ical` and `github.com/teambition/rrule-go`.
- Pass 1 (Exception Discovery): Catalogs all `VEVENT` components containing `RECURRENCE-ID`, indexing overridden occurrence timestamps by event `UID`.
- Pass 2 (Expansion & Normalization):
  - Parses master recurring events with `RRULE`, setting `DTSTART` and generating occurrences intersecting `[windowStart, windowEnd]`.
  - Infinite RRULE Defense: Rejects runaway high-frequency rules (`FREQ=SECONDLY`, `FREQ=MINUTELY`) and clamps maximum generated occurrences per event to 500.
  - Exclusions & Overrides: Drops occurrences matching any `EXDATE` exclusion or `RECURRENCE-ID` timestamp discovered in Pass 1.
  - Overridden Occurrences: Evaluated as discrete events and appended to the final schedule.
  - Multi-Day & All-Day Inclusivity: Normalizes `DTEND` for `VALUE=DATE` events by subtracting 24 hours from non-inclusive ends, rendering true inclusive spans (e.g. `2026-09-25` to `2026-09-25` for single-day events).
  - Timezone Normalization: Inspects `TZID` parameters and parses local wall-clock times into `time.Time` instances with designated location or UTC.

### C. Built-in `calendar-agenda` Widget Package
- Declarative Manifest (`widgets/calendar-agenda/manifest.yaml`): Declares `name: calendar-agenda`, provider `calendar-agenda`, default dimensions `[4, 2]`, supported dimensions `[2, 1]` through `[6, 2]`, and JSON Schema without forbidden `default` keywords.
- Cyber HUD Layout (`widgets/calendar-agenda/views/widget.html`):
  - Groups events under date headers using `$prevDate` check and `relDate` helper (rendering "TODAY", "TOMORROW", or "Monday, Jan 2").
  - Displays mono time chips (`15:04` or "All Day"), calendar color accent bars, bold event titles, and calendar name/location metadata.
  - Supports touch-optimized vertical scroll (`touch-action: pan-y; -webkit-overflow-scrolling: touch;`).
  - Implements localized empty state ("No upcoming events") and cold loading state ("Loading calendar agenda...").

---

## 3. Verification & Compliance
- **Hermetic Unit Testing**: 12 comprehensive test suites in `internal/provider/calendar_test.go` covering single/all-day events, daily/weekly recurrence, `EXDATE` exclusions, `RECURRENCE-ID` overrides, timezone handling, ISO 8601 duration parsing, Slowloris 2MB ceiling, structured secrets, high-frequency RRULE protection, partial calendar failures, and registry lifecycle.
- **Statement Coverage**: Maintained 95.5% statement coverage in `internal/provider` and 96.9% in `internal/render`, strictly exceeding the >= 95.0% floor across all packages.
- **Verification Pipeline**: Verified clean `./scripts/verify.sh --staged` compliance including code generation, go vet, golangci-lint, deadcode analysis, Go unit tests, and Node UI integration tests.
