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

type blockingBrainClient struct {
	blockCh chan struct{}
}

func (b *blockingBrainClient) Ask(ctx context.Context, req AskRequest, onStatus func(status string)) (string, error) {
	if b.blockCh != nil {
		select {
		case <-b.blockCh:
			return "unblocked", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	<-ctx.Done()
	return "", ctx.Err()
}

type blockingStreamingBrainClient struct {
	blockCh chan struct{}
}

func (b *blockingStreamingBrainClient) Ask(ctx context.Context, req AskRequest, onStatus func(status string)) (string, error) {
	return b.AskStreaming(ctx, req, onStatus, nil)
}

func (b *blockingStreamingBrainClient) AskStreaming(ctx context.Context, req AskRequest, onStatus func(status string), onAudio func(chunk BrainAudioChunk)) (string, error) {
	if b.blockCh != nil {
		select {
		case <-b.blockCh:
			return "unblocked", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	<-ctx.Done()
	return "", ctx.Err()
}

type blockingTTSClient struct {
	blockCh chan struct{}
}

func (t *blockingTTSClient) Synthesize(ctx context.Context, text string, onChunk func(chunk TTSAudioChunk) error) error {
	if t.blockCh != nil {
		select {
		case <-t.blockCh:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	<-ctx.Done()
	return ctx.Err()
}

type heartbeatWaitingBrain struct {
	heartbeatSeen chan struct{}
}

func (b *heartbeatWaitingBrain) Ask(ctx context.Context, req AskRequest, onStatus func(status string)) (string, error) {
	select {
	case <-b.heartbeatSeen:
		return "heartbeat verified", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

type streamingHeartbeatWaitingBrain struct {
	heartbeatSeen chan struct{}
}

func (b *streamingHeartbeatWaitingBrain) Ask(ctx context.Context, req AskRequest, onStatus func(status string)) (string, error) {
	return "done", nil
}

func (b *streamingHeartbeatWaitingBrain) AskStreaming(ctx context.Context, req AskRequest, onStatus func(status string), onAudio func(chunk BrainAudioChunk)) (string, error) {
	select {
	case <-b.heartbeatSeen:
		if onAudio != nil {
			onAudio(BrainAudioChunk{Format: "pcm", SampleRate: 24000, Channels: 1, Data: make([]byte, 4800)})
		}
		return "stream done", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func TestHub_ThinkingHeartbeat_UnaryBrain(t *testing.T) {
	t.Parallel()

	heartbeatSignal := make(chan struct{}, 1)
	brain := &heartbeatWaitingBrain{heartbeatSeen: heartbeatSignal}
	stt := &mockSTT{text: "tell me a story"}

	var mu sync.Mutex
	var thinkingEvents []map[string]any
	var thinkingReceived bool

	sink := func(event string, data any) error {
		mu.Lock()
		defer mu.Unlock()
		if event == "thinking" {
			if m, ok := data.(map[string]int); ok {
				thinkingEvents = append(thinkingEvents, map[string]any{"elapsed_seconds": m["elapsed_seconds"]})
			}
			if !thinkingReceived {
				thinkingReceived = true
				heartbeatSignal <- struct{}{}
			}
		}
		return nil
	}

	cfg := &config.VoiceHubConfig{Enabled: true}
	h := NewHub(
		cfg,
		nil,
		WithSTTClient(stt),
		WithBrainClient(brain),
		WithThinkingHeartbeatInterval(10*time.Millisecond),
		WithBrainTimeout(2*time.Second),
	)

	wav := makeValidWAV(1600)
	err := h.Interact(context.Background(), bytes.NewReader(wav), "kiosk-1", "sess-1", sink, EdgeTimings{})
	if err != nil {
		t.Fatalf("expected successful interaction, got: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(thinkingEvents) == 0 {
		t.Fatalf("expected at least one thinking event, got none")
	}
	first := thinkingEvents[0]
	elapsed, ok := first["elapsed_seconds"].(int)
	if !ok || elapsed <= 0 {
		t.Errorf("expected positive elapsed_seconds, got %v", first["elapsed_seconds"])
	}
}

func TestHub_ThinkingHeartbeat_StreamingBrain(t *testing.T) {
	t.Parallel()

	heartbeatSignal := make(chan struct{}, 1)
	brain := &streamingHeartbeatWaitingBrain{heartbeatSeen: heartbeatSignal}
	stt := &mockSTT{text: "stream audio"}

	var mu sync.Mutex
	var eventsReceived []string
	var thinkingReceived bool

	sink := func(event string, data any) error {
		mu.Lock()
		defer mu.Unlock()
		eventsReceived = append(eventsReceived, event)
		if event == "thinking" && !thinkingReceived {
			thinkingReceived = true
			heartbeatSignal <- struct{}{}
		}
		return nil
	}

	cfg := &config.VoiceHubConfig{Enabled: true}
	h := NewHub(
		cfg,
		nil,
		WithSTTClient(stt),
		WithBrainClient(brain),
		WithThinkingHeartbeatInterval(10*time.Millisecond),
		WithBrainTimeout(2*time.Second),
	)

	wav := makeValidWAV(1600)
	err := h.Interact(context.Background(), bytes.NewReader(wav), "kiosk-1", "sess-1", sink, EdgeTimings{})
	if err != nil {
		t.Fatalf("expected successful interaction, got: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	var hadThinking, hadAudioChunk bool
	var thinkingAfterAudio bool
	for _, ev := range eventsReceived {
		if ev == "thinking" {
			hadThinking = true
			if hadAudioChunk {
				thinkingAfterAudio = true
			}
		}
		if ev == "audio_chunk" {
			hadAudioChunk = true
		}
	}

	if !hadThinking {
		t.Errorf("expected thinking event before audio streaming")
	}
	if !hadAudioChunk {
		t.Errorf("expected audio_chunk event")
	}
	if thinkingAfterAudio {
		t.Errorf("thinking event must not be emitted after audio streaming has begun")
	}
}

func TestHub_BrainDeliberation_Timeout_Unary(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)
	coord := NewCoordinator(nil)

	brain := &blockingBrainClient{}
	stt := &mockSTT{text: "hang forever"}

	sink, getEvents := collectEvents()

	cfg := &config.VoiceHubConfig{Enabled: true}
	h := NewHub(
		cfg,
		coord,
		WithSTTClient(stt),
		WithBrainClient(brain),
		WithMetrics(m),
		WithBrainTimeout(20*time.Millisecond),
	)

	wav := makeValidWAV(1600)
	err := h.Interact(context.Background(), bytes.NewReader(wav), "kiosk-kitchen", "sess-1", sink, EdgeTimings{})
	if err == nil {
		t.Fatalf("expected timeout error, got nil")
	}
	if !errors.Is(err, ErrBrainTimeout) {
		t.Errorf("expected ErrBrainTimeout, got: %v", err)
	}
	if !errors.Is(err, ErrBrainFailed) {
		t.Errorf("expected error to wrap ErrBrainFailed, got: %v", err)
	}

	if !hasErrorCode(getEvents(), "brain_timeout") {
		t.Errorf("expected brain_timeout event, got %v", eventNames(getEvents()))
	}

	if coord.GetState().State != StateError {
		t.Errorf("expected coordinator state to be %q, got %q", StateError, coord.GetState().State)
	}

	mfs, gatherErr := reg.Gather()
	if gatherErr != nil {
		t.Fatalf("failed to gather metrics: %v", gatherErr)
	}
	var foundBrainTimeout bool
	for _, mf := range mfs {
		if mf.GetName() == "mirrormere_voice_errors_total" {
			for _, metric := range mf.GetMetric() {
				var stage, errType string
				for _, lbl := range metric.GetLabel() {
					if lbl.GetName() == "stage" {
						stage = lbl.GetValue()
					}
					if lbl.GetName() == "error_type" {
						errType = lbl.GetValue()
					}
				}
				if stage == "brain" && errType == "brain_timeout" && metric.GetCounter().GetValue() == 1 {
					foundBrainTimeout = true
				}
			}
		}
	}
	if !foundBrainTimeout {
		t.Errorf("expected mirrormere_voice_errors_total metric for brain_timeout")
	}
}

func TestHub_BrainDeliberation_Timeout_Streaming(t *testing.T) {
	t.Parallel()

	brain := &blockingStreamingBrainClient{}
	stt := &mockSTT{text: "stream hang"}
	sink, getEvents := collectEvents()

	cfg := &config.VoiceHubConfig{Enabled: true}
	h := NewHub(
		cfg,
		nil,
		WithSTTClient(stt),
		WithBrainClient(brain),
		WithBrainTimeout(20*time.Millisecond),
	)

	wav := makeValidWAV(1600)
	err := h.Interact(context.Background(), bytes.NewReader(wav), "kiosk-kitchen", "sess-1", sink, EdgeTimings{})
	if err == nil {
		t.Fatalf("expected timeout error, got nil")
	}
	if !errors.Is(err, ErrBrainTimeout) {
		t.Errorf("expected ErrBrainTimeout, got: %v", err)
	}
	if !hasErrorCode(getEvents(), "brain_timeout") {
		t.Errorf("expected brain_timeout event, got %v", eventNames(getEvents()))
	}
}

func TestHub_TTS_Timeout(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	stt := &mockSTT{text: "hello"}
	brain := &mockBrain{reply: "quick reply"}
	tts := &blockingTTSClient{}

	sink, getEvents := collectEvents()

	cfg := &config.VoiceHubConfig{Enabled: true}
	h := NewHub(
		cfg,
		nil,
		WithSTTClient(stt),
		WithBrainClient(brain),
		WithTTSClient(tts),
		WithMetrics(m),
		WithTTSTimeout(20*time.Millisecond),
	)

	wav := makeValidWAV(1600)
	err := h.Interact(context.Background(), bytes.NewReader(wav), "kiosk-kitchen", "sess-1", sink, EdgeTimings{})
	if err != nil {
		t.Fatalf("expected graceful degradation on TTS timeout, got err: %v", err)
	}

	if !hasErrorCode(getEvents(), "tts_timeout") {
		t.Errorf("expected tts_timeout event, got %v", eventNames(getEvents()))
	}

	mfs, gatherErr := reg.Gather()
	if gatherErr != nil {
		t.Fatalf("failed to gather metrics: %v", gatherErr)
	}
	var foundTTSTimeout bool
	for _, mf := range mfs {
		if mf.GetName() == "mirrormere_voice_errors_total" {
			for _, metric := range mf.GetMetric() {
				var stage, errType string
				for _, lbl := range metric.GetLabel() {
					if lbl.GetName() == "stage" {
						stage = lbl.GetValue()
					}
					if lbl.GetName() == "error_type" {
						errType = lbl.GetValue()
					}
				}
				if stage == "tts" && errType == "tts_timeout" && metric.GetCounter().GetValue() == 1 {
					foundTTSTimeout = true
				}
			}
		}
	}
	if !foundTTSTimeout {
		t.Errorf("expected mirrormere_voice_errors_total metric for tts_timeout")
	}
}

func TestHub_ClientCancellation_DoesNotReportBrainTimeout(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	ctx, cancel := context.WithCancel(context.Background())

	brain := &mockBrain{
		statuses: []string{"thinking..."},
	}
	// We cancel client context during deliberation
	cancelOnce := sync.Once{}
	stt := &mockSTT{text: "cancel turn"}

	sink, getEvents := collectEvents()

	cfg := &config.VoiceHubConfig{Enabled: true}
	h := NewHub(
		cfg,
		nil,
		WithSTTClient(stt),
		WithBrainClient(brain),
		WithMetrics(m),
		WithBrainTimeout(5*time.Second),
	)

	// Cancel context when status arrives
	wrappedSink := func(event string, data any) error {
		if event == "status" {
			cancelOnce.Do(func() {
				cancel()
			})
		}
		return sink(event, data)
	}

	wav := makeValidWAV(1600)
	_ = h.Interact(ctx, bytes.NewReader(wav), "kiosk-kitchen", "sess-1", wrappedSink, EdgeTimings{})

	if hasErrorCode(getEvents(), "brain_timeout") {
		t.Errorf("client cancellation must not report brain_timeout error")
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
		{
			name:       "parent_canceled_returns_false",
			err:        context.DeadlineExceeded,
			timeoutCtx: deadlineCtx,
			parentCtx:  canceledCtx,
			expected:   false,
		},
		{
			name:       "err_is_deadline_exceeded",
			err:        context.DeadlineExceeded,
			timeoutCtx: nil,
			parentCtx:  validCtx,
			expected:   true,
		},
		{
			name:       "timeout_ctx_is_deadline_exceeded",
			err:        errors.New("read tcp: network down"),
			timeoutCtx: deadlineCtx,
			parentCtx:  validCtx,
			expected:   true,
		},
		{
			name:       "general_error_returns_false",
			err:        errors.New("generic error"),
			timeoutCtx: validCtx,
			parentCtx:  validCtx,
			expected:   false,
		},
		{
			name:       "nil_err_returns_false",
			err:        nil,
			timeoutCtx: validCtx,
			parentCtx:  validCtx,
			expected:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := isTimeoutError(tc.err, tc.timeoutCtx, tc.parentCtx)
			if got != tc.expected {
				t.Errorf("isTimeoutError() = %v, expected %v", got, tc.expected)
			}
		})
	}
}

type midStreamFailTTS struct {
	failErr error
}

func (m *midStreamFailTTS) Synthesize(ctx context.Context, text string, onChunk func(chunk TTSAudioChunk) error) error {
	// Emit one valid chunk first
	if err := onChunk(TTSAudioChunk{Data: make([]byte, 4800), Format: "pcm", SampleRate: 24000, Channels: 1}); err != nil {
		return err
	}
	return m.failErr
}

func TestHub_TTS_MidStreamFailure_And_Timeout(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)
	coord := NewCoordinator(nil)

	// Case 1: Mid-stream timeout
	ttsTimeout := &midStreamFailTTS{failErr: context.DeadlineExceeded}
	h1 := NewHub(
		&config.VoiceHubConfig{Enabled: true},
		coord,
		WithTTSClient(ttsTimeout),
		WithMetrics(m),
		WithTTSTimeout(50*time.Millisecond),
	)

	sink1, getEvents1 := collectEvents()
	tr1 := "transcript"
	status1, err1 := h1.synthesizeAndEmitReply(context.Background(), "mid-stream fail", "node-mid", "kokoro", &tr1, sink1)
	if status1 != "error" || err1 == nil {
		t.Fatalf("expected error status and non-nil error, got status=%q, err=%v", status1, err1)
	}
	if !hasErrorCode(getEvents1(), "tts_timeout") {
		t.Errorf("expected tts_timeout event in mid-stream timeout, got %v", eventNames(getEvents1()))
	}
	if coord.GetState().State != StateError {
		t.Errorf("expected coordinator state %q, got %q", StateError, coord.GetState().State)
	}

	// Case 2: Mid-stream generic error
	ttsGeneric := &midStreamFailTTS{failErr: errors.New("i/o error")}
	h2 := NewHub(
		&config.VoiceHubConfig{Enabled: true},
		coord,
		WithTTSClient(ttsGeneric),
		WithMetrics(m),
	)

	sink2, getEvents2 := collectEvents()
	status2, err2 := h2.synthesizeAndEmitReply(context.Background(), "mid-stream generic", "node-mid", "kokoro", &tr1, sink2)
	if status2 != "error" || err2 == nil {
		t.Fatalf("expected error status and non-nil error, got status=%q, err=%v", status2, err2)
	}
	if !hasErrorCode(getEvents2(), "tts_error") {
		t.Errorf("expected tts_error event in mid-stream error, got %v", eventNames(getEvents2()))
	}
}

func TestHub_ThinkingHeartbeat_SinkError_TerminatesTicker(t *testing.T) {
	t.Parallel()

	heartbeatDone := make(chan struct{})
	brain := funcBrain(func(ctx context.Context, req AskRequest, onStatus func(status string)) (string, error) {
		<-heartbeatDone
		return "ok", nil
	})

	sink := func(event string, data any) error {
		if event == "thinking" {
			select {
			case heartbeatDone <- struct{}{}:
			default:
			}
			return errors.New("client socket closed")
		}
		return nil
	}

	h := NewHub(
		&config.VoiceHubConfig{Enabled: true},
		nil,
		WithSTTClient(&mockSTT{text: "hi"}),
		WithBrainClient(brain),
		WithThinkingHeartbeatInterval(5*time.Millisecond),
	)

	wav := makeValidWAV(1600)
	err := h.Interact(context.Background(), bytes.NewReader(wav), "node-err", "s1", sink, EdgeTimings{})
	if err != nil {
		t.Fatalf("unexpected interact error: %v", err)
	}
}

type funcBrain func(ctx context.Context, req AskRequest, onStatus func(status string)) (string, error)

func (f funcBrain) Ask(ctx context.Context, req AskRequest, onStatus func(status string)) (string, error) {
	return f(ctx, req, onStatus)
}

func TestInactivityContext_ExpiresOnTimeout(t *testing.T) {
	t.Parallel()

	ctx, stop := newInactivityContext(context.Background(), 50*time.Millisecond)
	defer stop()

	select {
	case <-ctx.Done():
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Fatalf("expected context.DeadlineExceeded, got %v", ctx.Err())
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for inactivity context expiration")
	}
}

func TestInactivityContext_ResetExtendsDeadline(t *testing.T) {
	t.Parallel()

	timeout := 80 * time.Millisecond
	ctx, stop := newInactivityContext(context.Background(), timeout)
	defer stop()

	// Wait 45ms and reset. Without reset, timeout would fire at 80ms.
	time.Sleep(45 * time.Millisecond)
	ctx.Reset()

	// At 90ms total (45ms + 45ms), the original 80ms deadline has passed,
	// but context must still be active due to the reset extending it to 45ms + 80ms = 125ms.
	time.Sleep(45 * time.Millisecond)
	select {
	case <-ctx.Done():
		t.Fatalf("context was cancelled prematurely at 90ms: %v", ctx.Err())
	default:
	}

	// Wait for the rescheduled timeout to expire (expected at ~125ms total).
	select {
	case <-ctx.Done():
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Fatalf("expected context.DeadlineExceeded, got %v", ctx.Err())
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for rescheduled inactivity context expiration")
	}
}

func TestInactivityContext_ParentCancellation(t *testing.T) {
	t.Parallel()

	parent, parentCancel := context.WithCancel(context.Background())
	ctx, stop := newInactivityContext(parent, 200*time.Millisecond)
	defer stop()

	parentCancel()

	select {
	case <-ctx.Done():
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", ctx.Err())
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Fatalf("did not expect context.DeadlineExceeded, got %v", ctx.Err())
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for parent context cancellation")
	}
}

func TestInactivityContext_StopCancelsContext(t *testing.T) {
	t.Parallel()

	ctx, stop := newInactivityContext(context.Background(), 500*time.Millisecond)
	stop()

	select {
	case <-ctx.Done():
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", ctx.Err())
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Fatalf("did not expect context.DeadlineExceeded, got %v", ctx.Err())
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("context not canceled after calling stop")
	}
}

func TestInactivityContext_ZeroDuration(t *testing.T) {
	t.Parallel()

	for _, timeout := range []time.Duration{0, -1 * time.Second} {
		ctx, stop := newInactivityContext(context.Background(), timeout)
		select {
		case <-ctx.Done():
			t.Fatalf("zero/negative duration context (%v) unexpectedly cancelled: %v", timeout, ctx.Err())
		case <-time.After(30 * time.Millisecond):
		}

		ctx.Reset() // Reset on zero duration should be safe and a no-op

		select {
		case <-ctx.Done():
			t.Fatalf("zero/negative duration context (%v) unexpectedly cancelled after reset: %v", timeout, ctx.Err())
		case <-time.After(30 * time.Millisecond):
		}

		stop()

		select {
		case <-ctx.Done():
			if !errors.Is(ctx.Err(), context.Canceled) {
				t.Fatalf("expected context.Canceled after stop, got %v", ctx.Err())
			}
		case <-time.After(50 * time.Millisecond):
			t.Fatal("context not canceled after calling stop")
		}
	}
}


type periodicStreamingBrain struct {
	chunkCount int
	interval   time.Duration
	reply      string
}

func (b *periodicStreamingBrain) Ask(ctx context.Context, req AskRequest, onStatus func(status string)) (string, error) {
	return b.AskStreaming(ctx, req, onStatus, nil)
}

func (b *periodicStreamingBrain) AskStreaming(ctx context.Context, req AskRequest, onStatus func(status string), onAudio func(chunk BrainAudioChunk)) (string, error) {
	ticker := time.NewTicker(b.interval)
	defer ticker.Stop()
	for i := 0; i < b.chunkCount; i++ {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
			if onAudio != nil {
				onAudio(BrainAudioChunk{
					Text:       fmt.Sprintf("part-%d", i),
					Data:       make([]byte, 4800),
					Format:     "pcm",
					SampleRate: 24000,
					Channels:   1,
				})
			}
		}
	}
	return b.reply, nil
}

func TestHub_BrainStreaming_InactivityTimeout_LongReply(t *testing.T) {
	t.Parallel()

	brain := &periodicStreamingBrain{
		chunkCount: 6,
		interval:   10 * time.Millisecond,
		reply:      "full reply text",
	}
	stt := &mockSTT{text: "stream long reply"}
	sink, getEvents := collectEvents()

	cfg := &config.VoiceHubConfig{Enabled: true}
	h := NewHub(
		cfg,
		nil,
		WithSTTClient(stt),
		WithBrainClient(brain),
		WithBrainTimeout(25 * time.Millisecond),
	)

	wav := makeValidWAV(1600)
	err := h.Interact(context.Background(), bytes.NewReader(wav), "kiosk-kitchen", "sess-long", sink, EdgeTimings{})
	if err != nil {
		t.Fatalf("expected turn to complete successfully, got err: %v", err)
	}

	if hasErrorCode(getEvents(), "brain_timeout") {
		t.Errorf("did not expect brain_timeout event, got %v", eventNames(getEvents()))
	}

	events := getEvents()
	var foundReply bool
	for _, ev := range events {
		if ev.Event == "reply" {
			foundReply = true
			if m, ok := ev.Data.(map[string]string); ok {
			if m["reply"] != "full reply text" {
				t.Errorf("expected reply %q, got %q", "full reply text", m["reply"])
			}
		}
		}
	}
	if !foundReply {
		t.Errorf("expected reply event, got %v", eventNames(events))
	}
}

type stallingStreamingBrain struct {
	stallDuration time.Duration
}

func (b *stallingStreamingBrain) Ask(ctx context.Context, req AskRequest, onStatus func(status string)) (string, error) {
	return b.AskStreaming(ctx, req, onStatus, nil)
}

func (b *stallingStreamingBrain) AskStreaming(ctx context.Context, req AskRequest, onStatus func(status string), onAudio func(chunk BrainAudioChunk)) (string, error) {
	if onAudio != nil {
		onAudio(BrainAudioChunk{
			Text:       "start chunk",
			Data:       make([]byte, 4800),
			Format:     "pcm",
			SampleRate: 24000,
			Channels:   1,
		})
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(b.stallDuration):
		return "should have timed out", nil
	}
}

func TestHub_BrainStreaming_InactivityTimeout_MidStreamStall(t *testing.T) {
	t.Parallel()

	brain := &stallingStreamingBrain{
		stallDuration: 50 * time.Millisecond,
	}
	stt := &mockSTT{text: "stream stall"}
	sink, getEvents := collectEvents()

	cfg := &config.VoiceHubConfig{Enabled: true}
	h := NewHub(
		cfg,
		nil,
		WithSTTClient(stt),
		WithBrainClient(brain),
		WithBrainTimeout(20 * time.Millisecond),
	)

	wav := makeValidWAV(1600)
	err := h.Interact(context.Background(), bytes.NewReader(wav), "kiosk-kitchen", "sess-stall", sink, EdgeTimings{})
	if err == nil {
		t.Fatalf("expected error due to inactivity timeout stall, got nil")
	}
	if !errors.Is(err, ErrBrainTimeout) {
		t.Errorf("expected ErrBrainTimeout, got %v", err)
	}
	if !hasErrorCode(getEvents(), "brain_timeout") {
		t.Errorf("expected brain_timeout event, got %v", eventNames(getEvents()))
	}
}

type statusResetStreamingBrain struct{}

func (b *statusResetStreamingBrain) Ask(ctx context.Context, req AskRequest, onStatus func(status string)) (string, error) {
	return b.AskStreaming(ctx, req, onStatus, nil)
}

func (b *statusResetStreamingBrain) AskStreaming(ctx context.Context, req AskRequest, onStatus func(status string), onAudio func(chunk BrainAudioChunk)) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(15 * time.Millisecond):
	}
	if onStatus != nil {
		onStatus("tool executing: web search")
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(15 * time.Millisecond):
	}
	if onAudio != nil {
		onAudio(BrainAudioChunk{
			Text:       "done answer",
			Data:       make([]byte, 4800),
			Format:     "pcm",
			SampleRate: 24000,
			Channels:   1,
		})
	}
	return "done answer", nil
}

func TestHub_BrainStreaming_StatusEvent_ResetsTimeout(t *testing.T) {
	t.Parallel()

	brain := &statusResetStreamingBrain{}
	stt := &mockSTT{text: "stream status reset"}
	sink, getEvents := collectEvents()

	cfg := &config.VoiceHubConfig{Enabled: true}
	h := NewHub(
		cfg,
		nil,
		WithSTTClient(stt),
		WithBrainClient(brain),
		WithBrainTimeout(25 * time.Millisecond),
	)

	wav := makeValidWAV(1600)
	err := h.Interact(context.Background(), bytes.NewReader(wav), "kiosk-kitchen", "sess-status", sink, EdgeTimings{})
	if err != nil {
		t.Fatalf("expected turn to complete cleanly after status reset, got err: %v", err)
	}
	if hasErrorCode(getEvents(), "brain_timeout") {
		t.Errorf("did not expect brain_timeout event, got %v", eventNames(getEvents()))
	}
}

type periodicTTSClient struct {
	chunkCount int
	interval   time.Duration
}

func (t *periodicTTSClient) Synthesize(ctx context.Context, text string, onChunk func(chunk TTSAudioChunk) error) error {
	ticker := time.NewTicker(t.interval)
	defer ticker.Stop()
	for i := 0; i < t.chunkCount; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := onChunk(TTSAudioChunk{
				Data:       make([]byte, 4800),
				Format:     "pcm",
				SampleRate: 24000,
				Channels:   1,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func TestHub_TTS_InactivityTimeout_LongReply(t *testing.T) {
	t.Parallel()

	tts := &periodicTTSClient{
		chunkCount: 5,
		interval:   10 * time.Millisecond,
	}
	sink, getEvents := collectEvents()

	cfg := &config.VoiceHubConfig{Enabled: true}
	h := NewHub(
		cfg,
		nil,
		WithTTSClient(tts),
		WithTTSTimeout(25 * time.Millisecond),
	)

	tr := "transcript"
	status, err := h.synthesizeAndEmitReply(context.Background(), "a longer reply for TTS", "node-tts", "kokoro", &tr, sink)
	if err != nil {
		t.Fatalf("expected synthesizeAndEmitReply to succeed, got err: %v", err)
	}
	if status != "success" {
		t.Fatalf("expected status %q, got %q", "success", status)
	}
	if hasErrorCode(getEvents(), "tts_timeout") {
		t.Errorf("did not expect tts_timeout event, got %v", eventNames(getEvents()))
	}
}

type stallingTTSClient struct {
	stallDuration time.Duration
}

func (t *stallingTTSClient) Synthesize(ctx context.Context, text string, onChunk func(chunk TTSAudioChunk) error) error {
	if err := onChunk(TTSAudioChunk{
		Data:       make([]byte, 4800),
		Format:     "pcm",
		SampleRate: 24000,
		Channels:   1,
	}); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(t.stallDuration):
		return nil
	}
}

func TestHub_TTS_InactivityTimeout_MidStreamStall(t *testing.T) {
	t.Parallel()

	tts := &stallingTTSClient{
		stallDuration: 50 * time.Millisecond,
	}
	sink, getEvents := collectEvents()

	cfg := &config.VoiceHubConfig{Enabled: true}
	h := NewHub(
		cfg,
		nil,
		WithTTSClient(tts),
		WithTTSTimeout(20 * time.Millisecond),
	)

	tr := "transcript"
	status, err := h.synthesizeAndEmitReply(context.Background(), "mid-stream stalling reply", "node-tts", "kokoro", &tr, sink)
	if status != "error" || err == nil {
		t.Fatalf("expected error status and non-nil err, got status=%q, err=%v", status, err)
	}
	if !hasErrorCode(getEvents(), "tts_timeout") {
		t.Errorf("expected tts_timeout event, got %v", eventNames(getEvents()))
	}
}
