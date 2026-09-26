package provider_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/provider"
	"github.com/azylman/mirrormere/internal/tasks"
)

type mockTasksStore struct {
	list        *tasks.List
	items       []tasks.ListItem
	snapshot    *tasks.TasksSnapshot
	getSnapErr  error
	upsertErr   error
	closeErr    error
	closed      bool
	upsertCalls int
	lastListID  string
	lastShow    int
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
		"source":         "todoist",
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
	if snap.List.ID != "groceries" || snap.List.Name != "Family Groceries" || snap.List.Source != "todoist" {
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


