package voice

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/config"

	"github.com/azylman/mirrormere/internal/events"
	"github.com/prometheus/client_golang/prometheus"
)


type mockSTT struct {
	mu         sync.Mutex
	delay      time.Duration
	text       string
	err        error
	calledWith []byte
}

func (m *mockSTT) Transcribe(ctx context.Context, wavData []byte) (string, error) {
	m.mu.Lock()
	delay := m.delay
	m.calledWith = wavData
	text, err := m.text, m.err
	m.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
	return text, err
}

type mockBrain struct {
	mu         sync.Mutex
	reply      string
	statuses   []string
	err        error
	calledWith string
	lastReq    AskRequest
}

func (m *mockBrain) Ask(ctx context.Context, req AskRequest, onStatus func(status string)) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calledWith = req.Prompt
	m.lastReq = req
	for _, s := range m.statuses {
		if onStatus != nil {
			onStatus(s)
		}
	}
	return m.reply, m.err
}

type mockTTS struct {
	mu          sync.Mutex
	delay       time.Duration
	audio       []byte
	format      string
	err         error
	calledWith  string
	calledTexts []string
	calls       int
}

func (m *mockTTS) Synthesize(ctx context.Context, text string, onChunk func(chunk TTSAudioChunk) error) error {
	m.mu.Lock()
	delay := m.delay
	m.calledWith = text
	m.calledTexts = append(m.calledTexts, text)
	m.calls++
	audio, format, err := m.audio, m.format, m.err
	m.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
	if err != nil {
		return err
	}
	if len(audio) > 0 && onChunk != nil {
		if format == "" {
			format = "pcm"
		}
		return onChunk(TTSAudioChunk{
			Data:       audio,
			Format:     format,
			SampleRate: 24000,
			Channels:   1,
		})
	}
	return nil
}


// mockStreamingBrain (AudioStreamingBrainClient) lives in hub_stream_test.go
// alongside the streaming-specific tests that use it.

func makeValidWAV(sampleCount int) []byte {
	// 44-byte standard WAV header + PCM samples
	buf := make([]byte, 44+sampleCount*2)
	copy(buf[0:4], []byte("RIFF"))
	copy(buf[8:12], []byte("WAVE"))
	copy(buf[12:16], []byte("fmt "))
	buf[16] = 16 // PCM format chunk size
	buf[20] = 1  // format = 1 (PCM)
	buf[22] = 1  // channels = 1
	buf[24] = 0x80
	buf[25] = 0x3E // 16000 Hz
	buf[34] = 16   // bits per sample
	copy(buf[36:40], []byte("data"))
	return buf
}

func TestHub_Interact_Success(t *testing.T) {
	t.Parallel()

	cfg := &config.VoiceHubConfig{
		Enabled: true,
	}
	hubEvents := events.NewHub(events.HubConfig{}, nil, nil)
	defer hubEvents.Close()
	coord := NewCoordinator(hubEvents)

	stt := &mockSTT{text: "Turn off office lights"}
	brain := &mockBrain{
		reply:    "Turned off office lights.",
		statuses: []string{"⚡ Checking devices...", "⚡ Turning off lights..."},
	}
	tts := &mockTTS{audio: []byte("fake-pcm-bytes"), format: "pcm"}

	h := NewHub(cfg, coord,
		WithSTTClient(stt),
		WithBrainClient(brain),
		WithTTSClient(tts),
	)

	if !h.IsEnabled() {
		t.Fatal("expected hub to be enabled")
	}

	var emittedEvents []struct {
		Event string
		Data  any
	}
	var mu sync.Mutex
	sink := func(event string, data any) error {
		mu.Lock()
		defer mu.Unlock()
		emittedEvents = append(emittedEvents, struct {
			Event string
			Data  any
		}{Event: event, Data: data})
		return nil
	}

	wav := makeValidWAV(1600)
	err := h.Interact(context.Background(), bytes.NewReader(wav), "kiosk-kitchen", "sess-1", sink, EdgeTimings{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	expectedSequence := []string{"state", "transcript", "status", "status", "reply", "audio_chunk", "audio_chunk", "done"}
	if len(emittedEvents) != len(expectedSequence) {
		t.Fatalf("expected %d events, got %d: %+v", len(expectedSequence), len(emittedEvents), emittedEvents)
	}
	for i, exp := range expectedSequence {
		if emittedEvents[i].Event != exp {
			t.Errorf("at index %d: expected event %q, got %q", i, exp, emittedEvents[i].Event)
		}
	}

	// Coordinator must be reset to idle
	if coord.GetState().State != StateIdle {
		t.Errorf("expected final state idle, got %s", coord.GetState().State)
	}
}

func TestHub_Interact_Disabled(t *testing.T) {
	t.Parallel()
	h := NewHub(&config.VoiceHubConfig{Enabled: false}, nil)
	err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(100)), "", "", func(string, any) error { return nil }, EdgeTimings{})
	if !errors.Is(err, ErrHubDisabled) {
		t.Fatalf("expected ErrHubDisabled, got: %v", err)
	}
}

func TestHub_Interact_InvalidAudio(t *testing.T) {
	t.Parallel()
	h := NewHub(&config.VoiceHubConfig{Enabled: true}, nil)

	// Nil reader
	err := h.Interact(context.Background(), nil, "", "", func(string, any) error { return nil }, EdgeTimings{})
	if !errors.Is(err, ErrInvalidAudio) {
		t.Fatalf("expected ErrInvalidAudio for nil, got: %v", err)
	}

	// Too short (< 44 bytes)
	err = h.Interact(context.Background(), bytes.NewReader([]byte("RIFFshort")), "", "", func(string, any) error { return nil }, EdgeTimings{})
	if !errors.Is(err, ErrInvalidAudio) {
		t.Fatalf("expected ErrInvalidAudio for short, got: %v", err)
	}

	// Not RIFF
	notWav := make([]byte, 50)
	copy(notWav, []byte("NOT_A_WAV_FILE_HEADER"))
	err = h.Interact(context.Background(), bytes.NewReader(notWav), "", "", func(string, any) error { return nil }, EdgeTimings{})
	if !errors.Is(err, ErrInvalidAudio) {
		t.Fatalf("expected ErrInvalidAudio for non-RIFF, got: %v", err)
	}
}

func TestHub_Interact_ConcurrentBusy(t *testing.T) {
	t.Parallel()
	cfg := &config.VoiceHubConfig{Enabled: true}
	stt := &mockSTT{text: "hello"}
	sttStarted := make(chan struct{})
	sttBlock := make(chan struct{})

	blockingSTT := &mockBlockingSTT{
		onTranscribe: func() {
			close(sttStarted)
			<-sttBlock
		},
	}
	h := NewHub(cfg, nil, WithSTTClient(blockingSTT))

	doneCh := make(chan error)
	go func() {
		wav := makeValidWAV(100)
		doneCh <- h.Interact(context.Background(), bytes.NewReader(wav), "", "", func(string, any) error { return nil }, EdgeTimings{})
	}()

	<-sttStarted

	// Second concurrent call must return ErrInteractionBusy
	secondErr := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(100)), "", "", func(string, any) error { return nil }, EdgeTimings{})
	if !errors.Is(secondErr, ErrInteractionBusy) {
		t.Errorf("expected ErrInteractionBusy, got: %v", secondErr)
	}

	close(sttBlock)
	firstErr := <-doneCh
	// First call should fail at Brain because brain is nil, but not ErrInteractionBusy
	if errors.Is(firstErr, ErrInteractionBusy) {
		t.Errorf("first call should not be busy: %v", firstErr)
	}
	_ = stt
}

type mockBlockingSTT struct {
	onTranscribe func()
}

func (m *mockBlockingSTT) Transcribe(ctx context.Context, wavData []byte) (string, error) {
	m.onTranscribe()
	return "test", nil
}

func TestHub_Interact_STTFailure(t *testing.T) {
	t.Parallel()
	cfg := &config.VoiceHubConfig{Enabled: true}
	coord := NewCoordinator(nil)
	stt := &mockSTT{err: errors.New("whisper offline")}
	h := NewHub(cfg, coord, WithSTTClient(stt))

	var eventsEmitted []string
	sink := func(event string, data any) error {
		eventsEmitted = append(eventsEmitted, event)
		return nil
	}

	err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(100)), "", "", sink, EdgeTimings{})
	if !errors.Is(err, ErrSTTFailed) {
		t.Fatalf("expected ErrSTTFailed, got: %v", err)
	}
	if coord.GetState().State != StateError {
		t.Errorf("expected state error, got: %s", coord.GetState().State)
	}
	if len(eventsEmitted) < 2 || eventsEmitted[1] != "error" {
		t.Errorf("expected error event emitted, got: %+v", eventsEmitted)
	}
}

func TestHub_Interact_BrainFailure(t *testing.T) {
	t.Parallel()
	cfg := &config.VoiceHubConfig{Enabled: true}
	coord := NewCoordinator(nil)
	stt := &mockSTT{text: "hello"}
	brain := &mockBrain{err: errors.New("aerial unreachable")}
	h := NewHub(cfg, coord, WithSTTClient(stt), WithBrainClient(brain))

	var eventsEmitted []string
	sink := func(event string, data any) error {
		eventsEmitted = append(eventsEmitted, event)
		return nil
	}

	err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(100)), "", "", sink, EdgeTimings{})
	if !errors.Is(err, ErrBrainFailed) {
		t.Fatalf("expected ErrBrainFailed, got: %v", err)
	}
	if coord.GetState().State != StateError {
		t.Errorf("expected state error, got: %s", coord.GetState().State)
	}
}

func TestHub_Interact_TTSDegradation(t *testing.T) {
	t.Parallel()
	cfg := &config.VoiceHubConfig{Enabled: true}
	coord := NewCoordinator(nil)
	stt := &mockSTT{text: "hello"}
	brain := &mockBrain{reply: "hi there"}
	tts := &mockTTS{err: errors.New("kokoro timeout")}
	h := NewHub(cfg, coord, WithSTTClient(stt), WithBrainClient(brain), WithTTSClient(tts))

	var eventsEmitted []string
	sink := func(event string, data any) error {
		eventsEmitted = append(eventsEmitted, event)
		return nil
	}

	err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(100)), "", "", sink, EdgeTimings{})
	if err != nil {
		t.Fatalf("expected success with degraded TTS, got: %v", err)
	}

	// Verify reply was still emitted before tts error
	hasReply := false
	hasError := false
	hasDone := false
	for _, e := range eventsEmitted {
		if e == "reply" {
			hasReply = true
		}
		if e == "error" {
			hasError = true
		}
		if e == "done" {
			hasDone = true
		}
	}
	if !hasReply || !hasError || !hasDone {
		t.Errorf("expected reply, error, and done events, got: %+v", eventsEmitted)
	}
}

func drainMockWyomingRequest(reader *bufio.Reader) {
	for {
		line, rErr := reader.ReadBytes('\n')
		if rErr != nil {
			break
		}
		var hdr struct {
			Type          string `json:"type"`
			DataLength    int    `json:"data_length"`
			PayloadLength int    `json:"payload_length"`
		}
		_ = json.Unmarshal(line, &hdr)
		if hdr.DataLength > 0 {
			_, _ = io.CopyN(io.Discard, reader, int64(hdr.DataLength))
		}
		if hdr.PayloadLength > 0 {
			_, _ = io.CopyN(io.Discard, reader, int64(hdr.PayloadLength))
		}
		if hdr.Type == "audio-stop" {
			break
		}
	}
}

func TestDefaultSTTClient_Wyoming(t *testing.T) {
	t.Parallel()

	// Launch mock Wyoming TCP server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer listener.Close()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		// Read events until audio-stop
		reader := bufio.NewReader(conn)
		drainMockWyomingRequest(reader)

		// Send transcript response
		respJSON := `{"text":"test transcript from wyoming"}`
		header := fmt.Sprintf("{\"type\":\"transcript\",\"data_length\":%d}\n", len(respJSON))
		_, _ = conn.Write([]byte(header))
		_, _ = conn.Write([]byte(respJSON))
	}()

	client := NewDefaultSTTClient("tcp://" + listener.Addr().String())
	txt, err := client.Transcribe(context.Background(), makeValidWAV(100))
	if err != nil {
		t.Fatalf("unexpected wyoming error: %v", err)
	}
	if txt != "test transcript from wyoming" {
		t.Fatalf("expected 'test transcript from wyoming', got %q", txt)
	}
}

func TestDefaultSTTClient_HTTP(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"text": "test transcript from http",
		})
	}))
	defer ts.Close()

	client := NewDefaultSTTClient(ts.URL)
	txt, err := client.Transcribe(context.Background(), makeValidWAV(100))
	if err != nil {
		t.Fatalf("unexpected http stt error: %v", err)
	}
	if txt != "test transcript from http" {
		t.Fatalf("expected 'test transcript from http', got %q", txt)
	}
}

func TestDefaultSTTClient_Errors(t *testing.T) {
	t.Parallel()

	// Empty URL
	c := NewDefaultSTTClient("")
	_, err := c.Transcribe(context.Background(), makeValidWAV(100))
	if err == nil {
		t.Fatal("expected error for empty URL")
	}

	// Dial failure
	c2 := NewDefaultSTTClient("tcp://127.0.0.1:59999")
	_, err = c2.Transcribe(context.Background(), makeValidWAV(100))
	if err == nil {
		t.Fatal("expected dial error")
	}

	// HTTP error response
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer ts.Close()

	c3 := NewDefaultSTTClient(ts.URL)
	_, err = c3.Transcribe(context.Background(), makeValidWAV(100))
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected 500 error, got: %v", err)
	}
}

func TestDefaultBrainClient_SSEAndJSON(t *testing.T) {
	t.Parallel()

	// SSE server
	sseServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = w.Write([]byte("event: status\ndata: {\"status\":\"⚡ executing tool\"}\n\n"))
		flusher.Flush()
		_, _ = w.Write([]byte("event: reply\ndata: {\"reply\":\"The light is on\"}\n\n"))
		flusher.Flush()
		_, _ = w.Write([]byte("event: done\ndata: {}\n\n"))
		flusher.Flush()
	}))
	defer sseServer.Close()

	var statuses []string
	b := NewDefaultBrainClient(sseServer.URL, 5)
	reply, err := b.Ask(context.Background(), AskRequest{Prompt: "turn on light", SessionID: "s1"}, func(status string) {
		statuses = append(statuses, status)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reply != "The light is on" {
		t.Errorf("expected reply 'The light is on', got %q", reply)
	}
	if len(statuses) != 1 || statuses[0] != "⚡ executing tool" {
		t.Errorf("expected status '⚡ executing tool', got %+v", statuses)
	}

	// JSON server
	jsonServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"reply": "JSON reply",
		})
	}))
	defer jsonServer.Close()

	bJSON := NewDefaultBrainClient(jsonServer.URL, 5)
	reply, err = bJSON.Ask(context.Background(), AskRequest{Prompt: "hello", SessionID: "s1"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reply != "JSON reply" {
		t.Errorf("expected 'JSON reply', got %q", reply)
	}

	// Empty URL fallback
	bEmpty := NewDefaultBrainClient("", 5)
	reply, err = bEmpty.Ask(context.Background(), AskRequest{Prompt: "test prompt", SessionID: "s1"}, nil)
	if err != nil || !strings.Contains(reply, "test prompt") {
		t.Errorf("expected fallback containing test prompt, got: %q, err: %v", reply, err)
	}

	// Server error
	errServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}))
	defer errServer.Close()
	bErr := NewDefaultBrainClient(errServer.URL, 5)
	_, err = bErr.Ask(context.Background(), AskRequest{Prompt: "fail", SessionID: "s1"}, nil)
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Errorf("expected 502 error, got: %v", err)
	}
}

func TestDefaultBrainClient_TurnSessionGenerationAndNodeID(t *testing.T) {
	t.Parallel()

	var receivedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&receivedBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"reply": "ok"})
	}))
	defer server.Close()

	b := NewDefaultBrainClient(server.URL, 5)
	_, err := b.Ask(context.Background(), AskRequest{Prompt: "hello", NodeID: "touch-kiosk-kitchen"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sessID, ok := receivedBody["session_id"].(string)
	if !ok || !strings.HasPrefix(sessID, "turn-") {
		t.Errorf("expected generated per-turn session_id starting with 'turn-', got %v", receivedBody["session_id"])
	}
	if receivedBody["node_id"] != "touch-kiosk-kitchen" {
		t.Errorf("expected node_id 'touch-kiosk-kitchen', got %v", receivedBody["node_id"])
	}
	if _, exists := receivedBody["device_name"]; exists {
		t.Errorf("expected no device_name in body, got %v", receivedBody["device_name"])
	}
}

func TestDefaultTTSClient(t *testing.T) {
	t.Parallel()

	t.Run("WAV stream parsed to PCM chunks", func(t *testing.T) {
		t.Parallel()
		wavData := makeValidWAV(4800) // 9600 bytes PCM at 16kHz mono
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "audio/wav")
			_, _ = w.Write(wavData[:44+4800])
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			_, _ = w.Write(wavData[44+4800:])
		}))
		defer ts.Close()

		tts := NewDefaultTTSClient(ts.URL, "kokoro", "af_bella", 5)
		var chunks []TTSAudioChunk
		err := tts.Synthesize(context.Background(), "hello world", func(chunk TTSAudioChunk) error {
			chunks = append(chunks, chunk)
			return nil
		})
		if err != nil {
			t.Fatalf("unexpected streaming error: %v", err)
		}
		if len(chunks) == 0 {
			t.Fatalf("expected at least 1 audio chunk, got 0")
		}
		var totalPCM int
		for i, c := range chunks {
			if c.Format != "pcm" {
				t.Errorf("chunk %d: expected format pcm, got %s", i, c.Format)
			}
			if c.SampleRate != 16000 {
				t.Errorf("chunk %d: expected sample rate 16000, got %d", i, c.SampleRate)
			}
			if c.Channels != 1 {
				t.Errorf("chunk %d: expected channels 1, got %d", i, c.Channels)
			}
			totalPCM += len(c.Data)
		}
		if totalPCM != 9600 {
			t.Errorf("expected 9600 bytes total PCM, got %d", totalPCM)
		}
	})

	t.Run("raw PCM stream pass-through", func(t *testing.T) {
		t.Parallel()
		pcmData := bytes.Repeat([]byte{0x01, 0x02}, 2400)
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "audio/pcm")
			_, _ = w.Write(pcmData)
		}))
		defer ts.Close()

		tts := NewDefaultTTSClient(ts.URL, "kokoro", "af_bella", 5)
		var chunks []TTSAudioChunk
		err := tts.Synthesize(context.Background(), "hello", func(chunk TTSAudioChunk) error {
			chunks = append(chunks, chunk)
			return nil
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(chunks) == 0 || chunks[0].Format != "pcm" {
			t.Fatalf("expected pcm chunks, got %+v", chunks)
		}
	})

	t.Run("MP3 response is unsupported", func(t *testing.T) {
		t.Parallel()
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write([]byte("fake-streaming-mp3-bytes"))
		}))
		defer ts.Close()

		tts := NewDefaultTTSClient(ts.URL, "kokoro", "af_bella", 5)
		err := tts.Synthesize(context.Background(), "hello", func(chunk TTSAudioChunk) error {
			t.Errorf("no chunk should be emitted for mp3, got %+v", chunk)
			return nil
		})
		if !errors.Is(err, ErrUnsupportedAudioFormat) {
			t.Fatalf("expected ErrUnsupportedAudioFormat, got: %v", err)
		}
	})

	t.Run("empty URL and empty text return nil", func(t *testing.T) {
		t.Parallel()
		ttsEmpty := NewDefaultTTSClient("", "kokoro", "af_bella", 5)
		if err := ttsEmpty.Synthesize(context.Background(), "hello", func(TTSAudioChunk) error {
			t.Fatal("unexpected chunk emitted for empty URL")
			return nil
		}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		ttsReal := NewDefaultTTSClient("http://127.0.0.1:9", "kokoro", "af_bella", 5)
		if err := ttsReal.Synthesize(context.Background(), "   ", func(TTSAudioChunk) error {
			t.Fatal("unexpected chunk emitted for empty text")
			return nil
		}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("server error returns error", func(t *testing.T) {
		t.Parallel()
		errServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "server dead", http.StatusInternalServerError)
		}))
		defer errServer.Close()

		ttsErr := NewDefaultTTSClient(errServer.URL, "kokoro", "af_bella", 5)
		err := ttsErr.Synthesize(context.Background(), "hello", func(TTSAudioChunk) error { return nil })
		if err == nil || !strings.Contains(err.Error(), "500") {
			t.Fatalf("expected 500 error, got: %v", err)
		}
	})

	t.Run("onChunk error in WAV stream terminates early", func(t *testing.T) {
		t.Parallel()
		wavData := makeValidWAV(4800)
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "audio/wav")
			_, _ = w.Write(wavData)
		}))
		defer ts.Close()

		tts := NewDefaultTTSClient(ts.URL, "kokoro", "af_bella", 5)
		err := tts.Synthesize(context.Background(), "hello", func(chunk TTSAudioChunk) error {
			return errors.New("sink abort")
		})
		if err == nil || !strings.Contains(err.Error(), "sink abort") {
			t.Fatalf("expected sink abort error, got: %v", err)
		}
	})

	t.Run("onChunk error in raw PCM stream terminates early", func(t *testing.T) {
		t.Parallel()
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "audio/pcm")
			_, _ = w.Write([]byte("some-initial-header-and-body-payload-for-pcm"))
		}))
		defer ts.Close()

		tts := NewDefaultTTSClient(ts.URL, "kokoro", "af_bella", 5)
		err := tts.Synthesize(context.Background(), "hello", func(chunk TTSAudioChunk) error {
			return errors.New("pcm sink abort")
		})
		if err == nil || !strings.Contains(err.Error(), "pcm sink abort") {
			t.Fatalf("expected pcm sink abort error, got: %v", err)
		}
	})

	t.Run("WAV with zero sample rate and channels uses defaults", func(t *testing.T) {
		t.Parallel()
		wavData := makeValidWAV(2400)
		copy(wavData[22:24], []byte{0, 0})
		copy(wavData[24:28], []byte{0, 0, 0, 0})
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "audio/wav")
			_, _ = w.Write(wavData)
		}))
		defer ts.Close()

		tts := NewDefaultTTSClient(ts.URL, "kokoro", "af_bella", 5)
		var chunk TTSAudioChunk
		err := tts.Synthesize(context.Background(), "hello", func(c TTSAudioChunk) error {
			chunk = c
			return nil
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if chunk.Channels != 1 || chunk.SampleRate != 24000 {
			t.Errorf("expected defaults (1 ch, 24000 rate), got %d ch, %d rate", chunk.Channels, chunk.SampleRate)
		}
	})
}

func TestHub_Interact_EdgeCases(t *testing.T) {
	t.Parallel()

	cfg := &config.VoiceHubConfig{Enabled: true}

	t.Run("nil STT returns ErrSTTFailed", func(t *testing.T) {
		t.Parallel()
		h := NewHub(cfg, nil, WithSTTClient(nil), WithBrainClient(&mockBrain{}), WithTTSClient(&mockTTS{}))
		err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(100)), "", "", func(event string, data any) error {
			return nil
		}, EdgeTimings{})
		if !errors.Is(err, ErrSTTFailed) {
			t.Fatalf("expected ErrSTTFailed, got: %v", err)
		}
	})

	t.Run("nil Brain returns ErrBrainFailed", func(t *testing.T) {
		t.Parallel()
		h := NewHub(cfg, nil, WithSTTClient(&mockSTT{text: "hello"}), WithBrainClient(nil), WithTTSClient(&mockTTS{}))
		err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(100)), "", "", func(event string, data any) error {
			return nil
		}, EdgeTimings{})
		if !errors.Is(err, ErrBrainFailed) {
			t.Fatalf("expected ErrBrainFailed, got: %v", err)
		}
	})

	t.Run("sink error on state aborts", func(t *testing.T) {
		t.Parallel()
		h := NewHub(cfg, nil, WithSTTClient(&mockSTT{text: "hello"}), WithBrainClient(&mockBrain{reply: "hi"}), WithTTSClient(&mockTTS{}))
		err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(100)), "", "", func(event string, data any) error {
			if event == "state" {
				return errors.New("disconnected")
			}
			return nil
		}, EdgeTimings{})
		if err == nil || !strings.Contains(err.Error(), "disconnected") {
			t.Fatalf("expected disconnected error, got: %v", err)
		}
	})

	t.Run("sink error on transcript aborts", func(t *testing.T) {
		t.Parallel()
		h := NewHub(cfg, nil, WithSTTClient(&mockSTT{text: "hello"}), WithBrainClient(&mockBrain{reply: "hi"}), WithTTSClient(&mockTTS{}))
		err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(100)), "", "", func(event string, data any) error {
			if event == "transcript" {
				return errors.New("aborted on transcript")
			}
			return nil
		}, EdgeTimings{})
		if err == nil || !strings.Contains(err.Error(), "aborted on transcript") {
			t.Fatalf("expected aborted on transcript, got: %v", err)
		}
	})

	t.Run("sink error on reply aborts", func(t *testing.T) {
		t.Parallel()
		h := NewHub(cfg, nil, WithSTTClient(&mockSTT{text: "hello"}), WithBrainClient(&mockBrain{reply: "hi"}), WithTTSClient(&mockTTS{}))
		err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(100)), "", "", func(event string, data any) error {
			if event == "reply" {
				return errors.New("stream closed")
			}
			return nil
		}, EdgeTimings{})
		if err == nil || !strings.Contains(err.Error(), "stream closed") {
			t.Fatalf("expected stream closed error, got: %v", err)
		}
	})
}

func TestDefaultSTTClient_Wyoming_Advanced(t *testing.T) {
	t.Parallel()

	// Server sending non-JSON line and payload before transcript
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer listener.Close()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		drainMockWyomingRequest(reader)

		// Send non-json garbage line
		_, _ = conn.Write([]byte("not-json-line\n"))
		// Send event with payload_length > 0
		_, _ = conn.Write([]byte("{\"type\":\"ping\",\"payload_length\":4}\npong"))
		// Send transcript event with text
		transcriptJSON := `{"text":"advanced transcript"}`
		hdr := fmt.Sprintf("{\"type\":\"transcript\",\"data_length\":%d}\n", len(transcriptJSON))
		_, _ = conn.Write([]byte(hdr))
		_, _ = conn.Write([]byte(transcriptJSON))
	}()


	client := NewDefaultSTTClient("tcp://" + listener.Addr().String())
	txt, err := client.Transcribe(context.Background(), makeValidWAV(100))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if txt != "advanced transcript" {
		t.Fatalf("expected 'advanced transcript', got %q", txt)
	}
}

func TestDefaultSTTClient_HTTP_Fallbacks(t *testing.T) {
	t.Parallel()

	// STT returning transcript key instead of text
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"transcript": "fallback transcript",
		})
	}))
	defer ts.Close()

	client := NewDefaultSTTClient(ts.URL)
	txt, err := client.Transcribe(context.Background(), makeValidWAV(100))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if txt != "fallback transcript" {
		t.Errorf("expected 'fallback transcript', got %q", txt)
	}

	// STT returning malformed JSON
	badJSONServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("not valid json"))
	}))
	defer badJSONServer.Close()

	clientBad := NewDefaultSTTClient(badJSONServer.URL)
	_, err = clientBad.Transcribe(context.Background(), makeValidWAV(100))
	if err == nil || !strings.Contains(err.Error(), "stt decode error") {
		t.Errorf("expected decode error, got: %v", err)
	}
}

func TestDefaultBrainClient_Fallbacks(t *testing.T) {
	t.Parallel()

	// JSON response with "text"
	tsText := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"text": "text fallback"})
	}))
	defer tsText.Close()

	b1 := NewDefaultBrainClient(tsText.URL, 5)
	rep, err := b1.Ask(context.Background(), AskRequest{Prompt: "q", SessionID: "s"}, nil)
	if err != nil || rep != "text fallback" {
		t.Errorf("expected 'text fallback', got %q, err: %v", rep, err)
	}

	// JSON response with "content"
	tsContent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"content": "content fallback"})
	}))
	defer tsContent.Close()

	b2 := NewDefaultBrainClient(tsContent.URL, 5)
	rep, err = b2.Ask(context.Background(), AskRequest{Prompt: "q", SessionID: "s"}, nil)
	if err != nil || rep != "content fallback" {
		t.Errorf("expected 'content fallback', got %q, err: %v", rep, err)
	}

	// JSON decode error
	tsBadJSON := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("invalid json"))
	}))
	defer tsBadJSON.Close()

	b3 := NewDefaultBrainClient(tsBadJSON.URL, 5)
	_, err = b3.Ask(context.Background(), AskRequest{Prompt: "q", SessionID: "s"}, nil)
	if err == nil {
		t.Error("expected json decode error")
	}

	// SSE stream without "done" event returns reply on EOF
	tsNoDone := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: reply\ndata: {\"reply\":\"streamed without done\"}\n\n"))
	}))
	defer tsNoDone.Close()

	b4 := NewDefaultBrainClient(tsNoDone.URL, 5)
	rep, err = b4.Ask(context.Background(), AskRequest{Prompt: "q", SessionID: "s"}, nil)
	if err != nil || rep != "streamed without done" {
		t.Errorf("expected 'streamed without done', got %q, err: %v", rep, err)
	}
}

func TestHub_Interact_SinkErrors(t *testing.T) {
	t.Parallel()
	cfg := &config.VoiceHubConfig{Enabled: true}
	coord := NewCoordinator(nil)

	t.Run("sink error on status is non-fatal", func(t *testing.T) {
		t.Parallel()
		stt := &mockSTT{text: "hello"}
		brain := &mockBrain{reply: "hi", statuses: []string{"thinking..."}}
		tts := &mockTTS{audio: []byte("wav"), format: "pcm"}
		h := NewHub(cfg, coord, WithSTTClient(stt), WithBrainClient(brain), WithTTSClient(tts))

		err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(100)), "", "", func(event string, data any) error {
			if event == "status" {
				return errors.New("status sink error")
			}
			return nil
		}, EdgeTimings{})
		if err != nil {
			t.Fatalf("status sink error should be tolerated, got: %v", err)
		}
	})

	t.Run("sink error on audio_chunk aborts", func(t *testing.T) {
		t.Parallel()
		stt := &mockSTT{text: "hello"}
		brain := &mockBrain{reply: "hi"}
		tts := &mockTTS{audio: []byte("wav"), format: "pcm"}
		h := NewHub(cfg, coord, WithSTTClient(stt), WithBrainClient(brain), WithTTSClient(tts))

		err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(100)), "", "", func(event string, data any) error {
			if event == "audio_chunk" {
				return errors.New("audio sink error")
			}
			return nil
		}, EdgeTimings{})
		if err == nil || !strings.Contains(err.Error(), "audio sink error") {
			t.Fatalf("expected audio sink error, got: %v", err)
		}
	})

	t.Run("sink error on done aborts", func(t *testing.T) {
		t.Parallel()
		stt := &mockSTT{text: "hello"}
		brain := &mockBrain{reply: "hi"}
		tts := &mockTTS{audio: []byte("wav"), format: "pcm"}
		h := NewHub(cfg, coord, WithSTTClient(stt), WithBrainClient(brain), WithTTSClient(tts))

		err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(100)), "", "", func(event string, data any) error {
			if event == "done" {
				return errors.New("done sink error")
			}
			return nil
		}, EdgeTimings{})
		if err == nil || !strings.Contains(err.Error(), "done sink error") {
			t.Fatalf("expected done sink error, got: %v", err)
		}
	})

	t.Run("sink error on stt error propagates sink error", func(t *testing.T) {
		t.Parallel()
		stt := &mockSTT{err: errors.New("stt failure")}
		h := NewHub(cfg, coord, WithSTTClient(stt))

		err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(100)), "", "", func(event string, data any) error {
			if event == "error" {
				return errors.New("sink error on stt failure")
			}
			return nil
		}, EdgeTimings{})
		if err == nil || !strings.Contains(err.Error(), "sink error on stt failure") {
			t.Fatalf("expected sink error on stt failure, got: %v", err)
		}
	})

	t.Run("sink error on brain error propagates sink error", func(t *testing.T) {
		t.Parallel()
		stt := &mockSTT{text: "hello"}
		brain := &mockBrain{err: errors.New("brain failure")}
		h := NewHub(cfg, coord, WithSTTClient(stt), WithBrainClient(brain))

		err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(100)), "", "", func(event string, data any) error {
			if event == "error" {
				return errors.New("sink error on brain failure")
			}
			return nil
		}, EdgeTimings{})
		if err == nil || !strings.Contains(err.Error(), "sink error on brain failure") {
			t.Fatalf("expected sink error on brain failure, got: %v", err)
		}
	})

	t.Run("sink error on tts error propagates sink error", func(t *testing.T) {
		t.Parallel()
		stt := &mockSTT{text: "hello"}
		brain := &mockBrain{reply: "hi"}
		tts := &mockTTS{err: errors.New("tts failure")}
		h := NewHub(cfg, coord, WithSTTClient(stt), WithBrainClient(brain), WithTTSClient(tts))

		err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(100)), "", "", func(event string, data any) error {
			if event == "error" {
				return errors.New("sink error on tts failure")
			}
			return nil
		}, EdgeTimings{})
		if err == nil || !strings.Contains(err.Error(), "sink error on tts failure") {
			t.Fatalf("expected sink error on tts failure, got: %v", err)
		}
	})
}

func TestDefaultSTTClient_Wyoming_ReadCloseAndRawText(t *testing.T) {
	t.Parallel()

	// Server closes immediately
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer listener.Close()

	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		_ = conn.Close()
	}()

	client := NewDefaultSTTClient("tcp://" + listener.Addr().String())
	_, err = client.Transcribe(context.Background(), makeValidWAV(100))
	if err == nil {
		t.Fatal("expected error when wyoming server closes early")
	}

	// Server returns raw string in dataBytes instead of valid JSON
	listenerRaw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer listenerRaw.Close()

	go func() {
		conn, acceptErr := listenerRaw.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()

		drainMockWyomingRequest(bufio.NewReader(conn))

		rawString := "raw transcript fallback"
		hdr := fmt.Sprintf("{\"type\":\"transcript\",\"data_length\":%d}\n", len(rawString))
		_, _ = conn.Write([]byte(hdr))
		_, _ = conn.Write([]byte(rawString))
	}()


	clientRaw := NewDefaultSTTClient("tcp://" + listenerRaw.Addr().String())
	txt, err := clientRaw.Transcribe(context.Background(), makeValidWAV(100))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if txt != "raw transcript fallback" {
		t.Errorf("expected 'raw transcript fallback', got %q", txt)
	}
}

func TestClients_NetworkFailures(t *testing.T) {
	t.Parallel()

	// HTTP STT network failure
	stt := NewDefaultSTTClient("http://127.0.0.1:59998/invalid")
	_, err := stt.Transcribe(context.Background(), makeValidWAV(100))
	if err == nil {
		t.Error("expected network error for http stt")
	}

	// Brain client network failure
	brain := NewDefaultBrainClient("http://127.0.0.1:59998/invalid", 1)
	_, err = brain.Ask(context.Background(), AskRequest{Prompt: "hi", SessionID: "s1"}, nil)
	if err == nil {
		t.Error("expected network error for brain client")
	}

	// TTS client network failure
	tts := NewDefaultTTSClient("http://127.0.0.1:59998/invalid", "k", "v", 1)
	err = tts.Synthesize(context.Background(), "hi", func(chunk TTSAudioChunk) error { return nil })
	if err == nil {
		t.Error("expected network error for tts client")
	}
}

func TestDefaultBrainClient_SSE_MalformedDataAndError(t *testing.T) {
	t.Parallel()

	// Server sending malformed JSON on data: line, followed by valid done
	tsMalformed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: status\ndata: {not-valid-json}\n\nevent: reply\ndata: {\"reply\":\"recovered\"}\n\nevent: done\ndata: {}\n\n"))
	}))
	defer tsMalformed.Close()

	brain := NewDefaultBrainClient(tsMalformed.URL, 5)
	reply, err := brain.Ask(context.Background(), AskRequest{Prompt: "hi", SessionID: "s1"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reply != "recovered" {
		t.Errorf("expected 'recovered', got %q", reply)
	}
}

func TestDefaultBrainClient_SSE_ScannerError(t *testing.T) {
	t.Parallel()
	longLine := "data: " + strings.Repeat("A", 70000) + "\n\n"
	tsLong := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(longLine))
	}))
	defer tsLong.Close()

	brain := NewDefaultBrainClient(tsLong.URL, 5)
	_, err := brain.Ask(context.Background(), AskRequest{Prompt: "hi", SessionID: "s1"}, nil)
	if err == nil {
		t.Fatal("expected scanner error for line exceeding max scan token size")
	}
}

func TestDefaultSTTClient_Wyoming_TruncatedDataAndPayload(t *testing.T) {
	t.Parallel()

	// Truncated dataLength
	l1, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l1.Close()
	go func() {
		conn, acceptErr := l1.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		drainMockWyomingRequest(bufio.NewReader(conn))
		_, _ = conn.Write([]byte("{\"type\":\"event\",\"data_length\":100}\nshort"))
	}()
	c1 := NewDefaultSTTClient("tcp://" + l1.Addr().String())
	_, err = c1.Transcribe(context.Background(), makeValidWAV(100))
	if err == nil {
		t.Fatal("expected error on truncated data")
	}

	// Truncated payloadLength
	l2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	go func() {
		conn, acceptErr := l2.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		drainMockWyomingRequest(bufio.NewReader(conn))
		_, _ = conn.Write([]byte("{\"type\":\"event\",\"payload_length\":100}\nshort"))
	}()
	c2 := NewDefaultSTTClient("tcp://" + l2.Addr().String())
	_, err = c2.Transcribe(context.Background(), makeValidWAV(100))
	if err == nil {
		t.Fatal("expected error on truncated payload")
	}
}

func TestHub_MetricsRecording_Success(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	cfg := &config.VoiceHubConfig{
		Enabled:  true,
		TTSModel: "kokoro",
	}
	stt := &mockSTT{delay: 2 * time.Millisecond, text: "lights on"}
	brain := &mockBrain{reply: "turning on lights"}
	tts := &mockTTS{delay: 2 * time.Millisecond, audio: []byte("tts-audio-bytes"), format: "pcm"}


	h := NewHub(cfg, nil,
		WithSTTClient(stt),
		WithBrainClient(brain),
		WithTTSClient(tts),
		WithMetrics(m),
	)

	// 16000 samples = 1 second of audio at 16kHz
	wav := makeValidWAV(16000)
	timings := EdgeTimings{
		WakeEvalSec:         0.05,
		UtteranceSpeechSec:  1.0,
		UtteranceSilenceSec: 0.25,
	}

	sink := func(string, any) error { return nil }
	err := h.Interact(context.Background(), bytes.NewReader(wav), "kiosk-kitchen", "sess-1", sink, timings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	stagesFound := make(map[string]bool)
	var turnsSuccessCount float64
	var foundRTF, foundCPS bool

	for _, mf := range mfs {
		switch mf.GetName() {
		case "mirrormere_voice_stage_duration_seconds":
			for _, metric := range mf.GetMetric() {
				var stage, status string
				for _, lbl := range metric.GetLabel() {
					if lbl.GetName() == "stage" {
						stage = lbl.GetValue()
					}
					if lbl.GetName() == "status" {
						status = lbl.GetValue()
					}
				}
				if status == "success" {
					stagesFound[stage] = true
				}
			}
		case "mirrormere_voice_turns_total":
			for _, metric := range mf.GetMetric() {
				var status string
				for _, lbl := range metric.GetLabel() {
					if lbl.GetName() == "status" {
						status = lbl.GetValue()
					}
				}
				if status == "success" {
					turnsSuccessCount += metric.GetCounter().GetValue()
				}
			}
		case "mirrormere_voice_stt_rtf":
			for _, metric := range mf.GetMetric() {
				var engine string
				for _, lbl := range metric.GetLabel() {
					if lbl.GetName() == "engine" {
						engine = lbl.GetValue()
					}
				}
				if engine == "whisper" && metric.GetGauge().GetValue() >= 0 {
					foundRTF = true
				}
			}
		case "mirrormere_voice_tts_chars_per_second":
			for _, metric := range mf.GetMetric() {
				var voiceLbl string
				for _, lbl := range metric.GetLabel() {
					if lbl.GetName() == "voice" {
						voiceLbl = lbl.GetValue()
					}
				}
				if voiceLbl == "kokoro" && metric.GetGauge().GetValue() > 0 {
					foundCPS = true
				}
			}
		}
	}

	expectedStages := []string{"wake_eval", "utterance_speech", "utterance_silence", "stt", "brain", "tts"}
	for _, s := range expectedStages {
		if !stagesFound[s] {
			t.Errorf("missing success stage duration metric for: %s", s)
		}
	}
	if turnsSuccessCount != 1 {
		t.Errorf("expected 1 successful turn, got %f", turnsSuccessCount)
	}
	if !foundRTF {
		t.Errorf("expected valid RTF gauge for whisper")
	}
	if !foundCPS {
		t.Errorf("expected valid CPS gauge for kokoro")
	}
}

func TestHub_MetricsRecording_STTFailure(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	cfg := &config.VoiceHubConfig{Enabled: true}
	stt := &mockSTT{err: errors.New("whisper down")}

	h := NewHub(cfg, nil,
		WithSTTClient(stt),
		WithMetrics(m),
	)

	wav := makeValidWAV(1600)
	sink := func(string, any) error { return nil }
	err := h.Interact(context.Background(), bytes.NewReader(wav), "kiosk-kitchen", "sess-1", sink, EdgeTimings{})
	if !errors.Is(err, ErrSTTFailed) {
		t.Fatalf("expected ErrSTTFailed, got: %v", err)
	}

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	var foundSTTErrorStage bool
	var foundSTTErrorCounter bool
	var foundErrorTurn bool

	for _, mf := range mfs {
		switch mf.GetName() {
		case "mirrormere_voice_stage_duration_seconds":
			for _, metric := range mf.GetMetric() {
				var stage, status string
				for _, lbl := range metric.GetLabel() {
					if lbl.GetName() == "stage" {
						stage = lbl.GetValue()
					}
					if lbl.GetName() == "status" {
						status = lbl.GetValue()
					}
				}
				if stage == "stt" && status == "error" {
					foundSTTErrorStage = true
				}
			}
		case "mirrormere_voice_errors_total":
			for _, metric := range mf.GetMetric() {
				var stage, errType string
				for _, lbl := range metric.GetLabel() {
					if lbl.GetName() == "stage" {
						stage = lbl.GetValue()
					}
					if lbl.GetName() == "error_type" {
						errType = lbl.GetValue()
					}
				}
				if stage == "stt" && errType == "stt_error" && metric.GetCounter().GetValue() == 1 {
					foundSTTErrorCounter = true
				}
			}
		case "mirrormere_voice_turns_total":
			for _, metric := range mf.GetMetric() {
				for _, lbl := range metric.GetLabel() {
					if lbl.GetName() == "status" && lbl.GetValue() == "error" && metric.GetCounter().GetValue() == 1 {
						foundErrorTurn = true
					}
				}
			}
		}
	}

	if !foundSTTErrorStage {
		t.Errorf("missing stt error stage duration metric")
	}
	if !foundSTTErrorCounter {
		t.Errorf("missing stt error counter metric")
	}
	if !foundErrorTurn {
		t.Errorf("missing turns_total error counter metric")
	}
}

func TestHub_MetricsRecording_BrainFailure(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	cfg := &config.VoiceHubConfig{Enabled: true}
	stt := &mockSTT{text: "hello"}
	brain := &mockBrain{err: errors.New("brain failed")}

	h := NewHub(cfg, nil,
		WithSTTClient(stt),
		WithBrainClient(brain),
		WithMetrics(m),
	)

	wav := makeValidWAV(1600)
	sink := func(string, any) error { return nil }
	err := h.Interact(context.Background(), bytes.NewReader(wav), "kiosk-kitchen", "sess-1", sink, EdgeTimings{})
	if !errors.Is(err, ErrBrainFailed) {
		t.Fatalf("expected ErrBrainFailed, got: %v", err)
	}

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	var foundBrainErrorStage bool
	var foundBrainErrorCounter bool
	var foundErrorTurn bool

	for _, mf := range mfs {
		switch mf.GetName() {
		case "mirrormere_voice_stage_duration_seconds":
			for _, metric := range mf.GetMetric() {
				var stage, status string
				for _, lbl := range metric.GetLabel() {
					if lbl.GetName() == "stage" {
						stage = lbl.GetValue()
					}
					if lbl.GetName() == "status" {
						status = lbl.GetValue()
					}
				}
				if stage == "brain" && status == "error" {
					foundBrainErrorStage = true
				}
			}
		case "mirrormere_voice_errors_total":
			for _, metric := range mf.GetMetric() {
				var stage, errType string
				for _, lbl := range metric.GetLabel() {
					if lbl.GetName() == "stage" {
						stage = lbl.GetValue()
					}
					if lbl.GetName() == "error_type" {
						errType = lbl.GetValue()
					}
				}
				if stage == "brain" && errType == "brain_error" && metric.GetCounter().GetValue() == 1 {
					foundBrainErrorCounter = true
				}
			}
		case "mirrormere_voice_turns_total":
			for _, metric := range mf.GetMetric() {
				for _, lbl := range metric.GetLabel() {
					if lbl.GetName() == "status" && lbl.GetValue() == "error" && metric.GetCounter().GetValue() == 1 {
						foundErrorTurn = true
					}
				}
			}
		}
	}

	if !foundBrainErrorStage {
		t.Errorf("missing brain error stage duration metric")
	}
	if !foundBrainErrorCounter {
		t.Errorf("missing brain error counter metric")
	}
	if !foundErrorTurn {
		t.Errorf("missing turns_total error counter metric")
	}
}

func TestHub_MetricsRecording_TTSFailure(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	cfg := &config.VoiceHubConfig{Enabled: true}
	stt := &mockSTT{text: "hello"}
	brain := &mockBrain{reply: "hi there"}
	tts := &mockTTS{err: errors.New("tts failed")}

	h := NewHub(cfg, nil,
		WithSTTClient(stt),
		WithBrainClient(brain),
		WithTTSClient(tts),
		WithMetrics(m),
	)

	wav := makeValidWAV(1600)
	sink := func(string, any) error { return nil }
	err := h.Interact(context.Background(), bytes.NewReader(wav), "kiosk-kitchen", "sess-1", sink, EdgeTimings{})
	if err != nil {
		t.Fatalf("expected graceful degradation on TTS failure, got err: %v", err)
	}

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	var foundTTSErrorStage bool
	var foundTTSErrorCounter bool
	var foundErrorTurn bool

	for _, mf := range mfs {
		switch mf.GetName() {
		case "mirrormere_voice_stage_duration_seconds":
			for _, metric := range mf.GetMetric() {
				var stage, status string
				for _, lbl := range metric.GetLabel() {
					if lbl.GetName() == "stage" {
						stage = lbl.GetValue()
					}
					if lbl.GetName() == "status" {
						status = lbl.GetValue()
					}
				}
				if stage == "tts" && status == "error" {
					foundTTSErrorStage = true
				}
			}
		case "mirrormere_voice_errors_total":
			for _, metric := range mf.GetMetric() {
				var stage, errType string
				for _, lbl := range metric.GetLabel() {
					if lbl.GetName() == "stage" {
						stage = lbl.GetValue()
					}
					if lbl.GetName() == "error_type" {
						errType = lbl.GetValue()
					}
				}
				if stage == "tts" && errType == "tts_error" && metric.GetCounter().GetValue() == 1 {
					foundTTSErrorCounter = true
				}
			}
		case "mirrormere_voice_turns_total":
			for _, metric := range mf.GetMetric() {
				for _, lbl := range metric.GetLabel() {
					if lbl.GetName() == "status" && lbl.GetValue() == "error" && metric.GetCounter().GetValue() == 1 {
						foundErrorTurn = true
					}
				}
			}
		}
	}

	if !foundTTSErrorStage {
		t.Errorf("missing tts error stage duration metric")
	}
	if !foundTTSErrorCounter {
		t.Errorf("missing tts error counter metric")
	}
	if !foundErrorTurn {
		t.Errorf("missing turns_total error counter metric on degraded TTS")
	}
}

type delayStreamingBrain struct {
	*mockStreamingBrain
	delay time.Duration
}

func (d *delayStreamingBrain) AskStreaming(ctx context.Context, req AskRequest, onStatus func(status string), onAudio func(chunk BrainAudioChunk)) (string, error) {
	reply, err := d.mockStreamingBrain.AskStreaming(ctx, req, onStatus, onAudio)
	if d.delay > 0 {
		time.Sleep(d.delay)
	}
	return reply, err
}

func TestHub_MetricsRecording_StreamingBrain(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	cfg := &config.VoiceHubConfig{
		Enabled:  true,
		TTSModel: "kokoro",
	}
	stt := &mockSTT{delay: 2 * time.Millisecond, text: "what is the weather"}
	baseBrain := &mockStreamingBrain{
		reply: "It's sunny today. Enjoy the warmth.",
		sentences: []BrainAudioChunk{
			{Text: "It's sunny today.", Format: "pcm", Data: []byte("chunk-1")},
			{Text: "Enjoy the warmth.", Format: "pcm", Data: []byte("chunk-2")},
		},
	}
	brain := &delayStreamingBrain{
		mockStreamingBrain: baseBrain,
		delay:              2 * time.Millisecond,
	}

	h := NewHub(cfg, nil,
		WithSTTClient(stt),
		WithBrainClient(brain),
		WithMetrics(m),
	)


	wav := makeValidWAV(16000)
	sink := func(string, any) error { return nil }
	err := h.Interact(context.Background(), bytes.NewReader(wav), "kiosk-kitchen", "sess-1", sink, EdgeTimings{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	var foundTTSSuccessStage bool
	var foundCPS bool
	var foundSuccessTurn bool

	for _, mf := range mfs {
		switch mf.GetName() {
		case "mirrormere_voice_stage_duration_seconds":
			for _, metric := range mf.GetMetric() {
				var stage, status string
				for _, lbl := range metric.GetLabel() {
					if lbl.GetName() == "stage" {
						stage = lbl.GetValue()
					}
					if lbl.GetName() == "status" {
						status = lbl.GetValue()
					}
				}
				if stage == "tts" && status == "success" {
					foundTTSSuccessStage = true
				}
			}
		case "mirrormere_voice_tts_chars_per_second":
			for _, metric := range mf.GetMetric() {
				var voiceLbl string
				for _, lbl := range metric.GetLabel() {
					if lbl.GetName() == "voice" {
						voiceLbl = lbl.GetValue()
					}
				}
				if voiceLbl == "kokoro" && metric.GetGauge().GetValue() > 0 {
					foundCPS = true
				}
			}
		case "mirrormere_voice_turns_total":
			for _, metric := range mf.GetMetric() {
				for _, lbl := range metric.GetLabel() {
					if lbl.GetName() == "status" && lbl.GetValue() == "success" && metric.GetCounter().GetValue() == 1 {
						foundSuccessTurn = true
					}
				}
			}
		}
	}

	if !foundTTSSuccessStage {
		t.Errorf("missing tts success stage duration metric for streaming brain")
	}
	if !foundCPS {
		t.Errorf("missing tts_chars_per_second metric for streaming brain")
	}
	if !foundSuccessTurn {
		t.Errorf("missing turns_total success counter metric for streaming brain")
	}
}

func TestMetrics_RecordFalseWakes(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	m.RecordFalseWakes("kitchen-display", 3)
	// <= 0 ignored
	m.RecordFalseWakes("kitchen-display", 0)
	m.RecordFalseWakes("kitchen-display", -2)
	// clamped to 1000
	m.RecordFalseWakes("kitchen-display", 1500)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	var found bool
	for _, mf := range mfs {
		if mf.GetName() == "mirrormere_voice_false_wakes_total" {
			found = true
			val := mf.GetMetric()[0].GetCounter().GetValue()
			if val != 1003 { // 3 + 1000
				t.Fatalf("expected 1003 false wakes, got %f", val)
			}
		}
	}
	if !found {
		t.Fatal("mirrormere_voice_false_wakes_total metric not found")
	}

	// Nil safety check
	var mNil *Metrics
	mNil.RecordFalseWakes("kitchen-display", 5)
}

func TestMetrics_RecordStageDuration_DeliberationClamp(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	// 120s deliberation turn accepted
	m.RecordStageDuration("kitchen-display", "brain", "success", 120.0)
	// > 300s deliberation turn rejected
	m.RecordStageDuration("kitchen-display", "brain", "timeout", 305.0)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	var foundBrainCount int
	for _, mf := range mfs {
		if mf.GetName() == "mirrormere_voice_stage_duration_seconds" {
			for _, metric := range mf.GetMetric() {
				for _, lbl := range metric.GetLabel() {
					if lbl.GetName() == "stage" && lbl.GetValue() == "brain" {
						foundBrainCount++
					}
				}
			}
		}
	}
	if foundBrainCount != 1 {
		t.Fatalf("expected 1 brain metric observed (120s accepted, 305s ignored), got %d", foundBrainCount)
	}
}


