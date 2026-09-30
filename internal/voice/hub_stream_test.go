package voice

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/azylman/mirrormere/internal/config"
	"github.com/azylman/mirrormere/internal/events"
)

// TestDefaultBrainClient_AskStreaming_GatewayWireFormat pins the exact wire
// shape the karakos gateway's POST /ask/stream sends (gateway.py's
// _handle_ask_stream / _sse_event("sentence", {"text":..., "engine":...,
// "audio_b64":...})): status events call onStatus, sentence events decode
// audio_b64 and call onAudio with Format "wav" (the gateway's synthesize()
// always returns WAV; there's no explicit "format" field on the wire), and
// reply/done close out the call exactly like plain /ask.
func TestDefaultBrainClient_AskStreaming_GatewayWireFormat(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		write := func(event, data string) {
			_, _ = w.Write([]byte("event: " + event + "\ndata: " + data + "\n\n"))
			flusher.Flush()
		}
		write("status", `{"status":"thinking"}`)
		write("sentence", `{"text":"It's sunny.","engine":"desktop-tts","audio_b64":"`+base64.StdEncoding.EncodeToString([]byte("wav-bytes-one"))+`"}`)
		write("sentence", `{"text":"Bring sunglasses.","engine":"piper","audio_b64":"`+base64.StdEncoding.EncodeToString([]byte("wav-bytes-two"))+`"}`)
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
		t.Fatalf("expected 2 sentence audio chunks, got %d: %+v", len(chunks), chunks)
	}
	if chunks[0].Text != "It's sunny." || string(chunks[0].Data) != "wav-bytes-one" || chunks[0].Format != "wav" {
		t.Errorf("unexpected chunk 0: %+v", chunks[0])
	}
	if chunks[1].Text != "Bring sunglasses." || string(chunks[1].Data) != "wav-bytes-two" || chunks[1].Format != "wav" {
		t.Errorf("unexpected chunk 1: %+v", chunks[1])
	}
}

// TestDefaultBrainClient_AskStreaming_MissingAudioB64 covers a `sentence`
// event with no (or an undecodable) audio_b64 field: onAudio must still
// fire, with an empty Data, so the hub can decide how to fall back — the
// sentence's text must not be silently lost.
func TestDefaultBrainClient_AskStreaming_MissingAudioB64(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		write := func(event, data string) {
			_, _ = w.Write([]byte("event: " + event + "\ndata: " + data + "\n\n"))
			flusher.Flush()
		}
		write("sentence", `{"text":"This one broke.","engine":"none"}`)
		write("reply", `{"reply":"This one broke."}`)
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
	if reply != "This one broke." {
		t.Errorf("expected reply preserved, got %q", reply)
	}
	if len(chunks) != 1 {
		t.Fatalf("expected onAudio to fire once even with no audio, got %d", len(chunks))
	}
	if chunks[0].Text != "This one broke." || len(chunks[0].Data) != 0 {
		t.Errorf("expected empty Data with text preserved, got %+v", chunks[0])
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
func TestHub_Interact_StreamingBrain_SentenceAudio(t *testing.T) {
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
			{Text: "It's sunny.", Format: "wav", Data: []byte("audio-one")},
			{Text: "High of seventy two.", Format: "wav", Data: []byte("audio-two")},
			{Text: "Bring sunglasses.", Format: "wav", Data: []byte("audio-three")},
		},
	}
	tts := &mockTTS{audio: []byte("should-not-be-used"), format: "wav"}

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
	if tts.calls != 0 {
		t.Errorf("expected hub TTS to never be called (no double speech), got %d calls: %+v", tts.calls, tts.calledTexts)
	}

	expectedSequence := []string{"state", "transcript", "status", "audio_chunk", "audio_chunk", "audio_chunk", "audio_chunk", "reply", "done"}
	if got := eventSequence(evs); !equalStrings(got, expectedSequence) {
		t.Fatalf("expected sequence %v, got %v", expectedSequence, got)
	}

	chunks := extractAudioChunkEvents(t, evs)
	if len(chunks) != 4 {
		t.Fatalf("expected 4 audio_chunk events (3 sentences + 1 completion marker), got %d", len(chunks))
	}
	wantData := []string{"audio-one", "audio-two", "audio-three", ""}
	for i, c := range chunks {
		if idx, ok := c["chunk_index"].(int); !ok || idx != i {
			t.Errorf("chunk %d: expected chunk_index %d, got %v", i, i, c["chunk_index"])
		}
		wantFinal := i == len(chunks)-1
		if final, ok := c["is_final"].(bool); !ok || final != wantFinal {
			t.Errorf("chunk %d: expected is_final=%v, got %v", i, wantFinal, c["is_final"])
		}
		gotData, decErr := base64.StdEncoding.DecodeString(c["data"].(string))
		if decErr != nil {
			t.Fatalf("chunk %d: bad base64: %v", i, decErr)
		}
		if string(gotData) != wantData[i] {
			t.Errorf("chunk %d: expected data %q, got %q", i, wantData[i], string(gotData))
		}
		if c["format"] != "wav" {
			t.Errorf("chunk %d: expected format wav, got %v", i, c["format"])
		}
	}
	// The completion marker (last chunk) must carry empty data, not
	// re-send the last sentence's audio.
	if len(chunks[3]["data"].(string)) != 0 {
		t.Errorf("expected completion marker chunk to have empty data, got %q", chunks[3]["data"])
	}

	if coord.GetState().State != StateIdle {
		t.Errorf("expected final state idle, got %s", coord.GetState().State)
	}
}

// TestHub_Interact_StreamingBrain_NoSentenceAudio covers a brain that
// implements AudioStreamingBrainClient (so AskStreaming is called) but this
// turn produced no sentence events — e.g. it's pointed at plain /ask rather
// than /ask/stream. Behaviour must be identical to a non-streaming brain:
// reply first, then exactly one hub TTS call producing a single
// is_final:true audio_chunk.
func TestHub_Interact_StreamingBrain_NoSentenceAudio(t *testing.T) {
	t.Parallel()

	cfg := &config.VoiceHubConfig{Enabled: true}
	hubEvents := events.NewHub(events.HubConfig{}, nil, nil)
	defer hubEvents.Close()
	coord := NewCoordinator(hubEvents)

	stt := &mockSTT{text: "turn off the lights"}
	brain := &mockStreamingBrain{reply: "Lights off.", statuses: []string{"turning off lights"}}
	tts := &mockTTS{audio: []byte("fallback-full-reply-audio"), format: "wav"}

	h := NewHub(cfg, coord, WithSTTClient(stt), WithBrainClient(brain), WithTTSClient(tts))

	sink, getEvents := collectEvents()

	wav := makeValidWAV(1600)
	if err := h.Interact(context.Background(), bytes.NewReader(wav), "kiosk-kitchen", "sess-1", sink, EdgeTimings{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	evs := getEvents()

	if tts.calls != 1 {
		t.Fatalf("expected exactly 1 hub TTS call for the full reply, got %d", tts.calls)
	}
	if tts.calledWith != "Lights off." {
		t.Errorf("expected TTS called with full reply text, got %q", tts.calledWith)
	}

	expectedSequence := []string{"state", "transcript", "status", "reply", "audio_chunk", "audio_chunk", "done"}
	if got := eventSequence(evs); !equalStrings(got, expectedSequence) {
		t.Fatalf("expected sequence %v, got %v", expectedSequence, got)
	}

	chunks := extractAudioChunkEvents(t, evs)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 audio_chunk events (1 data + 1 marker), got %d", len(chunks))
	}
	if final, _ := chunks[1]["is_final"].(bool); !final {
		t.Errorf("expected is_final=true on completion marker")
	}
}

// TestHub_Interact_StreamingBrain_MixedAudioStreamRejected covers streaming turns
// that attempt to mix brain audio and text-only sentences: the hub must latch to
// either brain-audio or remote-tts on chunk 0 and reject conflicting chunks with
// ErrMixedAudioStream.
func TestHub_Interact_StreamingBrain_MixedAudioStreamRejected(t *testing.T) {
	t.Parallel()

	t.Run("brain_audio_latched_rejects_text_only", func(t *testing.T) {
		t.Parallel()
		cfg := &config.VoiceHubConfig{Enabled: true}
		coord := NewCoordinator(nil)

		stt := &mockSTT{text: "tell me a story"}
		brain := &mockStreamingBrain{
			reply: "Once upon a time. The audio broke here. The end.",
			sentences: []BrainAudioChunk{
				{Text: "Once upon a time.", Format: "wav", Data: []byte("audio-one")},
				{Text: "The audio broke here.", Format: "", Data: nil}, // conflicting text-only
			},
		}
		tts := &mockTTS{audio: []byte("fallback-audio"), format: "wav"}
		h := NewHub(cfg, coord, WithSTTClient(stt), WithBrainClient(brain), WithTTSClient(tts))

		sink, getEvents := collectEvents()
		wav := makeValidWAV(1600)
		err := h.Interact(context.Background(), bytes.NewReader(wav), "kiosk-kitchen", "sess-1", sink, EdgeTimings{})
		if !errors.Is(err, ErrMixedAudioStream) {
			t.Fatalf("expected ErrMixedAudioStream, got %v", err)
		}
		if !hasErrorCode(getEvents(), "brain_error") {
			t.Errorf("expected brain_error event in sink")
		}
	})

	t.Run("remote_tts_latched_rejects_brain_audio", func(t *testing.T) {
		t.Parallel()
		cfg := &config.VoiceHubConfig{Enabled: true}
		coord := NewCoordinator(nil)

		stt := &mockSTT{text: "tell me a story"}
		brain := &mockStreamingBrain{
			reply: "Once upon a time. Brain audio suddenly sent.",
			sentences: []BrainAudioChunk{
				{Text: "Once upon a time.", Format: "", Data: nil}, // latches remote-tts
				{Text: "Brain audio suddenly sent.", Format: "wav", Data: []byte("audio-two")}, // conflicting audio
			},
		}
		tts := &mockTTS{audio: []byte("fallback-audio"), format: "wav"}
		h := NewHub(cfg, coord, WithSTTClient(stt), WithBrainClient(brain), WithTTSClient(tts))

		sink, getEvents := collectEvents()
		wav := makeValidWAV(1600)
		err := h.Interact(context.Background(), bytes.NewReader(wav), "kiosk-kitchen", "sess-1", sink, EdgeTimings{})
		if !errors.Is(err, ErrMixedAudioStream) {
			t.Fatalf("expected ErrMixedAudioStream, got %v", err)
		}
		if !hasErrorCode(getEvents(), "brain_error") {
			t.Errorf("expected brain_error event in sink")
		}
	})
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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
			{Text: "It's sunny.", Format: "wav", Data: []byte("a1")},
			{Text: "High of 72.", Format: "wav", Data: []byte("a2")},
		},
	}
	tts := &mockTTS{audio: []byte("local"), format: "wav"}
	h := NewHub(cfg, coord, WithSTTClient(stt), WithBrainClient(brain), WithTTSClient(tts))

	sink, _ := collectEvents()
	if err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(1600)), "kiosk-kitchen", "sess-1", sink, EdgeTimings{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	var speaking []string
	for _, s := range rec.states {
		if s.State == StateSpeaking {
			if s.Reply == nil {
				t.Fatalf("speaking state published with nil reply: kiosk caption would be blank")
			}
			speaking = append(speaking, *s.Reply)
		}
	}
	want := []string{"It's sunny.", "It's sunny. High of 72.", "It's sunny. High of 72."}
	if len(speaking) != len(want) {
		t.Fatalf("expected %d speaking states %q, got %d %q", len(want), want, len(speaking), speaking)
	}
	for i := range want {
		if speaking[i] != want[i] {
			t.Errorf("speaking state %d: want caption %q, got %q", i, want[i], speaking[i])
		}
	}
	if last := rec.states[len(rec.states)-1]; last.State != StateIdle {
		t.Errorf("expected final state idle, got %s", last.State)
	}
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
			{Text: "One.", Format: "pcm", Data: make([]byte, 3000), SampleRate: 24000, Channels: 1},
			{Text: "Two.", Format: "pcm", Data: make([]byte, 3000), SampleRate: 24000, Channels: 1},
			{Text: "Three.", Format: "pcm", Data: make([]byte, 2000), SampleRate: 24000, Channels: 1},
		},
	}
	h := NewHub(cfg, nil, WithSTTClient(stt), WithBrainClient(brain))

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
			{Text: "Sentence one.", Format: "", Data: nil},
			{Text: "Sentence two.", Format: "", Data: nil},
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

// TestHub_Interact_NonStreamingBrain_StreamingTTSClient_MP3 tests streaming non-PCM (MP3)
// audio chunks directly through to the edge dock.
func TestHub_Interact_NonStreamingBrain_StreamingTTSClient_MP3(t *testing.T) {
	t.Parallel()

	cfg := &config.VoiceHubConfig{Enabled: true}
	rec := &recordingStateSink{}
	coord := NewCoordinator(nil, rec)
	stt := &mockSTT{text: "play mp3"}
	brain := &mockBrain{reply: "Streaming MP3 audio reply."}
	tts := &mockStreamingTTS{
		streamingFn: func(ctx context.Context, text string, onChunk func(TTSAudioChunk) error) error {
			// emit empty data chunk first (should be safely ignored)
			_ = onChunk(TTSAudioChunk{Data: nil, Format: "mp3"})
			// emit real mp3 chunk
			return onChunk(TTSAudioChunk{
				Data:       []byte("mp3-data-frame"),
				Format:     "mp3",
				SampleRate: 44100,
				Channels:   2,
			})
		},
	}

	h := NewHub(cfg, coord, WithSTTClient(stt), WithBrainClient(brain), WithTTSClient(tts))

	sink, getEvents := collectEvents()
	if err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(1600)), "kiosk-kitchen", "sess-1", sink, EdgeTimings{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	chunks := extractAudioChunkEvents(t, getEvents())
	if len(chunks) != 2 {
		t.Fatalf("expected 2 audio chunks (1 mp3 + 1 completion marker), got %d", len(chunks))
	}
	if chunks[0]["format"] != "mp3" || chunks[0]["is_final"] != false || chunks[0]["sample_rate"] != 44100 || chunks[0]["channels"] != 2 {
		t.Errorf("chunk 0 mismatch: %v", chunks[0])
	}
	if chunks[1]["is_final"] != true || chunks[1]["format"] != "mp3" {
		t.Errorf("chunk 1 mismatch: %v", chunks[1])
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
			{Text: "First sentence.", Format: "", Data: nil},
			{Text: "Second sentence.", Format: "", Data: nil},
		},
	}
	tts := &mockTTS{audio: makeValidWAV(1600), format: "wav"}
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
			{Text: "Failing sentence.", Format: "", Data: nil},
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

// TestHub_Interact_StreamingBrain_RemoteTTS_StreamingTTSClient_MP3 tests streaming non-PCM (MP3)
// audio chunks directly through to the edge dock in remote-tts mode.
func TestHub_Interact_StreamingBrain_RemoteTTS_StreamingTTSClient_MP3(t *testing.T) {
	t.Parallel()

	cfg := &config.VoiceHubConfig{Enabled: true}
	stt := &mockSTT{text: "stream mp3"}
	brain := &mockStreamingBrain{
		reply: "MP3 sentence.",
		sentences: []BrainAudioChunk{
			{Text: "MP3 sentence.", Format: "", Data: nil},
		},
	}
	tts := &mockStreamingTTS{
		streamingFn: func(ctx context.Context, text string, onChunk func(TTSAudioChunk) error) error {
			return onChunk(TTSAudioChunk{
				Data:       []byte("mp3-raw-frame"),
				Format:     "mp3",
				SampleRate: 44100,
				Channels:   2,
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
		t.Fatalf("expected 2 audio chunks, got %d", len(chunks))
	}
	if chunks[0]["format"] != "mp3" || chunks[0]["is_final"] != false || chunks[0]["sample_rate"] != 44100 || chunks[0]["channels"] != 2 {
		t.Errorf("chunk 0 mismatch: %v", chunks[0])
	}
	if chunks[1]["format"] != "mp3" || chunks[1]["is_final"] != true {
		t.Errorf("chunk 1 marker mismatch: %v", chunks[1])
	}
}





