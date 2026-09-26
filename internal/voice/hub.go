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

// SSEEventSink is a callback to emit an SSE event to the client stream.
type SSEEventSink func(event string, data any) error

// STTClient abstracts speech-to-text inference.
type STTClient interface {
	Transcribe(ctx context.Context, wavData []byte) (string, error)
}

// BrainClient abstracts agent deliberation and live tool status streaming.
type BrainClient interface {
	Ask(ctx context.Context, prompt string, sessionID string, onStatus func(status string)) (string, error)
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

	// Step 2: Speech-to-Text transcription
	if h.stt == nil {
		return ErrSTTFailed
	}
	transcript, err := h.stt.Transcribe(ctx, wavData)
	if err != nil {
		if h.coord != nil {
			h.coord.Transition(StateError, nil, nil, nil, nil)
		}
		if sinkErr := safeSink("error", map[string]string{"error": "stt_error", "message": err.Error()}); sinkErr != nil {
			return sinkErr
		}
		return fmt.Errorf("%w: %w", ErrSTTFailed, err)
	}

	// Step 3: Transition to thinking with recognized transcript
	if h.coord != nil {
		h.coord.Transition(StateThinking, &transcript, nil, nil, nil)
	}
	if err := safeSink("transcript", map[string]string{"transcript": transcript}); err != nil {
		return err
	}

	// Step 4: Brain deliberation with live tool status forwarding
	if h.brain == nil {
		return ErrBrainFailed
	}
	reply, err := h.brain.Ask(ctx, transcript, sessionID, func(status string) {
		if h.coord != nil {
			h.coord.SetStatus(&status)
		}
		if sinkErr := safeSink("status", map[string]string{"status": status}); sinkErr != nil {
			// Status stream dropped by client; deliberate gracefully
			slog.Debug("status sink error", "error", sinkErr)
		}
	})
	if err != nil {
		if h.coord != nil {
			h.coord.Transition(StateError, nil, nil, nil, nil)
		}
		if sinkErr := safeSink("error", map[string]string{"error": "brain_error", "message": err.Error()}); sinkErr != nil {
			return sinkErr
		}
		return fmt.Errorf("%w: %w", ErrBrainFailed, err)
	}

	engine := h.cfg.GetTTSModel()
	// Devil's Advocate catch: Always emit reply event so kiosk caption toast renders reply text,
	// even if TTS synthesis subsequently fails!
	if err := safeSink("reply", map[string]string{"reply": reply, "tts_engine": engine}); err != nil {
		return err
	}

	// Step 5: Speech synthesis (TTS)
	if h.tts != nil {
		audioBytes, format, ttsErr := h.tts.Synthesize(ctx, reply)
		if ttsErr != nil {
			// Graceful degradation: Log/emit error for TTS but don't drop the interaction reply
			if sinkErr := safeSink("error", map[string]string{"error": "tts_error", "message": ttsErr.Error()}); sinkErr != nil {
				return sinkErr
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
				return err
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
	buf.WriteString("{\"type\":\"audio-start\",\"data\":{\"rate\":16000,\"width\":2,\"channels\":1}}\n")
	fmt.Fprintf(&buf, "{\"type\":\"audio-chunk\",\"data\":{\"rate\":16000,\"width\":2,\"channels\":1},\"payload_length\":%d}\n", len(pcm))
	buf.Write(pcm)
	buf.WriteString("{\"type\":\"audio-stop\"}\n")

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
func (b *DefaultBrainClient) Ask(ctx context.Context, prompt string, sessionID string, onStatus func(status string)) (string, error) {
	if b.url == "" {
		return fmt.Sprintf("I heard: %s", prompt), nil
	}

	reqBody, err := json.Marshal(map[string]any{
		"prompt":     prompt,
		"session_id": sessionID,
		"effort":     "low",
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
