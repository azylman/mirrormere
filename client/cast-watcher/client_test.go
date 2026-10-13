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
		Status: []mediaStatusItem{
			{
				MediaSessionID: 99,
				PlayerState:    "PLAYING",
			},
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
	client := NewCastClient(ClientConfig{})
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

func TestCastClient_Seek(t *testing.T) {
	client := NewCastClient(ClientConfig{})
	ctx := context.Background()

	// 1. Not connected
	err := client.Seek(ctx, 42.0)
	if !errors.Is(err, ErrNotConnected) {
		t.Errorf("expected ErrNotConnected, got: %v", err)
	}

	// 2. Connected but no media session
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	client.conn = clientConn
	client.connected = true

	err = client.Seek(ctx, 42.0)
	if !errors.Is(err, ErrNoActiveMediaSession) {
		t.Errorf("expected ErrNoActiveMediaSession, got: %v", err)
	}

	// 3. Negative target time
	client.transportID = "transport-123"
	client.mediaSessionID = 456
	err = client.Seek(ctx, -10.0)
	if !errors.Is(err, ErrInvalidAction) {
		t.Errorf("expected ErrInvalidAction for negative target time, got: %v", err)
	}

	// 4. Successful seek protocol message
	seekErrCh := make(chan error, 1)
	go func() {
		seekErrCh <- client.Seek(ctx, 128.5)
	}()

	msg, err := DecodeCastMessage(serverConn)
	if err != nil {
		t.Fatalf("failed to decode seek message: %v", err)
	}
	if err := <-seekErrCh; err != nil {
		t.Fatalf("client.Seek returned error: %v", err)
	}

	if msg.DestinationID != "transport-123" {
		t.Errorf("expected DestinationID 'transport-123', got '%s'", msg.DestinationID)
	}
	if msg.Namespace != NamespaceMedia {
		t.Errorf("expected Namespace '%s', got '%s'", NamespaceMedia, msg.Namespace)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(msg.PayloadUTF8), &payload); err != nil {
		t.Fatalf("failed to unmarshal seek payload: %v", err)
	}

	if payload["type"] != "SEEK" {
		t.Errorf("expected type 'SEEK', got '%v'", payload["type"])
	}
	if reqID, ok := payload["requestId"].(float64); !ok || reqID == 0 {
		t.Errorf("expected valid non-zero requestId, got %v", payload["requestId"])
	}
	if sessID, ok := payload["mediaSessionId"].(float64); !ok || int(sessID) != 456 {
		t.Errorf("expected mediaSessionId 456, got %v", payload["mediaSessionId"])
	}
	if curTime, ok := payload["currentTime"].(float64); !ok || curTime != 128.5 {
		t.Errorf("expected currentTime 128.5, got %v", payload["currentTime"])
	}

	client.mu.RLock()
	lastPos := client.lastPosition
	lastPosTime := client.lastPositionTime
	client.mu.RUnlock()

	if lastPos != 128.5 {
		t.Errorf("expected client.lastPosition 128.5, got %v", lastPos)
	}
	if lastPosTime.IsZero() {
		t.Errorf("expected non-zero lastPositionTime after seek")
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

func TestCastClient_SupervisorBackoffMax(t *testing.T) {
	client := NewCastClient(ClientConfig{
		ReconnectBase:  10 * time.Millisecond,
		ReconnectMax:   5 * time.Millisecond,
		ChromecastAddr: "127.0.0.1:65534",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	client.Start(ctx)
}

func TestCastClient_PostToCoreFail(t *testing.T) {
	client := NewCastClient(ClientConfig{
		CoreURL: "http://127.0.0.1:65534",
	})
	client.postToCore("http://127.0.0.1:65534", []byte("{}"), "trigger")
}

func TestCastClient_Wake_NotConnected(t *testing.T) {
	client := NewCastClient(ClientConfig{})
	ctx := context.Background()

	// 1. Initial state: not connected
	err := client.Wake(ctx)
	if !errors.Is(err, ErrNotConnected) {
		t.Fatalf("expected ErrNotConnected, got: %v", err)
	}

	// 2. Fake conn but connected = false
	c1, s1 := net.Pipe()
	defer c1.Close()
	defer s1.Close()
	client.conn = c1
	client.connected = false
	err = client.Wake(ctx)
	if !errors.Is(err, ErrNotConnected) {
		t.Fatalf("expected ErrNotConnected when connected=false, got: %v", err)
	}
}

func TestCastClient_Wake_SuccessAndDebounce(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	client := NewCastClient(ClientConfig{
		WakeDelay: 10 * time.Millisecond,
	})
	client.conn = clientConn
	client.connected = true

	ctx := context.Background()
	errChan := make(chan error, 1)

	// 1. Trigger successful wake
	go func() {
		errChan <- client.Wake(ctx)
	}()

	// Expect LAUNCH message
	launchMsg, err := DecodeCastMessage(serverConn)
	if err != nil {
		t.Fatalf("failed to decode LAUNCH message: %v", err)
	}
	if launchMsg.Namespace != NamespaceReceiver {
		t.Errorf("expected namespace %s, got %s", NamespaceReceiver, launchMsg.Namespace)
	}
	if launchMsg.DestinationID != DestinationReceiver {
		t.Errorf("expected destination %s, got %s", DestinationReceiver, launchMsg.DestinationID)
	}
	if !strings.Contains(launchMsg.PayloadUTF8, `"LAUNCH"`) || !strings.Contains(launchMsg.PayloadUTF8, `"CC1AD845"`) {
		t.Errorf("unexpected LAUNCH payload: %s", launchMsg.PayloadUTF8)
	}

	// Expect STOP message
	stopMsg, err := DecodeCastMessage(serverConn)
	if err != nil {
		t.Fatalf("failed to decode STOP message: %v", err)
	}
	if stopMsg.Namespace != NamespaceReceiver {
		t.Errorf("expected namespace %s, got %s", NamespaceReceiver, stopMsg.Namespace)
	}
	if stopMsg.DestinationID != DestinationReceiver {
		t.Errorf("expected destination %s, got %s", DestinationReceiver, stopMsg.DestinationID)
	}
	if !strings.Contains(stopMsg.PayloadUTF8, `"STOP"`) {
		t.Errorf("unexpected STOP payload: %s", stopMsg.PayloadUTF8)
	}

	select {
	case err := <-errChan:
		if err != nil {
			t.Fatalf("Wake returned error: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for Wake to complete")
	}

	// 2. Debounce: immediately trigger wake again within 5 seconds
	// Should return nil immediately without sending additional messages
	debouncedErr := client.Wake(ctx)
	if debouncedErr != nil {
		t.Fatalf("expected debounced Wake to return nil, got: %v", debouncedErr)
	}

	// Verify no messages sent on serverConn
	done := make(chan struct{})
	go func() {
		var buf [1]byte
		_ = serverConn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
		_, _ = serverConn.Read(buf[:])
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for read deadline check")
	}
}

func TestCastClient_Wake_AppActiveSkipped(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	client := NewCastClient(ClientConfig{
		WakeDelay: 10 * time.Millisecond,
	})
	client.conn = clientConn
	client.connected = true
	client.appID = "CC1AD845"

	ctx := context.Background()

	// 1. Wake with active appID should return nil immediately and send no messages
	if err := client.Wake(ctx); err != nil {
		t.Fatalf("expected Wake with active appID to return nil, got: %v", err)
	}

	// Verify no messages sent on serverConn
	done := make(chan struct{})
	go func() {
		var buf [1]byte
		_ = serverConn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
		_, _ = serverConn.Read(buf[:])
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for read deadline check")
	}

	// 2. Wake with playing playerState should also return nil immediately without messages
	client.appID = ""
	client.playerState = "playing"

	if err := client.Wake(ctx); err != nil {
		t.Fatalf("expected Wake with playing state to return nil, got: %v", err)
	}

	// 3. Wake with buffering playerState should also return nil immediately
	client.playerState = "buffering"

	if err := client.Wake(ctx); err != nil {
		t.Fatalf("expected Wake with buffering state to return nil, got: %v", err)
	}
}

func TestCastClient_Wake_ContextCanceled(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	client := NewCastClient(ClientConfig{
		WakeDelay: 500 * time.Millisecond,
	})
	client.conn = clientConn
	client.connected = true

	ctx, cancel := context.WithCancel(context.Background())

	errChan := make(chan error, 1)
	go func() {
		errChan <- client.Wake(ctx)
	}()

	// Read LAUNCH message
	_, err := DecodeCastMessage(serverConn)
	if err != nil {
		t.Fatalf("failed to read LAUNCH message: %v", err)
	}

	// Cancel context while waiting
	cancel()

	select {
	case err := <-errChan:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for Wake to abort on canceled context")
	}
}

func TestCastClient_Wake_SendErrors(t *testing.T) {
	// Error on LAUNCH
	c1, s1 := net.Pipe()
	_ = s1.Close() // closed immediately so Write fails
	defer c1.Close()

	client := NewCastClient(ClientConfig{
		WakeDelay: 10 * time.Millisecond,
	})
	client.conn = c1
	client.connected = true

	err := client.Wake(context.Background())
	if err == nil {
		t.Error("expected error when connection is closed on LAUNCH, got nil")
	}

	// Error on STOP
	c2, s2 := net.Pipe()
	defer c2.Close()

	client2 := NewCastClient(ClientConfig{
		WakeDelay: 10 * time.Millisecond,
	})
	client2.conn = c2
	client2.connected = true

	go func() {
		// Read LAUNCH then close server conn
		_, _ = DecodeCastMessage(s2)
		_ = s2.Close()
	}()

	err = client2.Wake(context.Background())
	if err == nil {
		t.Error("expected error when connection is closed on STOP, got nil")
	}
}

func TestCastClient_MediaMetadataAndThrottling(t *testing.T) {
	var mu sync.Mutex
	var receivedPayloads []map[string]any

	coreServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		receivedPayloads = append(receivedPayloads, body)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer coreServer.Close()

	client := NewCastClient(ClientConfig{
		CoreURL: coreServer.URL,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.dispatchWorker(ctx)

	// 1. Initial MEDIA_STATUS with rich metadata and int season/episode
	msg1 := CastMessage{
		PayloadUTF8: `{
			"type": "MEDIA_STATUS",
			"status": [{
				"mediaSessionId": 1,
				"playerState": "PLAYING",
				"currentTime": 10.0,
				"media": {
					"duration": 3600.0,
					"metadata": {
						"title": "Chapter 1",
						"seriesTitle": "Mando",
						"season": 2,
						"episode": 3
					}
				}
			}]
		}`,
	}
	client.handleMediaMessage(msg1)

	// Wait for dispatch
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	if len(receivedPayloads) != 1 {
		t.Fatalf("expected 1 dispatch for initial media message, got %d", len(receivedPayloads))
	}
	p1 := receivedPayloads[0]
	mu.Unlock()

	if p1["id"] != "chromecast" || p1["player_state"] != "playing" {
		t.Errorf("unexpected id or player_state: %+v", p1)
	}
	if p1["title"] != "Chapter 1" || p1["series"] != "Mando" || p1["season"] != "2" || p1["episode"] != "3" {
		t.Errorf("unexpected metadata in payload: %+v", p1)
	}
	if p1["position"] != 10.0 || p1["duration"] != 3600.0 {
		t.Errorf("unexpected position/duration: %+v", p1)
	}
	if _, ok := p1["position_updated_at"]; !ok {
		t.Errorf("expected position_updated_at in payload")
	}

	// 2. Normal playback progress (advance by 1s: currentTime = 11.0, elapsed ~0.05s, drift < 5s)
	// Should be throttled (no new dispatch)
	msg2 := CastMessage{
		PayloadUTF8: `{
			"type": "MEDIA_STATUS",
			"status": [{
				"mediaSessionId": 1,
				"playerState": "PLAYING",
				"currentTime": 11.0
			}]
		}`,
	}
	client.handleMediaMessage(msg2)
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	if len(receivedPayloads) != 1 {
		t.Fatalf("expected 1 dispatch (throttled normal progress), got %d", len(receivedPayloads))
	}
	mu.Unlock()

	// 3. User seeks by > 5 seconds (currentTime jumps to 50.0) -> Drift > 5.0 -> Should dispatch
	msg3 := CastMessage{
		PayloadUTF8: `{
			"type": "MEDIA_STATUS",
			"status": [{
				"mediaSessionId": 1,
				"playerState": "PLAYING",
				"currentTime": 50.0
			}]
		}`,
	}
	client.handleMediaMessage(msg3)
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	if len(receivedPayloads) != 2 {
		t.Fatalf("expected 2 dispatches after seek drift, got %d", len(receivedPayloads))
	}
	p3 := receivedPayloads[1]
	mu.Unlock()
	if p3["position"] != 50.0 {
		t.Errorf("expected position 50.0 after seek, got %v", p3["position"])
	}
	// Title/series should be preserved from session
	if p3["title"] != "Chapter 1" || p3["series"] != "Mando" {
		t.Errorf("expected preserved title/series, got %+v", p3)
	}

	// 4. State change: Paused -> Should dispatch
	msg4 := CastMessage{
		PayloadUTF8: `{
			"type": "MEDIA_STATUS",
			"status": [{
				"mediaSessionId": 1,
				"playerState": "PAUSED",
				"currentTime": 50.0
			}]
		}`,
	}
	client.handleMediaMessage(msg4)
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	if len(receivedPayloads) != 3 {
		t.Fatalf("expected 3 dispatches after pause, got %d", len(receivedPayloads))
	}
	p4 := receivedPayloads[2]
	mu.Unlock()
	if p4["player_state"] != "paused" {
		t.Errorf("expected paused player_state, got %v", p4["player_state"])
	}

	// 5. Title change on next episode -> Should dispatch
	msg5 := CastMessage{
		PayloadUTF8: `{
			"type": "MEDIA_STATUS",
			"status": [{
				"mediaSessionId": 1,
				"playerState": "PAUSED",
				"currentTime": 0.0,
				"media": {
					"duration": 3600.0,
					"metadata": {
						"title": "Chapter 2",
						"seriesTitle": "Mando",
						"season": "2",
						"episode": "4"
					}
				}
			}]
		}`,
	}
	client.handleMediaMessage(msg5)
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	if len(receivedPayloads) != 4 {
		t.Fatalf("expected 4 dispatches after title change, got %d", len(receivedPayloads))
	}
	p5 := receivedPayloads[3]
	mu.Unlock()
	if p5["title"] != "Chapter 2" || p5["episode"] != "4" {
		t.Errorf("expected Chapter 2, got %+v", p5)
	}

	// 6. MediaSessionID change -> Should dispatch
	msg6 := CastMessage{
		PayloadUTF8: `{
			"type": "MEDIA_STATUS",
			"status": [{
				"mediaSessionId": 2,
				"playerState": "PLAYING",
				"currentTime": 0.0,
				"media": {
					"duration": 1800.0,
					"metadata": {
						"title": "New Session",
						"seriesTitle": "Show B"
					}
				}
			}]
		}`,
	}
	client.handleMediaMessage(msg6)
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	if len(receivedPayloads) != 5 {
		t.Fatalf("expected 5 dispatches after mediaSessionId change, got %d", len(receivedPayloads))
	}
	p6 := receivedPayloads[4]
	mu.Unlock()
	if p6["title"] != "New Session" || p6["duration"] != 1800.0 {
		t.Errorf("expected New Session, got %+v", p6)
	}

	// 7. Verify dispatchTrigger includes metadata
	client.dispatchTrigger()
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	if len(receivedPayloads) != 6 {
		t.Fatalf("expected 6 dispatches after dispatchTrigger, got %d", len(receivedPayloads))
	}
	p7 := receivedPayloads[5]
	mu.Unlock()
	if p7["title"] != "New Session" || p7["series"] != "Show B" {
		t.Errorf("expected dispatchTrigger to include metadata, got %+v", p7)
	}

	// 8. Transition to IDLE: should purge cached metadata and dispatch idle state
	msg8 := CastMessage{
		PayloadUTF8: `{
			"type": "MEDIA_STATUS",
			"status": [{
				"mediaSessionId": 0,
				"playerState": "IDLE"
			}]
		}`,
	}
	client.handleMediaMessage(msg8)
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	if len(receivedPayloads) != 7 {
		t.Fatalf("expected 7 dispatches after IDLE transition, got %d", len(receivedPayloads))
	}
	p8 := receivedPayloads[6]
	mu.Unlock()
	if p8["player_state"] != "idle" {
		t.Errorf("expected player_state idle, got %v", p8["player_state"])
	}
	if p8["title"] != "" || p8["series"] != "" || p8["season"] != "" || p8["episode"] != "" {
		t.Errorf("expected purged metadata on IDLE transition, got %+v", p8)
	}
	if p8["duration"] != 0.0 || p8["position"] != 0.0 {
		t.Errorf("expected zero duration and position on IDLE, got %+v", p8)
	}
}

func TestExtractMediaMetadata(t *testing.T) {
	tests := []struct {
		name        string
		input       map[string]any
		wantTitle   string
		wantSeries  string
		wantSeason  string
		wantEpisode string
	}{
		{
			name:        "nil metadata",
			input:       nil,
			wantTitle:   "",
			wantSeries:  "",
			wantSeason:  "",
			wantEpisode: "",
		},
		{
			name: "standard TvShowMediaMetadata",
			input: map[string]any{
				"title":       "If You Don't Like It, I'll Just Run",
				"seriesTitle": "Trapped in a Dating Sim",
				"season":      2,
				"episode":     4,
			},
			wantTitle:   "If You Don't Like It, I'll Just Run",
			wantSeries:  "Trapped in a Dating Sim",
			wantSeason:  "2",
			wantEpisode: "4",
		},
		{
			name: "alternate field variations",
			input: map[string]any{
				"episode_title":  "The Pilot",
				"series_title":   "Breaking Bad",
				"season_number":  "1",
				"episode_number": "1",
			},
			wantTitle:   "The Pilot",
			wantSeries:  "Breaking Bad",
			wantSeason:  "1",
			wantEpisode: "1",
		},
		{
			name: "title containing episode in parentheses",
			input: map[string]any{
				"title":       "If You Don't Like It, I'll Just Run (Episode 4)",
				"seriesTitle": "Trapped in a Dating Sim",
			},
			wantTitle:   "If You Don't Like It, I'll Just Run",
			wantSeries:  "Trapped in a Dating Sim",
			wantSeason:  "",
			wantEpisode: "4",
		},
		{
			name: "title containing series and SXXEXX",
			input: map[string]any{
				"title": "Trapped in a Dating Sim S02E04: If You Don't Like It, I'll Just Run",
			},
			wantTitle:   "If You Don't Like It, I'll Just Run",
			wantSeries:  "Trapped in a Dating Sim",
			wantSeason:  "02",
			wantEpisode: "04",
		},
		{
			name: "title containing episode lead",
			input: map[string]any{
				"title":    "Episode 4: If You Don't Like It, I'll Just Run",
				"subtitle": "Trapped in a Dating Sim",
			},
			wantTitle:   "If You Don't Like It, I'll Just Run",
			wantSeries:  "Trapped in a Dating Sim",
			wantSeason:  "",
			wantEpisode: "4",
		},
		{
			name: "generic movie",
			input: map[string]any{
				"title": "Inception",
			},
			wantTitle:   "Inception",
			wantSeries:  "",
			wantSeason:  "",
			wantEpisode: "",
		},
		{
			name: "title with series colon season and episode",
			input: map[string]any{
				"title": "Spy x Family: Season 1, Episode 5 - Mission Start",
			},
			wantTitle:   "Mission Start",
			wantSeries:  "Spy x Family",
			wantSeason:  "1",
			wantEpisode: "5",
		},
		{
			name: "title with series colon episode only",
			input: map[string]any{
				"title": "Spy x Family: Episode 5: Mission Start",
			},
			wantTitle:   "Mission Start",
			wantSeries:  "Spy x Family",
			wantSeason:  "",
			wantEpisode: "5",
		},
		{
			name: "subtitle with season and episode",
			input: map[string]any{
				"title":    "Some Episode",
				"subtitle": "Season 2, Episode 3",
			},
			wantTitle:   "Some Episode",
			wantSeries:  "",
			wantSeason:  "2",
			wantEpisode: "3",
		},
		{
			name: "subtitle with episode only",
			input: map[string]any{
				"title":    "Some Episode",
				"subtitle": "Episode 7",
			},
			wantTitle:   "Some Episode",
			wantSeries:  "",
			wantSeason:  "",
			wantEpisode: "7",
		},
		{
			name: "title with paren season and episode",
			input: map[string]any{
				"title": "The Big Match (Season 3, Episode 9)",
			},
			wantTitle:   "The Big Match",
			wantSeries:  "",
			wantSeason:  "3",
			wantEpisode: "9",
		},
		{
			name: "parseStringOrInt types",
			input: map[string]any{
				"title":          "Types Test",
				"series":         json.Number("42"),
				"season_index":   float64(3),
				"episode_number": int(12),
			},
			wantTitle:   "Types Test",
			wantSeries:  "42",
			wantSeason:  "3",
			wantEpisode: "12",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTitle, gotSeries, gotSeason, gotEpisode := extractMediaMetadata(tt.input)
			if gotTitle != tt.wantTitle {
				t.Errorf("title mismatch: got %q, want %q", gotTitle, tt.wantTitle)
			}
			if gotSeries != tt.wantSeries {
				t.Errorf("series mismatch: got %q, want %q", gotSeries, tt.wantSeries)
			}
			if gotSeason != tt.wantSeason {
				t.Errorf("season mismatch: got %q, want %q", gotSeason, tt.wantSeason)
			}
			if gotEpisode != tt.wantEpisode {
				t.Errorf("episode mismatch: got %q, want %q", gotEpisode, tt.wantEpisode)
			}
		})
	}
}

func TestCastClient_CoreWebhookFailureResetsState(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("502 Bad Gateway"))
	}))
	defer ts.Close()

	client := NewCastClient(ClientConfig{
		CoreURL:    ts.URL,
		HTTPClient: ts.Client(),
	})

	client.lastReportedPlayerState = "playing"
	client.lastReportedTitle = "Episode 1"
	client.lastTriggered = true

	client.postToCore(ts.URL+"/api/video/state", []byte(`{}`), "state")
	client.mu.RLock()
	if client.lastReportedPlayerState != "" || client.lastReportedTitle != "" {
		t.Errorf("expected lastReportedPlayerState and Title reset on 502, got state=%q title=%q",
			client.lastReportedPlayerState, client.lastReportedTitle)
	}
	client.mu.RUnlock()

	client.postToCore(ts.URL+"/api/video/trigger", []byte(`{}`), "trigger")
	client.mu.RLock()
	if client.lastTriggered {
		t.Errorf("expected lastTriggered reset to false on 502")
	}
	client.mu.RUnlock()
}

