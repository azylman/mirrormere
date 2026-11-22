package voice

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/azylman/mirrormere/internal/config"
)

// timeoutFakeClock is a manual clock: AfterFunc callbacks run synchronously
// inside Advance, in due order, with Now() set to each callback's due time.
type timeoutFakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*timeoutFakeTimer
}

type timeoutFakeTimer struct {
	clk  *timeoutFakeClock
	when time.Time
	f    func()
}

func newTimeoutFakeClock() *timeoutFakeClock {
	return &timeoutFakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *timeoutFakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *timeoutFakeClock) AfterFunc(d time.Duration, f func()) stopper {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &timeoutFakeTimer{clk: c, when: c.now.Add(d), f: f}
	c.timers = append(c.timers, t)
	return t
}

func (t *timeoutFakeTimer) Stop() bool {
	t.clk.mu.Lock()
	defer t.clk.mu.Unlock()
	i := slices.Index(t.clk.timers, t)
	if i < 0 {
		return false
	}
	t.clk.timers = slices.Delete(t.clk.timers, i, i+1)
	return true
}

// Advance moves the clock forward by d, firing every timer due by then.
func (c *timeoutFakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	end := c.now.Add(d)
	for {
		next := -1
		for i, t := range c.timers {
			if !t.when.After(end) && (next < 0 || t.when.Before(c.timers[next].when)) {
				next = i
			}
		}
		if next < 0 {
			break
		}
		t := c.timers[next]
		c.timers = slices.Delete(c.timers, next, next+1)
		if t.when.After(c.now) {
			c.now = t.when
		}
		c.mu.Unlock()
		t.f()
		c.mu.Lock()
	}
	c.now = end
	c.mu.Unlock()
}

type funcBrain func(context.Context, AskRequest, func(string)) (string, error)

func (f funcBrain) Ask(ctx context.Context, r AskRequest, s func(string)) (string, error) {
	return f(ctx, r, s)
}

type funcStreamingBrain func(context.Context, AskRequest, func(string), func(BrainAudioChunk)) (string, error)

func (f funcStreamingBrain) Ask(ctx context.Context, r AskRequest, s func(string)) (string, error) {
	return f(ctx, r, s, nil)
}

func (f funcStreamingBrain) AskStreaming(ctx context.Context, r AskRequest, s func(string), a func(BrainAudioChunk)) (string, error) {
	return f(ctx, r, s, a)
}

var pcm4800 = TTSAudioChunk{Data: make([]byte, 4800), Format: "pcm", SampleRate: 24000, Channels: 1}

// timeoutBrainScript is a declarative brain that advances the fake clock:
// status (if set) after delay, then steps audio chunks each after delay, then
// idle, then the reply. Any clock advance that expires ctx returns ctx.Err().
type timeoutBrainScript struct {
	streaming   bool
	status      string
	steps       int
	delay, idle time.Duration
	reply       string
}

func (s timeoutBrainScript) client(clk *timeoutFakeClock) BrainClient {
	run := funcStreamingBrain(func(ctx context.Context, _ AskRequest, onStatus func(string), onAudio func(BrainAudioChunk)) (string, error) {
		if s.status != "" {
			clk.Advance(s.delay)
			if err := ctx.Err(); err != nil {
				return "", err
			}
			onStatus(s.status)
		}
		for i := range s.steps {
			clk.Advance(s.delay)
			if err := ctx.Err(); err != nil {
				return "", err
			}
			if onAudio != nil {
				onAudio(BrainAudioChunk{Text: fmt.Sprintf("part-%d", i)})
			}
		}
		clk.Advance(s.idle)
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return s.reply, nil
	})
	if s.streaming {
		return run
	}
	return funcBrain(func(ctx context.Context, r AskRequest, onStatus func(string)) (string, error) {
		return run(ctx, r, onStatus, nil)
	})
}

// timeoutTTSScript is a declarative TTS that advances the fake clock: one
// empty chunk, chunks PCM chunks each after delay, then idle, then finalErr.
type timeoutTTSScript struct {
	chunks      int
	delay, idle time.Duration
	finalErr    error
}

func (s timeoutTTSScript) client(clk *timeoutFakeClock) TTSClient {
	return mkStreamTTS(func(ctx context.Context, _ string, onChunk func(TTSAudioChunk) error) error {
		_ = onChunk(TTSAudioChunk{})
		for range s.chunks {
			clk.Advance(s.delay)
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := onChunk(pcm4800); err != nil {
				return err
			}
		}
		clk.Advance(s.idle)
		if err := ctx.Err(); err != nil {
			return err
		}
		return s.finalErr
	})
}

// heartbeatEventLog renders thinking and audio_chunk events in order.
func heartbeatEventLog(evs []recordedEvent) []string {
	var out []string
	for _, ev := range evs {
		switch ev.Event {
		case "thinking":
			out = append(out, fmt.Sprintf("thinking %v", ev.Data))
		case "audio_chunk":
			out = append(out, "audio_chunk")
		}
	}
	return out
}

func TestHub_ThinkingHeartbeat(t *testing.T) {
	t.Parallel()
	tb, tt := 45, 12
	cfg := &config.VoiceHubConfig{Enabled: true, BrainTimeoutSeconds: &tb, TTSTimeoutSeconds: &tt}
	if h0 := NewHub(cfg, nil); h0.brainTimeout != 45*time.Second || h0.ttsTimeout != 12*time.Second || h0.heartbeatInterval != 5*time.Second {
		t.Fatalf("unexpected config defaults: %+v", h0)
	}
	tests := []struct {
		name             string
		brain            timeoutBrainScript
		failThinkingSink bool
		want             []string
	}{
		{"unary", timeoutBrainScript{idle: 5 * time.Second, reply: "done"}, false, []string{"thinking map[elapsed_seconds:2]", "thinking map[elapsed_seconds:4]", "audio_chunk", "audio_chunk"}},
		{"streaming", timeoutBrainScript{streaming: true, steps: 1, delay: 4 * time.Second, idle: 4 * time.Second, reply: "done"}, false, []string{"thinking map[elapsed_seconds:2]", "thinking map[elapsed_seconds:4]", "audio_chunk", "audio_chunk"}},
		{"sink_error", timeoutBrainScript{idle: 5 * time.Second, reply: "done"}, true, []string{"thinking map[elapsed_seconds:2]", "audio_chunk", "audio_chunk"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			clk := newTimeoutFakeClock()
			sink, getEvents := collectEvents()
			h := NewHub(&config.VoiceHubConfig{Enabled: true}, nil, WithClock(clk), WithSTTClient(&mockSTT{text: "hi"}), WithBrainClient(tc.brain.client(clk)), WithTTSClient(&mockTTS{audio: makeValidWAV(1600), format: "pcm"}), WithThinkingHeartbeatInterval(2*time.Second))
			err := h.Interact(context.Background(), bytes.NewReader(makeValidWAV(1600)), "node-1", "s-1", func(event string, data any) error {
				_ = sink(event, data)
				if event == "thinking" && tc.failThinkingSink {
					return errors.New("client socket closed")
				}
				return nil
			}, EdgeTimings{})
			if got := heartbeatEventLog(getEvents()); err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Errorf("err=%v events=%q, want nil and %q", err, got, tc.want)
			}
		})
	}
}

func TestHub_BrainTimeouts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		brain            timeoutBrainScript
		timeout          time.Duration
		cancelOnStatus   bool
		wantErr          error
		wantState        string
		wantMetric       bool
		wantTimeoutEvent bool
		wantReply        string
	}{
		{"unary_timeout", timeoutBrainScript{idle: 20 * time.Millisecond}, 20 * time.Millisecond, false, ErrBrainTimeout, StateError, true, true, ""},
		{"unary_one_tick_short", timeoutBrainScript{idle: 20*time.Millisecond - 1, reply: "in time"}, 20 * time.Millisecond, false, nil, StateSpeaking, false, false, "in time"},
		{"streaming_timeout", timeoutBrainScript{streaming: true, idle: 20 * time.Millisecond}, 20 * time.Millisecond, false, ErrBrainTimeout, StateError, true, true, ""},
		{"client_cancellation", timeoutBrainScript{status: "thinking...", reply: "late"}, 5 * time.Second, true, context.Canceled, StateError, false, false, ""},
		{"streaming_long_reply", timeoutBrainScript{streaming: true, steps: 30, delay: 10 * time.Millisecond, reply: "full reply text"}, 250 * time.Millisecond, false, nil, StateSpeaking, false, false, "full reply text"},
		{"streaming_stall", timeoutBrainScript{streaming: true, steps: 1, idle: 20 * time.Millisecond}, 20 * time.Millisecond, false, ErrBrainTimeout, StateError, true, true, ""},
		{"status_reset", timeoutBrainScript{streaming: true, status: "tool executing: web search", steps: 20, delay: 15 * time.Millisecond, reply: "done answer"}, 250 * time.Millisecond, false, nil, StateSpeaking, false, false, "done answer"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			clk := newTimeoutFakeClock()
			reg := prometheus.NewRegistry()
			coord := NewCoordinator(nil)
			sink, getEvents := collectEvents()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			h := NewHub(&config.VoiceHubConfig{Enabled: true}, coord, WithClock(clk), WithSTTClient(&mockSTT{text: "hello"}), WithBrainClient(tc.brain.client(clk)), WithMetrics(NewMetrics(reg)), WithBrainTimeout(tc.timeout), WithThinkingHeartbeatInterval(tc.timeout))
			err := h.Interact(ctx, bytes.NewReader(makeValidWAV(1600)), "node-brain", "s-brain", func(event string, data any) error {
				if tc.cancelOnStatus && event == "status" {
					cancel()
				}
				return sink(event, data)
			}, EdgeTimings{})
			if (tc.wantErr == nil) != (err == nil) || (tc.wantErr != nil && (!errors.Is(err, tc.wantErr) || !errors.Is(err, ErrBrainFailed))) {
				t.Fatalf("err=%v, want %v wrapped with ErrBrainFailed", err, tc.wantErr)
			}
			evs := getEvents()
			var gotReply string
			for _, ev := range evs {
				if m, ok := ev.Data.(map[string]string); ok && ev.Event == "reply" {
					gotReply = m["reply"]
				}
			}
			val, _ := metricVal(t, reg, "mirrormere_voice_errors_total", lbl("stage", "brain", "error_type", "brain_timeout"))
			// The heartbeat shares the brain deadline: on timeout rows both fire at
			// the same instant, the deadline first, so no "thinking" may follow it.
			if slices.Contains(eventNames(evs), "thinking") {
				t.Errorf("thinking emitted after the brain context ended: %v", eventNames(evs))
			}
			if got := coord.GetState().State; got != tc.wantState || hasErrorCode(evs, "brain_timeout") != tc.wantTimeoutEvent || (val == 1) != tc.wantMetric || gotReply != tc.wantReply {
				t.Errorf("state=%q timeoutEvent=%v metric=%v reply=%q, want %q %v %v %q", got, hasErrorCode(evs, "brain_timeout"), val, gotReply, tc.wantState, tc.wantTimeoutEvent, tc.wantMetric, tc.wantReply)
			}
		})
	}
}

func TestHub_TTSTimeouts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		tts           timeoutTTSScript
		timeout       time.Duration
		wantStatus    string
		wantErr       bool
		wantEventCode string
		wantState     string
		wantMetric    bool
	}{
		{"initial_timeout_graceful", timeoutTTSScript{idle: 20 * time.Millisecond}, 20 * time.Millisecond, "error", false, "tts_timeout", StateIdle, true},
		{"initial_one_tick_short", timeoutTTSScript{idle: 20*time.Millisecond - 1}, 20 * time.Millisecond, "success", false, "", StateIdle, false},
		{"mid_stream_timeout", timeoutTTSScript{chunks: 1, finalErr: context.DeadlineExceeded}, 50 * time.Millisecond, "error", true, "tts_timeout", StateError, true},
		{"mid_stream_generic_error", timeoutTTSScript{chunks: 1, finalErr: errors.New("i/o error")}, -1 * time.Second, "error", true, "tts_error", StateError, false},
		{"long_reply_inactivity", timeoutTTSScript{chunks: 25, delay: 12 * time.Millisecond}, 250 * time.Millisecond, "success", false, "", StateSpeaking, false},
		{"mid_stream_stall", timeoutTTSScript{chunks: 1, idle: 20 * time.Millisecond}, 20 * time.Millisecond, "error", true, "tts_timeout", StateError, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			clk := newTimeoutFakeClock()
			reg := prometheus.NewRegistry()
			coord := NewCoordinator(nil)
			sink, getEvents := collectEvents()
			h := NewHub(&config.VoiceHubConfig{Enabled: true}, coord, WithClock(clk), WithTTSClient(tc.tts.client(clk)), WithMetrics(NewMetrics(reg)), WithTTSTimeout(tc.timeout))
			tr := "transcript"
			status, err := h.synthesizeAndEmitReply(context.Background(), "reply text", "node-tts", "kokoro", &tr, sink)
			if status != tc.wantStatus || (err != nil) != tc.wantErr {
				t.Fatalf("status=%q err=%v, want status=%q wantErr=%v", status, err, tc.wantStatus, tc.wantErr)
			}
			evs := getEvents()
			gotCode := ""
			for _, code := range []string{"tts_timeout", "tts_error"} {
				if hasErrorCode(evs, code) {
					gotCode = code
				}
			}
			val, _ := metricVal(t, reg, "mirrormere_voice_errors_total", lbl("stage", "tts", "error_type", "tts_timeout"))
			if got := coord.GetState().State; gotCode != tc.wantEventCode || got != tc.wantState || (val == 1) != tc.wantMetric {
				t.Errorf("code=%q state=%q metric=%v, want %q %q %v", gotCode, got, val, tc.wantEventCode, tc.wantState, tc.wantMetric)
			}
		})
	}
}

func TestInactivityContext(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                string
		timeout, resetAfter time.Duration
		cancelParent, stop  bool
		advance             time.Duration
		wantErr             error
	}{
		{"expires_on_timeout", 50 * time.Millisecond, 0, false, false, 50 * time.Millisecond, context.DeadlineExceeded},
		{"live_one_tick_before_timeout", 50 * time.Millisecond, 0, false, false, 50*time.Millisecond - 1, nil},
		{"reset_extends_deadline", 400 * time.Millisecond, 220 * time.Millisecond, false, false, 400*time.Millisecond - 1, nil},
		{"reset_deadline_expires", 400 * time.Millisecond, 220 * time.Millisecond, false, false, 400 * time.Millisecond, context.DeadlineExceeded},
		{"parent_cancellation", 200 * time.Millisecond, 0, true, false, 200 * time.Millisecond, context.Canceled},
		{"stop_cancels_context", 500 * time.Millisecond, 0, false, true, 500 * time.Millisecond, context.Canceled},
		{"zero_duration_never_expires", 0, 15 * time.Millisecond, false, false, time.Hour, nil},
		{"negative_duration_never_expires", -1 * time.Second, 15 * time.Millisecond, false, false, time.Hour, nil},
		{"zero_duration_stop", 0, 0, false, true, 0, context.Canceled},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			clk := newTimeoutFakeClock()
			parent, cancelParent := context.WithCancel(context.Background())
			defer cancelParent()
			ctx, reset, stop := newInactivityContext(parent, tc.timeout, clk)
			defer stop()
			if tc.resetAfter > 0 {
				clk.Advance(tc.resetAfter)
				reset()
			}
			if tc.cancelParent {
				cancelParent()
			}
			if tc.stop {
				stop()
			}
			clk.Advance(tc.advance)
			if err := ctx.Err(); !errors.Is(err, tc.wantErr) || (tc.wantErr == nil && err != nil) {
				t.Fatalf("ctx.Err() = %v, want %v", err, tc.wantErr)
			}
			reset()
		})
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
		name                  string
		err                   error
		timeoutCtx, parentCtx context.Context
		expected              bool
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
			if got := isTimeoutError(tc.timeoutCtx, tc.parentCtx, tc.err); got != tc.expected {
				t.Errorf("isTimeoutError() = %v, expected %v", got, tc.expected)
			}
		})
	}
}
