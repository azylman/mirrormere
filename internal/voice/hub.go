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
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"strings"
	"sync"
	"time"

	"github.com/azylman/mirrormere/internal/config"
)

var (
	// ErrHubDisabled is returned when the voice hub is disabled in configuration.
	ErrHubDisabled = errors.New("voice hub is disabled")
	// ErrInteractionBusy is returned when a concurrent voice interaction is already active.
	ErrInteractionBusy = errors.New("concurrent voice interaction in flight")
	// ErrInvalidAudio is returned when the audio payload is malformed or not a valid WAV file.
	ErrInvalidAudio = errors.New("invalid audio payload: must be non-empty WAV audio")
	// ErrSTTFailed is returned when speech-to-text transcription fails.
	ErrSTTFailed = errors.New("speech-to-text transcription failed")
	// ErrBrainFailed is returned when agent brain deliberation fails.
	ErrBrainFailed = errors.New("agent brain deliberation failed")
	// ErrTTSFailed is returned when text-to-speech synthesis fails.
	ErrTTSFailed = errors.New("text-to-speech synthesis failed")
	// ErrStreamingNotConfigured is returned by DefaultBrainClient.AskStream
	// when no brain_stream_url was configured. It is not a real failure —
	// Hub treats it as "streaming isn't available for this turn" and falls
	// back to the non-streaming Ask() path without logging a warning.
	ErrStreamingNotConfigured = errors.New("streaming brain client not configured")
)

// speakerGrace is how long the hub waits for speaker matching after STT has
// already finished. Matching normally finishes first; this bounds the worst case.
var speakerGrace = 500 * time.Millisecond

// SSEEventSink is a callback to emit an SSE event to the client stream.
type SSEEventSink func(event string, data any) error

// STTClient abstracts speech-to-text inference.
type STTClient interface {
	Transcribe(ctx context.Context, wavData []byte) (string, error)
}

// AskRequest is one turn sent to the agent brain.
type AskRequest struct {
	Prompt    string
	SessionID string
	// NodeID is the edge device that heard the utterance.
	NodeID string
	// Speaker is the matched enrolled speaker, or "" (SPEC-011 §Speaker Identification).
	// It is a personalisation hint, not authentication.
	Speaker string
	// SpeakerScore is the best cosine similarity seen, 0 when matching did not run.
	SpeakerScore float64
}

// BrainClient abstracts agent deliberation and live tool status streaming.
type BrainClient interface {
	Ask(ctx context.Context, req AskRequest, onStatus func(status string)) (string, error)
}

// StreamingBrainClient is an optional capability a BrainClient may also
// implement: MANDOS sentence-by-sentence streaming
// (specs/2026-09-28-mandos-streaming.md's /ask/stream). onSentence is
// called once per completed sentence, strictly in reply order, with that
// sentence's synthesized audio (wav is nil/empty when the upstream
// sentence carried no audio — e.g. a per-sentence TTS failure — and the
// caller is expected to synthesize a fallback). onStatus mirrors Ask's
// status callback. AskStream returns the full reply text once the
// upstream stream completes, exactly like Ask.
//
// A brain client that implements this is still free to report
// ErrStreamingNotConfigured (or any other error) if streaming isn't
// available for a given call; Hub falls back to Ask() when that happens
// before any sentence was emitted.
type StreamingBrainClient interface {
	AskStream(ctx context.Context, req AskRequest, onStatus func(status string), onSentence func(text, engine string, wav []byte) error) (string, error)
}

// brainDeliberationError marks an error as a genuine brain-call failure
// (as opposed to a downstream SSE sink write failure), so Hub.Interact
// knows to apply the same StateError/brain_error/ErrBrainFailed handling
// that a non-streaming Ask() failure gets, and only that handling.
type brainDeliberationError struct{ err error }

func (e *brainDeliberationError) Error() string { return e.err.Error() }
func (e *brainDeliberationError) Unwrap() error { return e.err }

// TTSClient abstracts speech synthesis.
type TTSClient interface {
	Synthesize(ctx context.Context, text string) ([]byte, string, error) // audioBytes, format ("mp3"), error
}

// Hub coordinates the end-to-end voice pipeline (STT -> Brain -> TTS).
type Hub struct {
	mu       sync.Mutex
	inFlight bool
	cfg      *config.VoiceHubConfig
	coord    *Coordinator
	stt      STTClient
	brain    BrainClient
	tts      TTSClient
	speaker  SpeakerIdentifier
}

// HubOption configures optional Hub overrides (e.g. for testing).
type HubOption func(*Hub)

// WithSTTClient overrides the STT client implementation.
func WithSTTClient(stt STTClient) HubOption {
	return func(h *Hub) { h.stt = stt }
}

// WithBrainClient overrides the Brain client implementation.
func WithBrainClient(brain BrainClient) HubOption {
	return func(h *Hub) { h.brain = brain }
}

// WithTTSClient overrides the TTS client implementation.
func WithTTSClient(tts TTSClient) HubOption {
	return func(h *Hub) { h.tts = tts }
}

// WithSpeakerIdentifier overrides the speaker identification implementation.
func WithSpeakerIdentifier(id SpeakerIdentifier) HubOption {
	return func(h *Hub) { h.speaker = id }
}

// NewHub constructs a Voice Hub coordinator.
func NewHub(cfg *config.VoiceHubConfig, coord *Coordinator, opts ...HubOption) *Hub {
	h := &Hub{
		cfg:   cfg,
		coord: coord,
	}
	if cfg != nil && cfg.Enabled {
		h.stt = NewDefaultSTTClient(cfg.STTURL)
		h.brain = NewDefaultBrainClientWithStream(cfg.BrainURL, cfg.BrainStreamURL, cfg.GetBrainTimeoutSeconds())
		h.tts = NewDefaultTTSClient(cfg.TTSURL, cfg.GetTTSModel(), cfg.GetTTSVoice(), cfg.GetTTSTimeoutSeconds())
		if sid := NewSpeakerIdentifierFromConfig(cfg.SpeakerID); sid != nil {
			h.speaker = sid
		}
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// IsEnabled reports whether the voice hub is enabled.
func (h *Hub) IsEnabled() bool {
	return h != nil && h.cfg.IsEnabled()
}

// Interact coordinates the complete voice interaction pipeline for an incoming audio recording.
func (h *Hub) Interact(ctx context.Context, audio io.Reader, nodeID, sessionID string, sink SSEEventSink) error {
	if !h.IsEnabled() {
		return ErrHubDisabled
	}

	h.mu.Lock()
	if h.inFlight {
		h.mu.Unlock()
		return ErrInteractionBusy
	}
	h.inFlight = true
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		h.inFlight = false
		h.mu.Unlock()
	}()

	// Mutex to protect concurrent SSE writes from multiple goroutines
	var sinkMu sync.Mutex
	safeSink := func(event string, data any) error {
		sinkMu.Lock()
		defer sinkMu.Unlock()
		return sink(event, data)
	}

	// 1. Read and validate WAV audio payload
	if audio == nil {
		return ErrInvalidAudio
	}
	wavData, err := io.ReadAll(audio)
	if err != nil || len(wavData) < 44 || !bytes.HasPrefix(wavData, []byte("RIFF")) {
		return ErrInvalidAudio
	}

	start := time.Now()

	// Step 1: Notify HUD and client: transcribing
	if h.coord != nil {
		h.coord.Transition(StateTranscribing, nil, nil, nil, nil)
	}
	if err := safeSink("state", map[string]string{"state": StateTranscribing}); err != nil {
		return err
	}

	// Step 2: Speech-to-Text transcription, with speaker matching in parallel
	if h.stt == nil {
		return ErrSTTFailed
	}
	// The channel is buffered so the goroutine never blocks, and idCancel stops
	// a slow embed call once the turn no longer needs it.
	speakerCh := make(chan SpeakerMatch, 1)
	idCtx, idCancel := context.WithCancel(ctx)
	defer idCancel()
	if h.speaker != nil {
		go func() {
			m, idErr := h.speaker.Identify(idCtx, wavData)
			if idErr != nil {
				// Speaker matching is best-effort: the turn proceeds without a speaker.
				slog.Warn("speaker identification failed", "error", idErr)
				m = SpeakerMatch{}
			}
			speakerCh <- m
		}()
	} else {
		speakerCh <- SpeakerMatch{}
	}
	transcript, err := h.stt.Transcribe(ctx, wavData)
	var match SpeakerMatch
	if err == nil {
		// STT is the critical path. Give matching a short grace period after STT
		// finishes, then continue without a speaker rather than stall the turn.
		select {
		case match = <-speakerCh:
		case <-time.After(speakerGrace):
			idCancel()
			slog.Warn("speaker identification exceeded grace period after STT; continuing without speaker", "grace", speakerGrace)
		}
	}
	speaker := match.Speaker
	if err != nil {
		if h.coord != nil {
			h.coord.Transition(StateError, nil, nil, nil, nil)
		}
		if sinkErr := safeSink("error", map[string]string{"error": "stt_error", "message": err.Error()}); sinkErr != nil {
			return sinkErr
		}
		return fmt.Errorf("%w: %w", ErrSTTFailed, err)
	}

	// Step 2.5: Blank transcript guard. STT occasionally returns an empty
	// or whitespace-only transcript (silence, noise, a false wake). Do not
	// call the brain at all in that case — it has nothing to answer and a
	// blank prompt has reached the agent this way before.
	if strings.TrimSpace(transcript) == "" {
		if h.coord != nil {
			h.coord.Reset()
		}
		if sinkErr := safeSink("error", map[string]string{"error": "empty_transcript"}); sinkErr != nil {
			return sinkErr
		}
		durationMs := time.Since(start).Milliseconds()
		if sinkErr := safeSink("done", map[string]int64{"duration_ms": durationMs}); sinkErr != nil {
			return sinkErr
		}
		return nil
	}

	// Step 3: Transition to thinking with recognized transcript
	if h.coord != nil {
		h.coord.Transition(StateThinking, &transcript, nil, nil, nil)
	}
	if err := safeSink("transcript", map[string]string{"transcript": transcript, "speaker": speaker}); err != nil {
		return err
	}

	// Step 4+5: Brain deliberation and speech synthesis. Streams
	// sentence-by-sentence audio via the karakos gateway's /ask/stream
	// (specs/2026-09-28-mandos-streaming.md) when the brain client
	// supports it and it's configured; otherwise, and as a fallback if the
	// stream fails before any sentence was spoken, uses the original
	// Ask()-then-one-shot-TTS path. See doBrainTurn.
	if h.brain == nil {
		return ErrBrainFailed
	}
	engine := h.cfg.GetTTSModel()
	_, err = h.doBrainTurn(ctx, AskRequest{
		Prompt:       transcript,
		SessionID:    sessionID,
		NodeID:       nodeID,
		Speaker:      speaker,
		SpeakerScore: match.Score,
	}, transcript, engine, safeSink)
	if err != nil {
		var bde *brainDeliberationError
		if errors.As(err, &bde) {
			if h.coord != nil {
				h.coord.Transition(StateError, nil, nil, nil, nil)
			}
			if sinkErr := safeSink("error", map[string]string{"error": "brain_error", "message": bde.Unwrap().Error()}); sinkErr != nil {
				return sinkErr
			}
			return fmt.Errorf("%w: %w", ErrBrainFailed, bde.Unwrap())
		}
		// A raw (non-brainDeliberationError) failure is a downstream SSE
		// sink write error (e.g. the client went away mid-turn), not a
		// brain failure — propagate it as-is, matching the non-streaming
		// path's sink-error behavior exactly (no extra wrap, no extra
		// "error" event attempt against an already-broken sink).
		return err
	}

	// Step 6: Complete interaction and return to idle
	if h.coord != nil {
		h.coord.Reset()
	}
	durationMs := time.Since(start).Milliseconds()
	if err := safeSink("done", map[string]int64{"duration_ms": durationMs}); err != nil {
		return err
	}

	return nil
}

// doBrainTurn runs one turn of brain deliberation and speech synthesis and
// returns the full reply text. When h.brain also implements
// StreamingBrainClient, it tries the MANDOS streaming path first
// (speakStreaming): audio for each sentence is emitted as soon as that
// sentence is synthesized, well before the full reply is known. If the
// stream fails before any sentence was spoken (streaming not configured,
// connect error, non-2xx, wrong content type, ...), it falls back to the
// non-streaming Ask()-then-one-shot-TTS path (speakNonStreaming) — from
// the caller's perspective nothing distinguishes the two paths having run.
// If the stream fails after some sentences were already played, it does
// NOT fall back (that would replay part of the reply aloud); it returns a
// *brainDeliberationError instead, same as a non-streaming Ask() failure.
func (h *Hub) doBrainTurn(ctx context.Context, req AskRequest, transcript, engine string, safeSink SSEEventSink) (string, error) {
	if streamBrain, ok := h.brain.(StreamingBrainClient); ok {
		reply, sentenceCount, err := h.speakStreaming(ctx, streamBrain, req, transcript, engine, safeSink)
		switch {
		case err == nil:
			return reply, nil
		case sentenceCount > 0:
			return reply, &brainDeliberationError{err}
		case errors.Is(err, ErrStreamingNotConfigured):
			// Expected and silent: no brain_stream_url configured.
		default:
			slog.Warn("mandos stream failed before any sentence; falling back to non-streaming ask", "error", err)
		}
	}
	return h.speakNonStreaming(ctx, req, transcript, engine, safeSink)
}

// speakStreaming runs one turn via StreamingBrainClient.AskStream, emitting
// an audio_chunk per completed sentence (is_final:false) as it arrives, then
// a trailing reply event and a terminal empty audio_chunk (is_final:true)
// once the full reply text is known. It returns the reply text, the number
// of sentences it successfully submitted for audio (so the caller can tell
// whether any audio was already played before an error), and any error.
func (h *Hub) speakStreaming(ctx context.Context, sb StreamingBrainClient, req AskRequest, transcript, engine string, safeSink SSEEventSink) (string, int, error) {
	sentenceCount := 0
	chunkIndex := 0
	firstSentence := true

	onStatus := func(status string) {
		if h.coord != nil {
			h.coord.SetStatus(&status)
		}
		if sinkErr := safeSink("status", map[string]string{"status": status}); sinkErr != nil {
			slog.Debug("status sink error", "error", sinkErr)
		}
	}

	onSentence := func(text, sentEngine string, wav []byte) error {
		sentenceCount++
		format := "wav" // the gateway's `sentence` event audio is always WAV
		if len(wav) == 0 {
			// The upstream sentence carried no audio (e.g. a per-sentence
			// TTS failure on the gateway). Fall back to synthesizing this
			// one sentence locally rather than dropping it silently.
			if h.tts == nil {
				if sinkErr := safeSink("error", map[string]string{"error": "tts_error", "message": "sentence had no audio and no local TTS is configured"}); sinkErr != nil {
					return sinkErr
				}
				return nil
			}
			fallback, fbFormat, ttsErr := h.tts.Synthesize(ctx, text)
			if ttsErr != nil || len(fallback) == 0 {
				msg := "tts fallback returned no audio"
				if ttsErr != nil {
					msg = ttsErr.Error()
				}
				if sinkErr := safeSink("error", map[string]string{"error": "tts_error", "message": msg}); sinkErr != nil {
					return sinkErr
				}
				return nil
			}
			wav, format = fallback, fbFormat
		}

		if firstSentence {
			firstSentence = false
			if h.coord != nil {
				h.coord.Transition(StateSpeaking, &transcript, nil, &engine, nil)
			}
		}

		idx := chunkIndex
		chunkIndex++
		return safeSink("audio_chunk", map[string]any{
			"chunk_index": idx,
			"format":      format,
			"is_final":    false,
			"data":        base64.StdEncoding.EncodeToString(wav),
		})
	}

	reply, err := sb.AskStream(ctx, req, onStatus, onSentence)
	if err != nil {
		return reply, sentenceCount, err
	}

	// Devil's Advocate catch: Always emit reply event so kiosk caption toast
	// renders reply text, even though (for streaming) it only lands once
	// the audio has already been spoken.
	if sinkErr := safeSink("reply", map[string]string{"reply": reply, "tts_engine": engine}); sinkErr != nil {
		return reply, sentenceCount, sinkErr
	}
	if sinkErr := safeSink("audio_chunk", map[string]any{
		"chunk_index": chunkIndex,
		"format":      "wav",
		"is_final":    true,
		"data":        "",
	}); sinkErr != nil {
		return reply, sentenceCount, sinkErr
	}
	return reply, sentenceCount, nil
}

// speakNonStreaming runs one turn via BrainClient.Ask and a single
// after-the-fact TTS.Synthesize call — the original, pre-MANDOS behavior.
func (h *Hub) speakNonStreaming(ctx context.Context, req AskRequest, transcript, engine string, safeSink SSEEventSink) (string, error) {
	reply, err := h.brain.Ask(ctx, req, func(status string) {
		if h.coord != nil {
			h.coord.SetStatus(&status)
		}
		if sinkErr := safeSink("status", map[string]string{"status": status}); sinkErr != nil {
			// Status stream dropped by client; deliberate gracefully
			slog.Debug("status sink error", "error", sinkErr)
		}
	})
	if err != nil {
		return "", &brainDeliberationError{err}
	}

	// Devil's Advocate catch: Always emit reply event so kiosk caption toast renders reply text,
	// even if TTS synthesis subsequently fails!
	if err := safeSink("reply", map[string]string{"reply": reply, "tts_engine": engine}); err != nil {
		return reply, err
	}

	// Speech synthesis (TTS)
	if h.tts != nil {
		audioBytes, format, ttsErr := h.tts.Synthesize(ctx, reply)
		if ttsErr != nil {
			// Graceful degradation: Log/emit error for TTS but don't drop the interaction reply
			if sinkErr := safeSink("error", map[string]string{"error": "tts_error", "message": ttsErr.Error()}); sinkErr != nil {
				return reply, sinkErr
			}
		} else if len(audioBytes) > 0 {
			if h.coord != nil {
				h.coord.Transition(StateSpeaking, &transcript, &reply, &engine, nil)
			}
			b64 := base64.StdEncoding.EncodeToString(audioBytes)
			if err := safeSink("audio_chunk", map[string]any{
				"chunk_index": 0,
				"format":      format,
				"is_final":    true,
				"data":        b64,
			}); err != nil {
				return reply, err
			}
		}
	}

	return reply, nil
}

// --- Default STT Client ---

// DefaultSTTClient supports both Wyoming TCP protocol and HTTP Whisper endpoints.
type DefaultSTTClient struct {
	rawURL     string
	isWyoming  bool
	httpClient *http.Client
}

// NewDefaultSTTClient constructs an STTClient based on target URL scheme.
func NewDefaultSTTClient(rawURL string) STTClient {
	trimmed := strings.TrimSpace(rawURL)
	isWyoming := strings.HasPrefix(trimmed, "tcp://") || strings.HasPrefix(trimmed, "wyoming://")
	return &DefaultSTTClient{
		rawURL:    trimmed,
		isWyoming: isWyoming,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Transcribe transcribes WAV audio bytes to text.
func (c *DefaultSTTClient) Transcribe(ctx context.Context, wavData []byte) (string, error) {
	if c.rawURL == "" {
		return "", errors.New("stt url is empty")
	}

	if c.isWyoming {
		return c.transcribeWyoming(ctx, wavData)
	}
	return c.transcribeHTTP(ctx, wavData)
}

func (c *DefaultSTTClient) transcribeWyoming(ctx context.Context, wavData []byte) (string, error) {
	addr := c.rawURL
	addr = strings.TrimPrefix(addr, "tcp://")
	addr = strings.TrimPrefix(addr, "wyoming://")

	d := net.Dialer{Timeout: 5 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return "", fmt.Errorf("wyoming dial failed: %w", err)
	}
	var closer io.Closer = conn
	defer closer.Close()

	// Watch context cancellation
	go func() {
		<-ctx.Done()
		closer.Close()
	}()

	// Extract raw PCM bytes from WAV (skip 44-byte standard header if present)
	pcm := wavData
	if len(wavData) > 44 && bytes.HasPrefix(wavData, []byte("RIFF")) {
		pcm = wavData[44:]
	}

	var buf bytes.Buffer
	buf.WriteString("{\"type\":\"transcribe\"}\n")

	startData := `{"rate":16000,"width":2,"channels":1,"timestamp":null}`
	fmt.Fprintf(&buf, "{\"type\":\"audio-start\",\"data_length\":%d}\n%s", len(startData), startData)

	chunkData := `{"rate":16000,"width":2,"channels":1,"timestamp":null}`
	fmt.Fprintf(&buf, "{\"type\":\"audio-chunk\",\"data_length\":%d,\"payload_length\":%d}\n%s", len(chunkData), len(pcm), chunkData)
	buf.Write(pcm)

	stopData := `{"timestamp":null}`
	fmt.Fprintf(&buf, "{\"type\":\"audio-stop\",\"data_length\":%d}\n%s", len(stopData), stopData)

	if _, err := conn.Write(buf.Bytes()); err != nil {
		return "", err
	}

	// 5. Read response events until transcript is received
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return "", fmt.Errorf("wyoming read failed: %w", err)
		}
		var header struct {
			Type          string `json:"type"`
			DataLength    int    `json:"data_length"`
			PayloadLength int    `json:"payload_length"`
		}
		if err := json.Unmarshal(line, &header); err != nil {
			continue
		}

		var dataBytes []byte
		if header.DataLength > 0 {
			dataBytes = make([]byte, header.DataLength)
			if _, err := io.ReadFull(reader, dataBytes); err != nil {
				return "", err
			}
		}
		if header.PayloadLength > 0 {
			payloadBytes := make([]byte, header.PayloadLength)
			if _, err := io.ReadFull(reader, payloadBytes); err != nil {
				return "", err
			}
		}

		if header.Type == "transcript" {
			var result struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(dataBytes, &result); err == nil {
				return strings.TrimSpace(result.Text), nil
			}
			return strings.TrimSpace(string(dataBytes)), nil
		}
	}
}

func (c *DefaultSTTClient) transcribeHTTP(ctx context.Context, wavData []byte) (string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	partHeader := make(textproto.MIMEHeader)
	partHeader.Set("Content-Disposition", `form-data; name="audio"; filename="audio.wav"`)
	partHeader.Set("Content-Type", "audio/wav")
	part, err := writer.CreatePart(partHeader)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(wavData); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.rawURL, &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 1024))
		if readErr != nil {
			respBody = []byte("unknown error")
		}
		return "", fmt.Errorf("stt http error %d: %s", resp.StatusCode, string(respBody))
	}

	var res struct {
		Text       string `json:"text"`
		Transcript string `json:"transcript"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", fmt.Errorf("stt decode error: %w", err)
	}

	if res.Text != "" {
		return strings.TrimSpace(res.Text), nil
	}
	return strings.TrimSpace(res.Transcript), nil
}

// --- Default Brain Client ---

// DefaultBrainClient communicates with Aerial Brain via HTTP SSE or JSON,
// and optionally with the karakos gateway's MANDOS streaming endpoint
// (specs/2026-09-28-mandos-streaming.md's /ask/stream) when streamURL is set.
type DefaultBrainClient struct {
	url        string
	streamURL  string
	httpClient *http.Client
}

// NewDefaultBrainClient constructs a BrainClient with no streaming support
// (AskStream always returns ErrStreamingNotConfigured).
func NewDefaultBrainClient(url string, timeoutSec int) BrainClient {
	return NewDefaultBrainClientWithStream(url, "", timeoutSec)
}

// NewDefaultBrainClientWithStream constructs a BrainClient that also
// implements StreamingBrainClient. streamURL is optional (e.g.
// config.VoiceHubConfig.BrainStreamURL); when empty, AskStream behaves
// exactly like NewDefaultBrainClient's client — it returns
// ErrStreamingNotConfigured immediately, with no network call, so Hub falls
// back to the non-streaming Ask() path for every turn.
func NewDefaultBrainClientWithStream(url, streamURL string, timeoutSec int) *DefaultBrainClient {
	return &DefaultBrainClient{
		url:       strings.TrimSpace(url),
		streamURL: strings.TrimSpace(streamURL),
		httpClient: &http.Client{
			Transport: &http.Transport{
				ResponseHeaderTimeout: time.Duration(timeoutSec) * time.Second,
			},
		},
	}
}

// Ask sends the transcribed prompt to the agent brain and listens for status updates and reply.
func (b *DefaultBrainClient) Ask(ctx context.Context, ask AskRequest, onStatus func(status string)) (string, error) {
	if b.url == "" {
		return fmt.Sprintf("I heard: %s", ask.Prompt), nil
	}

	reqBody, err := json.Marshal(map[string]any{
		"prompt":        ask.Prompt,
		"session_id":    ask.SessionID,
		"effort":        "low",
		"node_id":       ask.NodeID,
		"speaker":       ask.Speaker,
		"speaker_score": ask.SpeakerScore,
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.url, bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream, application/json")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, 1024))
		if readErr != nil {
			respBytes = []byte("unknown error")
		}
		return "", fmt.Errorf("brain http error %d: %s", resp.StatusCode, string(respBytes))
	}

	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "text/event-stream") {
		return b.consumeSSE(resp.Body, onStatus)
	}

	// JSON fallback
	var res struct {
		Reply   string `json:"reply"`
		Text    string `json:"text"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}
	if res.Reply != "" {
		return strings.TrimSpace(res.Reply), nil
	}
	if res.Text != "" {
		return strings.TrimSpace(res.Text), nil
	}
	return strings.TrimSpace(res.Content), nil
}

func (b *DefaultBrainClient) consumeSSE(r io.Reader, onStatus func(status string)) (string, error) {
	var reply string
	err := scanSSE(r, func(ev sseEvent) bool {
		var payload map[string]any
		if json.Unmarshal([]byte(ev.Data), &payload) != nil {
			return false
		}
		switch ev.Event {
		case "status":
			if statusVal, ok := payload["status"].(string); ok && onStatus != nil {
				onStatus(statusVal)
			}
		case "reply":
			if replyVal, ok := payload["reply"].(string); ok {
				reply = replyVal
			}
		case "done":
			return true
		}
		return false
	})
	return strings.TrimSpace(reply), err
}

// AskStream implements StreamingBrainClient against the karakos gateway's
// MANDOS streaming endpoint (specs/2026-09-28-mandos-streaming.md's
// /ask/stream): status* sentence* reply done, where each `sentence` event
// carries {"text","engine","audio_b64"} (base64 WAV), in reply order.
//
// Returns ErrStreamingNotConfigured immediately, with no network call, when
// streamURL is empty.
func (b *DefaultBrainClient) AskStream(ctx context.Context, ask AskRequest, onStatus func(status string), onSentence func(text, engine string, wav []byte) error) (string, error) {
	if b.streamURL == "" {
		return "", ErrStreamingNotConfigured
	}

	reqBody, err := json.Marshal(map[string]any{
		"prompt":        ask.Prompt,
		"session_id":    ask.SessionID,
		"effort":        "low",
		"node_id":       ask.NodeID,
		"speaker":       ask.Speaker,
		"speaker_score": ask.SpeakerScore,
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.streamURL, bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, 1024))
		if readErr != nil {
			respBytes = []byte("unknown error")
		}
		return "", fmt.Errorf("brain stream http error %d: %s", resp.StatusCode, string(respBytes))
	}
	if contentType := resp.Header.Get("Content-Type"); !strings.Contains(contentType, "text/event-stream") {
		return "", fmt.Errorf("brain stream returned unexpected content-type %q", contentType)
	}

	return b.consumeSSEStream(resp.Body, onStatus, onSentence)
}

func (b *DefaultBrainClient) consumeSSEStream(r io.Reader, onStatus func(status string), onSentence func(text, engine string, wav []byte) error) (string, error) {
	var reply string
	var sentenceErr error
	err := scanSSE(r, func(ev sseEvent) bool {
		var payload map[string]any
		if json.Unmarshal([]byte(ev.Data), &payload) != nil {
			return false
		}
		switch ev.Event {
		case "status":
			if statusVal, ok := payload["status"].(string); ok && onStatus != nil {
				onStatus(statusVal)
			}
		case "sentence":
			text, _ := payload["text"].(string)     //nolint:errcheck // ok is intentionally discarded: absent/non-string fields just mean empty text
			engine, _ := payload["engine"].(string) //nolint:errcheck // same as above, for engine
			var wav []byte
			if audioB64, ok := payload["audio_b64"].(string); ok && audioB64 != "" {
				decoded, decErr := base64.StdEncoding.DecodeString(audioB64)
				if decErr == nil {
					wav = decoded
				}
			}
			if onSentence != nil {
				if err := onSentence(text, engine, wav); err != nil {
					sentenceErr = err
					return true
				}
			}
		case "error":
			msg, _ := payload["error"].(string) //nolint:errcheck // ok is intentionally discarded: a non-string/absent field just falls back to the generic message below
			if msg == "" {
				msg = "brain stream reported an error"
			}
			sentenceErr = errors.New(msg)
			return true
		case "reply":
			if replyVal, ok := payload["reply"].(string); ok {
				reply = replyVal
			}
		case "done":
			return true
		}
		return false
	})
	if sentenceErr != nil {
		return reply, sentenceErr
	}
	return strings.TrimSpace(reply), err
}

// sseEvent is one decoded Server-Sent Event: the event name (default "" —
// SSE treats an unlabeled event as "message") and its data, with multi-line
// `data:` fields joined by "\n" per the SSE spec.
type sseEvent struct {
	Event string
	Data  string
}

// scanSSE reads r as a Server-Sent Events stream and calls handle once per
// complete event (dispatched on a blank line, matching the SSE spec and
// what both gateway endpoints this client talks to actually send). handle
// returns true to stop reading early (e.g. once "done" or a terminal error
// event has been seen) — scanSSE then stops and returns nil, leaving any
// remaining body unread.
//
// The default bufio.Scanner token limit (64KB) is too small for a
// `sentence` event carrying several seconds of base64-encoded WAV audio, so
// this uses a much larger buffer.
func scanSSE(r io.Reader, handle func(sseEvent) bool) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var event string
	var dataLines []string
	dispatch := func() bool {
		if event == "" && len(dataLines) == 0 {
			return false
		}
		stop := handle(sseEvent{Event: event, Data: strings.Join(dataLines, "\n")})
		event = ""
		dataLines = nil
		return stop
	}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if dispatch() {
				return nil
			}
			continue
		}
		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			continue
		}
		// Comments (":") and other SSE fields (id:, retry:) are ignored.
	}
	if event != "" || len(dataLines) > 0 {
		dispatch()
	}
	return scanner.Err()
}

// --- Default TTS Client ---

// DefaultTTSClient communicates with OpenAI-compatible TTS endpoints (Kokoro).
type DefaultTTSClient struct {
	url        string
	model      string
	voice      string
	httpClient *http.Client
}

// NewDefaultTTSClient constructs a TTSClient.
func NewDefaultTTSClient(url, model, voice string, timeoutSec int) TTSClient {
	return &DefaultTTSClient{
		url:   strings.TrimSpace(url),
		model: model,
		voice: voice,
		httpClient: &http.Client{
			Transport: &http.Transport{
				ResponseHeaderTimeout: time.Duration(timeoutSec) * time.Second,
			},
		},
	}
}

// Synthesize sends text to TTS and returns synthesized audio bytes and format.
func (t *DefaultTTSClient) Synthesize(ctx context.Context, text string) ([]byte, string, error) {
	if t.url == "" {
		return nil, "mp3", nil
	}

	payload, err := json.Marshal(map[string]string{
		"model":           t.model,
		"input":           text,
		"voice":           t.voice,
		"response_format": "mp3",
	})
	if err != nil {
		return nil, "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(payload))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "audio/mpeg, audio/mp3, audio/wav, */*")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, 1024))
		if readErr != nil {
			errBytes = []byte("unknown error")
		}
		return nil, "", fmt.Errorf("tts http error %d: %s", resp.StatusCode, string(errBytes))
	}

	audioBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}

	format := "mp3"
	if strings.Contains(resp.Header.Get("Content-Type"), "wav") {
		format = "wav"
	}

	return audioBytes, format, nil
}
