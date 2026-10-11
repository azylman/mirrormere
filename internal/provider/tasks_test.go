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
	list            *tasks.List
	items           []tasks.ListItem
	snapshot        *tasks.TasksSnapshot
	getSnapErr      error
	upsertErr       error
	closeErr        error
	closed          bool
	upsertCalls     int
	lastListID      string
	lastShow        int
	completeCalls   int
	lastCompletedID string
	completeErr     error
	deleteCalls     int
	lastDeletedID   string
}

func (m *mockTasksStore) GetList(ctx context.Context, id string) (*tasks.List, error) {
	if m.list != nil && m.list.ID == id {
		return m.list, nil
	}
	return nil, tasks.ErrListNotFound
}

func (m *mockTasksStore) GetListItems(ctx context.Context, listID string, includeDone bool) ([]tasks.ListItem, error) {
	return m.items, nil
}

func (m *mockTasksStore) GetTasksSnapshot(ctx context.Context, listID string, showCompleted int) (*tasks.TasksSnapshot, error) {
	m.lastListID = listID
	m.lastShow = showCompleted
	if m.getSnapErr != nil {
		return nil, m.getSnapErr
	}
	return m.snapshot, nil
}

func (m *mockTasksStore) UpsertList(ctx context.Context, list tasks.List) error {
	m.upsertCalls++
	m.list = &list
	if m.upsertErr != nil {
		return m.upsertErr
	}
	return nil
}

func (m *mockTasksStore) UpsertItem(ctx context.Context, item tasks.ListItem) error {
	return nil
}

func (m *mockTasksStore) SyncList(ctx context.Context, list tasks.List, items []tasks.ListItem) (bool, error) {
	m.list = &list
	m.items = items
	if m.upsertErr != nil {
		return false, m.upsertErr
	}
	return true, nil
}

func (m *mockTasksStore) DeleteList(ctx context.Context, id string) error {
	return nil
}

func (m *mockTasksStore) DeleteItem(ctx context.Context, listID, itemID string) error {
	m.deleteCalls++
	m.lastDeletedID = itemID
	return nil
}

func (m *mockTasksStore) CompleteItem(ctx context.Context, listID, itemID string) error {
	m.completeCalls++
	m.lastCompletedID = itemID
	if m.completeErr != nil {
		return m.completeErr
	}
	return nil
}

func (m *mockTasksStore) Close() error {
	m.closed = true
	return m.closeErr
}

func TestTasksProvider_Registry(t *testing.T) {
	t.Parallel()

	reg := provider.NewRegistry()
	if !reg.Has("tasks") {
		t.Fatal("expected registry to have 'tasks' provider")
	}

	p, err := reg.Create("tasks")
	if err != nil {
		t.Fatalf("failed to create 'tasks' provider: %v", err)
	}
	if p == nil {
		t.Fatal("created provider is nil")
	}
}

func TestTasksProvider_Init_DefaultsAndCustom(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "custom.db")

	// Case 1: Custom config with string & float show_completed
	p := provider.NewTasksProvider()
	ctx := context.Background()

	cfg := map[string]any{
		"list_id":        "groceries",
		"list_name":      "Family Groceries",
		"source":         "custom",
		"show_completed": 5,
		"db_path":        dbPath,
	}
	opts := provider.InitOptions{ID: "widget-tasks-1"}

	if err := p.Init(ctx, cfg, opts); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Fetch should succeed and auto-seed since DB is newly created and empty
	val, err := p.Fetch(ctx)
	if err != nil {
		t.Fatalf("Fetch on new DB failed: %v", err)
	}
	snap, ok := val.(*tasks.TasksSnapshot)
	if !ok {
		t.Fatalf("expected *tasks.TasksSnapshot, got %T", val)
	}
	if snap.List.ID != "groceries" || snap.List.Name != "Family Groceries" || snap.List.Source != "custom" {
		t.Errorf("list mismatch: %+v", snap.List)
	}
	if len(snap.Items) != 0 {
		t.Errorf("expected 0 items on empty list, got %d", len(snap.Items))
	}

	if err := p.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown failed: %v", err)
	}

	// Case 2: Config with float64 show_completed
	pFloat := provider.NewTasksProvider()
	cfgFloat := map[string]any{
		"db_path":        dbPath,
		"show_completed": float64(7),
	}
	if err := pFloat.Init(ctx, cfgFloat, opts); err != nil {
		t.Fatalf("Init with float64 failed: %v", err)
	}
	_ = pFloat.Shutdown(ctx)

	// Case 3: Config with string show_completed
	pStr := provider.NewTasksProvider()
	cfgStr := map[string]any{
		"db_path":        dbPath,
		"show_completed": "10",
	}
	if err := pStr.Init(ctx, cfgStr, opts); err != nil {
		t.Fatalf("Init with string failed: %v", err)
	}
	_ = pStr.Shutdown(ctx)

	// Case 4: Config with negative show_completed clamped to 0
	pNeg := provider.NewTasksProvider()
	cfgNeg := map[string]any{
		"db_path":        dbPath,
		"show_completed": -2,
	}
	if err := pNeg.Init(ctx, cfgNeg, opts); err != nil {
		t.Fatalf("Init with negative failed: %v", err)
	}
	_ = pNeg.Shutdown(ctx)

	// Case 5: Invalid db_path directory returns error
	pBad := provider.NewTasksProvider()
	cfgBad := map[string]any{
		"db_path": "/dev/null/forbidden/path.db",
	}
	if err := pBad.Init(ctx, cfgBad, opts); err == nil {
		t.Fatal("expected error for invalid db_path directory, got nil")
	}
}

func TestTasksProvider_Fetch_Uninitialized(t *testing.T) {
	t.Parallel()

	p := provider.NewTasksProvider()
	_, err := p.Fetch(context.Background())
	if err == nil {
		t.Fatal("expected error when Fetching uninitialized provider, got nil")
	}
}

func TestTasksProvider_Fetch_ColdBootAutoSeed(t *testing.T) {
	t.Parallel()

	mock := &mockTasksStore{
		getSnapErr: tasks.ErrListNotFound,
	}
	p := provider.NewTasksProviderWithStore(mock)

	ctx := context.Background()
	opts := provider.InitOptions{ID: "reminders"}
	if err := p.Init(ctx, map[string]any{}, opts); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	val, err := p.Fetch(ctx)
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}

	snap, ok := val.(*tasks.TasksSnapshot)
	if !ok {
		t.Fatalf("expected *tasks.TasksSnapshot, got %T", val)
	}
	if snap.List.ID != "reminders" || snap.List.Name != "reminders" {
		t.Errorf("expected list ID & Name 'reminders', got %+v", snap.List)
	}
	if mock.upsertCalls != 1 {
		t.Errorf("expected 1 UpsertList call, got %d", mock.upsertCalls)
	}

	// Case: Auto-seed upsert error
	mockBad := &mockTasksStore{
		getSnapErr: tasks.ErrListNotFound,
		upsertErr:  errors.New("disk full"),
	}
	pBad := provider.NewTasksProviderWithStore(mockBad)
	_ = pBad.Init(ctx, map[string]any{}, opts)
	if _, err := pBad.Fetch(ctx); err == nil {
		t.Fatal("expected error when upsert fails during auto-seed, got nil")
	}
}

func TestTasksProvider_Fetch_ExistingList(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	expectedSnap := &tasks.TasksSnapshot{
		List: tasks.List{
			ID:        "groceries",
			Name:      "Groceries",
			Source:    "local",
			Sections:  []string{"Produce"},
			CreatedAt: now,
			UpdatedAt: now,
		},
		Items: []tasks.ListItem{
			{
				ID:        "item-1",
				ListID:    "groceries",
				Title:     "Apples",
				Done:      false,
				Section:   "Produce",
				Position:  0,
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
	}

	mock := &mockTasksStore{
		snapshot: expectedSnap,
	}
	p := provider.NewTasksProviderWithStore(mock)

	ctx := context.Background()
	opts := provider.InitOptions{ID: "groceries"}
	_ = p.Init(ctx, map[string]any{"show_completed": 5}, opts)

	val, err := p.Fetch(ctx)
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}
	snap := val.(*tasks.TasksSnapshot)
	if len(snap.Items) != 1 || snap.Items[0].Title != "Apples" {
		t.Errorf("snapshot mismatch: %+v", snap)
	}
	if mock.lastShow != 5 {
		t.Errorf("expected showCompleted 5, got %d", mock.lastShow)
	}
}

func TestTasksProvider_Fetch_StoreError(t *testing.T) {
	t.Parallel()

	mock := &mockTasksStore{
		getSnapErr: errors.New("corrupt db"),
	}
	p := provider.NewTasksProviderWithStore(mock)

	ctx := context.Background()
	opts := provider.InitOptions{ID: "groceries"}
	_ = p.Init(ctx, map[string]any{}, opts)

	if _, err := p.Fetch(ctx); err == nil {
		t.Fatal("expected error when store returns corrupt db error, got nil")
	}
}

func TestTasksProvider_SubscribeAndShutdown(t *testing.T) {
	t.Parallel()

	mock := &mockTasksStore{}
	p := provider.NewTasksProviderWithStore(mock)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.Subscribe(ctx, nil); err != nil {
		t.Errorf("Subscribe should return nil, got %v", err)
	}

	// Non-owned store is NOT closed on Shutdown
	if err := p.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown should return nil, got %v", err)
	}
	if mock.closed {
		t.Error("expected non-owned store not to be closed")
	}
}

func TestTasksProvider_Init_AdapterValidation(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "val.db")
	opts := provider.InitOptions{ID: "w-tasks"}

	// 1. source: "http" missing http block
	p1 := provider.NewTasksProvider()
	err := p1.Init(context.Background(), map[string]any{
		"source":  "http",
		"db_path": dbPath,
	}, opts)
	if err == nil || !strings.Contains(err.Error(), "requires 'http' config block") {
		t.Fatalf("expected missing http block error, got %v", err)
	}

	// 2. source: "http" missing base_url
	p2 := provider.NewTasksProvider()
	err = p2.Init(context.Background(), map[string]any{
		"source":  "http",
		"db_path": dbPath,
		"http":    map[string]any{},
	}, opts)
	if err == nil || !strings.Contains(err.Error(), "requires 'http.base_url'") {
		t.Fatalf("expected missing base_url error, got %v", err)
	}

	// 3. source: "gtasks" missing gtasks block
	p3 := provider.NewTasksProvider()
	err = p3.Init(context.Background(), map[string]any{
		"source":  "gtasks",
		"db_path": dbPath,
	}, opts)
	if err == nil || !strings.Contains(err.Error(), "requires 'gtasks' config block") {
		t.Fatalf("expected missing gtasks block error, got %v", err)
	}

	// 4. source: "gtasks" missing tasklist_id
	p4 := provider.NewTasksProvider()
	err = p4.Init(context.Background(), map[string]any{
		"source":  "gtasks",
		"db_path": dbPath,
		"gtasks":  map[string]any{},
	}, opts)
	if err == nil || !strings.Contains(err.Error(), "requires 'gtasks.tasklist_id'") {
		t.Fatalf("expected missing tasklist_id error, got %v", err)
	}

	// 5. source: "http" with invalid URL returns error
	p5 := provider.NewTasksProvider()
	err = p5.Init(context.Background(), map[string]any{
		"source":  "http",
		"db_path": dbPath,
		"http": map[string]any{
			"base_url": "ftp://invalid-scheme",
		},
	}, opts)
	if err == nil || !strings.Contains(err.Error(), "failed to create http task adapter") {
		t.Fatalf("expected error for invalid http base_url, got %v", err)
	}

	// 6. source: "gtasks" with invalid base_url returns error
	p6 := provider.NewTasksProvider()
	err = p6.Init(context.Background(), map[string]any{
		"source":  "gtasks",
		"db_path": dbPath,
		"gtasks": map[string]any{
			"tasklist_id": "tl-1",
			"base_url":    "ftp://invalid-scheme",
		},
	}, opts)
	if err == nil || !strings.Contains(err.Error(), "failed to create gtasks adapter") {
		t.Fatalf("expected error for invalid gtasks base_url, got %v", err)
	}

	// 7. Successful HTTP adapter creation with secret resolution
	optsSecrets := provider.InitOptions{
		ID: "http-tasks",
		Secrets: map[string]string{
			"http.token": "http-secret-token",
		},
	}
	pHTTP := provider.NewTasksProvider()
	if err := pHTTP.Init(context.Background(), map[string]any{
		"source":  "http",
		"db_path": dbPath,
		"http": map[string]any{
			"base_url":  "https://example.com/api/tasks",
			"token_env": "HTTP_TOKEN",
		},
	}, optsSecrets); err != nil {
		t.Fatalf("Init with http adapter failed: %v", err)
	}
	_ = pHTTP.Shutdown(context.Background())

	// 8. Successful GTasks adapter creation with secret resolution and top-level token fallback
	optsGTasks := provider.InitOptions{
		ID: "gtasks-widget",
		Secrets: map[string]string{
			"gtasks.token": "gtasks-secret-token",
		},
	}
	pGTasks := provider.NewTasksProvider()
	if err := pGTasks.Init(context.Background(), map[string]any{
		"source":  "gtasks",
		"db_path": dbPath,
		"gtasks": map[string]any{
			"tasklist_id": "MDk3...",
			"token_env":   "GTASKS_TOKEN",
		},
	}, optsGTasks); err != nil {
		t.Fatalf("Init with gtasks adapter failed: %v", err)
	}
	_ = pGTasks.Shutdown(context.Background())

	// 9. Constructors with nil notifiers fallback to DefaultChangeNotifier
	pNil1 := provider.NewTasksProviderWithStoreAndNotifier(nil, nil)
	if pNil1 == nil {
		t.Fatal("expected non-nil provider")
	}
	pNil2 := provider.NewTasksProviderWithAdapter(nil, nil, nil)
	if pNil2 == nil {
		t.Fatal("expected non-nil provider")
	}
}

type fakeAdapter struct {
	name     string
	list     *tasks.List
	items    []tasks.ListItem
	fetchErr error
}

func (f *fakeAdapter) Name() string { return f.name }
func (f *fakeAdapter) FetchList(ctx context.Context) (*tasks.List, []tasks.ListItem, error) {
	if f.fetchErr != nil {
		return nil, nil, f.fetchErr
	}
	return f.list, f.items, nil
}

func TestTasksProvider_Fetch_WithAdapter(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "adapter_test.db")
	store, err := tasks.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// 1. Successful HTTP Adapter Ingestion
	due := "2026-10-01"
	fake := &fakeAdapter{
		name: "http",
		list: &tasks.List{
			ID:     "daily-chores",
			Name:   "Daily Chores",
			Source: "http",
		},
		items: []tasks.ListItem{
			{
				ID:       "t1",
				ListID:   "daily-chores",
				Title:    "Water plants",
				Done:     false,
				DueDate:  &due,
				Position: 0,
			},
		},
	}

	notifier := tasks.NewChangeNotifier()
	p := provider.NewTasksProviderWithAdapter(store, fake, notifier)
	opts := provider.InitOptions{ID: "widget-chores"}
	if err := p.Init(ctx, map[string]any{
		"list_id": "daily-chores",
		"source":  "http",
		"db_path": dbPath,
	}, opts); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	val, err := p.Fetch(ctx)
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}
	snap, ok := val.(*tasks.TasksSnapshot)
	if !ok {
		t.Fatalf("expected *tasks.TasksSnapshot, got %T", val)
	}
	if snap.List.ID != "daily-chores" || len(snap.Items) != 1 || snap.Items[0].Title != "Water plants" {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}

	// 2. Adapter failure returns error (for coordinator SWR cache degraded state)
	fake.fetchErr = errors.New("upstream timeout")
	_, err = p.Fetch(ctx)
	if err == nil || !strings.Contains(err.Error(), "upstream timeout") {
		t.Fatalf("expected error from Fetch on upstream failure, got %v", err)
	}

	// 3. Store SyncList error returns error
	mockErrStore := &mockTasksStore{
		upsertErr: errors.New("db write failure"),
	}
	pSyncErr := provider.NewTasksProviderWithAdapter(mockErrStore, fake, notifier)
	_ = pSyncErr.Init(ctx, map[string]any{"list_id": "daily-chores"}, opts)
	fake.fetchErr = nil
	if _, err := pSyncErr.Fetch(ctx); err == nil || !strings.Contains(err.Error(), "failed to sync list") {
		t.Fatalf("expected failed to sync list error, got %v", err)
	}
}

func TestTasksProvider_Subscribe_ReactiveNotifications(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "sub_test.db")
	store, err := tasks.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	defer store.Close()

	notifier := tasks.NewChangeNotifier()

	// Consumer provider representing secondary compact-chores widget
	pConsumer := provider.NewTasksProviderWithStoreAndNotifier(store, notifier)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	optsConsumer := provider.InitOptions{ID: "compact-chores"}
	if err := pConsumer.Init(ctx, map[string]any{
		"list_id":        "shared-chores",
		"show_completed": 0,
		"db_path":        dbPath,
	}, optsConsumer); err != nil {
		t.Fatalf("consumer Init failed: %v", err)
	}

	eventSink := make(chan provider.WidgetPayload, 4)
	subErrCh := make(chan error, 1)
	subscribed := make(chan struct{})

	pConsumer.SetOnSubscribeForTest(func() {
		close(subscribed)
	})

	go func() {
		subErrCh <- pConsumer.Subscribe(ctx, eventSink)
	}()

	select {
	case <-subscribed:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for consumer subscription")
	}

	// Primary provider ingests items
	fake := &fakeAdapter{
		name: "gtasks",
		list: &tasks.List{
			ID:     "shared-chores",
			Name:   "Shared Household Chores",
			Source: "gtasks",
		},
		items: []tasks.ListItem{
			{
				ID:       "c1",
				ListID:   "shared-chores",
				Title:    "Take out recycling",
				Done:     false,
				Position: 0,
			},
		},
	}
	pPrimary := provider.NewTasksProviderWithAdapter(store, fake, notifier)
	optsPrimary := provider.InitOptions{ID: "full-chores"}
	if err := pPrimary.Init(ctx, map[string]any{
		"list_id": "shared-chores",
		"source":  "gtasks",
		"db_path": dbPath,
	}, optsPrimary); err != nil {
		t.Fatalf("primary Init failed: %v", err)
	}

	// Fetch on primary provider syncs to DB and fires notifier
	if _, err := pPrimary.Fetch(ctx); err != nil {
		t.Fatalf("primary Fetch failed: %v", err)
	}

	// Consumer widget should immediately receive reactive update via eventSink
	select {
	case payload := <-eventSink:
		if payload.WidgetID != "compact-chores" {
			t.Errorf("expected WidgetID 'compact-chores', got %q", payload.WidgetID)
		}
		snap, ok := payload.Data.(*tasks.TasksSnapshot)
		if !ok || len(snap.Items) != 1 || snap.Items[0].Title != "Take out recycling" {
			t.Fatalf("unexpected data in payload: %+v", payload.Data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for reactive push on eventSink")
	}

	// Cancel context to terminate Subscribe cleanly
	cancel()
	select {
	case err := <-subErrCh:
		if err != nil {
			t.Fatalf("Subscribe returned error on cancel: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Subscribe to return")
	}
}

func TestTasksProvider_Fetch_IDNormalizationAndNilList(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "id_norm.db")
	store, err := tasks.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	defer store.Close()

	// 1. Adapter returns different list.ID than cfg.ListID
	fake := &fakeAdapter{
		name: "gtasks",
		list: &tasks.List{
			ID:     "external-google-id-123",
			Name:   "Google List Title",
			Source: "gtasks",
		},
		items: []tasks.ListItem{
			{
				ID:     "task-1",
				ListID: "external-google-id-123",
				Title:  "Clean kitchen",
			},
		},
	}
	p := provider.NewTasksProviderWithAdapter(store, fake, nil)
	opts := provider.InitOptions{ID: "my-widget"}
	if err := p.Init(ctx, map[string]any{
		"list_id": "canonical-chores",
		"source":  "gtasks",
		"db_path": dbPath,
	}, opts); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	rawSnap, err := p.Fetch(ctx)
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}
	snap, ok := rawSnap.(*tasks.TasksSnapshot)
	if !ok || snap == nil {
		t.Fatalf("expected *tasks.TasksSnapshot, got %T", rawSnap)
	}
	if snap.List.ID != "canonical-chores" {
		t.Errorf("expected normalized list ID 'canonical-chores', got %q", snap.List.ID)
	}
	if len(snap.Items) != 1 || snap.Items[0].ListID != "canonical-chores" {
		t.Errorf("expected normalized item list ID 'canonical-chores', got %+v", snap.Items)
	}

	// 2. Adapter returns nil list
	fakeNil := &fakeAdapter{
		name:  "http",
		list:  nil,
		items: nil,
	}
	pNil := provider.NewTasksProviderWithAdapter(store, fakeNil, nil)
	if err := pNil.Init(ctx, map[string]any{
		"list_id": "nil-test",
		"source":  "http",
		"db_path": dbPath,
	}, opts); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	_, err = pNil.Fetch(ctx)
	if err == nil || !strings.Contains(err.Error(), "adapter returned nil list") {
		t.Fatalf("expected 'adapter returned nil list' error, got %v", err)
	}
}

func TestTasksProvider_Init_TokenEnv_SecretResolution(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// 1. HTTP with token_env resolved from opts.Secrets
	pHTTP := provider.NewTasksProvider()
	optsHTTP := provider.InitOptions{
		ID: "http-widget",
		Secrets: map[string]string{
			"MY_HTTP_TOKEN": "vault-http-secret",
		},
	}
	err := pHTTP.Init(ctx, map[string]any{
		"source":  "http",
		"db_path": filepath.Join(t.TempDir(), "http_token.db"),
		"http": map[string]any{
			"base_url":  "https://example.com/api/tasks",
			"token_env": "MY_HTTP_TOKEN",
		},
	}, optsHTTP)
	if err != nil {
		t.Fatalf("HTTP Init with token_env in secrets failed: %v", err)
	}

	// 2. GTasks with token_env resolved from opts.Secrets
	pGTasks := provider.NewTasksProvider()
	optsGTasks := provider.InitOptions{
		ID: "gtasks-widget",
		Secrets: map[string]string{
			"MY_GTASKS_TOKEN": "vault-gtasks-secret",
		},
	}
	err = pGTasks.Init(ctx, map[string]any{
		"source":  "gtasks",
		"db_path": filepath.Join(t.TempDir(), "gtasks_token.db"),
		"gtasks": map[string]any{
			"tasklist_id": "list-abc",
			"token_env":   "MY_GTASKS_TOKEN",
		},
	}, optsGTasks)
	if err != nil {
		t.Fatalf("GTasks Init with token_env in secrets failed: %v", err)
	}

	// 3. GTasks with OAuth credentials resolved from config and secrets
	pOAuth := provider.NewTasksProvider()
	optsOAuth := provider.InitOptions{
		ID: "gtasks-oauth-widget",
		Secrets: map[string]string{
			"G_REFRESH": "vault-refresh-token",
		},
	}
	err = pOAuth.Init(ctx, map[string]any{
		"source":  "gtasks",
		"db_path": filepath.Join(t.TempDir(), "gtasks_oauth.db"),
		"gtasks": map[string]any{
			"tasklist_id":       "list-oauth",
			"client_id":         "my-client-id",
			"client_secret":     "my-client-secret",
			"refresh_token_env": "G_REFRESH",
			"token_url":         "https://example.com/oauth/token",
		},
	}, optsOAuth)
	if err != nil {
		t.Fatalf("GTasks Init with OAuth credentials failed: %v", err)
	}
}

func TestTasksProvider_Init_GTasks_CredentialsFile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// 1. Authorized user credentials JSON file
	credsFile := filepath.Join(t.TempDir(), "authorized_user.json")
	credsContent := `{"client_id":"file-client-id","client_secret":"file-client-secret","refresh_token":"file-refresh-token","token":"file-token"}`
	if err := os.WriteFile(credsFile, []byte(credsContent), 0o600); err != nil {
		t.Fatalf("failed to write test creds: %v", err)
	}

	p := provider.NewTasksProvider()
	opts := provider.InitOptions{
		ID: "gtasks-file-widget",
	}
	err := p.Init(ctx, map[string]any{
		"source":  "gtasks",
		"db_path": filepath.Join(t.TempDir(), "gtasks_file.db"),
		"gtasks": map[string]any{
			"tasklist_id":      "list-file",
			"credentials_file": credsFile,
			"token_url":        "https://example.com/oauth/token",
		},
	}, opts)
	if err != nil {
		t.Fatalf("GTasks Init with credentials_file failed: %v", err)
	}

	// 2. Client secret file with "installed" Google OAuth format + env refresh token
	secretFile := filepath.Join(t.TempDir(), "client_secret.json")
	secretContent := `{"installed":{"client_id":"installed-id","client_secret":"installed-secret"}}`
	if err := os.WriteFile(secretFile, []byte(secretContent), 0o600); err != nil {
		t.Fatalf("failed to write test secret: %v", err)
	}

	pInstalled := provider.NewTasksProvider()
	optsInstalled := provider.InitOptions{
		ID: "gtasks-installed-widget",
		Secrets: map[string]string{
			"G_REFRESH": "vault-installed-refresh",
		},
	}
	err = pInstalled.Init(ctx, map[string]any{
		"source":  "gtasks",
		"db_path": filepath.Join(t.TempDir(), "gtasks_installed.db"),
		"gtasks": map[string]any{
			"tasklist_id":        "list-installed",
			"client_secret_file": secretFile,
			"refresh_token_env":  "G_REFRESH",
			"token_url":          "https://example.com/oauth/token",
		},
	}, optsInstalled)
	if err != nil {
		t.Fatalf("GTasks Init with client_secret_file failed: %v", err)
	}

	// 3. Client secret file with "web" Google OAuth format
	webSecretFile := filepath.Join(t.TempDir(), "web_secret.json")
	webContent := `{"web":{"client_id":"web-id","client_secret":"web-secret"}}`
	if err := os.WriteFile(webSecretFile, []byte(webContent), 0o600); err != nil {
		t.Fatalf("failed to write test web secret: %v", err)
	}

	pWeb := provider.NewTasksProvider()
	optsWeb := provider.InitOptions{
		ID: "gtasks-web-widget",
		Secrets: map[string]string{
			"MY_REFRESH_FILE": webSecretFile,
		},
	}
	err = pWeb.Init(ctx, map[string]any{
		"source":  "gtasks",
		"db_path": filepath.Join(t.TempDir(), "gtasks_web.db"),
		"gtasks": map[string]any{
			"tasklist_id":            "list-web",
			"client_secret_file_env": "MY_REFRESH_FILE",
			"refresh_token":          "web-refresh",
			"token_url":              "https://example.com/oauth/token",
		},
	}, optsWeb)
	if err != nil {
		t.Fatalf("GTasks Init with client_secret_file_env failed: %v", err)
	}

	// 4. Non-existent and malformed files are ignored gracefully
	pBad := provider.NewTasksProvider()
	badFile := filepath.Join(t.TempDir(), "bad.json")
	_ = os.WriteFile(badFile, []byte("not-json"), 0o600)
	err = pBad.Init(ctx, map[string]any{
		"source":  "gtasks",
		"db_path": filepath.Join(t.TempDir(), "gtasks_bad.db"),
		"gtasks": map[string]any{
			"tasklist_id":      "list-bad",
			"credentials_file": badFile,
			"token":            "fallback-token",
		},
	}, provider.InitOptions{ID: "gtasks-bad"})
	if err != nil {
		t.Fatalf("GTasks Init with bad file should fall back to explicit token: %v", err)
	}
}

func TestTasksProvider_Subscribe_CancelledContextUnblocking(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "cancel.db")
	store, err := tasks.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	defer store.Close()

	ctx, cancel := context.WithCancel(context.Background())
	notifier := tasks.NewChangeNotifier()
	p := provider.NewTasksProviderWithAdapter(store, nil, notifier)
	_ = p.Init(ctx, map[string]any{
		"list_id": "cancel-list",
		"db_path": dbPath,
	}, provider.InitOptions{ID: "cancel-widget"})

	// Seed list
	_ = store.UpsertList(ctx, tasks.List{ID: "cancel-list", Name: "Cancel List"})

	// Unbuffered eventSink that nobody reads from
	eventSink := make(chan provider.WidgetPayload)

	subDone := make(chan error, 1)
	go func() {
		subDone <- p.Subscribe(ctx, eventSink)
	}()

	// Fire change notification while eventSink has no reader
	notifier.NotifyChange("cancel-list")

	// Cancel context immediately
	cancel()

	select {
	case err := <-subDone:
		if err != nil {
			t.Errorf("expected clean exit from Subscribe on cancel, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Subscribe deadlocked on cancelled context with unread eventSink")
	}
}

func TestTasksProvider_Init_TodoistValidation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	p := provider.NewTasksProvider()

	// 1. Missing todoist config block
	err := p.Init(ctx, map[string]any{
		"source": "todoist",
	}, provider.InitOptions{ID: "w-todoist-bad"})
	if err == nil || !strings.Contains(err.Error(), "requires 'todoist' config block") {
		t.Fatalf("expected error for missing todoist block, got %v", err)
	}

	// 2. Missing token
	err = p.Init(ctx, map[string]any{
		"source":  "todoist",
		"todoist": map[string]any{"project_id": "123"},
	}, provider.InitOptions{ID: "w-todoist-no-tok"})
	if err == nil || !strings.Contains(err.Error(), "requires a valid token") {
		t.Fatalf("expected error for missing token, got %v", err)
	}
}

func TestTasksProvider_Init_Todoist_TokenResolution(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// 1. Token in inline config
	p1 := provider.NewTasksProvider()
	err := p1.Init(ctx, map[string]any{
		"source":  "todoist",
		"db_path": filepath.Join(t.TempDir(), "t1.db"),
		"todoist": map[string]any{
			"token": "inline-tok",
		},
	}, provider.InitOptions{ID: "w1"})
	if err != nil {
		t.Fatalf("expected success with inline token: %v", err)
	}
	defer p1.Shutdown(ctx)

	// 2. Token in token_env via opts.Secrets
	p2 := provider.NewTasksProvider()
	err = p2.Init(ctx, map[string]any{
		"source":  "todoist",
		"db_path": filepath.Join(t.TempDir(), "t2.db"),
		"todoist": map[string]any{
			"token_env": "CUSTOM_TODOIST_KEY",
		},
	}, provider.InitOptions{
		ID: "w2",
		Secrets: map[string]string{
			"CUSTOM_TODOIST_KEY": "env-secret-tok",
		},
	})
	if err != nil {
		t.Fatalf("expected success with token_env in secrets: %v", err)
	}
	defer p2.Shutdown(ctx)

	// 3. Token in opts.Token
	p3 := provider.NewTasksProvider()
	err = p3.Init(ctx, map[string]any{
		"source":  "todoist",
		"db_path": filepath.Join(t.TempDir(), "t3.db"),
		"todoist": map[string]any{},
	}, provider.InitOptions{
		ID:    "w3",
		Token: "opts-token",
	})
	if err != nil {
		t.Fatalf("expected success with opts.Token: %v", err)
	}
	defer p3.Shutdown(ctx)

	// 4. Token in opts.Secrets["todoist.token"]
	p4 := provider.NewTasksProvider()
	err = p4.Init(ctx, map[string]any{
		"source":  "todoist",
		"db_path": filepath.Join(t.TempDir(), "t4.db"),
		"todoist": map[string]any{},
	}, provider.InitOptions{
		ID: "w4",
		Secrets: map[string]string{
			"todoist.token": "namespaced-secret-tok",
		},
	})
	if err != nil {
		t.Fatalf("expected success with todoist.token secret: %v", err)
	}
	defer p4.Shutdown(ctx)
}

func TestTasksProvider_Init_Todoist_EnvFallback(t *testing.T) {
	ctx := context.Background()

	// 1. TODOIST_API_TOKEN environment variable fallback
	t.Setenv("TODOIST_API_TOKEN", "api-token-from-env")
	pEnv := provider.NewTasksProvider()
	err := pEnv.Init(ctx, map[string]any{
		"source":  "todoist",
		"db_path": filepath.Join(t.TempDir(), "env1.db"),
		"todoist": map[string]any{},
	}, provider.InitOptions{ID: "w-env1"})
	if err != nil {
		t.Fatalf("expected success with TODOIST_API_TOKEN in env: %v", err)
	}
	defer pEnv.Shutdown(ctx)

	// 2. TODOIST_TOKEN environment variable fallback
	t.Setenv("TODOIST_API_TOKEN", "")
	t.Setenv("TODOIST_TOKEN", "token-from-env")
	pEnv2 := provider.NewTasksProvider()
	err = pEnv2.Init(ctx, map[string]any{
		"source":  "todoist",
		"db_path": filepath.Join(t.TempDir(), "env2.db"),
		"todoist": map[string]any{},
	}, provider.InitOptions{ID: "w-env2"})
	if err != nil {
		t.Fatalf("expected success with TODOIST_TOKEN in env: %v", err)
	}
	defer pEnv2.Shutdown(ctx)
}

func TestTasksProvider_Fetch_TodoistIntegration(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/projects/p-family":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":   "p-family",
				"name": "Family Checklist",
			})
		case "/api/v1/sections":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": "sec-1", "name": "Chores", "order": 1},
			})
		case "/api/v1/tasks":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"id":         "task-101",
					"section_id": "sec-1",
					"content":    "Figure out halloween costumes",
					"order":      1,
				},
			})
		case "/api/v1/tasks/completed":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ctx := context.Background()
	p := provider.NewTasksProvider()
	dbPath := filepath.Join(t.TempDir(), "integration.db")

	err := p.Init(ctx, map[string]any{
		"list_id": "family-tasks",
		"source":  "todoist",
		"db_path": dbPath,
		"todoist": map[string]any{
			"project_id": "p-family",
			"token":      "mock-bearer-token",
			"base_url":   server.URL,
		},
	}, provider.InitOptions{ID: "family-tasks"})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer p.Shutdown(ctx)

	res, err := p.Fetch(ctx)
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}

	snap, ok := res.(*tasks.TasksSnapshot)
	if !ok || snap == nil {
		t.Fatalf("expected *tasks.TasksSnapshot, got %T", res)
	}

	if snap.List.ID != "family-tasks" {
		t.Errorf("expected list ID 'family-tasks', got %q", snap.List.ID)
	}
	if snap.List.Name != "Family Checklist" {
		t.Errorf("expected list name 'Family Checklist', got %q", snap.List.Name)
	}
	if len(snap.Items) != 1 {
		t.Fatalf("expected 1 task item, got %d", len(snap.Items))
	}
	if snap.Items[0].Title != "Figure out halloween costumes" {
		t.Errorf("unexpected task title: %q", snap.Items[0].Title)
	}
	if snap.Items[0].Section != "Chores" {
		t.Errorf("expected Section 'Chores', got %q", snap.Items[0].Section)
	}
}




type mockCompleterAdapter struct {
	name        string
	calls       int
	lastItem    string
	err         error
	fetchListFn func(ctx context.Context) (*tasks.List, []tasks.ListItem, error)
}

func (m *mockCompleterAdapter) Name() string {
	if m.name != "" {
		return m.name
	}
	return "mock-completer"
}

func (m *mockCompleterAdapter) FetchList(ctx context.Context) (*tasks.List, []tasks.ListItem, error) {
	if m.fetchListFn != nil {
		return m.fetchListFn(ctx)
	}
	return &tasks.List{ID: "test"}, nil, nil
}

func (m *mockCompleterAdapter) CompleteItem(ctx context.Context, itemID string) error {
	m.calls++
	m.lastItem = itemID
	return m.err
}

func TestTasksProvider_CompleteItem(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Case 1: Nil store returns error
	pNil := provider.NewTasksProvider()
	if err := pNil.CompleteItem(ctx, "i1"); err == nil {
		t.Fatal("expected error with nil store")
	}

	// Case 2: Adapter with Completer succeeds
	adapter := &mockCompleterAdapter{}
	store := &mockTasksStore{}
	notifier := tasks.NewChangeNotifier()

	ch := make(chan struct{}, 1)
	unsub := notifier.RegisterListener("list-1", ch)
	defer unsub()

	p := provider.NewTasksProviderWithAdapter(store, adapter, notifier)
	_ = p.Init(ctx, map[string]any{"list_id": "list-1", "source": "todoist"}, provider.InitOptions{ID: "w-tasks"})

	if err := p.CompleteItem(ctx, "item-100"); err != nil {
		t.Fatalf("unexpected complete error: %v", err)
	}
	if adapter.calls != 1 || adapter.lastItem != "item-100" {
		t.Errorf("expected 1 adapter call with item-100, got %d, %s", adapter.calls, adapter.lastItem)
	}
	if store.completeCalls != 1 || store.lastCompletedID != "item-100" {
		t.Errorf("expected 1 store complete call with item-100, got %d, %s", store.completeCalls, store.lastCompletedID)
	}
	select {
	case <-ch:
		// Notified successfully
	default:
		t.Error("expected change notification on completion")
	}

	// Case 3: Adapter returns ErrUpstreamNotFound -> purges item from store and returns tasks.ErrItemNotFound
	adapter404 := &mockCompleterAdapter{err: adapters.ErrUpstreamNotFound}
	store404 := &mockTasksStore{}
	p404 := provider.NewTasksProviderWithAdapter(store404, adapter404, notifier)
	_ = p404.Init(ctx, map[string]any{"list_id": "list-1"}, provider.InitOptions{ID: "w-tasks"})

	if err := p404.CompleteItem(ctx, "item-ghost"); !errors.Is(err, tasks.ErrItemNotFound) {
		t.Fatalf("expected ErrItemNotFound, got: %v", err)
	}
	if store404.deleteCalls != 1 || store404.lastDeletedID != "item-ghost" {
		t.Errorf("expected ghost item to be deleted from store, got calls=%d", store404.deleteCalls)
	}
	if store404.completeCalls != 0 {
		t.Errorf("expected 0 store complete calls on 404, got %d", store404.completeCalls)
	}

	// Case 4: Adapter returns other upstream error -> aborts without store modification
	adapterErr := &mockCompleterAdapter{err: errors.New("upstream timeout")}
	storeErr := &mockTasksStore{}
	pErr := provider.NewTasksProviderWithAdapter(storeErr, adapterErr, notifier)
	_ = pErr.Init(ctx, map[string]any{"list_id": "list-1"}, provider.InitOptions{ID: "w-tasks"})

	if err := pErr.CompleteItem(ctx, "item-fail"); err == nil {
		t.Fatal("expected error when adapter fails")
	}
	if storeErr.completeCalls != 0 {
		t.Errorf("expected 0 store complete calls on upstream error, got %d", storeErr.completeCalls)
	}

	// Case 5: Local store without adapter succeeds
	storeLocal := &mockTasksStore{}
	pLocal := provider.NewTasksProviderWithStoreAndNotifier(storeLocal, notifier)
	_ = pLocal.Init(ctx, map[string]any{"list_id": "local-list"}, provider.InitOptions{ID: "w-tasks"})

	if err := pLocal.CompleteItem(ctx, "item-local"); err != nil {
		t.Fatalf("unexpected error on local complete: %v", err)
	}
	if storeLocal.completeCalls != 1 || storeLocal.lastCompletedID != "item-local" {
		t.Errorf("expected local store complete call, got %d", storeLocal.completeCalls)
	}
	if pLocal.ListID() != "local-list" {
		t.Errorf("expected ListID 'local-list', got %q", pLocal.ListID())
	}
}
