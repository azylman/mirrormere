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

### Authentication
Mirrormere is a LAN appliance, not a cloud service, and has no user accounts.
Two rules still hold:
- The daemon binds to the local network by default. Exposure through a tunnel
  (Cloudflare, Tailscale Funnel) requires setting `server.shared_secret`,
  which clients then send as `Authorization: Bearer <secret>`.
- Upstream credentials (Google OAuth tokens, Home Assistant long-lived tokens,
  private API tokens) live in config or env files readable only by the daemon.
  They are the most sensitive thing Mirrormere holds.

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

```yaml
providers:
  lists:
    db_path: "/data/lists.db"       # Local SQLite database path (default: /data/lists.db)
    poll_interval_seconds: 120
    lists:
      - id: groceries
        name: "Groceries"
        source: local
      - id: family-todo
        name: "To Do"
        source: http
        http:
          base_url: "http://192.168.1.77:8300/lists/family-todo"
          token_env: HOUSEHOLD_LISTS_TOKEN
      - id: chores
        name: "Chores"
        source: gtasks
        gtasks:
          tasklist_id: "MDk3..."
```

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
The local SQLite store persists at `/data/lists.db` (configurable via `providers.lists.db_path`):

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
All automated unit and contract tests in `pkg/storage/sqlite` or `providers/lists` MUST use in-memory SQLite handles (`file::memory:?cache=shared`) or temporary file fixtures (`t.TempDir()`). Unit tests must never write to `/data` or touch shared disk state.

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
