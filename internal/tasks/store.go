package tasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

var defaultDBPath = "/data/lists.db"

const itemCols = "id, list_id, title, done, section, position, assignee, due_date, created_at, updated_at"

// Store defines the data access contract for task lists and items.
type Store interface {
	GetList(ctx context.Context, id string) (*List, error)
	GetListItems(ctx context.Context, listID string, includeDone bool) ([]ListItem, error)
	GetTasksSnapshot(ctx context.Context, listID string, showCompleted int) (*TasksSnapshot, error)
	UpsertList(ctx context.Context, list List) error
	UpsertItem(ctx context.Context, item ListItem) error
	SyncList(ctx context.Context, list List, items []ListItem) (bool, error)
	DeleteList(ctx context.Context, id string) error
	DeleteItem(ctx context.Context, listID, itemID string) error
	CompleteItem(ctx context.Context, listID, itemID string) error
	Close() error
}

type sqlExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// SQLiteStore implements Store using modernc.org/sqlite.
type SQLiteStore struct {
	db *sql.DB
	mu sync.RWMutex
}

// NewSQLiteStore opens and initializes a SQLite database at dbPath.
// If dbPath is empty, it defaults to /data/lists.db.
func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	if dbPath == "" {
		dbPath = defaultDBPath
	}
	if dir := filepath.Dir(dbPath); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create database directory: %w", err)
		}
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	initSQL := `PRAGMA journal_mode = WAL; PRAGMA synchronous = NORMAL; PRAGMA busy_timeout = 5000; PRAGMA foreign_keys = ON; PRAGMA temp_store = MEMORY;`
	if _, err := db.ExecContext(context.Background(), initSQL); err != nil {
		_ = db.Close() //nolint:errcheck
		return nil, fmt.Errorf("failed to execute pragmas: %w", err)
	}

	store := &SQLiteStore{db: db}
	if err := store.migrate(); err != nil {
		_ = db.Close() //nolint:errcheck
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}
	return store, nil
}

func (s *SQLiteStore) migrate() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("database is closed")
	}

	ctx := context.Background()
	ddl := `CREATE TABLE IF NOT EXISTS lists (
		id TEXT PRIMARY KEY, name TEXT NOT NULL, source TEXT NOT NULL DEFAULT 'local',
		sections TEXT NOT NULL DEFAULT '[]', created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
	CREATE TABLE IF NOT EXISTS list_items (
		id TEXT NOT NULL, list_id TEXT NOT NULL REFERENCES lists(id) ON DELETE CASCADE,
		title TEXT NOT NULL, done BOOLEAN NOT NULL DEFAULT 0, section TEXT,
		position INTEGER NOT NULL DEFAULT 0, assignee TEXT, due_date TEXT,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, PRIMARY KEY (list_id, id));
	CREATE INDEX IF NOT EXISTS idx_list_items_list_id_position ON list_items(list_id, position);
	CREATE INDEX IF NOT EXISTS idx_list_items_updated_at ON list_items(updated_at);
	CREATE INDEX IF NOT EXISTS idx_list_items_list_done_pos ON list_items(list_id, done, position, created_at);
	CREATE INDEX IF NOT EXISTS idx_list_items_list_done_updated ON list_items(list_id, done, updated_at DESC);`
	if _, err := s.db.ExecContext(ctx, ddl); err != nil {
		return err
	}

	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(list_items)`)
	if err != nil {
		return fmt.Errorf("failed to inspect list_items schema: %w", err)
	}

	var listIDIsPK bool
	for rows.Next() {
		var cid, notNull, pk int
		var name, colType string
		var dfltVal sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltVal, &pk); err == nil && name == "list_id" && pk > 0 {
			listIDIsPK = true
		}
	}
	_ = rows.Close() //nolint:errcheck

	var migratedTableExists bool
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) > 0 FROM sqlite_master WHERE type='table' AND name='list_items_migrated'`).Scan(&migratedTableExists); err == nil && migratedTableExists {
		var itemsCount int
		_ = s.db.QueryRowContext(ctx, `SELECT count(*) FROM list_items`).Scan(&itemsCount) //nolint:errcheck
		if itemsCount == 0 {
			if _, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO list_items (id, list_id, title, done, section, position, assignee, due_date, created_at, updated_at)
				SELECT id, list_id, title, done, section, position, assignee, due_date, created_at, updated_at FROM list_items_migrated;`); err != nil {
				return fmt.Errorf("failed to recover orphaned list_items_migrated rows: %w", err)
			}
		}
		if _, err := s.db.ExecContext(ctx, `DROP TABLE IF EXISTS list_items_migrated;`); err != nil {
			return fmt.Errorf("failed to drop leftover list_items_migrated table: %w", err)
		}
	}

	if !listIDIsPK {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("failed to begin migration transaction: %w", err)
		}
		defer func() {
			if tx != nil {
				_ = tx.Rollback() //nolint:errcheck
			}
		}()

		migrationStatements := []string{
			`DROP TABLE IF EXISTS list_items_migrated;`,
			`CREATE TABLE list_items_migrated (
				id TEXT NOT NULL, list_id TEXT NOT NULL REFERENCES lists(id) ON DELETE CASCADE,
				title TEXT NOT NULL, done BOOLEAN NOT NULL DEFAULT 0, section TEXT,
				position INTEGER NOT NULL DEFAULT 0, assignee TEXT, due_date TEXT,
				created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
				updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, PRIMARY KEY (list_id, id));`,
			`INSERT OR REPLACE INTO list_items_migrated (id, list_id, title, done, section, position, assignee, due_date, created_at, updated_at)
				SELECT id, list_id, title, done, section, position, assignee, due_date, created_at, updated_at FROM list_items;`,
			`DROP TABLE list_items;`,
			`ALTER TABLE list_items_migrated RENAME TO list_items;`,
			`CREATE INDEX IF NOT EXISTS idx_list_items_list_id_position ON list_items(list_id, position);`,
			`CREATE INDEX IF NOT EXISTS idx_list_items_updated_at ON list_items(updated_at);`,
			`CREATE INDEX IF NOT EXISTS idx_list_items_list_done_pos ON list_items(list_id, done, position, created_at);`,
			`CREATE INDEX IF NOT EXISTS idx_list_items_list_done_updated ON list_items(list_id, done, updated_at DESC);`,
		}
		for _, stmt := range migrationStatements {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("failed to execute migration statement: %w", err)
			}
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit migration transaction: %w", err)
		}
		tx = nil
	}
	return nil
}

// Close closes the underlying SQLite database handle.
func (s *SQLiteStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

// GetList retrieves a single List by its identifier. Returns ErrListNotFound if not present.
func (s *SQLiteStore) GetList(ctx context.Context, id string) (*List, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.getListLocked(ctx, id)
}

func unmarshalSections(raw string) []string {
	if raw == "" {
		return []string{}
	}
	var s []string
	if err := json.Unmarshal([]byte(raw), &s); err != nil || s == nil {
		return []string{}
	}
	return s
}

func (s *SQLiteStore) getListLocked(ctx context.Context, id string) (*List, error) {
	if s.db == nil {
		return nil, errors.New("database is closed")
	}

	var l List
	var sectionsJSON, createdAtStr, updatedAtStr string
	err := s.db.QueryRowContext(ctx, `SELECT id, name, source, sections, created_at, updated_at FROM lists WHERE id = ?`, id).
		Scan(&l.ID, &l.Name, &l.Source, &sectionsJSON, &createdAtStr, &updatedAtStr)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrListNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to scan list: %w", err)
	}

	l.CreatedAt = parseTime(createdAtStr)
	l.UpdatedAt = parseTime(updatedAtStr)
	l.Sections = unmarshalSections(sectionsJSON)
	return &l, nil
}

func (s *SQLiteStore) queryItems(ctx context.Context, query string, args ...any) (items []ListItem, err error) {
	rows, queryErr := s.db.QueryContext(ctx, query, args...)
	if queryErr != nil {
		return nil, fmt.Errorf("failed to query list items: %w", queryErr)
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck

	items = make([]ListItem, 0)
	for rows.Next() {
		item, scanErr := scanItem(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("error reading list items: %w", rowsErr)
	}
	return items, nil
}

// GetListItems returns all items for listID.
func (s *SQLiteStore) GetListItems(ctx context.Context, listID string, includeDone bool) ([]ListItem, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, errors.New("database is closed")
	}

	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM lists WHERE id = ?`, listID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrListNotFound
		}
		return nil, fmt.Errorf("failed to verify list existence: %w", err)
	}

	filter := " AND done = 0"
	if includeDone {
		filter = ""
	}
	return s.queryItems(ctx, fmt.Sprintf(`SELECT %s FROM list_items WHERE list_id = ?%s ORDER BY position ASC, created_at ASC`, itemCols, filter), listID)
}

// GetTasksSnapshot aggregates list metadata and items for widget presentation.
func (s *SQLiteStore) GetTasksSnapshot(ctx context.Context, listID string, showCompleted int) (*TasksSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	list, err := s.getListLocked(ctx, listID)
	if err != nil {
		return nil, err
	}

	items, err := s.queryItems(ctx, fmt.Sprintf(`SELECT %s FROM list_items WHERE list_id = ? AND done = 0 ORDER BY position ASC, created_at ASC`, itemCols), listID)
	if err != nil {
		return nil, err
	}

	if showCompleted != 0 {
		q, args := fmt.Sprintf(`SELECT %s FROM list_items WHERE list_id = ? AND done = 1 ORDER BY updated_at DESC, position ASC`, itemCols), []any{listID}
		if showCompleted > 0 {
			q += ` LIMIT ?`
			args = append(args, showCompleted)
		}
		completed, err := s.queryItems(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		items = append(items, completed...)
	}

	return &TasksSnapshot{List: *list, Items: items}, nil
}

func upsertListOn(ctx context.Context, ex sqlExecer, list List, now time.Time) error {
	if list.CreatedAt.IsZero() { list.CreatedAt = now }
	if list.UpdatedAt.IsZero() { list.UpdatedAt = now }
	if list.Source == "" { list.Source = "local" }
	if list.Sections == nil { list.Sections = []string{} }
	sectionsJSON, _ := json.Marshal(list.Sections) //nolint:errcheck,errchkjson

	const q = `INSERT INTO lists (id, name, source, sections, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name = excluded.name, source = excluded.source, sections = excluded.sections, updated_at = excluded.updated_at`
	if _, err := ex.ExecContext(ctx, q, list.ID, list.Name, list.Source, string(sectionsJSON),
		list.CreatedAt.Format(time.RFC3339Nano), list.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("failed to upsert list: %w", err)
	}
	return nil
}

// UpsertList inserts or updates a List definition.
func (s *SQLiteStore) UpsertList(ctx context.Context, list List) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("database is closed")
	}
	return upsertListOn(ctx, s.db, list, time.Now().UTC())
}

func upsertItemOn(ctx context.Context, ex sqlExecer, item ListItem, now time.Time) error {
	if item.CreatedAt.IsZero() { item.CreatedAt = now }
	if item.UpdatedAt.IsZero() { item.UpdatedAt = now }
	doneInt := 0
	if item.Done { doneInt = 1 }
	var secVal, assVal, dueVal any
	if item.Section != "" { secVal = item.Section }
	if item.Assignee != nil { assVal = *item.Assignee }
	if item.DueDate != nil { dueVal = *item.DueDate }

	const q = `INSERT INTO list_items (id, list_id, title, done, section, position, assignee, due_date, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(list_id, id) DO UPDATE SET title = excluded.title, done = excluded.done, section = excluded.section, position = excluded.position, assignee = excluded.assignee, due_date = excluded.due_date, updated_at = excluded.updated_at`
	if _, err := ex.ExecContext(ctx, q, item.ID, item.ListID, item.Title, doneInt, secVal, item.Position, assVal, dueVal,
		item.CreatedAt.Format(time.RFC3339Nano), item.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("failed to upsert list item: %w", err)
	}
	return nil
}

// UpsertItem inserts or updates a ListItem.
func (s *SQLiteStore) UpsertItem(ctx context.Context, item ListItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("database is closed")
	}
	return upsertItemOn(ctx, s.db, item, time.Now().UTC())
}

type syncItemKey struct {
	title, section, assignee, due string
	done                          bool
	pos                           int
}

// SyncList reconciles an external task list and its items into SQLite in a single transaction.
func (s *SQLiteStore) SyncList(ctx context.Context, list List, items []ListItem) (changed bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return false, errors.New("database is closed")
	}

	tx, txErr := s.db.BeginTx(ctx, nil)
	if txErr != nil {
		return false, fmt.Errorf("failed to begin sync transaction: %w", txErr)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback() //nolint:errcheck
		}
	}()

	var existingName, existingSource, existingSectionsJSON string
	row := tx.QueryRowContext(ctx, `SELECT name, source, sections FROM lists WHERE id = ?`, list.ID)
	if err := row.Scan(&existingName, &existingSource, &existingSectionsJSON); errors.Is(err, sql.ErrNoRows) {
		changed = true
	} else if err != nil {
		return false, fmt.Errorf("failed to query existing list: %w", err)
	} else {
		existingSections := unmarshalSections(existingSectionsJSON)
		incomingSections := list.Sections
		if incomingSections == nil {
			incomingSections = []string{}
		}
		if existingName != list.Name || existingSource != list.Source || !slices.Equal(existingSections, incomingSections) {
			changed = true
		}
	}

	rows, queryErr := tx.QueryContext(ctx, `SELECT id, title, done, section, position, assignee, due_date FROM list_items WHERE list_id = ?`, list.ID)
	if queryErr != nil {
		return false, fmt.Errorf("failed to query existing items: %w", queryErr)
	}
	existingItems := make(map[string]syncItemKey)
	for rows.Next() {
		var id, title string
		var doneInt, pos int
		var sec, ass, due sql.NullString
		if err := rows.Scan(&id, &title, &doneInt, &sec, &pos, &ass, &due); err != nil {
			_ = rows.Close() //nolint:errcheck
			return false, fmt.Errorf("failed to scan item state: %w", err)
		}
		existingItems[id] = syncItemKey{
			title: title, section: sec.String, assignee: ass.String, due: due.String,
			done: doneInt != 0, pos: pos,
		}
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		_ = rows.Close() //nolint:errcheck
		return false, fmt.Errorf("error reading existing items: %w", rowsErr)
	}
	_ = rows.Close() //nolint:errcheck

	incomingIDs := make(map[string]bool, len(items))
	for _, item := range items {
		incomingIDs[item.ID] = true
	}

	deref := func(s *string) string {
		if s == nil { return "" }
		return *s
	}
	if !changed {
		if len(incomingIDs) != len(existingItems) {
			changed = true
		} else {
			for _, item := range items {
				prev, exists := existingItems[item.ID]
				inKey := syncItemKey{
					title: item.Title, section: item.Section, assignee: deref(item.Assignee),
					due: deref(item.DueDate), done: item.Done, pos: item.Position,
				}
				if !exists || prev != inKey {
					changed = true
					break
				}
			}
		}
	}

	if !changed {
		if err := tx.Commit(); err != nil {
			return false, fmt.Errorf("failed to commit no-op transaction: %w", err)
		}
		committed = true
		return false, nil
	}

	now := time.Now().UTC()
	if err := upsertListOn(ctx, tx, list, now); err != nil {
		return false, err
	}
	for _, item := range items {
		item.ListID = list.ID
		if err := upsertItemOn(ctx, tx, item, now); err != nil {
			return false, err
		}
	}
	for existingID := range existingItems {
		if !incomingIDs[existingID] {
			if _, delErr := tx.ExecContext(ctx, `DELETE FROM list_items WHERE list_id = ? AND id = ?`, list.ID, existingID); delErr != nil {
				return false, fmt.Errorf("failed to delete removed item %s: %w", existingID, delErr)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("failed to commit sync transaction: %w", err)
	}
	committed = true
	return true, nil
}

func (s *SQLiteStore) deleteWhere(ctx context.Context, notFoundErr error, label, query string, args ...any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("database is closed")
	}

	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to delete %s: %w", label, err)
	}
	rows, err := res.RowsAffected()
	if err != nil || rows == 0 {
		return notFoundErr
	}
	return nil
}

// DeleteList removes a list and cascades item deletions. Returns ErrListNotFound if no rows affected.
func (s *SQLiteStore) DeleteList(ctx context.Context, id string) error {
	return s.deleteWhere(ctx, ErrListNotFound, "list", `DELETE FROM lists WHERE id = ?`, id)
}

// DeleteItem removes an individual item from listID. Returns ErrItemNotFound if no rows affected.
func (s *SQLiteStore) DeleteItem(ctx context.Context, listID, itemID string) error {
	return s.deleteWhere(ctx, ErrItemNotFound, "list item", `DELETE FROM list_items WHERE list_id = ? AND id = ?`, listID, itemID)
}

// CompleteItem marks an item as done in listID. Returns ErrItemNotFound if not found.
// If the item is already marked done, it returns nil idempotently.
func (s *SQLiteStore) CompleteItem(ctx context.Context, listID, itemID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("database is closed")
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx, `UPDATE list_items SET done = 1, updated_at = ? WHERE list_id = ? AND id = ? AND done = 0`, now, listID, itemID)
	if err != nil {
		return fmt.Errorf("failed to complete list item: %w", err)
	}
	rows, _ := res.RowsAffected() //nolint:errcheck
	if rows == 0 {
		var doneVal int
		if err := s.db.QueryRowContext(ctx, `SELECT done FROM list_items WHERE list_id = ? AND id = ?`, listID, itemID).Scan(&doneVal); errors.Is(err, sql.ErrNoRows) {
			return ErrItemNotFound
		}
	}
	return nil
}

func scanItem(rows *sql.Rows) (ListItem, error) {
	var item ListItem
	var section, assignee, dueDate sql.NullString
	var createdAtStr, updatedAtStr string
	var doneInt int

	if err := rows.Scan(
		&item.ID, &item.ListID, &item.Title, &doneInt, &section,
		&item.Position, &assignee, &dueDate, &createdAtStr, &updatedAtStr,
	); err != nil {
		return item, fmt.Errorf("failed to scan item row: %w", err)
	}

	item.Done = doneInt != 0
	if section.Valid { item.Section = section.String }
	if assignee.Valid { item.Assignee = &assignee.String }
	if dueDate.Valid { item.DueDate = &dueDate.String }
	item.CreatedAt = parseTime(createdAtStr)
	item.UpdatedAt = parseTime(updatedAtStr)
	return item, nil
}

var timeLayouts = [...]string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05-07:00",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05",
}

func parseTime(str string) time.Time {
	if str == "" {
		return time.Time{}
	}
	for _, l := range timeLayouts {
		if t, err := time.Parse(l, str); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
