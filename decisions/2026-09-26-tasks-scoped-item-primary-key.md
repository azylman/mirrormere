# Architecture Decision Record: List-Scoped Primary Key for Tasks Items

- **Date:** 2026-09-26
- **Status:** Accepted
- **Context:** Resolving Issue #266 on `azylman/mirrormere`: `fix(tasks): list_items id is a global primary key, so two lists with the same item id overwrite each other`.

---

## 1. Problem Statement
In `internal/tasks/store.go`, the `list_items` DDL previously declared `id TEXT PRIMARY KEY`.
Furthermore, `UpsertItem` and `SyncList` utilized `ON CONFLICT(id) DO UPDATE ...`.

Upstream task list adapters (e.g. `http`, Google Tasks) generate item IDs that are scoped locally to their parent list (such as `"i1"` or integer offsets). When two distinct task lists (e.g. `groceries` and `chores`) contain items with colliding IDs:
1. Syncing List B with item `"i1"` causes SQLite to match the global primary key on List A's row, overwriting List A's row with List B's content while retaining `list_id = A` (or clobbering it depending on the statement).
2. List B subsequently misses the item during queries because `WHERE list_id = 'chores'` finds no matching rows.
3. On every subsequent poll cycle of List A or List B, `SyncList` perceives item changes or missing items, causing continuous diff churn (`changed=true` on every poll) and high event bus churn.
4. Item deletion in one list inadvertently deletes or fails to isolate items in the sibling list.

---

## 2. Decision & Architecture

### A. Composite Primary Key on `(list_id, id)`
- **Schema Update**:
  `list_items` table definition updated to:
  ```sql
  CREATE TABLE IF NOT EXISTS list_items (
      id TEXT NOT NULL,
      list_id TEXT NOT NULL REFERENCES lists(id) ON DELETE CASCADE,
      title TEXT NOT NULL,
      done BOOLEAN NOT NULL DEFAULT 0,
      section TEXT,
      position INTEGER NOT NULL DEFAULT 0,
      assignee TEXT,
      due_date TEXT,
      created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
      updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
      PRIMARY KEY (list_id, id)
  );
  ```
- **Upsert Conflict Target**:
  Both `UpsertItem` and `SyncList` now target `ON CONFLICT(list_id, id) DO UPDATE SET ...`, allowing identical item IDs across different lists to coexist independently.

### B. Transparent SQLite Table Migration
- In `SQLiteStore.migrate()`, the store queries `PRAGMA table_info(list_items)`.
- If `list_items` already exists from a prior release where `list_id` was not part of the primary key (`pk == 0` for `list_id`), an automatic table recreation migration is executed:
  1. Creates temporary table `list_items_migrated` with `PRIMARY KEY (list_id, id)`.
  2. Copies existing rows: `INSERT OR REPLACE INTO list_items_migrated SELECT ... FROM list_items`.
  3. Drops old `list_items` table.
  4. Renames `list_items_migrated` to `list_items`.
  5. Recreates composite indexes (`idx_list_items_list_id_position`, `idx_list_items_updated_at`, `idx_list_items_list_done_pos`, `idx_list_items_list_done_updated`).
- Upgrades existing persistent databases without data loss or operator intervention.

### C. Spec Alignment
- Updated `specs/008-tasks-and-lists.md` schema documentation to explicitly define `PRIMARY KEY (list_id, id)` matching the architectural decision and implementation.

---

## 3. Verification & Compliance
- **Multi-List Isolation Test**: Added `TestSQLiteStore_MultiList_SharedItemIDs` to verify that two lists with colliding item IDs (`"item-1"`, `"item-2"`) sync cleanly without overwriting, return `changed=false` on repeat sync (zero diff churn), and isolate `UpsertItem` and `DeleteItem` operations.
- **Migration Test**: Added `TestSQLiteStore_Migration_LegacySingleColumnPK` creating a legacy SQLite database with `id TEXT PRIMARY KEY`, executing `NewSQLiteStore()`, and asserting that legacy items are preserved while composite primary keys allow new colliding IDs.
- **Test Coverage**: Maintains >= 95.5% statement test coverage across `internal/tasks` and 96.1% across `internal/tasks/adapters`.
