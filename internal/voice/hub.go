package voice

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/azylman/mirrormere/internal/chatlog"
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
	// ErrBrainTimeout is returned when agent brain deliberation exceeds its timeout.
	ErrBrainTimeout = fmt.Errorf("%w: agent brain deliberation timed out", ErrBrainFailed)
	// ErrTTSFailed is returned when text-to-speech synthesis fails.
	ErrTTSFailed = errors.New("text-to-speech synthesis failed")
	// ErrTTSTimeout is returned when text-to-speech synthesis exceeds its timeout.
	ErrTTSTimeout = fmt.Errorf("%w: text-to-speech synthesis timed out", ErrTTSFailed)
	// ErrUnsupportedAudioFormat is returned when audio is not raw 16-bit PCM (s16le). The hub
	// only plays PCM; wav/mp3 chunks and non-PCM TTS responses abort the turn.
	ErrUnsupportedAudioFormat = errors.New("unsupported audio format: only raw s16le PCM is supported")
	// ErrMixedPCMFormat is returned when PCM audio changes sample rate or channel count mid-turn.
	ErrMixedPCMFormat = errors.New("PCM sample rate or channel count changed mid-turn")
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
	// Speaker is the matched enrolled speaker, or "".
	// It is a personalisation hint, not authentication.
	Speaker string
	// SpeakerScore is the best cosine similarity seen, 0 when matching did not run.
	SpeakerScore float64
}

// BrainClient abstracts agent deliberation and live tool status streaming.
type BrainClient interface {
	Ask(ctx context.Context, req AskRequest, onStatus func(status string)) (string, error)
}

// BrainAudioChunk is one sentence's worth of text delivered by an
// AudioStreamingBrainClient as each sentence finishes generation,
// to be synthesized via TTS.
type BrainAudioChunk struct {
	Text string
}

// AudioStreamingBrainClient is an optional capability a BrainClient may
// additionally implement: alongside status and the final reply, it delivers
// sentence-level audio as soon as each sentence is synthesized upstream, so
// the hub can start playback well before the full reply is known. Hub
// checks for this via a type assertion, so a plain BrainClient (JSON /ask,
// an older brain, or a test fake that only implements Ask) keeps compiling
// and behaves exactly as before — no double speech, one audio_chunk at the
// end. See .
type AudioStreamingBrainClient interface {
	BrainClient
	AskStreaming(ctx context.Context, req AskRequest, onStatus func(status string), onAudio func(chunk BrainAudioChunk)) (string, error)
}

// TTSAudioChunk represents a slice of audio streamed from a TTS backend.
type TTSAudioChunk struct {
	Data       []byte
	Format     string
	SampleRate int
	Channels   int
}

// TTSClient abstracts streaming speech synthesis.
type TTSClient interface {
	Synthesize(ctx context.Context, text string, onChunk func(chunk TTSAudioChunk) error) error
}

// Hub coordinates the end-to-end voice pipeline (STT -> Brain -> TTS).
type Hub struct {
	mu                sync.Mutex
	inFlight          bool
	cfg               *config.VoiceHubConfig
	coord             *Coordinator
	stt               STTClient
	brain             BrainClient
	fastPath          FastPathClient
	tts               TTSClient
	speaker           SpeakerIdentifier
	metrics           *Metrics
	chatLog           *chatlog.Store
	onChat            func()
	brainTimeout      time.Duration
	ttsTimeout        time.Duration
	heartbeatInterval time.Duration
}

// HubOption configures optional Hub overrides (e.g. for testing).
type HubOption func(*Hub)

// WithChatLog makes the hub record every turn (the transcribed user text and
// the brain's final reply) into store, keyed by voice node, for the chat-log
// widget. onChange (optional) runs after each recorded message; the server
// uses it to run the normal widget push path (cache update + widget.update
// broadcast) so displays re-render.
func WithChatLog(store *chatlog.Store, onChange func()) HubOption {
	return func(h *Hub) { h.chatLog, h.onChat = store, onChange }
}

// recordChat appends one message to the node's conversation. Empty text is ignored.
func (h *Hub) recordChat(nodeID, role, author, text string) {
	if h.chatLog == nil || strings.TrimSpace(text) == "" {
		return
	}
	h.chatLog.Add(chatlog.NodeKey(nodeID), chatlog.Message{Role: role, Author: author, Text: text})
	if h.onChat != nil {
		h.onChat()
	}
}

// inactivityContext provides a sliding window inactivity timeout.
// When no activity occurs for the specified duration, the context is canceled
// with context.DeadlineExceeded. Calls to Reset() advance the deadline.
type inactivityContext struct {
	context.Context
	cancel       context.CancelFunc
	timer        *time.Timer
	d            time.Duration
	mu           sync.Mutex
	lastActivity time.Time
	err          error
	done         bool
}

func newInactivityContext(parent context.Context, timeout time.Duration) (*inactivityContext, func()) {
	ctx, cancel := context.WithCancel(parent)
	ic := &inactivityContext{
		Context:      ctx,
		cancel:       cancel,
		d:            timeout,
		lastActivity: time.Now(),
	}

	if timeout > 0 {
		var onTimeout func()
		onTimeout = func() {
			ic.mu.Lock()
			defer ic.mu.Unlock()

			if ic.done || ic.Context.Err() != nil {
				return
			}

			elapsed := time.Since(ic.lastActivity)
			if elapsed < ic.d {
				remaining := ic.d - elapsed
				ic.timer = time.AfterFunc(remaining, onTimeout)
				return
			}

			ic.err = context.DeadlineExceeded
			ic.done = true
			ic.cancel()
		}
		ic.mu.Lock()
		ic.timer = time.AfterFunc(timeout, onTimeout)
		ic.mu.Unlock()
	}

	stop := func() {
		ic.mu.Lock()
		defer ic.mu.Unlock()
		ic.done = true
		if ic.timer != nil {
			ic.timer.Stop()
		}
		ic.cancel()
	}

	return ic, stop
}

func (ic *inactivityContext) Err() error {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	if ic.err != nil {
		return ic.err
	}
	return ic.Context.Err()
}

func (ic *inactivityContext) Reset() {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	if ic.done || ic.Context.Err() != nil {
		return
	}
	ic.lastActivity = time.Now()
}

// isTimeoutError reports whether an error or context deadline was caused by a timeout,
// explicitly ignoring client-side parent context cancellations.
func isTimeoutError(err error, timeoutCtx, parentCtx context.Context) bool {
	if parentCtx != nil && errors.Is(parentCtx.Err(), context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if timeoutCtx != nil && errors.Is(timeoutCtx.Err(), context.DeadlineExceeded) {
		return true
	}
	return false
}

// NewHub constructs a Voice Hub coordinator.
func NewHub(cfg *config.VoiceHubConfig, coord *Coordinator, opts ...HubOption) *Hub {
	h := &Hub{
		cfg:               cfg,
		coord:             coord,
		brainTimeout:      30 * time.Second,
		ttsTimeout:        10 * time.Second,
		heartbeatInterval: 5 * time.Second,
	}
	if cfg != nil {
		h.brainTimeout = time.Duration(cfg.GetBrainTimeoutSeconds()) * time.Second
		h.ttsTimeout = time.Duration(cfg.GetTTSTimeoutSeconds()) * time.Second
	}
	if cfg != nil && cfg.Enabled {
		h.stt = NewDefaultSTTClient(cfg.STTURL)
		h.brain = NewDefaultBrainClient(cfg.BrainURL, cfg.GetBrainTimeoutSeconds())
		h.tts = NewDefaultTTSClient(cfg.TTSURL, cfg.GetTTSModel(), cfg.GetTTSVoice(), cfg.GetTTSTimeoutSeconds())
		if sid := NewSpeakerIdentifierFromConfig(cfg.SpeakerID); sid != nil {
			h.speaker = sid
		}
	}
	if cfg != nil && cfg.Enabled && cfg.FastPath.IsEnabled() {
		h.fastPath = NewDefaultFastPathClient(cfg.FastPath.URL, time.Duration(cfg.FastPath.GetTimeoutMS())*time.Millisecond)
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

	var turnActive bool
	defer func() {
		if r := recover(); r != nil {
			if h.coord != nil {
				h.coord.Transition(StateError, nil, nil, nil, nil)
			}
			panic(r)
		}
		if retErr != nil && turnActive && h.coord != nil {
			h.coord.Transition(StateError, nil, nil, nil, nil)
		}
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
	turnActive = true
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
	h.recordChat(nodeID, chatlog.RoleHuman, speaker, transcript)
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

	brainTimeout := h.brainTimeout
	if brainTimeout <= 0 {
		brainTimeout = 30 * time.Second
	}
	heartbeatInterval := h.heartbeatInterval
	if heartbeatInterval <= 0 {
		heartbeatInterval = 5 * time.Second
	}

	var heartbeatMu sync.Mutex
	heartbeatActive := true
	stopHeartbeat := make(chan struct{})
	var stopOnce sync.Once
	stopThinking := func() {
		stopOnce.Do(func() {
			heartbeatMu.Lock()
			heartbeatActive = false
			heartbeatMu.Unlock()
			close(stopHeartbeat)
		})
	}
	defer stopThinking()

	startHeartbeat := func(doneCtx context.Context) {
		go func() {
			ticker := time.NewTicker(heartbeatInterval)
			defer ticker.Stop()
			tickerStart := time.Now()
			for {
				select {
				case <-stopHeartbeat:
					return
				case <-doneCtx.Done():
					return
				case tick := <-ticker.C:
					heartbeatMu.Lock()
					active := heartbeatActive
					heartbeatMu.Unlock()
					if !active {
						return
					}
					elapsedSec := int(math.Round(tick.Sub(tickerStart).Seconds()))
					if elapsedSec <= 0 {
						elapsedSec = 1
					}
					if sinkErr := safeSink("thinking", map[string]int{"elapsed_seconds": elapsedSec}); sinkErr != nil {
						slog.Debug("thinking heartbeat sink error", "error", sinkErr)
						return
					}
				}
			}
		}()
	}

	engine := h.cfg.GetTTSModel()

	// Optional LAMMAS fast path: wrap the brain for this turn so the
	// deterministic endpoint is tried first and its sentences flow through
	// the same streaming playback code as a brain reply. With no fast path
	// configured the brain is used untouched.
	turnBrain := h.brain
	if h.fastPath != nil {
		turnBrain = &fastPathBrain{inner: h.brain, fp: h.fastPath, metrics: h.metrics, nodeID: nodeID}
	}

	emitNonStreamingReply := func(reply string) error {
		if h.coord != nil {
			h.coord.Transition(StateSpeaking, &transcript, &reply, &engine, nil)
		}
		if err := safeSink("reply", map[string]string{"reply": reply, "tts_engine": engine}); err != nil {
			return err
		}
		if h.tts != nil {
			status, ttsErr := h.synthesizeAndEmitReply(ctx, reply, nodeID, engine, &transcript, safeSink)
			if ttsErr != nil {
				return ttsErr
			}
			if status == "error" {
				finalStatus = "error"
			}
		}
		return nil
	}

	var reply string
	if streamingBrain, ok := turnBrain.(AudioStreamingBrainClient); ok {
		var (
			streamedAny      bool
			streamErr        error
			caption          strings.Builder
			streamChars      int
			totalTTSDuration float64
		)

		streamCtx, stopStream := newInactivityContext(ctx, brainTimeout)
		defer stopStream()
		cancelStream := stopStream
		startHeartbeat(streamCtx)

		streamOnStatus := func(status string) {
			streamCtx.Reset()
			onStatus(status)
		}

		streamer := &pcmChunkStreamer{
			sink:          safeSink,
			ignoreSinkErr: true,
		}

		onAudio := func(chunk BrainAudioChunk) {
			stopThinking()
			if chunk.Text != "" {
				streamCtx.Reset()
			}
			if streamErr != nil {
				return
			}
			if strings.TrimSpace(chunk.Text) == "" {
				return
			}
			streamedAny = true
			streamChars += len(chunk.Text)

			if caption.Len() > 0 && chunk.Text != "" {
				caption.WriteString(" ")
			}
			caption.WriteString(chunk.Text)
			if h.coord != nil {
				captionSoFar := caption.String()
				h.coord.Transition(StateSpeaking, &transcript, &captionSoFar, &engine, nil)
			}

			if h.tts == nil {
				slog.Warn("no TTS client configured; dropping streamed sentence", "text", chunk.Text)
				return
			}
			var streamedTTSAny bool
			ttsTimeout := h.ttsTimeout
			if ttsTimeout <= 0 {
				ttsTimeout = 10 * time.Second
			}
			ttsCtx, stopTTS := newInactivityContext(ctx, ttsTimeout)
			defer stopTTS()
			t0 := time.Now()
			ttsErr := h.tts.Synthesize(ttsCtx, chunk.Text, func(tc TTSAudioChunk) error {
				if len(tc.Data) == 0 {
					return nil
				}
				ttsCtx.Reset()
				streamedTTSAny = true
				if err := streamer.push(tc, chunk.Text); err != nil {
					streamErr = err
					slog.Error("TTS stream chunk rejected", "error", streamErr)
					cancelStream()
					return streamErr
				}
				return nil
			})
			totalTTSDuration += time.Since(t0).Seconds()
			if errors.Is(ttsErr, ErrUnsupportedAudioFormat) && streamErr == nil {
				// DefaultTTSClient rejected the response itself.
				streamErr = fmt.Errorf("%w (sentence %q)", ttsErr, chunk.Text)
				cancelStream()
				return
			}
			if ttsErr != nil || !streamedTTSAny {
				slog.Warn("TTS synthesis for streamed sentence failed; dropping sentence", "text", chunk.Text, "error", ttsErr)
				return
			}
			slog.Debug("synthesized streamed sentence via TTS", "text", chunk.Text)
		}

		brainStart := time.Now()
		reply, err = streamingBrain.AskStreaming(streamCtx, askReq, streamOnStatus, onAudio)
		brainDuration, recordBrain := brainStageSeconds(turnBrain, time.Since(brainStart))
		stopThinking()
		if streamErr != nil {
			err = streamErr
		}
		if err != nil {
			finalStatus = "error"
			h.metrics.RecordStageDuration(nodeID, "brain", "error", brainDuration)
			errType := "brain_error"
			retErr := fmt.Errorf("%w: %w", ErrBrainFailed, err)
			if isTimeoutError(err, streamCtx, ctx) {
				errType = "brain_timeout"
				retErr = fmt.Errorf("%w: %w", ErrBrainTimeout, err)
			}
			h.metrics.RecordError(nodeID, "brain", errType)
			if h.coord != nil {
				h.coord.Transition(StateError, nil, nil, nil, nil)
			}
			if sinkErr := safeSink("error", map[string]string{"error": errType, "message": err.Error()}); sinkErr != nil {
				return sinkErr
			}
			return retErr
		}
		if recordBrain {
			h.metrics.RecordStageDuration(nodeID, "brain", "success", brainDuration)
		}
		h.recordChat(nodeID, chatlog.RoleAgent, "", reply)

		if streamedAny {
			// The brain streamed sentence audio, so the hub must NOT also
			// run its own TTS over the full reply (no double speech).
			if h.coord != nil {
				h.coord.Transition(StateSpeaking, &transcript, &reply, &engine, nil)
			}
			if streamer.anySent || len(streamer.pcmBuf) > 0 {
				if finishErr := streamer.finish(); finishErr != nil {
					return finishErr
				}

				h.metrics.RecordStageDuration(nodeID, "tts", "success", totalTTSDuration)
				if totalTTSDuration > 0.001 && streamChars > 0 {
					cps := float64(streamChars) / totalTTSDuration
					h.metrics.RecordTTSCPS(nodeID, engine, cps)
				}
			} else if h.tts != nil {
				// Every streamed sentence in remote-tts mode failed synthesis:
				// last resort is one TTS call over the full reply
				// so the reply is never silently dropped.
				status, ttsErr := h.synthesizeAndEmitReply(ctx, reply, nodeID, engine, &transcript, safeSink)
				if ttsErr != nil {
					return ttsErr
				}
				if status == "error" {
					finalStatus = "error"
				}
			}
			// Devil's Advocate catch: still emit reply so the kiosk caption
			// toast renders the reply text, even though (for streaming) it
			// arrives after the audio rather than before.
			if err := safeSink("reply", map[string]string{"reply": reply, "tts_engine": engine}); err != nil {
				return err
			}
		} else {
			if err := emitNonStreamingReply(reply); err != nil {
				return err
			}
		}
	} else {
		brainCtx, stopBrain := newInactivityContext(ctx, brainTimeout)
		defer stopBrain()
		startHeartbeat(brainCtx)

		unaryOnStatus := func(status string) {
			brainCtx.Reset()
			onStatus(status)
		}

		brainStart := time.Now()
		reply, err = turnBrain.Ask(brainCtx, askReq, unaryOnStatus)
		brainDuration, recordBrain := brainStageSeconds(turnBrain, time.Since(brainStart))
		stopThinking()
		if err != nil {
			finalStatus = "error"
			h.metrics.RecordStageDuration(nodeID, "brain", "error", brainDuration)
			errType := "brain_error"
			retErr := fmt.Errorf("%w: %w", ErrBrainFailed, err)
			if isTimeoutError(err, brainCtx, ctx) {
				errType = "brain_timeout"
				retErr = fmt.Errorf("%w: %w", ErrBrainTimeout, err)
			}
			h.metrics.RecordError(nodeID, "brain", errType)
			if h.coord != nil {
				h.coord.Transition(StateError, nil, nil, nil, nil)
			}
			if sinkErr := safeSink("error", map[string]string{"error": errType, "message": err.Error()}); sinkErr != nil {
				return sinkErr
			}
			return retErr
		}
		if recordBrain {
			h.metrics.RecordStageDuration(nodeID, "brain", "success", brainDuration)
		}
		h.recordChat(nodeID, chatlog.RoleAgent, "", reply)

		if err := emitNonStreamingReply(reply); err != nil {
			return err
		}
	}

	// Step 6: Complete interaction and return to idle
	durationMs := time.Since(start).Milliseconds()
	if err := safeSink("done", map[string]int64{"duration_ms": durationMs}); err != nil {
		return err
	}

	return nil
}

const pcmChunkFloor = 4800

type pcmChunkStreamer struct {
	sink          SSEEventSink
	ignoreSinkErr bool
	pcmBuf        []byte
	chunkIdx      int
	latched       bool
	rate          int
	channels      int
	anySent       bool
}

func (s *pcmChunkStreamer) emit(data []byte, isFinal bool) error {
	rate := s.rate
	if rate <= 0 {
		rate = 24000
	}
	channels := s.channels
	if channels <= 0 {
		channels = 1
	}
	payload := map[string]any{
		"chunk_index": s.chunkIdx,
		"format":      "pcm",
		"sample_rate": rate,
		"channels":    channels,
		"is_final":    isFinal,
		"data":        base64.StdEncoding.EncodeToString(data),
	}
	s.chunkIdx++
	if err := s.sink("audio_chunk", payload); err != nil {
		if s.ignoreSinkErr {
			slog.Debug("streamed audio_chunk sink error", "error", err)
			return nil
		}
		return err
	}
	return nil
}

func (s *pcmChunkStreamer) push(tc TTSAudioChunk, sentenceContext string) error {
	if len(tc.Data) == 0 {
		return nil
	}
	if tc.Format != "pcm" {
		if sentenceContext != "" {
			return fmt.Errorf("%w: TTS returned %q audio for sentence %q", ErrUnsupportedAudioFormat, tc.Format, sentenceContext)
		}
		return fmt.Errorf("%w: TTS returned %q audio", ErrUnsupportedAudioFormat, tc.Format)
	}
	if tc.SampleRate > 0 || tc.Channels > 0 {
		if !s.latched {
			s.latched = true
			s.rate = 24000
			s.channels = 1
			if tc.SampleRate > 0 {
				s.rate = tc.SampleRate
			}
			if tc.Channels > 0 {
				s.channels = tc.Channels
			}
		} else if (tc.SampleRate > 0 && tc.SampleRate != s.rate) || (tc.Channels > 0 && tc.Channels != s.channels) {
			return fmt.Errorf("%w: latched %d Hz/%d ch, got %d Hz/%d ch", ErrMixedPCMFormat, s.rate, s.channels, tc.SampleRate, tc.Channels)
		}
	}
	s.pcmBuf = append(s.pcmBuf, tc.Data...)
	for len(s.pcmBuf) >= pcmChunkFloor {
		piece := s.pcmBuf[:pcmChunkFloor]
		s.pcmBuf = s.pcmBuf[pcmChunkFloor:]
		s.anySent = true
		if err := s.emit(piece, false); err != nil {
			return err
		}
	}
	return nil
}

func (s *pcmChunkStreamer) finish() error {
	if len(s.pcmBuf) > 0 {
		s.anySent = true
		piece := s.pcmBuf
		s.pcmBuf = nil
		if err := s.emit(piece, false); err != nil {
			return err
		}
	}
	if s.anySent {
		if err := s.emit(nil, true); err != nil {
			return err
		}
	}
	return nil
}

// synthesizeAndEmitReply synthesizes speech for a complete reply (non-streaming brain or full-reply fallback),
// streaming audio chunks in real-time if h.tts implements StreamingTTSClient or falling back to unary Synthesize.
func (h *Hub) synthesizeAndEmitReply(
	ctx context.Context,
	reply string,
	nodeID string,
	engine string,
	transcript *string,
	safeSink SSEEventSink,
) (string, error) {
	if h.tts == nil || strings.TrimSpace(reply) == "" {
		return "success", nil
	}

	ttsTimeout := h.ttsTimeout
	if ttsTimeout <= 0 {
		ttsTimeout = 10 * time.Second
	}
	ttsCtx, stopTTS := newInactivityContext(ctx, ttsTimeout)
	defer stopTTS()

	ttsStart := time.Now()
	finalStatus := "success"

	streamer := &pcmChunkStreamer{
		sink:          safeSink,
		ignoreSinkErr: false,
	}

	ttsErr := h.tts.Synthesize(ttsCtx, reply, func(tc TTSAudioChunk) error {
		if len(tc.Data) == 0 {
			return nil
		}
		ttsCtx.Reset()
		if h.coord != nil && !streamer.anySent {
			h.coord.Transition(StateSpeaking, transcript, &reply, &engine, nil)
		}
		return streamer.push(tc, "")
	})

	if errors.Is(ttsErr, ErrMixedPCMFormat) || errors.Is(ttsErr, ErrUnsupportedAudioFormat) {
		// Fail fast: one response must keep one audio format (#347).
		slog.Error("TTS audio rejected mid-response", "node_id", nodeID, "error", ttsErr)
		h.metrics.RecordStageDuration(nodeID, "tts", "error", time.Since(ttsStart).Seconds())
		h.metrics.RecordError(nodeID, "tts", "tts_error")
		if h.coord != nil {
			h.coord.Transition(StateError, nil, nil, nil, nil)
		}
		if sinkErr := safeSink("error", map[string]string{"error": "tts_error", "message": ttsErr.Error()}); sinkErr != nil {
			return "error", sinkErr
		}
		return "error", ttsErr
	}

	if err := streamer.finish(); err != nil {
		return "error", err
	}

	ttsDuration := time.Since(ttsStart).Seconds()
	if ttsErr != nil && !streamer.anySent {
		slog.Warn("TTS synthesis failed", "node_id", nodeID, "error", ttsErr)
		finalStatus = "error"
		h.metrics.RecordStageDuration(nodeID, "tts", "error", ttsDuration)
		errType := "tts_error"
		if isTimeoutError(ttsErr, ttsCtx, ctx) {
			errType = "tts_timeout"
		}
		h.metrics.RecordError(nodeID, "tts", errType)
		if sinkErr := safeSink("error", map[string]string{"error": errType, "message": ttsErr.Error()}); sinkErr != nil {
			return "error", sinkErr
		}
	} else if ttsErr != nil && streamer.anySent {
		slog.Warn("TTS synthesis failed mid-stream", "node_id", nodeID, "error", ttsErr)
		h.metrics.RecordStageDuration(nodeID, "tts", "error", ttsDuration)
		errType := "tts_error"
		if isTimeoutError(ttsErr, ttsCtx, ctx) {
			errType = "tts_timeout"
		}
		h.metrics.RecordError(nodeID, "tts", errType)
		if h.coord != nil {
			h.coord.Transition(StateError, nil, nil, nil, nil)
		}
		if sinkErr := safeSink("error", map[string]string{"error": errType, "message": ttsErr.Error()}); sinkErr != nil {
			return "error", sinkErr
		}
		return "error", ttsErr
	} else {
		h.metrics.RecordStageDuration(nodeID, "tts", "success", ttsDuration)
		if ttsDuration > 0.001 && len(reply) > 0 {
			cps := float64(len(reply)) / ttsDuration
			h.metrics.RecordTTSCPS(nodeID, engine, cps)
		}
	}
	return finalStatus, nil
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

	if err := readHTTPError(resp, "stt"); err != nil {
		return "", err
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
// streams in (MANDOS, ). Same request as Ask — the only thing that
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

	if err := readHTTPError(resp, "brain"); err != nil {
		_ = resp.Body.Close()
		return nil, "", err
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
	return consumeStreamingSSE(r, onStatus, nil)
}

// sseScannerMaxLine is the max single SSE `data:` line consumeSSEStreaming
// will accept. Sentence events deliver text sentences inline.
const sseScannerMaxLine = 1024 * 1024

// consumeSSEStreaming is consumeSSE plus sentence-level events for low-latency
// playback. Sentence events deliver text as each sentence is generated, which
// the hub synthesizes via its configured TTS client.
func (b *DefaultBrainClient) consumeSSEStreaming(r io.Reader, onStatus func(status string), onAudio func(chunk BrainAudioChunk)) (string, error) {
	return consumeStreamingSSE(r, onStatus, onAudio)
}

// consumeStreamingSSE parses the /ask/stream-shaped SSE event set (status,
// sentence, reply, done). It is shared by the brain client and the fast-path
// client, which speak the same events.
func consumeStreamingSSE(r io.Reader, onStatus func(status string), onAudio func(chunk BrainAudioChunk)) (string, error) {
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
				if textVal, ok := payload["text"].(string); ok && strings.TrimSpace(textVal) != "" {
					onAudio(BrainAudioChunk{Text: textVal})
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

// Synthesize sends text to TTS and streams synthesized audio chunks via onChunk as they arrive.
func (t *DefaultTTSClient) Synthesize(ctx context.Context, text string, onChunk func(chunk TTSAudioChunk) error) error {
	if t.url == "" || strings.TrimSpace(text) == "" {
		return nil
	}

	payload, err := json.Marshal(map[string]string{
		"model":           t.model,
		"input":           text,
		"voice":           t.voice,
		"response_format": "wav",
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "audio/wav, audio/x-wav, audio/pcm;q=0.9, */*;q=0.1")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if err := readHTTPError(resp, "tts"); err != nil {
		return err
	}

	ct := strings.ToLower(resp.Header.Get("Content-Type"))

	// Walk the RIFF chunk list (fmt, then any others such as LIST, until
	// data) instead of assuming a fixed 44-byte header; the WAV is only a
	// wrapper around 16-bit PCM. Consumed bytes are retained so a raw PCM
	// response that is not RIFF is not truncated.
	var consumed bytes.Buffer
	info := parseWAVHeader(io.TeeReader(resp.Body, &consumed))

	if info.streamable {
		var body io.Reader = resp.Body
		// A streamed data chunk carries size 0 or 0xFFFFFFFF (unknown
		// length); a known size bounds the PCM so trailing chunks are not
		// played as audio.
		if info.dataSize > 0 && info.dataSize != 0xFFFFFFFF {
			body = io.LimitReader(resp.Body, int64(info.dataSize))
		}
		return streamPCM(body, "pcm", info.sampleRate, info.channels, onChunk)
	}

	if info.isRIFF {
		// A WAV wrapper whose payload is not 16-bit PCM.
		return fmt.Errorf("%w: WAV payload is not 16-bit PCM", ErrUnsupportedAudioFormat)
	}
	if strings.Contains(ct, "pcm") || strings.Contains(ct, "raw") {
		// Raw PCM has no container, so it can be sliced anywhere.
		rate, channels := ttsRateFromResponse(resp)
		body := io.MultiReader(bytes.NewReader(consumed.Bytes()), resp.Body)
		return streamPCM(body, "pcm", rate, channels, onChunk)
	}
	return fmt.Errorf("%w: TTS response content type %q", ErrUnsupportedAudioFormat, ct)
}

// streamPCM reads r and emits it as chunks of at most 4800 bytes. Each chunk
// owns its Data: the read buffer is reused, so the slice is copied before
// onChunk sees it and callbacks may retain it.
func streamPCM(r io.Reader, format string, sampleRate, channels int, onChunk func(chunk TTSAudioChunk) error) error {
	buf := make([]byte, 4800)
	for {
		nr, rErr := r.Read(buf)
		if nr > 0 {
			data := make([]byte, nr)
			copy(data, buf[:nr])
			if err := onChunk(TTSAudioChunk{Format: format, Data: data, SampleRate: sampleRate, Channels: channels}); err != nil {
				return err
			}
		}
		if rErr != nil {
			if errors.Is(rErr, io.EOF) {
				return nil
			}
			return rErr
		}
	}
}

// ttsRateFromResponse reads the PCM sample rate and channel count from the
// Content-Type parameters (audio/pcm;rate=22050;channels=1) or the
// X-Sample-Rate / X-Channels headers, defaulting to 24 kHz mono.
func ttsRateFromResponse(resp *http.Response) (int, int) {
	rate, channels := 24000, 1
	params := map[string]string{}
	if _, p, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err == nil {
		params = p
	}
	pick := func(param, header string) int {
		for _, s := range []string{params[param], resp.Header.Get(header)} {
			if v, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && v > 0 {
				return v
			}
		}
		return 0
	}
	if v := pick("rate", "X-Sample-Rate"); v > 0 {
		rate = v
	}
	if v := pick("channels", "X-Channels"); v > 0 {
		channels = v
	}
	return rate, channels
}

// wavInfo is the result of walking a RIFF/WAVE header.
type wavInfo struct {
	isRIFF     bool // starts with RIFF....WAVE
	haveFmt    bool // a fmt chunk was parsed (sampleRate/channels valid)
	streamable bool // 16-bit PCM and the data chunk was reached
	sampleRate int
	channels   int
	dataSize   uint32
}

const maxWAVHeaderSkip = 1 << 20

// parseWAVHeader consumes r up to and including the data chunk header,
// walking chunks (fmt, LIST, ...). It tolerates fmt sizes of 16/18/40,
// WAVE_FORMAT_EXTENSIBLE, and a data size of 0 or 0xFFFFFFFF.
func parseWAVHeader(r io.Reader) wavInfo {
	var info wavInfo
	var riff [12]byte
	if _, err := io.ReadFull(r, riff[:]); err != nil {
		return info
	}
	if string(riff[0:4]) != "RIFF" || string(riff[8:12]) != "WAVE" {
		return info
	}
	info.isRIFF = true

	var isPCM bool
	var bits int
	for {
		var ch [8]byte
		if _, err := io.ReadFull(r, ch[:]); err != nil {
			return info
		}
		id := string(ch[0:4])
		size := binary.LittleEndian.Uint32(ch[4:8])
		switch id {
		case "data":
			info.dataSize = size
			info.streamable = info.haveFmt && isPCM && bits == 16
			return info
		case "fmt ":
			if size < 16 || size > 4096 {
				return info
			}
			body := make([]byte, size)
			if _, err := io.ReadFull(r, body); err != nil {
				return info
			}
			if size%2 == 1 {
				if _, err := io.CopyN(io.Discard, r, 1); err != nil {
					return info
				}
			}
			tag := binary.LittleEndian.Uint16(body[0:2])
			if tag == 0xFFFE && size >= 26 {
				tag = binary.LittleEndian.Uint16(body[24:26])
			}
			isPCM = tag == 1
			info.channels = int(binary.LittleEndian.Uint16(body[2:4]))
			info.sampleRate = int(binary.LittleEndian.Uint32(body[4:8]))
			bits = int(binary.LittleEndian.Uint16(body[14:16]))
			if info.channels <= 0 {
				info.channels = 1
			}
			if info.sampleRate <= 0 {
				info.sampleRate = 24000
			}
			info.haveFmt = true
		default:
			skip := int64(size) + int64(size%2)
			if skip > maxWAVHeaderSkip {
				return info
			}
			if _, err := io.CopyN(io.Discard, r, skip); err != nil {
				return info
			}
		}
	}
}
