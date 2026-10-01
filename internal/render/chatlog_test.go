package render_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azylman/mirrormere/internal/chatlog"
	"github.com/azylman/mirrormere/internal/config"
	"github.com/azylman/mirrormere/internal/domain"
	"github.com/azylman/mirrormere/internal/render"
	"github.com/azylman/mirrormere/internal/widget"
)

func TestEngine_RenderWidget_ChatLogPackage(t *testing.T) {
	t.Parallel()

	repoWidgetsDir := filepath.Join("..", "..", "widgets")
	loader := widget.NewLoader(repoWidgetsDir, t.TempDir())
	pkg, err := loader.LoadPackage("chat-log")
	if err != nil {
		t.Fatalf("failed to load chat-log package: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(repoWidgetsDir, "chat-log", "testdata", "sample.json"))
	if err != nil {
		t.Fatalf("failed to read sample payload: %v", err)
	}
	var sample map[string]any
	if err := json.Unmarshal(raw, &sample); err != nil {
		t.Fatalf("invalid sample payload: %v", err)
	}

	snap := &config.Snapshot{
		Config: &config.Config{
			Display: config.DisplayConfig{
				Widgets: []config.WidgetConfig{
					{ID: "w-chat-loaded", Type: "chat-log", Dimensions: []int{3, 2}},
					{ID: "w-chat-empty", Type: "chat-log", Dimensions: []int{3, 2}},
					{ID: "w-chat-escape", Type: "chat-log", Dimensions: []int{3, 2}},
				},
			},
		},
		Packages: map[string]*domain.Package{"chat-log": pkg},
	}

	p := &mockSnapshotProvider{
		snapshot: snap,
		states: map[string]mockState{
			"w-chat-loaded": {data: sample, state: "healthy", timestamp: "2026-01-02T18:42:00Z"},
			"w-chat-empty":  {data: map[string]any{}, state: "healthy", timestamp: "2026-01-02T18:42:00Z"},
			"w-chat-escape": {data: map[string]any{
				"messages": []any{map[string]any{"role": "agent", "text": "<script>alert(1)</script>"}},
			}, state: "healthy", timestamp: "2026-01-02T18:42:00Z"},
		},
	}

	engine := render.NewEngine(&mockResolver{packages: map[string]*domain.Package{"chat-log": pkg}}, p)

	html, err := engine.RenderWidget(context.Background(), "w-chat-loaded")
	if err != nil {
		t.Fatalf("RenderWidget failed on loaded chat-log: %v", err)
	}
	out := string(html)
	for _, want := range []string{
		"Kitchen assistant", "18:42", "sam", "What is on the calendar tomorrow?",
		"Dentist at 9:00 and soccer practice at 4:30.", "chat-log-human", "chat-log-agent",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output: %s", want, out)
		}
	}
	if strings.Index(out, "What is on the calendar") > strings.Index(out, "Done. Milk and eggs") {
		t.Errorf("expected messages rendered oldest first")
	}

	htmlEmpty, err := engine.RenderWidget(context.Background(), "w-chat-empty")
	if err != nil {
		t.Fatalf("RenderWidget failed on empty chat-log: %v", err)
	}
	if !strings.Contains(string(htmlEmpty), "No messages yet") {
		t.Errorf("expected empty-state text in output: %s", htmlEmpty)
	}

	htmlEsc, err := engine.RenderWidget(context.Background(), "w-chat-escape")
	if err != nil {
		t.Fatalf("RenderWidget failed on escape chat-log: %v", err)
	}
	if strings.Contains(string(htmlEsc), "<script>alert") {
		t.Errorf("message text must be HTML-escaped: %s", htmlEsc)
	}
	if !strings.Contains(string(htmlEsc), "Chat") {
		t.Errorf("expected default session title: %s", htmlEsc)
	}
}

// The widget renders from the chat-log provider, per requesting display: the
// "node" query parameter on the render request selects that device's conversation.
func TestChatLogWidget_RendersFromProviderPerNode(t *testing.T) {
	t.Parallel()

	repoWidgetsDir := filepath.Join("..", "..", "widgets")
	loader := widget.NewLoader(repoWidgetsDir, t.TempDir())
	pkg, err := loader.LoadPackage("chat-log")
	if err != nil {
		t.Fatalf("failed to load chat-log package: %v", err)
	}
	if pkg.Manifest.Provider != "chat-log" {
		t.Fatalf("manifest should use the endpoint-less chat-log provider, got %q", pkg.Manifest.Provider)
	}

	store := chatlog.NewStore()
	store.Add(chatlog.NodeKey("kitchen"), chatlog.Message{Role: chatlog.RoleHuman, Author: "sam", Text: "kitchen question"})
	store.Add(chatlog.NodeKey("kitchen"), chatlog.Message{Role: chatlog.RoleAgent, Text: "kitchen answer"})
	store.Add(chatlog.NodeKey("office"), chatlog.Message{Role: chatlog.RoleHuman, Text: "office question <b>"})

	snap := &config.Snapshot{
		Config: &config.Config{Display: config.DisplayConfig{Widgets: []config.WidgetConfig{
			{ID: "chat", Type: "chat-log", Dimensions: []int{3, 2}, Config: map[string]any{"title": "Home"}},
		}}},
		Packages: map[string]*domain.Package{"chat-log": pkg},
	}
	state := &mockSnapshotProvider{snapshot: snap, states: map[string]mockState{}}
	engine := render.NewEngine(&mockResolver{packages: map[string]*domain.Package{"chat-log": pkg}}, state,
		render.WithNodeDataFunc(func(widgetID, nodeID string) (any, bool) {
			if widgetID != "chat" {
				return nil, false
			}
			return store.View(widgetID, nodeID), true
		}),
	)

	kitchen, err := engine.RenderWidgetForNode(context.Background(), "chat", "kitchen")
	if err != nil {
		t.Fatalf("render kitchen: %v", err)
	}
	for _, want := range []string{"Home", "kitchen question", "kitchen answer", "sam", "chat-log-human", "chat-log-agent"} {
		if !strings.Contains(string(kitchen), want) {
			t.Errorf("kitchen render missing %q: %s", want, kitchen)
		}
	}
	if strings.Contains(string(kitchen), "office question") {
		t.Errorf("kitchen render leaked the office conversation")
	}

	office, err := engine.RenderWidgetForNode(context.Background(), "chat", "office")
	if err != nil {
		t.Fatalf("render office: %v", err)
	}
	if !strings.Contains(string(office), "office question &lt;b&gt;") || strings.Contains(string(office), "kitchen question") {
		t.Errorf("unexpected office render: %s", office)
	}

	// No node: the most recently active node (office).
	fallback, err := engine.RenderWidget(context.Background(), "chat")
	if err != nil {
		t.Fatalf("render fallback: %v", err)
	}
	if !strings.Contains(string(fallback), "office question") {
		t.Errorf("fallback should show the most recent node: %s", fallback)
	}

	// Never-spoken node: empty state, not an error.
	empty, err := engine.RenderWidgetForNode(context.Background(), "chat", "garage")
	if err != nil || !strings.Contains(string(empty), "No messages yet") {
		t.Errorf("garage render: err=%v out=%s", err, empty)
	}

	// The HTTP handler forwards ?node= to the engine.
	h := render.NewHandler(engine, &mockResolver{packages: map[string]*domain.Package{"chat-log": pkg}})
	rec := httptest.NewRecorder()
	h.GetWidgetRender(rec, httptest.NewRequest(http.MethodGet, "/api/widgets/chat/render?node=kitchen", nil), "chat")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "kitchen answer") {
		t.Errorf("handler render: %d %s", rec.Code, rec.Body.String())
	}
}
