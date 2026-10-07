package voice

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/azylman/mirrormere/internal/config"
	"github.com/azylman/mirrormere/internal/events"
)

// TestDefaultBrainClient_AskStreaming_WireFormat tests that status events call
// onStatus, sentence events deliver text to onAudio, and reply/done close out the call.
func TestDefaultBrainClient_AskStreaming_WireFormat(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		write := func(event, data string) {
			_, _ = w.Write([]byte("event: " + event + "\ndata: " + data + "\n\n"))
			flusher.Flush()
		}
		write("status", `{"status":"thinking"}`)
		write("sentence", `{"text":"It's sunny."}`)
		write("sentence", `{"text":"Bring sunglasses."}`)
		write("reply", `{"reply":"It's sunny. Bring sunglasses."}`)
		write("done", `{}`)
	}))
	defer server.Close()

	var statuses []string
	var chunks []BrainAudioChunk
	b := NewDefaultBrainClient(server.URL, 5).(*DefaultBrainClient)
	reply, err := b.AskStreaming(
		context.Background(),
		AskRequest{Prompt: "what's the weather", SessionID: "s1"},
		func(status string) { statuses = append(statuses, status) },
		func(chunk BrainAudioChunk) { chunks = append(chunks, chunk) },
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reply != "It's sunny. Bring sunglasses." {
		t.Errorf("expected full reply, got %q", reply)
	}
	if len(statuses) != 1 || statuses[0] != "thinking" {
		t.Errorf("expected 1 status 'thinking', got %+v", statuses)
	}
	if len(chunks) != 2 {
		t.Fatalf("expected 2 sentence chunks, got %d: %+v", len(chunks), chunks)
	}
	if chunks[0].Text != "It's sunny." {
		t.Errorf("unexpected chunk 0: %+v", chunks[0])
	}
	if chunks[1].Text != "Bring sunglasses." {
		t.Errorf("unexpected chunk 1: %+v", chunks[1])
	}
}

// TestDefaultBrainClient_AskStreaming_WhitespaceSentenceIgnored covers an empty
// or whitespace sentence event, ensuring it is ignored.
func TestDefaultBrainClient_AskStreaming_WhitespaceSentenceIgnored(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		write := func(event, data string) {
			_, _ = w.Write([]byte("event: " + event + "\ndata: " + data + "\n\n"))
			flusher.Flush()
		}
		write("sentence", `{"text":"   "}`)
		write("sentence", `{"text":"Valid sentence."}`)
		write("reply", `{"reply":"Valid sentence."}`)
		write("done", `{}`)
	}))
	defer server.Close()

	var chunks []BrainAudioChunk
	b := NewDefaultBrainClient(server.URL, 5).(*DefaultBrainClient)
	reply, err := b.AskStreaming(
		context.Background(),
		AskRequest{Prompt: "hi", SessionID: "s1"},
		nil,
		func(chunk BrainAudioChunk) { chunks = append(chunks, chunk) },
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reply != "Valid sentence." {
		t.Errorf("expected reply preserved, got %q", reply)
	}
	if len(chunks) != 1 {
		t.Fatalf("expected onAudio to fire once for valid sentence, got %d", len(chunks))
	}
	if chunks[0].Text != "Valid sentence." {
		t.Errorf("expected valid text, got %+v", chunks[0])
	}
}

// TestDefaultBrainClient_AskStreaming_PlainAskFallsBackCleanly covers a
// brain URL that behaves like plain /ask (no `sentence` events at all):
// AskStreaming must behave exactly like Ask — onAudio never fires, but the
// reply still comes through.
func TestDefaultBrainClient_AskStreaming_PlainAskFallsBackCleanly(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = w.Write([]byte("event: reply\ndata: {\"reply\":\"plain answer\"}\n\n"))
		flusher.Flush()
		_, _ = w.Write([]byte("event: done\ndata: {}\n\n"))
		flusher.Flush()
	}))
	defer server.Close()

	audioCalled := false
	b := NewDefaultBrainClient(server.URL, 5).(*DefaultBrainClient)
	reply, err := b.AskStreaming(
		context.Background(),
		AskRequest{Prompt: "hi", SessionID: "s1"},
		nil,
		func(chunk BrainAudioChunk) { audioCalled = true },
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reply != "plain answer" {
		t.Errorf("expected reply, got %q", reply)
	}
	if audioCalled {
		t.Error("expected onAudio never called for a plain /ask-shaped stream")
	}
}

// mockStreamingBrain implements AudioStreamingBrainClient: Ask behaves like
// mockBrain, and AskStreaming additionally replays a fixed list of
// BrainAudioChunk sentence events through onAudio, in order, before
// returning the same reply/err as Ask would.
type mockStreamingBrain struct {
	mu        sync.Mutex
	reply     string
	statuses  []string
	sentences []BrainAudioChunk
	err       error

	lastReq            AskRequest
	askCalled          bool
	askStreamingCalled bool
}

func (m *mockStreamingBrain) Ask(ctx context.Context, req AskRequest, onStatus func(status string)) (string, error) {
	m.mu.Lock()
	m.askCalled = true
	m.lastReq = req
	statuses := append([]string(nil), m.statuses...)
	reply, err := m.reply, m.err
	m.mu.Unlock()
	for _, s := range statuses {
		if onStatus != nil {
			onStatus(s)
		}
	}
	return reply, err
}

func (m *mockStreamingBrain) AskStreaming(ctx context.Context, req AskRequest, onStatus func(status string), onAudio func(chunk BrainAudioChunk)) (string, error) {
	m.mu.Lock()
	m.askStreamingCalled = true
	m.lastReq = req
	statuses := append([]string(nil), m.statuses...)
	sentences := append([]BrainAudioChunk(nil), m.sentences...)
	reply, err := m.reply, m.err
	m.mu.Unlock()
	for _, s := range statuses {
		if onStatus != nil {
			onStatus(s)
		}
	}
	for _, c := range sentences {
		if onAudio != nil {
			onAudio(c)
		}
	}
	return reply, err
}

type recordedEvent struct {
	Event string
	Data  any
}

func collectEvents() (func(event string, data any) error, func() []recordedEvent) {
	var mu sync.Mutex
	var evs []recordedEvent
	sink := func(event string, data any) error {
		mu.Lock()
		defer mu.Unlock()
		evs = append(evs, recordedEvent{Event: event, Data: data})
		return nil
	}
	get := func() []recordedEvent {
		mu.Lock()
		defer mu.Unlock()
		out := make([]recordedEvent, len(evs))
		copy(out, evs)
		return out
	}
	return sink, get
}

func extractAudioChunkEvents(t *testing.T, evs []recordedEvent) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, e := range evs {
		if e.Event == "audio_chunk" {
			m, ok := e.Data.(map[string]any)
			if !ok {
				t.Fatalf("audio_chunk event data was not map[string]any: %T", e.Data)
			}
			out = append(out, m)
		}
	}
	return out
}

func eventSequence(evs []recordedEvent) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.Event
	}
	return out
}

// TestHub_Interact_StreamingBrain_SentenceAudio covers a brain that
// implements AudioStreamingBrainClient and streams sentence audio: the hub
// must emit one audio_chunk per sentence IMMEDIATELY as each arrives (in
// order, chunk_index incrementing from 0, is_final:false — the Hub doesn't
// know in advance how many sentences there will be), followed by one more
// empty-data audio_chunk with is_final:true once the brain call returns
// (the completion marker). It must NOT call its own TTS (no double
// speech), and the `reply` event must still be sent (after the audio,
// since it isn't known until the brain call returns).
func TestHub_Interact_StreamingBrain_SentenceSynthesizedViaTTS(t *testing.T) {
	t.Parallel()

	cfg := &config.VoiceHubConfig{Enabled: true}
	hubEvents := events.NewHub(events.HubConfig{}, nil, nil)
	defer hubEvents.Close()
	coord := NewCoordinator(hubEvents)

	stt := &mockSTT{text: "what's the weather"}
	brain := &mockStreamingBrain{
		reply:    "It's sunny. High of seventy two. Bring sunglasses.",
		statuses: []string{"checking weather"},
		sentences: []BrainAudioChunk{
			{Text: "It's sunny."},
			{Text: "High of seventy two."},
			{Text: "Bring sunglasses."},
		},
	}
	tts := &mockTTS{audio: []byte("synthesized-pcm"), format: "pcm"}

	h := NewHub(cfg, coord, WithSTTClient(stt), WithBrainClient(brain), WithTTSClient(tts))

	sink, getEvents := collectEvents()

	wav := makeValidWAV(1600)
	if err := h.Interact(context.Background(), bytes.NewReader(wav), "kiosk-kitchen", "sess-1", sink, EdgeTimings{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	evs := getEvents()

	if !brain.askStreamingCalled {
		t.Error("expected AskStreaming to be called on a streaming-capable brain")
	}
	if brain.askCalled {
		t.Error("expected Ask NOT to be called when AskStreaming is available")
	}
	if tts.calls != 3 {
		t.Errorf("expected 3 TTS calls for 3 sentences, got %d calls: %+v", tts.calls, tts.calledTexts)
	}

	chunks := extractAudioChunkEvents(t, evs)
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 audio_chunk events (data + completion marker), got %d", len(chunks))
	}
	finalChunk := chunks[len(chunks)-1]
	if final, ok := finalChunk["is_final"].(bool); !ok || !final {
		t.Errorf("expected final chunk is_final: true, got %v", finalChunk["is_final"])
	}
}


// recordingStateSink captures every voice.state the coordinator publishes,
// which is what the touch kiosk HUD renders (not the dock's SSE stream).
type recordingStateSink struct {
	mu     sync.Mutex
	states []events.VoiceStateData
}

func (r *recordingStateSink) SetVoiceState(s *events.VoiceStateData) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.states = append(r.states, *s)
}

// TestHub_Interact_StreamingBrain_KioskCaption proves the kiosk caption is
// populated during a streaming turn: it grows sentence by sentence, then
// shows the brain's final reply before the coordinator resets to idle.
func TestHub_Interact_StreamingBrain_KioskCaption(t *testing.T) {
	t.Parallel()

	cfg := &config.VoiceHubConfig{Enabled: true}
	rec := &recordingStateSink{}
	coord := NewCoordinator(nil, rec)

	stt := &mockSTT{text: "weather?"}
	brain := &mockStreamingBrain{
		reply: "It's sunny. High of 72.",
		sentences: []BrainAudioChunk{
			{Text: "It's sunny."},
			{Text: "High of 72."},
		},
	}
	tts := &mockTTS{audio: []byte("local"), format: "pcm"}
	h := NewHub(cfg, coord, WithSTTClient(stt), WithBrainClient(brain), WithTTSClient(tts))

	sink, _ := collectEvents()
	if err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(1600)), "kiosk-kitchen", "sess-1", sink, EdgeTimings{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rec.mu.Lock()
	var speaking []string
	for _, s := range rec.states {
		if s.State == StateSpeaking {
			if s.Reply == nil {
				rec.mu.Unlock()
				t.Fatalf("speaking state published with nil reply: kiosk caption would be blank")
			}
			speaking = append(speaking, *s.Reply)
		}
	}
	want := []string{"It's sunny.", "It's sunny. High of 72.", "It's sunny. High of 72."}
	if len(speaking) != len(want) {
		rec.mu.Unlock()
		t.Fatalf("expected %d speaking states %q, got %d %q", len(want), want, len(speaking), speaking)
	}
	for i := range want {
		if speaking[i] != want[i] {
			t.Errorf("speaking state %d: want caption %q, got %q", i, want[i], speaking[i])
		}
	}
	if last := rec.states[len(rec.states)-1]; last.State != StateSpeaking {
		t.Errorf("expected final state speaking, got %s", last.State)
	}
	rec.mu.Unlock()

	coord.Reset()

	rec.mu.Lock()
	if last := rec.states[len(rec.states)-1]; last.State != StateIdle {
		t.Errorf("expected final state idle after reset, got %s", last.State)
	}
	rec.mu.Unlock()
}

// TestHub_Interact_StreamingBrain_PCM_BufferingAndFlush tests that raw PCM streams
// are buffered to ~100ms (4800 bytes) and any trailing bytes are flushed on stream completion.
func TestHub_Interact_StreamingBrain_PCM_BufferingAndFlush(t *testing.T) {
	t.Parallel()

	cfg := &config.VoiceHubConfig{Enabled: true}
	stt := &mockSTT{text: "stream pcm"}
	brain := &mockStreamingBrain{
		reply: "Streaming PCM audio test.",
		sentences: []BrainAudioChunk{
			{Text: "One."},
			{Text: "Two."},
			{Text: "Three."},
		},
	}
	tts := &mockStreamingTTS{
		streamingFn: func(ctx context.Context, text string, onChunk func(TTSAudioChunk) error) error {
			size := 3000
			if text == "Three." {
				size = 2000
			}
			return onChunk(TTSAudioChunk{Data: make([]byte, size), Format: "pcm", SampleRate: 24000, Channels: 1})
		},
	}
	h := NewHub(cfg, nil, WithSTTClient(stt), WithBrainClient(brain), WithTTSClient(tts))

	sink, getEvents := collectEvents()
	if err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(1600)), "kiosk-kitchen", "sess-1", sink, EdgeTimings{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var pcmChunks []map[string]any
	for _, e := range getEvents() {
		if e.Event == "audio_chunk" {
			dataMap, ok := e.Data.(map[string]any)
			if !ok {
				t.Fatalf("unexpected audio_chunk payload type: %T", e.Data)
			}
			pcmChunks = append(pcmChunks, dataMap)
		}
	}

	// We expect 3 audio_chunk events:
	// Chunk 0: 4800 bytes, is_final: false
	// Chunk 1: 3200 bytes (flushed remainder), is_final: false
	// Chunk 2: 0 bytes (completion marker), is_final: true
	if len(pcmChunks) != 3 {
		t.Fatalf("expected 3 audio_chunk events (1 buffered + 1 flushed remainder + 1 completion marker), got %d", len(pcmChunks))
	}

	// Verify Chunk 0
	c0 := pcmChunks[0]
	if c0["format"] != "pcm" || c0["is_final"] != false || c0["sample_rate"] != 24000 || c0["channels"] != 1 {
		t.Errorf("chunk 0 mismatch: %v", c0)
	}
	dec0, _ := base64.StdEncoding.DecodeString(c0["data"].(string))
	if len(dec0) != 4800 {
		t.Errorf("expected chunk 0 length 4800, got %d", len(dec0))
	}

	// Verify Chunk 1 (flushed trailing bytes)
	c1 := pcmChunks[1]
	if c1["format"] != "pcm" || c1["is_final"] != false || c1["sample_rate"] != 24000 || c1["channels"] != 1 {
		t.Errorf("chunk 1 mismatch: %v", c1)
	}
	dec1, _ := base64.StdEncoding.DecodeString(c1["data"].(string))
	if len(dec1) != 3200 {
		t.Errorf("expected chunk 1 length 3200, got %d", len(dec1))
	}

	// Verify Chunk 2 (final marker)
	c2 := pcmChunks[2]
	if c2["format"] != "pcm" || c2["is_final"] != true || c2["data"] != "" {
		t.Errorf("chunk 2 marker mismatch: %v", c2)
	}
}

type mockStreamingTTS struct {
	mu          sync.Mutex
	streamingFn func(ctx context.Context, text string, onChunk func(TTSAudioChunk) error) error
	streamCalls []string
}

func (m *mockStreamingTTS) Synthesize(ctx context.Context, text string, onChunk func(chunk TTSAudioChunk) error) error {
	m.mu.Lock()
	m.streamCalls = append(m.streamCalls, text)
	fn := m.streamingFn
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, text, onChunk)
	}
	if err := onChunk(TTSAudioChunk{Data: make([]byte, 2400), Format: "pcm", SampleRate: 24000, Channels: 1}); err != nil {
		return err
	}
	return onChunk(TTSAudioChunk{Data: make([]byte, 2400), Format: "pcm", SampleRate: 24000, Channels: 1})
}

// TestHub_Interact_StreamingBrain_RemoteTTS_StreamingTTSClient tests that when the
// brain streams text-only sentences, a StreamingTTSClient synthesizes and flushes
// audio chunks in real-time as chunks arrive from TTS.
func TestHub_Interact_StreamingBrain_RemoteTTS_StreamingTTSClient(t *testing.T) {
	t.Parallel()

	cfg := &config.VoiceHubConfig{Enabled: true}
	stt := &mockSTT{text: "what is the news"}
	brain := &mockStreamingBrain{
		reply: "Sentence one. Sentence two.",
		sentences: []BrainAudioChunk{
			{Text: "Sentence one."},
			{Text: "Sentence two."},
		},
	}
	tts := &mockStreamingTTS{}
	h := NewHub(cfg, nil, WithSTTClient(stt), WithBrainClient(brain), WithTTSClient(tts))

	sink, getEvents := collectEvents()
	if err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(1600)), "kiosk-kitchen", "sess-1", sink, EdgeTimings{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tts.mu.Lock()
	streamCalls := append([]string(nil), tts.streamCalls...)
	tts.mu.Unlock()

	if len(streamCalls) != 2 || streamCalls[0] != "Sentence one." || streamCalls[1] != "Sentence two." {
		t.Errorf("unexpected streaming calls: %v", streamCalls)
	}

	chunks := extractAudioChunkEvents(t, getEvents())
	// Sentence 1: two 2400-byte chunks -> one 4800-byte chunk flushed
	// Sentence 2: two 2400-byte chunks -> one 4800-byte chunk flushed
	// Final: completion marker
	if len(chunks) != 3 {
		t.Fatalf("expected 3 audio chunks (2 data + 1 completion marker), got %d", len(chunks))
	}

	for i := 0; i < 2; i++ {
		c := chunks[i]
		if c["format"] != "pcm" || c["is_final"] != false || c["sample_rate"] != 24000 || c["channels"] != 1 {
			t.Errorf("chunk %d mismatch: %v", i, c)
		}
		dec, _ := base64.StdEncoding.DecodeString(c["data"].(string))
		if len(dec) != 4800 {
			t.Errorf("chunk %d expected 4800 bytes, got %d", i, len(dec))
		}
	}

	finalMarker := chunks[2]
	if finalMarker["format"] != "pcm" || finalMarker["is_final"] != true || finalMarker["data"] != "" {
		t.Errorf("final marker mismatch: %v", finalMarker)
	}
}

// TestHub_Interact_NonStreamingBrain_StreamingTTSClient tests that for a non-streaming brain
// (or plain Ask), a StreamingTTSClient streams response chunks into the sink in real-time
// and transitions coordinator to StateSpeaking on chunk 0.
func TestHub_Interact_NonStreamingBrain_StreamingTTSClient(t *testing.T) {
	t.Parallel()

	cfg := &config.VoiceHubConfig{Enabled: true}
	rec := &recordingStateSink{}
	coord := NewCoordinator(nil, rec)
	stt := &mockSTT{text: "hello"}
	brain := &mockBrain{reply: "Full non-streaming reply."}
	tts := &mockStreamingTTS{}

	h := NewHub(cfg, coord, WithSTTClient(stt), WithBrainClient(brain), WithTTSClient(tts))

	sink, getEvents := collectEvents()
	if err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(1600)), "kiosk-kitchen", "sess-1", sink, EdgeTimings{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	chunks := extractAudioChunkEvents(t, getEvents())
	// Expect 1 buffered 4800-byte chunk (is_final: false) + 1 completion marker (is_final: true)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 audio chunks, got %d", len(chunks))
	}

	c0 := chunks[0]
	if c0["format"] != "pcm" || c0["is_final"] != false || c0["chunk_index"] != 0 {
		t.Errorf("chunk 0 mismatch: %v", c0)
	}
	dec, _ := base64.StdEncoding.DecodeString(c0["data"].(string))
	if len(dec) != 4800 {
		t.Errorf("chunk 0 expected 4800 bytes, got %d", len(dec))
	}

	c1 := chunks[1]
	if c1["format"] != "pcm" || c1["is_final"] != true || c1["data"] != "" || c1["chunk_index"] != 1 {
		t.Errorf("chunk 1 mismatch: %v", c1)
	}

	rec.mu.Lock()
	states := rec.states
	rec.mu.Unlock()

	var spoke bool
	for _, s := range states {
		if s.State == StateSpeaking && s.Reply != nil && *s.Reply == "Full non-streaming reply." {
			spoke = true
			break
		}
	}
	if !spoke {
		t.Errorf("expected StateSpeaking state with reply 'Full non-streaming reply.'")
	}
}

// TestHub_Interact_NonStreamingBrain_StreamingTTSClient_FallbackOnError verifies that if
// StreamingTTSClient.SynthesizeStreaming fails, the hub seamlessly falls back to plain
// Synthesize and emits a single is_final: true chunk.
func TestHub_Interact_NonStreamingBrain_StreamingTTSClient_FallbackOnError(t *testing.T) {
	t.Parallel()

	cfg := &config.VoiceHubConfig{Enabled: true}
	stt := &mockSTT{text: "hello"}
	brain := &mockBrain{reply: "Fallback test."}
	tts := &mockStreamingTTS{
		streamingFn: func(ctx context.Context, text string, onChunk func(TTSAudioChunk) error) error {
			return errors.New("streaming connection dropped")
		},
	}

	h := NewHub(cfg, nil, WithSTTClient(stt), WithBrainClient(brain), WithTTSClient(tts))

	sink, getEvents := collectEvents()
	if err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(1600)), "kiosk-kitchen", "sess-1", sink, EdgeTimings{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !hasErrorCode(getEvents(), "tts_error") {
		t.Errorf("expected tts_error in sink")
	}
}

// TestHub_Interact_NonStreamingBrain_StreamingTTSClient_MP3FailsFast: non-PCM
// TTS audio aborts the turn with ErrUnsupportedAudioFormat and an error event.
func TestHub_Interact_NonStreamingBrain_StreamingTTSClient_MP3FailsFast(t *testing.T) {
	t.Parallel()

	tts := &mockStreamingTTS{
		streamingFn: func(ctx context.Context, text string, onChunk func(TTSAudioChunk) error) error {
			return onChunk(TTSAudioChunk{Data: []byte("mp3-data-frame"), Format: "mp3", SampleRate: 44100, Channels: 2})
		},
	}
	h := NewHub(&config.VoiceHubConfig{Enabled: true}, nil, WithSTTClient(&mockSTT{text: "play mp3"}), WithBrainClient(&mockBrain{reply: "Streaming MP3 audio reply."}), WithTTSClient(tts))

	sink, getEvents := collectEvents()
	err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(1600)), "kiosk-kitchen", "sess-1", sink, EdgeTimings{})
	if !errors.Is(err, ErrUnsupportedAudioFormat) {
		t.Fatalf("expected ErrUnsupportedAudioFormat, got %v", err)
	}
	evs := getEvents()
	if n := len(extractAudioChunkEvents(t, evs)); n != 0 {
		t.Errorf("expected no audio chunks, got %d", n)
	}
	if seq := eventSequence(evs); len(seq) == 0 || seq[len(seq)-1] != "error" {
		t.Errorf("expected trailing error event, got %v", seq)
	}
}

// TestHub_Interact_NonStreamingBrain_StreamingTTSClient_PCMRemainder tests that unaligned
// PCM slices (<4800 bytes) are flushed at the end of the stream.
func TestHub_Interact_NonStreamingBrain_StreamingTTSClient_PCMRemainder(t *testing.T) {
	t.Parallel()

	cfg := &config.VoiceHubConfig{Enabled: true}
	stt := &mockSTT{text: "hello"}
	brain := &mockBrain{reply: "Short reply."}
	tts := &mockStreamingTTS{
		streamingFn: func(ctx context.Context, text string, onChunk func(TTSAudioChunk) error) error {
			// Emits 2000 bytes (less than 4800 floor)
			return onChunk(TTSAudioChunk{
				Data:       make([]byte, 2000),
				Format:     "pcm",
				SampleRate: 24000,
				Channels:   1,
			})
		},
	}

	h := NewHub(cfg, nil, WithSTTClient(stt), WithBrainClient(brain), WithTTSClient(tts))

	sink, getEvents := collectEvents()
	if err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(1600)), "kiosk-kitchen", "sess-1", sink, EdgeTimings{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	chunks := extractAudioChunkEvents(t, getEvents())
	if len(chunks) != 2 {
		t.Fatalf("expected 2 audio chunks (1 flushed remainder + 1 completion marker), got %d", len(chunks))
	}
	dec, _ := base64.StdEncoding.DecodeString(chunks[0]["data"].(string))
	if len(dec) != 2000 {
		t.Errorf("expected 2000 bytes flushed, got %d", len(dec))
	}
	if chunks[0]["is_final"] != false {
		t.Errorf("expected chunk 0 is_final: false")
	}
	if chunks[1]["is_final"] != true {
		t.Errorf("expected chunk 1 is_final: true")
	}
}

// TestHub_Interact_EmptyReplyDoesNotSynthesize tests that if brain returns empty or whitespace reply,
// synthesizeAndEmitReply returns immediately with no TTS calls.
func TestHub_Interact_EmptyReplyDoesNotSynthesize(t *testing.T) {
	t.Parallel()

	cfg := &config.VoiceHubConfig{Enabled: true}
	stt := &mockSTT{text: "hello"}
	brain := &mockBrain{reply: "   "}
	tts := &mockStreamingTTS{}

	h := NewHub(cfg, nil, WithSTTClient(stt), WithBrainClient(brain), WithTTSClient(tts))

	sink, _ := collectEvents()
	if err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(1600)), "kiosk-kitchen", "sess-1", sink, EdgeTimings{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(tts.streamCalls) != 0 {
		t.Errorf("expected 0 TTS calls for whitespace reply")
	}
}

// TestHub_Interact_StreamingBrain_RemoteTTS_PlainTTSClient tests that if h.tts does not implement
// StreamingTTSClient, the hub synthesizes each sentence using unary Synthesize.
func TestHub_Interact_StreamingBrain_RemoteTTS_PlainTTSClient(t *testing.T) {
	t.Parallel()

	cfg := &config.VoiceHubConfig{Enabled: true}
	stt := &mockSTT{text: "stream text"}
	brain := &mockStreamingBrain{
		reply: "First sentence. Second sentence.",
		sentences: []BrainAudioChunk{
			{Text: "First sentence."},
			{Text: "Second sentence."},
		},
	}
	tts := &mockTTS{audio: makeValidWAV(1600), format: "pcm"}
	h := NewHub(cfg, nil, WithSTTClient(stt), WithBrainClient(brain), WithTTSClient(tts))

	sink, getEvents := collectEvents()
	if err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(1600)), "kiosk-kitchen", "sess-1", sink, EdgeTimings{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if tts.calls != 2 {
		t.Errorf("expected 2 unary TTS calls, got %d", tts.calls)
	}
	chunks := extractAudioChunkEvents(t, getEvents())
	// 2 sentences + 1 completion marker
	if len(chunks) != 3 {
		t.Fatalf("expected 3 audio chunks, got %d", len(chunks))
	}
}

// TestHub_Interact_StreamingBrain_RemoteTTS_AllSentencesFailedFallback tests that if every
// sentence synthesis fails in remote-tts mode, the hub falls back to synthesizing the full reply.
func TestHub_Interact_StreamingBrain_RemoteTTS_AllSentencesFailedFallback(t *testing.T) {
	t.Parallel()

	cfg := &config.VoiceHubConfig{Enabled: true}
	stt := &mockSTT{text: "stream text"}
	brain := &mockStreamingBrain{
		reply: "Failing sentence.",
		sentences: []BrainAudioChunk{
			{Text: "Failing sentence."},
		},
	}
	// StreamingTTS fails during sentence synthesis, but succeeds for full reply fallback
	var mu sync.Mutex
	sentenceCalled := false
	fallbackCalled := false
	tts := &mockStreamingTTS{
		streamingFn: func(ctx context.Context, text string, onChunk func(TTSAudioChunk) error) error {
			mu.Lock()
			defer mu.Unlock()
			if !sentenceCalled {
				sentenceCalled = true
				return errors.New("synthesis failed on sentence")
			}
			fallbackCalled = true
			return onChunk(TTSAudioChunk{Data: make([]byte, 4800), Format: "pcm", SampleRate: 24000, Channels: 1})
		},
	}

	h := NewHub(cfg, nil, WithSTTClient(stt), WithBrainClient(brain), WithTTSClient(tts))

	sink, getEvents := collectEvents()
	if err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(1600)), "kiosk-kitchen", "sess-1", sink, EdgeTimings{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mu.Lock()
	sc, fc := sentenceCalled, fallbackCalled
	mu.Unlock()

	if !sc || !fc {
		t.Errorf("expected sentence attempt and fallback attempt: sc=%v, fc=%v", sc, fc)
	}

	chunks := extractAudioChunkEvents(t, getEvents())
	if len(chunks) != 2 {
		t.Fatalf("expected 2 audio chunks from fallback (1 data + 1 marker), got %d", len(chunks))
	}
}

// TestHub_Interact_StreamingBrain_RemoteTTS_MP3FailsFast: non-PCM TTS audio in
// remote-tts mode aborts the turn.
func TestHub_Interact_StreamingBrain_RemoteTTS_MP3FailsFast(t *testing.T) {
	t.Parallel()

	brain := &mockStreamingBrain{
		reply:     "MP3 sentence.",
		sentences: []BrainAudioChunk{{Text: "MP3 sentence."}},
	}
	tts := &mockStreamingTTS{
		streamingFn: func(ctx context.Context, text string, onChunk func(TTSAudioChunk) error) error {
			return onChunk(TTSAudioChunk{Data: []byte("mp3-raw-frame"), Format: "mp3", SampleRate: 44100, Channels: 2})
		},
	}
	h := NewHub(&config.VoiceHubConfig{Enabled: true}, nil, WithSTTClient(&mockSTT{text: "stream mp3"}), WithBrainClient(brain), WithTTSClient(tts))

	sink, getEvents := collectEvents()
	err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(1600)), "kiosk-kitchen", "sess-1", sink, EdgeTimings{})
	if !errors.Is(err, ErrUnsupportedAudioFormat) {
		t.Fatalf("expected ErrUnsupportedAudioFormat, got %v", err)
	}
	if n := len(extractAudioChunkEvents(t, getEvents())); n != 0 {
		t.Errorf("expected no audio chunks, got %d", n)
	}
}

// --- PCM rate change mid-turn fails fast ---

func TestHub_Interact_StreamingBrain_RemoteTTS_RateChangeFailsFast(t *testing.T) {
	t.Parallel()

	brain := &mockStreamingBrain{
		reply: "Sentence one. Sentence two.",
		sentences: []BrainAudioChunk{
			{Text: "Sentence one."},
			{Text: "Sentence two."},
		},
	}
	tts := &mockStreamingTTS{
		streamingFn: func(ctx context.Context, text string, onChunk func(TTSAudioChunk) error) error {
			rate := 24000
			if text == "Sentence two." {
				rate = 22050
			}
			return onChunk(TTSAudioChunk{Data: make([]byte, 1000), Format: "pcm", SampleRate: rate, Channels: 1})
		},
	}
	h := NewHub(&config.VoiceHubConfig{Enabled: true}, nil, WithSTTClient(&mockSTT{text: "q"}), WithBrainClient(brain), WithTTSClient(tts))

	sink, getEvents := collectEvents()
	err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(1600)), "kiosk-kitchen", "sess-1", sink, EdgeTimings{})
	if !errors.Is(err, ErrMixedPCMFormat) {
		t.Fatalf("expected ErrMixedPCMFormat (wrapping ErrMixedAudioStream), got %v", err)
	}
	for _, c := range extractAudioChunkEvents(t, getEvents()) {
		if c["sample_rate"] == 22050 {
			t.Errorf("audio at the changed rate must not be emitted: %v", c)
		}
	}
}

func TestHub_SynthesizeAndEmitReply_RateChangeFailsFast(t *testing.T) {
	t.Parallel()

	tts := &mockStreamingTTS{
		streamingFn: func(ctx context.Context, text string, onChunk func(TTSAudioChunk) error) error {
			if err := onChunk(TTSAudioChunk{Data: make([]byte, 1000), Format: "pcm", SampleRate: 24000, Channels: 1}); err != nil {
				return err
			}
			return onChunk(TTSAudioChunk{Data: make([]byte, 600), Format: "pcm", SampleRate: 22050, Channels: 1})
		},
	}
	h := NewHub(&config.VoiceHubConfig{Enabled: true}, nil, WithTTSClient(tts))
	sink, getEvents := collectEvents()
	tr := "q"
	status, err := h.synthesizeAndEmitReply(context.Background(), "hello there", "n1", "e", &tr, sink)
	if !errors.Is(err, ErrMixedPCMFormat) || status != "error" {
		t.Fatalf("expected status error + ErrMixedPCMFormat, got %q, %v", status, err)
	}
	seq := eventSequence(getEvents())
	if len(seq) == 0 || seq[len(seq)-1] != "error" {
		t.Errorf("expected trailing error event, got %v", seq)
	}
}

// --- #357 / #358 / #359: DefaultTTSClient ---

func riffChunk(id string, body []byte) []byte {
	out := append([]byte(id), 0, 0, 0, 0)
	binary.LittleEndian.PutUint32(out[4:], uint32(len(body)))
	out = append(out, body...)
	if len(body)%2 == 1 {
		out = append(out, 0)
	}
	return out
}

func fmtBody(tag uint16, ch uint16, rate uint32, bits uint16, extra int) []byte {
	b := make([]byte, 16+extra)
	binary.LittleEndian.PutUint16(b[0:], tag)
	binary.LittleEndian.PutUint16(b[2:], ch)
	binary.LittleEndian.PutUint32(b[4:], rate)
	binary.LittleEndian.PutUint32(b[8:], rate*uint32(ch)*uint32(bits)/8)
	binary.LittleEndian.PutUint16(b[12:], ch*bits/8)
	binary.LittleEndian.PutUint16(b[14:], bits)
	return b
}

func buildWAV(chunks ...[]byte) []byte {
	out := []byte("RIFF\x00\x00\x00\x00WAVE")
	for _, c := range chunks {
		out = append(out, c...)
	}
	binary.LittleEndian.PutUint32(out[4:], uint32(len(out)-8))
	return out
}

func synthesizeFromServer(t *testing.T, contentType string, hdr map[string]string, body []byte) []TTSAudioChunk {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		for k, v := range hdr {
			w.Header().Set(k, v)
		}
		_, _ = w.Write(body)
	}))
	defer ts.Close()
	var chunks []TTSAudioChunk
	err := NewDefaultTTSClient(ts.URL, "m", "v", 5).Synthesize(context.Background(), "hello", func(c TTSAudioChunk) error {
		chunks = append(chunks, c)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return chunks
}

func concatData(chunks []TTSAudioChunk) []byte {
	var out []byte
	for _, c := range chunks {
		out = append(out, c.Data...)
	}
	return out
}

func TestDefaultTTSClient_WAVChunkWalking(t *testing.T) {
	t.Parallel()
	pcm := bytes.Repeat([]byte{0x11, 0x22}, 5000) // 10000 bytes

	cases := map[string][]byte{
		"LIST chunk before data": buildWAV(
			riffChunk("fmt ", fmtBody(1, 1, 22050, 16, 0)),
			riffChunk("LIST", []byte("INFOISFT\x05\x00\x00\x00Lavf\x00")),
			riffChunk("data", pcm)),
		"fmt size 18": buildWAV(
			riffChunk("fmt ", fmtBody(1, 1, 22050, 16, 2)),
			riffChunk("data", pcm)),
		"WAVE_FORMAT_EXTENSIBLE": buildWAV(
			riffChunk("fmt ", func() []byte {
				b := fmtBody(0xFFFE, 1, 22050, 16, 24)
				binary.LittleEndian.PutUint16(b[16:], 22) // cbSize
				binary.LittleEndian.PutUint16(b[24:], 1)  // subformat PCM
				return b
			}()),
			riffChunk("data", pcm)),
	}
	for name, wav := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			chunks := synthesizeFromServer(t, "audio/wav", nil, wav)
			if !bytes.Equal(concatData(chunks), pcm) {
				t.Fatalf("header bytes leaked into or audio lost from PCM output (got %d bytes)", len(concatData(chunks)))
			}
			for _, c := range chunks {
				if c.Format != "pcm" || c.SampleRate != 22050 || c.Channels != 1 {
					t.Errorf("bad chunk: format=%s rate=%d ch=%d", c.Format, c.SampleRate, c.Channels)
				}
			}
		})
	}

	t.Run("streamed data size 0xFFFFFFFF and 0", func(t *testing.T) {
		t.Parallel()
		for _, size := range []uint32{0xFFFFFFFF, 0} {
			wav := buildWAV(riffChunk("fmt ", fmtBody(1, 2, 16000, 16, 0)))
			hdr := append([]byte("data"), 0, 0, 0, 0)
			binary.LittleEndian.PutUint32(hdr[4:], size)
			wav = append(append(wav, hdr...), pcm...)
			chunks := synthesizeFromServer(t, "audio/wav", nil, wav)
			if !bytes.Equal(concatData(chunks), pcm) {
				t.Fatalf("size %#x: expected all %d PCM bytes, got %d", size, len(pcm), len(concatData(chunks)))
			}
			if chunks[0].SampleRate != 16000 || chunks[0].Channels != 2 {
				t.Errorf("size %#x: bad rate/channels %d/%d", size, chunks[0].SampleRate, chunks[0].Channels)
			}
		}
	})

	t.Run("known data size excludes trailing chunks", func(t *testing.T) {
		t.Parallel()
		wav := buildWAV(riffChunk("fmt ", fmtBody(1, 1, 24000, 16, 0)), riffChunk("data", pcm), riffChunk("LIST", []byte("INFOtrailing!")))
		chunks := synthesizeFromServer(t, "audio/wav", nil, wav)
		if !bytes.Equal(concatData(chunks), pcm) {
			t.Fatalf("expected exactly the data chunk, got %d bytes", len(concatData(chunks)))
		}
	})
}

// #358: non-PCM audio (mp3, or a WAV wrapping something other than 16-bit
// PCM) is unsupported and fails fast rather than being sliced or passed through.
func TestDefaultTTSClient_NonPCMIsUnsupported(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		contentType string
		body        []byte
	}{
		"mp3":                  {"audio/mpeg", bytes.Repeat([]byte("mp3frame"), 2000)},
		"wav float":            {"audio/wav", buildWAV(riffChunk("fmt ", fmtBody(3, 1, 44100, 32, 0)), riffChunk("data", make([]byte, 12000)))},
		"wav 8-bit":            {"audio/wav", buildWAV(riffChunk("fmt ", fmtBody(1, 1, 8000, 8, 0)), riffChunk("data", make([]byte, 4000)))},
		"unknown content type": {"application/octet-stream", make([]byte, 100)},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				_, _ = w.Write(tc.body)
			}))
			defer ts.Close()
			err := NewDefaultTTSClient(ts.URL, "m", "v", 5).Synthesize(context.Background(), "hello", func(c TTSAudioChunk) error {
				t.Errorf("no chunk should be emitted, got format %s", c.Format)
				return nil
			})
			if !errors.Is(err, ErrUnsupportedAudioFormat) {
				t.Fatalf("expected ErrUnsupportedAudioFormat, got %v", err)
			}
		})
	}
}

// #359: Data must not alias the reused read buffer; raw PCM rate comes from
// the response.
func TestDefaultTTSClient_ChunkDataNotAliased(t *testing.T) {
	t.Parallel()
	pcm := make([]byte, 4800*3)
	for i := range pcm {
		pcm[i] = byte(i/4800) + 1
	}
	wav := buildWAV(riffChunk("fmt ", fmtBody(1, 1, 24000, 16, 0)), riffChunk("data", pcm))
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		f := w.(http.Flusher)
		for i := 0; i < len(wav); i += 1000 { // many small flushed writes -> many reads
			end := i + 1000
			if end > len(wav) {
				end = len(wav)
			}
			_, _ = w.Write(wav[i:end])
			f.Flush()
		}
	}))
	defer ts.Close()

	var retained [][]byte
	err := NewDefaultTTSClient(ts.URL, "m", "v", 5).Synthesize(context.Background(), "hi", func(c TTSAudioChunk) error {
		retained = append(retained, c.Data) // retain without copying
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got []byte
	for _, d := range retained {
		got = append(got, d...)
	}
	if !bytes.Equal(got, pcm) {
		t.Fatal("retained chunk Data was corrupted by buffer reuse")
	}
}

func TestDefaultTTSClient_RawPCMRateFromResponse(t *testing.T) {
	t.Parallel()
	pcm := bytes.Repeat([]byte{1, 2}, 1000)

	chunks := synthesizeFromServer(t, "audio/pcm;rate=22050;channels=2", nil, pcm)
	if len(chunks) == 0 || chunks[0].SampleRate != 22050 || chunks[0].Channels != 2 || !bytes.Equal(concatData(chunks), pcm) {
		t.Errorf("content-type params not honoured: %+v", chunks)
	}

	chunks = synthesizeFromServer(t, "audio/pcm", map[string]string{"X-Sample-Rate": "16000"}, pcm)
	if len(chunks) == 0 || chunks[0].SampleRate != 16000 || chunks[0].Channels != 1 {
		t.Errorf("X-Sample-Rate not honoured: %+v", chunks)
	}

	chunks = synthesizeFromServer(t, "audio/pcm", nil, pcm)
	if len(chunks) == 0 || chunks[0].SampleRate != 24000 || chunks[0].Channels != 1 {
		t.Errorf("expected 24000/1 default: %+v", chunks)
	}
}
