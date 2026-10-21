package main

import (
	"errors"
	"testing"

	"github.com/azylman/mirrormere/internal/chatlog"
	"github.com/azylman/mirrormere/internal/config"
	"github.com/azylman/mirrormere/internal/provider"
)

type fakeSnapshots struct{ snap *config.Snapshot }

func (f fakeSnapshots) CurrentSnapshot() *config.Snapshot                 { return f.snap }
func (f fakeSnapshots) GetWidgetState(string) (any, string, string, bool) { return nil, "", "", false }
func (f fakeSnapshots) CurrentStatus() config.Status                      { return config.Status{} }

type fakePusher struct {
	ids []string
	err error
}

func (f *fakePusher) PushUpdate(id string, _ any) (provider.WidgetPayload, error) {
	f.ids = append(f.ids, id)
	return provider.WidgetPayload{}, f.err
}

func chatSnap() fakeSnapshots {
	return fakeSnapshots{snap: &config.Snapshot{Config: &config.Config{Display: config.DisplayConfig{Widgets: []config.WidgetConfig{
		{ID: "a", Type: "chat-log"}, {ID: "s", Type: "spacer"}, {ID: "b", Type: "chat-log"},
	}}}}}
}

func TestChatLogWiring(t *testing.T) {
	p := &fakePusher{err: errors.New("boom")}
	pushChatLogUpdates(chatSnap(), p, chatlog.NewStore())
	pushChatLogUpdates(fakeSnapshots{}, p, chatlog.NewStore())
	if len(p.ids) != 2 || p.ids[0] != "a" || p.ids[1] != "b" {
		t.Fatalf("pushChatLogUpdates ids = %v, want [a b]", p.ids)
	}
	store := chatlog.NewStore()
	store.Add(chatlog.NodeKey("kitchen"), chatlog.Message{Role: chatlog.RoleHuman, Text: "hi"})
	fn := chatLogNodeData(chatSnap(), store)
	if d, ok := fn("a", "kitchen"); !ok || len(d.(map[string]any)["messages"].([]any)) != 1 {
		t.Fatalf("kitchen data mismatch: %v, %v", d, ok)
	}
	if d, _ := fn("a", "office"); len(d.(map[string]any)["messages"].([]any)) != 0 {
		t.Fatalf("office data leaked kitchen: %v", d)
	}
	if _, ok := fn("s", "kitchen"); ok {
		t.Fatal("expected ok=false for spacer widget")
	}
	var lazyPusher chatLogPusher
	hook := newChatLogHook(chatSnap(), func() chatLogPusher { return lazyPusher }, chatlog.NewStore())
	lazyPusher = &fakePusher{}
	hook()
	if len(lazyPusher.(*fakePusher).ids) != 2 {
		t.Fatalf("lazy hook pushed to %v, want 2 widgets", lazyPusher.(*fakePusher).ids)
	}
}
