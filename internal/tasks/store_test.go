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

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("newTestStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func mkList(id, name, source string, sections ...string) List {
	return List{ID: id, Name: name, Source: source, Sections: sections}
}

func mkItem(id, listID, title, section string, done bool, pos int, assignee, due string) ListItem {
	it := ListItem{ID: id, ListID: listID, Title: title, Section: section, Done: done, Position: pos}
	if assignee != "" { it.Assignee = &assignee }
	if due != "" { it.DueDate = &due }
	return it
}

const legCols = "id TEXT PRIMARY KEY, list_id TEXT REFERENCES lists(id), title TEXT NOT NULL, done BOOLEAN DEFAULT 0, section TEXT, position INTEGER DEFAULT 0, assignee TEXT, due_date TEXT, created_at DATETIME DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME DEFAULT CURRENT_TIMESTAMP"

func TestSQLiteStore_InitAndMigrate(t *testing.T) {
	tests := []struct {
		name, pathType, setupSQL, listID string
		wantInitErr                      bool
		wantItems                        int
	}{
		{name: "pragmas", pathType: "nested"},
		{name: "flat_path", pathType: "flat"},
		{name: "default_path", pathType: "default"},
		{name: "mkdir_error", pathType: "bad_dir", wantInitErr: true},
		{name: "pragma_error", pathType: "bad_device", wantInitErr: true},
		{
			name:     "legacy_single_col_pk",
			setupSQL: "CREATE TABLE lists (id TEXT PRIMARY KEY, name TEXT); CREATE TABLE list_items (" + legCols + "); INSERT INTO lists VALUES ('legacy-list', 'Legacy'); INSERT INTO list_items VALUES ('i1', 'legacy-list', 'Milk', 0, '', 1, '', '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);",
			listID:   "legacy-list", wantItems: 1,
		},
		{
			name:     "preexisting_migrated_cleanup",
			setupSQL: "CREATE TABLE lists (id TEXT PRIMARY KEY, name TEXT); CREATE TABLE list_items (" + legCols + "); CREATE TABLE list_items_migrated (junk_col TEXT); INSERT INTO list_items_migrated VALUES ('stale'); INSERT INTO lists VALUES ('todo', 'Todo'); INSERT INTO list_items VALUES ('t1', 'todo', 'Buy Milk', 0, '', 1, '', '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);",
			listID:   "todo", wantItems: 1,
		},
		{
			name:     "orphaned_migrated_recovery",
			setupSQL: "CREATE TABLE lists (id TEXT PRIMARY KEY, name TEXT); CREATE TABLE list_items_migrated (id TEXT NOT NULL, list_id TEXT, title TEXT NOT NULL, done BOOLEAN DEFAULT 0, section TEXT, position INTEGER DEFAULT 0, assignee TEXT, due_date TEXT, created_at DATETIME DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME DEFAULT CURRENT_TIMESTAMP, PRIMARY KEY (list_id, id)); INSERT INTO lists VALUES ('chores', 'Chores'); INSERT INTO list_items_migrated VALUES ('c1', 'chores', 'Sweep', 1, '', 0, '', '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);",
			listID:   "chores", wantItems: 1,
		},
		{
			name:        "not_null_violation_rollback",
			setupSQL:    "CREATE TABLE lists (id TEXT PRIMARY KEY, name TEXT); CREATE TABLE list_items (id TEXT PRIMARY KEY, list_id TEXT, title TEXT, done BOOLEAN DEFAULT 0, section TEXT, position INTEGER DEFAULT 0, assignee TEXT, due_date TEXT, created_at DATETIME DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME DEFAULT CURRENT_TIMESTAMP); INSERT INTO lists VALUES ('l1', 'List 1'); INSERT INTO list_items (id, list_id, title) VALUES ('i1', 'l1', NULL);",
			wantInitErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "store.db")
			switch tc.pathType {
			case "nested": dbPath = filepath.Join(t.TempDir(), "nested", "deep", "store.db")
			case "flat":
				origWd, _ := os.Getwd()
				_ = os.Chdir(t.TempDir())
				defer func() { _ = os.Chdir(origWd) }()
				dbPath = "flat.db"
			case "default":
				orig := defaultDBPath
				defaultDBPath = filepath.Join(t.TempDir(), "def.db")
				defer func() { defaultDBPath = orig }()
				dbPath = ""
			case "bad_dir": dbPath = "/dev/null/impossible/lists.db"
			case "bad_device": dbPath = "/dev/null"
			}
			if tc.setupSQL != "" {
				rawDB, _ := sql.Open("sqlite", dbPath)
				_, _ = rawDB.Exec(tc.setupSQL)
				_ = rawDB.Close()
			}
			s, err := NewSQLiteStore(dbPath)
			if (err != nil) != tc.wantInitErr {
				t.Fatalf("NewSQLiteStore err = %v, wantInitErr = %v", err, tc.wantInitErr)
			}
			if err != nil {
				return
			}
			defer s.Close()
			if tc.pathType == "nested" {
				var jm string; var fk, bt int
				_ = s.db.QueryRow("PRAGMA journal_mode;").Scan(&jm)
				_ = s.db.QueryRow("PRAGMA foreign_keys;").Scan(&fk)
				_ = s.db.QueryRow("PRAGMA busy_timeout;").Scan(&bt)
				if jm != "wal" || fk != 1 || bt != 5000 {
					t.Fatalf("pragmas mismatch")
				}
			}
			if tc.listID != "" {
				if items, err := s.GetListItems(context.Background(), tc.listID, true); err != nil || len(items) != tc.wantItems {
					t.Fatalf("count mismatch")
				}
			}
		})
	}
}

func TestSQLiteStore_ListAndItemCRUD(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, action, targetID, itemID string
		seedLists                     []List
		seedItems                     []ListItem
		rawSQL                        string
		includeDone                   bool
		wantCount                     int
		wantErr                       error
	}{
		{name: "get_list_not_found", action: "get_list", targetID: "missing", wantErr: ErrListNotFound},
		{name: "get_list_success", action: "get_list", targetID: "l1", seedLists: []List{mkList("l1", "List 1", "local", "S1", "S2")}, wantCount: 2},
		{name: "get_list_malformed_sections", action: "get_list", targetID: "bad", rawSQL: `INSERT INTO lists (id, name, sections) VALUES ('bad', 'Bad', '{invalid')`},
		{name: "get_list_null_sections", action: "get_list", targetID: "null_sec", rawSQL: `INSERT INTO lists (id, name, sections) VALUES ('null_sec', 'Null', 'null')`},
		{name: "get_list_empty_sections", action: "get_list", targetID: "emp_sec", rawSQL: `INSERT INTO lists (id, name, sections) VALUES ('emp_sec', 'Emp', '')`},
		{name: "upsert_list", action: "upsert_list", targetID: "l_up"},
		{name: "delete_list", action: "delete_list", targetID: "l_del", seedLists: []List{mkList("l_del", "Del", "local")}},
		{name: "delete_list_cascade", action: "delete_list_cascade", targetID: "l_cas", seedLists: []List{mkList("l_cas", "Cas", "local")}, seedItems: []ListItem{mkItem("i_cas", "l_cas", "I", "", false, 0, "", "")}},
		{name: "get_items_missing_list", action: "get_items", targetID: "missing_list", wantErr: ErrListNotFound},
		{name: "get_items_empty_list", action: "get_items", targetID: "l_emp", seedLists: []List{mkList("l_emp", "Emp", "local")}},
		{
			name: "get_items_filtered", action: "get_items_filtered", targetID: "l_items", seedLists: []List{mkList("l_items", "Items", "local")},
			seedItems: []ListItem{mkItem("c1", "l_items", "A", "", false, 10, "Alex", "2026-09-26"), mkItem("c2", "l_items", "B", "", true, 5, "", ""), mkItem("c3", "l_items", "C", "", false, 2, "", "")},
		},
		{name: "upsert_item", action: "upsert_item", targetID: "l_iu", itemID: "i_up", seedLists: []List{mkList("l_iu", "L", "local")}},
		{name: "delete_item", action: "delete_item", targetID: "l_dit", itemID: "it1", seedLists: []List{mkList("l_dit", "L", "local")}, seedItems: []ListItem{mkItem("it1", "l_dit", "I", "", false, 0, "", "")}},
		{
			name: "shared_item_ids_isolation", action: "shared_item_ids", targetID: "la", itemID: "shared",
			seedLists: []List{mkList("la", "A", "local"), mkList("lb", "B", "local")},
			seedItems: []ListItem{mkItem("shared", "la", "Apples", "", false, 0, "", ""), mkItem("shared", "lb", "Oranges", "", true, 0, "", "")},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s, ctx := newTestStore(t), context.Background()
			if tc.rawSQL != "" { _, _ = s.db.ExecContext(ctx, tc.rawSQL) }
			for _, l := range tc.seedLists { _ = s.UpsertList(ctx, l) }
			for _, it := range tc.seedItems { _ = s.UpsertItem(ctx, it) }
			switch tc.action {
			case "get_list":
				l, err := s.GetList(ctx, tc.targetID)
				if !errors.Is(err, tc.wantErr) || (err == nil && len(l.Sections) != tc.wantCount) {
					t.Fatalf("GetList mismatch")
				}
			case "upsert_list":
				_ = s.UpsertList(ctx, List{ID: tc.targetID, Name: "Initial"})
				_ = s.UpsertList(ctx, List{ID: tc.targetID, Name: "Updated", Sections: []string{"A"}})
				if got, err := s.GetList(ctx, tc.targetID); err != nil || got.Name != "Updated" || len(got.Sections) != 1 {
					t.Fatalf("upsert mismatch")
				}
			case "delete_list":
				if s.DeleteList(ctx, tc.targetID) != nil || !errors.Is(s.DeleteList(ctx, tc.targetID), ErrListNotFound) {
					t.Fatalf("delete list failed")
				}
			case "delete_list_cascade":
				_ = s.DeleteList(ctx, tc.targetID)
				_ = s.UpsertList(ctx, mkList(tc.targetID, "Revived", "local"))
				if items, err := s.GetListItems(ctx, tc.targetID, true); err != nil || len(items) != 0 {
					t.Fatalf("cascade failed")
				}
			case "get_items":
				if items, err := s.GetListItems(ctx, tc.targetID, tc.includeDone); !errors.Is(err, tc.wantErr) || (err == nil && len(items) != tc.wantCount) {
					t.Fatalf("GetListItems mismatch")
				}
			case "get_items_filtered":
				all, err1 := s.GetListItems(ctx, tc.targetID, true)
				act, err2 := s.GetListItems(ctx, tc.targetID, false)
				if err1 != nil || err2 != nil || len(all) != 3 || len(act) != 2 || all[0].ID != "c3" || act[0].ID != "c3" {
					t.Fatalf("filtering mismatch")
				}
			case "upsert_item":
				_ = s.UpsertItem(ctx, ListItem{ID: tc.itemID, ListID: tc.targetID, Title: "Item"})
				_ = s.UpsertItem(ctx, ListItem{ID: tc.itemID, ListID: tc.targetID, Title: "Item Updated"})
			case "delete_item":
				if s.DeleteItem(ctx, tc.targetID, tc.itemID) != nil || !errors.Is(s.DeleteItem(ctx, tc.targetID, tc.itemID), ErrItemNotFound) {
					t.Fatalf("delete item failed")
				}
			case "shared_item_ids":
				_ = s.UpsertItem(ctx, mkItem(tc.itemID, tc.targetID, "Updated Apples", "", false, 0, "", ""))
				if itemsB, err := s.GetListItems(ctx, "lb", true); err != nil || len(itemsB) != 1 || itemsB[0].Title != "Oranges" {
					t.Fatalf("List B corrupted")
				}
				_ = s.DeleteItem(ctx, tc.targetID, tc.itemID)
				if itemsBAfter, err := s.GetListItems(ctx, "lb", true); err != nil || len(itemsBAfter) != 1 || itemsBAfter[0].ID != tc.itemID {
					t.Fatalf("List B corrupted delete")
				}
			}
		})
	}
}

func TestSQLiteStore_GetTasksSnapshot(t *testing.T) {
	t.Parallel()
	store, ctx, now := newTestStore(t), context.Background(), time.Now().UTC()
	assignee, dueDate := "Alex", "2026-09-26"
	_ = store.UpsertList(ctx, mkList("groceries", "Groceries", "local"))
	for _, it := range []ListItem{
		{ID: "i1", ListID: "groceries", Title: "Milk", Section: "Dairy", Assignee: &assignee, DueDate: &dueDate, Position: 1, CreatedAt: now},
		{ID: "i2", ListID: "groceries", Title: "Eggs", Section: "Dairy", Position: 2, CreatedAt: now},
		{ID: "i3", ListID: "groceries", Title: "Bread", Done: true, Section: "Bakery", Position: 3, UpdatedAt: now.Add(-3 * time.Minute)},
		{ID: "i4", ListID: "groceries", Title: "Butter", Done: true, Section: "Dairy", Position: 4, UpdatedAt: now.Add(-2 * time.Minute)},
		{ID: "i5", ListID: "groceries", Title: "Cheese", Done: true, Section: "Dairy", Assignee: &assignee, DueDate: &dueDate, Position: 5, UpdatedAt: now.Add(-time.Minute)},
	} {
		_ = store.UpsertItem(ctx, it)
	}

	tests := []struct {
		name, listID             string
		showCompleted, wantCount int
		wantIDs                  []string
		wantErr                  error
	}{
		{"missing_list", "missing", 3, 0, nil, ErrListNotFound},
		{"show_completed_0", "groceries", 0, 2, []string{"i1", "i2"}, nil},
		{"show_completed_2", "groceries", 2, 4, []string{"i1", "i2", "i5", "i4"}, nil},
		{"show_completed_all", "groceries", -1, 5, []string{"i1", "i2", "i5", "i4", "i3"}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			snap, err := store.GetTasksSnapshot(ctx, tc.listID, tc.showCompleted)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil && len(snap.Items) != tc.wantCount {
				t.Fatalf("count = %d, want %d", len(snap.Items), tc.wantCount)
			}
		})
	}
}

func TestSQLiteStore_SyncList(t *testing.T) {
	t.Parallel()
	due, ass := "2026-09-30", "Alex"
	tests := []struct {
		name        string
		seedList    *List
		seedItems   []ListItem
		corruptSQL  string
		cancelCtx   bool
		closeDB     bool
		syncList    List
		syncItems   []ListItem
		wantChanged bool
		wantErr     bool
		wantCount   int
	}{
		{name: "cold_sync", syncList: mkList("s1", "Initial", ""), syncItems: []ListItem{mkItem("i1", "s1", "A", "", false, 0, ass, due), mkItem("i2", "s1", "B", "", false, 1, "", "")}, wantChanged: true, wantCount: 2},
		{name: "noop_repeat", seedList: &List{ID: "s2", Name: "L", Source: "local"}, seedItems: []ListItem{mkItem("i1", "s2", "A", "", false, 0, "", "")}, syncList: mkList("s2", "L", "local"), syncItems: []ListItem{mkItem("i1", "s2", "A", "", false, 0, "", "")}, wantChanged: false, wantCount: 1},
		{name: "item_done_modification", seedList: &List{ID: "s3", Name: "L", Source: "local"}, seedItems: []ListItem{mkItem("i1", "s3", "A", "", false, 0, "", "")}, syncList: mkList("s3", "L", "local"), syncItems: []ListItem{mkItem("i1", "s3", "A", "", true, 0, "", "")}, wantChanged: true, wantCount: 1},
		{name: "item_attr_change", seedList: &List{ID: "s3b", Name: "L", Source: "local"}, seedItems: []ListItem{mkItem("i1", "s3b", "A", "Old", false, 0, "A", "2026-09-01")}, syncList: mkList("s3b", "L", "local"), syncItems: []ListItem{mkItem("i1", "s3b", "A", "New", false, 5, "B", "2026-10-01")}, wantChanged: true, wantCount: 1},
		{name: "item_deletion", seedList: &List{ID: "s4", Name: "L", Source: "local"}, seedItems: []ListItem{mkItem("i1", "s4", "A", "", false, 0, "", ""), mkItem("i2", "s4", "B", "", false, 1, "", "")}, syncList: mkList("s4", "L", "local"), syncItems: []ListItem{mkItem("i1", "s4", "A", "", false, 0, "", "")}, wantChanged: true, wantCount: 1},
		{name: "list_name_sections_change", seedList: &List{ID: "s5", Name: "Old", Source: "local", Sections: []string{"A"}}, seedItems: []ListItem{mkItem("i1", "s5", "A", "", false, 0, "", "")}, syncList: mkList("s5", "New", "local", "B"), syncItems: []ListItem{mkItem("i1", "s5", "A", "", false, 0, "", "")}, wantChanged: true, wantCount: 1},
		{name: "list_source_change", seedList: &List{ID: "s5b", Name: "L", Source: "local"}, syncList: mkList("s5b", "L", "http"), wantChanged: true, wantCount: 0},
		{name: "empty_items_sync", seedList: &List{ID: "s6", Name: "L", Source: "local"}, seedItems: []ListItem{mkItem("i1", "s6", "A", "", false, 0, "", "")}, syncList: mkList("s6", "L", "local"), syncItems: []ListItem{}, wantChanged: true, wantCount: 0},
		{name: "item_id_replacement", seedList: &List{ID: "s7", Name: "L", Source: "local"}, seedItems: []ListItem{mkItem("i1", "s7", "A", "", false, 0, "", "")}, syncList: mkList("s7", "L", "local"), syncItems: []ListItem{mkItem("i2", "s7", "B", "", false, 0, "", "")}, wantChanged: true, wantCount: 1},
		{name: "duplicate_incoming_ids", seedList: &List{ID: "s8", Name: "L", Source: "local"}, seedItems: []ListItem{mkItem("i1", "s8", "A", "", false, 0, "", ""), mkItem("i2", "s8", "B", "", false, 1, "", "")}, syncList: mkList("s8", "L", "local"), syncItems: []ListItem{mkItem("i1", "s8", "A", "", false, 0, "", ""), mkItem("i1", "s8", "A Dup", "", false, 0, "", "")}, wantChanged: true, wantCount: 1},
		{name: "canceled_ctx", cancelCtx: true, syncList: mkList("s9", "L", "local"), wantErr: true},
		{name: "closed_db", closeDB: true, syncList: mkList("s10", "L", "local"), wantErr: true},
		{name: "dropped_lists_table", corruptSQL: "DROP TABLE lists;", syncList: mkList("s11", "L", "local"), wantErr: true},
		{name: "dropped_items_table", seedList: &List{ID: "s12", Name: "L", Source: "local"}, corruptSQL: "DROP TABLE list_items;", syncList: mkList("s12", "L", "local"), wantErr: true},
		{name: "scan_error", seedList: &List{ID: "s13", Name: "L", Source: "local"}, corruptSQL: "DROP TABLE list_items; CREATE TABLE list_items (id TEXT, list_id TEXT, title TEXT, done TEXT, section TEXT, position TEXT, assignee TEXT, due_date TEXT); INSERT INTO list_items VALUES ('i1', 's13', 't', 'not-int', 's', 'not-int', 'a', 'd');", syncList: mkList("s13", "L", "local"), wantErr: true},
		{name: "corrupt_sections_json", seedList: &List{ID: "s14", Name: "L", Source: "local"}, corruptSQL: "UPDATE lists SET sections = '{bad-json' WHERE id = 's14';", syncList: mkList("s14", "L", "local"), wantChanged: false, wantCount: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s, ctx := newTestStore(t), context.Background()
			if tc.seedList != nil { _ = s.UpsertList(ctx, *tc.seedList) }
			for _, it := range tc.seedItems { _ = s.UpsertItem(ctx, it) }
			if tc.corruptSQL != "" { _, _ = s.db.ExecContext(ctx, tc.corruptSQL) }
			if tc.closeDB { _ = s.Close() }
			if tc.cancelCtx {
				cCtx, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cCtx
			}
			changed, err := s.SyncList(ctx, tc.syncList, tc.syncItems)
			if (err != nil) != tc.wantErr {
				t.Fatalf("SyncList err = %v, wantErr = %v", err, tc.wantErr)
			}
			if err == nil {
				if changed != tc.wantChanged {
					t.Fatalf("changed = %v, want %v", changed, tc.wantChanged)
				}
				items, err := s.GetListItems(context.Background(), tc.syncList.ID, true)
				if err != nil || len(items) != tc.wantCount {
					t.Fatalf("GetListItems count = %d, want %d, err = %v", len(items), tc.wantCount, err)
				}
			}
		})
	}
}

func TestSQLiteStore_CompleteItem(t *testing.T) {
	t.Parallel()
	s, ctx := newTestStore(t), context.Background()
	_ = s.UpsertList(ctx, mkList("list-1", "Test", "local"))
	_ = s.UpsertItem(ctx, mkItem("item-1", "list-1", "Buy", "", false, 0, "", ""))

	tests := []struct{ name, listID, itemID string; wantErr error }{
		{"first_completion", "list-1", "item-1", nil}, {"idempotent_repeat", "list-1", "item-1", nil},
		{"item_not_found", "list-1", "item-999", ErrItemNotFound}, {"list_not_found", "non-existent-list", "item-1", ErrItemNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := s.CompleteItem(ctx, tc.listID, tc.itemID); !errors.Is(err, tc.wantErr) {
				t.Fatalf("CompleteItem err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func runOp(ts *SQLiteStore, c context.Context, op string) (err error) {
	switch op {
	case "get_list": _, err = ts.GetList(c, "any")
	case "get_items": _, err = ts.GetListItems(c, "any", true)
	case "snapshot": _, err = ts.GetTasksSnapshot(c, "any", 3)
	case "upsert_list": err = ts.UpsertList(c, mkList("any", "Any", "local"))
	case "upsert_item": err = ts.UpsertItem(c, mkItem("any", "any", "Any", "", false, 0, "", ""))
	case "delete_list": err = ts.DeleteList(c, "any")
	case "delete_item": err = ts.DeleteItem(c, "any", "any")
	case "complete_item": err = ts.CompleteItem(c, "any", "any")
	case "migrate": err = ts.migrate()
	case "syntax": _, err = ts.queryItems(c, "SELECT INVALID SYNTAX")
	case "scan": _, err = ts.queryItems(c, "SELECT 'id', 'lid', 't', 'not-int', 's', 'not-int', 'a', 'd', 'cr', 'up'")
	case "drop_snap", "drop_items":
		_ = ts.UpsertList(c, mkList("fd", "FD", "local"))
		_, _ = ts.db.Exec("DROP TABLE list_items")
		if op == "drop_snap" { _, err = ts.GetTasksSnapshot(c, "fd", 3) } else { _, err = ts.GetListItems(c, "fd", true) }
	}
	return err
}

func TestSQLiteStore_ConcurrencyAndErrors(t *testing.T) {
	t.Parallel()
	s, ctx := newTestStore(t), context.Background()
	_ = s.UpsertList(ctx, mkList("stress", "Stress", "local"))

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for op := 0; op < 20; op++ {
				_ = s.UpsertItem(ctx, mkItem(filepath.Join("it", string(rune('A'+workerID)), string(rune('0'+op))), "stress", "W", "", false, op, "", ""))
				_, _ = s.GetListItems(ctx, "stress", true)
				_, _ = s.GetTasksSnapshot(ctx, "stress", 3)
			}
		}(w)
	}
	wg.Wait()
	if err := s.Close(); err != nil || s.Close() != nil {
		t.Fatalf("close failed")
	}

	tests := []struct{ name, state, op string }{
		{"cancel_get_list", "cancel", "get_list"}, {"cancel_get_items", "cancel", "get_items"},
		{"cancel_snapshot", "cancel", "snapshot"}, {"cancel_upsert_list", "cancel", "upsert_list"},
		{"cancel_upsert_item", "cancel", "upsert_item"}, {"cancel_delete_list", "cancel", "delete_list"},
		{"cancel_delete_item", "cancel", "delete_item"}, {"cancel_complete_item", "cancel", "complete_item"},
		{"closed_get_list", "closed", "get_list"}, {"closed_get_items", "closed", "get_items"},
		{"closed_snapshot", "closed", "snapshot"}, {"closed_upsert_list", "closed", "upsert_list"},
		{"closed_upsert_item", "closed", "upsert_item"}, {"closed_delete_list", "closed", "delete_list"},
		{"closed_delete_item", "closed", "delete_item"}, {"closed_complete_item", "closed", "complete_item"},
		{"closed_migrate", "closed", "migrate"}, {"query_syntax", "syntax", "syntax"}, {"query_scan", "scan", "scan"}, {"drop_snap", "ok", "drop_snap"}, {"drop_items", "ok", "drop_items"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ts, c := newTestStore(t), context.Background()
			if tc.state == "cancel" {
				var cancel context.CancelFunc
				c, cancel = context.WithCancel(c)
				cancel()
			} else if tc.state == "closed" {
				_ = ts.Close()
			}
			if err := runOp(ts, c, tc.op); err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}

func TestParseTime(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		raw      string
		wantZero bool
		wantYear int
	}{
		{"empty", "", true, 0}, {"invalid", "bad", true, 0},
		{"rfc3339", "2026-10-18T19:43:43Z", false, 2026}, {"rfc3339_nano", "2026-10-18T19:43:43.123456789Z", false, 2026},
		{"sqlite_dt", "2026-09-26 14:30:00", false, 2026}, {"sqlite_tz", "2026-09-26 14:30:00-07:00", false, 2026},
		{"sqlite_iso", "2026-09-26T14:30:00", false, 2026},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := parseTime(tc.raw); (tc.wantZero && !got.IsZero()) || (!tc.wantZero && (got.IsZero() || got.Year() != tc.wantYear)) {
				t.Fatalf("parseTime(%q) = %v", tc.raw, got)
			}
		})
	}
}
