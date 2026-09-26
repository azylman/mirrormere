package provider

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/azylman/mirrormere/internal/tasks"
)

const (
	defaultTasksDBPath        = "/data/lists.db"
	defaultTasksSource        = "local"
	defaultTasksShowCompleted = 3
)

// TasksConfig holds parsed configuration for TasksProvider.
type TasksConfig struct {
	ListID        string
	ListName      string
	Source        string
	ShowCompleted int
	DBPath        string
}

// TasksProvider implements Provider for the built-in tasks widget.
// Backed by the local pure-Go SQLite store per SPEC-008.
type TasksProvider struct {
	mu        sync.RWMutex
	cfg       TasksConfig
	store     tasks.Store
	ownsStore bool
}

// NewTasksProvider constructs a new TasksProvider.
func NewTasksProvider() *TasksProvider {
	return &TasksProvider{}
}

// NewTasksProviderWithStore constructs a TasksProvider with an injected Store.
func NewTasksProviderWithStore(store tasks.Store) *TasksProvider {
	return &TasksProvider{
		store:     store,
		ownsStore: false,
	}
}

// Init configures the tasks provider.
func (p *TasksProvider) Init(ctx context.Context, config map[string]any, opts InitOptions) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	cfg := TasksConfig{
		ListID:        opts.ID,
		Source:        defaultTasksSource,
		ShowCompleted: defaultTasksShowCompleted,
		DBPath:        defaultTasksDBPath,
	}

	if raw, ok := config["list_id"].(string); ok && raw != "" {
		cfg.ListID = raw
	}
	if raw, ok := config["list_name"].(string); ok && raw != "" {
		cfg.ListName = raw
	} else {
		cfg.ListName = cfg.ListID
	}
	if raw, ok := config["source"].(string); ok && raw != "" {
		cfg.Source = raw
	}
	if raw, ok := config["db_path"].(string); ok && raw != "" {
		cfg.DBPath = raw
	}
	if raw, ok := config["show_completed"]; ok {
		switch v := raw.(type) {
		case int:
			cfg.ShowCompleted = v
		case float64:
			cfg.ShowCompleted = int(v)
		case string:
			if n, err := strconv.Atoi(v); err == nil {
				cfg.ShowCompleted = n
			}
		}
	}
	if cfg.ShowCompleted < 0 {
		cfg.ShowCompleted = 0
	}

	p.cfg = cfg

	if p.store == nil {
		store, err := tasks.NewSQLiteStore(cfg.DBPath)
		if err != nil {
			return fmt.Errorf("failed to initialize tasks sqlite store: %w", err)
		}
		p.store = store
		p.ownsStore = true
	}

	return nil
}

// Fetch retrieves the tasks snapshot for the configured list.
// If the list does not exist yet (cold boot), it auto-upserts an initial empty list definition.
func (p *TasksProvider) Fetch(ctx context.Context) (any, error) {
	p.mu.RLock()
	store := p.store
	cfg := p.cfg
	p.mu.RUnlock()

	if store == nil {
		return nil, errors.New("tasks provider not initialized: nil store")
	}

	snap, err := store.GetTasksSnapshot(ctx, cfg.ListID, cfg.ShowCompleted)
	if err != nil {
		if errors.Is(err, tasks.ErrListNotFound) {
			// Auto-seed empty list on cold boot per SPEC-008
			now := time.Now().UTC().Truncate(time.Second)
			newList := tasks.List{
				ID:        cfg.ListID,
				Name:      cfg.ListName,
				Source:    cfg.Source,
				Sections:  []string{},
				CreatedAt: now,
				UpdatedAt: now,
			}
			if upsertErr := store.UpsertList(ctx, newList); upsertErr != nil {
				return nil, fmt.Errorf("failed to auto-seed list %q: %w", cfg.ListID, upsertErr)
			}
			return &tasks.TasksSnapshot{
				List:  newList,
				Items: []tasks.ListItem{},
			}, nil
		}
		return nil, err
	}

	return snap, nil
}

// Subscribe is a no-op as the coordinator polls tasks periodically via SWR cache.
func (p *TasksProvider) Subscribe(ctx context.Context, eventSink chan<- WidgetPayload) error {
	return nil
}

// Shutdown closes the store if owned by the provider.
func (p *TasksProvider) Shutdown(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.store != nil && p.ownsStore {
		err := p.store.Close()
		p.store = nil
		p.ownsStore = false
		return err
	}
	return nil
}
