package provider_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/chatlog"
	"github.com/azylman/mirrormere/internal/config"
	"github.com/azylman/mirrormere/internal/domain"
	"github.com/azylman/mirrormere/internal/provider"
)

// countingRegistry records which provider types the coordinator instantiates.
type countingRegistry struct {
	provider.Registry
	mu      sync.Mutex
	created []string
}

func (r *countingRegistry) Create(t string) (provider.Provider, error) {
	r.mu.Lock()
	r.created = append(r.created, t)
	r.mu.Unlock()
	return r.Registry.Create(t)
}

func chatLogSnapshot() *config.Snapshot {
	return &config.Snapshot{
		Config: &config.Config{Display: config.DisplayConfig{Widgets: []config.WidgetConfig{
			{ID: "chat", Type: "chat-log"},
			{ID: "cal", Type: "calendar-agenda"},
		}}},
		Packages: map[string]*domain.Package{
			"chat-log": {Type: "chat-log", Manifest: domain.WidgetManifest{
				Name: "chat-log", Provider: "chat-log",
				ResponseSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"messages": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type":       "object",
								"required":   []any{"role", "text"},
								"properties": map[string]any{"role": map[string]any{"type": "string", "enum": []any{"human", "agent"}}},
							},
						},
					},
				},
			}},
			"calendar-agenda": {Type: "calendar-agenda", Manifest: domain.WidgetManifest{Name: "calendar-agenda", Provider: "calendar-agenda"}},
		},
	}
}

func TestChatLogWidget_NoPollingWorkerAndEmptyCache(t *testing.T) {
	t.Parallel()
	reg := &countingRegistry{Registry: provider.NewRegistry()}
	sink := newMockStateSink()
	coord := provider.NewCoordinator(provider.CoordinatorConfig{
		Registry: reg, Broadcaster: &mockBroadcaster{}, StateSink: sink,
	}, chatLogSnapshot())
	defer func() { _ = coord.Stop() }()

	reg.mu.Lock()
	defer reg.mu.Unlock()
	for _, typ := range reg.created {
		if typ == "chat-log" {
			t.Fatal("chat-log must not get a provider or polling worker")
		}
	}
	if _, ok := sink.widgetStates["chat"]; !ok {
		t.Fatal("chat-log widget should have an initial (empty) cached state so it is never missing")
	}
}

func TestChatLogWidget_PushValidatesStoresAndBroadcasts(t *testing.T) {
	t.Parallel()
	store := chatlog.NewStore()
	bc := &mockBroadcaster{}
	coord := provider.NewCoordinator(provider.CoordinatorConfig{
		Broadcaster: bc, StateSink: newMockStateSink(), ChatLog: store,
	}, chatLogSnapshot())
	defer func() { _ = coord.Stop() }()
	ctx := context.Background()

	// Drain the start-up broadcast.
	bc.waitForPublish(time.Second)

	if _, err := coord.PushWidgetData(ctx, "chat", map[string]any{
		"messages": []any{map[string]any{"role": "human", "author": "sam", "text": "hi"}},
	}); err != nil {
		t.Fatalf("push: %v", err)
	}
	got := store.View("chat", "any-node")["messages"].([]any)
	if len(got) != 1 || got[0].(map[string]any)["text"] != "hi" {
		t.Fatalf("pushed message should be in the store: %v", got)
	}
	if _, ok := bc.waitForPublish(time.Second); !ok {
		t.Fatal("push must broadcast widget.update")
	}

	// Schema violations and bad timestamps are 400-class errors and leave the store alone.
	for _, bad := range []map[string]any{
		{"messages": []any{map[string]any{"role": "robot", "text": "x"}}},
		{"messages": []any{map[string]any{"role": "human"}}},
		{"messages": []any{map[string]any{"role": "human", "text": "x", "ts": "yesterday"}}},
	} {
		if _, err := coord.PushWidgetData(ctx, "chat", bad); !errors.Is(err, provider.ErrSchemaValidation) {
			t.Errorf("expected ErrSchemaValidation for %v, got %v", bad, err)
		}
	}
	if n := len(store.View("chat", "")["messages"].([]any)); n != 1 {
		t.Errorf("rejected pushes must not change the store, got %d messages", n)
	}

	// Other built-in widgets are still protected.
	if _, err := coord.PushWidgetData(ctx, "cal", map[string]any{"a": 1}); !errors.Is(err, provider.ErrNonHTTPWidgetPushForbidden) {
		t.Fatalf("expected ErrNonHTTPWidgetPushForbidden, got %v", err)
	}
}

func TestChatLogWidget_PushUpdateBroadcastsWidgetUpdate(t *testing.T) {
	t.Parallel()
	bc := &mockBroadcaster{}
	coord := provider.NewCoordinator(provider.CoordinatorConfig{
		Broadcaster: bc, StateSink: newMockStateSink(), ChatLog: chatlog.NewStore(),
	}, chatLogSnapshot())
	defer func() { _ = coord.Stop() }()
	bc.waitForPublish(time.Second)

	if _, err := coord.PushUpdate("chat", map[string]any{"messages": []any{}}); err != nil {
		t.Fatal(err)
	}
	evt, ok := bc.waitForPublish(time.Second)
	if !ok || evt.Type != "widget.update" {
		t.Fatalf("expected a widget.update broadcast, got %v %v", evt, ok)
	}
}
