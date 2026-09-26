package provider

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/azylman/mirrormere/internal/tasks"
	"github.com/azylman/mirrormere/internal/tasks/adapters"
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
// Backed by the local pure-Go SQLite store and pluggable ingestion adapters per SPEC-008.
type TasksProvider struct {
	mu        sync.RWMutex
	widgetID  string
	cfg       TasksConfig
	store     tasks.Store
	ownsStore bool
	adapter   adapters.Adapter
	notifier  *tasks.ChangeNotifier
}

// NewTasksProvider constructs a new TasksProvider.
func NewTasksProvider() *TasksProvider {
	return &TasksProvider{
		notifier: tasks.DefaultChangeNotifier(),
	}
}

// NewTasksProviderWithStore constructs a TasksProvider with an injected Store.
func NewTasksProviderWithStore(store tasks.Store) *TasksProvider {
	return &TasksProvider{
		store:     store,
		ownsStore: false,
		notifier:  tasks.DefaultChangeNotifier(),
	}
}

// NewTasksProviderWithStoreAndNotifier constructs a TasksProvider with injected Store and ChangeNotifier.
func NewTasksProviderWithStoreAndNotifier(store tasks.Store, notifier *tasks.ChangeNotifier) *TasksProvider {
	if notifier == nil {
		notifier = tasks.DefaultChangeNotifier()
	}
	return &TasksProvider{
		store:     store,
		ownsStore: false,
		notifier:  notifier,
	}
}

// NewTasksProviderWithAdapter constructs a TasksProvider with an injected Store and Adapter for testing.
func NewTasksProviderWithAdapter(store tasks.Store, adapter adapters.Adapter, notifier *tasks.ChangeNotifier) *TasksProvider {
	if notifier == nil {
		notifier = tasks.DefaultChangeNotifier()
	}
	return &TasksProvider{
		store:     store,
		ownsStore: false,
		adapter:   adapter,
		notifier:  notifier,
	}
}

// Init configures the tasks provider and instantiates external adapters if declared.
func (p *TasksProvider) Init(ctx context.Context, config map[string]any, opts InitOptions) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.widgetID = opts.ID

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
	if raw, ok := config["source"].(string); ok {
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

	// Instantiate external source adapter if configured and not already injected
	if p.adapter == nil {
		switch cfg.Source {
		case "http":
			httpCfg, ok := config["http"].(map[string]any)
			if !ok || httpCfg == nil {
				return errors.New("tasks provider with source 'http' requires 'http' config block")
			}
			baseURL, ok := httpCfg["base_url"].(string)
			if !ok || baseURL == "" {
				return errors.New("tasks provider with source 'http' requires 'http.base_url'")
			}

			token := opts.GetSecret("http.token")
			if token == "" {
				token = opts.Token
			}
			if token == "" {
				if rawTok, tokOk := httpCfg["token"].(string); tokOk {
					token = rawTok
				}
			}
			if token == "" {
				token = opts.GetSecret("token")
			}
			if token == "" {
				if tokEnv, envOk := httpCfg["token_env"].(string); envOk && tokEnv != "" {
					token = opts.GetSecret(tokEnv)
					if token == "" {
						token = os.Getenv(tokEnv)
					}
				}
			}

			ad, err := adapters.NewHTTPAdapter(adapters.HTTPAdapterConfig{
				BaseURL: baseURL,
				Token:   token,
			})
			if err != nil {
				return fmt.Errorf("failed to create http task adapter: %w", err)
			}
			p.adapter = ad

		case "gtasks":
			gtasksCfg, ok := config["gtasks"].(map[string]any)
			if !ok || gtasksCfg == nil {
				return errors.New("tasks provider with source 'gtasks' requires 'gtasks' config block")
			}
			taskListID, ok := gtasksCfg["tasklist_id"].(string)
			if !ok || taskListID == "" {
				return errors.New("tasks provider with source 'gtasks' requires 'gtasks.tasklist_id'")
			}
			baseURL := ""
			if rawURL, ok := gtasksCfg["base_url"].(string); ok {
				baseURL = rawURL
			}

			token := opts.GetSecret("gtasks.token")
			if token == "" {
				token = opts.Token
			}
			if token == "" {
				if rawTok, tokOk := gtasksCfg["token"].(string); tokOk {
					token = rawTok
				}
			}
			if token == "" {
				token = opts.GetSecret("token")
			}
			if token == "" {
				if tokEnv, envOk := gtasksCfg["token_env"].(string); envOk && tokEnv != "" {
					token = opts.GetSecret(tokEnv)
					if token == "" {
						token = os.Getenv(tokEnv)
					}
				}
			}

			ad, err := adapters.NewGTasksAdapter(adapters.GTasksAdapterConfig{
				TaskListID: taskListID,
				Token:      token,
				ListName:   cfg.ListName,
				BaseURL:    baseURL,
			})
			if err != nil {
				return fmt.Errorf("failed to create gtasks adapter: %w", err)
			}
			p.adapter = ad
		}
	}

	if p.notifier == nil {
		p.notifier = tasks.DefaultChangeNotifier()
	}

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
// If an external adapter is configured, it fetches from upstream and reconciles into SQLite.
func (p *TasksProvider) Fetch(ctx context.Context) (any, error) {
	p.mu.RLock()
	store := p.store
	cfg := p.cfg
	adapter := p.adapter
	notifier := p.notifier
	p.mu.RUnlock()

	if store == nil {
		return nil, errors.New("tasks provider not initialized: nil store")
	}

	// 1. External adapter ingestion
	if adapter != nil {
		list, items, err := adapter.FetchList(ctx)
		if err != nil {
			return nil, fmt.Errorf("task list sync failed for %s (%s): %w", cfg.ListID, adapter.Name(), err)
		}
		if list == nil {
			return nil, fmt.Errorf("task list sync failed for %s (%s): adapter returned nil list", cfg.ListID, adapter.Name())
		}
		if cfg.ListID != "" && list.ID != cfg.ListID {
			list.ID = cfg.ListID
			for i := range items {
				items[i].ListID = cfg.ListID
			}
		}

		changed, syncErr := store.SyncList(ctx, *list, items)
		if syncErr != nil {
			return nil, fmt.Errorf("failed to sync list %s to database: %w", cfg.ListID, syncErr)
		}
		if changed && notifier != nil {
			notifier.NotifyChange(cfg.ListID)
		}
	}

	// 2. Retrieve snapshot from store
	snap, err := store.GetTasksSnapshot(ctx, cfg.ListID, cfg.ShowCompleted)
	if err != nil {
		if errors.Is(err, tasks.ErrListNotFound) {
			if adapter == nil {
				// Auto-seed empty list on cold boot per SPEC-008
				now := time.Now().UTC().Truncate(time.Second)
				source := cfg.Source
				if source == "" {
					source = defaultTasksSource
				}
				newList := tasks.List{
					ID:        cfg.ListID,
					Name:      cfg.ListName,
					Source:    source,
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
		}
		return nil, err
	}

	return snap, nil
}

// Subscribe listens for real-time task list change notifications and pushes updated payloads to eventSink.
// Blocks until ctx is cancelled, conforming to the Provider coordinator subscription model.
func (p *TasksProvider) Subscribe(ctx context.Context, eventSink chan<- WidgetPayload) error {
	p.mu.RLock()
	notifier := p.notifier
	listID := p.cfg.ListID
	showCompleted := p.cfg.ShowCompleted
	widgetID := p.widgetID
	store := p.store
	p.mu.RUnlock()

	if notifier == nil || store == nil || eventSink == nil {
		return nil
	}

	updateCh := make(chan struct{}, 4)
	unregister := notifier.RegisterListener(listID, updateCh)
	defer unregister()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-updateCh:
			snap, err := store.GetTasksSnapshot(ctx, listID, showCompleted)
			if err == nil && eventSink != nil {
				select {
				case <-ctx.Done():
					return nil
				case eventSink <- WidgetPayload{
					WidgetID:  widgetID,
					Timestamp: time.Now().UTC().Format(time.RFC3339),
					State:     StateHealthy,
					Data:      snap,
				}:
				}
			}
		}
	}
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
