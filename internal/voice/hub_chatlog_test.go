package voice

import (
	"bytes"
	"context"
	"sync/atomic"
	"testing"

	"github.com/azylman/mirrormere/internal/chatlog"
	"github.com/azylman/mirrormere/internal/config"
	"github.com/azylman/mirrormere/internal/events"
)

type fixedSpeaker struct{ name string }

func (f fixedSpeaker) Identify(context.Context, []byte) (SpeakerMatch, error) {
	return SpeakerMatch{Speaker: f.name, Score: 0.9}, nil
}

func chatTurn(t *testing.T, h *Hub, node string) error {
	t.Helper()
	sink := func(string, any) error { return nil }
	return h.Interact(context.Background(), bytes.NewReader(makeValidWAV(1600)), node, "", sink, EdgeTimings{})
}

func TestHub_RecordsTurnInChatLog(t *testing.T) {
	t.Parallel()

	hubEvents := events.NewHub(events.HubConfig{}, nil, nil)
	defer hubEvents.Close()
	store := chatlog.NewStore()
	var changes atomic.Int32

	h := NewHub(&config.VoiceHubConfig{Enabled: true}, NewCoordinator(hubEvents),
		WithSTTClient(&mockSTT{text: "Turn off office lights"}),
		WithBrainClient(&mockBrain{reply: "Turned off office lights."}),
		WithTTSClient(&mockTTS{audio: []byte("pcm"), format: "pcm"}),
		WithSpeakerIdentifier(fixedSpeaker{name: "sam"}),
		WithChatLog(store, func() { changes.Add(1) }),
	)

	if err := chatTurn(t, h, "touch-kiosk-kitchen"); err != nil {
		t.Fatalf("interact: %v", err)
	}

	msgs := store.Messages(func(k string) bool { return k == chatlog.NodeKey("touch-kiosk-kitchen") })
	if len(msgs) != 2 {
		t.Fatalf("expected a human and an agent message, got %+v", msgs)
	}
	if msgs[0].Role != chatlog.RoleHuman || msgs[0].Text != "Turn off office lights" || msgs[0].Author != "sam" {
		t.Errorf("unexpected user message: %+v", msgs[0])
	}
	if msgs[1].Role != chatlog.RoleAgent || msgs[1].Text != "Turned off office lights." {
		t.Errorf("unexpected agent message: %+v", msgs[1])
	}
	if msgs[0].At.IsZero() || msgs[1].At.Before(msgs[0].At) {
		t.Errorf("timestamps missing or out of order: %v %v", msgs[0].At, msgs[1].At)
	}
	if got := changes.Load(); got != 2 {
		t.Errorf("expected onChange after each of the 2 recorded messages, got %d", got)
	}
	if other := store.Messages(func(k string) bool { return k == chatlog.NodeKey("eink-display-livingroom") }); len(other) != 0 {
		t.Errorf("other node must not see this conversation: %+v", other)
	}
}

func TestHub_ChatLogBrainFailureKeepsUserTurn(t *testing.T) {
	t.Parallel()

	hubEvents := events.NewHub(events.HubConfig{}, nil, nil)
	defer hubEvents.Close()
	store := chatlog.NewStore()

	h := NewHub(&config.VoiceHubConfig{Enabled: true}, NewCoordinator(hubEvents),
		WithSTTClient(&mockSTT{text: "hello"}),
		WithBrainClient(&mockBrain{err: ErrBrainFailed}),
		WithChatLog(store, nil),
	)
	if err := chatTurn(t, h, "n1"); err == nil {
		t.Fatal("expected brain failure")
	}
	msgs := store.Messages(nil)
	if len(msgs) != 1 || msgs[0].Role != chatlog.RoleHuman || msgs[0].Author != "" {
		t.Fatalf("expected only the user's turn (no known speaker), got %+v", msgs)
	}
}

func TestHub_WithoutChatLogStillWorks(t *testing.T) {
	t.Parallel()
	hubEvents := events.NewHub(events.HubConfig{}, nil, nil)
	defer hubEvents.Close()
	h := NewHub(&config.VoiceHubConfig{Enabled: true}, NewCoordinator(hubEvents),
		WithSTTClient(&mockSTT{text: "hi"}),
		WithBrainClient(&mockBrain{reply: "yo"}),
	)
	if err := chatTurn(t, h, "n1"); err != nil {
		t.Fatalf("interact: %v", err)
	}
}
