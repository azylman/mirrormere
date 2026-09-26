package provider_test

import (
	"context"
	"errors"
	"path/filepath"
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

	if err := p.Subscribe(context.Background(), nil); err != nil {
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
