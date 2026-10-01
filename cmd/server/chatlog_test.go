package main

import (
	"testing"

	"github.com/azylman/mirrormere/internal/chatlog"
	"github.com/azylman/mirrormere/internal/config"
	"github.com/azylman/mirrormere/internal/provider"
)

type fakeSnapshots struct{ snap *config.Snapshot }

func (f fakeSnapshots) CurrentSnapshot() *config.Snapshot { return f.snap }
func (f fakeSnapshots) GetWidgetState(string) (any, string, string, bool) {
	return nil, "", "", false
}
func (f fakeSnapshots) CurrentStatus() config.Status { return config.Status{} }

type fakePusher struct{ ids []string }

func (f *fakePusher) PushUpdate(id string, _ any) (provider.WidgetPayload, error) {
	f.ids = append(f.ids, id)
	return provider.WidgetPayload{}, nil
}

func chatSnap() fakeSnapshots {
	return fakeSnapshots{snap: &config.Snapshot{Config: &config.Config{Display: config.DisplayConfig{Widgets: []config.WidgetConfig{
		{ID: "a", Type: "chat-log"}, {ID: "s", Type: "spacer"}, {ID: "b", Type: "chat-log"},
	}}}}}
}

func TestPushChatLogUpdates_HitsOnlyChatLogWidgets(t *testing.T) {
	p := &fakePusher{}
	pushChatLogUpdates(chatSnap(), p, chatlog.NewStore())
	if len(p.ids) != 2 || p.ids[0] != "a" || p.ids[1] != "b" {
		t.Fatalf("expected push to a and b only, got %v", p.ids)
	}
	pushChatLogUpdates(fakeSnapshots{}, p, chatlog.NewStore()) // no config: no-op, no panic
}

func TestChatLogNodeData(t *testing.T) {
	store := chatlog.NewStore()
	store.Add(chatlog.NodeKey("kitchen"), chatlog.Message{Role: chatlog.RoleHuman, Text: "hi"})
	fn := chatLogNodeData(chatSnap(), store)

	d, ok := fn("a", "kitchen")
	if !ok || len(d.(map[string]any)["messages"].([]any)) != 1 {
		t.Fatalf("chat-log widget should render the kitchen conversation: %v %v", d, ok)
	}
	if d, _ := fn("a", "office"); len(d.(map[string]any)["messages"].([]any)) != 0 {
		t.Fatalf("office must not see kitchen messages: %v", d)
	}
	if _, ok := fn("s", "kitchen"); ok {
		t.Fatal("non chat-log widgets keep their shared data")
	}
}
