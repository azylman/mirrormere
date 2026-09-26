# ADR: Transactional Composite-PK SQLite Migration and Orphan Recovery for Tasks

## Context & Problem Statement
In PR #296 (commit ea238af, resolving Issue #266), Mirrormere introduced list-scoped composite primary keys (`PRIMARY KEY (list_id, id)`) on `list_items` to eliminate cross-list item clobbering and diff churn. To migrate existing single-column PK tables transparently, `SQLiteStore.migrate()` executed a table rebuild (`CREATE TABLE list_items_migrated ... INSERT ... DROP TABLE list_items ... ALTER TABLE list_items_migrated RENAME TO list_items`).

In Issue #299, Mike Carmody identified that this migration was executed without an explicit transaction (`db.Exec` under SQLite autocommit). If the server crashed or was terminated mid-migration:
1. **Crash before `DROP TABLE list_items`**: On the next startup, `PRAGMA table_info(list_items)` still observed the legacy PK. `migrate()` attempted `CREATE TABLE list_items_migrated`, which failed with `table list_items_migrated already exists`. `NewSQLiteStore` failed completely, forcing `cmd/server/main.go` to fall back to an in-memory database (`:memory:`), causing persistent data loss on every subsequent boot.
2. **Crash after `DROP TABLE list_items` but before `RENAME`**: On the next startup, `CREATE TABLE IF NOT EXISTS list_items` created an empty composite-PK table. The PRAGMA check passed, leaving the user's actual tasks orphaned in `list_items_migrated` indefinitely.

## Decision
1. **Atomic Transactional Migration**:
   - Wrapped the entire rebuild block in an explicit database transaction (`tx, err := s.db.Begin()`), with deferred rollback (`defer func() { if tx != nil { _ = tx.Rollback() } }()`) and explicit commit (`tx.Commit()`).
   - SQLite supports fully transactional DDL. Any mid-flight crash or statement error immediately rolls back table creation, row copies, drops, and renames.

2. **Idempotent Leftover Table Cleanup**:
   - Started the migration transaction with `DROP TABLE IF EXISTS list_items_migrated;`.
   - Any stale or incompatible `list_items_migrated` table created by an interrupted run from older code is cleanly purged before recreation.

3. **Orphan Row Recovery**:
   - Prior to migration, `migrate()` queries `sqlite_master` for `list_items_migrated`.
   - If `list_items_migrated` exists and `list_items` is empty (recovering from an unpatched crash between `DROP` and `RENAME`), all rows from `list_items_migrated` are copied into `list_items` before `list_items_migrated` is dropped.

4. **Automated Hermetic Tests**:
   - Added `TestSQLiteStore_Migration_PreExistingMigratedTable` asserting that pre-existing `list_items_migrated` tables do not wedge startup and all legacy rows migrate successfully.
   - Added `TestSQLiteStore_Migration_OrphanedMigratedTableRecovery` asserting that orphaned rows from interrupted drops are recovered into `list_items`.
   - Updated `TestSQLiteStore_MigrateErrors` to verify that statement failures inside the transaction trigger rollback without leaving corrupt tables.

## Consequences & Alternatives Considered
- **External migration tools (e.g. migrate, goose)**:
  Rejected. Mirrormere runs as a zero-CGO, dependency-light static binary. A simple, self-healing transactional check within `migrate()` handles all schema evolutions with zero external operational overhead.
- **Positive**:
  Robust resilience against power loss, daemon restarts, or process termination during schema migrations. Guarantees durable data preservation for local task lists per SPEC-008.
