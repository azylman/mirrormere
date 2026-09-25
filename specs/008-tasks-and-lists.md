# SPEC-008: Tasks & Lists (Checklists, Grocery, Chores)

## Status
Proposed

## Context & Motivation
Both reference households want an ambient checklist surface on day one: grocery,
to-do, or chore lists shown on the Touch Kiosk and the Ambient E-Ink node. The two
households keep their lists in different places. One may use Google Tasks; the other
keeps lists in a locally hosted list service that already pushes selected lists to a
Skylight frame.

Mirrormere operates as a **strictly read-only ambient display** for tasks and lists.
It does not own the authoritative list or accept write mutations. It:
1. Ships an embedded local SQLite store, so a household can seed or ingest lists cleanly out of the box.
2. Lets each list point at an external **source of truth** through a pluggable read-only adapter (Google Tasks, generic HTTP service, or local store).
3. Presents every list through one uniform shape, rendering ambiently on the wall without bidirectional sync complexity, optimistic UI reconciliation, provisional IDs, or write conflict handling.

### Security & Local-Network Trust Model
Mirrormere operates strictly as an appliance on trusted local networks and has no user accounts:
- **Zero Inbound Authentication**: All inbound REST mutations, SSE streams, and webhook pushes require no authentication or authorization; all LAN calls are fully trusted.
- **Upstream Credentials**: Outbound integration credentials (Google OAuth tokens, Home Assistant long-lived tokens, private API keys) live in config or env files readable only by the daemon. They are the most sensitive thing Mirrormere holds.

---

## Data Model

```go
type List struct {
    ID        string    `json:"id"`         // stable, adapter-scoped: "local:groceries", "gtasks:MDk3..."
    Name      string    `json:"name"`
    Source    string    `json:"source"`     // adapter name: "local", "gtasks", "http"
    Sections  []string  `json:"sections"`   // optional ordered section names ("Produce", "Dairy")
    UpdatedAt time.Time `json:"updated_at"`
}

type ListItem struct {
    ID        string    `json:"id"`
    ListID    string    `json:"list_id"`
    Title     string    `json:"title"`
    Done      bool      `json:"done"`
    Section   string    `json:"section,omitempty"`
    Position  int       `json:"position"`
    Assignee  string    `json:"assignee,omitempty"` // chores: free-text household member
    DueDate   string    `json:"due_date,omitempty"` // YYYY-MM-DD, chores/to-dos only
    UpdatedAt time.Time `json:"updated_at"`
}
```

Chores are lists whose items carry `assignee` and/or `due_date`. No separate
chore model is needed for v1.

---

## Source Adapters (`providers/lists`)

```go
type ListSource interface {
    Name() string
    Lists(ctx context.Context) ([]List, error)
    Items(ctx context.Context, listID string) ([]ListItem, error)
    // Changes may return nil when the source cannot push; the daemon then polls.
    Changes(ctx context.Context) (<-chan ListChange, error)
}
```

| Adapter | Scope | Notes |
|---|---|---|
| `local` | Core | Pure-Go SQLite (`modernc.org/sqlite`, zero CGO; `lists`, `list_items` tables), active when `source: local` is explicitly declared. Operators and local tools can seed or modify `lists.db` directly for testing/demo environments. |
| `gtasks` | Core, optional | Google Tasks API. Needs OAuth; polled (default 120 s). |
| `http` | Core | Generic adapter for a household's own list service exposing the endpoint shape below. Lets private systems plug in without Go code. |
| Private | Sidecar / HTTP | Anything else (e.g. a Skylight bridge) runs as an out-of-process HTTP provider sidecar per SPEC-003. |

### Configuration Schema (`config.yaml`)

Each tasks/checklist widget instance configures standard instance settings (`id`, `type`, `dimensions`, `refresh_interval_seconds`) at the top level, with its list identity and upstream source parameters nested in `config:`:

```yaml
display:
  widgets:
    - id: daily-chores              # Primary widget definition establishing chores sync
      type: tasks
      dimensions: [2, 1]
      refresh_interval_seconds: 120
      config:
        list_id: "chores"           # Canonical list identifier (defaults to widget id if omitted)
        list_name: "Daily Chores"
        source: gtasks              # Explicit source adapter ("local" | "gtasks" | "http")
        gtasks:
          tasklist_id: "MDk3..."

    - id: groceries
      type: tasks
      dimensions: [2, 1]
      config:
        list_id: "groceries"
        list_name: "Groceries"
        source: local               # Explicit local SQLite database
        show_completed: 3

    - id: family-todo
      type: tasks
      dimensions: [2, 1]
      refresh_interval_seconds: 120
      config:
        list_id: "family-todo"
        list_name: "To Do"
        source: http                # Generic HTTP list service adapter
        http:
          base_url: "http://192.0.2.10:8300/lists/family-todo"
          token_env: HOUSEHOLD_LISTS_TOKEN

    - id: compact-chores            # Secondary consumer widget referencing existing list!
      type: tasks
      dimensions: [1, 1]
      config:
        list_id: "chores"           # References the chores list defined above (omits source)
        show_completed: 0
```

### Shared List State & Source Ownership Rules
1. **Canonical Identifier (`list_id`)**: Every list has a stable identifier (`list_id`). If omitted from `config:`, `list_id` defaults to the widget's instance `id`.
2. **Explicit Source Mandate & Validation (No Implicit Fallback)**: Every unique `list_id` MUST be backed by exactly one primary widget instance declaring an explicit `source:` (`gtasks`, `http`, or `local`). Omitting `source` does NOT default to `local`. If a widget defines a `list_id` but no widget in the configuration provides an explicit `source:` for that `list_id`, configuration validation fails fast at startup with an explicit diagnostic error: `[Mirrormere Config Error] tasks widget '<id>' references list_id '<list_id>' with no primary source definition`.
3. **Consumer Reference Widgets**: Any secondary widget on another screen (e.g. `id: compact-chores`) can display an existing list simply by declaring `list_id: "chores"` (omitting `source:`) with its own presentation parameters (e.g. `show_completed: 0`). It reuses the in-memory cache and background sync worker established by the primary definition without spinning up redundant polling loops.
4. **Conflict Validation**: If multiple widgets declare `source:` for the same `list_id`, their source configurations must be identical; conflicting definitions are rejected at startup with an explicit validation error.
5. **Read-Only Ingestion Invariant**: Mirrormere treats task and checklist widgets as strictly read-only ambient surfaces. Task creations, completion toggles, edits, and deletions are performed directly in the user's primary client (e.g. Google Tasks mobile app, web application, or household service). Mirrormere periodically polls or ingests updates from the source of truth, updating all widgets sharing `list_id` regardless of which screen is currently visible.

---

## Local SQLite Storage Architecture

Mirrormere ships an embedded, zero-maintenance local database for household lists, groceries, and chore items that do not sync to external cloud providers.

### 1. Pure Go Zero-CGO Driver Mandate
- **Driver Standard**: Mirrormere standardizes strictly on **`modernc.org/sqlite`** as its local SQLite engine across all database packages and build tooling.
- **Hermetic Zero-CGO Invariant**: CGO-dependent drivers (such as `github.com/mattn/go-sqlite3`) are **strictly prohibited**. The daemon compiles hermetically with `CGO_ENABLED=0` across all architectures (`linux/amd64`, `linux/arm64`), producing fully static, standalone binaries with zero dynamic libc bindings and no host C-compiler toolchain dependencies.
- **Concurrency & Connection Pool**: SQLite writers require serialization. The local store configures single-writer connection pooling via `SetMaxOpenConns(1)` (or dedicated single-writer serialized locks) with a busy timeout to eliminate write contention and `SQLITE_BUSY` errors.

### 2. Pragmas & Operational Modes
On database connection initialization, the storage engine executes the following connection pragmas:
```sql
PRAGMA journal_mode = WAL;         -- Write-Ahead Logging for high concurrency and crash resilience
PRAGMA synchronous = NORMAL;       -- Safe and performant for WAL mode
PRAGMA busy_timeout = 5000;        -- Wait up to 5000ms on locks before erroring
PRAGMA foreign_keys = ON;          -- Enforce relational cascades on list deletion
PRAGMA temp_store = MEMORY;        -- Avoid ephemeral disk I/O on embedded flash
```

### 3. Database Schema & Migrations
The local SQLite store persists at `/data/lists.db` (internal daemon default):

```sql
CREATE TABLE IF NOT EXISTS lists (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    source TEXT NOT NULL DEFAULT 'local',
    sections TEXT NOT NULL DEFAULT '[]', -- JSON string array
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS list_items (
    id TEXT PRIMARY KEY,
    list_id TEXT NOT NULL REFERENCES lists(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    done BOOLEAN NOT NULL DEFAULT 0,
    section TEXT,
    position INTEGER NOT NULL DEFAULT 0,
    assignee TEXT,
    due_date TEXT, -- YYYY-MM-DD
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_list_items_list_id_position ON list_items(list_id, position);
CREATE INDEX IF NOT EXISTS idx_list_items_updated_at ON list_items(updated_at);
```

### 4. Hermetic Testing Policy
All automated unit and contract tests in `internal/storage/sqlite` or `internal/providers/lists` MUST use in-memory SQLite handles (`file::memory:?cache=shared`) or temporary file fixtures (`t.TempDir()`). Unit tests must never write to `/data` or touch shared disk state.

---

## Wire Protocol

Lists follow SPEC-006: state flows out over SSE (`widget.update`), and snapshot state can be inspected via `GET /api/widgets/{widget_id}/state` or `GET /api/lists/{list_id}/items`. Task widgets are strictly read-only ambient surfaces—Core exposes no list mutation endpoints (`POST/PATCH/DELETE /api/lists/...`) and no on-screen widget action handlers (`toggle_item`, `add_item`).

### Reads: `widget.update`
A list widget publishes its full current list (items are few; no deltas):
```http
event: widget.update
id: evt_1727217000_07
data: {"widget_id":"groceries","timestamp":"2026-09-24T22:30:00Z","state":"healthy","data":{"list":{"id":"groceries","name":"Groceries","source":"local"},"items":[{"id":"i1","title":"Oat milk","done":false,"section":"Dairy","position":0}]}}
```

### Snapshot Reads
Snapshot reads for clients that do not hold an SSE stream:
- `GET /api/widgets/{widget_id}/state` returns the widget's current cached state payload (SPEC-006 §2).
- `GET /api/lists/{list_id}/items` returns the full item list for a canonical `list_id` (`include_done=false` optional query per SPEC-006 §7).

### `http` adapter contract
A household list service is compatible for read-only ingestion if it serves:
- `GET  {base_url}` → `List`
- `GET  {base_url}/items` → `[]ListItem`

---

## Consistency Rules

1. **Source of Truth is External.** Mirrormere holds only a local cache for rendering, never an authoritative or mutable copy. State reflects what the upstream source actually provides.
2. **Zero Inbound Write Conflicts.** Because Mirrormere is strictly read-only for task lists, there is no bidirectional cloud synchronization, no provisional ID reconciliation, no 409 conflict rollbacks, and no client-side merge logic.
3. **Downstream Mirrors are the Source's Job.** If a household also shows a list on another device (e.g. a Skylight frame), the source system pushes to it. Mirrormere does not fan out writes to multiple destinations.
4. **Source Outage Degrades Gracefully.** When an adapter fails or an upstream service is unreachable, the widget retains its last known good cached state and marks the provider degraded in `system.status`.
5. **Push Webhooks Prohibited on List Widgets.** Pushing arbitrary state to `POST /api/widgets/{widget_id}/push` on a list widget is rejected with `409 Conflict`. List widgets are managed strictly via their configured source adapter (polling or provider push channel) to maintain cache integrity.

---

## Display Profile Behavior

### Touch Kiosk (Profile A)
- **Ambient Read-Only Display**: Checklists serve as glanceable ambient references (e.g. kitchen grocery glances or daily chore boards). Tapping checkboxes does not mutate items (zero write actions).
- **Zero On-Screen Keyboard (OSK)**: No virtual keyboard daemon or touch keyboard overlays. List additions and task checking are handled on personal devices (phones, tablets, voice hubs) at the source.
- Touch gestures on the kiosk are reserved for navigation (e.g. screen swipes) and HUD media controls.

### Ambient E-Ink (Profile B)
- **Read-only.** Renders unchecked items first, then at most `show_completed`
  (default 3) recently completed items struck through, grouped by section.
- Re-render is triggered by `widget.update` but coalesced: at most one panel
  refresh per `refresh.min_refresh_seconds` (default 60 per SPEC-009).
- Items that overflow the widget cell render as "+N more" rather than
  shrinking text below the 1-bit legibility floor.
