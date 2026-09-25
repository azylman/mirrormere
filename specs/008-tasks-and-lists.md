# SPEC-008: Tasks & Lists (Checklists, Grocery, Chores)

## Status
Proposed

## Context & Motivation
Both reference households want a checklist surface on day one: a grocery or
to-do list on the Touch Kiosk, and the same list shown read-only on the E-Ink
node. The two households keep their lists in different places. One may use
Google Tasks; the other keeps lists in a locally hosted list service that
already pushes selected lists to a Skylight frame.

Mirrormere therefore must not own the one true list. It must:
1. Ship a working local list store, so a household with no list tool gets
   checklists out of the box.
2. Let each list point at an external **source of truth** through an adapter.
3. Present every list through one uniform shape, whatever sits behind it.

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
    Add(ctx context.Context, listID string, item ListItem) (ListItem, error)
    Update(ctx context.Context, listID, itemID string, patch ItemPatch) (ListItem, error)
    Delete(ctx context.Context, listID, itemID string) error
    // Changes may return nil when the source cannot push; the daemon then polls.
    Changes(ctx context.Context) (<-chan ListChange, error)
}
```

| Adapter | Scope | Notes |
|---|---|---|
| `local` | Core | Pure-Go SQLite (`modernc.org/sqlite`, zero CGO; `lists`, `list_items` tables), default for any list with no `source`. Pushes changes natively. |
| `gtasks` | Core, optional | Google Tasks API. Needs OAuth; polled (default 120 s). |
| `http` | Core | Generic adapter for a household's own list service exposing the endpoint shape below. Lets private systems plug in without Go code. |
| Private | Sidecar / HTTP | Anything else (e.g. a Skylight bridge) runs as an out-of-process HTTP provider sidecar per SPEC-003. |

### Configuration Schema (`config.yaml`)

Each tasks/checklist widget instance configures its list identity, upstream source, and sync cadence directly in `display.widgets[].config`:

```yaml
display:
  widgets:
    - id: daily-chores              # Primary widget definition establishing chores sync
      type: tasks
      dimensions: [2, 1]
      config:
        list_id: "chores"           # Canonical list identifier (defaults to widget id if omitted)
        list_name: "Daily Chores"
        source: gtasks              # Source adapter ("local" | "gtasks" | "http")
        refresh_interval_seconds: 120
        gtasks:
          tasklist_id: "MDk3..."

    - id: groceries
      type: tasks
      dimensions: [2, 1]
      config:
        list_id: "groceries"
        list_name: "Groceries"
        source: local               # Local SQLite database (default)
        show_completed: 3

    - id: family-todo
      type: tasks
      dimensions: [2, 1]
      config:
        list_id: "family-todo"
        list_name: "To Do"
        source: http                # Generic HTTP list service adapter
        refresh_interval_seconds: 120
        http:
          base_url: "http://192.0.2.10:8300/lists/family-todo"
          token_env: HOUSEHOLD_LISTS_TOKEN

    - id: compact-chores            # Secondary consumer widget referencing existing list!
      type: tasks
      dimensions: [1, 1]
      config:
        list_id: "chores"           # References the chores list defined above
        show_completed: 0
```

### Shared List State & Source Ownership Rules
1. **Canonical Identifier (`list_id`)**: Every list has a stable identifier (`list_id`). If omitted from `config:`, `list_id` defaults to the widget's instance `id`.
2. **Primary Source Definition**: A widget instance that specifies `source:` (and its adapter options such as `gtasks:` or `http:`) acts as the primary definition for that `list_id`, establishing its upstream sync loop in the Go daemon.
3. **Consumer Reference Widgets**: Any secondary widget on another screen (e.g. `id: compact-chores`) can display the same list simply by declaring `list_id: "chores"` with its own presentation parameters (e.g. `show_completed: 0`). It reuses the in-memory cache and background sync worker created by the primary definition without spinning up redundant polling loops.
4. **Conflict Validation**: If multiple widgets define `source` for the same `list_id`, their source configurations must be identical; conflicting definitions are rejected at startup with an explicit validation error.
5. **Decoupled Phone Writes**: Companion apps and local REST mutations (`POST /api/widgets/{widget_id}/action` or `/api/lists/{list_id}/items`) interact with the list provider keyed by `list_id`, persisting changes directly to SQLite (`/data/lists.db`) or forwarding upstream regardless of which screen is currently visible.

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

Lists follow SPEC-006 unchanged: state flows out over SSE, mutations flow in
over the widget action endpoint. There are no list-specific transports.

### Reads: `widget.update`
A list widget publishes its full current list (items are few; no deltas):
```http
event: widget.update
id: evt_1727217000_07
data: {"widget_id":"groceries","timestamp":"2026-09-24T22:30:00Z","data":{"list":{"id":"groceries","name":"Groceries","source":"local"},"items":[{"id":"i1","title":"Oat milk","done":false,"section":"Dairy","position":0}]}}
```

A snapshot read for clients that do not hold an SSE stream:
- `GET /api/widgets/{widget_id}/state` returns the same `data` object.

### Writes: `POST /api/widgets/{widget_id}/action`

| `action` | `params` |
|---|---|
| `add_item` | `{"title": "...", "section": "..."}` |
| `toggle_item` | `{"item_id": "...", "done": true}` |
| `update_item` | `{"item_id": "...", "title": "...", "section": "...", "position": 3}` (all optional but `item_id`) |
| `delete_item` | `{"item_id": "..."}` |
| `clear_done` | `{}` |

Responses use SPEC-006 codes. `200` when the source confirmed the write
synchronously, `202` when it was accepted but not yet confirmed, `502` when the
source rejected it or was unreachable.

### `http` adapter contract
A household list service is compatible if it serves:
- `GET  {base_url}` → `List`
- `GET  {base_url}/items` → `[]ListItem`
- `POST {base_url}/items` → created `ListItem`
- `PATCH {base_url}/items/{id}` → updated `ListItem`
- `DELETE {base_url}/items/{id}` → `204`

---

## Consistency Rules

1. **Writes go to the source.** An action is forwarded through the adapter to
   the system that owns the list. Mirrormere holds only a cache for rendering,
   never a second authoritative copy. The `widget.update` that follows reflects
   what the source actually stored.
2. **Last write wins on `updated_at`.** Concurrent edits from two surfaces
   (kiosk and phone app) resolve by the newest timestamp. Household lists need
   no merge logic.
3. **Downstream mirrors are the source's job.** If a household also shows a
   list on another device (e.g. a Skylight frame), the source system pushes to
   it. Mirrormere does not fan out writes to multiple destinations.
4. **Source outage degrades to read-only.** When an adapter fails, the widget
   keeps its last good state, marks it stale in `system.status`, and rejects
   actions with `502`. The kiosk rolls back its optimistic update (SPEC-006 §3).

---

## Display Profile Behavior

### Touch Kiosk (Profile A)
- Tap-and-gesture interaction only: tap checkbox to toggle completion state, swipe to dismiss/delete.
- Zero On-Screen Keyboard (OSK): task and list additions or text edits are handled companion/phone-first (via mobile browser, companion app, or voice pipeline per SPEC-002, SPEC-010, and SPEC-011). No virtual keyboard overlay or daemon runs on the kiosk.
- Optimistic updates per SPEC-006.

### Ambient E-Ink (Profile B)
- **Read-only.** Renders unchecked items first, then at most the three most
  recently completed items struck through, grouped by section.
- Re-render is triggered by `widget.update` but coalesced: at most one panel
  refresh per `eink.min_refresh_seconds` (default 60), so a burst of kiosk taps
  produces one refresh.
- Items that overflow the widget cell render as "+N more" rather than
  shrinking text below the 1-bit legibility floor.
