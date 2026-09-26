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

	"github.com/azylman/mirrormere/internal/config"
	"github.com/azylman/mirrormere/internal/events"
)

type mockSTT struct {
	mu         sync.Mutex
	text       string
	err        error
	calledWith []byte
}

func (m *mockSTT) Transcribe(ctx context.Context, wavData []byte) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calledWith = wavData
	return m.text, m.err
}

type mockBrain struct {
	mu         sync.Mutex
	reply      string
	statuses   []string
	err        error
	calledWith string
}

func (m *mockBrain) Ask(ctx context.Context, prompt string, sessionID string, onStatus func(status string)) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calledWith = prompt
	for _, s := range m.statuses {
		if onStatus != nil {
			onStatus(s)
		}
	}
	return m.reply, m.err
}

type mockTTS struct {
	mu         sync.Mutex
	audio      []byte
	format     string
	err        error
	calledWith string
}

func (m *mockTTS) Synthesize(ctx context.Context, text string) ([]byte, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calledWith = text
	return m.audio, m.format, m.err
}

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
	tts := &mockTTS{audio: []byte("fake-mp3-bytes"), format: "mp3"}

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
	err := h.Interact(context.Background(), bytes.NewReader(wav), "kiosk-kitchen", "sess-1", sink)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	expectedSequence := []string{"state", "transcript", "status", "status", "reply", "audio_chunk", "done"}
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
	err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(100)), "", "", func(string, any) error { return nil })
	if !errors.Is(err, ErrHubDisabled) {
		t.Fatalf("expected ErrHubDisabled, got: %v", err)
	}
}

func TestHub_Interact_InvalidAudio(t *testing.T) {
	t.Parallel()
	h := NewHub(&config.VoiceHubConfig{Enabled: true}, nil)

	// Nil reader
	err := h.Interact(context.Background(), nil, "", "", func(string, any) error { return nil })
	if !errors.Is(err, ErrInvalidAudio) {
		t.Fatalf("expected ErrInvalidAudio for nil, got: %v", err)
	}

	// Too short (< 44 bytes)
	err = h.Interact(context.Background(), bytes.NewReader([]byte("RIFFshort")), "", "", func(string, any) error { return nil })
	if !errors.Is(err, ErrInvalidAudio) {
		t.Fatalf("expected ErrInvalidAudio for short, got: %v", err)
	}

	// Not RIFF
	notWav := make([]byte, 50)
	copy(notWav, []byte("NOT_A_WAV_FILE_HEADER"))
	err = h.Interact(context.Background(), bytes.NewReader(notWav), "", "", func(string, any) error { return nil })
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
		doneCh <- h.Interact(context.Background(), bytes.NewReader(wav), "", "", func(string, any) error { return nil })
	}()

	<-sttStarted

	// Second concurrent call must return ErrInteractionBusy
	secondErr := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(100)), "", "", func(string, any) error { return nil })
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

	err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(100)), "", "", sink)
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

	err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(100)), "", "", sink)
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

	err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(100)), "", "", sink)
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
		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				return
			}
			var hdr struct {
				Type          string `json:"type"`
				PayloadLength int    `json:"payload_length"`
			}
			_ = json.Unmarshal(line, &hdr)
			if hdr.PayloadLength > 0 {
				payload := make([]byte, hdr.PayloadLength)
				_, _ = io.ReadFull(reader, payload)
			}
			if hdr.Type == "audio-stop" {
				break
			}
		}

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
	reply, err := b.Ask(context.Background(), "turn on light", "s1", func(status string) {
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
	reply, err = bJSON.Ask(context.Background(), "hello", "s1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reply != "JSON reply" {
		t.Errorf("expected 'JSON reply', got %q", reply)
	}

	// Empty URL fallback
	bEmpty := NewDefaultBrainClient("", 5)
	reply, err = bEmpty.Ask(context.Background(), "test prompt", "s1", nil)
	if err != nil || !strings.Contains(reply, "test prompt") {
		t.Errorf("expected fallback containing test prompt, got: %q, err: %v", reply, err)
	}

	// Server error
	errServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}))
	defer errServer.Close()
	bErr := NewDefaultBrainClient(errServer.URL, 5)
	_, err = bErr.Ask(context.Background(), "fail", "s1", nil)
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Errorf("expected 502 error, got: %v", err)
	}
}

func TestDefaultTTSClient(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("fake-mp3-bytes"))
	}))
	defer ts.Close()

	tts := NewDefaultTTSClient(ts.URL, "kokoro", "af_bella", 5)
	audio, fmtStr, err := tts.Synthesize(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(audio) != "fake-mp3-bytes" || fmtStr != "mp3" {
		t.Errorf("expected fake-mp3-bytes mp3, got %s %s", string(audio), fmtStr)
	}

	// Empty URL
	ttsEmpty := NewDefaultTTSClient("", "kokoro", "af_bella", 5)
	audio, fmtStr, err = ttsEmpty.Synthesize(context.Background(), "hello")
	if err != nil || len(audio) != 0 || fmtStr != "mp3" {
		t.Errorf("expected empty audio, got %v %s %v", audio, fmtStr, err)
	}

	// WAV response format
	wavServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write([]byte("fake-wav-bytes"))
	}))
	defer wavServer.Close()
	ttsWAV := NewDefaultTTSClient(wavServer.URL, "kokoro", "af_bella", 5)
	audio, fmtStr, err = ttsWAV.Synthesize(context.Background(), "hello wav")
	if err != nil || string(audio) != "fake-wav-bytes" || fmtStr != "wav" {
		t.Errorf("expected fake-wav-bytes wav, got %s %s %v", string(audio), fmtStr, err)
	}

	// Server error
	errServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "tts service error", http.StatusInternalServerError)
	}))
	defer errServer.Close()
	ttsErr := NewDefaultTTSClient(errServer.URL, "kokoro", "af_bella", 5)
	_, _, err = ttsErr.Synthesize(context.Background(), "fail")
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("expected 500 error, got: %v", err)
	}
}

func TestHub_Interact_EdgeCases(t *testing.T) {
	t.Parallel()

	cfg := &config.VoiceHubConfig{Enabled: true}

	t.Run("nil STT returns ErrSTTFailed", func(t *testing.T) {
		t.Parallel()
		h := NewHub(cfg, nil, WithSTTClient(nil), WithBrainClient(&mockBrain{}), WithTTSClient(&mockTTS{}))
		err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(100)), "", "", func(event string, data any) error {
			return nil
		})
		if !errors.Is(err, ErrSTTFailed) {
			t.Fatalf("expected ErrSTTFailed, got: %v", err)
		}
	})

	t.Run("nil Brain returns ErrBrainFailed", func(t *testing.T) {
		t.Parallel()
		h := NewHub(cfg, nil, WithSTTClient(&mockSTT{text: "hello"}), WithBrainClient(nil), WithTTSClient(&mockTTS{}))
		err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(100)), "", "", func(event string, data any) error {
			return nil
		})
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
		})
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
		})
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
		})
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
	rep, err := b1.Ask(context.Background(), "q", "s", nil)
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
	rep, err = b2.Ask(context.Background(), "q", "s", nil)
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
	_, err = b3.Ask(context.Background(), "q", "s", nil)
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
	rep, err = b4.Ask(context.Background(), "q", "s", nil)
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
		tts := &mockTTS{audio: []byte("wav"), format: "wav"}
		h := NewHub(cfg, coord, WithSTTClient(stt), WithBrainClient(brain), WithTTSClient(tts))

		err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(100)), "", "", func(event string, data any) error {
			if event == "status" {
				return errors.New("status sink error")
			}
			return nil
		})
		if err != nil {
			t.Fatalf("status sink error should be tolerated, got: %v", err)
		}
	})

	t.Run("sink error on audio_chunk aborts", func(t *testing.T) {
		t.Parallel()
		stt := &mockSTT{text: "hello"}
		brain := &mockBrain{reply: "hi"}
		tts := &mockTTS{audio: []byte("wav"), format: "wav"}
		h := NewHub(cfg, coord, WithSTTClient(stt), WithBrainClient(brain), WithTTSClient(tts))

		err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(100)), "", "", func(event string, data any) error {
			if event == "audio_chunk" {
				return errors.New("audio sink error")
			}
			return nil
		})
		if err == nil || !strings.Contains(err.Error(), "audio sink error") {
			t.Fatalf("expected audio sink error, got: %v", err)
		}
	})

	t.Run("sink error on done aborts", func(t *testing.T) {
		t.Parallel()
		stt := &mockSTT{text: "hello"}
		brain := &mockBrain{reply: "hi"}
		tts := &mockTTS{audio: []byte("wav"), format: "wav"}
		h := NewHub(cfg, coord, WithSTTClient(stt), WithBrainClient(brain), WithTTSClient(tts))

		err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(100)), "", "", func(event string, data any) error {
			if event == "done" {
				return errors.New("done sink error")
			}
			return nil
		})
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
		})
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
		})
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
		})
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
	_, err = brain.Ask(context.Background(), "hi", "s1", nil)
	if err == nil {
		t.Error("expected network error for brain client")
	}

	// TTS client network failure
	tts := NewDefaultTTSClient("http://127.0.0.1:59998/invalid", "k", "v", 1)
	_, _, err = tts.Synthesize(context.Background(), "hi")
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
	reply, err := brain.Ask(context.Background(), "hi", "s1", nil)
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
	_, err := brain.Ask(context.Background(), "hi", "s1", nil)
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
		_, _ = conn.Write([]byte("{\"type\":\"event\",\"payload_length\":100}\nshort"))
	}()
	c2 := NewDefaultSTTClient("tcp://" + l2.Addr().String())
	_, err = c2.Transcribe(context.Background(), makeValidWAV(100))
	if err == nil {
		t.Fatal("expected error on truncated payload")
	}
}


