package voice

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azylman/mirrormere/internal/config"
)

// funcTTS lets a test decide synthesis per text.
type funcTTS func(text string) ([]byte, string, error)

func (f funcTTS) Synthesize(_ context.Context, text string) ([]byte, string, error) {
	return f(text)
}

func runStreamingTurn(t *testing.T, brain BrainClient, tts TTSClient, sink func(string, any) error) error {
	t.Helper()
	cfg := &config.VoiceHubConfig{Enabled: true}
	opts := []HubOption{WithSTTClient(&mockSTT{text: "hello"}), WithBrainClient(brain)}
	if tts != nil {
		opts = append(opts, WithTTSClient(tts))
	}
	h := NewHub(cfg, NewCoordinator(nil), opts...)
	if tts == nil {
		h.tts = nil
	}
	return h.Interact(context.Background(), bytes.NewReader(makeValidWAV(1600)), "kiosk", "s1", sink, EdgeTimings{})
}

func eventNames(evs []recordedEvent) []string {
	out := make([]string, 0, len(evs))
	for _, e := range evs {
		out = append(out, e.Event)
	}
	return out
}

func hasErrorCode(evs []recordedEvent, code string) bool {
	for _, e := range evs {
		if e.Event != "error" {
			continue
		}
		if m, ok := e.Data.(map[string]string); ok && m["error"] == code {
			return true
		}
	}
	return false
}

func TestHub_Streaming_BrainError(t *testing.T) {
	t.Parallel()
	brain := &mockStreamingBrain{err: errors.New("boom")}
	sink, get := collectEvents()
	err := runStreamingTurn(t, brain, &mockTTS{audio: []byte("a"), format: "wav"}, sink)
	if !errors.Is(err, ErrBrainFailed) {
		t.Fatalf("expected ErrBrainFailed, got %v", err)
	}
	if !hasErrorCode(get(), "brain_error") {
		t.Errorf("expected brain_error event, got %v", eventNames(get()))
	}
}

func TestHub_Streaming_TextOnlyWithoutLocalTTS(t *testing.T) {
	t.Parallel()
	brain := &mockStreamingBrain{
		reply:     "Hi there.",
		sentences: []BrainAudioChunk{{Text: "Hi there."}},
	}
	sink, get := collectEvents()
	if err := runStreamingTurn(t, brain, nil, sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	evs := get()
	if n := len(extractAudioChunkEvents(t, evs)); n != 0 {
		t.Errorf("expected no audio with no local TTS, got %d chunks", n)
	}
	names := strings.Join(eventNames(evs), ",")
	if !strings.Contains(names, "reply") {
		t.Errorf("reply must still be emitted, got %s", names)
	}
}

func TestHub_Streaming_AllSentencesFail_FullReplyTTSSucceeds(t *testing.T) {
	t.Parallel()
	brain := &mockStreamingBrain{
		reply:     "One. Two.",
		sentences: []BrainAudioChunk{{Text: "One."}, {Text: "Two."}},
	}
	tts := funcTTS(func(text string) ([]byte, string, error) {
		if text == "One. Two." {
			return []byte("full"), "wav", nil
		}
		return nil, "", errors.New("sentence synth failed")
	})
	sink, get := collectEvents()
	if err := runStreamingTurn(t, brain, tts, sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	chunks := extractAudioChunkEvents(t, get())
	if len(chunks) != 1 {
		t.Fatalf("expected one full-reply chunk, got %d", len(chunks))
	}
	if final, ok := chunks[0]["is_final"].(bool); !ok || !final {
		t.Errorf("full-reply fallback chunk must be is_final:true")
	}
}

func TestHub_Streaming_AllSentencesFail_FullReplyTTSFails(t *testing.T) {
	t.Parallel()
	brain := &mockStreamingBrain{
		reply:     "One.",
		sentences: []BrainAudioChunk{{Text: "One."}},
	}
	tts := &mockTTS{err: errors.New("tts down")}
	sink, get := collectEvents()
	if err := runStreamingTurn(t, brain, tts, sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasErrorCode(get(), "tts_error") {
		t.Errorf("expected tts_error event, got %v", eventNames(get()))
	}
}

func TestHub_Streaming_NoSentences_TTSFails(t *testing.T) {
	t.Parallel()
	brain := &mockStreamingBrain{reply: "Plain."}
	sink, get := collectEvents()
	if err := runStreamingTurn(t, brain, &mockTTS{err: errors.New("tts down")}, sink); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasErrorCode(get(), "tts_error") {
		t.Errorf("expected tts_error event, got %v", eventNames(get()))
	}
}

func TestHub_Streaming_AudioChunkSinkErrorDoesNotAbortTurn(t *testing.T) {
	t.Parallel()
	brain := &mockStreamingBrain{
		reply:     "One.",
		sentences: []BrainAudioChunk{{Text: "One.", Format: "wav", Data: []byte("a")}},
	}
	var got []string
	sink := func(event string, _ any) error {
		got = append(got, event)
		if event == "audio_chunk" {
			return errors.New("dock went away")
		}
		return nil
	}
	_ = runStreamingTurn(t, brain, &mockTTS{audio: []byte("x"), format: "wav"}, sink)
	if !strings.Contains(strings.Join(got, ","), "audio_chunk") {
		t.Errorf("expected an audio_chunk attempt, got %v", got)
	}
}

func sseServer(t *testing.T, contentType, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write([]byte(body))
	}))
}

func TestDefaultBrainClient_AskStreaming_EdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("empty url echoes", func(t *testing.T) {
		t.Parallel()
		b := NewDefaultBrainClient("", 5).(*DefaultBrainClient)
		reply, err := b.AskStreaming(context.Background(), AskRequest{Prompt: "hi"}, nil, nil)
		if err != nil || reply != "I heard: hi" {
			t.Fatalf("got %q, %v", reply, err)
		}
	})

	t.Run("json response", func(t *testing.T) {
		t.Parallel()
		srv := sseServer(t, "application/json", `{"reply":"json answer"}`)
		defer srv.Close()
		b := NewDefaultBrainClient(srv.URL, 5).(*DefaultBrainClient)
		reply, err := b.AskStreaming(context.Background(), AskRequest{Prompt: "hi"}, nil, nil)
		if err != nil || reply != "json answer" {
			t.Fatalf("got %q, %v", reply, err)
		}
	})

	t.Run("http error", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "nope", http.StatusBadGateway)
		}))
		defer srv.Close()
		b := NewDefaultBrainClient(srv.URL, 5).(*DefaultBrainClient)
		if _, err := b.AskStreaming(context.Background(), AskRequest{Prompt: "hi"}, nil, nil); err == nil {
			t.Fatal("expected error on 502")
		}
	})

	t.Run("malformed data, nil onAudio, bad base64, no done", func(t *testing.T) {
		t.Parallel()
		body := "event: status\ndata: not-json\n\n" +
			"event: sentence\ndata: {\"text\":\"A.\",\"audio_b64\":\"!!!\"}\n\n" +
			"event: reply\ndata: {\"reply\":\"  A.  \"}\n\n"
		srv := sseServer(t, "text/event-stream", body)
		defer srv.Close()
		b := NewDefaultBrainClient(srv.URL, 5).(*DefaultBrainClient)

		// nil onAudio: sentence events are ignored.
		reply, err := b.AskStreaming(context.Background(), AskRequest{Prompt: "hi"}, nil, nil)
		if err != nil || reply != "A." {
			t.Fatalf("nil onAudio: got %q, %v", reply, err)
		}

		// bad base64: onAudio still fires, with empty Data.
		var chunks []BrainAudioChunk
		reply, err = b.AskStreaming(context.Background(), AskRequest{Prompt: "hi"}, nil,
			func(c BrainAudioChunk) { chunks = append(chunks, c) })
		if err != nil || reply != "A." {
			t.Fatalf("got %q, %v", reply, err)
		}
		if len(chunks) != 1 || chunks[0].Text != "A." || len(chunks[0].Data) != 0 {
			t.Fatalf("expected one text-only chunk, got %+v", chunks)
		}
	})
}
