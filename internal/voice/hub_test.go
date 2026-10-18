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

type mockBlockingSTT struct {
	onTranscribe func()
}

func (m *mockBlockingSTT) Transcribe(ctx context.Context, wavData []byte) (string, error) {
	m.onTranscribe()
	return "test", nil
}

func makeValidWAV(sampleCount int) []byte {
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

func mkSTT(text string, err error) *mockSTT {
	return &mockSTT{text: text, err: err}
}

func wavRdr(n int) io.Reader {
	return bytes.NewReader(makeValidWAV(n))
}

func wavWithZeroRateAndChannels(samples int) []byte {
	b := makeValidWAV(samples)
	copy(b[22:24], []byte{0, 0})
	copy(b[24:28], []byte{0, 0, 0, 0})
	return b
}

func startWyomingServer(t *testing.T, handler func(conn net.Conn, r *bufio.Reader)) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		handler(conn, bufio.NewReader(conn))
	}()
	return "tcp://" + l.Addr().String()
}

func TestHub_Interact_Success(t *testing.T) {
	t.Parallel()

	cfg := &config.VoiceHubConfig{
		Enabled: true,
	}
	hubEvents := events.NewHub(events.HubConfig{}, nil, nil)
	defer hubEvents.Close()
	coord := NewCoordinator(hubEvents)

	stt := mkSTT("Turn off office lights", nil)
	brain := &mockBrain{
		reply:    "Turned off office lights.",
		statuses: []string{"⚡ Checking devices...", "⚡ Turning off lights..."},
	}
	tts := mkTTS([]byte("fake-pcm-bytes"), "pcm", nil)

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

	st := coord.GetState()
	if st.State != StateSpeaking {
		t.Errorf("expected final state speaking, got %s", st.State)
	}
	if st.Reply == nil || *st.Reply != brain.reply {
		t.Errorf("expected final reply %q, got %v", brain.reply, st.Reply)
	}

	reset := coord.Reset()
	if reset.State != StateIdle {
		t.Errorf("expected state idle after reset, got %s", reset.State)
	}
}

func TestHub_Interact_Scenarios(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		disabled     bool
		noCoord      bool
		audio        io.Reader
		stt          STTClient
		brain        BrainClient
		tts          TTSClient
		sinkErrEvent string
		wantErr      error
		wantErrStr   string
		wantState    string
		wantReply    string
		wantEvents   []string
	}{
		{"disabled", true, false, wavRdr(100), nil, nil, nil, "", ErrHubDisabled, "", "", "", nil},
		{"invalid_audio_nil", false, false, nil, nil, nil, nil, "", ErrInvalidAudio, "", "", "", nil},
		{"invalid_audio_short", false, false, strings.NewReader("RIFFshort"), nil, nil, nil, "", ErrInvalidAudio, "", "", "", nil},
		{"invalid_audio_non_riff", false, false, strings.NewReader(strings.Repeat("NOT_WAV_", 7)), nil, nil, nil, "", ErrInvalidAudio, "", "", "", nil},
		{"stt_failure", false, false, wavRdr(100), mkSTT("", errors.New("whisper offline")), nil, nil, "", ErrSTTFailed, "", StateError, "", []string{"error"}},
		{"brain_failure", false, false, wavRdr(100), mkSTT("hello", nil), mkBrain("", errors.New("aerial unreachable")), nil, "", ErrBrainFailed, "", StateError, "", nil},
		{"tts_degradation", false, false, wavRdr(100), mkSTT("hello", nil), mkBrain("hi there", nil), mkTTS(nil, "", errors.New("kokoro timeout")), "", nil, "", StateSpeaking, "hi there", []string{"reply", "error", "done"}},
		{"nil_tts_speaking", false, false, wavRdr(100), mkSTT("hello", nil), mkBrain("quiet hours response", nil), nil, "", nil, "", StateSpeaking, "quiet hours response", nil},
		{"nil_stt_coord_error", false, false, wavRdr(100), nil, mkBrain("", nil), mkTTS(nil, "", nil), "", ErrSTTFailed, "", StateError, "", nil},
		{"nil_brain_coord_error", false, false, wavRdr(100), mkSTT("hello", nil), nil, mkTTS(nil, "", nil), "", ErrBrainFailed, "", StateError, "", nil},
		{"sink_state_coord_error", false, false, wavRdr(100), mkSTT("hello", nil), mkBrain("hi", nil), mkTTS(nil, "", nil), "state", nil, "sink error on state", StateError, "", nil},
		{"sink_transcript_coord_error", false, false, wavRdr(100), mkSTT("hello", nil), mkBrain("hi", nil), mkTTS(nil, "", nil), "transcript", nil, "sink error on transcript", StateError, "", nil},
		{"sink_reply_coord_error", false, false, wavRdr(100), mkSTT("hello", nil), mkBrain("hi", nil), mkTTS(nil, "", nil), "reply", nil, "sink error on reply", StateError, "", nil},
		{"sink_done_coord_error", false, false, wavRdr(100), mkSTT("hello", nil), mkBrain("hi", nil), mkTTS(nil, "", nil), "done", nil, "sink error on done", StateError, "", nil},
		{"nil_stt_no_coord", false, true, wavRdr(100), nil, mkBrain("", nil), mkTTS(nil, "", nil), "", ErrSTTFailed, "", "", "", nil},
		{"nil_brain_no_coord", false, true, wavRdr(100), mkSTT("hello", nil), nil, mkTTS(nil, "", nil), "", ErrBrainFailed, "", "", "", nil},
		{"sink_state_no_coord", false, true, wavRdr(100), mkSTT("hello", nil), mkBrain("hi", nil), mkTTS(nil, "", nil), "state", nil, "sink error on state", "", "", nil},
		{"sink_transcript_no_coord", false, true, wavRdr(100), mkSTT("hello", nil), mkBrain("hi", nil), mkTTS(nil, "", nil), "transcript", nil, "sink error on transcript", "", "", nil},
		{"sink_reply_no_coord", false, true, wavRdr(100), mkSTT("hello", nil), mkBrain("hi", nil), mkTTS(nil, "", nil), "reply", nil, "sink error on reply", "", "", nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := &config.VoiceHubConfig{Enabled: !tc.disabled}
			var coord *Coordinator
			if !tc.noCoord {
				coord = NewCoordinator(nil)
			}
			h := NewHub(cfg, coord,
				WithSTTClient(tc.stt),
				WithBrainClient(tc.brain),
				WithTTSClient(tc.tts),
			)

			var mu sync.Mutex
			var emitted []string
			sink := func(event string, data any) error {
				mu.Lock()
				defer mu.Unlock()
				emitted = append(emitted, event)
				if tc.sinkErrEvent != "" && event == tc.sinkErrEvent {
					return fmt.Errorf("sink error on %s", event)
				}
				return nil
			}

			err := h.Interact(context.Background(), tc.audio, "", "", sink, EdgeTimings{})
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected error %v, got %v", tc.wantErr, err)
			}
			if tc.wantErrStr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErrStr)) {
				t.Fatalf("expected error containing %q, got: %v", tc.wantErrStr, err)
			}
			if tc.wantErr == nil && tc.wantErrStr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if coord != nil && tc.wantState != "" && coord.GetState().State != tc.wantState {
				t.Errorf("expected state %s, got %s", tc.wantState, coord.GetState().State)
			}
			if coord != nil && tc.wantReply != "" {
				rep := coord.GetState().Reply
				if rep == nil || *rep != tc.wantReply {
					t.Errorf("expected reply %q, got %v", tc.wantReply, rep)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			for _, exp := range tc.wantEvents {
				found := false
				for _, ev := range emitted {
					if ev == exp {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected event %q in emitted: %v", exp, emitted)
				}
			}
		})
	}
}

func TestHub_Interact_SinkErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		stt        STTClient
		brain      BrainClient
		tts        TTSClient
		failEvent  string
		wantErrStr string
	}{
		{"status_non_fatal", mkSTT("hello", nil), &mockBrain{reply: "hi", statuses: []string{"thinking..."}}, mkTTS([]byte("wav"), "pcm", nil), "status", ""},
		{"audio_chunk_aborts", mkSTT("hello", nil), mkBrain("hi", nil), mkTTS([]byte("wav"), "pcm", nil), "audio_chunk", "audio sink error"},
		{"done_aborts", mkSTT("hello", nil), mkBrain("hi", nil), mkTTS([]byte("wav"), "pcm", nil), "done", "done sink error"},
		{"stt_error_propagates", mkSTT("", errors.New("stt failure")), nil, nil, "error", "sink error on stt failure"},
		{"brain_error_propagates", mkSTT("hello", nil), mkBrain("", errors.New("brain failure")), nil, "error", "sink error on brain failure"},
		{"tts_error_propagates", mkSTT("hello", nil), mkBrain("hi", nil), mkTTS(nil, "", errors.New("tts failure")), "error", "sink error on tts failure"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := &config.VoiceHubConfig{Enabled: true}
			coord := NewCoordinator(nil)
			h := NewHub(cfg, coord,
				WithSTTClient(tc.stt),
				WithBrainClient(tc.brain),
				WithTTSClient(tc.tts),
			)

			sink := func(event string, data any) error {
				if event == tc.failEvent {
					if tc.wantErrStr != "" {
						return errors.New(tc.wantErrStr)
					}
					return errors.New("status sink error")
				}
				return nil
			}

			err := h.Interact(context.Background(), wavRdr(100), "", "", sink, EdgeTimings{})
			if tc.wantErrStr == "" {
				if err != nil {
					t.Fatalf("expected nil error, got: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErrStr) {
				t.Fatalf("expected error containing %q, got: %v", tc.wantErrStr, err)
			}
		})
	}
}

func TestHub_Interact_ConcurrencyAndPanic(t *testing.T) {
	t.Parallel()

	t.Run("concurrent_busy", func(t *testing.T) {
		t.Parallel()
		cfg := &config.VoiceHubConfig{Enabled: true}
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
			doneCh <- h.Interact(context.Background(), wavRdr(100), "", "", func(string, any) error { return nil }, EdgeTimings{})
		}()

		<-sttStarted
		secondErr := h.Interact(context.Background(), wavRdr(100), "", "", func(string, any) error { return nil }, EdgeTimings{})
		if !errors.Is(secondErr, ErrInteractionBusy) {
			t.Errorf("expected ErrInteractionBusy, got: %v", secondErr)
		}
		close(sttBlock)
		firstErr := <-doneCh
		if errors.Is(firstErr, ErrInteractionBusy) {
			t.Errorf("first call should not be busy: %v", firstErr)
		}
	})

	t.Run("panic_recovers_and_transitions_coordinator", func(t *testing.T) {
		t.Parallel()
		cfg := &config.VoiceHubConfig{Enabled: true}
		coord := NewCoordinator(nil)
		panickingSTT := &mockBlockingSTT{
			onTranscribe: func() {
				panic("unexpected boom")
			},
		}
		h := NewHub(cfg, coord, WithSTTClient(panickingSTT))

		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("expected panic, got none")
			}
			if coord.GetState().State != StateError {
				t.Errorf("expected coordinator state error on panic, got %s", coord.GetState().State)
			}
		}()

		_ = h.Interact(context.Background(), wavRdr(100), "", "", func(string, any) error { return nil }, EdgeTimings{})
	})
}

func TestDefaultSTTClient_Wyoming(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		drain    bool
		resp     string
		wantText string
		wantErr  bool
	}{
		{"wyoming_success", true, fmt.Sprintf("{\"type\":\"transcript\",\"data_length\":%d}\n{\"text\":\"test transcript from wyoming\"}", len(`{"text":"test transcript from wyoming"}`)), "test transcript from wyoming", false},
		{"wyoming_advanced", true, fmt.Sprintf("not-json-line\n{\"type\":\"ping\",\"payload_length\":4}\npong{\"type\":\"transcript\",\"data_length\":%d}\n{\"text\":\"advanced transcript\"}", len(`{"text":"advanced transcript"}`)), "advanced transcript", false},
		{"server_early_close", false, "", "", true},
		{"raw_text_fallback", true, "{\"type\":\"transcript\",\"data_length\":23}\nraw transcript fallback", "raw transcript fallback", false},
		{"truncated_data_length", true, "{\"type\":\"event\",\"data_length\":100}\nshort", "", true},
		{"truncated_payload_length", true, "{\"type\":\"event\",\"payload_length\":100}\nshort", "", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			addr := startWyomingServer(t, func(conn net.Conn, r *bufio.Reader) {
				if tc.drain {
					drainMockWyomingRequest(r)
				}
				if len(tc.resp) > 0 {
					_, _ = conn.Write([]byte(tc.resp))
				}
			})
			client := NewDefaultSTTClient(addr)
			txt, err := client.Transcribe(context.Background(), makeValidWAV(100))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if txt != tc.wantText {
				t.Fatalf("expected %q, got %q", tc.wantText, txt)
			}
		})
	}
}

func TestDefaultSTTClient_HTTPAndErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		status     int
		respBody   string
		rawURL     string
		wantText   string
		wantErr    bool
		wantErrStr string
	}{
		{"http_success", http.StatusOK, `{"text":"test transcript from http"}`, "", "test transcript from http", false, ""},
		{"transcript_fallback", http.StatusOK, `{"transcript":"fallback transcript"}`, "", "fallback transcript", false, ""},
		{"malformed_json", http.StatusOK, `not valid json`, "", "", true, "stt decode error"},
		{"empty_url", 0, "", "", "", true, "stt url is empty"},
		{"tcp_dial_failure", 0, "", "tcp://127.0.0.1:59999", "", true, ""},
		{"http_500", http.StatusInternalServerError, `internal server error`, "", "", true, "500"},
		{"network_failure", 0, "", "http://127.0.0.1:59998/invalid", "", true, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			targetURL := tc.rawURL
			if tc.status > 0 {
				ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.respBody))
				}))
				defer ts.Close()
				targetURL = ts.URL
			}
			client := NewDefaultSTTClient(targetURL)
			txt, err := client.Transcribe(context.Background(), makeValidWAV(100))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tc.wantErrStr != "" && !strings.Contains(err.Error(), tc.wantErrStr) {
					t.Fatalf("expected error containing %q, got: %v", tc.wantErrStr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if txt != tc.wantText {
				t.Fatalf("expected %q, got %q", tc.wantText, txt)
			}
		})
	}
}

func TestDefaultBrainClient(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		contentType  string
		status       int
		rawURL       string
		respBody     string
		req          AskRequest
		wantReply    string
		wantStatuses []string
		wantErr      bool
		wantErrStr   string
		wantNodeID   string
	}{
		{"sse_success", "text/event-stream", 200, "", "event: status\ndata: {\"status\":\"⚡ executing tool\"}\n\nevent: reply\ndata: {\"reply\":\"The light is on\"}\n\nevent: done\ndata: {}\n\n", AskRequest{Prompt: "turn on light", SessionID: "s1"}, "The light is on", []string{"⚡ executing tool"}, false, "", ""},
		{"json_success", "application/json", 200, "", `{"reply":"JSON reply"}`, AskRequest{Prompt: "hello", SessionID: "s1"}, "JSON reply", nil, false, "", ""},
		{"turn_session_and_node_id", "application/json", 200, "", `{"reply":"ok"}`, AskRequest{Prompt: "hello", NodeID: "touch-kiosk-kitchen"}, "ok", nil, false, "", "touch-kiosk-kitchen"},
		{"empty_url_fallback", "", 0, "", "", AskRequest{Prompt: "test prompt", SessionID: "s1"}, "test prompt", nil, false, "", ""},
		{"status_502", "text/plain", 502, "", "bad gateway", AskRequest{Prompt: "fail", SessionID: "s1"}, "", nil, true, "502", ""},
		{"fallback_text", "application/json", 200, "", `{"text":"text fallback"}`, AskRequest{Prompt: "q", SessionID: "s"}, "text fallback", nil, false, "", ""},
		{"fallback_content", "application/json", 200, "", `{"content":"content fallback"}`, AskRequest{Prompt: "q", SessionID: "s"}, "content fallback", nil, false, "", ""},
		{"invalid_json", "application/json", 200, "", `invalid json`, AskRequest{Prompt: "q", SessionID: "s"}, "", nil, true, "", ""},
		{"sse_without_done", "text/event-stream", 200, "", "event: reply\ndata: {\"reply\":\"streamed without done\"}\n\n", AskRequest{Prompt: "q", SessionID: "s"}, "streamed without done", nil, false, "", ""},
		{"sse_malformed_data", "text/event-stream", 200, "", "event: status\ndata: {not-valid-json}\n\nevent: reply\ndata: {\"reply\":\"recovered\"}\n\nevent: done\ndata: {}\n\n", AskRequest{Prompt: "hi", SessionID: "s1"}, "recovered", nil, false, "", ""},
		{"sse_scanner_overflow", "text/event-stream", 200, "", "data: " + strings.Repeat("a", sseScannerMaxLine+1024) + "\n\n", AskRequest{Prompt: "hi", SessionID: "s1"}, "", nil, true, "", ""},
		{"network_failure", "", 0, "http://127.0.0.1:59998/invalid", "", AskRequest{Prompt: "hi", SessionID: "s1"}, "", nil, true, "", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			targetURL := tc.rawURL
			var mu sync.Mutex
			var lastBody map[string]any
			if tc.status > 0 {
				ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					_ = json.NewDecoder(r.Body).Decode(&lastBody)
					mu.Unlock()
					if tc.contentType != "" {
						w.Header().Set("Content-Type", tc.contentType)
					}
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.respBody))
				}))
				defer ts.Close()
				targetURL = ts.URL
			}

			client := NewDefaultBrainClient(targetURL, 5)
			var statuses []string
			reply, err := client.Ask(context.Background(), tc.req, func(s string) {
				statuses = append(statuses, s)
			})
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tc.wantErrStr != "" && !strings.Contains(err.Error(), tc.wantErrStr) {
					t.Fatalf("expected error containing %q, got: %v", tc.wantErrStr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(reply, tc.wantReply) {
				t.Fatalf("expected reply containing %q, got %q", tc.wantReply, reply)
			}
			if len(tc.wantStatuses) > 0 {
				if len(statuses) != len(tc.wantStatuses) {
					t.Fatalf("expected statuses %v, got %v", tc.wantStatuses, statuses)
				}
				for i, wantS := range tc.wantStatuses {
					if statuses[i] != wantS {
						t.Errorf("at index %d: expected status %q, got %q", i, wantS, statuses[i])
					}
				}
			}
			if tc.wantNodeID != "" {
				mu.Lock()
				defer mu.Unlock()
				if lastBody["node_id"] != tc.wantNodeID {
					t.Errorf("expected node_id %q, got %v", tc.wantNodeID, lastBody["node_id"])
				}
				sess, _ := lastBody["session_id"].(string)
				if !strings.HasPrefix(sess, "turn-") {
					t.Errorf("expected session_id starting with 'turn-', got %v", sess)
				}
				if _, exists := lastBody["device_name"]; exists {
					t.Errorf("expected no device_name, got %v", lastBody["device_name"])
				}
			}
		})
	}
}

func TestDefaultTTSClient(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		contentType    string
		status         int
		rawURL         string
		body           []byte
		text           string
		sinkErr        error
		wantErr        error
		wantErrStr     string
		wantFormat     string
		wantTotalBytes int
		wantRate       int
		wantChannels   int
	}{
		{"wav_stream", "audio/wav", 200, "", makeValidWAV(4800), "hello world", nil, nil, "", "pcm", 9600, 16000, 1},
		{"pcm_stream", "audio/pcm", 200, "", bytes.Repeat([]byte{0x01, 0x02}, 2400), "hello", nil, nil, "", "pcm", 4800, 0, 0},
		{"mp3_unsupported", "audio/mpeg", 200, "", []byte("fake-mp3"), "hello", nil, ErrUnsupportedAudioFormat, "", "", 0, 0, 0},
		{"empty_url", "", 0, "", nil, "hello", nil, nil, "", "", 0, 0, 0},
		{"empty_text", "", 0, "http://127.0.0.1:9", nil, "   ", nil, nil, "", "", 0, 0, 0},
		{"server_error", "text/plain", 500, "", []byte("server dead"), "hello", nil, nil, "500", "", 0, 0, 0},
		{"wav_sink_error", "audio/wav", 200, "", makeValidWAV(4800), "hello", errors.New("sink abort"), nil, "sink abort", "", 0, 0, 0},
		{"pcm_sink_error", "audio/pcm", 200, "", []byte("header-body-payload"), "hello", errors.New("pcm sink abort"), nil, "pcm sink abort", "", 0, 0, 0},
		{"wav_zero_rate_channels", "audio/wav", 200, "", wavWithZeroRateAndChannels(2400), "hello", nil, nil, "", "pcm", 0, 24000, 1},
		{"network_failure", "", 0, "http://127.0.0.1:59998/invalid", nil, "hi", nil, nil, "error", "", 0, 0, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			targetURL := tc.rawURL
			if tc.status > 0 {
				ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if tc.contentType != "" {
						w.Header().Set("Content-Type", tc.contentType)
					}
					w.WriteHeader(tc.status)
					_, _ = w.Write(tc.body)
				}))
				defer ts.Close()
				targetURL = ts.URL
			}

			tts := NewDefaultTTSClient(targetURL, "kokoro", "af_bella", 5)
			var totalPCM int
			var lastChunk TTSAudioChunk
			err := tts.Synthesize(context.Background(), tc.text, func(chunk TTSAudioChunk) error {
				if tc.sinkErr != nil {
					return tc.sinkErr
				}
				lastChunk = chunk
				totalPCM += len(chunk.Data)
				return nil
			})

			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected error %v, got %v", tc.wantErr, err)
			}
			if tc.wantErrStr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErrStr)
				}
				if tc.wantErrStr != "error" && !strings.Contains(err.Error(), tc.wantErrStr) {
					t.Fatalf("expected error containing %q, got: %v", tc.wantErrStr, err)
				}
				return
			}
			if tc.wantErr == nil && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantFormat != "" && lastChunk.Format != tc.wantFormat {
				t.Errorf("expected format %q, got %q", tc.wantFormat, lastChunk.Format)
			}
			if tc.wantRate > 0 && lastChunk.SampleRate != tc.wantRate {
				t.Errorf("expected sample rate %d, got %d", tc.wantRate, lastChunk.SampleRate)
			}
			if tc.wantChannels > 0 && lastChunk.Channels != tc.wantChannels {
				t.Errorf("expected channels %d, got %d", tc.wantChannels, lastChunk.Channels)
			}
			if tc.wantTotalBytes > 0 && totalPCM != tc.wantTotalBytes {
				t.Errorf("expected %d total PCM bytes, got %d", tc.wantTotalBytes, totalPCM)
			}
		})
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

type metricExpectation struct {
	name   string
	labels []string
	minVal float64
}

func mkExp(name string, minVal float64, kv ...string) metricExpectation {
	return metricExpectation{name: name, minVal: minVal, labels: kv}
}

func TestHub_MetricsRecording(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		stt     STTClient
		brain   BrainClient
		tts     TTSClient
		timings EdgeTimings
		wantErr error
		metrics []metricExpectation
	}{
		{
			name:    "success",
			stt:     &mockSTT{delay: 2 * time.Millisecond, text: "lights on"},
			brain:   mkBrain("turning on lights", nil),
			tts:     &mockTTS{delay: 2 * time.Millisecond, audio: []byte("tts-audio-bytes"), format: "pcm"},
			timings: EdgeTimings{WakeEvalSec: 0.05, UtteranceSpeechSec: 1.0, UtteranceSilenceSec: 0.25},
			metrics: []metricExpectation{
				mkExp("mirrormere_voice_stage_duration_seconds", 1, "stage", "wake_eval", "status", "success"),
				mkExp("mirrormere_voice_stage_duration_seconds", 1, "stage", "utterance_speech", "status", "success"),
				mkExp("mirrormere_voice_stage_duration_seconds", 1, "stage", "utterance_silence", "status", "success"),
				mkExp("mirrormere_voice_stage_duration_seconds", 1, "stage", "stt", "status", "success"),
				mkExp("mirrormere_voice_stage_duration_seconds", 1, "stage", "brain", "status", "success"),
				mkExp("mirrormere_voice_stage_duration_seconds", 1, "stage", "tts", "status", "success"),
				mkExp("mirrormere_voice_turns_total", 1, "status", "success"),
				mkExp("mirrormere_voice_stt_rtf", 0, "engine", "whisper"),
				mkExp("mirrormere_voice_tts_chars_per_second", 0.001, "voice", "kokoro"),
			},
		},
		{
			name:    "stt_failure",
			stt:     &mockSTT{err: errors.New("whisper down")},
			wantErr: ErrSTTFailed,
			metrics: []metricExpectation{
				mkExp("mirrormere_voice_stage_duration_seconds", 1, "stage", "stt", "status", "error"),
				mkExp("mirrormere_voice_errors_total", 1, "stage", "stt", "error_type", "stt_error"),
				mkExp("mirrormere_voice_turns_total", 1, "status", "error"),
			},
		},
		{
			name:    "brain_failure",
			stt:     &mockSTT{text: "hello"},
			brain:   mkBrain("", errors.New("brain failed")),
			wantErr: ErrBrainFailed,
			metrics: []metricExpectation{
				mkExp("mirrormere_voice_stage_duration_seconds", 1, "stage", "brain", "status", "error"),
				mkExp("mirrormere_voice_errors_total", 1, "stage", "brain", "error_type", "brain_error"),
				mkExp("mirrormere_voice_turns_total", 1, "status", "error"),
			},
		},
		{
			name:    "tts_failure",
			stt:     &mockSTT{text: "hello"},
			brain:   mkBrain("hi there", nil),
			tts:     mkTTS(nil, "", errors.New("tts failed")),
			metrics: []metricExpectation{
				mkExp("mirrormere_voice_stage_duration_seconds", 1, "stage", "tts", "status", "error"),
				mkExp("mirrormere_voice_errors_total", 1, "stage", "tts", "error_type", "tts_error"),
				mkExp("mirrormere_voice_turns_total", 1, "status", "error"),
			},
		},
		{
			name: "streaming_brain",
			stt:  &mockSTT{delay: 2 * time.Millisecond, text: "what is the weather"},
			brain: &delayStreamingBrain{
				mockStreamingBrain: &mockStreamingBrain{
					reply:     "It's sunny today. Enjoy the warmth.",
					sentences: []BrainAudioChunk{{Text: "It's sunny today."}, {Text: "Enjoy the warmth."}},
				},
				delay: 2 * time.Millisecond,
			},
			tts: &mockTTS{delay: 2 * time.Millisecond, audio: makeValidWAV(1600), format: "pcm"},
			metrics: []metricExpectation{
				mkExp("mirrormere_voice_stage_duration_seconds", 1, "stage", "tts", "status", "success"),
				mkExp("mirrormere_voice_tts_chars_per_second", 0.001, "voice", "kokoro"),
				mkExp("mirrormere_voice_turns_total", 1, "status", "success"),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reg := prometheus.NewRegistry()
			m := NewMetrics(reg)
			cfg := &config.VoiceHubConfig{Enabled: true, TTSModel: "kokoro"}
			h := NewHub(cfg, nil,
				WithSTTClient(tc.stt),
				WithBrainClient(tc.brain),
				WithTTSClient(tc.tts),
				WithMetrics(m),
			)
			wav := makeValidWAV(16000)
			err := h.Interact(context.Background(), bytes.NewReader(wav), "kiosk-kitchen", "sess-1", func(string, any) error { return nil }, tc.timings)
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected error %v, got: %v", tc.wantErr, err)
			}
			if tc.wantErr == nil && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for _, exp := range tc.metrics {
				val, ok := metricVal(t, reg, exp.name, lbl(exp.labels...))
				if !ok {
					t.Errorf("metric %s with labels %v not found", exp.name, exp.labels)
					continue
				}
				if val < exp.minVal {
					t.Errorf("metric %s with labels %v: got %f, want >= %f", exp.name, exp.labels, val, exp.minVal)
				}
			}
		})
	}
}
