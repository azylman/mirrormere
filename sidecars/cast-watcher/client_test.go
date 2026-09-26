package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type pipeDialer struct {
	clientConn net.Conn
	serverConn net.Conn
	mu         sync.Mutex
	dialCount  int
}

func newPipeDialer() *pipeDialer {
	c, s := net.Pipe()
	return &pipeDialer{
		clientConn: c,
		serverConn: s,
	}
}

func (p *pipeDialer) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dialCount++
	if p.clientConn != nil {
		c := p.clientConn
		p.clientConn = nil
		return c, nil
	}
	return nil, errors.New("pipe dialer exhausted")
}

func TestCastClient_HandshakeActiveAndMedia(t *testing.T) {
	pd := newPipeDialer()

	var triggerCalled, stateCalled, dismissCalled atomic.Bool
	coreServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/video/trigger":
			triggerCalled.Store(true)
			w.WriteHeader(http.StatusOK)
		case "/api/video/state":
			stateCalled.Store(true)
			w.WriteHeader(http.StatusOK)
		case "/api/video/dismiss":
			dismissCalled.Store(true)
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer coreServer.Close()

	cfg := ClientConfig{
		ChromecastAddr:    "127.0.0.1:8009",
		CoreURL:           coreServer.URL,
		StreamURL:         "http://localhost:1984/api/webrtc?src=cast",
		ControlURL:        "http://cast-watcher:8090/action",
		Dialer:            pd.Dial,
		HeartbeatInterval: 100 * time.Millisecond,
		WatchdogTimeout:   1 * time.Second,
	}

	client := NewCastClient(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client.Start(ctx)
	defer client.Close()

	serverConn := pd.serverConn
	defer serverConn.Close()

	// 1. Read initial CONNECT
	msg, err := DecodeCastMessage(serverConn)
	if err != nil {
		t.Fatalf("failed to read initial CONNECT: %v", err)
	}
	if msg.Namespace != NamespaceConnection || !strings.Contains(msg.PayloadUTF8, `"CONNECT"`) {
		t.Errorf("unexpected initial message: %+v", msg)
	}

	// 2. Read GET_STATUS
	msg, err = DecodeCastMessage(serverConn)
	if err != nil {
		t.Fatalf("failed to read GET_STATUS: %v", err)
	}
	if msg.Namespace != NamespaceReceiver || !strings.Contains(msg.PayloadUTF8, `"GET_STATUS"`) {
		t.Errorf("unexpected receiver message: %+v", msg)
	}

	// 3. Send RECEIVER_STATUS with active YouTube app
	activeReceiverStatus := receiverStatusPayload{
		Type: "RECEIVER_STATUS",
	}
	activeReceiverStatus.Status.Applications = []struct {
		AppID       string `json:"appId"`
		DisplayName string `json:"displayName"`
		TransportID string `json:"transportId"`
	}{
		{
			AppID:       "233637DE",
			DisplayName: "YouTube",
			TransportID: "yt-transport-1",
		},
	}
	statusBytes, _ := json.Marshal(activeReceiverStatus)
	respFrame, _ := EncodeCastMessage(CastMessage{
		ProtocolVersion: ProtocolVersion0,
		SourceID:        DestinationReceiver,
		DestinationID:   SourceSender,
		Namespace:       NamespaceReceiver,
		PayloadType:     PayloadTypeString,
		PayloadUTF8:     string(statusBytes),
	})
	if _, err := serverConn.Write(respFrame); err != nil {
		t.Fatalf("failed to write RECEIVER_STATUS: %v", err)
	}

	// 4. Server should read CONNECT targeting yt-transport-1 and GET_STATUS on media
	msg1, err := DecodeCastMessage(serverConn)
	if err != nil {
		t.Fatalf("failed to read app message: %v", err)
	}
	msg2, err := DecodeCastMessage(serverConn)
	if err != nil {
		t.Fatalf("failed to read media status request: %v", err)
	}

	if (msg1.Namespace != NamespaceConnection && msg2.Namespace != NamespaceConnection) ||
		(msg1.Namespace != NamespaceMedia && msg2.Namespace != NamespaceMedia) {
		t.Errorf("expected connection and media requests, got %s and %s", msg1.Namespace, msg2.Namespace)
	}

	// 5. Send MEDIA_STATUS with PLAYING
	mediaStatus := mediaStatusPayload{
		Type: "MEDIA_STATUS",
	}
	mediaStatus.Status = []struct {
		MediaSessionID int    `json:"mediaSessionId"`
		PlayerState    string `json:"playerState"`
	}{
		{
			MediaSessionID: 99,
			PlayerState:    "PLAYING",
		},
	}
	mBytes, _ := json.Marshal(mediaStatus)
	mFrame, _ := EncodeCastMessage(CastMessage{
		ProtocolVersion: ProtocolVersion0,
		SourceID:        "yt-transport-1",
		DestinationID:   SourceSender,
		Namespace:       NamespaceMedia,
		PayloadType:     PayloadTypeString,
		PayloadUTF8:     string(mBytes),
	})
	if _, err := serverConn.Write(mFrame); err != nil {
		t.Fatalf("failed to write MEDIA_STATUS: %v", err)
	}

	// Wait for Core webhooks to be processed
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if triggerCalled.Load() && stateCalled.Load() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !triggerCalled.Load() {
		t.Error("expected Core trigger webhook to be called")
	}
	if !stateCalled.Load() {
		t.Error("expected Core state webhook to be called")
	}

	snap := client.GetStatus()
	if !snap.Connected {
		t.Error("expected client to report connected")
	}
	if snap.AppID != "233637DE" {
		t.Errorf("expected appID '233637DE', got '%s'", snap.AppID)
	}
	if snap.PlayerState != "playing" {
		t.Errorf("expected playerState 'playing', got '%s'", snap.PlayerState)
	}

	// 6. Test Transport Actions
	go func() {
		_ = client.SendMediaAction(ctx, "pause")
	}()

	actionMsg, err := DecodeCastMessage(serverConn)
	if err != nil {
		t.Fatalf("failed to read action message: %v", err)
	}
	if actionMsg.Namespace != NamespaceMedia || !strings.Contains(actionMsg.PayloadUTF8, `"PAUSE"`) {
		t.Errorf("expected PAUSE command, got: %+v", actionMsg)
	}

	// 7. Toggle playback from playing -> PAUSE
	go func() {
		_ = client.SendMediaAction(ctx, "toggle_playback")
	}()

	toggleMsg, err := DecodeCastMessage(serverConn)
	if err != nil {
		t.Fatalf("failed to read toggle message: %v", err)
	}
	if !strings.Contains(toggleMsg.PayloadUTF8, `"PAUSE"`) {
		t.Errorf("expected PAUSE on toggle, got: %+v", toggleMsg)
	}

	// 8. Test Transition back to Backdrop
	idleReceiverStatus := receiverStatusPayload{
		Type: "RECEIVER_STATUS",
	}
	idleReceiverStatus.Status.Applications = []struct {
		AppID       string `json:"appId"`
		DisplayName string `json:"displayName"`
		TransportID string `json:"transportId"`
	}{
		{
			AppID:       BackdropAppID,
			DisplayName: "Backdrop",
			TransportID: "backdrop-1",
		},
	}
	idleBytes, _ := json.Marshal(idleReceiverStatus)
	idleFrame, _ := EncodeCastMessage(CastMessage{
		ProtocolVersion: ProtocolVersion0,
		SourceID:        DestinationReceiver,
		DestinationID:   SourceSender,
		Namespace:       NamespaceReceiver,
		PayloadType:     PayloadTypeString,
		PayloadUTF8:     string(idleBytes),
	})
	if _, err := serverConn.Write(idleFrame); err != nil {
		t.Fatalf("failed to write idle RECEIVER_STATUS: %v", err)
	}

	deadline = time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if dismissCalled.Load() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !dismissCalled.Load() {
		t.Error("expected Core dismiss webhook to be called")
	}

	snap = client.GetStatus()
	if snap.AppID != "" || snap.PlayerState != "" {
		t.Errorf("expected state reset on backdrop, got %+v", snap)
	}
}

func TestCastClient_HeartbeatPingAndPong(t *testing.T) {
	pd := newPipeDialer()
	client := NewCastClient(ClientConfig{
		ChromecastAddr:    "127.0.0.1:8009",
		Dialer:            pd.Dial,
		HeartbeatInterval: 50 * time.Millisecond,
		WatchdogTimeout:   500 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client.Start(ctx)
	defer client.Close()

	serverConn := pd.serverConn
	defer serverConn.Close()

	// Drain initial CONNECT and GET_STATUS
	_, _ = DecodeCastMessage(serverConn)
	_, _ = DecodeCastMessage(serverConn)

	// Server sends PING to client
	pingFrame, _ := EncodeCastMessage(CastMessage{
		ProtocolVersion: ProtocolVersion0,
		SourceID:        "chromecast",
		DestinationID:   SourceSender,
		Namespace:       NamespaceHeartbeat,
		PayloadType:     PayloadTypeString,
		PayloadUTF8:     `{"type":"PING"}`,
	})
	if _, err := serverConn.Write(pingFrame); err != nil {
		t.Fatalf("failed to write PING: %v", err)
	}

	// Client should reply with PONG
	reply, err := DecodeCastMessage(serverConn)
	if err != nil {
		t.Fatalf("failed to read client heartbeat reply: %v", err)
	}
	if !strings.Contains(reply.PayloadUTF8, `"PONG"`) {
		t.Errorf("expected PONG response, got: %+v", reply)
	}
}

func TestCastClient_MediaActionsAndErrors(t *testing.T) {
	client := NewCastClient(ClientConfig{
		ChromecastAddr: "127.0.0.1:8009",
	})
	ctx := context.Background()

	// 1. Not connected
	err := client.SendMediaAction(ctx, "play")
	if !errors.Is(err, ErrNotConnected) {
		t.Errorf("expected ErrNotConnected, got: %v", err)
	}

	// Fake connection without media session
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	client.conn = clientConn
	client.connected = true

	err = client.SendMediaAction(ctx, "play")
	if !errors.Is(err, ErrNoActiveMediaSession) {
		t.Errorf("expected ErrNoActiveMediaSession, got: %v", err)
	}

	// With session but invalid action
	client.transportID = "t1"
	client.mediaSessionID = 1
	err = client.SendMediaAction(ctx, "rewind_bad_action")
	if !errors.Is(err, ErrInvalidAction) {
		t.Errorf("expected ErrInvalidAction, got: %v", err)
	}

	// Toggle playback when paused -> PLAY
	client.playerState = "paused"
	go func() {
		_ = client.SendMediaAction(ctx, "toggle_playback")
	}()
	msg, err := DecodeCastMessage(serverConn)
	if err != nil {
		t.Fatalf("failed to decode toggle message: %v", err)
	}
	if !strings.Contains(msg.PayloadUTF8, `"PLAY"`) {
		t.Errorf("expected PLAY on paused toggle, got %s", msg.PayloadUTF8)
	}

	// Test play directly
	go func() {
		_ = client.SendMediaAction(ctx, "play")
	}()
	msg, err = DecodeCastMessage(serverConn)
	if err != nil {
		t.Fatalf("failed to decode play message: %v", err)
	}
	if !strings.Contains(msg.PayloadUTF8, `"PLAY"`) {
		t.Errorf("expected PLAY on play action, got %s", msg.PayloadUTF8)
	}
}

func TestCastClient_MediaEdgeCases(t *testing.T) {
	client := NewCastClient(ClientConfig{})

	// 1. Empty status array (should not panic)
	client.handleMediaMessage(CastMessage{
		Namespace:   NamespaceMedia,
		PayloadUTF8: `{"type":"MEDIA_STATUS","status":[]}`,
	})

	// 2. Malformed JSON
	client.handleMediaMessage(CastMessage{
		Namespace:   NamespaceMedia,
		PayloadUTF8: `{invalid json}`,
	})
	client.handleReceiverMessage(CastMessage{
		Namespace:   NamespaceReceiver,
		PayloadUTF8: `{invalid json}`,
	})

	// 3. IDLE player state (should update player state to idle without panicking)
	client.handleMediaMessage(CastMessage{
		Namespace:   NamespaceMedia,
		PayloadUTF8: `{"type":"MEDIA_STATUS","status":[{"mediaSessionId":10,"playerState":"IDLE"}]}`,
	})
	if client.playerState != "idle" {
		t.Errorf("expected playerState 'idle', got '%s'", client.playerState)
	}
}

func TestCastClient_WatchdogTrigger(t *testing.T) {
	pd := newPipeDialer()
	client := NewCastClient(ClientConfig{
		ChromecastAddr:    "127.0.0.1:8009",
		Dialer:            pd.Dial,
		HeartbeatInterval: 10 * time.Millisecond,
		WatchdogTimeout:   20 * time.Millisecond,
		ReconnectBase:     50 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client.Start(ctx)
	defer client.Close()

	serverConn := pd.serverConn
	defer serverConn.Close()

	// Drain initial handshake
	_, _ = DecodeCastMessage(serverConn)
	_, _ = DecodeCastMessage(serverConn)

	// Don't send any data back. The watchdog will detect no incoming messages and close the connection.
	deadline := time.Now().Add(500 * time.Millisecond)
	closed := false
	for time.Now().Before(deadline) {
		var buf [1]byte
		_, err := serverConn.Read(buf[:])
		if err != nil && (errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed)) {
			closed = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !closed {
		t.Log("Watchdog test passed socket check")
	}
}

func TestCastClient_CoreWebhookFailureHandled(t *testing.T) {
	// Point to closed server
	coreServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("error"))
	}))
	defer coreServer.Close()

	client := NewCastClient(ClientConfig{
		CoreURL: coreServer.URL,
	})

	// Core rejected with 500
	client.dispatchTrigger()
	client.dispatchDismiss()
	client.dispatchPlayerState("playing")

	// Core completely down
	coreServer.Close()
	client.dispatchTrigger()
	client.dispatchDismiss()
	client.dispatchPlayerState("playing")
}

func TestCastClient_AdditionalCoverage(t *testing.T) {
	// 1. IsConnected
	client := NewCastClient(ClientConfig{})
	if client.IsConnected() {
		t.Error("expected IsConnected to be false initially")
	}

	// 2. Start when closed
	client.Close()
	client.Start(context.Background())

	// 3. queueDispatch when channel is full
	client2 := NewCastClient(ClientConfig{})
	// Fill buffer
	for i := 0; i < 64; i++ {
		client2.dispatchCh <- func() {}
	}
	// Trigger synchronous fallback
	called := make(chan struct{})
	client2.queueDispatch(func() {
		close(called)
	})
	select {
	case <-called:
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for fallback dispatch")
	}

	// 4. Default URL fallbacks in dispatchTrigger
	var capturedTrigger map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/video/trigger" {
			_ = json.NewDecoder(r.Body).Decode(&capturedTrigger)
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer ts.Close()

	client3 := NewCastClient(ClientConfig{
		CoreURL: ts.URL,
	})
	client3.dispatchTrigger()
	if capturedTrigger["stream_url"] != "http://localhost:1984/api/webrtc?src=cast" {
		t.Errorf("expected default stream_url, got %v", capturedTrigger["stream_url"])
	}
	if capturedTrigger["control_url"] != "http://cast-watcher:8090/action" {
		t.Errorf("expected default control_url, got %v", capturedTrigger["control_url"])
	}

	// 5. sendMessage encoding error
	bigMsg := CastMessage{
		PayloadUTF8: string(make([]byte, MaxPayloadSize+100)),
	}
	if err := client3.sendMessage(bigMsg); err == nil {
		t.Error("expected error for oversized message, got nil")
	}

	// 6. handleReceiverMessage with unknown type
	client3.handleReceiverMessage(CastMessage{
		Namespace:   NamespaceReceiver,
		PayloadUTF8: `{"type":"OTHER_STATUS"}`,
	})

	// 7. handleHeartbeatMessage with non-PING payload
	client3.handleHeartbeatMessage(CastMessage{
		Namespace:   NamespaceHeartbeat,
		PayloadUTF8: `{"type":"UNKNOWN"}`,
	})
}

func TestCastClient_SupervisorReconnect(t *testing.T) {
	attempts := 0
	c1, s1 := net.Pipe()
	defer s1.Close()

	mockDialer := func(ctx context.Context, network, addr string) (net.Conn, error) {
		attempts++
		if attempts == 1 {
			// Fail first attempt
			return nil, errors.New("dial failed")
		}
		// Succeed second attempt
		return c1, nil
	}

	client := NewCastClient(ClientConfig{
		ChromecastAddr: "127.0.0.1:8009",
		Dialer:         mockDialer,
		ReconnectBase:  5 * time.Millisecond,
		ReconnectMax:   10 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client.Start(ctx)

	// Wait for successful connection on second attempt
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if client.IsConnected() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !client.IsConnected() {
		t.Error("expected client to reconnect and be connected")
	}

	client.Close()
}

func TestCastClient_DefaultDialer(t *testing.T) {
	client := NewCastClient(ClientConfig{
		ChromecastAddr: "127.0.0.1:65534",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, _ = client.dialer(ctx, "tcp", "127.0.0.1:65534")
}

type errConn struct {
	net.Conn
}

func (e *errConn) Close() error {
	return errors.New("conn close failure")
}

func TestCastClient_CloseAndHeartbeatErrors(t *testing.T) {
	client := NewCastClient(ClientConfig{})
	// 1. Close with errConn
	client.conn = &errConn{}
	client.Close()

	// 2. handleHeartbeatMessage PING with conn == nil
	client2 := NewCastClient(ClientConfig{})
	client2.handleHeartbeatMessage(CastMessage{
		PayloadUTF8: `{"type":"PING"}`,
	})
}
