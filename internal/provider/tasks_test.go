package provider_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/provider"
	"github.com/azylman/mirrormere/internal/tasks"
	"github.com/azylman/mirrormere/internal/tasks/adapters"
)

type mockTasksStore struct {
	list                                         *tasks.List
	items                                        []tasks.ListItem
	snapshot                                     *tasks.TasksSnapshot
	getSnapErr, upsertErr, closeErr, completeErr error
	closed                                       bool
	upsertCalls, completeCalls, deleteCalls      int
	lastListID, lastCompletedID, lastDeletedID   string
	lastShow                                     int
}

func (m *mockTasksStore) GetList(_ context.Context, id string) (*tasks.List, error) {
	if m.list != nil && m.list.ID == id {
		return m.list, nil
	}
	return nil, tasks.ErrListNotFound
}
func (m *mockTasksStore) GetListItems(_ context.Context, _ string, _ bool) ([]tasks.ListItem, error) {
	return m.items, nil
}
func (m *mockTasksStore) GetTasksSnapshot(_ context.Context, listID string, show int) (*tasks.TasksSnapshot, error) {
	m.lastListID, m.lastShow = listID, show
	return m.snapshot, m.getSnapErr
}
func (m *mockTasksStore) UpsertList(_ context.Context, list tasks.List) error {
	m.upsertCalls++
	m.list = &list
	return m.upsertErr
}
func (m *mockTasksStore) UpsertItem(_ context.Context, _ tasks.ListItem) error { return nil }
func (m *mockTasksStore) SyncList(_ context.Context, list tasks.List, items []tasks.ListItem) (bool, error) {
	m.list, m.items = &list, items
	return m.upsertErr == nil, m.upsertErr
}
func (m *mockTasksStore) DeleteList(_ context.Context, _ string) error { return nil }
func (m *mockTasksStore) DeleteItem(_ context.Context, _, itemID string) error {
	m.deleteCalls++
	m.lastDeletedID = itemID
	return nil
}
func (m *mockTasksStore) CompleteItem(_ context.Context, _, itemID string) error {
	m.completeCalls++
	m.lastCompletedID = itemID
	return m.completeErr
}
func (m *mockTasksStore) Close() error { m.closed = true; return m.closeErr }

type fakeAdapter struct {
	name     string
	list     *tasks.List
	items    []tasks.ListItem
	fetchErr error
}

func (f *fakeAdapter) Name() string { return f.name }
func (f *fakeAdapter) FetchList(_ context.Context) (*tasks.List, []tasks.ListItem, error) {
	return f.list, f.items, f.fetchErr
}

type mockCompleterAdapter struct {
	lastItem string
	calls    int
	err      error
}

func (m *mockCompleterAdapter) Name() string { return "mock-completer" }
func (m *mockCompleterAdapter) FetchList(_ context.Context) (*tasks.List, []tasks.ListItem, error) {
	return &tasks.List{ID: "test"}, nil, nil
}
func (m *mockCompleterAdapter) CompleteItem(_ context.Context, itemID string) error {
	m.calls++
	m.lastItem = itemID
	return m.err
}

func mkTaskList(id, name, source string) tasks.List {
	return tasks.List{ID: id, Name: name, Source: source}
}
func mkTaskItem(id, listID, title, section string, done bool) tasks.ListItem {
	return tasks.ListItem{ID: id, ListID: listID, Title: title, Section: section, Done: done}
}
func mkOpts(id string, sec map[string]string, tok string) provider.InitOptions {
	return provider.InitOptions{ID: id, Secrets: sec, Token: tok}
}
func mkCfg(src, subKey string, sub map[string]any) map[string]any {
	if subKey == "" {
		return map[string]any{"source": src}
	}
	return map[string]any{"source": src, subKey: sub}
}

func TestTasksProvider_Init(t *testing.T) {
	t.Parallel()
	reg := provider.NewRegistry()
	if p, err := reg.Create("tasks"); err != nil || p == nil || !reg.Has("tasks") {
		t.Fatal("registry tasks provider creation failed")
	}
	if provider.NewTasksProviderWithStoreAndNotifier(nil, nil) == nil || provider.NewTasksProviderWithAdapter(nil, nil, nil) == nil {
		t.Fatal("expected non-nil provider from constructors")
	}

	tmpDir := t.TempDir()
	for f, c := range map[string]string{
		"creds.json":     `{"client_id":"cid","client_secret":"csec","refresh_token":"rt","token":"tok"}`,
		"installed.json": `{"installed":{"client_id":"iid","client_secret":"isec"}}`,
		"web.json":       `{"web":{"client_id":"wid","client_secret":"wsec"}}`,
		"bad.json":       "not-json",
	} {
		_ = os.WriteFile(filepath.Join(tmpDir, f), []byte(c), 0o600)
	}
	tests := []struct {
		name, wantErrSub string
		cfg              map[string]any
		opts             provider.InitOptions
	}{
		{"http_missing_block", "requires 'http' config block", map[string]any{"source": "http"}, mkOpts("w", nil, "")},
		{"http_missing_base_url", "requires 'http.base_url'", mkCfg("http", "http", map[string]any{}), mkOpts("w", nil, "")},
		{"http_invalid_url", "failed to create http task adapter", mkCfg("http", "http", map[string]any{"base_url": "ftp://bad"}), mkOpts("w", nil, "")},
		{"http_secrets_success", "", mkCfg("http", "http", map[string]any{"base_url": "https://example.com", "token_env": "TOK"}), mkOpts("w", map[string]string{"TOK": "sec"}, "")},
		{"http_token_env_res", "", mkCfg("http", "http", map[string]any{"base_url": "https://example.com/api", "token_env": "MTOK"}), mkOpts("w", map[string]string{"MTOK": "sec"}, "")},
		{"gtasks_missing_block", "requires 'gtasks' config block", map[string]any{"source": "gtasks"}, mkOpts("w", nil, "")},
		{"gtasks_missing_tasklist", "requires 'gtasks.tasklist_id'", mkCfg("gtasks", "gtasks", map[string]any{}), mkOpts("w", nil, "")},
		{"gtasks_invalid_url", "failed to create gtasks adapter", mkCfg("gtasks", "gtasks", map[string]any{"tasklist_id": "tl", "base_url": "ftp://bad"}), mkOpts("w", nil, "")},
		{"gtasks_secrets_success", "", mkCfg("gtasks", "gtasks", map[string]any{"tasklist_id": "tl", "token_env": "GTOK"}), mkOpts("w", map[string]string{"gtasks.token": "sec"}, "")},
		{"gtasks_oauth_secrets", "", mkCfg("gtasks", "gtasks", map[string]any{"tasklist_id": "tl", "client_id": "cid", "client_secret": "csec", "refresh_token_env": "RTOK", "token_url": "https://x.com/tok"}), mkOpts("w", map[string]string{"RTOK": "rsec"}, "")},
		{"gtasks_creds_file", "", mkCfg("gtasks", "gtasks", map[string]any{"tasklist_id": "tl", "credentials_file": filepath.Join(tmpDir, "creds.json"), "token_url": "https://x.com/tok"}), mkOpts("w", nil, "")},
		{"gtasks_installed_file", "", mkCfg("gtasks", "gtasks", map[string]any{"tasklist_id": "tl", "client_secret_file": filepath.Join(tmpDir, "installed.json"), "refresh_token_env": "RTOK", "token_url": "https://x.com/tok"}), mkOpts("w", map[string]string{"RTOK": "rsec"}, "")},
		{"gtasks_web_file", "", mkCfg("gtasks", "gtasks", map[string]any{"tasklist_id": "tl", "client_secret_file_env": "FENV", "refresh_token": "rtok", "token_url": "https://x.com/tok"}), mkOpts("w", map[string]string{"FENV": filepath.Join(tmpDir, "web.json")}, "")},
		{"gtasks_bad_file_fallback", "", mkCfg("gtasks", "gtasks", map[string]any{"tasklist_id": "tl", "credentials_file": filepath.Join(tmpDir, "bad.json"), "token": "fallback-tok"}), mkOpts("w", nil, "")},
		{"todoist_missing_block", "requires 'todoist' config block", map[string]any{"source": "todoist"}, mkOpts("w", nil, "")},
		{"todoist_missing_token", "requires a valid token", mkCfg("todoist", "todoist", map[string]any{"project_id": "1"}), mkOpts("w", nil, "")},
		{"todoist_inline_token", "", mkCfg("todoist", "todoist", map[string]any{"token": "tok"}), mkOpts("w", nil, "")},
		{"todoist_token_env_sec", "", mkCfg("todoist", "todoist", map[string]any{"token_env": "TTOK"}), mkOpts("w", map[string]string{"TTOK": "tok"}, "")},
		{"todoist_opts_token", "", mkCfg("todoist", "todoist", map[string]any{}), mkOpts("w", nil, "tok")},
		{"todoist_namespaced_secret", "", mkCfg("todoist", "todoist", map[string]any{}), mkOpts("w", map[string]string{"todoist.token": "tok"}, "")},
		{"custom_float_show", "", map[string]any{"show_completed": float64(7)}, mkOpts("w", nil, "")},
		{"custom_str_show", "", map[string]any{"show_completed": "10"}, mkOpts("w", nil, "")},
		{"custom_neg_show", "", map[string]any{"show_completed": -2}, mkOpts("w", nil, "")},
		{"bad_db_path", "failed to initialize tasks", map[string]any{"db_path": "/dev/null/forbidden/path.db"}, mkOpts("w", nil, "")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := provider.NewTasksProvider()
			if _, ok := tc.cfg["db_path"]; !ok {
				tc.cfg["db_path"] = filepath.Join(tmpDir, tc.name+".db")
			}
			err := p.Init(context.Background(), tc.cfg, tc.opts)
			if tc.wantErrSub != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErrSub)) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErrSub, err)
			}
			if tc.wantErrSub == "" && err != nil {
				t.Fatalf("unexpected Init error: %v", err)
			}
			_ = p.Shutdown(context.Background())
		})
	}
}

func TestTasksProvider_Init_Todoist_EnvFallback(t *testing.T) {
	tmpDir := t.TempDir()
	for _, tc := range []struct{ name, key, val string }{
		{"api_token_env", "TODOIST_API_TOKEN", "api-token-from-env"},
		{"token_env", "TODOIST_TOKEN", "token-from-env"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TODOIST_API_TOKEN", "")
			t.Setenv("TODOIST_TOKEN", "")
			t.Setenv(tc.key, tc.val)
			p := provider.NewTasksProvider()
			err := p.Init(context.Background(), map[string]any{"source": "todoist", "db_path": filepath.Join(tmpDir, tc.name+".db"), "todoist": map[string]any{}}, provider.InitOptions{ID: tc.name})
			if err != nil {
				t.Fatalf("Init failed: %v", err)
			}
			_ = p.Shutdown(context.Background())
		})
	}
}

func TestTasksProvider_Fetch(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/projects/p-family":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "p-family", "name": "Family Checklist"})
		case "/api/v1/sections":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "sec-1", "name": "Chores"}})
		case "/api/v1/tasks":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "task-101", "section_id": "sec-1", "content": "Costumes"}})
		case "/api/v1/tasks/completed":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	due := "2026-10-01"
	lCh := mkTaskList("daily-chores", "Daily Chores", "http")
	fakeWithDue := &fakeAdapter{name: "http", list: &lCh, items: []tasks.ListItem{{ID: "t1", ListID: "daily-chores", Title: "Water plants", DueDate: &due}}}
	existingSnap := &tasks.TasksSnapshot{List: mkTaskList("groceries", "Groceries", "local"), Items: []tasks.ListItem{mkTaskItem("item-1", "groceries", "Apples", "Produce", false)}}
	tests := []struct {
		name, wantErrSub, wantListID string
		store                        tasks.Store
		adapter                      adapters.Adapter
		cfg                          map[string]any
		opts                         provider.InitOptions
		skipInit                     bool
		wantItems                    int
	}{
		{"uninitialized", "not initialized", "", nil, nil, nil, provider.InitOptions{}, true, 0},
		{"cold_boot_auto_seed", "", "reminders", &mockTasksStore{getSnapErr: tasks.ErrListNotFound}, nil, map[string]any{}, mkOpts("reminders", nil, ""), false, 0},
		{"cold_boot_upsert_error", "disk full", "", &mockTasksStore{getSnapErr: tasks.ErrListNotFound, upsertErr: errors.New("disk full")}, nil, map[string]any{}, mkOpts("reminders", nil, ""), false, 0},
		{"existing_list", "", "groceries", &mockTasksStore{snapshot: existingSnap}, nil, map[string]any{"show_completed": 5}, mkOpts("groceries", nil, ""), false, 1},
		{"store_corrupt_error", "corrupt db", "", &mockTasksStore{getSnapErr: errors.New("corrupt db")}, nil, map[string]any{}, mkOpts("groceries", nil, ""), false, 0},
		{"adapter_success", "", "daily-chores", nil, fakeWithDue, map[string]any{"list_id": "daily-chores", "source": "http"}, mkOpts("widget-chores", nil, ""), false, 1},
		{"adapter_failure", "upstream timeout", "", nil, &fakeAdapter{name: "http", fetchErr: errors.New("upstream timeout")}, map[string]any{"list_id": "daily-chores"}, mkOpts("widget-chores", nil, ""), false, 0},
		{"store_sync_error", "failed to sync list", "", &mockTasksStore{upsertErr: errors.New("db write failure")}, &fakeAdapter{name: "http", list: &tasks.List{ID: "daily-chores"}, items: []tasks.ListItem{mkTaskItem("t1", "daily-chores", "x", "", false)}}, map[string]any{"list_id": "daily-chores"}, mkOpts("widget-chores", nil, ""), false, 0},
		{"id_normalization", "", "canonical-chores", nil, &fakeAdapter{name: "gtasks", list: &tasks.List{ID: "ext-123", Name: "Ext Title", Source: "gtasks"}, items: []tasks.ListItem{mkTaskItem("task-1", "ext-123", "Clean kitchen", "", false)}}, map[string]any{"list_id": "canonical-chores", "source": "gtasks"}, mkOpts("my-widget", nil, ""), false, 1},
		{"adapter_nil_list", "adapter returned nil list", "", nil, &fakeAdapter{name: "http"}, map[string]any{"list_id": "nil-test", "source": "http"}, mkOpts("my-widget", nil, ""), false, 0},
		{"todoist_integration", "", "family-tasks", nil, nil, map[string]any{"list_id": "family-tasks", "source": "todoist", "todoist": map[string]any{"project_id": "p-family", "token": "mock-token", "base_url": server.URL}}, mkOpts("family-tasks", nil, ""), false, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			p := provider.NewTasksProviderWithAdapter(tc.store, tc.adapter, nil)
			if !tc.skipInit {
				if _, ok := tc.cfg["db_path"]; !ok {
					tc.cfg["db_path"] = filepath.Join(tmpDir, tc.name+".db")
				}
				if err := p.Init(ctx, tc.cfg, tc.opts); err != nil {
					t.Fatalf("Init failed: %v", err)
				}
				defer p.Shutdown(ctx)
			}
			res, err := p.Fetch(ctx)
			if tc.wantErrSub != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErrSub)) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErrSub, err)
			}
			if tc.wantErrSub == "" && err != nil {
				t.Fatalf("unexpected Fetch error: %v", err)
			}
			if tc.wantErrSub != "" {
				return
			}
			snap, ok := res.(*tasks.TasksSnapshot)
			if !ok || snap == nil {
				t.Fatalf("expected *tasks.TasksSnapshot, got %T", res)
			}
			if tc.wantListID != "" && snap.List.ID != tc.wantListID {
				t.Errorf("expected List.ID %q, got %q", tc.wantListID, snap.List.ID)
			}
			for _, itm := range snap.Items {
				if tc.wantListID != "" && itm.ListID != tc.wantListID {
					t.Errorf("expected item ListID %q, got %q", tc.wantListID, itm.ListID)
				}
			}
			if tc.wantItems >= 0 && len(snap.Items) != tc.wantItems {
				t.Errorf("expected %d items, got %d", tc.wantItems, len(snap.Items))
			}
		})
	}
}

func TestTasksProvider_SubscribeAndShutdown(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	mStore := &mockTasksStore{}
	if err := provider.NewTasksProviderWithStore(mStore).Shutdown(context.Background()); err != nil || mStore.closed {
		t.Fatal("expected non-owned store not closed on Shutdown")
	}
	tests := []struct {
		name        string
		preCancel   bool
		readPayload bool
		sinkCap     int
		listID      string
	}{
		{"initial_cancel", true, false, 0, "cancel-list"},
		{"reactive_delivery", false, true, 4, "shared-chores"},
		{"unbuffered_unblock", false, false, 0, "unbuf-list"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			dbPath := filepath.Join(tmpDir, tc.name+".db")
			store, err := tasks.NewSQLiteStore(dbPath)
			if err != nil {
				t.Fatalf("failed to init store: %v", err)
			}
			defer store.Close()

			notifier := tasks.NewChangeNotifier()
			p := provider.NewTasksProviderWithStoreAndNotifier(store, notifier)
			if err := p.Init(ctx, map[string]any{"list_id": tc.listID, "db_path": dbPath}, provider.InitOptions{ID: "w-sub"}); err != nil {
				t.Fatalf("Init failed: %v", err)
			}
			_ = store.UpsertList(ctx, tasks.List{ID: tc.listID, Name: tc.listID})

			eventSink := make(chan provider.WidgetPayload, tc.sinkCap)
			subscribed := make(chan struct{})
			p.SetOnSubscribeForTest(func() { close(subscribed) })

			if tc.preCancel {
				cancel()
			}

			subErrCh := make(chan error, 1)
			go func() { subErrCh <- p.Subscribe(ctx, eventSink) }()

			if !tc.preCancel {
				select {
				case <-subscribed:
				case <-time.After(2 * time.Second):
					t.Fatal("timed out waiting for subscribe")
				}
				_ = store.UpsertItem(ctx, tasks.ListItem{ID: "c1", ListID: tc.listID, Title: "Take out recycling"})
				notifier.NotifyChange(tc.listID)
			}

			if tc.readPayload {
				select {
				case payload := <-eventSink:
					snap, ok := payload.Data.(*tasks.TasksSnapshot)
					if payload.WidgetID != "w-sub" || !ok || len(snap.Items) != 1 || snap.Items[0].Title != "Take out recycling" {
						t.Fatalf("unexpected data in payload: %+v", payload.Data)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("timed out waiting for reactive push on eventSink")
				}
			}

			cancel()

			select {
			case err := <-subErrCh:
				if err != nil {
					t.Fatalf("Subscribe returned error on cancel: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("timed out waiting for Subscribe to return")
			}
		})
	}
}

func TestTasksProvider_CompleteItem(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                                         string
		hasStore, hasAdapter                         bool
		storeCompErr, adapterErr, wantErrIs          error
		itemID, wantErrSub                           string
		wantAdapterCall, wantStoreComp, wantStoreDel int
	}{
		{"nil_store", false, false, nil, nil, nil, "i1", "not initialized", 0, 0, 0},
		{"completer_success", true, true, nil, nil, nil, "item-100", "", 1, 1, 0},
		{"upstream_not_found", true, true, nil, adapters.ErrUpstreamNotFound, tasks.ErrItemNotFound, "item-ghost", "", 1, 0, 1},
		{"upstream_error", true, true, nil, errors.New("upstream timeout"), nil, "item-fail", "upstream task completion failed", 1, 0, 0},
		{"local_store_success", true, false, nil, nil, nil, "item-local", "", 0, 1, 0},
		{"local_store_error", true, false, errors.New("db lock"), nil, nil, "item-err", "failed to complete task in local store", 0, 1, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			var store *mockTasksStore
			if tc.hasStore {
				store = &mockTasksStore{completeErr: tc.storeCompErr}
			}
			var compAdapter *mockCompleterAdapter
			var adapter adapters.Adapter
			if tc.hasAdapter {
				compAdapter = &mockCompleterAdapter{err: tc.adapterErr}
				adapter = compAdapter
			}
			notifier := tasks.NewChangeNotifier()
			ch := make(chan struct{}, 1)
			unsub := notifier.RegisterListener("list-1", ch)
			defer unsub()

			var p *provider.TasksProvider
			if !tc.hasStore {
				p = provider.NewTasksProvider()
			} else {
				p = provider.NewTasksProviderWithAdapter(store, adapter, notifier)
				_ = p.Init(ctx, map[string]any{"list_id": "list-1"}, provider.InitOptions{ID: "w-tasks"})
			}

			err := p.CompleteItem(ctx, tc.itemID)
			if tc.wantErrIs != nil && !errors.Is(err, tc.wantErrIs) {
				t.Fatalf("expected error Is %v, got %v", tc.wantErrIs, err)
			} else if tc.wantErrSub != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErrSub)) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErrSub, err)
			} else if tc.wantErrIs == nil && tc.wantErrSub == "" && err != nil {
				t.Fatalf("unexpected complete error: %v", err)
			}
			if compAdapter != nil && compAdapter.calls != tc.wantAdapterCall {
				t.Errorf("expected %d adapter calls, got %d", tc.wantAdapterCall, compAdapter.calls)
			}
			if store != nil && (store.completeCalls != tc.wantStoreComp || store.deleteCalls != tc.wantStoreDel) {
				t.Errorf("store calls mismatch comp=%d del=%d", store.completeCalls, store.deleteCalls)
			}
		})
	}

	pLocal := provider.NewTasksProviderWithStoreAndNotifier(&mockTasksStore{}, nil)
	_ = pLocal.Init(context.Background(), map[string]any{"list_id": "local-list"}, provider.InitOptions{ID: "w-tasks"})
	if pLocal.ListID() != "local-list" {
		t.Errorf("expected ListID 'local-list', got %q", pLocal.ListID())
	}
}
