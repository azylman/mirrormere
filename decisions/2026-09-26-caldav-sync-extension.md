# Architecture Decision Record: CalDAV Protocol Sync Extension

- **Date:** 2026-09-26
- **Status:** Accepted
- **Context:** Implementing Issue #232 on `azylman/mirrormere` (SPEC-013 Phase 3, Chunk 3.3C), fulfilling SPEC-007 §1, SPEC-003 §3, and SPEC-006 §2.B.

---

## 1. Problem Statement
Following Chunk 3.3B's implementation of the `calendar-agenda` provider for static and polling RFC 5545 iCalendar (`.ics`, `webcal://`) feeds:
1. **CalDAV Protocol Absence**: No driver existed to interact with CalDAV calendar stores (RFC 4791) such as Nextcloud, Apple iCloud, Radicale, Fastmail, Baïkal, or Google Calendar via CalDAV.
2. **Server-Side Range Filtering**: Fetching full calendar feeds via HTTP GET over long-lived personal accounts transfers decades of historical events. CalDAV servers require XML-based `REPORT` methods with `<c:calendar-query>` and `<c:time-range>` to restrict returned data to the configured rolling window (`[now - window_days_past, now + window_days_future]`).
3. **Endpoint Discovery Traversal**: CalDAV URLs provided by users or services often point to server roots, user principals (`/principals/users/...`), or calendar home sets rather than direct calendar collection endpoints. Automated discovery via `PROPFIND` traversal (`current-user-principal`, `calendar-home-set`) is required to resolve the actual collection URL.
4. **Authentication & Secret Isolation**: CalDAV requires authenticated HTTP sessions via HTTP Basic Auth (`Authorization: Basic`) or Bearer tokens (`Authorization: Bearer`). Credentials must be resolved via the 3-tier secret pipeline without hardcoding or leaking tokens in error messages or logs.
5. **Slowloris & Resource Defense**: Remote CalDAV multi-status XML payloads must be bounded with a strict payload ceiling (2MB) while preventing XML bomb or slowloris denial-of-service vectors.

---

## 2. Decision & Architecture

### A. CalDAV Client Driver (`internal/provider/caldav.go`)
- **`CalDAVClient`**: Encapsulates `*http.Client`, `*slog.Logger`, and an in-memory thread-safe discovery cache guarded by `sync.RWMutex`.
- **RFC 4791 `REPORT` Calendar Query**:
  - Issues `REPORT` HTTP requests with `Depth: 1` and `Content-Type: application/xml`.
  - Formats query filter with `<c:time-range>` in strict UTC format (`YYYYMMDD'T'HHMMSS'Z'`) per RFC 4791 §9.9:
    ```xml
    <c:calendar-query xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
      <d:prop><c:calendar-data/></d:prop>
      <c:filter>
        <c:comp-filter name="VCALENDAR">
          <c:comp-filter name="VEVENT">
            <c:time-range start="..." end="..."/>
          </c:comp-filter>
        </c:comp-filter>
      </c:filter>
    </c:calendar-query>
    ```
- **Slowloris & Body Bounds**: Wraps response streams in `io.LimitReader(resp.Body, maxCalendarFeedBytes+1)` (2MB limit). Rejects oversized payloads with an explicit error to defend against unbounded payloads.
- **Namespace-Tolerant Multi-Status Parsing**: Unmarshals RFC 2518 / RFC 4791 `207 Multi-Status` XML. Inspects `<propstat>` blocks for HTTP 200 status codes across namespace variations (`d:`, `c:`, `cal:`), extracting embedded `<calendar-data>`. Implements fallback detection for servers returning raw iCalendar text bodies.
- **Dynamic Collection Discovery**:
  - `DiscoverCalendarURL` tests direct collection access via `PROPFIND` Depth 0. If `<calendar/>` resource type is present, it returns immediately (fast path).
  - Otherwise traverses `<calendar-home-set>` or `<current-user-principal>` properties via `PROPFIND` Depth 0, followed by Depth 1 enumeration on the home set to identify calendar collections matching the configured calendar name or fallback to the first discovered collection.
  - Resolves relative XML `<href>` links against the base request URL using `url.URL.ResolveReference` and normalizes trailing slashes.
  - Caches discovered collection URLs in memory, avoiding discovery overhead on subsequent poll cycles.

### B. Configuration & Credential Resolution (`internal/provider/calendar.go`)
- **Extended `CalendarSource`**: Adds `Type` (`"ical"` or `"caldav"`, default `"ical"`), `Username`, `UsernameEnv`, `Password`, `PasswordEnv`, `Token`, and `TokenEnv`.
- **3-Tier Secret Resolution Pipeline**:
  - Tier 1: Structured secrets in `InitOptions.Secrets` (e.g. `calendars[0].url`, `calendars[0].username`, `calendars[0].password`, `calendars[0].token`).
  - Tier 2: Named environment keys in `InitOptions.Secrets` matching `*_env`.
  - Tier 3: Host environment fallback via `os.Getenv`.
- **Validation & Redaction**:
  - Enforces mutual exclusivity between Basic Auth (`username`/`password`) and Bearer Token (`token`).
  - Enforces fail-fast startup errors when any `*_env` field is specified but resolves to an empty or missing value.
  - Normalizes URLs and applies `url.Redacted()` across all logging and error reporting to prevent credential leakage.

### C. Recurrence Pipeline Integration & Manifest Updates
- **Recurrence Engine Reuse**: Extracted `<calendar-data>` components are parsed by `golang-ical` and funneled directly through the two-pass recurrence expansion engine (`parseCalendarEvents`), ensuring identical `RRULE`, `EXDATE`, `RECURRENCE-ID`, and all-day `DTEND` normalization across both iCal and CalDAV sources.
- **Declarative Manifest (`widgets/calendar-agenda/manifest.yaml`)**: Extended JSON Schema items properties to permit `type`, `username`, `username_env`, `password`, `password_env`, `token`, and `token_env` while strictly omitting forbidden `default` schema keywords.

---

## 3. Verification & Compliance
- **Hermetic Testing**: Comprehensive unit tests in `internal/provider/caldav_test.go` and internal tests in `internal/provider/caldav_internal_test.go` verifying:
  - Direct `REPORT` queries with UTC time ranges.
  - HTTP Basic Auth and Bearer Token header generation and validation.
  - Multi-step `PROPFIND` calendar-home-set and collection discovery.
  - Multi-status 207 response parsing with multiple events and empty responses.
  - Slowloris 2MB ceiling rejection.
  - Fallback raw iCalendar response handling.
  - URL credential redaction and relative href resolution.
  - Partial failure handling and multi-calendar merging in `CalendarProvider.Fetch`.
- **Statement Coverage**: Maintained >= 95.0% statement coverage across all packages (`internal/provider` passes at 95.1%+).
- **Verification Pipeline**: Verified clean `./scripts/verify.sh --staged` compliance covering codegen, go vet, golangci-lint, deadcode, Go unit tests, and Node UI integration tests.
