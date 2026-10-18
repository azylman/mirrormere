package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
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
	lastBody atomic.Value
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

func newFastPathHarness(t *testing.T, fp *config.FastPathConfig) *fastPathHarness {
	t.Helper()
	h := &fastPathHarness{brain: &mockBrain{reply: "brain answer"}, store: chatlog.NewStore(), tts: &recordingTTS{}}
	h.hub = NewHub(&config.VoiceHubConfig{Enabled: true, FastPath: fp}, NewCoordinator(nil),
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
	t.Cleanup(func() {
		srv.CloseClientConnections()
		srv.Close()
	})
	return &config.FastPathConfig{URL: srv.URL}
}

func writeEvent(w http.ResponseWriter, event, data string) {
	_, _ = w.Write([]byte("event: " + event + "\ndata: " + data + "\n\n"))
	w.(http.Flusher).Flush()
}

func sseHandler(events ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for i := 0; i+1 < len(events); i += 2 {
			writeEvent(w, events[i], events[i+1])
		}
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

func TestFastPath_HitOutcomes(t *testing.T) {
	t.Parallel()
	streamH := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected request")
		}
		sseHandler("turn", `{"turn_id":"turn-1"}`, "sentence", `{"text":"Paused."}`, "sentence", `{"text":"Anything else?"}`, "reply", `{"reply":"Paused. Anything else?"}`, "done", `{}`)(w, r)
	}
	abortH := func(w http.ResponseWriter, r *http.Request) {
		sseHandler("sentence", `{"text":"Paused."}`)(w, r)
		panic(http.ErrAbortHandler)
	}
	tests := []struct {
		name       string
		handler    http.HandlerFunc
		wantSpoken []string
		wantChat   []string
		wantBody   map[string]string
		wantEvents bool
	}{
		{"streamed", streamH, []string{"Paused.", "Anything else?"}, []string{"Paused. Anything else?"}, map[string]string{"text": "pause the music", "speaker": "sam", "node": "kitchen-ear", "turn_id": "turn-1", "tts": "none"}, true},
		{"abort", abortH, []string{"Paused."}, []string{"Paused."}, nil, false},
		{"reply_only", sseHandler("reply", `{"reply":"It is 3 PM."}`, "done", `{}`), []string{"It is 3 PM."}, []string{"It is 3 PM."}, nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newFastPathHarness(t, nil)
			fp := h.server(t, tc.handler)
			h.hub.fastPath = NewDefaultFastPathClient(fp.URL, time.Second)
			if err := h.turn(t); err != nil || h.brainCalled() {
				t.Fatalf("turn err=%v brainCalled=%v", err, h.brainCalled())
			}
			if !slices.Equal(h.tts.spoken(), tc.wantSpoken) || !slices.Equal(h.agentChat(), tc.wantChat) {
				t.Errorf("spoken=%v chat=%v", h.tts.spoken(), h.agentChat())
			}
			if tc.wantBody != nil {
				body, _ := h.lastBody.Load().(map[string]string)
				for k, v := range tc.wantBody {
					if body[k] != v {
						t.Errorf("body[%q] = %q, want %q", k, body[k], v)
					}
				}
			}
			if tc.wantEvents {
				names := eventNames(h.events)
				if len(names) == 0 || names[len(names)-1] != "done" || !containsEvent(names, "reply") || !containsEvent(names, "audio_chunk") {
					t.Errorf("events: %v", names)
				}
			}
		})
	}
}

func TestFastPath_FallThroughToBrain(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		handler http.HandlerFunc
		timeout time.Duration
		closed  bool
	}{
		{"204_no_match", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }, 0, false},
		{"500_error", func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "boom", http.StatusInternalServerError) }, 0, false},
		{"404_not_found", func(w http.ResponseWriter, _ *http.Request) { http.NotFound(w, nil) }, 0, false},
		{"not_sse", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"reply":"json"}`))
		}, 0, false},
		{"empty_stream", sseHandler("turn", `{}`, "done", `{}`), 0, false},
		{"malformed_stream", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: sentence\ndata: {bad\n\n"))
		}, 0, false},
		{"timeout", func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }, 50 * time.Millisecond, false},
		{"connection_error", nil, 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newFastPathHarness(t, nil)
			to := tc.timeout
			if to == 0 {
				to = time.Second
			}
			url := ""
			if tc.closed {
				s := httptest.NewServer(http.NotFoundHandler())
				url = s.URL
				s.Close()
			} else {
				url = h.server(t, tc.handler).URL
			}
			h.hub.fastPath = NewDefaultFastPathClient(url, to)
			if err := h.turn(t); err != nil || !h.brainCalled() {
				t.Fatalf("turn err=%v brainCalled=%v", err, h.brainCalled())
			}
			if !tc.closed && h.fpCalls.Load() != 1 {
				t.Errorf("expected 1 fast-path call, got %d", h.fpCalls.Load())
			}
			if got := h.agentChat(); len(got) != 1 || got[0] != "brain answer" {
				t.Errorf("chat: %v", got)
			}
		})
	}
}

func TestFastPath_UnsetURLNeverCallsOut(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		cfg  *config.FastPathConfig
	}{
		{"nil", nil},
		{"empty_url", &config.FastPathConfig{URL: "  "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newFastPathHarness(t, tc.cfg)
			if h.hub.fastPath != nil {
				t.Fatal("expected nil fastPath")
			}
			if err := h.turn(t); err != nil || !h.brainCalled() {
				t.Fatalf("turn err=%v brainCalled=%v", err, h.brainCalled())
			}
		})
	}
}

func TestFastPath_ConfiguredURLBuildsClient(t *testing.T) {
	t.Parallel()
	ms := 250
	h := newFastPathHarness(t, &config.FastPathConfig{URL: "http://127.0.0.1:1/intent", TimeoutMS: &ms})
	c, ok := h.hub.fastPath.(*DefaultFastPathClient)
	if !ok || c.timeout != 250*time.Millisecond || c.url != "http://127.0.0.1:1/intent" {
		t.Fatalf("unexpected client %+v", h.hub.fastPath)
	}
	if err := h.turn(t); err != nil || !h.brainCalled() {
		t.Fatalf("turn err=%v brainCalled=%v", err, h.brainCalled())
	}
}

type streamingBrainStub struct{ called atomic.Bool }

func (s *streamingBrainStub) Ask(context.Context, AskRequest, func(string)) (string, error) {
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
	if err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(1600)), "kitchen-ear", "s", func(string, any) error { return nil }, EdgeTimings{}); err != nil {
		t.Fatalf("turn: %v", err)
	}
	if !brain.called.Load() || len(tts.spoken()) != 1 || tts.spoken()[0] != "From the brain." {
		t.Errorf("miss streaming brain: called=%v spoken=%v", brain.called.Load(), tts.spoken())
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
		t.Error("expected error")
	}
}

func TestBrainStageSeconds(t *testing.T) {
	t.Parallel()
	if d, ok := brainStageSeconds(nil, 2*time.Second); !ok || d != 2 {
		t.Fatalf("plain: %v %v", d, ok)
	}
	hit := &fastPathBrain{handled: true, elapsed: 40 * time.Millisecond}
	if _, ok := brainStageSeconds(hit, 50*time.Millisecond); ok {
		t.Fatal("hit must not record brain stage")
	}
	miss := &fastPathBrain{elapsed: 500 * time.Millisecond}
	if d, ok := brainStageSeconds(miss, 2*time.Second); !ok || d != 1.5 {
		t.Fatalf("miss: %v %v", d, ok)
	}
	if d, ok := brainStageSeconds(miss, 100*time.Millisecond); !ok || d != 0 {
		t.Fatalf("clamped: %v %v", d, ok)
	}
}

func TestDefaultFastPathClient_SlowHeadersAreTimeout(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer func() {
		close(release)
		srv.Close()
	}()
	res := NewDefaultFastPathClient(srv.URL, 50*time.Millisecond).Try(context.Background(), AskRequest{Prompt: "x"}, nil)
	if res.Handled || res.Reason != "timeout" {
		t.Fatalf("want unhandled timeout, got %+v", res)
	}
}
