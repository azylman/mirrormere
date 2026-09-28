package voice

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
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
				DataLength    int    `json:"data_length"`
				PayloadLength int    `json:"payload_length"`
			}
			_ = json.Unmarshal(line, &hdr)
			if hdr.DataLength > 0 {
				dataBytes := make([]byte, hdr.DataLength)
				_, _ = io.ReadFull(reader, dataBytes)
			}
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
	_, err = brain.Ask(context.Background(), AskRequest{Prompt: "hi", SessionID: "s1"}, nil)
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
	// scanSSE's buffer is 8MB, up from bufio.Scanner's 64KB default — a
	// `sentence` event's base64-encoded WAV audio (MANDOS streaming,
	// specs/2026-09-28-mandos-streaming.md) can easily exceed 64KB for a
	// few seconds of speech, so 70000 bytes (the old test's size) no
	// longer exceeds the limit. Assert against a line that exceeds the
	// new, larger limit instead.
	longLine := "data: " + strings.Repeat("A", 9*1024*1024) + "\n\n"
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

// --- MANDOS streaming (specs/2026-09-28-mandos-streaming.md) ---

// fakeStreamingBrain implements both BrainClient and StreamingBrainClient so
// Hub-level tests can exercise the streaming path, the non-streaming
// fallback, and mid-stream failure without a real HTTP server.
type fakeStreamingBrain struct {
	mu sync.Mutex

	// AskStream behavior
	sentences  []fakeSentence
	streamErr  error // returned by AskStream after emitting `sentences`
	finalReply string
	statuses   []string

	// Ask (fallback) behavior
	askReply string
	askErr   error

	calledAsk       bool
	calledAskStream bool
}

type fakeSentence struct {
	text, engine string
	wav          []byte
}

func (f *fakeStreamingBrain) Ask(ctx context.Context, req AskRequest, onStatus func(status string)) (string, error) {
	f.mu.Lock()
	f.calledAsk = true
	f.mu.Unlock()
	for _, s := range f.statuses {
		if onStatus != nil {
			onStatus(s)
		}
	}
	return f.askReply, f.askErr
}

func (f *fakeStreamingBrain) AskStream(ctx context.Context, req AskRequest, onStatus func(status string), onSentence func(text, engine string, wav []byte) error) (string, error) {
	f.mu.Lock()
	f.calledAskStream = true
	f.mu.Unlock()
	for _, s := range f.statuses {
		if onStatus != nil {
			onStatus(s)
		}
	}
	for _, sent := range f.sentences {
		if err := onSentence(sent.text, sent.engine, sent.wav); err != nil {
			return f.finalReply, err
		}
	}
	if f.streamErr != nil {
		return f.finalReply, f.streamErr
	}
	return f.finalReply, nil
}

func TestDefaultBrainClient_AskStream_SSE(t *testing.T) {
	t.Parallel()

	wav1 := []byte("wav-bytes-one")
	wav2 := []byte("wav-bytes-two")
	body := "event: status\ndata: {\"status\":\"thinking\"}\n\n" +
		"event: sentence\ndata: {\"text\":\"Hello there.\",\"engine\":\"kokoro\",\"audio_b64\":\"" + base64.StdEncoding.EncodeToString(wav1) + "\"}\n\n" +
		"event: sentence\ndata: {\"text\":\"How are you?\",\"engine\":\"kokoro\",\"audio_b64\":\"" + base64.StdEncoding.EncodeToString(wav2) + "\"}\n\n" +
		"event: reply\ndata: {\"reply\":\"Hello there. How are you?\"}\n\n" +
		"event: done\ndata: {}\n\n"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(body))
	}))
	defer ts.Close()

	b := NewDefaultBrainClientWithStream("http://unused.invalid/ask", ts.URL, 5)

	var statuses []string
	var gotSentences []fakeSentence
	reply, err := b.AskStream(context.Background(), AskRequest{Prompt: "hi"}, func(status string) {
		statuses = append(statuses, status)
	}, func(text, engine string, wav []byte) error {
		gotSentences = append(gotSentences, fakeSentence{text: text, engine: engine, wav: wav})
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reply != "Hello there. How are you?" {
		t.Errorf("unexpected reply: %q", reply)
	}
	if len(statuses) != 1 || statuses[0] != "thinking" {
		t.Errorf("expected status forwarded, got: %+v", statuses)
	}
	if len(gotSentences) != 2 {
		t.Fatalf("expected 2 sentences, got %d: %+v", len(gotSentences), gotSentences)
	}
	if gotSentences[0].text != "Hello there." || !bytes.Equal(gotSentences[0].wav, wav1) {
		t.Errorf("sentence 0 mismatch: %+v", gotSentences[0])
	}
	if gotSentences[1].text != "How are you?" || !bytes.Equal(gotSentences[1].wav, wav2) {
		t.Errorf("sentence 1 mismatch: %+v", gotSentences[1])
	}
	if gotSentences[0].engine != "kokoro" || gotSentences[1].engine != "kokoro" {
		t.Errorf("expected engine forwarded, got: %+v", gotSentences)
	}
}

func TestDefaultBrainClient_AskStream_NotConfigured(t *testing.T) {
	t.Parallel()
	b := NewDefaultBrainClient("http://unused.invalid/ask", 5)
	sb, ok := b.(StreamingBrainClient)
	if !ok {
		t.Fatal("expected DefaultBrainClient to implement StreamingBrainClient")
	}
	_, err := sb.AskStream(context.Background(), AskRequest{Prompt: "hi"}, nil, func(string, string, []byte) error {
		t.Fatal("onSentence should never be called when streaming isn't configured")
		return nil
	})
	if !errors.Is(err, ErrStreamingNotConfigured) {
		t.Fatalf("expected ErrStreamingNotConfigured, got: %v", err)
	}
}

func TestDefaultBrainClient_AskStream_ErrorEvent(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: error\ndata: {\"error\":\"empty_transcript\"}\n\n"))
	}))
	defer ts.Close()

	b := NewDefaultBrainClientWithStream("http://unused.invalid/ask", ts.URL, 5)
	_, err := b.AskStream(context.Background(), AskRequest{Prompt: "hi"}, nil, func(string, string, []byte) error {
		t.Fatal("onSentence should not be called for an error event")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "empty_transcript") {
		t.Fatalf("expected error event surfaced, got: %v", err)
	}
}

func TestDefaultBrainClient_AskStream_Non2xxAndBadContentType(t *testing.T) {
	t.Parallel()

	ts404 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer ts404.Close()
	b404 := NewDefaultBrainClientWithStream("http://unused.invalid/ask", ts404.URL, 5)
	if _, err := b404.AskStream(context.Background(), AskRequest{Prompt: "hi"}, nil, nil); err == nil {
		t.Fatal("expected error on 404")
	}

	tsJSON := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"reply":"not a stream"}`))
	}))
	defer tsJSON.Close()
	bJSON := NewDefaultBrainClientWithStream("http://unused.invalid/ask", tsJSON.URL, 5)
	if _, err := bJSON.AskStream(context.Background(), AskRequest{Prompt: "hi"}, nil, nil); err == nil {
		t.Fatal("expected error on non-event-stream content-type")
	}
}

func TestHub_Interact_StreamingSuccess(t *testing.T) {
	t.Parallel()
	cfg := &config.VoiceHubConfig{Enabled: true}
	coord := NewCoordinator(nil)
	stt := &mockSTT{text: "hello"}
	wav1 := []byte("wav-one")
	wav2 := []byte("wav-two")
	brain := &fakeStreamingBrain{
		sentences: []fakeSentence{
			{text: "Hi.", engine: "kokoro", wav: wav1},
			{text: "There.", engine: "kokoro", wav: wav2},
		},
		finalReply: "Hi. There.",
	}
	h := NewHub(cfg, coord, WithSTTClient(stt), WithBrainClient(brain))

	type recorded struct {
		event string
		data  any
	}
	var events []recorded
	sink := func(event string, data any) error {
		events = append(events, recorded{event, data})
		return nil
	}

	err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(100)), "", "", sink)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !brain.calledAskStream {
		t.Error("expected AskStream to be called")
	}
	if brain.calledAsk {
		t.Error("did not expect fallback Ask() to be called on a fully successful stream")
	}

	var chunks []map[string]any
	var replyIdx, doneIdx = -1, -1
	for i, e := range events {
		if e.event == "audio_chunk" {
			chunks = append(chunks, e.data.(map[string]any))
		}
		if e.event == "reply" {
			replyIdx = i
		}
		if e.event == "done" {
			doneIdx = i
		}
	}
	if len(chunks) != 3 {
		t.Fatalf("expected 3 audio_chunk events (2 sentences + terminal), got %d: %+v", len(chunks), chunks)
	}
	for i, c := range chunks {
		if c["chunk_index"] != i {
			t.Errorf("chunk %d: expected chunk_index %d, got %v", i, i, c["chunk_index"])
		}
	}
	if chunks[0]["is_final"] != false || chunks[1]["is_final"] != false {
		t.Errorf("expected only the terminal chunk to carry is_final:true, got %+v", chunks)
	}
	if chunks[2]["is_final"] != true || chunks[2]["data"] != "" {
		t.Errorf("expected terminal chunk to be is_final:true with empty data, got %+v", chunks[2])
	}
	if replyIdx == -1 || doneIdx == -1 || replyIdx >= doneIdx {
		t.Errorf("expected reply before done, got events: %+v", events)
	}
	// The terminal audio_chunk and reply may be emitted in either order
	// relative to each other, but both must precede done and both must
	// follow every sentence chunk.
	if len(chunks) > 0 {
		lastSentenceChunkIdx := -1
		for i, e := range events {
			if e.event == "audio_chunk" && e.data.(map[string]any)["is_final"] == false {
				lastSentenceChunkIdx = i
			}
		}
		if lastSentenceChunkIdx >= replyIdx {
			t.Errorf("expected sentence audio to precede the reply event")
		}
	}
	if coord.GetState().State != StateIdle {
		t.Errorf("expected coordinator reset to idle after done, got: %s", coord.GetState().State)
	}
}

func TestHub_Interact_StreamingFallbackBeforeAnySentence(t *testing.T) {
	t.Parallel()
	cfg := &config.VoiceHubConfig{Enabled: true}
	coord := NewCoordinator(nil)
	stt := &mockSTT{text: "hello"}
	brain := &fakeStreamingBrain{
		streamErr: errors.New("stream endpoint 404"), // no sentences emitted first
		askReply:  "fallback reply",
	}
	h := NewHub(cfg, coord, WithSTTClient(stt), WithBrainClient(brain))

	var sawReply, sawDone bool
	var replyText string
	sink := func(event string, data any) error {
		if event == "reply" {
			sawReply = true
			replyText = data.(map[string]string)["reply"]
		}
		if event == "done" {
			sawDone = true
		}
		return nil
	}

	err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(100)), "", "", sink)
	if err != nil {
		t.Fatalf("expected fallback turn to complete without error, got: %v", err)
	}
	if !brain.calledAskStream {
		t.Error("expected AskStream to have been attempted")
	}
	if !brain.calledAsk {
		t.Error("expected fallback to non-streaming Ask() after a stream failure with zero sentences")
	}
	if !sawReply || replyText != "fallback reply" {
		t.Errorf("expected fallback reply emitted, sawReply=%v text=%q", sawReply, replyText)
	}
	if !sawDone {
		t.Error("expected turn to complete with a done event")
	}
}

func TestHub_Interact_StreamingMidFailureAborts(t *testing.T) {
	t.Parallel()
	cfg := &config.VoiceHubConfig{Enabled: true}
	coord := NewCoordinator(nil)
	stt := &mockSTT{text: "hello"}
	brain := &fakeStreamingBrain{
		sentences: []fakeSentence{
			{text: "Hi.", engine: "kokoro", wav: []byte("wav")},
		},
		streamErr:  errors.New("connection reset mid-stream"),
		finalReply: "Hi.",
	}
	h := NewHub(cfg, coord, WithSTTClient(stt), WithBrainClient(brain))

	var events []string
	sink := func(event string, data any) error {
		events = append(events, event)
		return nil
	}

	err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(100)), "", "", sink)
	if !errors.Is(err, ErrBrainFailed) {
		t.Fatalf("expected ErrBrainFailed, got: %v", err)
	}
	if brain.calledAsk {
		t.Error("must not replay via non-streaming Ask() after audio already played")
	}
	for _, e := range events {
		if e == "done" {
			t.Error("must not emit done after a mid-stream failure")
		}
	}
	if coord.GetState().State != StateError {
		t.Errorf("expected state error, got: %s", coord.GetState().State)
	}
}

func TestHub_Interact_StreamingSentenceMissingAudio_TTSFallback(t *testing.T) {
	t.Parallel()
	cfg := &config.VoiceHubConfig{Enabled: true}
	coord := NewCoordinator(nil)
	stt := &mockSTT{text: "hello"}
	brain := &fakeStreamingBrain{
		sentences: []fakeSentence{
			{text: "Hi there.", engine: "kokoro", wav: nil}, // no audio from the gateway
		},
		finalReply: "Hi there.",
	}
	tts := &mockTTS{audio: []byte("local-wav"), format: "wav"}
	h := NewHub(cfg, coord, WithSTTClient(stt), WithBrainClient(brain), WithTTSClient(tts))

	var chunks []map[string]any
	sink := func(event string, data any) error {
		if event == "audio_chunk" {
			chunks = append(chunks, data.(map[string]any))
		}
		return nil
	}

	err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(100)), "", "", sink)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tts.calledWith != "Hi there." {
		t.Errorf("expected local TTS fallback synthesis of the sentence text, got calledWith=%q", tts.calledWith)
	}
	if len(chunks) < 1 {
		t.Fatal("expected at least one audio_chunk from the TTS fallback")
	}
	got, err := base64.StdEncoding.DecodeString(chunks[0]["data"].(string))
	if err != nil || !bytes.Equal(got, []byte("local-wav")) {
		t.Errorf("expected fallback audio in chunk 0, got %+v (err=%v)", chunks[0], err)
	}
}

func TestHub_Interact_StreamingSentenceMissingAudio_NoFallbackTTS_EmitsError(t *testing.T) {
	t.Parallel()
	cfg := &config.VoiceHubConfig{Enabled: true}
	coord := NewCoordinator(nil)
	stt := &mockSTT{text: "hello"}
	brain := &fakeStreamingBrain{
		sentences: []fakeSentence{
			{text: "Hi there.", engine: "kokoro", wav: nil},
		},
		finalReply: "Hi there.",
	}
	// No WithTTSClient: h.tts is nil, so there is no local fallback available.
	h := NewHub(cfg, coord, WithSTTClient(stt), WithBrainClient(brain))

	var sawError, sawDone bool
	sink := func(event string, data any) error {
		if event == "error" {
			sawError = true
		}
		if event == "done" {
			sawDone = true
		}
		return nil
	}

	err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(100)), "", "", sink)
	if err != nil {
		t.Fatalf("a missing sentence audio must not abort the turn, got: %v", err)
	}
	if !sawError {
		t.Error("expected a tts_error event when no fallback audio could be produced")
	}
	if !sawDone {
		t.Error("expected the turn to still complete")
	}
}

func TestHub_Interact_BlankTranscript(t *testing.T) {
	t.Parallel()
	cfg := &config.VoiceHubConfig{Enabled: true}
	coord := NewCoordinator(nil)
	stt := &mockSTT{text: "   "}
	brain := &mockBrain{reply: "should never be used"}
	h := NewHub(cfg, coord, WithSTTClient(stt), WithBrainClient(brain))

	var events []string
	var errPayload map[string]string
	sink := func(event string, data any) error {
		events = append(events, event)
		if event == "error" {
			errPayload = data.(map[string]string)
		}
		return nil
	}

	err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(100)), "", "", sink)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if brain.calledWith != "" {
		t.Errorf("brain must never be called for a blank transcript, calledWith=%q", brain.calledWith)
	}
	if errPayload["error"] != "empty_transcript" {
		t.Errorf("expected empty_transcript error event, got: %+v", errPayload)
	}
	found := map[string]bool{}
	for _, e := range events {
		found[e] = true
	}
	if !found["error"] || !found["done"] {
		t.Errorf("expected error and done events, got: %+v", events)
	}
	if found["transcript"] || found["reply"] || found["audio_chunk"] {
		t.Errorf("did not expect transcript/reply/audio_chunk events for a blank transcript, got: %+v", events)
	}
	if coord.GetState().State != StateIdle {
		t.Errorf("expected coordinator reset to idle, got: %s", coord.GetState().State)
	}
}

func TestHub_Interact_EmptyTranscriptAfterTrim(t *testing.T) {
	t.Parallel()
	cfg := &config.VoiceHubConfig{Enabled: true}
	coord := NewCoordinator(nil)
	stt := &mockSTT{text: ""}
	brain := &mockBrain{reply: "should never be used"}
	h := NewHub(cfg, coord, WithSTTClient(stt), WithBrainClient(brain))

	err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(100)), "", "", func(string, any) error { return nil })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if brain.calledWith != "" {
		t.Error("brain must never be called for an empty transcript")
	}
}
