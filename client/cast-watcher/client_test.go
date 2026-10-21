package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type pipeDialer struct {
	clientConn net.Conn
	serverConn net.Conn
}

func newPipeDialer() *pipeDialer {
	c1, c2 := net.Pipe()
	return &pipeDialer{clientConn: c1, serverConn: c2}
}

func (p *pipeDialer) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	return p.clientConn, nil
}

func TestCastClient_HandshakeActiveAndMedia(t *testing.T) {
	pd := newPipeDialer()
	eventCh := make(chan string, 16)
	coreServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		eventCh <- r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer coreServer.Close()

	client := NewCastClient(ClientConfig{
		ChromecastAddr:    "127.0.0.1:8009",
		CoreURL:           coreServer.URL,
		Dialer:            pd.Dial,
		HeartbeatInterval: 100 * time.Millisecond,
		WatchdogTimeout:   1 * time.Second,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client.Start(ctx)
	defer client.Close()
	serverConn := pd.serverConn
	defer serverConn.Close()

	// 1. Initial handshake
	msg, err := DecodeCastMessage(serverConn)
	if err != nil || msg.Namespace != NamespaceConnection || !strings.Contains(msg.PayloadUTF8, "CONNECT") {
		t.Fatalf("expected initial CONNECT, got msg=%+v err=%v", msg, err)
	}
	msg, err = DecodeCastMessage(serverConn)
	if err != nil || msg.Namespace != NamespaceReceiver || !strings.Contains(msg.PayloadUTF8, "GET_STATUS") {
		t.Fatalf("expected GET_STATUS, got msg=%+v err=%v", msg, err)
	}

	// 2. Active YouTube app
	f, _ := EncodeCastMessage(CastMessage{SourceID: DestinationReceiver, DestinationID: SourceSender, Namespace: NamespaceReceiver, PayloadUTF8: `{"type":"RECEIVER_STATUS","status":{"applications":[{"appId":"233637DE","displayName":"YouTube","transportId":"yt-1"}]}}`})
	_, _ = serverConn.Write(f)

	// App CONNECT and GET_STATUS
	m1, _ := DecodeCastMessage(serverConn)
	m2, _ := DecodeCastMessage(serverConn)
	if m1.Namespace != NamespaceConnection && m2.Namespace != NamespaceConnection {
		t.Errorf("expected app CONNECT")
	}

	// 3. MEDIA_STATUS PLAYING
	mf, _ := EncodeCastMessage(CastMessage{SourceID: "yt-1", DestinationID: SourceSender, Namespace: NamespaceMedia, PayloadUTF8: `{"type":"MEDIA_STATUS","status":[{"mediaSessionId":99,"playerState":"PLAYING"}]}`})
	_, _ = serverConn.Write(mf)

	waitEvents := func(expected ...string) {
		for _, exp := range expected {
			select {
			case p := <-eventCh:
				if p != exp {
					t.Fatalf("got path %s, want %s", p, exp)
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("timed out waiting for event %s", exp)
			}
		}
	}
	waitEvents("/api/video/trigger", "/api/video/state")

	snap := client.GetStatus()
	if !snap.Connected || snap.AppID != "233637DE" || snap.PlayerState != "playing" {
		t.Errorf("unexpected status: %+v", snap)
	}

	// 4. Media actions
	go func() { _ = client.SendMediaAction(ctx, "pause") }()
	actMsg, _ := DecodeCastMessage(serverConn)
	if !strings.Contains(actMsg.PayloadUTF8, "PAUSE") {
		t.Errorf("expected PAUSE, got %s", actMsg.PayloadUTF8)
	}

	go func() { _ = client.SendMediaAction(ctx, "toggle_playback") }()
	togMsg, _ := DecodeCastMessage(serverConn)
	if !strings.Contains(togMsg.PayloadUTF8, "PAUSE") {
		t.Errorf("expected PAUSE on toggle, got %s", togMsg.PayloadUTF8)
	}

	// 5. Return to Backdrop
	ib, _ := EncodeCastMessage(CastMessage{SourceID: DestinationReceiver, DestinationID: SourceSender, Namespace: NamespaceReceiver, PayloadUTF8: `{"type":"RECEIVER_STATUS","status":{"applications":[{"appId":"` + BackdropAppID + `","displayName":"Backdrop","transportId":"b-1"}]}}`})
	_, _ = serverConn.Write(ib)
	waitEvents("/api/video/dismiss")

	snap = client.GetStatus()
	if snap.AppID != "" || snap.PlayerState != "" {
		t.Errorf("expected state reset on backdrop: %+v", snap)
	}
}

func TestCastClient_HeartbeatPingAndPong(t *testing.T) {
	client := NewCastClient(ClientConfig{})
	c1, s1 := net.Pipe()
	defer c1.Close()
	defer s1.Close()
	client.conn, client.connected = c1, true

	go client.handleHeartbeatMessage(CastMessage{SourceID: "sender-test", Namespace: NamespaceHeartbeat, PayloadUTF8: `{"type":"PING"}`})

	msg, err := DecodeCastMessage(s1)
	if err != nil || !strings.Contains(msg.PayloadUTF8, "PONG") || msg.DestinationID != "sender-test" {
		t.Fatalf("expected PONG response, got msg=%+v err=%v", msg, err)
	}
}

func TestCastClient_MediaActionsAndSeek(t *testing.T) {
	client := NewCastClient(ClientConfig{})
	ctx := context.Background()

	if err := client.SendMediaAction(ctx, "play"); !errors.Is(err, ErrNotConnected) {
		t.Errorf("expected ErrNotConnected, got: %v", err)
	}
	if err := client.Seek(ctx, 42.0); !errors.Is(err, ErrNotConnected) {
		t.Errorf("expected ErrNotConnected on seek, got: %v", err)
	}

	c1, s1 := net.Pipe()
	defer c1.Close()
	defer s1.Close()
	client.conn, client.connected = c1, true

	if err := client.SendMediaAction(ctx, "play"); !errors.Is(err, ErrNoActiveMediaSession) {
		t.Errorf("expected ErrNoActiveMediaSession, got: %v", err)
	}
	if err := client.Seek(ctx, 42.0); !errors.Is(err, ErrNoActiveMediaSession) {
		t.Errorf("expected ErrNoActiveMediaSession on seek, got: %v", err)
	}

	client.transportID, client.mediaSessionID = "t1", 1
	if err := client.SendMediaAction(ctx, "rewind_bad"); !errors.Is(err, ErrInvalidAction) {
		t.Errorf("expected ErrInvalidAction, got: %v", err)
	}
	if err := client.Seek(ctx, -10.0); !errors.Is(err, ErrInvalidAction) {
		t.Errorf("expected ErrInvalidAction for negative seek, got: %v", err)
	}

	sendAction := func(action string) CastMessage {
		t.Helper()
		errCh := make(chan error, 1)
		go func() { errCh <- client.SendMediaAction(ctx, action) }()
		msg, err := DecodeCastMessage(s1)
		if err != nil {
			t.Fatalf("decode %s failed: %v", action, err)
		}
		if err := <-errCh; err != nil {
			t.Fatalf("SendMediaAction(%s) failed: %v", action, err)
		}
		return msg
	}

	client.playerState = "paused"
	m := sendAction("toggle_playback")
	if !strings.Contains(m.PayloadUTF8, "PLAY") {
		t.Errorf("expected PLAY on toggle, got %s", m.PayloadUTF8)
	}

	m = sendAction("play")
	if !strings.Contains(m.PayloadUTF8, "PLAY") {
		t.Errorf("expected PLAY on play, got %s", m.PayloadUTF8)
	}

	seekErrCh := make(chan error, 1)
	go func() { seekErrCh <- client.Seek(ctx, 128.5) }()
	m, _ = DecodeCastMessage(s1)
	if err := <-seekErrCh; err != nil {
		t.Fatalf("seek failed: %v", err)
	}
	if m.DestinationID != "t1" || m.Namespace != NamespaceMedia || !strings.Contains(m.PayloadUTF8, `"SEEK"`) || !strings.Contains(m.PayloadUTF8, "128.5") {
		t.Errorf("unexpected seek frame: %+v", m)
	}
	if client.lastPosition != 128.5 || client.lastPositionTime.IsZero() {
		t.Errorf("expected recorded position 128.5, got %f", client.lastPosition)
	}

	// Test rewind and fast_forward dispatch SEEK frames (#605)
	client.lastPosition = 100.0
	m = sendAction("rewind")
	if m.DestinationID != "t1" || m.Namespace != NamespaceMedia || !strings.Contains(m.PayloadUTF8, `"SEEK"`) || !strings.Contains(m.PayloadUTF8, "90") {
		t.Errorf("expected SEEK frame around 90 for rewind, got %+v", m)
	}

	client.lastPosition = 100.0
	m = sendAction("fast_forward")
	if m.DestinationID != "t1" || m.Namespace != NamespaceMedia || !strings.Contains(m.PayloadUTF8, `"SEEK"`) || !strings.Contains(m.PayloadUTF8, "110") {
		t.Errorf("expected SEEK frame around 110 for fast_forward, got %+v", m)
	}

	// Test rewind clamping at 0 and playing drift calculation
	client.playerState = "playing"
	client.lastPosition = 5.0
	client.lastPositionTime = time.Now()
	m = sendAction("rewind")
	if !strings.Contains(m.PayloadUTF8, `"SEEK"`) || !strings.Contains(m.PayloadUTF8, `"currentTime":0`) {
		t.Errorf("expected clamped SEEK 0 frame for rewind, got %+v", m)
	}
}

func TestCastClient_Wake(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	client := NewCastClient(ClientConfig{})
	if err := client.Wake(ctx); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("expected ErrNotConnected, got: %v", err)
	}

	c1, s1 := net.Pipe()
	defer c1.Close()
	defer s1.Close()
	client.conn, client.connected = c1, true
	for _, st := range []struct{ app, ps string }{{"CC1AD845", ""}, {"", "playing"}, {"", "buffering"}} {
		client.appID, client.playerState = st.app, st.ps
		if err := client.Wake(ctx); err != nil {
			t.Fatalf("expected nil wake for active state %+v, got %v", st, err)
		}
	}
	client.appID, client.playerState = "", ""

	client.cfg.WakeDelay = 2 * time.Millisecond
	errCh := make(chan error, 1)
	go func() { errCh <- client.Wake(ctx) }()

	launchMsg, _ := DecodeCastMessage(s1)
	if !strings.Contains(launchMsg.PayloadUTF8, "LAUNCH") {
		t.Errorf("expected LAUNCH, got %s", launchMsg.PayloadUTF8)
	}
	stopMsg, _ := DecodeCastMessage(s1)
	if !strings.Contains(stopMsg.PayloadUTF8, "STOP") {
		t.Errorf("expected STOP, got %s", stopMsg.PayloadUTF8)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("wake failed: %v", err)
	}
	if err := client.Wake(ctx); err != nil {
		t.Fatalf("expected nil on debounce, got: %v", err)
	}

	c2, s2 := net.Pipe()
	defer c2.Close()
	defer s2.Close()
	client2 := NewCastClient(ClientConfig{WakeDelay: 500 * time.Millisecond})
	client2.conn, client2.connected = c2, true
	ctxCancel, cancel := context.WithCancel(context.Background())
	go func() {
		_, _ = DecodeCastMessage(s2)
		cancel()
	}()
	if err := client2.Wake(ctxCancel); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}

	c3, s3 := net.Pipe()
	_ = s3.Close()
	c3.Close()
	client3 := NewCastClient(ClientConfig{WakeDelay: 2 * time.Millisecond})
	client3.conn, client3.connected = c3, true
	if err := client3.Wake(ctx); err == nil {
		t.Error("expected error on closed conn for LAUNCH")
	}

	c4, s4 := net.Pipe()
	defer c4.Close()
	client4 := NewCastClient(ClientConfig{WakeDelay: 2 * time.Millisecond})
	client4.conn, client4.connected = c4, true
	go func() {
		_, _ = DecodeCastMessage(s4)
		_ = s4.Close()
	}()
	if err := client4.Wake(ctx); err == nil {
		t.Error("expected error on closed conn for STOP")
	}
}

func TestCastClient_MediaMetadataAndThrottling(t *testing.T) {
	var mu sync.Mutex
	var received []map[string]any
	coreServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		received = append(received, body)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer coreServer.Close()

	client := NewCastClient(ClientConfig{CoreURL: coreServer.URL})
	drain := func() {
		for {
			select {
			case fn := <-client.dispatchCh:
				fn()
			default:
				return
			}
		}
	}

	steps := []struct {
		name      string
		payload   string
		trigger   bool
		wantCount int
		wantState string
		wantTitle string
		wantSer   string
		wantS     string
		wantEp    string
		wantPos   float64
		wantDur   float64
	}{
		{"initial_rich", `{"type":"MEDIA_STATUS","status":[{"mediaSessionId":1,"playerState":"PLAYING","currentTime":10.0,"media":{"duration":3600.0,"metadata":{"title":"Chapter 1","seriesTitle":"Mando","season":2,"episode":3}}}]}`, false, 1, "playing", "Chapter 1", "Mando", "2", "3", 10.0, 0},
		{"normal_progress", `{"type":"MEDIA_STATUS","status":[{"mediaSessionId":1,"playerState":"PLAYING","currentTime":11.0}]}`, false, 1, "", "", "", "", "", 0, 0},
		{"seek_drift", `{"type":"MEDIA_STATUS","status":[{"mediaSessionId":1,"playerState":"PLAYING","currentTime":50.0}]}`, false, 2, "", "Chapter 1", "", "", "", 50.0, 0},
		{"paused", `{"type":"MEDIA_STATUS","status":[{"mediaSessionId":1,"playerState":"PAUSED","currentTime":50.0}]}`, false, 3, "paused", "", "", "", "", 0, 0},
		{"title_change", `{"type":"MEDIA_STATUS","status":[{"mediaSessionId":1,"playerState":"PAUSED","currentTime":0.0,"media":{"duration":3600.0,"metadata":{"title":"Chapter 2","seriesTitle":"Mando","season":"2","episode":"4"}}}]}`, false, 4, "", "Chapter 2", "", "", "4", 0, 0},
		{"same_session_movie", `{"type":"MEDIA_STATUS","status":[{"mediaSessionId":1,"playerState":"PLAYING","currentTime":0.0,"media":{"duration":5400.0,"metadata":{"title":"Movie In Same Session"}}}]}`, false, 5, "playing", "Movie In Same Session", "", "", "", 0, 5400.0},
		{"new_session", `{"type":"MEDIA_STATUS","status":[{"mediaSessionId":2,"playerState":"PLAYING","currentTime":0.0,"media":{"duration":1800.0,"metadata":{"title":"New Session","seriesTitle":"Show B"}}}]}`, false, 6, "", "New Session", "", "", "", 0, 1800.0},
		{"dispatch_trigger", "", true, 7, "", "New Session", "Show B", "", "", 0, 0},
		{"transient_idle", `{"type":"MEDIA_STATUS","status":[{"mediaSessionId":2,"playerState":"IDLE"}]}`, false, 7, "", "", "", "", "", 0, 0},
		{"transient_buffering", `{"type":"MEDIA_STATUS","status":[{"mediaSessionId":2,"playerState":"BUFFERING","currentTime":0.0}]}`, false, 8, "buffering", "New Session", "", "", "", 0, 1800.0},
		{"transient_playing", `{"type":"MEDIA_STATUS","status":[{"mediaSessionId":2,"playerState":"PLAYING","currentTime":1.0}]}`, false, 9, "playing", "New Session", "", "", "", 0, 1800.0},
		{"idle_purges", `{"type":"MEDIA_STATUS","status":[{"mediaSessionId":0,"playerState":"IDLE"}]}`, false, 10, "idle", "", "", "", "", 0, 0},
		{"repeat_terminal_idle", `{"type":"MEDIA_STATUS","status":[{"mediaSessionId":2,"playerState":"IDLE","idleReason":"FINISHED","media":{"duration":1800.0,"metadata":{"title":"Stale Finished"}}}]}`, false, 10, "", "", "", "", "", 0, 0},
		{"post_idle_trigger", "", true, 11, "", "", "", "", "", 0, 0},
	}

	for _, step := range steps {
		if step.trigger {
			client.dispatchTrigger()
		} else {
			client.handleMediaMessage(CastMessage{PayloadUTF8: step.payload})
		}
		drain()
		mu.Lock()
		if len(received) != step.wantCount {
			t.Fatalf("[%s] expected %d dispatches, got %d", step.name, step.wantCount, len(received))
		}
		if step.wantCount > 0 && len(received) > 0 {
			last := received[len(received)-1]
			if step.wantState != "" && last["player_state"] != step.wantState {
				t.Errorf("[%s] state=%v, want %s", step.name, last["player_state"], step.wantState)
			}
			if step.wantTitle != "" && last["title"] != step.wantTitle {
				t.Errorf("[%s] title=%v, want %s", step.name, last["title"], step.wantTitle)
			}
			if step.wantSer != "" && last["series"] != step.wantSer {
				t.Errorf("[%s] series=%v, want %s", step.name, last["series"], step.wantSer)
			}
			if step.wantS != "" && last["season"] != step.wantS {
				t.Errorf("[%s] season=%v, want %s", step.name, last["season"], step.wantS)
			}
			if step.wantEp != "" && last["episode"] != step.wantEp {
				t.Errorf("[%s] episode=%v, want %s", step.name, last["episode"], step.wantEp)
			}
			if step.wantPos != 0 && last["position"] != step.wantPos {
				t.Errorf("[%s] position=%v, want %f", step.name, last["position"], step.wantPos)
			}
			if step.wantDur != 0 && last["duration"] != step.wantDur {
				t.Errorf("[%s] duration=%v, want %f", step.name, last["duration"], step.wantDur)
			}
			if step.name == "same_session_movie" && (last["series"] != "" || last["season"] != "" || last["episode"] != "") {
				t.Errorf("[%s] expected cleared series/season/episode, got %+v", step.name, last)
			}
			if step.name == "post_idle_trigger" && (last["title"] != nil || last["duration"] != nil) {
				t.Errorf("[%s] expected cleared metadata after repeat terminal idle, got %+v", step.name, last)
			}
		}
		mu.Unlock()
	}
}

func TestExtractMediaMetadata(t *testing.T) {
	tests := []struct {
		name                          string
		input                         map[string]any
		wantT, wantSer, wantS, wantEp string
	}{
		{"nil", nil, "", "", "", ""},
		{"standard_tv", map[string]any{"title": "Ep Title", "seriesTitle": "Show", "season": 2, "episode": 4}, "Ep Title", "Show", "2", "4"},
		{"alternate_keys", map[string]any{"episode_title": "Pilot", "series_title": "BB", "season_number": "1", "episode_number": "1"}, "Pilot", "BB", "1", "1"},
		{"paren_episode", map[string]any{"title": "Title (Episode 4)", "seriesTitle": "Show"}, "Title", "Show", "", "4"},
		{"sxxexx", map[string]any{"title": "Show S02E04: Title"}, "Title", "Show", "02", "04"},
		{"episode_lead", map[string]any{"title": "Episode 4: Title", "subtitle": "Show"}, "Title", "Show", "", "4"},
		{"movie", map[string]any{"title": "Inception"}, "Inception", "", "", ""},
		{"series_colon_season_ep", map[string]any{"title": "Show: Season 1, Episode 5 - Title"}, "Title", "Show", "1", "5"},
		{"series_colon_ep_only", map[string]any{"title": "Show: Episode 5: Title"}, "Title", "Show", "", "5"},
		{"subtitle_season_ep", map[string]any{"title": "Ep", "subtitle": "Season 2, Episode 3"}, "Ep", "", "2", "3"},
		{"subtitle_ep_only", map[string]any{"title": "Ep", "subtitle": "Episode 7"}, "Ep", "", "", "7"},
		{"paren_season_ep", map[string]any{"title": "Match (Season 3, Episode 9)"}, "Match", "", "3", "9"},
		{"types_variations", map[string]any{"title": "Types", "series": json.Number("42"), "season_index": float64(3), "episode_number": int(12)}, "Types", "42", "3", "12"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotT, gotSer, gotS, gotEp := extractMediaMetadata(tc.input)
			if gotT != tc.wantT || gotSer != tc.wantSer || gotS != tc.wantS || gotEp != tc.wantEp {
				t.Fatalf("got (%q,%q,%q,%q), want (%q,%q,%q,%q)", gotT, gotSer, gotS, gotEp, tc.wantT, tc.wantSer, tc.wantS, tc.wantEp)
			}
		})
	}
}

func TestCastClient_WatchdogTrigger(t *testing.T) {
	pd := newPipeDialer()
	client := NewCastClient(ClientConfig{
		ChromecastAddr:    "127.0.0.1:8009",
		Dialer:            pd.Dial,
		HeartbeatInterval: 5 * time.Millisecond,
		WatchdogTimeout:   10 * time.Millisecond,
		ReconnectBase:     20 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client.Start(ctx)
	defer client.Close()
	serverConn := pd.serverConn
	defer serverConn.Close()

	_, _ = DecodeCastMessage(serverConn)
	_, _ = DecodeCastMessage(serverConn)

	_ = serverConn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	var buf [1]byte
	for {
		if _, err := serverConn.Read(buf[:]); err != nil {
			break
		}
	}
}

func TestCastClient_SupervisorReconnect(t *testing.T) {
	attempts := 0
	c1, s1 := net.Pipe()
	defer s1.Close()
	connectedCh := make(chan struct{}, 1)
	mockDialer := func(ctx context.Context, network, addr string) (net.Conn, error) {
		attempts++
		if attempts == 1 {
			return nil, errors.New("dial failed")
		}
		select {
		case connectedCh <- struct{}{}:
		default:
		}
		return c1, nil
	}

	client := NewCastClient(ClientConfig{ChromecastAddr: "127.0.0.1:8009", Dialer: mockDialer, ReconnectBase: 2 * time.Millisecond, ReconnectMax: 5 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client.Start(ctx)
	select {
	case <-connectedCh:
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for reconnect")
	}
	client.Close()
}

type errConn struct{ net.Conn }

func (e *errConn) Close() error { return errors.New("conn close failure") }

func TestCastClient_LifecycleAndEdgeCases(t *testing.T) {
	client := NewCastClient(ClientConfig{})
	client.Close()
	client.Start(context.Background())

	cErr := NewCastClient(ClientConfig{})
	cErr.conn = &errConn{}
	cErr.Close()

	cDial := NewCastClient(ClientConfig{ChromecastAddr: "127.0.0.1:65534"})
	ctxT, cancelT := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelT()
	_, _ = cDial.dialer(ctxT, "tcp", "127.0.0.1:65534")

	cBack := NewCastClient(ClientConfig{ReconnectBase: 5 * time.Millisecond, ReconnectMax: 2 * time.Millisecond, ChromecastAddr: "127.0.0.1:65534"})
	ctxB, cancelB := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancelB()
	cBack.Start(ctxB)

	cQueue := NewCastClient(ClientConfig{})
	for i := 0; i < 64; i++ {
		cQueue.dispatchCh <- func() {}
	}
	called := make(chan struct{})
	cQueue.queueDispatch(func() { close(called) })
	<-called

	var capturedTrigger map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/video/trigger" {
			_ = json.NewDecoder(r.Body).Decode(&capturedTrigger)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	cDefaults := NewCastClient(ClientConfig{CoreURL: ts.URL})
	cDefaults.dispatchTrigger()
	if capturedTrigger["stream_url"] != "http://localhost:1984/api/webrtc?src=cast" {
		t.Errorf("unexpected default stream_url: %v", capturedTrigger["stream_url"])
	}

	if err := cDefaults.sendMessage(CastMessage{PayloadUTF8: string(make([]byte, MaxPayloadSize+100))}); err == nil {
		t.Error("expected error for oversized frame")
	}

	cDefaults.handleReceiverMessage(CastMessage{Namespace: NamespaceReceiver, PayloadUTF8: `{"type":"OTHER"}`})
	cDefaults.handleReceiverMessage(CastMessage{Namespace: NamespaceReceiver, PayloadUTF8: `{invalid`})
	cDefaults.handleHeartbeatMessage(CastMessage{Namespace: NamespaceHeartbeat, PayloadUTF8: `{"type":"UNKNOWN"}`})
	cDefaults.handleHeartbeatMessage(CastMessage{PayloadUTF8: `{"type":"PING"}`})
	cDefaults.handleMediaMessage(CastMessage{Namespace: NamespaceMedia, PayloadUTF8: `{"type":"MEDIA_STATUS","status":[]}`})
	cDefaults.handleMediaMessage(CastMessage{Namespace: NamespaceMedia, PayloadUTF8: `{invalid`})
	cDefaults.handleMediaMessage(CastMessage{Namespace: NamespaceMedia, PayloadUTF8: `{"type":"MEDIA_STATUS","status":[{"mediaSessionId":10,"playerState":"IDLE","idleReason":"FINISHED"}]}`})
	if cDefaults.playerState != "idle" {
		t.Errorf("expected idle state, got %s", cDefaults.playerState)
	}

	downServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	cFail := NewCastClient(ClientConfig{CoreURL: downServer.URL, HTTPClient: downServer.Client()})
	cFail.lastReportedPlayerState, cFail.lastReportedTitle, cFail.lastTriggered = "playing", "Ep 1", true
	cFail.dispatchTrigger()
	cFail.dispatchDismiss()
	cFail.dispatchPlayerState("playing")
	if cFail.lastReportedPlayerState != "" || cFail.lastTriggered {
		t.Errorf("expected state reset on 502 webhook failure")
	}
	downServer.Close()
	cFail.postToCore(downServer.URL+"/api/video/state", []byte(`{}`), "state")
	cFail.postToCore(downServer.URL+"/api/video/trigger", []byte(`{}`), "trigger")
}
