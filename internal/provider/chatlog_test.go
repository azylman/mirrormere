package provider_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/chatlog"
	"github.com/azylman/mirrormere/internal/config"
	"github.com/azylman/mirrormere/internal/domain"
	"github.com/azylman/mirrormere/internal/provider"
	"github.com/azylman/mirrormere/internal/testutil"
	"github.com/azylman/mirrormere/internal/testutil/eventstest"
)

func chatLogSnapshot() *config.Snapshot {
	m := map[string]any{"type": "object", "properties": map[string]any{"messages": map[string]any{"type": "array", "items": map[string]any{"type": "object", "required": []any{"role", "text"}, "properties": map[string]any{"role": map[string]any{"type": "string", "enum": []any{"human", "agent"}}}}}}}
	return &config.Snapshot{
		Config:   &config.Config{Display: config.DisplayConfig{Widgets: []config.WidgetConfig{{ID: "chat", Type: "chat-log"}, {ID: "cal", Type: "spacer"}}}},
		Packages: map[string]*domain.Package{"chat-log": {Type: "chat-log", Manifest: domain.WidgetManifest{Provider: "chat-log", ResponseSchema: m}}, "spacer": {Type: "spacer", Manifest: domain.WidgetManifest{Provider: "spacer"}}},
	}
}

func TestChatLogWidget_PushValidatesStoresAndBroadcasts(t *testing.T) {
	t.Parallel()
	store, bc := chatlog.NewStore(), &eventstest.Broadcaster{}
	coord := provider.NewCoordinator(provider.CoordinatorConfig{Broadcaster: bc, StateSink: newMockStateSink(), ChatLog: store}, chatLogSnapshot())
	defer func() { _ = coord.Stop() }()
	bc.WaitForPublish(time.Second)

	if _, err := coord.PushWidgetData(context.Background(), "chat", map[string]any{"messages": []any{map[string]any{"role": "human", "text": "hi"}}}); err != nil {
		t.Fatalf("push: %v", err)
	}
	if got := testutil.As[[]any](t, store.View("chat", "node")["messages"]); len(got) != 1 || testutil.As[map[string]any](t, got[0])["text"] != "hi" {
		t.Fatalf("bad store: %v", got)
	}
	if _, ok := bc.WaitForPublish(time.Second); !ok {
		t.Fatal("must broadcast")
	}

	for _, bad := range []map[string]any{{"messages": []any{map[string]any{"role": "robot", "text": "x"}}}, {"messages": []any{map[string]any{"role": "human"}}}, {"messages": []any{map[string]any{"role": "human", "text": "x", "ts": "yesterday"}}}} {
		if _, err := coord.PushWidgetData(context.Background(), "chat", bad); !errors.Is(err, provider.ErrSchemaValidation) {
			t.Errorf("expected ErrSchemaValidation for %v, got %v", bad, err)
		}
	}
	if _, err := coord.PushWidgetData(context.Background(), "cal", map[string]any{"a": 1}); len(testutil.As[[]any](t, store.View("chat", "")["messages"])) != 1 || !errors.Is(err, provider.ErrNonHTTPWidgetPushForbidden) {
		t.Fatalf("unexpected store state or non-http push error: %v", err)
	}
}
