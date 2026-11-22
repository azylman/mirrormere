package events

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/testutil"
)

type flushingRecorder struct {
	*httptest.ResponseRecorder

	mu      sync.Mutex
	flushCh chan struct{}
}

func newFlushingRecorder() *flushingRecorder {
	return &flushingRecorder{ResponseRecorder: httptest.NewRecorder(), flushCh: make(chan struct{}, 100)}
}

func (f *flushingRecorder) Write(b []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ResponseRecorder.Write(b)
}

func (f *flushingRecorder) Flush() {
	select {
	case f.flushCh <- struct{}{}:
	default:
	}
}

func (f *flushingRecorder) BodyString() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Body.String()
}

func (f *flushingRecorder) wait(n int) {
	for range n {
		select {
		case <-f.flushCh:
		case <-time.After(time.Second):
		}
	}
}

type hookStateProvider struct {
	StateProvider

	onHydration func()
}

func (h *hookStateProvider) GetScreenRotateData() *ScreenRotateData {
	h.onHydration()
	return h.StateProvider.GetScreenRotateData()
}

type slowWriterRecorder struct {
	*flushingRecorder

	blockCh, entered chan struct{}
	liveBlock        bool
}

func (s *slowWriterRecorder) Write(b []byte) (int, error) {
	if s.liveBlock && strings.Contains(string(b), "slow_1") {
		s.entered <- struct{}{}
		<-s.blockCh
	}
	return s.flushingRecorder.Write(b)
}

func TestHandler_Methods(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, method string
		wantCode     int
		wantSub      string
		wantHeaders  map[string]string
	}{
		{"options_preflight", http.MethodOptions, http.StatusNoContent, "", map[string]string{"Access-Control-Allow-Origin": "*"}},
		{"method_not_allowed", http.MethodPost, http.StatusMethodNotAllowed, "method not allowed", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			hub := NewHub(HubConfig{}, nil, nil)
			defer hub.Close()
			rec := newFlushingRecorder()
			Handler(hub).ServeHTTP(rec, httptest.NewRequest(tc.method, "/api/events", http.NoBody))
			if body := rec.BodyString(); rec.Code != tc.wantCode || !strings.Contains(body, tc.wantSub) {
				t.Fatalf("got %d %q, want %d containing %q", rec.Code, body, tc.wantCode, tc.wantSub)
			}
			for k, v := range tc.wantHeaders {
				if got := rec.Header().Get(k); got != v {
					t.Errorf("header %s = %q, want %q", k, got, v)
				}
			}
		})
	}
}

func TestHandler_WriterCapabilities(t *testing.T) {
	t.Parallel()
	nonFlusher, failing := &testutil.NonFlusher{}, &testutil.FailingFlusher{}
	tests := []struct {
		name     string
		w        http.ResponseWriter
		got      *testutil.NonFlusher
		wantCode int
		wantSub  string
	}{
		{"non_flusher", nonFlusher, nonFlusher, http.StatusInternalServerError, "streaming unsupported"},
		{"fail_write_initial_batch", failing, &failing.NonFlusher, http.StatusOK, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			hub := NewHub(HubConfig{}, nil, nil)
			defer hub.Close()
			Handler(hub).ServeHTTP(tc.w, httptest.NewRequest(http.MethodGet, "/api/events", http.NoBody))
			if body := tc.got.Body.String(); tc.got.Code != tc.wantCode || !strings.Contains(body, tc.wantSub) {
				t.Fatalf("got %d %q, want %d containing %q", tc.got.Code, body, tc.wantCode, tc.wantSub)
			}
		})
	}
}

type replayTC struct {
	n, m, p, hk, ws, wn string
	ex                  int
}

func rtc(n, m, p, hk, ws, wn string, ex int) replayTC { return replayTC{n, m, p, hk, ws, wn, ex} }

func executeSyncReplay(hub *Hub, rec *flushingRecorder, req *http.Request, cancel context.CancelFunc) {
	cancel()
	Handler(hub).ServeHTTP(rec, req)
}

func executeAsyncReplay(hub *Hub, rec *flushingRecorder, req *http.Request, cancel context.CancelFunc, mode string) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		Handler(hub).ServeHTTP(rec, req)
	}()
	rec.wait(2)
	typ := "card2"
	if mode == "live_after_replay" {
		typ = "card3"
	}
	hub.Publish(EventWidgetReload, []byte(`{"type":"`+typ+`"}`))
	rec.wait(1)
	cancel()
	<-done
}

func setupReplayRequest(tc replayTC, evt1ID, evt2ID string) (context.Context, context.CancelFunc, *http.Request) {
	reqPath := tc.p
	if strings.HasSuffix(reqPath, "=") {
		reqPath += evt1ID
	}
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, reqPath, http.NoBody).WithContext(ctx)
	if tc.hk != "" {
		hVal := evt1ID
		if tc.n == "unknown_id_fallback" {
			hVal = "unknown_999"
		}
		if tc.m == "live_up_to_date" {
			hVal = evt2ID
		}
		req.Header.Set(tc.hk, hVal)
	}
	return ctx, cancel, req
}

func TestHandler_InitialHydration(t *testing.T) {
	t.Parallel()
	for _, tc := range []replayTC{
		rtc("fresh_initial_hydration", "sync", "/api/events", "", "event: screen.rotate", "", 0),
		rtc("header_replay", "sync", "/api/events", "Last-Event-ID", "card2", "card1", 0),
		rtc("query_lastEventId", "sync", "/api/events?lastEventId=", "", "card2", "card1", 0),
		rtc("query_snake_case", "sync", "/api/events?last_event_id=", "", "card2", "", 0),
		rtc("unknown_id_fallback", "sync", "/api/events", "Last-Event-ID", "event: screen.rotate", "", 0),
		rtc("during_hydration", "during", "/api/events", "", "simulated failure", "", 0),
		rtc("replay_deduplication", "live_after_replay", "/api/events", "Last-Event-ID", "card2", "card1", 1),
		rtc("up_to_date_replay", "live_up_to_date", "/api/events", "Last-Event-ID", "card2", "card1", 0),
	} {
		t.Run(tc.n, func(t *testing.T) {
			t.Parallel()
			baseProvider := NewInMemoryStateProvider(nil)
			hub := NewHub(HubConfig{}, baseProvider, nil)
			defer hub.Close()
			evt1 := hub.Publish(EventWidgetReload, []byte(`{"type":"card1"}`))
			evt2 := hub.Publish(EventWidgetReload, []byte(`{"type":"card2"}`))
			rec := newFlushingRecorder()

			ctx, cancel, req := setupReplayRequest(tc, evt1.ID, evt2.ID)
			defer cancel()
			_ = ctx

			if tc.m == "during" {
				hub.stateProvider = &hookStateProvider{baseProvider, func() {
					hub.Publish(EventSystemStatus, []byte(`{"config_error":"simulated failure"}`))
				}}
			}
			if tc.m == "sync" {
				executeSyncReplay(hub, rec, req, cancel)
			} else {
				executeAsyncReplay(hub, rec, req, cancel, tc.m)
			}
			body := rec.BodyString()
			if (tc.ex > 0 && strings.Count(body, tc.ws) != tc.ex) || !strings.Contains(body, tc.ws) || (tc.wn != "" && strings.Contains(body, tc.wn)) {
				t.Fatalf("unexpected body: %s", body)
			}
		})
	}
}

func TestHandler_LiveStreamingAndHeartbeat(t *testing.T) {
	t.Parallel()
	hub := NewHub(HubConfig{HeartbeatInterval: 10 * time.Millisecond}, nil, nil)
	defer hub.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec := newFlushingRecorder()
	go Handler(hub).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/events", http.NoBody).WithContext(ctx))

	rec.wait(2)
	hub.Publish(EventStyleReload, []byte(`{"file":"custom.css"}`))
	rec.wait(2)
	cancel()

	if b := rec.BodyString(); !strings.Contains(b, "event: style.reload") || !strings.Contains(b, ": ping") {
		t.Fatalf("missing reload/ping: %s", b)
	}
}

func TestHandler_SlowConsumerDisconnect(t *testing.T) {
	t.Parallel()
	hub := NewHub(HubConfig{SubscriberBuffer: 1}, nil, nil)
	defer hub.Close()
	blockCh := make(chan struct{})
	rec := &slowWriterRecorder{flushingRecorder: newFlushingRecorder(), blockCh: blockCh, entered: make(chan struct{}, 1), liveBlock: true}
	go Handler(hub).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/events", http.NoBody))

	rec.wait(2)
	hub.Publish(EventWidgetUpdate, []byte("slow_1"))
	<-rec.entered
	hub.Publish(EventWidgetUpdate, []byte("slow_2"))
	hub.Publish(EventWidgetUpdate, []byte("slow_3"))

	hub.mu.RLock()
	n := len(hub.subscribers)
	hub.mu.RUnlock()
	if n != 0 {
		t.Fatalf("expected evicted, got %d", n)
	}
	close(blockCh)
}

// publishOnHeadersRecorder models a client reacting as soon as response headers
// arrive, before the handler has produced its initial hydration frames.
type publishOnHeadersRecorder struct {
	*flushingRecorder

	hub       *Hub
	eventType string
	once      sync.Once
	received  chan string
}

func (r *publishOnHeadersRecorder) Flush() {
	r.once.Do(func() { r.hub.Publish(r.eventType, []byte(`{}`)) })
	r.flushingRecorder.Flush()
}

func (r *publishOnHeadersRecorder) Write(p []byte) (int, error) {
	if strings.Contains(string(p), "event: "+r.eventType+"\n") {
		r.received <- string(p)
	}
	return r.flushingRecorder.Write(p)
}

func TestHandler_EventsAfterResponseHeaders(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, eventType string }{
		{"style_reload", EventStyleReload},
		{"widget_reload", EventWidgetReload},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			hub := NewHub(HubConfig{}, nil, nil)
			defer hub.Close()
			ctx, cancel := context.WithCancel(t.Context())
			rec := &publishOnHeadersRecorder{flushingRecorder: newFlushingRecorder(), hub: hub, eventType: tc.eventType, received: make(chan string, 1)}
			done := make(chan struct{})
			go func() {
				defer close(done)
				Handler(hub).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/events", http.NoBody).WithContext(ctx))
			}()
			defer func() { cancel(); <-done }()
			select {
			case frame := <-rec.received:
				if !strings.Contains(frame, "data: {}\n") {
					t.Fatalf("unexpected event frame: %q", frame)
				}
			case <-time.After(time.Second):
				t.Fatal("event published after response headers was lost")
			}
		})
	}
}
