# Architecture Decision Record: Tasks & Lists Local SQLite Storage Engine and Inspection API

- **Date:** 2026-09-26
- **Status:** Accepted
- **Context:** Implementing Issue #234 on `azylman/mirrormere` (SPEC-013 Phase 3, Chunk 3.4), fulfilling SPEC-008, SPEC-006 §7, and SPEC-003 §3.

---

## 1. Problem Statement
Prior to Chunk 3.4:
1. **Local Persistent Storage Engine Absent**: Mirrormere lacked a persistent, local-first storage engine for task lists, household checklists, and groceries per SPEC-008.
2. **CGO vs Hermetic Portability Constraints**: Traditional Go SQLite drivers (`mattn/go-sqlite3`) rely on CGO and cross-compilation toolchains, violating Mirrormere's static zero-CGO binary invariant across Linux x86_64, arm64, and macOS hosts.
3. **Write Contention & Concurrency Locks**: Concurrent read/write access against SQLite file databases risks `SQLITE_BUSY` database lock errors without dedicated connection pooling and WAL mode tuning.
4. **OpenAPI Inspection & Synchronization Missing**: No REST inspection endpoint existed for companion apps or CLI tools to inspect lists (`GET /api/lists/{list_id}/items`) with completed filters.
5. **Tasks Provider & Ambient Widget Package Missing**: No built-in `tasks` widget or provider existed to render ambient checklists with strikethrough styling and strict ambient display constraints (zero on-screen mutation controls).

---

## 2. Decision & Architecture

### A. Pure-Go Zero-CGO SQLite Engine (`internal/tasks/store.go`)
- Driver Selection: Employs `modernc.org/sqlite@v1.34.5`, compiling directly into pure Go machine code with zero CGO dependencies. Explicitly pinned `v1.34.5` to avoid Go 1.25 toolchain dependency bumps in upstream `x/sys`.
- Database Tuning & Concurrency:
  - Automatically initializes parent directories (e.g. `/data`) on cold start.
  - Configures pragmas on startup: `PRAGMA journal_mode = WAL;`, `PRAGMA synchronous = NORMAL;`, `PRAGMA busy_timeout = 5000;`, `PRAGMA foreign_keys = ON;`, `PRAGMA temp_store = MEMORY;`.
  - Single-Writer Connection Pool: Restricts `db.SetMaxOpenConns(1)`, `db.SetMaxIdleConns(1)`, and `db.SetConnMaxLifetime(0)` to completely eliminate write contention and lock timeouts under concurrent HTTP read and provider queries.
- Schema Migrations & Composite Indexes:
  - `lists`: `id` (PRIMARY KEY), `name`, `source`, `sections` (JSON TEXT), `created_at`, `updated_at`.
  - `list_items`: `id`, `list_id` (FOREIGN KEY CASCADE), `title`, `done` (INTEGER 0/1), `section`, `position`, `assignee` (NULLABLE), `due_date` (NULLABLE), `created_at`, `updated_at`. PRIMARY KEY `(list_id, id)`.
  - Composite indexes: `idx_list_items_list_done_pos` on `(list_id, done, position ASC, created_at ASC)` for fast uncompleted queries, and `idx_list_items_list_done_updated` on `(list_id, done, updated_at DESC)` for recently completed queries.
- Leak Prevention: Centralizes row scanning in `queryItems` with guaranteed `defer rows.Close()` and explicit row error checking.

### B. OpenAPI 3.1 Inspection Contract & Server Routing (`internal/server/lists.go`)
- OpenAPI Contract (`api/openapi.yaml`): Added `GET /api/lists/{list_id}/items` returning array of `ListItem` objects with optional boolean `include_done` query parameter (default: true). Generated Go types via `oapi-codegen` with zero drift.
- Nullable Pointer Semantics: Modeled `assignee` and `due_date` as `type: string, nullable: true` to generate clean `*string` pointers in Go that serialize to `null` or omitted when absent.
- HTTP Server Architecture:
  - Added `ListsHandler` interface in `internal/server/server.go`: `GetListItems(w http.ResponseWriter, r *http.Request, listID string)`.
  - Implemented `DefaultListsHandler` in `internal/server/lists.go`: validates HTTP method (GET, HEAD, OPTIONS), parses `include_done` with `strconv.ParseBool` returning 400 on invalid input, checks for `tasks.ErrListNotFound` returning 404 `ErrorResponse`, and returns 200 with JSON items (marshals as `[]` when empty).
  - Wired `/api/lists/{list_id}/items` route into `Server.mux` with `RegisterListsHandler` support.

### C. Tasks Provider & Cold-Boot Auto-Seeding (`internal/provider/tasks.go`)
- Registered `"tasks"` in `DefaultRegistry`.
- Implements `Provider` interface: `Init`, `Fetch`, `Subscribe` (no-op), `Shutdown`.
- Configuration Schema: `list_id` (defaults to widget ID), `list_name` (defaults to `list_id`), `source` (default "local"), `show_completed` (default 3), `db_path` (default "/data/lists.db").
- Cold-Boot Resilience: In `Fetch`, if `store.GetTasksSnapshot` returns `ErrListNotFound`, the provider automatically seeds the initial empty list definition into SQLite, ensuring companion endpoints return 200 with empty arrays instead of 404s.

### D. Built-in `tasks` Ambient Widget Package (`widgets/tasks/`)
- Declarative Manifest (`widgets/tasks/manifest.yaml`): Declares `name: tasks`, `provider: tasks`, dimensions `[2, 1]`, supported dimensions `[2, 1]` through `[4, 2]`, and JSON Schema without forbidden `default` keywords.
- Cyber HUD Ambient Checklist (`widgets/tasks/views/widget.html`):
  - Renders list title header with list icon and source badge.
  - Uses ambient SVG circle glyphs for unchecked tasks and purple accented check circles for completed tasks.
  - Applies strikethrough styling (`text-decoration: line-through; opacity: 0.55;`) to completed tasks.
  - Displays section tags, assignee tags, and due date tags.
  - Strict Ambient Display: Zero on-screen interactive form controls or checkboxes (mutation happens via companion API or upstream provider sync).
  - Handles empty ("All tasks completed") and cold loading ("Loading checklist...") states.

---

## 3. Verification & Compliance
- **Hermetic Testing**: All database unit tests use isolated temporary files via `t.TempDir()`, validating pragma application, WAL mode, cascading deletes, concurrency with 8 parallel workers, and error paths.
- **Statement Coverage**:
  - `internal/tasks`: 95.3% statement coverage (>= 95.0% floor).
  - `internal/server`: 98.3% statement coverage (>= 95.0% floor).
  - `internal/provider`: 95.9% statement coverage (>= 95.0% floor).
  - `internal/api`: 95.4% statement coverage (>= 95.0% floor).
- **Verification Pipeline**: Full pass of `./scripts/verify.sh --staged` covering go generate, go vet, golangci-lint, deadcode, Go unit tests, and Node UI integration tests.
