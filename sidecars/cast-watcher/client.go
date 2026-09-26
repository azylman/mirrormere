package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// Cast Namespaces
	NamespaceConnection = "urn:x-cast:com.google.cast.tp.connection"
	NamespaceHeartbeat  = "urn:x-cast:com.google.cast.tp.heartbeat"
	NamespaceReceiver   = "urn:x-cast:com.google.cast.receiver"
	NamespaceMedia      = "urn:x-cast:com.google.cast.media"

	// Default backdrop app ID on Google Chromecast
	BackdropAppID = "E8C28D3C"

	// Cast Destinations
	DestinationReceiver = "receiver-0"
	DestinationHeartbeat = "transport-0"
	SourceSender        = "sender-0"
)

var (
	ErrNotConnected         = errors.New("chromecast not connected")
	ErrNoActiveMediaSession = errors.New("no active media session on chromecast")
	ErrInvalidAction        = errors.New("invalid media action")
)

// DialFunc is a function type for establishing a network connection (TLS or mock).
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// ClientConfig holds configuration options for CastClient.
type ClientConfig struct {
	ChromecastAddr    string
	CoreURL           string
	StreamURL         string
	ControlURL        string
	HTTPClient        *http.Client
	Dialer            DialFunc
	HeartbeatInterval time.Duration
	WatchdogTimeout   time.Duration
	ReconnectBase     time.Duration
	ReconnectMax      time.Duration
	Logger            *slog.Logger
}

// CastClient monitors a Google Chromecast via CastV2 TLS socket and synchronizes with Mirrormere Core.
type CastClient struct {
	cfg       ClientConfig
	logger    *slog.Logger
	client    *http.Client
	dialer    DialFunc

	reqCounter uint64

	mu                     sync.RWMutex
	conn                   net.Conn
	writeMu                sync.Mutex
	connected              bool
	appID                  string
	transportID            string
	mediaSessionID         int
	playerState            string
	lastReportedPlayerState string
	lastTriggered          bool
	lastMessageTime        time.Time
	closed                 bool
	cancelFunc             context.CancelFunc

	dispatchCh chan func()
	stopOnce   sync.Once
}

// NewCastClient constructs a new CastClient with the provided configuration.
func NewCastClient(cfg ClientConfig) *CastClient {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 2 * time.Second,
		}
	}

	dialer := cfg.Dialer
	if dialer == nil {
		dialer = func(ctx context.Context, network, addr string) (net.Conn, error) {
			tlsDialer := &tls.Dialer{
				Config: &tls.Config{
					InsecureSkipVerify: true, // Self-signed TLS on Chromecast LAN
				},
			}
			return tlsDialer.DialContext(ctx, network, addr)
		}
	}

	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = 5 * time.Second
	}
	if cfg.WatchdogTimeout <= 0 {
		cfg.WatchdogTimeout = 15 * time.Second
	}
	if cfg.ReconnectBase <= 0 {
		cfg.ReconnectBase = 1 * time.Second
	}
	if cfg.ReconnectMax <= 0 {
		cfg.ReconnectMax = 30 * time.Second
	}

	return &CastClient{
		cfg:        cfg,
		logger:     logger,
		client:     httpClient,
		dialer:     dialer,
		dispatchCh: make(chan func(), 64),
	}
}

// Start begins background connection monitoring and Core webhook worker routines.
func (c *CastClient) Start(ctx context.Context) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	c.cancelFunc = cancel
	c.mu.Unlock()

	// Start asynchronous Core webhook dispatcher worker
	go c.dispatchWorker(runCtx)

	// Start socket supervisor loop
	go c.supervisorLoop(runCtx)
}

// Close stops the client and closes any active network connections.
func (c *CastClient) Close() {
	c.stopOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		cancel := c.cancelFunc
		conn := c.conn
		c.mu.Unlock()

		if cancel != nil {
			cancel()
		}
		if conn != nil {
			if err := conn.Close(); err != nil {
				c.logger.Debug("[CastWatcher] Error closing connection on shutdown", "error", err)
			}
		}
	})
}

// IsConnected returns whether the CastV2 socket is currently connected.
func (c *CastClient) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

// StatusSnapshot represents the current connection and media state for health reporting.
type StatusSnapshot struct {
	Connected   bool   `json:"chromecast_connected"`
	AppID       string `json:"app_id"`
	PlayerState string `json:"player_state"`
}

// GetStatus returns the current status snapshot for healthcheck inspection.
func (c *CastClient) GetStatus() StatusSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return StatusSnapshot{
		Connected:   c.connected,
		AppID:       c.appID,
		PlayerState: c.playerState,
	}
}

// SendMediaAction translates an action (toggle_playback, play, pause) into a Cast command.
func (c *CastClient) SendMediaAction(ctx context.Context, action string) error {
	c.mu.RLock()
	if !c.connected || c.conn == nil {
		c.mu.RUnlock()
		return ErrNotConnected
	}
	if c.transportID == "" || c.mediaSessionID == 0 {
		c.mu.RUnlock()
		return ErrNoActiveMediaSession
	}

	transportID := c.transportID
	mediaSessionID := c.mediaSessionID
	curState := c.playerState
	c.mu.RUnlock()

	var cmd string
	switch action {
	case "play":
		cmd = "PLAY"
	case "pause":
		cmd = "PAUSE"
	case "toggle_playback":
		if curState == "playing" {
			cmd = "PAUSE"
		} else {
			cmd = "PLAY"
		}
	default:
		return fmt.Errorf("%w: %s", ErrInvalidAction, action)
	}

	reqID := atomic.AddUint64(&c.reqCounter, 1)
	payload := map[string]any{
		"type":           cmd,
		"requestId":      reqID,
		"mediaSessionId": mediaSessionID,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	msg := CastMessage{
		ProtocolVersion: ProtocolVersion0,
		SourceID:        SourceSender,
		DestinationID:   transportID,
		Namespace:       NamespaceMedia,
		PayloadType:     PayloadTypeString,
		PayloadUTF8:     string(payloadBytes),
	}

	return c.sendMessage(msg)
}

func (c *CastClient) supervisorLoop(ctx context.Context) {
	backoff := c.cfg.ReconnectBase

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		err := c.connectAndServe(ctx)
		if ctx.Err() != nil {
			return
		}

		c.mu.Lock()
		c.connected = false
		c.conn = nil
		c.mu.Unlock()

		if err != nil {
			c.logger.Warn("[CastWatcher] Chromecast connection dropped or failed", "error", err, "retry_in", backoff)
		}

		// Jittered backoff wait
		var jitter time.Duration
		if b := int64(backoff / 4); b > 0 {
			jitter = time.Duration(rand.Int63n(b))
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff + jitter):
		}

		backoff *= 2
		if backoff > c.cfg.ReconnectMax {
			backoff = c.cfg.ReconnectMax
		}
	}
}

func (c *CastClient) connectAndServe(ctx context.Context) error {
	c.logger.Info("[CastWatcher] Connecting to Chromecast", "addr", c.cfg.ChromecastAddr)
	conn, err := c.dialer(ctx, "tcp", c.cfg.ChromecastAddr)
	if err != nil {
		return err
	}
	defer func() {
		if err := conn.Close(); err != nil {
			c.logger.Debug("[CastWatcher] Error closing connection on defer", "error", err)
		}
	}()

	c.mu.Lock()
	c.conn = conn
	c.connected = true
	c.lastMessageTime = time.Now()
	c.mu.Unlock()

	c.logger.Info("[CastWatcher] Connected to Chromecast", "addr", c.cfg.ChromecastAddr)

	// Step 1: Initial Handshake - Connect to receiver-0
	if err := c.sendMessage(CastMessage{
		ProtocolVersion: ProtocolVersion0,
		SourceID:        SourceSender,
		DestinationID:   DestinationReceiver,
		Namespace:       NamespaceConnection,
		PayloadType:     PayloadTypeString,
		PayloadUTF8:     `{"type":"CONNECT"}`,
	}); err != nil {
		return fmt.Errorf("failed to send initial CONNECT: %w", err)
	}

	// Step 2: Request initial receiver status
	reqID := atomic.AddUint64(&c.reqCounter, 1)
	if err := c.sendMessage(CastMessage{
		ProtocolVersion: ProtocolVersion0,
		SourceID:        SourceSender,
		DestinationID:   DestinationReceiver,
		Namespace:       NamespaceReceiver,
		PayloadType:     PayloadTypeString,
		PayloadUTF8:     fmt.Sprintf(`{"type":"GET_STATUS","requestId":%d}`, reqID),
	}); err != nil {
		return fmt.Errorf("failed to send GET_STATUS: %w", err)
	}

	// Step 3: Spawn Heartbeat & Watchdog Routine
	heartbeatDone := make(chan struct{})
	defer close(heartbeatDone)
	go c.heartbeatAndWatchdog(conn, heartbeatDone)

	// Step 4: Frame Reader Loop
	for {
		msg, err := DecodeCastMessage(conn)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				return err
			}
			return fmt.Errorf("error reading cast frame: %w", err)
		}

		c.mu.Lock()
		c.lastMessageTime = time.Now()
		c.mu.Unlock()

		c.handleIncomingMessage(msg)
	}
}

func (c *CastClient) heartbeatAndWatchdog(conn net.Conn, done <-chan struct{}) {
	ticker := time.NewTicker(c.cfg.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			// Check Watchdog
			c.mu.RLock()
			lastMsg := c.lastMessageTime
			c.mu.RUnlock()

			if time.Since(lastMsg) > c.cfg.WatchdogTimeout {
				c.logger.Warn("[CastWatcher] Watchdog timeout: no messages received within threshold, closing connection", "elapsed", time.Since(lastMsg))
				if err := conn.Close(); err != nil {
					c.logger.Debug("[CastWatcher] Error closing connection on watchdog timeout", "error", err)
				}
				return
			}

			// Send Heartbeat PING
			if err := c.sendMessage(CastMessage{
				ProtocolVersion: ProtocolVersion0,
				SourceID:        SourceSender,
				DestinationID:   DestinationHeartbeat,
				Namespace:       NamespaceHeartbeat,
				PayloadType:     PayloadTypeString,
				PayloadUTF8:     `{"type":"PING"}`,
			}); err != nil {
				c.logger.Warn("[CastWatcher] Failed to send heartbeat PING", "error", err)
				if closeErr := conn.Close(); closeErr != nil {
					c.logger.Debug("[CastWatcher] Error closing connection after failed PING", "error", closeErr)
				}
				return
			}
		}
	}
}

func (c *CastClient) handleIncomingMessage(msg CastMessage) {
	switch msg.Namespace {
	case NamespaceHeartbeat:
		c.handleHeartbeatMessage(msg)
	case NamespaceReceiver:
		c.handleReceiverMessage(msg)
	case NamespaceMedia:
		c.handleMediaMessage(msg)
	}
}

func (c *CastClient) handleHeartbeatMessage(msg CastMessage) {
	if strings.Contains(msg.PayloadUTF8, `"PING"`) {
		// Reply immediately with PONG to the sender
		if err := c.sendMessage(CastMessage{
			ProtocolVersion: ProtocolVersion0,
			SourceID:        SourceSender,
			DestinationID:   msg.SourceID,
			Namespace:       NamespaceHeartbeat,
			PayloadType:     PayloadTypeString,
			PayloadUTF8:     `{"type":"PONG"}`,
		}); err != nil {
			c.logger.Debug("[CastWatcher] Failed to send PONG", "error", err)
		}
	}
}

type receiverStatusPayload struct {
	Type   string `json:"type"`
	Status struct {
		Applications []struct {
			AppID       string `json:"appId"`
			DisplayName string `json:"displayName"`
			TransportID string `json:"transportId"`
		} `json:"applications"`
	} `json:"status"`
}

func (c *CastClient) handleReceiverMessage(msg CastMessage) {
	var payload receiverStatusPayload
	if err := json.Unmarshal([]byte(msg.PayloadUTF8), &payload); err != nil {
		return
	}

	if payload.Type != "RECEIVER_STATUS" {
		return
	}

	var activeAppID, activeTransportID string
	if len(payload.Status.Applications) > 0 {
		app := payload.Status.Applications[0]
		if app.AppID != "" && app.AppID != BackdropAppID {
			activeAppID = app.AppID
			activeTransportID = app.TransportID
		}
	}

	c.mu.Lock()
	prevAppID := c.appID
	prevTransportID := c.transportID
	c.appID = activeAppID
	c.transportID = activeTransportID

	if activeAppID != "" {
		// Active casting detected!
		if activeTransportID != prevTransportID {
			// Connect to the new app transport target
			c.logger.Info("[CastWatcher] Active cast app detected", "app_id", activeAppID, "transport_id", activeTransportID)
			go func(tid string) {
				if err := c.sendMessage(CastMessage{
					ProtocolVersion: ProtocolVersion0,
					SourceID:        SourceSender,
					DestinationID:   tid,
					Namespace:       NamespaceConnection,
					PayloadType:     PayloadTypeString,
					PayloadUTF8:     `{"type":"CONNECT"}`,
				}); err != nil {
					c.logger.Debug("[CastWatcher] Failed to send app CONNECT", "error", err)
				}

				reqID := atomic.AddUint64(&c.reqCounter, 1)
				if err := c.sendMessage(CastMessage{
					ProtocolVersion: ProtocolVersion0,
					SourceID:        SourceSender,
					DestinationID:   tid,
					Namespace:       NamespaceMedia,
					PayloadType:     PayloadTypeString,
					PayloadUTF8:     fmt.Sprintf(`{"type":"GET_STATUS","requestId":%d}`, reqID),
				}); err != nil {
					c.logger.Debug("[CastWatcher] Failed to send media GET_STATUS", "error", err)
				}
			}(activeTransportID)
		}

		if !c.lastTriggered {
			c.lastTriggered = true
			c.mu.Unlock()
			c.queueDispatch(func() {
				c.dispatchTrigger()
			})
			return
		}
	} else {
		// Returning to idle / backdrop
		if prevAppID != "" || c.lastTriggered {
			c.logger.Info("[CastWatcher] Chromecast returned to idle / backdrop")
			c.appID = ""
			c.transportID = ""
			c.mediaSessionID = 0
			c.playerState = ""
			c.lastReportedPlayerState = ""
			c.lastTriggered = false
			c.mu.Unlock()

			c.queueDispatch(func() {
				c.dispatchDismiss()
			})
			return
		}
	}
	c.mu.Unlock()
}

type mediaStatusPayload struct {
	Type   string `json:"type"`
	Status []struct {
		MediaSessionID int    `json:"mediaSessionId"`
		PlayerState    string `json:"playerState"`
	} `json:"status"`
}

func (c *CastClient) handleMediaMessage(msg CastMessage) {
	var payload mediaStatusPayload
	if err := json.Unmarshal([]byte(msg.PayloadUTF8), &payload); err != nil {
		return
	}

	if payload.Type != "MEDIA_STATUS" || len(payload.Status) == 0 {
		return
	}

	item := payload.Status[0]
	rawState := item.PlayerState
	normalizedState := strings.ToLower(rawState)

	c.mu.Lock()
	c.mediaSessionID = item.MediaSessionID

	// Core strictly accepts "playing", "paused", "buffering"
	if normalizedState == "playing" || normalizedState == "paused" || normalizedState == "buffering" {
		c.playerState = normalizedState
		if c.lastReportedPlayerState != normalizedState {
			c.lastReportedPlayerState = normalizedState
			c.mu.Unlock()

			c.queueDispatch(func() {
				c.dispatchPlayerState(normalizedState)
			})
			return
		}
	} else if strings.ToUpper(rawState) == "IDLE" {
		c.playerState = "idle"
	}
	c.mu.Unlock()
}

func (c *CastClient) sendMessage(msg CastMessage) error {
	frame, err := EncodeCastMessage(msg)
	if err != nil {
		return err
	}

	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()

	if conn == nil {
		return ErrNotConnected
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		c.logger.Debug("[CastWatcher] Failed to set write deadline", "error", err)
	}
	_, err = conn.Write(frame)
	return err
}

func (c *CastClient) queueDispatch(fn func()) {
	select {
	case c.dispatchCh <- fn:
	default:
		c.logger.Warn("[CastWatcher] Dispatch channel full, executing webhook synchronously in goroutine")
		go fn()
	}
}

func (c *CastClient) dispatchWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case fn := <-c.dispatchCh:
			fn()
		}
	}
}

func (c *CastClient) dispatchTrigger() {
	triggerURL := strings.TrimRight(c.cfg.CoreURL, "/") + "/api/video/trigger"
	streamURL := c.cfg.StreamURL
	if streamURL == "" {
		streamURL = "http://localhost:1984/api/webrtc?src=cast"
	}
	controlURL := c.cfg.ControlURL
	if controlURL == "" {
		controlURL = "http://cast-watcher:8090/action"
	}

	payload := map[string]any{
		"id":           "chromecast",
		"type":         "webrtc",
		"priority":     "persistent",
		"stream_url":   streamURL,
		"controllable": true,
		"control_url":  controlURL,
	}

	if data, err := json.Marshal(payload); err == nil {
		c.postToCore(triggerURL, data, "trigger")
	}
}

func (c *CastClient) dispatchDismiss() {
	dismissURL := strings.TrimRight(c.cfg.CoreURL, "/") + "/api/video/dismiss"
	payload := map[string]any{
		"id": "chromecast",
	}

	if data, err := json.Marshal(payload); err == nil {
		c.postToCore(dismissURL, data, "dismiss")
	}
}

func (c *CastClient) dispatchPlayerState(state string) {
	stateURL := strings.TrimRight(c.cfg.CoreURL, "/") + "/api/video/state"
	payload := map[string]any{
		"id":           "chromecast",
		"player_state": state,
	}

	if data, err := json.Marshal(payload); err == nil {
		c.postToCore(stateURL, data, "state")
	}
}

func (c *CastClient) postToCore(targetURL string, payload []byte, operation string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(payload))
	if err != nil {
		c.logger.Error("[CastWatcher] Failed to create HTTP request to Core", "operation", operation, "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		c.logger.Warn("[CastWatcher] Core webhook dispatch failed", "operation", operation, "error", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 512))
		if readErr != nil {
			body = []byte("<read error>")
		}
		c.logger.Warn("[CastWatcher] Core rejected webhook", "operation", operation, "status", resp.StatusCode, "response", string(body))
	} else {
		c.logger.Debug("[CastWatcher] Core webhook accepted", "operation", operation, "status", resp.StatusCode)
	}
}
