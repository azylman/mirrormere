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

// BrainAudioChunk is one sentence's worth of pre-synthesized audio delivered
// by a brain that streams TTS as it goes (MANDOS, SPEC-011 "Sentence
// Streaming" — the karakos gateway's POST /ask/stream). Format is the audio
// container (e.g. "wav"); Data is raw decoded bytes. An empty/undecodable
// Data (len(Data) == 0) signals that sentence's audio failed upstream —
// Text is still populated so the hub can synthesize a local fallback for
// just that sentence rather than dropping it.
type BrainAudioChunk struct {
	Text       string
	Format     string
	Data       []byte
	SampleRate int
	Channels   int
}

// AudioStreamingBrainClient is an optional capability a BrainClient may
// additionally implement: alongside status and the final reply, it delivers
// sentence-level audio as soon as each sentence is synthesized upstream, so
// the hub can start playback well before the full reply is known. Hub
// checks for this via a type assertion, so a plain BrainClient (JSON /ask,
// an older brain, or a test fake that only implements Ask) keeps compiling
// and behaves exactly as before — no double speech, one audio_chunk at the
// end. See SPEC-011.
type AudioStreamingBrainClient interface {
	BrainClient
	AskStreaming(ctx context.Context, req AskRequest, onStatus func(status string), onAudio func(chunk BrainAudioChunk)) (string, error)
}

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
	metrics  *Metrics
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

// WithMetrics overrides the voice metrics collector.
func WithMetrics(m *Metrics) HubOption {
	return func(h *Hub) { h.metrics = m }
}

// NewHub constructs a Voice Hub coordinator.
func NewHub(cfg *config.VoiceHubConfig, coord *Coordinator, opts ...HubOption) *Hub {
	h := &Hub{
		cfg:   cfg,
		coord: coord,
	}
	if cfg != nil && cfg.Enabled {
		h.stt = NewDefaultSTTClient(cfg.STTURL)
		h.brain = NewDefaultBrainClient(cfg.BrainURL, cfg.GetBrainTimeoutSeconds())
		h.tts = NewDefaultTTSClient(cfg.TTSURL, cfg.GetTTSModel(), cfg.GetTTSVoice(), cfg.GetTTSTimeoutSeconds())
		if sid := NewSpeakerIdentifierFromConfig(cfg.SpeakerID); sid != nil {
			h.speaker = sid
		}
	}
	for _, opt := range opts {
		opt(h)
	}
	if h.metrics == nil {
		h.metrics = DefaultMetrics()
	}
	return h
}


// IsEnabled reports whether the voice hub is enabled.
func (h *Hub) IsEnabled() bool {
	return h != nil && h.cfg.IsEnabled()
}

// Interact coordinates the complete voice interaction pipeline for an incoming audio recording.
func (h *Hub) Interact(ctx context.Context, audio io.Reader, nodeID, sessionID string, sink SSEEventSink, timings EdgeTimings) (retErr error) {
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

	if sessionID == "" {
		sessionID = fmt.Sprintf("turn-%d", time.Now().UnixNano())
	}

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

	if timings.WakeEvalSec > 0 {
		h.metrics.RecordStageDuration(nodeID, "wake_eval", "success", timings.WakeEvalSec)
	}
	if timings.UtteranceSpeechSec > 0 {
		h.metrics.RecordStageDuration(nodeID, "utterance_speech", "success", timings.UtteranceSpeechSec)
	}
	if timings.UtteranceSilenceSec > 0 {
		h.metrics.RecordStageDuration(nodeID, "utterance_silence", "success", timings.UtteranceSilenceSec)
	}

	finalStatus := "success"
	defer func() {
		if retErr != nil {
			finalStatus = "error"
		}
		h.metrics.RecordTurn(nodeID, finalStatus)
	}()

	// Step 1: Notify HUD and client: transcribing
	if h.coord != nil {
		h.coord.Transition(StateTranscribing, nil, nil, nil, nil)
	}
	if err := safeSink("state", map[string]string{"state": StateTranscribing}); err != nil {
		return err
	}

	// Step 2: Speech-to-Text transcription, with speaker matching in parallel
	if h.stt == nil {
		finalStatus = "error"
		h.metrics.RecordStageDuration(nodeID, "stt", "error", 0)
		h.metrics.RecordError(nodeID, "stt", "stt_error")
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
	sttStart := time.Now()
	transcript, err := h.stt.Transcribe(ctx, wavData)
	sttDuration := time.Since(sttStart).Seconds()
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
		finalStatus = "error"
		h.metrics.RecordStageDuration(nodeID, "stt", "error", sttDuration)
		h.metrics.RecordError(nodeID, "stt", "stt_error")
		if h.coord != nil {
			h.coord.Transition(StateError, nil, nil, nil, nil)
		}
		if sinkErr := safeSink("error", map[string]string{"error": "stt_error", "message": err.Error()}); sinkErr != nil {
			return sinkErr
		}
		return fmt.Errorf("%w: %w", ErrSTTFailed, err)
	}
	h.metrics.RecordStageDuration(nodeID, "stt", "success", sttDuration)
	pcmBytes := len(wavData) - 44
	if pcmBytes < 0 {
		pcmBytes = 0
	}
	audioSec := float64(pcmBytes) / (16000.0 * 2.0)
	if audioSec > 0.001 {
		rtf := sttDuration / audioSec
		h.metrics.RecordSTTRTF(nodeID, "whisper", rtf)
	}

	// Step 3: Transition to thinking with recognized transcript
	if h.coord != nil {
		h.coord.Transition(StateThinking, &transcript, nil, nil, nil)
	}
	if err := safeSink("transcript", map[string]string{"transcript": transcript, "speaker": speaker}); err != nil {
		return err
	}

	// Step 4: Brain deliberation with live tool status forwarding, and (if
	// the brain supports it) sentence-level audio streaming (MANDOS).
	if h.brain == nil {
		finalStatus = "error"
		h.metrics.RecordStageDuration(nodeID, "brain", "error", 0)
		h.metrics.RecordError(nodeID, "brain", "brain_error")
		return ErrBrainFailed
	}

	onStatus := func(status string) {
		if h.coord != nil {
			h.coord.SetStatus(&status)
		}
		if sinkErr := safeSink("status", map[string]string{"status": status}); sinkErr != nil {
			// Status stream dropped by client; deliberate gracefully
			slog.Debug("status sink error", "error", sinkErr)
		}
	}

	askReq := AskRequest{
		Prompt:       transcript,
		SessionID:    sessionID,
		NodeID:       nodeID,
		Speaker:      speaker,
		SpeakerScore: match.Score,
	}

	engine := h.cfg.GetTTSModel()

	var reply string
	if streamingBrain, ok := h.brain.(AudioStreamingBrainClient); ok {
		// Sentence audio is forwarded to the dock the instant it arrives —
		// that head start (skip the wait for the whole reply) is the point
		// of streaming at all. Since the Hub doesn't know in advance how
		// many sentences there will be, it can't mark a chunk is_final as
		// it sends it; instead every real chunk goes out as is_final:false,
		// and once the brain call returns, one extra marker audio_chunk
		// (empty data, is_final:true) tells the dock playback is complete.
		var (
			chunkIdx     int
			streamedAny  bool
			anyChunkSent bool
			// caption accumulates the text of every sentence actually sent,
			// so the kiosk HUD (fed by the coordinator's voice.state, not by
			// this SSE stream) shows a live caption that grows sentence by
			// sentence.
			caption strings.Builder

			streamChars   int
			streamStart   time.Time
			hasStreamTime bool

			pcmBuf       []byte
			streamFormat = "wav"
			streamRate   = 24000
			streamCh     = 1
		)

		const pcmChunkFloor = 4800 // ~100ms at 24kHz 16-bit mono

		emitChunk := func(format string, data []byte, sampleRate int, channels int, isFinal bool) {
			payload := map[string]any{
				"chunk_index": chunkIdx,
				"format":      format,
				"is_final":    isFinal,
				"data":        base64.StdEncoding.EncodeToString(data),
			}
			if sampleRate > 0 {
				payload["sample_rate"] = sampleRate
			}
			if channels > 0 {
				payload["channels"] = channels
			}
			if sinkErr := safeSink("audio_chunk", payload); sinkErr != nil {
				slog.Debug("streamed audio_chunk sink error", "error", sinkErr)
			}
			chunkIdx++
		}

		onAudio := func(chunk BrainAudioChunk) {
			streamedAny = true
			if !hasStreamTime {
				streamStart = time.Now()
				hasStreamTime = true
			}
			streamChars += len(chunk.Text)

			data := chunk.Data
			format := chunk.Format
			if format == "" {
				format = "wav"
			}
			streamFormat = format
			rate := chunk.SampleRate
			if rate <= 0 {
				rate = 24000
			}
			streamRate = rate
			ch := chunk.Channels
			if ch <= 0 {
				ch = 1
			}
			streamCh = ch

			if len(data) == 0 {
				// Text-only sentence: either the brain streams text and
				// leaves speech to this instance (a first-class mode, e.g.
				// a local Kokoro), or its audio was missing. Either way,
				// synthesize just that sentence locally.
				if h.tts == nil {
					slog.Warn("no local TTS client configured; dropping streamed sentence", "text", chunk.Text)
					return
				}
				fbData, fbFormat, fbErr := h.tts.Synthesize(ctx, chunk.Text)
				if fbErr != nil || len(fbData) == 0 {
					slog.Warn("local TTS fallback for streamed sentence failed; dropping sentence", "text", chunk.Text, "error", fbErr)
					return
				}
				slog.Debug("synthesized text-only streamed sentence locally", "text", chunk.Text)
				data, format = fbData, fbFormat
				streamFormat = format
			}

			if caption.Len() > 0 && chunk.Text != "" {
				caption.WriteString(" ")
			}
			caption.WriteString(chunk.Text)
			if h.coord != nil {
				captionSoFar := caption.String()
				h.coord.Transition(StateSpeaking, &transcript, &captionSoFar, &engine, nil)
			}

			if format == "pcm" {
				pcmBuf = append(pcmBuf, data...)
				for len(pcmBuf) >= pcmChunkFloor {
					emitPiece := pcmBuf[:pcmChunkFloor]
					pcmBuf = pcmBuf[pcmChunkFloor:]
					anyChunkSent = true
					emitChunk("pcm", emitPiece, rate, ch, false)
				}
			} else {
				anyChunkSent = true
				emitChunk(format, data, rate, ch, false)
			}
		}

		brainStart := time.Now()
		reply, err = streamingBrain.AskStreaming(ctx, askReq, onStatus, onAudio)
		brainDuration := time.Since(brainStart).Seconds()
		if err != nil {
			finalStatus = "error"
			h.metrics.RecordStageDuration(nodeID, "brain", "error", brainDuration)
			h.metrics.RecordError(nodeID, "brain", "brain_error")
			if h.coord != nil {
				h.coord.Transition(StateError, nil, nil, nil, nil)
			}
			if sinkErr := safeSink("error", map[string]string{"error": "brain_error", "message": err.Error()}); sinkErr != nil {
				return sinkErr
			}
			return fmt.Errorf("%w: %w", ErrBrainFailed, err)
		}
		h.metrics.RecordStageDuration(nodeID, "brain", "success", brainDuration)

		if streamedAny {
			// The brain streamed sentence audio, so the hub must NOT also
			// run its own TTS over the full reply (no double speech).
			if anyChunkSent || len(pcmBuf) > 0 {
				if len(pcmBuf) > 0 {
					anyChunkSent = true
					emitChunk("pcm", pcmBuf, streamRate, streamCh, false)
					pcmBuf = nil
				}

				streamTTSDuration := time.Since(streamStart).Seconds()
				h.metrics.RecordStageDuration(nodeID, "tts", "success", streamTTSDuration)
				if streamTTSDuration > 0.001 && streamChars > 0 {
					cps := float64(streamChars) / streamTTSDuration
					h.metrics.RecordTTSCPS(nodeID, engine, cps)
				}

				// The kiosk caption shows the brain's final reply text,
				// which is authoritative over the joined sentences.
				if h.coord != nil {
					h.coord.Transition(StateSpeaking, &transcript, &reply, &engine, nil)
				}
				// Every real chunk already went out as is_final:false above
				// (immediately, as it arrived) — this marker chunk (empty
				// data) is what tells the dock playback is complete now
				// that the brain call has returned.
				emitChunk(streamFormat, nil, streamRate, streamCh, true)
			} else if h.tts != nil {
				// Every streamed sentence's audio (and its local fallback)
				// failed: last resort is one TTS call over the full reply
				// so the reply is never silently dropped.
				ttsStart := time.Now()
				audioBytes, format, ttsErr := h.tts.Synthesize(ctx, reply)
				ttsDuration := time.Since(ttsStart).Seconds()
				if ttsErr != nil {
					slog.Warn("TTS synthesis failed", "node_id", nodeID, "error", ttsErr)
					finalStatus = "error"
					h.metrics.RecordStageDuration(nodeID, "tts", "error", ttsDuration)
					h.metrics.RecordError(nodeID, "tts", "tts_error")
					if sinkErr := safeSink("error", map[string]string{"error": "tts_error", "message": ttsErr.Error()}); sinkErr != nil {
						return sinkErr
					}
				} else {
					h.metrics.RecordStageDuration(nodeID, "tts", "success", ttsDuration)
					if ttsDuration > 0.001 && len(reply) > 0 {
						cps := float64(len(reply)) / ttsDuration
						h.metrics.RecordTTSCPS(nodeID, engine, cps)
					}
					if len(audioBytes) > 0 {
						if h.coord != nil {
							h.coord.Transition(StateSpeaking, &transcript, &reply, &engine, nil)
						}
						emitChunk(format, audioBytes, streamRate, streamCh, true)
					}
				}
			}
			// Devil's Advocate catch: still emit reply so the kiosk caption
			// toast renders the reply text, even though (for streaming) it
			// arrives after the audio rather than before.
			if err := safeSink("reply", map[string]string{"reply": reply, "tts_engine": engine}); err != nil {
				return err
			}
		} else {
			// The brain implements AudioStreamingBrainClient but this turn
			// streamed no sentence audio (e.g. it's pointed at plain /ask
			// rather than /ask/stream) — behave exactly like a
			// non-streaming brain: reply first, then one TTS call.
			if err := safeSink("reply", map[string]string{"reply": reply, "tts_engine": engine}); err != nil {
				return err
			}
			if h.tts != nil {
				ttsStart := time.Now()
				audioBytes, format, ttsErr := h.tts.Synthesize(ctx, reply)
				ttsDuration := time.Since(ttsStart).Seconds()
				if ttsErr != nil {
					slog.Warn("TTS synthesis failed", "node_id", nodeID, "error", ttsErr)
					finalStatus = "error"
					h.metrics.RecordStageDuration(nodeID, "tts", "error", ttsDuration)
					h.metrics.RecordError(nodeID, "tts", "tts_error")
					if sinkErr := safeSink("error", map[string]string{"error": "tts_error", "message": ttsErr.Error()}); sinkErr != nil {
						return sinkErr
					}
				} else {
					h.metrics.RecordStageDuration(nodeID, "tts", "success", ttsDuration)
					if ttsDuration > 0.001 && len(reply) > 0 {
						cps := float64(len(reply)) / ttsDuration
						h.metrics.RecordTTSCPS(nodeID, engine, cps)
					}
					if len(audioBytes) > 0 {
						if h.coord != nil {
							h.coord.Transition(StateSpeaking, &transcript, &reply, &engine, nil)
						}
						emitChunk(format, audioBytes, streamRate, streamCh, true)
					}
				}
			}
		}
	} else {
		brainStart := time.Now()
		reply, err = h.brain.Ask(ctx, askReq, onStatus)
		brainDuration := time.Since(brainStart).Seconds()
		if err != nil {
			finalStatus = "error"
			h.metrics.RecordStageDuration(nodeID, "brain", "error", brainDuration)
			h.metrics.RecordError(nodeID, "brain", "brain_error")
			if h.coord != nil {
				h.coord.Transition(StateError, nil, nil, nil, nil)
			}
			if sinkErr := safeSink("error", map[string]string{"error": "brain_error", "message": err.Error()}); sinkErr != nil {
				return sinkErr
			}
			return fmt.Errorf("%w: %w", ErrBrainFailed, err)
		}
		h.metrics.RecordStageDuration(nodeID, "brain", "success", brainDuration)

		// Devil's Advocate catch: Always emit reply event so kiosk caption toast renders reply text,
		// even if TTS synthesis subsequently fails!
		if err := safeSink("reply", map[string]string{"reply": reply, "tts_engine": engine}); err != nil {
			return err
		}

		// Step 5: Speech synthesis (TTS)
		if h.tts != nil {
			ttsStart := time.Now()
			audioBytes, format, ttsErr := h.tts.Synthesize(ctx, reply)
			ttsDuration := time.Since(ttsStart).Seconds()
			if ttsErr != nil {
				slog.Warn("TTS synthesis failed", "node_id", nodeID, "error", ttsErr)
				finalStatus = "error"
				h.metrics.RecordStageDuration(nodeID, "tts", "error", ttsDuration)
				h.metrics.RecordError(nodeID, "tts", "tts_error")
				// Graceful degradation: Log/emit error for TTS but don't drop the interaction reply
				if sinkErr := safeSink("error", map[string]string{"error": "tts_error", "message": ttsErr.Error()}); sinkErr != nil {
					return sinkErr
				}
			} else {
				h.metrics.RecordStageDuration(nodeID, "tts", "success", ttsDuration)
				if ttsDuration > 0.001 && len(reply) > 0 {
					cps := float64(len(reply)) / ttsDuration
					h.metrics.RecordTTSCPS(nodeID, engine, cps)
				}
				if len(audioBytes) > 0 {
					if h.coord != nil {
						h.coord.Transition(StateSpeaking, &transcript, &reply, &engine, nil)
					}
					b64 := base64.StdEncoding.EncodeToString(audioBytes)
					if err := safeSink("audio_chunk", map[string]any{
						"chunk_index": 0,
						"format":      format,
						"sample_rate": 24000,
						"channels":    1,
						"is_final":    true,
						"data":        b64,
					}); err != nil {
						return err
					}
				}
			}
		}
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

// DefaultBrainClient communicates with Aerial Brain via HTTP SSE or JSON.
type DefaultBrainClient struct {
	url        string
	httpClient *http.Client
}

// NewDefaultBrainClient constructs a BrainClient.
func NewDefaultBrainClient(url string, timeoutSec int) BrainClient {
	return &DefaultBrainClient{
		url: strings.TrimSpace(url),
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

	resp, jsonReply, err := b.doAskRequest(ctx, ask)
	if err != nil {
		return "", err
	}
	if resp == nil {
		// Non-SSE JSON response, already fully decoded.
		return jsonReply, nil
	}
	defer resp.Body.Close()
	return b.consumeSSE(resp.Body, onStatus)
}

// AskStreaming is like Ask, but also delivers sentence-level audio as it
// streams in (MANDOS, SPEC-011). Same request as Ask — the only thing that
// makes a brain "streaming" is the URL it's pointed at (the karakos
// gateway's POST /ask/stream instead of POST /ask): if that URL sends no
// `sentence` events, onAudio simply never fires and this behaves exactly
// like Ask.
func (b *DefaultBrainClient) AskStreaming(ctx context.Context, ask AskRequest, onStatus func(status string), onAudio func(chunk BrainAudioChunk)) (string, error) {
	if b.url == "" {
		return fmt.Sprintf("I heard: %s", ask.Prompt), nil
	}

	resp, jsonReply, err := b.doAskRequest(ctx, ask)
	if err != nil {
		return "", err
	}
	if resp == nil {
		return jsonReply, nil
	}
	defer resp.Body.Close()
	return b.consumeSSEStreaming(resp.Body, onStatus, onAudio)
}

// doAskRequest builds and sends the shared brain HTTP request for Ask and
// AskStreaming. For a text/event-stream response it returns the open
// *http.Response for the caller to consume (and closes nothing); for a
// plain JSON response it fully reads and decodes the body itself and
// returns (nil, reply, nil).
func (b *DefaultBrainClient) doAskRequest(ctx context.Context, ask AskRequest) (*http.Response, string, error) {
	sessionID := ask.SessionID
	if sessionID == "" {
		sessionID = fmt.Sprintf("turn-%d", time.Now().UnixNano())
	}
	reqBody, err := json.Marshal(map[string]any{
		"prompt":        ask.Prompt,
		"session_id":    sessionID,
		"effort":        "low",
		"node_id":       ask.NodeID,
		"speaker":       ask.Speaker,
		"speaker_score": ask.SpeakerScore,
	})
	if err != nil {
		return nil, "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream, application/json")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		respBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, 1024))
		if readErr != nil {
			respBytes = []byte("unknown error")
		}
		return nil, "", fmt.Errorf("brain http error %d: %s", resp.StatusCode, string(respBytes))
	}

	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "text/event-stream") {
		return resp, "", nil
	}
	defer resp.Body.Close()

	// JSON fallback
	var res struct {
		Reply   string `json:"reply"`
		Text    string `json:"text"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, "", err
	}
	if res.Reply != "" {
		return nil, strings.TrimSpace(res.Reply), nil
	}
	if res.Text != "" {
		return nil, strings.TrimSpace(res.Text), nil
	}
	return nil, strings.TrimSpace(res.Content), nil
}

func (b *DefaultBrainClient) consumeSSE(r io.Reader, onStatus func(status string)) (string, error) {
	scanner := bufio.NewScanner(r)
	var reply string
	var currentEvent string

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			currentEvent = ""
			continue
		}
		if strings.HasPrefix(line, "event:") {
			currentEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataStr := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			var payload map[string]any
			if unmarshalErr := json.Unmarshal([]byte(dataStr), &payload); unmarshalErr != nil {
				continue
			}

			switch currentEvent {
			case "status":
				if statusVal, ok := payload["status"].(string); ok && onStatus != nil {
					onStatus(statusVal)
				}
			case "reply":
				if replyVal, ok := payload["reply"].(string); ok {
					reply = replyVal
				}
			case "done":
				return strings.TrimSpace(reply), nil
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return reply, err
	}
	return strings.TrimSpace(reply), nil
}

// sseScannerMaxLine is the max single SSE `data:` line consumeSSEStreaming
// will accept. A `sentence` event's line carries a whole sentence's base64
// WAV audio inline (no chunked transfer within one line), which easily
// exceeds bufio.Scanner's 64KB default — a few seconds of 16kHz mono PCM,
// base64-inflated, is already past that.
const sseScannerMaxLine = 16 * 1024 * 1024

// consumeSSEStreaming is consumeSSE plus a `sentence` event: sentence-level
// pre-synthesized audio (karakos gateway's POST /ask/stream, MANDOS). A
// sentence event with missing or undecodable audio_b64 still calls onAudio,
// with an empty Data, so the hub can decide how to fall back — it is not
// silently dropped here.
func (b *DefaultBrainClient) consumeSSEStreaming(r io.Reader, onStatus func(status string), onAudio func(chunk BrainAudioChunk)) (string, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), sseScannerMaxLine)
	var reply string
	var currentEvent string

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			currentEvent = ""
			continue
		}
		if strings.HasPrefix(line, "event:") {
			currentEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataStr := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			var payload map[string]any
			if unmarshalErr := json.Unmarshal([]byte(dataStr), &payload); unmarshalErr != nil {
				continue
			}

			switch currentEvent {
			case "status":
				if statusVal, ok := payload["status"].(string); ok && onStatus != nil {
					onStatus(statusVal)
				}
			case "sentence":
				if onAudio == nil {
					continue
				}
				chunk := BrainAudioChunk{Format: "wav", SampleRate: 24000, Channels: 1}
				if fmtVal, ok := payload["format"].(string); ok && fmtVal != "" {
					chunk.Format = fmtVal
				}
				if rateVal, ok := payload["sample_rate"].(float64); ok && rateVal > 0 {
					chunk.SampleRate = int(rateVal)
				}
				if chVal, ok := payload["channels"].(float64); ok && chVal > 0 {
					chunk.Channels = int(chVal)
				}
				if textVal, ok := payload["text"].(string); ok {
					chunk.Text = textVal
				}
				if b64Val, ok := payload["audio_b64"].(string); ok && b64Val != "" {
					if decoded, decErr := base64.StdEncoding.DecodeString(b64Val); decErr == nil {
						chunk.Data = decoded
					} else {
						slog.Warn("sentence event audio_b64 failed to decode", "error", decErr)
					}
				}
				onAudio(chunk)
			case "reply":
				if replyVal, ok := payload["reply"].(string); ok {
					reply = replyVal
				}
			case "done":
				return strings.TrimSpace(reply), nil
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return reply, err
	}
	return strings.TrimSpace(reply), nil
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
		"response_format": "wav",
	})
	if err != nil {
		return nil, "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(payload))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "audio/wav, audio/mpeg, audio/mp3, */*")

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
