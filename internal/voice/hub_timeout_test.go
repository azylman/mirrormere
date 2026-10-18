package voice

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/config"
	"github.com/prometheus/client_golang/prometheus"
)

type funcBrain func(ctx context.Context, req AskRequest, onStatus func(status string)) (string, error)

func (f funcBrain) Ask(ctx context.Context, req AskRequest, onStatus func(status string)) (string, error) {
	return f(ctx, req, onStatus)
}

type funcStreamingBrain func(ctx context.Context, req AskRequest, onStatus func(string), onAudio func(BrainAudioChunk)) (string, error)

func (f funcStreamingBrain) Ask(ctx context.Context, req AskRequest, onStatus func(string)) (string, error) {
	return f(ctx, req, onStatus, nil)
}

func (f funcStreamingBrain) AskStreaming(ctx context.Context, req AskRequest, onStatus func(string), onAudio func(BrainAudioChunk)) (string, error) {
	return f(ctx, req, onStatus, onAudio)
}

func ttsFn(fn func(ctx context.Context, text string, onChunk func(TTSAudioChunk) error) error) *mockStreamingTTS {
	return &mockStreamingTTS{streamingFn: fn}
}

func mkBlockingBrain() BrainClient {
	return funcBrain(func(ctx context.Context, _ AskRequest, _ func(string)) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
}

func mkBlockingStreamBrain() BrainClient {
	return funcStreamingBrain(func(ctx context.Context, _ AskRequest, _ func(string), _ func(BrainAudioChunk)) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
}

func mkPeriodicStreamBrain(count int, interval time.Duration, reply string) BrainClient {
	return funcStreamingBrain(func(ctx context.Context, _ AskRequest, _ func(string), onAudio func(BrainAudioChunk)) (string, error) {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for i := 0; i < count; i++ {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-ticker.C:
				if onAudio != nil {
					onAudio(BrainAudioChunk{Text: fmt.Sprintf("part-%d", i)})
				}
			}
		}
		return reply, nil
	})
}

func mkStallStreamBrain() BrainClient {
	return funcStreamingBrain(func(ctx context.Context, _ AskRequest, _ func(string), onAudio func(BrainAudioChunk)) (string, error) {
		if onAudio != nil {
			onAudio(BrainAudioChunk{Text: "start chunk"})
		}
		<-ctx.Done()
		return "", ctx.Err()
	})
}

func mkStatusResetStreamBrain(delay time.Duration, status, reply string) BrainClient {
	return funcStreamingBrain(func(ctx context.Context, _ AskRequest, onStatus func(string), onAudio func(BrainAudioChunk)) (string, error) {
		t1 := time.NewTimer(delay)
		defer t1.Stop()
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-t1.C:
		}
		if onStatus != nil {
			onStatus(status)
		}
		t2 := time.NewTimer(delay)
		defer t2.Stop()
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-t2.C:
		}
		if onAudio != nil {
			onAudio(BrainAudioChunk{Text: reply})
		}
		return reply, nil
	})
}

func heartbeatBrain(streaming bool, ch chan struct{}) BrainClient {
	if streaming {
		return funcStreamingBrain(func(ctx context.Context, _ AskRequest, _ func(string), onAudio func(BrainAudioChunk)) (string, error) {
			select {
			case <-ch:
				if onAudio != nil {
					onAudio(BrainAudioChunk{Text: "chunk"})
				}
				return "stream done", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		})
	}
	return funcBrain(func(ctx context.Context, _ AskRequest, _ func(string)) (string, error) {
		select {
		case <-ch:
			return "done", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	})
}

func mkBlockingTTS() TTSClient {
	return ttsFn(func(ctx context.Context, _ string, _ func(TTSAudioChunk) error) error {
		<-ctx.Done()
		return ctx.Err()
	})
}

func mkMidStreamFailTTS(failErr error) TTSClient {
	return ttsFn(func(_ context.Context, _ string, onChunk func(TTSAudioChunk) error) error {
		_ = onChunk(TTSAudioChunk{Data: nil})
		if err := onChunk(TTSAudioChunk{Data: make([]byte, 4800), Format: "pcm", SampleRate: 24000, Channels: 1}); err != nil {
			return err
		}
		return failErr
	})
}

func mkPeriodicTTS(count int, interval time.Duration) TTSClient {
	return ttsFn(func(ctx context.Context, _ string, onChunk func(TTSAudioChunk) error) error {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for i := 0; i < count; i++ {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
				if err := onChunk(TTSAudioChunk{Data: make([]byte, 4800), Format: "pcm", SampleRate: 24000, Channels: 1}); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func mkStallingTTS() TTSClient {
	return ttsFn(func(ctx context.Context, _ string, onChunk func(TTSAudioChunk) error) error {
		if err := onChunk(TTSAudioChunk{Data: make([]byte, 4800), Format: "pcm", SampleRate: 24000, Channels: 1}); err != nil {
			return err
		}
		<-ctx.Done()
		return ctx.Err()
	})
}

func TestHub_ThinkingHeartbeat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                string
		streaming           bool
		failThinkingSink    bool
		wantAudioChunk      bool
		wantPositiveElapsed bool
	}{
		{"unary", false, false, false, true},
		{"streaming", true, false, true, false},
		{"sink_error", false, true, false, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			heartbeatSeen := make(chan struct{}, 1)
			brain := heartbeatBrain(tc.streaming, heartbeatSeen)

			var mu sync.Mutex
			var events []recordedEvent
			var thinkingReceived bool

			sink := func(event string, data any) error {
				mu.Lock()
				defer mu.Unlock()
				events = append(events, recordedEvent{Event: event, Data: data})
				if event == "thinking" {
					if !thinkingReceived {
						thinkingReceived = true
						select {
						case heartbeatSeen <- struct{}{}:
						default:
						}
					}
					if tc.failThinkingSink {
						return errors.New("client socket closed")
					}
				}
				return nil
			}

			h := NewHub(
				&config.VoiceHubConfig{Enabled: true},
				nil,
				WithSTTClient(&mockSTT{text: "hi"}),
				WithBrainClient(brain),
				WithTTSClient(&mockTTS{audio: makeValidWAV(1600), format: "pcm"}),
				WithThinkingHeartbeatInterval(10*time.Millisecond),
				WithBrainTimeout(2*time.Second),
			)

			err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(1600)), "node-1", "s-1", sink, EdgeTimings{})
			if err != nil {
				t.Fatalf("expected nil error, got %v", err)
			}

			mu.Lock()
			defer mu.Unlock()

			var hadThinking, hadAudioChunk, thinkingAfterAudio bool
			for _, ev := range events {
				if ev.Event == "thinking" {
					hadThinking = true
					if hadAudioChunk {
						thinkingAfterAudio = true
					}
					if tc.wantPositiveElapsed {
						if m, ok := ev.Data.(map[string]int); ok {
							if m["elapsed_seconds"] <= 0 {
								t.Errorf("expected positive elapsed_seconds, got %v", m["elapsed_seconds"])
							}
						}
					}
				}
				if ev.Event == "audio_chunk" {
					hadAudioChunk = true
				}
			}

			if !hadThinking {
				t.Error("expected thinking event")
			}
			if tc.wantAudioChunk && !hadAudioChunk {
				t.Error("expected audio_chunk event")
			}
			if thinkingAfterAudio {
				t.Error("thinking event emitted after audio streaming began")
			}
		})
	}
}

func TestHub_BrainTimeouts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		brain            BrainClient
		timeout          time.Duration
		cancelOnStatus   bool
		wantErr          error
		wantCoordErr     bool
		wantMetric       bool
		wantTimeoutEvent bool
		wantReply        string
	}{
		{"unary_timeout", mkBlockingBrain(), 20 * time.Millisecond, false, ErrBrainTimeout, true, true, true, ""},
		{"streaming_timeout", mkBlockingStreamBrain(), 20 * time.Millisecond, false, ErrBrainTimeout, false, false, true, ""},
		{"client_cancellation", &mockBrain{statuses: []string{"thinking..."}}, 5 * time.Second, true, nil, false, false, false, ""},
		{"streaming_long_reply", mkPeriodicStreamBrain(6, 10*time.Millisecond, "full reply text"), 25 * time.Millisecond, false, nil, false, false, false, "full reply text"},
		{"streaming_stall", mkStallStreamBrain(), 20 * time.Millisecond, false, ErrBrainTimeout, false, false, true, ""},
		{"status_reset", mkStatusResetStreamBrain(15*time.Millisecond, "tool executing: web search", "done answer"), 25 * time.Millisecond, false, nil, false, false, false, "done answer"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			reg := prometheus.NewRegistry()
			m := NewMetrics(reg)
			coord := NewCoordinator(nil)

			sink, getEvents := collectEvents()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			var cancelOnce sync.Once
			wrappedSink := func(event string, data any) error {
				if tc.cancelOnStatus && event == "status" {
					cancelOnce.Do(cancel)
				}
				return sink(event, data)
			}

			h := NewHub(
				&config.VoiceHubConfig{Enabled: true},
				coord,
				WithSTTClient(&mockSTT{text: "hello"}),
				WithBrainClient(tc.brain),
				WithMetrics(m),
				WithBrainTimeout(tc.timeout),
			)

			err := h.Interact(ctx, bytes.NewReader(makeValidWAV(1600)), "node-brain", "s-brain", wrappedSink, EdgeTimings{})
			if tc.wantErr != nil {
				if err == nil || !errors.Is(err, tc.wantErr) {
					t.Fatalf("expected error wrapping %v, got %v", tc.wantErr, err)
				}
				if !errors.Is(err, ErrBrainFailed) {
					t.Errorf("expected error wrapping ErrBrainFailed, got %v", err)
				}
			} else if !tc.cancelOnStatus && err != nil {
				t.Fatalf("expected nil error, got %v", err)
			}

			if tc.wantTimeoutEvent && !hasErrorCode(getEvents(), "brain_timeout") {
				t.Errorf("expected brain_timeout event, got %v", eventNames(getEvents()))
			}
			if !tc.wantTimeoutEvent && hasErrorCode(getEvents(), "brain_timeout") {
				t.Errorf("unexpected brain_timeout event, got %v", eventNames(getEvents()))
			}

			if tc.wantCoordErr && coord.GetState().State != StateError {
				t.Errorf("expected coord StateError, got %q", coord.GetState().State)
			}

			if tc.wantMetric {
				val, ok := metricVal(t, reg, "mirrormere_voice_errors_total", lbl("stage", "brain", "error_type", "brain_timeout"))
				if !ok || val != 1 {
					t.Errorf("expected brain_timeout metric 1, got %v (ok=%v)", val, ok)
				}
			}

			if tc.wantReply != "" {
				var foundReply bool
				for _, ev := range getEvents() {
					if ev.Event == "reply" {
						if m, ok := ev.Data.(map[string]string); ok && m["reply"] == tc.wantReply {
							foundReply = true
						}
					}
				}
				if !foundReply {
					t.Errorf("expected reply event with %q, got %v", tc.wantReply, getEvents())
				}
			}
		})
	}
}

func TestHub_TTSTimeouts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		tts           TTSClient
		timeout       time.Duration
		wantStatus    string
		wantErr       bool
		wantEventCode string
		wantCoordErr  bool
		wantMetric    bool
	}{
		{"initial_timeout_graceful", mkBlockingTTS(), 20 * time.Millisecond, "error", false, "tts_timeout", false, true},
		{"mid_stream_timeout", mkMidStreamFailTTS(context.DeadlineExceeded), 50 * time.Millisecond, "error", true, "tts_timeout", true, false},
		{"mid_stream_generic_error", mkMidStreamFailTTS(errors.New("i/o error")), -1 * time.Second, "error", true, "tts_error", false, false},
		{"long_reply_inactivity", mkPeriodicTTS(5, 10*time.Millisecond), 25 * time.Millisecond, "success", false, "", false, false},
		{"mid_stream_stall", mkStallingTTS(), 20 * time.Millisecond, "error", true, "tts_timeout", false, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			reg := prometheus.NewRegistry()
			m := NewMetrics(reg)
			coord := NewCoordinator(nil)

			sink, getEvents := collectEvents()

			opts := []HubOption{
				WithTTSClient(tc.tts),
				WithMetrics(m),
				WithTTSTimeout(tc.timeout),
			}

			h := NewHub(&config.VoiceHubConfig{Enabled: true}, coord, opts...)

			tr := "transcript"
			status, err := h.synthesizeAndEmitReply(context.Background(), "reply text", "node-tts", "kokoro", &tr, sink)
			if status != tc.wantStatus {
				t.Fatalf("expected status %q, got %q", tc.wantStatus, status)
			}
			if tc.wantErr && err == nil {
				t.Fatalf("expected non-nil error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected nil error, got %v", err)
			}

			if tc.wantEventCode != "" && !hasErrorCode(getEvents(), tc.wantEventCode) {
				t.Errorf("expected %s event, got %v", tc.wantEventCode, eventNames(getEvents()))
			}
			if tc.wantEventCode == "" && hasErrorCode(getEvents(), "tts_timeout") {
				t.Errorf("unexpected tts_timeout event, got %v", eventNames(getEvents()))
			}

			if tc.wantCoordErr && coord.GetState().State != StateError {
				t.Errorf("expected coord StateError, got %q", coord.GetState().State)
			}

			if tc.wantMetric {
				val, ok := metricVal(t, reg, "mirrormere_voice_errors_total", lbl("stage", "tts", "error_type", "tts_timeout"))
				if !ok || val != 1 {
					t.Errorf("expected tts_timeout metric 1, got %v (ok=%v)", val, ok)
				}
			}
		})
	}
}

func TestInactivityContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		timeout      time.Duration
		cancelParent bool
		triggerStop  bool
		resetDelay   time.Duration
		wantDoneErr  error
	}{
		{"expires_on_timeout", 50 * time.Millisecond, false, false, 0, context.DeadlineExceeded},
		{"reset_extends_deadline", 80 * time.Millisecond, false, false, 45 * time.Millisecond, context.DeadlineExceeded},
		{"parent_cancellation", 200 * time.Millisecond, true, false, 0, context.Canceled},
		{"stop_cancels_context", 500 * time.Millisecond, false, true, 0, context.Canceled},
		{"zero_duration", 0, false, true, 0, context.Canceled},
		{"negative_duration", -1 * time.Second, false, true, 0, context.Canceled},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			parent := context.Background()
			var cancelParent context.CancelFunc
			if tc.cancelParent {
				parent, cancelParent = context.WithCancel(parent)
				defer cancelParent()
			}
			ctx, stop := newInactivityContext(parent, tc.timeout)
			defer stop()

			if tc.resetDelay > 0 {
				t1 := time.NewTimer(tc.resetDelay)
				select {
				case <-ctx.Done():
					t.Fatalf("premature cancel: %v", ctx.Err())
				case <-t1.C:
				}
				ctx.Reset()

				t2 := time.NewTimer(tc.resetDelay)
				select {
				case <-ctx.Done():
					t.Fatalf("context canceled prematurely after reset: %v", ctx.Err())
				case <-t2.C:
				}
			}

			if tc.cancelParent {
				cancelParent()
			}
			if tc.triggerStop {
				if tc.timeout <= 0 {
					tZero := time.NewTimer(30 * time.Millisecond)
					select {
					case <-ctx.Done():
						t.Fatalf("zero duration context prematurely canceled: %v", ctx.Err())
					case <-tZero.C:
					}
					ctx.Reset()
				}
				stop()
			}

			timer := time.NewTimer(500 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				if !errors.Is(ctx.Err(), tc.wantDoneErr) {
					t.Fatalf("expected %v, got %v", tc.wantDoneErr, ctx.Err())
				}
				ctx.Reset()
			case <-timer.C:
				t.Fatal("timed out waiting for context completion")
			}
		})
	}
}

func TestHub_OptionsAndConfigDefaults(t *testing.T) {
	t.Parallel()

	timeoutBrain := 45
	timeoutTTS := 12
	cfg := &config.VoiceHubConfig{
		Enabled:             true,
		BrainTimeoutSeconds: &timeoutBrain,
		TTSTimeoutSeconds:   &timeoutTTS,
	}

	h := NewHub(cfg, nil)
	if h.brainTimeout != 45*time.Second {
		t.Errorf("expected brain timeout 45s, got %v", h.brainTimeout)
	}
	if h.ttsTimeout != 12*time.Second {
		t.Errorf("expected tts timeout 12s, got %v", h.ttsTimeout)
	}
	if h.heartbeatInterval != 5*time.Second {
		t.Errorf("expected default heartbeat interval 5s, got %v", h.heartbeatInterval)
	}

	h2 := NewHub(
		cfg,
		nil,
		WithBrainTimeout(15*time.Second),
		WithTTSTimeout(5*time.Second),
		WithThinkingHeartbeatInterval(2*time.Second),
	)
	if h2.brainTimeout != 15*time.Second {
		t.Errorf("expected overridden brain timeout 15s, got %v", h2.brainTimeout)
	}
	if h2.ttsTimeout != 5*time.Second {
		t.Errorf("expected overridden tts timeout 5s, got %v", h2.ttsTimeout)
	}
	if h2.heartbeatInterval != 2*time.Second {
		t.Errorf("expected overridden heartbeat interval 2s, got %v", h2.heartbeatInterval)
	}
}

func TestIsTimeoutError(t *testing.T) {
	t.Parallel()

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	deadlineCtx, cancelDeadline := context.WithTimeout(context.Background(), time.Nanosecond)
	t.Cleanup(cancelDeadline)
	<-deadlineCtx.Done()

	validCtx := context.Background()

	tests := []struct {
		name       string
		err        error
		timeoutCtx context.Context
		parentCtx  context.Context
		expected   bool
	}{
		{"parent_canceled_returns_false", context.DeadlineExceeded, deadlineCtx, canceledCtx, false},
		{"err_is_deadline_exceeded", context.DeadlineExceeded, nil, validCtx, true},
		{"timeout_ctx_is_deadline_exceeded", errors.New("net down"), deadlineCtx, validCtx, true},
		{"general_error_returns_false", errors.New("err"), validCtx, validCtx, false},
		{"nil_err_returns_false", nil, validCtx, validCtx, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isTimeoutError(tc.err, tc.timeoutCtx, tc.parentCtx); got != tc.expected {
				t.Errorf("isTimeoutError() = %v, expected %v", got, tc.expected)
			}
		})
	}
}
