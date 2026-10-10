package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/chatlog"
	"github.com/azylman/mirrormere/internal/config"
)

type fastPathHarness struct {
	hub      *Hub
	brain    *mockBrain
	store    *chatlog.Store
	tts      *recordingTTS
	mu       sync.Mutex
	events   []recordedEvent
	fpCalls  atomic.Int32
	lastBody atomic.Value // map[string]string
}

type recordingTTS struct {
	mu    sync.Mutex
	texts []string
}

func (r *recordingTTS) Synthesize(_ context.Context, text string, onChunk func(TTSAudioChunk) error) error {
	r.mu.Lock()
	r.texts = append(r.texts, text)
	r.mu.Unlock()
	return onChunk(TTSAudioChunk{Data: make([]byte, 9600), Format: "pcm", SampleRate: 24000, Channels: 1})
}

func (r *recordingTTS) spoken() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.texts...)
}

// newFastPathHarness runs the hub against handler as the fast-path endpoint.
// An empty fastURL leaves the feature unconfigured.
func newFastPathHarness(t *testing.T, fp *config.FastPathConfig) *fastPathHarness {
	t.Helper()
	h := &fastPathHarness{brain: &mockBrain{reply: "brain answer"}, store: chatlog.NewStore(), tts: &recordingTTS{}}
	cfg := &config.VoiceHubConfig{Enabled: true, FastPath: fp}
	h.hub = NewHub(cfg, NewCoordinator(nil),
		WithSTTClient(&mockSTT{text: "pause the music"}),
		WithBrainClient(h.brain),
		WithTTSClient(h.tts),
		WithSpeakerIdentifier(fixedSpeaker{name: "sam"}),
		WithChatLog(h.store, nil),
	)
	return h
}

func (h *fastPathHarness) turn(t *testing.T) error {
	t.Helper()
	sink := func(ev string, data any) error {
		h.mu.Lock()
		h.events = append(h.events, recordedEvent{Event: ev, Data: data})
		h.mu.Unlock()
		return nil
	}
	return h.hub.Interact(context.Background(), bytes.NewReader(makeValidWAV(1600)), "kitchen-ear", "turn-1", sink, EdgeTimings{})
}

func (h *fastPathHarness) brainCalled() bool {
	h.brain.mu.Lock()
	defer h.brain.mu.Unlock()
	return h.brain.calledWith != ""
}

func (h *fastPathHarness) agentChat() []string {
	var out []string
	for _, m := range h.store.Messages(func(k string) bool { return k == chatlog.NodeKey("kitchen-ear") }) {
		if m.Role == chatlog.RoleAgent {
			out = append(out, m.Text)
		}
	}
	return out
}

func (h *fastPathHarness) server(t *testing.T, handler http.HandlerFunc) *config.FastPathConfig {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.fpCalls.Add(1)
		var body map[string]string
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		h.lastBody.Store(body)
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return &config.FastPathConfig{URL: srv.URL}
}

func writeEvent(w http.ResponseWriter, event, data string) {
	_, _ = w.Write([]byte("event: " + event + "\ndata: " + data + "\n\n"))
	w.(http.Flusher).Flush()
}

func TestFastPath_HitIsPlayedAndBrainSkipped(t *testing.T) {
	t.Parallel()
	h := newFastPathHarness(t, nil)
	fp := h.server(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected request %s %s", r.Method, r.Header.Get("Content-Type"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		writeEvent(w, "turn", `{"turn_id":"turn-1"}`)
		writeEvent(w, "sentence", `{"text":"Paused."}`)
		writeEvent(w, "sentence", `{"text":"Anything else?"}`)
		writeEvent(w, "reply", `{"reply":"Paused. Anything else?"}`)
		writeEvent(w, "done", `{}`)
	})
	h.hub.fastPath = NewDefaultFastPathClient(fp.URL, time.Second)

	if err := h.turn(t); err != nil {
		t.Fatalf("turn: %v", err)
	}
	if h.brainCalled() {
		t.Error("brain must not be asked on a fast-path hit")
	}
	if got := h.tts.spoken(); len(got) != 2 || got[0] != "Paused." || got[1] != "Anything else?" {
		t.Errorf("expected both sentences spoken via local TTS, got %v", got)
	}
	if got := h.agentChat(); len(got) != 1 || got[0] != "Paused. Anything else?" {
		t.Errorf("expected reply recorded in chat log, got %v", got)
	}
	body, _ := h.lastBody.Load().(map[string]string)
	want := map[string]string{"text": "pause the music", "speaker": "sam", "node": "kitchen-ear", "turn_id": "turn-1", "tts": "none"}
	for k, v := range want {
		if body[k] != v {
			t.Errorf("request body %q = %q, want %q (body %v)", k, body[k], v, body)
		}
	}
	names := eventNames(h.events)
	if names[len(names)-1] != "done" || !containsEvent(names, "reply") || !containsEvent(names, "audio_chunk") {
		t.Errorf("expected audio_chunk, reply and done events, got %v", names)
	}
}

func containsEvent(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

func TestFastPath_FallsThroughToBrain(t *testing.T) {
	t.Parallel()
	cases := map[string]http.HandlerFunc{
		"204 no match": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) },
		"500":          func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "boom", http.StatusInternalServerError) },
		"404":          func(w http.ResponseWriter, _ *http.Request) { http.NotFound(w, nil) },
		"200 not sse": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"reply":"json is not supported"}`))
		},
		"200 empty stream": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			writeEvent(w, "turn", `{}`)
			writeEvent(w, "done", `{}`)
		},
		"200 malformed stream": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: sentence\ndata: {not json\n\n"))
		},
	}
	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := newFastPathHarness(t, nil)
			fp := h.server(t, handler)
			h.hub.fastPath = NewDefaultFastPathClient(fp.URL, time.Second)
			if err := h.turn(t); err != nil {
				t.Fatalf("turn: %v", err)
			}
			if h.fpCalls.Load() != 1 {
				t.Errorf("expected one fast-path call, got %d", h.fpCalls.Load())
			}
			if !h.brainCalled() {
				t.Error("expected fall-through to the brain")
			}
			if got := h.agentChat(); len(got) != 1 || got[0] != "brain answer" {
				t.Errorf("expected brain reply in chat log, got %v", got)
			}
		})
	}
}

func TestFastPath_TimeoutFallsThrough(t *testing.T) {
	t.Parallel()
	h := newFastPathHarness(t, nil)
	release := make(chan struct{})
	fp := h.server(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	t.Cleanup(func() { close(release) })
	h.hub.fastPath = NewDefaultFastPathClient(fp.URL, 50*time.Millisecond)

	start := time.Now()
	if err := h.turn(t); err != nil {
		t.Fatalf("turn: %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("fast path did not honor its timeout, turn took %v", time.Since(start))
	}
	if !h.brainCalled() {
		t.Error("expected fall-through to the brain after timeout")
	}
}

func TestFastPath_ConnectionErrorFallsThrough(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	h := newFastPathHarness(t, nil)
	h.hub.fastPath = NewDefaultFastPathClient(url, time.Second)
	if err := h.turn(t); err != nil {
		t.Fatalf("turn: %v", err)
	}
	if !h.brainCalled() {
		t.Error("expected fall-through to the brain on connection error")
	}
}

func TestFastPath_StreamBreaksAfterSentenceDoesNotAskBrain(t *testing.T) {
	t.Parallel()
	h := newFastPathHarness(t, nil)
	fp := h.server(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeEvent(w, "sentence", `{"text":"Paused."}`)
		// Abort the connection mid-stream.
		panic(http.ErrAbortHandler)
	})
	h.hub.fastPath = NewDefaultFastPathClient(fp.URL, time.Second)
	if err := h.turn(t); err != nil {
		t.Fatalf("turn should end cleanly, got %v", err)
	}
	if h.brainCalled() {
		t.Error("brain must not be re-asked after a sentence was spoken")
	}
	if got := h.tts.spoken(); len(got) != 1 || got[0] != "Paused." {
		t.Errorf("expected the spoken sentence, got %v", got)
	}
	if got := h.agentChat(); len(got) != 1 || got[0] != "Paused." {
		t.Errorf("expected spoken text recorded, got %v", got)
	}
}

func TestFastPath_ReplyOnlyIsSpokenByHub(t *testing.T) {
	t.Parallel()
	h := newFastPathHarness(t, nil)
	fp := h.server(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeEvent(w, "reply", `{"reply":"It is 3 PM."}`)
		writeEvent(w, "done", `{}`)
	})
	h.hub.fastPath = NewDefaultFastPathClient(fp.URL, time.Second)
	if err := h.turn(t); err != nil {
		t.Fatalf("turn: %v", err)
	}
	if h.brainCalled() {
		t.Error("brain must not be asked")
	}
	if got := h.tts.spoken(); len(got) != 1 || got[0] != "It is 3 PM." {
		t.Errorf("expected reply spoken once, got %v", got)
	}
}

func TestFastPath_UnsetURLNeverCallsOut(t *testing.T) {
	t.Parallel()
	for name, fp := range map[string]*config.FastPathConfig{"nil": nil, "empty url": {URL: "  "}} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := newFastPathHarness(t, fp)
			if h.hub.fastPath != nil {
				t.Fatal("fast-path client must not be built without a url")
			}
			if err := h.turn(t); err != nil {
				t.Fatalf("turn: %v", err)
			}
			if !h.brainCalled() {
				t.Error("expected the brain to answer")
			}
		})
	}
}

func TestFastPath_ConfiguredURLBuildsClient(t *testing.T) {
	t.Parallel()
	ms := 250
	h := newFastPathHarness(t, &config.FastPathConfig{URL: "http://127.0.0.1:1/intent", TimeoutMS: &ms})
	c, ok := h.hub.fastPath.(*DefaultFastPathClient)
	if !ok {
		t.Fatalf("expected DefaultFastPathClient, got %T", h.hub.fastPath)
	}
	if c.timeout != 250*time.Millisecond || c.url != "http://127.0.0.1:1/intent" {
		t.Errorf("unexpected client %+v", c)
	}
	// Unreachable endpoint: the turn must still be answered by the brain.
	if err := h.turn(t); err != nil {
		t.Fatalf("turn: %v", err)
	}
	if !h.brainCalled() {
		t.Error("expected brain fall-through")
	}
}

// streamingBrainStub lets a miss fall through to an AudioStreamingBrainClient.
type streamingBrainStub struct{ called atomic.Bool }

func (s *streamingBrainStub) Ask(ctx context.Context, req AskRequest, _ func(string)) (string, error) {
	s.called.Store(true)
	return "stream brain", nil
}

func (s *streamingBrainStub) AskStreaming(_ context.Context, _ AskRequest, _ func(string), onAudio func(BrainAudioChunk)) (string, error) {
	s.called.Store(true)
	onAudio(BrainAudioChunk{Text: "From the brain."})
	return "From the brain.", nil
}

func TestFastPath_MissUsesStreamingBrain(t *testing.T) {
	t.Parallel()
	brain := &streamingBrainStub{}
	tts := &recordingTTS{}
	h := NewHub(&config.VoiceHubConfig{Enabled: true}, NewCoordinator(nil),
		WithSTTClient(&mockSTT{text: "tell me a story"}),
		WithBrainClient(brain), WithTTSClient(tts),
		WithFastPathClient(stubFastPath{res: FastPathResult{Outcome: fastPathMiss, Reason: "no_match"}}),
	)
	err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(1600)), "kitchen-ear", "s", func(string, any) error { return nil }, EdgeTimings{})
	if err != nil {
		t.Fatalf("turn: %v", err)
	}
	if !brain.called.Load() {
		t.Error("expected streaming brain to be asked on a miss")
	}
	if got := tts.spoken(); len(got) != 1 || got[0] != "From the brain." {
		t.Errorf("unexpected speech %v", got)
	}
}

type stubFastPath struct{ res FastPathResult }

func (s stubFastPath) Try(context.Context, AskRequest, func(BrainAudioChunk)) FastPathResult {
	return s.res
}

func TestFastPath_NilBrainOnMiss(t *testing.T) {
	t.Parallel()
	b := &fastPathBrain{fp: stubFastPath{res: FastPathResult{Outcome: fastPathMiss}}, metrics: DefaultMetrics()}
	if _, err := b.Ask(context.Background(), AskRequest{}, nil); err == nil {
		t.Error("expected error when there is no brain to fall through to")
	}
}
