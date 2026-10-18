package voice

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
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

type funcTTS func(text string) ([]byte, string, error)

func (f funcTTS) Synthesize(_ context.Context, text string, onChunk func(chunk TTSAudioChunk) error) error {
	data, format, err := f(text)
	if err != nil {
		return err
	}
	if len(data) > 0 && onChunk != nil {
		if format == "" {
			format = "pcm"
		}
		return onChunk(TTSAudioChunk{Data: data, Format: format, SampleRate: 24000, Channels: 1})
	}
	return nil
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

type recordingStateSink struct {
	mu     sync.Mutex
	states []events.VoiceStateData
}

func (r *recordingStateSink) SetVoiceState(s *events.VoiceStateData) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.states = append(r.states, *s)
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

func mkStreamBrain(reply string, err error, sentences ...string) *mockStreamingBrain {
	var chunks []BrainAudioChunk
	for _, s := range sentences {
		chunks = append(chunks, BrainAudioChunk{Text: s})
	}
	return &mockStreamingBrain{reply: reply, err: err, sentences: chunks}
}

func mkBrain(reply string, err error) *mockBrain {
	return &mockBrain{reply: reply, err: err}
}

func mkTTS(audio []byte, format string, err error) *mockTTS {
	return &mockTTS{audio: audio, format: format, err: err}
}

func mkStreamTTS(fn func(ctx context.Context, text string, onChunk func(TTSAudioChunk) error) error) *mockStreamingTTS {
	return &mockStreamingTTS{streamingFn: fn}
}

func TestDefaultBrainClient_AskStreaming(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		contentType  string
		body         string
		statusCode   int
		emptyURL     bool
		collectAudio bool
		wantReply    string
		wantErr      bool
		wantStatuses []string
		wantChunks   []string
	}{
		{
			name:         "wire_format",
			contentType:  "text/event-stream",
			body:         "event: status\ndata: {\"status\":\"thinking\"}\n\nevent: sentence\ndata: {\"text\":\"It's sunny.\"}\n\nevent: sentence\ndata: {\"text\":\"Bring sunglasses.\"}\n\nevent: reply\ndata: {\"reply\":\"It's sunny. Bring sunglasses.\"}\n\nevent: done\ndata: {}\n\n",
			collectAudio: true,
			wantReply:    "It's sunny. Bring sunglasses.",
			wantStatuses: []string{"thinking"},
			wantChunks:   []string{"It's sunny.", "Bring sunglasses."},
		},
		{
			name:         "whitespace_sentence_ignored",
			contentType:  "text/event-stream",
			body:         "event: sentence\ndata: {\"text\":\"   \"}\n\nevent: sentence\ndata: {\"text\":\"Valid sentence.\"}\n\nevent: reply\ndata: {\"reply\":\"Valid sentence.\"}\n\nevent: done\ndata: {}\n\n",
			collectAudio: true,
			wantReply:    "Valid sentence.",
			wantChunks:   []string{"Valid sentence."},
		},
		{
			name:         "plain_ask_stream",
			contentType:  "text/event-stream",
			body:         "event: reply\ndata: {\"reply\":\"plain answer\"}\n\nevent: done\ndata: {}\n\n",
			collectAudio: true,
			wantReply:    "plain answer",
		},
		{
			name:      "empty_url",
			emptyURL:  true,
			wantReply: "I heard: hi",
		},
		{
			name:        "json_response",
			contentType: "application/json",
			body:        `{"reply":"json answer"}`,
			wantReply:   "json answer",
		},
		{
			name:       "http_error_502",
			statusCode: http.StatusBadGateway,
			body:       "nope",
			wantErr:    true,
		},
		{
			name:         "malformed_data_nil_audio",
			contentType:  "text/event-stream",
			body:         "event: status\ndata: not-json\n\nevent: sentence\ndata: {\"text\":\"A.\"}\n\nevent: reply\ndata: {\"reply\":\"  A.  \"}\n\n",
			collectAudio: false,
			wantReply:    "A.",
		},
		{
			name:         "malformed_data_with_audio",
			contentType:  "text/event-stream",
			body:         "event: status\ndata: not-json\n\nevent: sentence\ndata: {\"text\":\"A.\"}\n\nevent: reply\ndata: {\"reply\":\"  A.  \"}\n\n",
			collectAudio: true,
			wantReply:    "A.",
			wantChunks:   []string{"A."},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			url := ""
			if !tc.emptyURL {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if tc.statusCode != 0 {
						w.WriteHeader(tc.statusCode)
					} else if tc.contentType != "" {
						w.Header().Set("Content-Type", tc.contentType)
					}
					_, _ = w.Write([]byte(tc.body))
				}))
				defer srv.Close()
				url = srv.URL
			}
			b := NewDefaultBrainClient(url, 5).(*DefaultBrainClient)
			var statuses []string
			onStatus := func(s string) { statuses = append(statuses, s) }
			var chunks []BrainAudioChunk
			var onAudio func(BrainAudioChunk)
			if tc.collectAudio {
				onAudio = func(c BrainAudioChunk) { chunks = append(chunks, c) }
			}
			prompt := "hi"
			if tc.name == "wire_format" {
				prompt = "what's the weather"
			}
			reply, err := b.AskStreaming(context.Background(), AskRequest{Prompt: prompt, SessionID: "s1"}, onStatus, onAudio)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if reply != tc.wantReply {
				t.Errorf("reply: want %q, got %q", tc.wantReply, reply)
			}
			if len(tc.wantStatuses) > 0 && !reflect.DeepEqual(statuses, tc.wantStatuses) {
				t.Errorf("statuses: want %v, got %v", tc.wantStatuses, statuses)
			}
			if len(tc.wantChunks) != len(chunks) {
				t.Fatalf("chunks count: want %d, got %d (%+v)", len(tc.wantChunks), len(chunks), chunks)
			}
			for i, want := range tc.wantChunks {
				if chunks[i].Text != want {
					t.Errorf("chunk %d: want %q, got %q", i, want, chunks[i].Text)
				}
			}
		})
	}
}

func TestHub_Interact_StreamingScenarios(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                 string
		brain                BrainClient
		tts                  TTSClient
		noTTSClient          bool
		ignoreSinkAudioChunk bool
		wantErr              error
		wantErrorCode        string
		wantChunks           int
		wantNoChunks         bool
		wantFinal            bool
		wantReplyEvent       string
		wantRemainderBytes   int
	}{
		{
			name:       "sentence_synthesized_via_tts",
			brain:      mkStreamBrain("It's sunny. Bring sunglasses.", nil, "It's sunny.", "Bring sunglasses."),
			tts:        mkTTS([]byte("synthesized-pcm"), "pcm", nil),
			wantChunks: 2,
			wantFinal:  true,
		},
		{
			name:  "pcm_buffering_and_remainder_flush",
			brain: mkStreamBrain("Streaming PCM.", nil, "One.", "Two.", "Three."),
			tts: mkStreamTTS(func(_ context.Context, text string, onChunk func(TTSAudioChunk) error) error {
				size := 3000
				if text == "Three." {
					size = 2000
				}
				return onChunk(TTSAudioChunk{Data: make([]byte, size), Format: "pcm", SampleRate: 24000, Channels: 1})
			}),
			wantChunks:         3,
			wantFinal:          true,
			wantRemainderBytes: 3200,
		},
		{
			name:       "streaming_brain_remote_tts_streaming_client",
			brain:      mkStreamBrain("Sentence one. Sentence two.", nil, "Sentence one.", "Sentence two."),
			tts:        &mockStreamingTTS{},
			wantChunks: 3,
			wantFinal:  true,
		},
		{
			name:       "non_streaming_brain_streaming_tts",
			brain:      mkBrain("Full non-streaming reply.", nil),
			tts:        &mockStreamingTTS{},
			wantChunks: 2,
			wantFinal:  true,
		},
		{
			name:  "non_streaming_pcm_remainder",
			brain: mkBrain("Short reply.", nil),
			tts: mkStreamTTS(func(_ context.Context, _ string, onChunk func(TTSAudioChunk) error) error {
				return onChunk(TTSAudioChunk{Data: make([]byte, 2000), Format: "pcm", SampleRate: 24000, Channels: 1})
			}),
			wantChunks:         2,
			wantFinal:          true,
			wantRemainderBytes: 2000,
		},
		{
			name:         "empty_whitespace_reply",
			brain:        mkBrain("   ", nil),
			tts:          &mockStreamingTTS{},
			wantNoChunks: true,
		},
		{
			name:           "text_only_without_local_tts",
			brain:          mkStreamBrain("Hi there.", nil, "Hi there."),
			noTTSClient:    true,
			wantNoChunks:   true,
			wantReplyEvent: "Hi there.",
		},
		{
			name:  "sentence_fallback_to_full_reply_tts_success",
			brain: mkStreamBrain("One. Two.", nil, "One.", "Two."),
			tts: funcTTS(func(text string) ([]byte, string, error) {
				if text == "One. Two." {
					return []byte("full"), "pcm", nil
				}
				return nil, "", errors.New("sentence synth failed")
			}),
			wantChunks: 2,
			wantFinal:  true,
		},
		{
			name:          "sentence_fallback_to_full_reply_tts_failure",
			brain:         mkStreamBrain("One.", nil, "One."),
			tts:           mkTTS(nil, "", errors.New("tts down")),
			wantErrorCode: "tts_error",
		},
		{
			name:          "no_sentence_tts_fails",
			brain:         mkStreamBrain("Plain.", nil),
			tts:           mkTTS(nil, "", errors.New("tts down")),
			wantErrorCode: "tts_error",
		},
		{
			name:          "brain_error",
			brain:         &mockStreamingBrain{err: errors.New("boom")},
			tts:           mkTTS([]byte("a"), "pcm", nil),
			wantErr:       ErrBrainFailed,
			wantErrorCode: "brain_error",
		},
		{
			name:  "streaming_mp3_fails_fast",
			brain: mkStreamBrain("MP3 sentence.", nil, "MP3 sentence."),
			tts: mkStreamTTS(func(_ context.Context, _ string, onChunk func(TTSAudioChunk) error) error {
				return onChunk(TTSAudioChunk{Data: []byte("mp3-raw-frame"), Format: "mp3", SampleRate: 44100, Channels: 2})
			}),
			wantErr:      ErrUnsupportedAudioFormat,
			wantNoChunks: true,
		},
		{
			name:  "non_streaming_mp3_fails_fast",
			brain: mkBrain("Streaming MP3 reply.", nil),
			tts: mkStreamTTS(func(_ context.Context, _ string, onChunk func(TTSAudioChunk) error) error {
				return onChunk(TTSAudioChunk{Data: []byte("mp3-data-frame"), Format: "mp3", SampleRate: 44100, Channels: 2})
			}),
			wantErr:       ErrUnsupportedAudioFormat,
			wantNoChunks:  true,
			wantErrorCode: "tts_error",
		},
		{
			name:  "streaming_rate_change_fails_fast",
			brain: mkStreamBrain("Sentence one. Sentence two.", nil, "Sentence one.", "Sentence two."),
			tts: mkStreamTTS(func(_ context.Context, text string, onChunk func(TTSAudioChunk) error) error {
				rate := 24000
				if text == "Sentence two." {
					rate = 22050
				}
				return onChunk(TTSAudioChunk{Data: make([]byte, 1000), Format: "pcm", SampleRate: rate, Channels: 1})
			}),
			wantErr: ErrMixedPCMFormat,
		},
		{
			name:  "streaming_tts_fallback_on_error",
			brain: mkBrain("Fallback test.", nil),
			tts: mkStreamTTS(func(_ context.Context, _ string, _ func(TTSAudioChunk) error) error {
				return errors.New("streaming connection dropped")
			}),
			wantErrorCode: "tts_error",
		},
		{
			name:                 "audio_chunk_sink_error_tolerance",
			brain:                mkStreamBrain("One.", nil, "One."),
			tts:                  mkTTS([]byte("x"), "pcm", nil),
			ignoreSinkAudioChunk: true,
		},
		{
			name:       "streaming_brain_remote_tts_plain_client",
			brain:      mkStreamBrain("First sentence. Second sentence.", nil, "First sentence.", "Second sentence."),
			tts:        mkTTS(makeValidWAV(1600), "pcm", nil),
			wantChunks: 3,
			wantFinal:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := &config.VoiceHubConfig{Enabled: true}
			opts := []HubOption{WithSTTClient(&mockSTT{text: "hello"}), WithBrainClient(tc.brain)}
			if tc.tts != nil {
				opts = append(opts, WithTTSClient(tc.tts))
			}
			h := NewHub(cfg, NewCoordinator(nil), opts...)
			if tc.noTTSClient {
				h.tts = nil
			}
			sink, getEvents := collectEvents()
			if tc.ignoreSinkAudioChunk {
				origSink := sink
				sink = func(event string, data any) error {
					_ = origSink(event, data)
					if event == "audio_chunk" {
						return errors.New("dock went away")
					}
					return nil
				}
			}
			err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(1600)), "kiosk", "s1", sink, EdgeTimings{})
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("want error %v, got %v", tc.wantErr, err)
				}
			} else if err != nil && tc.wantErrorCode == "" {
				t.Fatalf("unexpected error: %v", err)
			}
			evs := getEvents()
			if tc.wantErrorCode != "" {
				if !hasErrorCode(evs, tc.wantErrorCode) {
					t.Errorf("expected error code %q in events: %v", tc.wantErrorCode, eventNames(evs))
				}
			}
			if tc.wantReplyEvent != "" {
				names := strings.Join(eventNames(evs), ",")
				if !strings.Contains(names, "reply") {
					t.Errorf("expected reply in events: %s", names)
				}
			}
			chunks := extractAudioChunkEvents(t, evs)
			if tc.wantNoChunks && len(chunks) != 0 {
				t.Fatalf("expected 0 audio chunks, got %d", len(chunks))
			}
			if tc.wantChunks > 0 && len(chunks) != tc.wantChunks {
				t.Fatalf("expected %d audio chunks, got %d", tc.wantChunks, len(chunks))
			}
			if tc.wantFinal && len(chunks) > 0 {
				last := chunks[len(chunks)-1]
				if final, ok := last["is_final"].(bool); !ok || !final {
					t.Errorf("expected last chunk is_final: true, got %v", last["is_final"])
				}
			}
			if tc.wantRemainderBytes > 0 && len(chunks) >= 2 {
				rem := chunks[len(chunks)-2]
				dec, _ := base64.StdEncoding.DecodeString(rem["data"].(string))
				if len(dec) != tc.wantRemainderBytes {
					t.Errorf("remainder bytes: want %d, got %d", tc.wantRemainderBytes, len(dec))
				}
			}
		})
	}
}

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

func TestHub_ChatLog(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		sttText         string
		brain           BrainClient
		tts             TTSClient
		speaker         SpeakerIdentifier
		withChatLog     bool
		wantErr         bool
		wantHumanText   string
		wantHumanAuthor string
		wantAgentText   string
		wantChanges     int32
		wantMsgs        int
	}{
		{
			name:            "records_turn_in_chatlog",
			sttText:         "Turn off office lights",
			brain:           &mockBrain{reply: "Turned off office lights."},
			tts:             &mockTTS{audio: []byte("pcm"), format: "pcm"},
			speaker:         fixedSpeaker{name: "sam"},
			withChatLog:     true,
			wantHumanText:   "Turn off office lights",
			wantHumanAuthor: "sam",
			wantAgentText:   "Turned off office lights.",
			wantChanges:     2,
			wantMsgs:        2,
		},
		{
			name:            "brain_failure_keeps_user_turn",
			sttText:         "hello",
			brain:           &mockBrain{err: ErrBrainFailed},
			withChatLog:     true,
			wantErr:         true,
			wantHumanText:   "hello",
			wantHumanAuthor: "",
			wantMsgs:        1,
		},
		{
			name:        "without_chatlog_still_works",
			sttText:     "hi",
			brain:       &mockBrain{reply: "yo"},
			withChatLog: false,
			wantMsgs:    0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			hubEvents := events.NewHub(events.HubConfig{}, nil, nil)
			defer hubEvents.Close()
			store := chatlog.NewStore()
			var changes atomic.Int32

			opts := []HubOption{
				WithSTTClient(&mockSTT{text: tc.sttText}),
				WithBrainClient(tc.brain),
			}
			if tc.tts != nil {
				opts = append(opts, WithTTSClient(tc.tts))
			}
			if tc.speaker != nil {
				opts = append(opts, WithSpeakerIdentifier(tc.speaker))
			}
			if tc.withChatLog {
				opts = append(opts, WithChatLog(store, func() { changes.Add(1) }))
			}
			h := NewHub(&config.VoiceHubConfig{Enabled: true}, NewCoordinator(hubEvents), opts...)

			err := chatTurn(t, h, "touch-kiosk-kitchen")
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
			} else if err != nil {
				t.Fatalf("interact: %v", err)
			}

			if !tc.withChatLog {
				return
			}

			msgs := store.Messages(func(k string) bool { return k == chatlog.NodeKey("touch-kiosk-kitchen") })
			if len(msgs) != tc.wantMsgs {
				t.Fatalf("messages count: want %d, got %d (%+v)", tc.wantMsgs, len(msgs), msgs)
			}
			if tc.wantMsgs >= 1 {
				if msgs[0].Role != chatlog.RoleHuman || msgs[0].Text != tc.wantHumanText || msgs[0].Author != tc.wantHumanAuthor {
					t.Errorf("user message mismatch: %+v", msgs[0])
				}
				if msgs[0].At.IsZero() {
					t.Errorf("user message missing timestamp")
				}
			}
			if tc.wantMsgs >= 2 {
				if msgs[1].Role != chatlog.RoleAgent || msgs[1].Text != tc.wantAgentText {
					t.Errorf("agent message mismatch: %+v", msgs[1])
				}
				if msgs[1].At.Before(msgs[0].At) {
					t.Errorf("timestamps out of order: %v %v", msgs[0].At, msgs[1].At)
				}
			}
			if tc.wantChanges > 0 && changes.Load() != tc.wantChanges {
				t.Errorf("changes count: want %d, got %d", tc.wantChanges, changes.Load())
			}
			other := store.Messages(func(k string) bool { return k == chatlog.NodeKey("eink-display-livingroom") })
			if len(other) != 0 {
				t.Errorf("other node must not see this conversation: %+v", other)
			}
		})
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
	pcm := bytes.Repeat([]byte{0x11, 0x22}, 5000)

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
				binary.LittleEndian.PutUint16(b[16:], 22)
				binary.LittleEndian.PutUint16(b[24:], 1)
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
		for i := 0; i < len(wav); i += 1000 {
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
		retained = append(retained, c.Data)
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
