package tasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Store defines the data access contract for task lists and items per SPEC-008.
type Store interface {
	GetList(ctx context.Context, id string) (*List, error)
	GetListItems(ctx context.Context, listID string, includeDone bool) ([]ListItem, error)
	GetTasksSnapshot(ctx context.Context, listID string, showCompleted int) (*TasksSnapshot, error)
	UpsertList(ctx context.Context, list List) error
	UpsertItem(ctx context.Context, item ListItem) error
	DeleteList(ctx context.Context, id string) error
	DeleteItem(ctx context.Context, listID, itemID string) error
	Close() error
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
		dbPath = "/data/lists.db"
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

	// Single-writer connection pool to eliminate write contention and SQLITE_BUSY errors per SPEC-008 §1
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	initSQL := `
	PRAGMA journal_mode = WAL;
	PRAGMA synchronous = NORMAL;
	PRAGMA busy_timeout = 5000;
	PRAGMA foreign_keys = ON;
	PRAGMA temp_store = MEMORY;
	`
	if _, err := db.Exec(initSQL); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			return nil, fmt.Errorf("failed to close database after pragma error: %w (pragma error: %w)", closeErr, err)
		}
		return nil, fmt.Errorf("failed to execute pragmas: %w", err)
	}

	store := &SQLiteStore{
		db: db,
	}

	if err := store.migrate(); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			return nil, fmt.Errorf("failed to close database after migration error: %w (migration error: %w)", closeErr, err)
		}
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	return store, nil
}

func (s *SQLiteStore) migrate() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ddl := `
	CREATE TABLE IF NOT EXISTS lists (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		source TEXT NOT NULL DEFAULT 'local',
		sections TEXT NOT NULL DEFAULT '[]',
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
		due_date TEXT,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_list_items_list_id_position ON list_items(list_id, position);
	CREATE INDEX IF NOT EXISTS idx_list_items_updated_at ON list_items(updated_at);
	CREATE INDEX IF NOT EXISTS idx_list_items_list_done_pos ON list_items(list_id, done, position, created_at);
	CREATE INDEX IF NOT EXISTS idx_list_items_list_done_updated ON list_items(list_id, done, updated_at DESC);
	`

	_, err := s.db.Exec(ddl)
	return err
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
	if s.db == nil {
		return nil, errors.New("database is closed")
	}

	query := `SELECT id, name, source, sections, created_at, updated_at FROM lists WHERE id = ?`
	row := s.db.QueryRowContext(ctx, query, id)

	var l List
	var sectionsJSON string
	var createdAtStr, updatedAtStr string

	err := row.Scan(&l.ID, &l.Name, &l.Source, &sectionsJSON, &createdAtStr, &updatedAtStr)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrListNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to scan list: %w", err)
	}

	l.CreatedAt = parseTime(createdAtStr)
	l.UpdatedAt = parseTime(updatedAtStr)

	if sectionsJSON != "" {
		if err := json.Unmarshal([]byte(sectionsJSON), &l.Sections); err != nil {
			l.Sections = []string{}
		}
	} else {
		l.Sections = []string{}
	}
	if l.Sections == nil {
		l.Sections = []string{}
	}

	return &l, nil
}

func (s *SQLiteStore) queryItems(ctx context.Context, query string, args ...any) (items []ListItem, err error) {
	rows, queryErr := s.db.QueryContext(ctx, query, args...)
	if queryErr != nil {
		return nil, fmt.Errorf("failed to query list items: %w", queryErr)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("failed to close rows: %w", closeErr)
		}
	}()

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
// If includeDone is false, items with done=1 are filtered out.
// Returns ErrListNotFound if the list does not exist.
func (s *SQLiteStore) GetListItems(ctx context.Context, listID string, includeDone bool) ([]ListItem, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, errors.New("database is closed")
	}

	// Verify parent list exists first per SPEC-006 §7
	var exists int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM lists WHERE id = ?`, listID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrListNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to verify list existence: %w", err)
	}

	if includeDone {
		query := `SELECT id, list_id, title, done, section, position, assignee, due_date, created_at, updated_at
			FROM list_items
			WHERE list_id = ?
			ORDER BY position ASC, created_at ASC`
		return s.queryItems(ctx, query, listID)
	}

	query := `SELECT id, list_id, title, done, section, position, assignee, due_date, created_at, updated_at
		FROM list_items
		WHERE list_id = ? AND done = 0
		ORDER BY position ASC, created_at ASC`
	return s.queryItems(ctx, query, listID)
}

// GetTasksSnapshot aggregates list metadata and items for widget presentation per SPEC-008 §Wire Protocol.
// showCompleted specifies the maximum number of recently completed items to include:
//   - showCompleted > 0: includes up to showCompleted completed items ordered by updated_at DESC
//   - showCompleted == 0: excludes completed items entirely
//   - showCompleted < 0: includes all completed items
func (s *SQLiteStore) GetTasksSnapshot(ctx context.Context, listID string, showCompleted int) (*TasksSnapshot, error) {
	list, err := s.GetList(ctx, listID)
	if err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	// 1. Unchecked items ordered by position ASC, created_at ASC
	uncheckedQuery := `SELECT id, list_id, title, done, section, position, assignee, due_date, created_at, updated_at
		FROM list_items
		WHERE list_id = ? AND done = 0
		ORDER BY position ASC, created_at ASC`
	items, err := s.queryItems(ctx, uncheckedQuery, listID)
	if err != nil {
		return nil, err
	}

	// 2. Completed items (if permitted by showCompleted)
	if showCompleted != 0 {
		var completedQuery string
		var args []any
		if showCompleted > 0 {
			completedQuery = `SELECT id, list_id, title, done, section, position, assignee, due_date, created_at, updated_at
				FROM list_items
				WHERE list_id = ? AND done = 1
				ORDER BY updated_at DESC, position ASC
				LIMIT ?`
			args = []any{listID, showCompleted}
		} else {
			completedQuery = `SELECT id, list_id, title, done, section, position, assignee, due_date, created_at, updated_at
				FROM list_items
				WHERE list_id = ? AND done = 1
				ORDER BY updated_at DESC, position ASC`
			args = []any{listID}
		}
		completed, err := s.queryItems(ctx, completedQuery, args...)
		if err != nil {
			return nil, err
		}
		items = append(items, completed...)
	}

	return &TasksSnapshot{
		List:  *list,
		Items: items,
	}, nil
}

// UpsertList inserts or updates a List definition.
func (s *SQLiteStore) UpsertList(ctx context.Context, list List) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("database is closed")
	}

	now := time.Now().UTC()
	if list.CreatedAt.IsZero() {
		list.CreatedAt = now
	}
	if list.UpdatedAt.IsZero() {
		list.UpdatedAt = now
	}
	if list.Source == "" {
		list.Source = "local"
	}
	if list.Sections == nil {
		list.Sections = []string{}
	}

	sectionsJSON, err := json.Marshal(list.Sections)
	if err != nil {
		return fmt.Errorf("failed to marshal sections: %w", err)
	}

	query := `INSERT INTO lists (id, name, source, sections, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			source = excluded.source,
			sections = excluded.sections,
			updated_at = excluded.updated_at`

	_, err = s.db.ExecContext(ctx, query,
		list.ID,
		list.Name,
		list.Source,
		string(sectionsJSON),
		list.CreatedAt.Format(time.RFC3339Nano),
		list.UpdatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("failed to upsert list: %w", err)
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

	now := time.Now().UTC()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = now
	}

	query := `INSERT INTO list_items (id, list_id, title, done, section, position, assignee, due_date, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			title = excluded.title,
			done = excluded.done,
			section = excluded.section,
			position = excluded.position,
			assignee = excluded.assignee,
			due_date = excluded.due_date,
			updated_at = excluded.updated_at`

	doneInt := 0
	if item.Done {
		doneInt = 1
	}

	var sectionVal, assigneeVal, dueDateVal any
	if item.Section != "" {
		sectionVal = item.Section
	}
	if item.Assignee != nil {
		assigneeVal = *item.Assignee
	}
	if item.DueDate != nil {
		dueDateVal = *item.DueDate
	}

	_, err := s.db.ExecContext(ctx, query,
		item.ID,
		item.ListID,
		item.Title,
		doneInt,
		sectionVal,
		item.Position,
		assigneeVal,
		dueDateVal,
		item.CreatedAt.Format(time.RFC3339Nano),
		item.UpdatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("failed to upsert list item: %w", err)
	}
	return nil
}

// DeleteList removes a list and cascades item deletions. Returns ErrListNotFound if no rows affected.
func (s *SQLiteStore) DeleteList(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("database is closed")
	}

	res, err := s.db.ExecContext(ctx, `DELETE FROM lists WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("failed to delete list: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil || rows == 0 {
		return ErrListNotFound
	}
	return nil
}

// DeleteItem removes an individual item from listID. Returns ErrItemNotFound if no rows affected.
func (s *SQLiteStore) DeleteItem(ctx context.Context, listID, itemID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("database is closed")
	}

	res, err := s.db.ExecContext(ctx, `DELETE FROM list_items WHERE list_id = ? AND id = ?`, listID, itemID)
	if err != nil {
		return fmt.Errorf("failed to delete list item: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil || rows == 0 {
		return ErrItemNotFound
	}
	return nil
}

func scanItem(rows *sql.Rows) (ListItem, error) {
	var item ListItem
	var section, assignee, dueDate sql.NullString
	var createdAtStr, updatedAtStr string
	var doneInt int

	if err := rows.Scan(
		&item.ID,
		&item.ListID,
		&item.Title,
		&doneInt,
		&section,
		&item.Position,
		&assignee,
		&dueDate,
		&createdAtStr,
		&updatedAtStr,
	); err != nil {
		return item, fmt.Errorf("failed to scan item row: %w", err)
	}

	item.Done = doneInt != 0
	if section.Valid {
		item.Section = section.String
	}
	if assignee.Valid {
		item.Assignee = &assignee.String
	}
	if dueDate.Valid {
		item.DueDate = &dueDate.String
	}
	item.CreatedAt = parseTime(createdAtStr)
	item.UpdatedAt = parseTime(updatedAtStr)

	return item, nil
}

func parseTime(str string) time.Time {
	if str == "" {
		return time.Time{}
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, str); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
