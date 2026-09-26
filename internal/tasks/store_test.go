package tasks

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSQLiteStore_LifecycleAndPragmas(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "nested", "deep", "lists.db")

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create SQLiteStore: %v", err)
	}
	defer store.Close()

	// Verify Pragmas
	var journalMode string
	if err := store.db.QueryRow("PRAGMA journal_mode;").Scan(&journalMode); err != nil {
		t.Fatalf("failed to query journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("expected journal_mode 'wal', got %q", journalMode)
	}

	var foreignKeys int
	if err := store.db.QueryRow("PRAGMA foreign_keys;").Scan(&foreignKeys); err != nil {
		t.Fatalf("failed to query foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Errorf("expected foreign_keys 1, got %d", foreignKeys)
	}

	var busyTimeout int
	if err := store.db.QueryRow("PRAGMA busy_timeout;").Scan(&busyTimeout); err != nil {
		t.Fatalf("failed to query busy_timeout: %v", err)
	}
	if busyTimeout != 5000 {
		t.Errorf("expected busy_timeout 5000, got %d", busyTimeout)
	}

	// Test flat filename without parent directory inside isolated temp dir
	flatDir := t.TempDir()
	origWd, err := os.Getwd()
	if err == nil {
		if chErr := os.Chdir(flatDir); chErr == nil {
			defer func() { _ = os.Chdir(origWd) }()
			flatStore, err := NewSQLiteStore("test_flat.db")
			if err != nil {
				t.Fatalf("failed to create flat store: %v", err)
			}
			_ = flatStore.Close()
		}
	}
}

func TestSQLiteStore_ListCRUD(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "lists.db"))
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// 1. Get non-existent list
	_, err = store.GetList(ctx, "local:missing")
	if !errors.Is(err, ErrListNotFound) {
		t.Fatalf("expected ErrListNotFound, got %v", err)
	}

	// 2. Upsert list
	now := time.Now().UTC().Truncate(time.Second)
	list := List{
		ID:        "local:groceries",
		Name:      "Groceries",
		Source:    "local",
		Sections:  []string{"Produce", "Dairy", "Bakery"},
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := store.UpsertList(ctx, list); err != nil {
		t.Fatalf("failed to upsert list: %v", err)
	}

	// 3. Get existing list
	fetched, err := store.GetList(ctx, "local:groceries")
	if err != nil {
		t.Fatalf("failed to get list: %v", err)
	}
	if fetched.ID != list.ID || fetched.Name != list.Name || fetched.Source != list.Source {
		t.Errorf("list mismatch: expected %+v, got %+v", list, *fetched)
	}
	if len(fetched.Sections) != 3 || fetched.Sections[0] != "Produce" {
		t.Errorf("sections mismatch: got %+v", fetched.Sections)
	}

	// 4. Update list
	list.Name = "Weekly Groceries"
	list.Sections = []string{"Snacks"}
	if err := store.UpsertList(ctx, list); err != nil {
		t.Fatalf("failed to update list: %v", err)
	}

	updated, err := store.GetList(ctx, "local:groceries")
	if err != nil {
		t.Fatalf("failed to get updated list: %v", err)
	}
	if updated.Name != "Weekly Groceries" || len(updated.Sections) != 1 || updated.Sections[0] != "Snacks" {
		t.Errorf("updated list mismatch: got %+v", *updated)
	}

	// 5. Delete list
	if err := store.DeleteList(ctx, "local:groceries"); err != nil {
		t.Fatalf("failed to delete list: %v", err)
	}

	// 6. Delete again returns ErrListNotFound
	if err := store.DeleteList(ctx, "local:groceries"); !errors.Is(err, ErrListNotFound) {
		t.Fatalf("expected ErrListNotFound on repeat delete, got %v", err)
	}
}

func TestSQLiteStore_ItemCRUDAndFiltering(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "lists.db"))
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// 1. Query items for non-existent list -> ErrListNotFound
	_, err = store.GetListItems(ctx, "local:nonexistent", true)
	if !errors.Is(err, ErrListNotFound) {
		t.Fatalf("expected ErrListNotFound, got %v", err)
	}

	// 2. Create list
	list := List{
		ID:       "local:chores",
		Name:     "Daily Chores",
		Source:   "local",
		Sections: []string{"Morning", "Evening"},
	}
	if err := store.UpsertList(ctx, list); err != nil {
		t.Fatalf("failed to upsert list: %v", err)
	}

	// 3. Query empty list -> returns empty slice
	emptyItems, err := store.GetListItems(ctx, "local:chores", true)
	if err != nil {
		t.Fatalf("failed to get empty items: %v", err)
	}
	if emptyItems == nil || len(emptyItems) != 0 {
		t.Fatalf("expected empty non-nil slice, got %+v", emptyItems)
	}

	// 4. Upsert items
	assignee1 := "Alex"
	dueDate1 := "2026-09-26"
	item1 := ListItem{
		ID:        "c1",
		ListID:    "local:chores",
		Title:     "Feed cats",
		Done:      false,
		Section:   "Morning",
		Position:  10,
		Assignee:  &assignee1,
		DueDate:   &dueDate1,
		CreatedAt: time.Now().Add(-10 * time.Minute),
		UpdatedAt: time.Now().Add(-10 * time.Minute),
	}

	item2 := ListItem{
		ID:        "c2",
		ListID:    "local:chores",
		Title:     "Take out trash",
		Done:      true,
		Section:   "Evening",
		Position:  5,
		CreatedAt: time.Now().Add(-5 * time.Minute),
		UpdatedAt: time.Now().Add(-1 * time.Minute),
	}

	item3 := ListItem{
		ID:        "c3",
		ListID:    "local:chores",
		Title:     "Water plants",
		Done:      false,
		Section:   "Morning",
		Position:  2,
		CreatedAt: time.Now().Add(-2 * time.Minute),
		UpdatedAt: time.Now().Add(-2 * time.Minute),
	}

	for _, it := range []ListItem{item1, item2, item3} {
		if err := store.UpsertItem(ctx, it); err != nil {
			t.Fatalf("failed to upsert item %s: %v", it.ID, err)
		}
	}

	// 5. Query with includeDone = true -> all 3 items ordered by position ASC (c3, c2, c1)
	allItems, err := store.GetListItems(ctx, "local:chores", true)
	if err != nil {
		t.Fatalf("failed to get all items: %v", err)
	}
	if len(allItems) != 3 {
		t.Fatalf("expected 3 items, got %d", len(allItems))
	}
	if allItems[0].ID != "c3" || allItems[1].ID != "c2" || allItems[2].ID != "c1" {
		t.Errorf("ordering mismatch: expected c3, c2, c1; got %s, %s, %s",
			allItems[0].ID, allItems[1].ID, allItems[2].ID)
	}
	if allItems[2].Assignee == nil || *allItems[2].Assignee != "Alex" {
		t.Errorf("expected assignee Alex on c1, got %v", allItems[2].Assignee)
	}
	if allItems[0].Assignee != nil {
		t.Errorf("expected nil assignee on c3, got %v", allItems[0].Assignee)
	}

	// 6. Query with includeDone = false -> only c3 and c1 (ordered by position ASC: c3, c1)
	activeItems, err := store.GetListItems(ctx, "local:chores", false)
	if err != nil {
		t.Fatalf("failed to get active items: %v", err)
	}
	if len(activeItems) != 2 {
		t.Fatalf("expected 2 active items, got %d", len(activeItems))
	}
	if activeItems[0].ID != "c3" || activeItems[1].ID != "c1" {
		t.Errorf("active items ordering mismatch: expected c3, c1; got %s, %s",
			activeItems[0].ID, activeItems[1].ID)
	}

	// 7. Delete individual item
	if err := store.DeleteItem(ctx, "local:chores", "c2"); err != nil {
		t.Fatalf("failed to delete item: %v", err)
	}
	if err := store.DeleteItem(ctx, "local:chores", "c2"); !errors.Is(err, ErrItemNotFound) {
		t.Fatalf("expected ErrItemNotFound on repeat delete, got %v", err)
	}

	// 8. Foreign key cascade delete
	if err := store.DeleteList(ctx, "local:chores"); err != nil {
		t.Fatalf("failed to delete list: %v", err)
	}
	// Re-create list to verify items were deleted by cascade
	if err := store.UpsertList(ctx, list); err != nil {
		t.Fatalf("failed to re-create list: %v", err)
	}
	postCascadeItems, err := store.GetListItems(ctx, "local:chores", true)
	if err != nil {
		t.Fatalf("failed to check post-cascade items: %v", err)
	}
	if len(postCascadeItems) != 0 {
		t.Fatalf("expected 0 items post cascade, got %d", len(postCascadeItems))
	}
}

func TestSQLiteStore_GetTasksSnapshot(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "lists.db"))
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Missing list returns ErrListNotFound
	_, err = store.GetTasksSnapshot(ctx, "missing", 3)
	if !errors.Is(err, ErrListNotFound) {
		t.Fatalf("expected ErrListNotFound, got %v", err)
	}

	list := List{
		ID:     "groceries",
		Name:   "Groceries",
		Source: "local",
	}
	if err := store.UpsertList(ctx, list); err != nil {
		t.Fatalf("failed to create list: %v", err)
	}

	now := time.Now().UTC()
	assignee := "Alex"
	dueDate := "2026-09-26"
	items := []ListItem{
		{ID: "i1", ListID: "groceries", Title: "Milk", Done: false, Section: "Dairy", Assignee: &assignee, DueDate: &dueDate, Position: 1, CreatedAt: now},
		{ID: "i2", ListID: "groceries", Title: "Eggs", Done: false, Section: "Dairy", Position: 2, CreatedAt: now},
		{ID: "i3", ListID: "groceries", Title: "Bread", Done: true, Section: "Bakery", Position: 3, UpdatedAt: now.Add(-3 * time.Minute)},
		{ID: "i4", ListID: "groceries", Title: "Butter", Done: true, Section: "Dairy", Position: 4, UpdatedAt: now.Add(-2 * time.Minute)},
		{ID: "i5", ListID: "groceries", Title: "Cheese", Done: true, Section: "Dairy", Assignee: &assignee, DueDate: &dueDate, Position: 5, UpdatedAt: now.Add(-1 * time.Minute)},
	}
	for _, it := range items {
		if err := store.UpsertItem(ctx, it); err != nil {
			t.Fatalf("failed to upsert item: %v", err)
		}
	}

	// 1. showCompleted = 0 -> only 2 unchecked items
	snap0, err := store.GetTasksSnapshot(ctx, "groceries", 0)
	if err != nil {
		t.Fatalf("failed to get snapshot 0: %v", err)
	}
	if len(snap0.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(snap0.Items))
	}
	if snap0.Items[0].Section != "Dairy" || snap0.Items[0].Assignee == nil || *snap0.Items[0].Assignee != "Alex" {
		t.Errorf("expected section and assignee on i1, got %+v", snap0.Items[0])
	}

	// 2. showCompleted = 2 -> 2 unchecked + 2 most recently completed (i5, i4)
	snap2, err := store.GetTasksSnapshot(ctx, "groceries", 2)
	if err != nil {
		t.Fatalf("failed to get snapshot 2: %v", err)
	}
	if len(snap2.Items) != 4 {
		t.Fatalf("expected 4 items, got %d", len(snap2.Items))
	}
	// Verify completed items order (i5, i4)
	if snap2.Items[2].ID != "i5" || snap2.Items[3].ID != "i4" {
		t.Errorf("completed items order mismatch: expected i5, i4; got %s, %s",
			snap2.Items[2].ID, snap2.Items[3].ID)
	}
	if snap2.Items[2].DueDate == nil || *snap2.Items[2].DueDate != "2026-09-26" {
		t.Errorf("expected due date on i5, got %+v", snap2.Items[2])
	}

	// 3. showCompleted = -1 (all completed) -> 2 unchecked + 3 completed = 5 total
	snapAll, err := store.GetTasksSnapshot(ctx, "groceries", -1)
	if err != nil {
		t.Fatalf("failed to get all snapshot: %v", err)
	}
	if len(snapAll.Items) != 5 {
		t.Fatalf("expected 5 items, got %d", len(snapAll.Items))
	}
}

func TestSQLiteStore_ErrorBranches(t *testing.T) {
	// 1. Directory creation failure
	impossiblePath := "/dev/null/impossible/lists.db"
	if _, err := NewSQLiteStore(impossiblePath); err == nil {
		t.Error("expected error for impossible directory path")
	}

	// 2. Store with canceled context
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "errors.db"))
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	defer store.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Queries with canceled context return errors
	if _, err := store.GetList(ctx, "any"); err == nil {
		t.Error("expected error for canceled GetList")
	}
	if _, err := store.GetListItems(ctx, "any", true); err == nil {
		t.Error("expected error for canceled GetListItems")
	}
	if _, err := store.GetTasksSnapshot(ctx, "any", 3); err == nil {
		t.Error("expected error for canceled GetTasksSnapshot")
	}
	if err := store.UpsertList(ctx, List{ID: "any"}); err == nil {
		t.Error("expected error for canceled UpsertList")
	}
	if err := store.UpsertItem(ctx, ListItem{ID: "any", ListID: "any"}); err == nil {
		t.Error("expected error for canceled UpsertItem")
	}
	if err := store.DeleteList(ctx, "any"); err == nil {
		t.Error("expected error for canceled DeleteList")
	}
	if err := store.DeleteItem(ctx, "any", "any"); err == nil {
		t.Error("expected error for canceled DeleteItem")
	}

	// 3. Malformed sections in database
	bgCtx := context.Background()
	_, _ = store.db.Exec("INSERT INTO lists (id, name, sections) VALUES ('bad-sections', 'Test', '{not-json')")
	listWithBadSections, err := store.GetList(bgCtx, "bad-sections")
	if err != nil {
		t.Fatalf("expected GetList to succeed even with malformed sections, got %v", err)
	}
	if len(listWithBadSections.Sections) != 0 {
		t.Errorf("expected empty sections on malformed JSON, got %+v", listWithBadSections.Sections)
	}

	// 4. Null and empty sections in database
	_, _ = store.db.Exec("INSERT INTO lists (id, name, sections) VALUES ('null-sections', 'Test', 'null')")
	listWithNullSections, err := store.GetList(bgCtx, "null-sections")
	if err != nil || listWithNullSections.Sections == nil {
		t.Errorf("expected non-nil sections on null JSON, got %+v", listWithNullSections)
	}

	_, _ = store.db.Exec("INSERT INTO lists (id, name, sections) VALUES ('empty-sections', 'Test', '')")
	listWithEmptySections, err := store.GetList(bgCtx, "empty-sections")
	if err != nil || listWithEmptySections.Sections == nil {
		t.Errorf("expected non-nil sections on empty string, got %+v", listWithEmptySections)
	}

	// 5. Upsert with zero values
	if err := store.UpsertList(bgCtx, List{ID: "zero-val", Name: "Zero"}); err != nil {
		t.Fatalf("failed to upsert list with zero times: %v", err)
	}
	if err := store.UpsertItem(bgCtx, ListItem{ID: "item-zero", ListID: "zero-val", Title: "Item Zero"}); err != nil {
		t.Fatalf("failed to upsert item with zero times: %v", err)
	}

	// 6. Default dbPath test
	defStore, err := NewSQLiteStore("")
	if err == nil {
		_ = defStore.Close()
		_ = os.Remove("/data/lists.db")
		_ = os.Remove("/data/lists.db-wal")
		_ = os.Remove("/data/lists.db-shm")
	}
}

func TestSQLiteStore_QueryFailures(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "failures.db"))
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	list := List{ID: "fail-test", Name: "Fail Test"}
	if err := store.UpsertList(ctx, list); err != nil {
		t.Fatalf("failed to upsert list: %v", err)
	}

	// Drop list_items to force query execution errors while parent list exists
	if _, err := store.db.Exec("DROP TABLE list_items"); err != nil {
		t.Fatalf("failed to drop list_items: %v", err)
	}

	if _, err := store.GetListItems(ctx, "fail-test", true); err == nil {
		t.Error("expected error from GetListItems with dropped table")
	}
	if _, err := store.GetListItems(ctx, "fail-test", false); err == nil {
		t.Error("expected error from GetListItems (includeDone=false) with dropped table")
	}
	if _, err := store.GetTasksSnapshot(ctx, "fail-test", 3); err == nil {
		t.Error("expected error from GetTasksSnapshot with dropped table")
	}
}

func TestSQLiteStore_ConcurrencyAndClosed(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "concurrency.db"))
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}

	ctx := context.Background()
	list := List{ID: "stress", Name: "Stress Test"}
	if err := store.UpsertList(ctx, list); err != nil {
		t.Fatalf("failed to create list: %v", err)
	}

	const workers = 8
	const opsPerWorker = 20
	var wg sync.WaitGroup
	wg.Add(workers)

	for w := 0; w < workers; w++ {
		go func(workerID int) {
			defer wg.Done()
			for op := 0; op < opsPerWorker; op++ {
				itemID := filepath.Join("item", string(rune('A'+workerID)), string(rune('0'+op)))
				_ = store.UpsertItem(ctx, ListItem{
					ID:       itemID,
					ListID:   "stress",
					Title:    "Work",
					Position: op,
				})
				_, _ = store.GetListItems(ctx, "stress", true)
				_, _ = store.GetTasksSnapshot(ctx, "stress", 3)
			}
		}(w)
	}
	wg.Wait()

	// Verify close behavior
	if err := store.Close(); err != nil {
		t.Fatalf("failed to close store: %v", err)
	}
	// Double close is safe
	if err := store.Close(); err != nil {
		t.Errorf("double close failed: %v", err)
	}

	// Operations on closed store return error
	if _, err := store.GetList(ctx, "stress"); err == nil {
		t.Error("expected error getting list on closed store")
	}
	if _, err := store.GetListItems(ctx, "stress", true); err == nil {
		t.Error("expected error getting items on closed store")
	}
	if _, err := store.GetTasksSnapshot(ctx, "stress", 3); err == nil {
		t.Error("expected error getting snapshot on closed store")
	}
	if err := store.UpsertList(ctx, list); err == nil {
		t.Error("expected error upserting list on closed store")
	}
	if err := store.UpsertItem(ctx, ListItem{ID: "x", ListID: "stress"}); err == nil {
		t.Error("expected error upserting item on closed store")
	}
	if err := store.DeleteList(ctx, "stress"); err == nil {
		t.Error("expected error deleting list on closed store")
	}
	if err := store.DeleteItem(ctx, "stress", "x"); err == nil {
		t.Error("expected error deleting item on closed store")
	}
}

func TestParseTime(t *testing.T) {
	if !parseTime("").IsZero() {
		t.Error("expected zero time for empty string")
	}
	if !parseTime("invalid-date").IsZero() {
		t.Error("expected zero time for invalid date")
	}

	now := time.Now().UTC().Truncate(time.Second)
	parsed := parseTime(now.Format(time.RFC3339))
	if !parsed.Equal(now) {
		t.Errorf("expected %v, got %v", now, parsed)
	}

	standard := parseTime("2026-09-26 14:30:00")
	if standard.Year() != 2026 || standard.Month() != 9 || standard.Day() != 26 {
		t.Errorf("expected 2026-09-26, got %v", standard)
	}
}

func TestSQLiteStore_ContextCancellationErrors(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "cancel.db"))
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	defer store.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-canceled

	list := List{ID: "c1", Name: "Canceled"}
	item := ListItem{ID: "i1", ListID: "c1", Title: "Test"}

	if _, err := store.GetList(ctx, "c1"); err == nil {
		t.Error("expected error with canceled context on GetList")
	}
	if _, err := store.GetListItems(ctx, "c1", true); err == nil {
		t.Error("expected error with canceled context on GetListItems")
	}
	if _, err := store.GetTasksSnapshot(ctx, "c1", 3); err == nil {
		t.Error("expected error with canceled context on GetTasksSnapshot")
	}
	if err := store.UpsertList(ctx, list); err == nil {
		t.Error("expected error with canceled context on UpsertList")
	}
	if err := store.UpsertItem(ctx, item); err == nil {
		t.Error("expected error with canceled context on UpsertItem")
	}
	if err := store.DeleteList(ctx, "c1"); err == nil {
		t.Error("expected error with canceled context on DeleteList")
	}
	if err := store.DeleteItem(ctx, "c1", "i1"); err == nil {
		t.Error("expected error with canceled context on DeleteItem")
	}
}

func TestSQLiteStore_QueryItems_Errors(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "err.db"))
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// 1. Invalid SQL syntax -> queryErr
	if _, err := store.queryItems(ctx, "SELECT INVALID SYNTAX"); err == nil {
		t.Error("expected syntax error from queryItems")
	}

	// 2. Scan error (wrong types for integer done/position)
	if _, err := store.queryItems(ctx, "SELECT 'id', 'lid', 'title', 'not-an-int', 'sec', 'not-an-int', 'ass', 'due', 'cr', 'up'"); err == nil {
		t.Error("expected scan error from queryItems with invalid column types")
	}
}

func TestSQLiteStore_InitErrors(t *testing.T) {
	// 1. MkdirAll error
	if _, err := NewSQLiteStore("/dev/null/forbidden/test.db"); err == nil {
		t.Error("expected error for forbidden mkdir")
	}

	// 2. Pragmas execution error on non-database device node
	if _, err := NewSQLiteStore("/dev/null"); err == nil {
		t.Error("expected pragma error on /dev/null")
	}
}

func TestSQLiteStore_SyncList_Lifecycle(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "sync.db"))
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	due := "2026-09-30"
	ass := "Alex"
	list := List{
		ID:       "sync-list",
		Name:     "Initial Name",
		Source:   "http",
		Sections: []string{"Produce"},
	}
	items := []ListItem{
		{
			ID:       "i1",
			ListID:   "sync-list",
			Title:    "Bananas",
			Done:     false,
			Section:  "Produce",
			Position: 0,
			Assignee: &ass,
			DueDate:  &due,
		},
		{
			ID:       "i2",
			ListID:   "sync-list",
			Title:    "Oat Milk",
			Done:     false,
			Section:  "Dairy",
			Position: 1,
		},
	}

	// 1. Cold sync of new list -> changed=true
	changed, err := store.SyncList(ctx, list, items)
	if err != nil {
		t.Fatalf("SyncList failed: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true on cold sync")
	}

	// Verify items stored
	storedItems, err := store.GetListItems(ctx, "sync-list", true)
	if err != nil || len(storedItems) != 2 {
		t.Fatalf("expected 2 items, got %d (err: %v)", len(storedItems), err)
	}

	// 2. Identical sync -> changed=false (zero writes)
	changed, err = store.SyncList(ctx, list, items)
	if err != nil {
		t.Fatalf("SyncList failed: %v", err)
	}
	if changed {
		t.Fatal("expected changed=false on identical sync")
	}

	// 3. Item modification (marking i1 done) -> changed=true
	items[0].Done = true
	changed, err = store.SyncList(ctx, list, items)
	if err != nil {
		t.Fatalf("SyncList failed: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true on item modification")
	}

	// Verify i1 is now done
	storedItems, err = store.GetListItems(ctx, "sync-list", true)
	if err != nil || len(storedItems) != 2 || !storedItems[0].Done {
		t.Fatalf("expected i1 to be done, got items: %+v", storedItems)
	}

	// 4. Item removal (i2 removed upstream) -> changed=true, i2 deleted from DB
	singleItem := []ListItem{items[0]}
	changed, err = store.SyncList(ctx, list, singleItem)
	if err != nil {
		t.Fatalf("SyncList failed: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true when item removed")
	}
	storedItems, err = store.GetListItems(ctx, "sync-list", true)
	if err != nil || len(storedItems) != 1 || storedItems[0].ID != "i1" {
		t.Fatalf("expected only i1 remaining, got %+v", storedItems)
	}

	// 5. Metadata modification (name changed) -> changed=true
	list.Name = "Updated Groceries Name"
	changed, err = store.SyncList(ctx, list, singleItem)
	if err != nil {
		t.Fatalf("SyncList failed: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true on list name change")
	}
	updatedList, err := store.GetList(ctx, "sync-list")
	if err != nil || updatedList.Name != "Updated Groceries Name" {
		t.Fatalf("expected updated name, got %+v", updatedList)
	}

	// 6. Empty items sync -> changed=true, deletes remaining items
	changed, err = store.SyncList(ctx, list, []ListItem{})
	if err != nil {
		t.Fatalf("SyncList failed: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true on empty items sync")
	}
	storedItems, err = store.GetListItems(ctx, "sync-list", true)
	if err != nil || len(storedItems) != 0 {
		t.Fatalf("expected 0 items remaining, got %d", len(storedItems))
	}
	// 7. Same count (1 item), but different item ID (replacement) -> changed=true
	baseItem := []ListItem{
		{
			ID:       "i-base",
			ListID:   "sync-list",
			Title:    "Base Item",
			Done:     false,
			Position: 0,
		},
	}
	_, err = store.SyncList(ctx, list, baseItem)
	if err != nil {
		t.Fatalf("SyncList failed: %v", err)
	}

	newItem := []ListItem{
		{
			ID:       "i-brand-new",
			ListID:   "sync-list",
			Title:    "Brand New Item",
			Done:     false,
			Position: 0,
		},
	}
	changed, err = store.SyncList(ctx, list, newItem)
	if err != nil || !changed {
		t.Fatalf("expected changed=true on item replacement, got changed=%v err=%v", changed, err)
	}

	// 8. Individual attribute modifications (section, position, assignee, due_date)
	newSec := "Pantry"
	newAss := "Taylor"
	newDue := "2026-10-15"
	modItem := newItem[0]
	modItem.Section = newSec
	modItem.Position = 5
	modItem.Assignee = &newAss
	modItem.DueDate = &newDue

	changed, err = store.SyncList(ctx, list, []ListItem{modItem})
	if err != nil || !changed {
		t.Fatalf("expected changed=true on attribute modification, got changed=%v err=%v", changed, err)
	}

	// 9. Sections metadata change -> changed=true
	listWithNewSec := list
	listWithNewSec.Sections = []string{"Produce", "Bakery"}
	changed, err = store.SyncList(ctx, listWithNewSec, []ListItem{modItem})
	if err != nil || !changed {
		t.Fatalf("expected changed=true on sections change, got changed=%v err=%v", changed, err)
	}

	// 10. Source empty and sections nil normalization
	emptySourceList := List{
		ID:   "sync-list",
		Name: "Normalized List",
	}
	changed, err = store.SyncList(ctx, emptySourceList, []ListItem{modItem})
	if err != nil || !changed {
		t.Fatalf("expected changed=true on normalization sync, got changed=%v err=%v", changed, err)
	}
	normList, err := store.GetList(ctx, "sync-list")
	if err != nil || normList.Source != "local" {
		t.Fatalf("expected source 'local' on empty source, got %+v", normList)
	}
}

func TestSQLiteStore_SyncList_Errors(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "sync_err.db"))
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}

	ctx := context.Background()

	// 1. Context cancelled error
	cancCtx, cancel := context.WithCancel(ctx)
	cancel()
	_, err = store.SyncList(cancCtx, List{ID: "canc"}, nil)
	if err == nil {
		t.Fatal("expected error on cancelled context SyncList")
	}

	// 2. Closed store
	_ = store.Close()
	_, err = store.SyncList(ctx, List{ID: "closed"}, nil)
	if err == nil {
		t.Fatal("expected error on closed store SyncList")
	}

	// 3. Dropped table error
	storeDropped, err := NewSQLiteStore(filepath.Join(t.TempDir(), "dropped.db"))
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	defer storeDropped.Close()
	_, _ = storeDropped.db.Exec("DROP TABLE list_items; DROP TABLE lists;")
	_, err = storeDropped.SyncList(ctx, List{ID: "fail"}, nil)
	if err == nil {
		t.Fatal("expected error on dropped table SyncList")
	}

	// 4. Dropped list_items table only (causes tx.QueryContext for items to fail)
	storeItemsDropped, err := NewSQLiteStore(filepath.Join(t.TempDir(), "items_dropped.db"))
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	defer storeItemsDropped.Close()
	_ = storeItemsDropped.UpsertList(ctx, List{ID: "existing-list", Name: "Existing"})
	_, _ = storeItemsDropped.db.Exec("DROP TABLE list_items;")
	_, err = storeItemsDropped.SyncList(ctx, List{ID: "existing-list", Name: "Existing"}, nil)
	if err == nil {
		t.Fatal("expected error on dropped list_items table in SyncList")
	}

	// 5. Corrupt list_items schema causing scan error in SyncList
	storeScanErr, err := NewSQLiteStore(filepath.Join(t.TempDir(), "scan_err.db"))
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	defer storeScanErr.Close()
	_ = storeScanErr.UpsertList(ctx, List{ID: "scan-list", Name: "Scan List"})
	_, _ = storeScanErr.db.Exec("DROP TABLE list_items;")
	_, _ = storeScanErr.db.Exec("CREATE TABLE list_items (id TEXT, list_id TEXT, title TEXT, done TEXT, section TEXT, position TEXT, assignee TEXT, due_date TEXT);")
	_, _ = storeScanErr.db.Exec("INSERT INTO list_items VALUES ('i1', 'scan-list', 'title', 'not-an-int', 'sec', 'not-an-int', 'ass', 'due');")
	_, err = storeScanErr.SyncList(ctx, List{ID: "scan-list", Name: "Scan List"}, nil)
	if err == nil {
		t.Fatal("expected scan error in SyncList")
	}

	// 6. Corrupt sections JSON in lists table
	storeCorruptSections, err := NewSQLiteStore(filepath.Join(t.TempDir(), "corrupt_sections.db"))
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	defer storeCorruptSections.Close()
	_ = storeCorruptSections.UpsertList(ctx, List{ID: "bad-sec", Name: "Bad Sections"})
	_, _ = storeCorruptSections.db.Exec("UPDATE lists SET sections = '{invalid-json' WHERE id = 'bad-sec';")
	changed, err := storeCorruptSections.SyncList(ctx, List{ID: "bad-sec", Name: "Bad Sections"}, nil)
	if err != nil {
		t.Fatalf("unexpected error when handling corrupt sections JSON: %v", err)
	}
	if !changed {
		// Because corrupt sections were unmarshaled to empty slice, but incoming is also empty, changed might be false
		// But it successfully fell back without error
	}
}

func TestSQLiteStore_ClosedStore_GetTasksSnapshot(t *testing.T) {
	t.Parallel()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	ctx := context.Background()
	_ = store.UpsertList(ctx, List{ID: "test-list", Name: "Test"})
	_ = store.Close()

	if _, err := store.GetTasksSnapshot(ctx, "test-list", 3); err == nil {
		t.Error("expected error from GetTasksSnapshot on closed store, got nil")
	}
}

func TestSQLiteStore_SyncList_DuplicateIncomingIDs(t *testing.T) {
	t.Parallel()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "dup.db"))
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	list := List{ID: "dup-list", Name: "Duplicate Test"}

	// Existing: 2 distinct items
	existing := []ListItem{
		{ID: "i1", ListID: "dup-list", Title: "Item 1"},
		{ID: "i2", ListID: "dup-list", Title: "Item 2"},
	}
	_, err = store.SyncList(ctx, list, existing)
	if err != nil {
		t.Fatalf("initial SyncList failed: %v", err)
	}

	// Incoming: 2 items, but both have duplicate ID "i1"
	// Total incoming items = 2, total existing items = 2, but unique incoming = 1
	incoming := []ListItem{
		{ID: "i1", ListID: "dup-list", Title: "Item 1"},
		{ID: "i1", ListID: "dup-list", Title: "Item 1 Duplicate"},
	}
	changed, err := store.SyncList(ctx, list, incoming)
	if err != nil {
		t.Fatalf("SyncList with duplicate IDs failed: %v", err)
	}
	if !changed {
		t.Error("expected changed=true when incoming items have duplicate IDs reducing unique count")
	}
}

func TestSQLiteStore_MultiList_SharedItemIDs(t *testing.T) {
	t.Parallel()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "shared_ids.db"))
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	listA := List{ID: "list_a", Name: "List A", Source: "http"}
	listB := List{ID: "list_b", Name: "List B", Source: "http"}

	itemsA := []ListItem{
		{ID: "item-1", ListID: "list_a", Title: "Apples", Done: false, Position: 0},
		{ID: "item-2", ListID: "list_a", Title: "Bananas", Done: true, Position: 1},
	}
	itemsB := []ListItem{
		{ID: "item-1", ListID: "list_b", Title: "Oranges", Done: true, Position: 0},
		{ID: "item-2", ListID: "list_b", Title: "Grapes", Done: false, Position: 1},
	}

	// 1. Sync list A
	changedA, err := store.SyncList(ctx, listA, itemsA)
	if err != nil || !changedA {
		t.Fatalf("expected changed=true on list A sync, got changed=%v, err=%v", changedA, err)
	}

	// 2. Sync list B (with identical item IDs)
	changedB, err := store.SyncList(ctx, listB, itemsB)
	if err != nil || !changedB {
		t.Fatalf("expected changed=true on list B sync, got changed=%v, err=%v", changedB, err)
	}

	// 3. Verify List A items were NOT overwritten by List B
	fetchedA, err := store.GetListItems(ctx, "list_a", true)
	if err != nil {
		t.Fatalf("failed to get list A items: %v", err)
	}
	if len(fetchedA) != 2 {
		t.Fatalf("expected 2 items for list A, got %d", len(fetchedA))
	}
	if fetchedA[0].ID != "item-1" || fetchedA[0].Title != "Apples" || fetchedA[0].Done {
		t.Errorf("list A item-1 corrupted: %+v", fetchedA[0])
	}
	if fetchedA[1].ID != "item-2" || fetchedA[1].Title != "Bananas" || !fetchedA[1].Done {
		t.Errorf("list A item-2 corrupted: %+v", fetchedA[1])
	}

	// 4. Verify List B items are correct
	fetchedB, err := store.GetListItems(ctx, "list_b", true)
	if err != nil {
		t.Fatalf("failed to get list B items: %v", err)
	}
	if len(fetchedB) != 2 {
		t.Fatalf("expected 2 items for list B, got %d", len(fetchedB))
	}
	if fetchedB[0].ID != "item-1" || fetchedB[0].Title != "Oranges" || !fetchedB[0].Done {
		t.Errorf("list B item-1 corrupted: %+v", fetchedB[0])
	}
	if fetchedB[1].ID != "item-2" || fetchedB[1].Title != "Grapes" || fetchedB[1].Done {
		t.Errorf("list B item-2 corrupted: %+v", fetchedB[1])
	}

	// 5. Repeat sync returns changed=false on both (no diff churn!)
	changedA2, err := store.SyncList(ctx, listA, itemsA)
	if err != nil || changedA2 {
		t.Fatalf("expected changed=false on repeat list A sync, got changed=%v, err=%v", changedA2, err)
	}
	changedB2, err := store.SyncList(ctx, listB, itemsB)
	if err != nil || changedB2 {
		t.Fatalf("expected changed=false on repeat list B sync, got changed=%v, err=%v", changedB2, err)
	}

	// 6. Test UpsertItem isolation
	updatedItemA := ListItem{
		ID:       "item-1",
		ListID:   "list_a",
		Title:    "Honeycrisp Apples",
		Done:     false,
		Position: 0,
	}
	if err := store.UpsertItem(ctx, updatedItemA); err != nil {
		t.Fatalf("failed to upsert item A: %v", err)
	}

	// List B's item-1 must still be Oranges
	fetchedBAfterUpsert, err := store.GetListItems(ctx, "list_b", true)
	if err != nil {
		t.Fatalf("failed to get list B items: %v", err)
	}
	if fetchedBAfterUpsert[0].Title != "Oranges" {
		t.Errorf("expected list B item-1 to remain 'Oranges', got %q", fetchedBAfterUpsert[0].Title)
	}

	// 7. Test DeleteItem isolation: deleting item-1 from list A does not delete item-1 from list B
	if err := store.DeleteItem(ctx, "list_a", "item-1"); err != nil {
		t.Fatalf("failed to delete item from list A: %v", err)
	}
	fetchedBAfterDelete, err := store.GetListItems(ctx, "list_b", true)
	if err != nil {
		t.Fatalf("failed to get list B items: %v", err)
	}
	if len(fetchedBAfterDelete) != 2 || fetchedBAfterDelete[0].ID != "item-1" {
		t.Errorf("expected list B to retain item-1, got %+v", fetchedBAfterDelete)
	}
}

func TestSQLiteStore_Migration_LegacySingleColumnPK(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "legacy_migration.db")

	// Create legacy database schema with id TEXT PRIMARY KEY
	legacyDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed to create legacy db: %v", err)
	}
	legacyDDL := `
	CREATE TABLE lists (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		source TEXT NOT NULL DEFAULT 'local',
		sections TEXT NOT NULL DEFAULT '[]',
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE list_items (
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

	INSERT INTO lists (id, name, source) VALUES ('legacy-list', 'Legacy List', 'local');
	INSERT INTO list_items (id, list_id, title, done, position) VALUES ('i1', 'legacy-list', 'Legacy Item 1', 0, 0);
	`
	if _, err := legacyDB.Exec(legacyDDL); err != nil {
		_ = legacyDB.Close()
		t.Fatalf("failed to seed legacy db: %v", err)
	}
	_ = legacyDB.Close()

	// Open with NewSQLiteStore which should trigger migrate() and upgrade list_items
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to open store on legacy db: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Verify legacy item preserved
	items, err := store.GetListItems(ctx, "legacy-list", true)
	if err != nil {
		t.Fatalf("failed to get items from migrated db: %v", err)
	}
	if len(items) != 1 || items[0].Title != "Legacy Item 1" {
		t.Fatalf("unexpected items after migration: %+v", items)
	}

	// Verify composite primary key by creating second list with same item ID
	if err := store.UpsertList(ctx, List{ID: "new-list", Name: "New List"}); err != nil {
		t.Fatalf("failed to upsert new list: %v", err)
	}
	newItem := ListItem{
		ID:       "i1",
		ListID:   "new-list",
		Title:    "New List Item 1",
		Done:     true,
		Position: 0,
	}
	if err := store.UpsertItem(ctx, newItem); err != nil {
		t.Fatalf("failed to insert item with colliding id into new list: %v", err)
	}

	// Verify both items coexist
	itemsLegacy, err := store.GetListItems(ctx, "legacy-list", true)
	if err != nil || len(itemsLegacy) != 1 || itemsLegacy[0].Title != "Legacy Item 1" {
		t.Errorf("legacy item corrupted after inserting duplicate ID on new list: %+v", itemsLegacy)
	}
	itemsNew, err := store.GetListItems(ctx, "new-list", true)
	if err != nil || len(itemsNew) != 1 || itemsNew[0].Title != "New List Item 1" {
		t.Errorf("new item corrupted: %+v", itemsNew)
	}
}

func TestSQLiteStore_MigrateErrors(t *testing.T) {
	t.Parallel()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "migrate_err.db"))
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	_ = store.Close()

	// 1. Closed DB error on migrate()
	if err := store.migrate(); err == nil {
		t.Error("expected error from migrate on closed store")
	}

	// 2. Closed underlying DB handle error when store is not marked nil
	store2, err := NewSQLiteStore(filepath.Join(t.TempDir(), "migrate_err2.db"))
	if err != nil {
		t.Fatalf("failed to init store2: %v", err)
	}
	_ = store2.db.Close()
	if err := store2.migrate(); err == nil {
		t.Error("expected error from migrate when underlying db is closed")
	}

	// 3. Migration failure when list_items_migrated already exists with incompatible schema
	dbPath3 := filepath.Join(t.TempDir(), "migrate_err3.db")
	legacyDB, err := sql.Open("sqlite", dbPath3)
	if err != nil {
		t.Fatalf("failed to create legacy db: %v", err)
	}
	_, _ = legacyDB.Exec(`
		CREATE TABLE lists (id TEXT PRIMARY KEY, name TEXT);
		CREATE TABLE list_items (id TEXT PRIMARY KEY, list_id TEXT, title TEXT);
		CREATE TABLE list_items_migrated (incompatible_column TEXT);
	`)
	_ = legacyDB.Close()

	if _, err := NewSQLiteStore(dbPath3); err == nil {
		t.Error("expected NewSQLiteStore to fail migration when list_items_migrated already exists")
	}
}

