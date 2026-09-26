package events

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type flushingRecorder struct {
	*httptest.ResponseRecorder
	flushed int
}

func newFlushingRecorder() *flushingRecorder {
	return &flushingRecorder{
		ResponseRecorder: httptest.NewRecorder(),
	}
}

func (f *flushingRecorder) Flush() {
	f.flushed++
}

type nonFlushingRecorder struct {
	header http.Header
	code   int
}

func (n *nonFlushingRecorder) Header() http.Header {
	if n.header == nil {
		n.header = make(http.Header)
	}
	return n.header
}

func (n *nonFlushingRecorder) Write(b []byte) (int, error) {
	return len(b), nil
}

func (n *nonFlushingRecorder) WriteHeader(statusCode int) {
	n.code = statusCode
}

func TestHandler_OptionsAndMethods(t *testing.T) {
	t.Parallel()

	hub := NewHub(HubConfig{}, nil, nil)
	defer hub.Close()
	handler := Handler(hub)

	// 1. OPTIONS preflight
	req := httptest.NewRequest(http.MethodOptions, "/api/events", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 No Content for OPTIONS, got %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("expected CORS origin '*', got %s", rec.Header().Get("Access-Control-Allow-Origin"))
	}

	// 2. Disallowed method (POST)
	postReq := httptest.NewRequest(http.MethodPost, "/api/events", nil)
	postRec := httptest.NewRecorder()
	handler.ServeHTTP(postRec, postReq)

	if postRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 Method Not Allowed, got %d", postRec.Code)
	}
	if postRec.Header().Get("Allow") != "GET, OPTIONS" {
		t.Errorf("expected Allow 'GET, OPTIONS', got %s", postRec.Header().Get("Allow"))
	}
}

func TestHandler_NonFlusher(t *testing.T) {
	t.Parallel()

	hub := NewHub(HubConfig{}, nil, nil)
	defer hub.Close()
	handler := Handler(hub)

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	rec := &nonFlushingRecorder{}
	handler.ServeHTTP(rec, req)

	if rec.code != http.StatusInternalServerError {
		t.Fatalf("expected 500 Internal Server Error for non-flusher, got %d", rec.code)
	}
}

func TestHandler_InitialHydration(t *testing.T) {
	t.Parallel()

	provider := NewInMemoryStateProvider(nil)
	hub := NewHub(HubConfig{}, provider, nil)
	defer hub.Close()
	handler := Handler(hub)

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after initial hydration
	cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)
	rec := newFlushingRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}
	if rec.Header().Get("Content-Type") != "text/event-stream; charset=utf-8" {
		t.Errorf("expected text/event-stream, got %s", rec.Header().Get("Content-Type"))
	}
	if rec.Header().Get("X-Accel-Buffering") != "no" {
		t.Errorf("expected X-Accel-Buffering 'no', got %s", rec.Header().Get("X-Accel-Buffering"))
	}

	body := rec.Body.String()
	// Must contain the hydration event types
	expectedTypes := []string{
		"event: screen.rotate",
		"event: header.update",
		"event: video.state",
		"event: audio.state",
		"event: voice.state",
		"event: system.status",
	}

	for _, exp := range expectedTypes {
		if !strings.Contains(body, exp) {
			t.Errorf("expected hydration output to contain %q, body:\n%s", exp, body)
		}
	}
}

func TestHandler_Replay(t *testing.T) {
	t.Parallel()

	hub := NewHub(HubConfig{}, nil, nil)
	defer hub.Close()
	handler := Handler(hub)

	// Publish two events into the ring
	evt1 := hub.Publish(EventWidgetReload, []byte(`{"type":"card1"}`))
	hub.Publish(EventWidgetReload, []byte(`{"type":"card2"}`))

	// 1. Replay hit with Last-Event-ID header
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)
	req.Header.Set("Last-Event-ID", evt1.ID)
	rec := newFlushingRecorder()

	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "card2") {
		t.Errorf("expected replayed event 'card2', got:\n%s", body)
	}
	if strings.Contains(body, "card1") {
		t.Errorf("replayed body should not contain event1: %s", body)
	}

	// 2. Replay miss with unknown Last-Event-ID falls back to hydration
	ctx2, cancel2 := context.WithCancel(context.Background())
	cancel2()

	req2 := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx2)
	req2.Header.Set("Last-Event-ID", "unknown_id_999")
	rec2 := newFlushingRecorder()

	handler.ServeHTTP(rec2, req2)
	body2 := rec2.Body.String()
	if !strings.Contains(body2, "event: screen.rotate") {
		t.Errorf("expected fallback to initial hydration on replay miss: %s", body2)
	}
}

func TestHandler_LiveStreamingAndHeartbeat(t *testing.T) {
	t.Parallel()

	cfg := HubConfig{
		HeartbeatInterval: 10 * time.Millisecond,
	}
	hub := NewHub(cfg, nil, nil)
	defer hub.Close()
	handler := Handler(hub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)
	rec := newFlushingRecorder()

	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		handler.ServeHTTP(rec, req)
	}()

	// Wait for subscriber registration
	for i := 0; i < 50; i++ {
		if hub.SubscriberCount() > 0 {
			break
		}
		time.Sleep(1 * time.Millisecond)
	}

	// Publish live event
	hub.Publish(EventStyleReload, []byte(`{"file":"custom.css"}`))

	// Allow one heartbeat tick and event delivery
	time.Sleep(25 * time.Millisecond)

	// Cancel context to terminate stream cleanly
	cancel()
	<-doneCh

	body := rec.Body.String()
	if !strings.Contains(body, "event: style.reload") {
		t.Errorf("expected live event delivered to client, got: %s", body)
	}
	if !strings.Contains(body, ": ping") {
		t.Errorf("expected keep-alive heartbeat ping, got: %s", body)
	}
}

func TestHandler_QueryParamLastEventID(t *testing.T) {
	t.Parallel()

	hub := NewHub(HubConfig{}, nil, nil)
	defer hub.Close()
	handler := Handler(hub)

	evt := hub.Publish(EventWidgetReload, []byte(`{"type":"card1"}`))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/events?lastEventId="+evt.ID, nil).WithContext(ctx)
	rec := newFlushingRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

type failOnWriteFlusher struct {
	header http.Header
	code   int
}

func (f *failOnWriteFlusher) Header() http.Header {
	if f.header == nil {
		f.header = make(http.Header)
	}
	return f.header
}

func (f *failOnWriteFlusher) Write(b []byte) (int, error) {
	return 0, errors.New("simulated write error")
}

func (f *failOnWriteFlusher) WriteHeader(statusCode int) {
	f.code = statusCode
}

func (f *failOnWriteFlusher) Flush() {}

func TestHandler_WriteErrorOnInitialBatch(t *testing.T) {
	t.Parallel()

	hub := NewHub(HubConfig{}, nil, nil)
	defer hub.Close()
	handler := Handler(hub)

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	rec := &failOnWriteFlusher{}

	// Handler returns cleanly on write error
	handler.ServeHTTP(rec, req)
}

type hookStateProvider struct {
	StateProvider
	onHydration func()
}

func (h *hookStateProvider) GetScreenRotateData() *ScreenRotateData {
	if h.onHydration != nil {
		h.onHydration()
	}
	return h.StateProvider.GetScreenRotateData()
}

func TestHandler_EventPublishedDuringHydrationDelivered(t *testing.T) {
	t.Parallel()

	baseProvider := NewInMemoryStateProvider(nil)
	hub := NewHub(HubConfig{}, nil, nil)
	defer hub.Close()

	hookProvider := &hookStateProvider{
		StateProvider: baseProvider,
		onHydration: func() {
			// Publish event while BuildHydration is executing
			hub.Publish(EventSystemStatus, []byte(`{"online":true,"time":"2026-09-25T20:00:00Z","config_status":"error","config_error":"simulated failure"}`))
		},
	}
	hub.stateProvider = hookProvider
	handler := Handler(hub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)
	rec := newFlushingRecorder()

	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		handler.ServeHTTP(rec, req)
	}()

	// Wait for event delivery
	for i := 0; i < 50; i++ {
		if strings.Contains(rec.Body.String(), "simulated failure") {
			break
		}
		time.Sleep(1 * time.Millisecond)
	}

	cancel()
	<-doneCh

	body := rec.Body.String()
	if !strings.Contains(body, "simulated failure") {
		t.Fatalf("expected event published during hydration to be delivered to client, got:\n%s", body)
	}
}

func TestHandler_ReplayDeduplication(t *testing.T) {
	t.Parallel()

	hub := NewHub(HubConfig{}, nil, nil)
	defer hub.Close()
	handler := Handler(hub)

	// Publish baseline events
	evt1 := hub.Publish(EventWidgetReload, []byte(`{"type":"card1"}`))
	evt2 := hub.Publish(EventWidgetReload, []byte(`{"type":"card2"}`))
	if evt2 == nil {
		t.Fatal("expected evt2 to be published")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)
	req.Header.Set("Last-Event-ID", evt1.ID)
	rec := newFlushingRecorder()

	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		handler.ServeHTTP(rec, req)
	}()

	// Wait for subscriber registration
	for i := 0; i < 50; i++ {
		if hub.SubscriberCount() > 0 {
			break
		}
		time.Sleep(1 * time.Millisecond)
	}

	// Publish a live event after subscriber is active
	hub.Publish(EventWidgetReload, []byte(`{"type":"card3"}`))

	for i := 0; i < 50; i++ {
		if strings.Contains(rec.Body.String(), "card3") {
			break
		}
		time.Sleep(1 * time.Millisecond)
	}

	cancel()
	<-doneCh

	body := rec.Body.String()
	if strings.Contains(body, "card1") {
		t.Errorf("expected evt1 to be excluded from replay, body:\n%s", body)
	}
	if !strings.Contains(body, "card2") {
		t.Errorf("expected evt2 in replay, body:\n%s", body)
	}
	if !strings.Contains(body, "card3") {
		t.Errorf("expected live evt3 in stream, body:\n%s", body)
	}

	// Assert card2 is present exactly once (no duplicate delivery)
	countCard2 := strings.Count(body, "card2")
	if countCard2 != 1 {
		t.Errorf("expected card2 to be delivered exactly once, got %d occurrences", countCard2)
	}
}

func TestHandler_ReplayUpToDateWithLiveEvent(t *testing.T) {
	t.Parallel()

	hub := NewHub(HubConfig{}, nil, nil)
	defer hub.Close()
	handler := Handler(hub)

	// Publish baseline event
	evt1 := hub.Publish(EventWidgetReload, []byte(`{"type":"card1"}`))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Client is already up to date with evt1
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)
	req.Header.Set("Last-Event-ID", evt1.ID)
	rec := newFlushingRecorder()

	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		handler.ServeHTTP(rec, req)
	}()

	for i := 0; i < 50; i++ {
		if hub.SubscriberCount() > 0 {
			break
		}
		time.Sleep(1 * time.Millisecond)
	}

	hub.Publish(EventWidgetReload, []byte(`{"type":"card2"}`))

	for i := 0; i < 50; i++ {
		if strings.Contains(rec.Body.String(), "card2") {
			break
		}
		time.Sleep(1 * time.Millisecond)
	}

	cancel()
	<-doneCh

	body := rec.Body.String()
	if strings.Contains(body, "card1") {
		t.Errorf("did not expect card1, got:\n%s", body)
	}
	if !strings.Contains(body, "card2") {
		t.Errorf("expected live card2, got:\n%s", body)
	}
}

type slowWriterRecorder struct {
	*flushingRecorder
	blockCh   chan struct{}
	liveBlock bool
}

func (s *slowWriterRecorder) Write(b []byte) (int, error) {
	if s.liveBlock && s.blockCh != nil && strings.Contains(string(b), "slow_event_1") {
		<-s.blockCh
	}
	return s.flushingRecorder.Write(b)
}

func TestHandler_SlowConsumerClosesConnection(t *testing.T) {
	t.Parallel()

	cfg := HubConfig{
		SubscriberBuffer: 1,
	}
	hub := NewHub(cfg, nil, nil)
	defer hub.Close()
	handler := Handler(hub)

	blockCh := make(chan struct{})
	rec := &slowWriterRecorder{
		flushingRecorder: newFlushingRecorder(),
		blockCh:          blockCh,
		liveBlock:        false,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)

	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		handler.ServeHTTP(rec, req)
	}()

	for i := 0; i < 50; i++ {
		if hub.SubscriberCount() > 0 {
			break
		}
		time.Sleep(1 * time.Millisecond)
	}

	// Arm write block for live streaming
	rec.liveBlock = true

	// Publish slow_event_1, which blocks the handler inside Write
	hub.Publish(EventWidgetUpdate, []byte("slow_event_1"))

	// Give a moment for handler to receive slow_event_1 and block in Write
	time.Sleep(10 * time.Millisecond)

	// Publish slow_event_2 to fill the 1-slot subscriber channel buffer
	hub.Publish(EventWidgetUpdate, []byte("slow_event_2"))

	// Publish slow_event_3 which overflows the channel buffer and evicts subscriber
	hub.Publish(EventWidgetUpdate, []byte("slow_event_3"))

	// Eviction must have occurred immediately
	if hub.SubscriberCount() != 0 {
		t.Fatalf("expected subscriber to be evicted immediately, count: %d", hub.SubscriberCount())
	}

	// Unblock writer so handler can drain and see channel closure
	close(blockCh)

	select {
	case <-doneCh:
		// Handler exited cleanly when subscriber channel closed
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for handler to terminate after slow consumer eviction")
	}
}
