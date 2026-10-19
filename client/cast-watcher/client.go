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
	"math"
	"math/rand"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	NamespaceConnection  = "urn:x-cast:com.google.cast.tp.connection"
	NamespaceHeartbeat   = "urn:x-cast:com.google.cast.tp.heartbeat"
	NamespaceReceiver    = "urn:x-cast:com.google.cast.receiver"
	NamespaceMedia       = "urn:x-cast:com.google.cast.media"
	BackdropAppID        = "E8C28D3C"
	DestinationReceiver  = "receiver-0"
	DestinationHeartbeat = "transport-0"
	SourceSender         = "sender-0"
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
	WakeDelay         time.Duration
	Logger            *slog.Logger
}

// CastClient monitors a Google Chromecast via CastV2 TLS socket and synchronizes with Mirrormere Core.
type CastClient struct {
	cfg        ClientConfig
	logger     *slog.Logger
	client     *http.Client
	dialer     DialFunc
	reqCounter uint64

	mu                         sync.RWMutex
	conn                       net.Conn
	writeMu                    sync.Mutex
	connected                  bool
	appID                      string
	transportID                string
	mediaSessionID             int
	playerState                string
	lastReportedPlayerState    string
	lastTriggered              bool
	lastMessageTime            time.Time
	closed                     bool
	cancelFunc                 context.CancelFunc
	lastPosition               float64
	lastDuration               float64
	lastTitle                  string
	lastSeries                 string
	lastSeason                 string
	lastEpisode                string
	lastPositionTime           time.Time
	lastReportedTitle          string
	lastReportedSeries         string
	lastReportedSeason         string
	lastReportedEpisode        string
	lastReportedMediaSessionID int

	wakeMu       sync.Mutex
	lastWakeTime time.Time
	dispatchCh   chan func()
	stopOnce     sync.Once
}

// NewCastClient constructs a new CastClient with the provided configuration.
func NewCastClient(cfg ClientConfig) *CastClient {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 2 * time.Second}
	}
	dialer := cfg.Dialer
	if dialer == nil {
		dialer = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return (&tls.Dialer{Config: &tls.Config{InsecureSkipVerify: true}}).DialContext(ctx, network, addr)
		}
	}
	if cfg.HeartbeatInterval <= 0 { cfg.HeartbeatInterval = 5 * time.Second }
	if cfg.WatchdogTimeout <= 0 { cfg.WatchdogTimeout = 15 * time.Second }
	if cfg.ReconnectBase <= 0 { cfg.ReconnectBase = 1 * time.Second }
	if cfg.ReconnectMax <= 0 { cfg.ReconnectMax = 30 * time.Second }
	if cfg.WakeDelay <= 0 { cfg.WakeDelay = 1500 * time.Millisecond }

	return &CastClient{cfg: cfg, logger: logger, client: httpClient, dialer: dialer, dispatchCh: make(chan func(), 64)}
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
	go c.dispatchWorker(runCtx)
	go c.supervisorLoop(runCtx)
}

// Close stops the client and closes any active network connections.
func (c *CastClient) Close() {
	c.stopOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		cancel, conn := c.cancelFunc, c.conn
		c.mu.Unlock()
		if cancel != nil { cancel() }
		if conn != nil { _ = conn.Close() } //nolint:errcheck
	})
}

// StatusSnapshot represents the current connection and media state for health reporting.
type StatusSnapshot struct {
	Connected   bool   `json:"chromecast_connected"`
	AppID       string `json:"app_id"`
	PlayerState string `json:"player_state"`
}

// GetStatus returns the current status snapshot for healthcheck inspection.
func (c *CastClient) GetStatus() StatusSnapshot {
	c.mu.RLock(); defer c.mu.RUnlock()
	return StatusSnapshot{Connected: c.connected, AppID: c.appID, PlayerState: c.playerState}
}

func newCastMsg(dest, ns, payload string) CastMessage {
	return CastMessage{
		ProtocolVersion: ProtocolVersion0,
		SourceID:        SourceSender,
		DestinationID:   dest,
		Namespace:       ns,
		PayloadType:     PayloadTypeString,
		PayloadUTF8:     payload,
	}
}

func (c *CastClient) sendJSONContext(ctx context.Context, dest, ns string, payload map[string]any) error {
	b, err := json.Marshal(payload)
	if err != nil { return err }
	return c.sendMessageContext(ctx, newCastMsg(dest, ns, string(b)))
}

func (c *CastClient) activeMediaTarget() (string, int, string, error) {
	c.mu.RLock(); defer c.mu.RUnlock()
	if !c.connected || c.conn == nil { return "", 0, "", ErrNotConnected }
	if c.transportID == "" || c.mediaSessionID == 0 { return "", 0, "", ErrNoActiveMediaSession }
	return c.transportID, c.mediaSessionID, c.playerState, nil
}

func (c *CastClient) resetMediaStateLocked() {
	c.mediaSessionID = 0
	c.playerState = ""
	c.lastTitle, c.lastSeries, c.lastSeason, c.lastEpisode = "", "", "", ""
	c.lastPosition, c.lastDuration = 0, 0
	c.lastPositionTime = time.Time{}
	c.lastReportedPlayerState, c.lastReportedTitle = "", ""
	c.lastReportedSeries, c.lastReportedSeason, c.lastReportedEpisode = "", "", ""
	c.lastReportedMediaSessionID = 0
}

func (c *CastClient) resetReportedOnFailure(operation string) {
	c.mu.Lock(); defer c.mu.Unlock()
	if operation == "state" {
		c.lastReportedPlayerState, c.lastReportedTitle = "", ""
		c.lastReportedSeries, c.lastReportedSeason, c.lastReportedEpisode = "", "", ""
	} else if operation == "trigger" {
		c.lastTriggered = false
	}
}

// SendMediaAction translates an action (toggle_playback, play, pause) into a Cast command.
func (c *CastClient) SendMediaAction(ctx context.Context, action string) error {
	transportID, mediaSessionID, curState, err := c.activeMediaTarget()
	if err != nil { return err }
	var cmd string
	switch action {
	case "play": cmd = "PLAY"
	case "pause": cmd = "PAUSE"
	case "toggle_playback":
		cmd = "PLAY"; if curState == "playing" { cmd = "PAUSE" }
	default:
		return fmt.Errorf("%w: %s", ErrInvalidAction, action)
	}
	return c.sendJSONContext(ctx, transportID, NamespaceMedia, map[string]any{"type": cmd, "requestId": atomic.AddUint64(&c.reqCounter, 1), "mediaSessionId": mediaSessionID})
}

// Seek commands the active Chromecast media session to jump to targetTime in seconds.
func (c *CastClient) Seek(ctx context.Context, targetTime float64) error {
	if targetTime < 0 { return fmt.Errorf("%w: target time must be >= 0", ErrInvalidAction) }
	transportID, mediaSessionID, _, err := c.activeMediaTarget()
	if err != nil { return err }
	if err := c.sendJSONContext(ctx, transportID, NamespaceMedia, map[string]any{"type": "SEEK", "requestId": atomic.AddUint64(&c.reqCounter, 1), "mediaSessionId": mediaSessionID, "currentTime": targetTime}); err != nil {
		return err
	}
	c.mu.Lock(); c.lastPosition = targetTime; c.lastPositionTime = time.Now(); c.mu.Unlock()
	return nil
}

// Wake sends an on-demand HDMI wake pulse by launching the default media player and stopping it.
func (c *CastClient) Wake(ctx context.Context) error {
	c.mu.RLock()
	notConnected := !c.connected || c.conn == nil
	notIdle := c.appID != "" || strings.EqualFold(c.playerState, "playing") || strings.EqualFold(c.playerState, "buffering")
	c.mu.RUnlock()
	if notConnected { return ErrNotConnected }
	if notIdle { return nil }

	c.wakeMu.Lock(); defer c.wakeMu.Unlock()
	c.mu.RLock()
	notIdle = c.appID != "" || strings.EqualFold(c.playerState, "playing") || strings.EqualFold(c.playerState, "buffering")
	c.mu.RUnlock()
	if notIdle || (!c.lastWakeTime.IsZero() && time.Since(c.lastWakeTime) < 5*time.Second) {
		return nil
	}
	c.lastWakeTime = time.Now()

	reqID := atomic.AddUint64(&c.reqCounter, 1)
	if err := c.sendJSONContext(ctx, DestinationReceiver, NamespaceReceiver, map[string]any{"type": "LAUNCH", "appId": "CC1AD845", "requestId": reqID}); err != nil {
		return err
	}
	wakeDelay := c.cfg.WakeDelay
	if wakeDelay <= 0 { wakeDelay = 1500 * time.Millisecond }
	select {
	case <-ctx.Done(): return ctx.Err()
	case <-time.After(wakeDelay):
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopCancel()
	stopReqID := atomic.AddUint64(&c.reqCounter, 1)
	if err := c.sendJSONContext(stopCtx, DestinationReceiver, NamespaceReceiver, map[string]any{"type": "STOP", "requestId": stopReqID}); err != nil {
		return err
	}
	c.logger.Info("[CastWatcher] Triggered on-demand Chromecast HDMI wake pulse")
	return nil
}

func (c *CastClient) supervisorLoop(ctx context.Context) {
	backoff := c.cfg.ReconnectBase
	for {
		select {
		case <-ctx.Done(): return
		default:
		}

		err := c.connectAndServe(ctx)
		if ctx.Err() != nil { return }

		c.mu.Lock(); c.connected = false; c.conn = nil; c.mu.Unlock()
		if err != nil {
			c.logger.Warn("[CastWatcher] Chromecast connection dropped or failed", "error", err, "retry_in", backoff)
		}

		var jitter time.Duration
		if b := int64(backoff / 4); b > 0 { jitter = time.Duration(rand.Int63n(b)) }
		select {
		case <-ctx.Done(): return
		case <-time.After(backoff + jitter):
		}
		backoff *= 2
		if backoff > c.cfg.ReconnectMax { backoff = c.cfg.ReconnectMax }
	}
}

func (c *CastClient) connectAndServe(ctx context.Context) error {
	c.logger.Info("[CastWatcher] Connecting to Chromecast", "addr", c.cfg.ChromecastAddr)
	conn, err := c.dialer(ctx, "tcp", c.cfg.ChromecastAddr)
	if err != nil { return err }
	defer func() { _ = conn.Close() }() //nolint:errcheck

	c.mu.Lock(); c.conn = conn; c.connected = true; c.lastMessageTime = time.Now(); c.mu.Unlock()
	c.logger.Info("[CastWatcher] Connected to Chromecast", "addr", c.cfg.ChromecastAddr)

	if err := c.sendMessage(newCastMsg(DestinationReceiver, NamespaceConnection, `{"type":"CONNECT"}`)); err != nil {
		return fmt.Errorf("failed to send initial CONNECT: %w", err)
	}
	reqID := atomic.AddUint64(&c.reqCounter, 1)
	if err := c.sendMessage(newCastMsg(DestinationReceiver, NamespaceReceiver, fmt.Sprintf(`{"type":"GET_STATUS","requestId":%d}`, reqID))); err != nil {
		return fmt.Errorf("failed to send GET_STATUS: %w", err)
	}

	heartbeatDone := make(chan struct{})
	defer close(heartbeatDone)
	go c.heartbeatAndWatchdog(conn, heartbeatDone)

	for {
		msg, err := DecodeCastMessage(conn)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) { return err }
			return fmt.Errorf("error reading cast frame: %w", err)
		}
		c.mu.Lock(); c.lastMessageTime = time.Now(); c.mu.Unlock()
		c.handleIncomingMessage(msg)
	}
}

func (c *CastClient) heartbeatAndWatchdog(conn net.Conn, done <-chan struct{}) {
	ticker := time.NewTicker(c.cfg.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-done: return
		case <-ticker.C:
			c.mu.RLock(); lastMsg := c.lastMessageTime; c.mu.RUnlock()
			if time.Since(lastMsg) > c.cfg.WatchdogTimeout {
				c.logger.Warn("[CastWatcher] Watchdog timeout: no messages received within threshold, closing connection", "elapsed", time.Since(lastMsg))
				_ = conn.Close() //nolint:errcheck
				return
			}
			if err := c.sendMessage(newCastMsg(DestinationHeartbeat, NamespaceHeartbeat, `{"type":"PING"}`)); err != nil {
				c.logger.Warn("[CastWatcher] Failed to send heartbeat PING", "error", err)
				_ = conn.Close() //nolint:errcheck
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
		_ = c.sendMessage(newCastMsg(msg.SourceID, NamespaceHeartbeat, `{"type":"PONG"}`)) //nolint:errcheck
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
	if err := json.Unmarshal([]byte(msg.PayloadUTF8), &payload); err != nil || payload.Type != "RECEIVER_STATUS" {
		return
	}

	var activeAppID, activeTransportID string
	if len(payload.Status.Applications) > 0 {
		app := payload.Status.Applications[0]
		if app.AppID != "" && app.AppID != BackdropAppID {
			activeAppID, activeTransportID = app.AppID, app.TransportID
		}
	}

	c.mu.Lock()
	prevAppID, prevTransportID := c.appID, c.transportID
	c.appID, c.transportID = activeAppID, activeTransportID

	if activeAppID != "" {
		if activeTransportID != prevTransportID {
			c.logger.Info("[CastWatcher] Active cast app detected", "app_id", activeAppID, "transport_id", activeTransportID)
			go func(tid string) {
				_ = c.sendMessage(newCastMsg(tid, NamespaceConnection, `{"type":"CONNECT"}`)) //nolint:errcheck
				reqID := atomic.AddUint64(&c.reqCounter, 1)
				_ = c.sendMessage(newCastMsg(tid, NamespaceMedia, fmt.Sprintf(`{"type":"GET_STATUS","requestId":%d}`, reqID))) //nolint:errcheck
			}(activeTransportID)
		}
		if !c.lastTriggered {
			c.lastTriggered = true
			c.mu.Unlock()
			c.queueDispatch(func() { c.dispatchTrigger() })
			return
		}
	} else if prevAppID != "" || c.lastTriggered {
		c.logger.Info("[CastWatcher] Chromecast returned to idle / backdrop")
		c.appID, c.transportID = "", ""
		c.lastTriggered = false
		c.resetMediaStateLocked()
		c.mu.Unlock()
		c.queueDispatch(func() { c.dispatchDismiss() })
		return
	}
	c.mu.Unlock()
}

type mediaStatusItem struct {
	MediaSessionID int            `json:"mediaSessionId"`
	PlayerState    string         `json:"playerState"`
	CurrentTime    float64        `json:"currentTime"`
	Media          struct {
		Duration float64        `json:"duration"`
		Metadata map[string]any `json:"metadata"`
	} `json:"media"`
}

type mediaStatusPayload struct {
	Type   string            `json:"type"`
	Status []mediaStatusItem `json:"status"`
}

func parseStringOrInt(v any) string {
	switch val := v.(type) {
	case string:
		return strings.TrimSpace(val)
	case float64:
		return fmt.Sprintf("%d", int(val))
	case int:
		return strconv.Itoa(val)
	case json.Number:
		return val.String()
	default:
		return ""
	}
}

var (
	rxParenEpisode = regexp.MustCompile(`(?i)^(.+?)\s*\((?:Season\s*(\d+)[,\s]*)?Episode\s*(\d+)\)$`)
	rxSeriesSXXEXX = regexp.MustCompile(`(?i)^(.+?)\s*(?:[-:]|\s)\s*S(\d+)\s*E(\d+)(?:\s*[:\-]\s*(.+))?$`)
	rxSeriesEpNum  = regexp.MustCompile(`(?i)^(.+?)\s*:\s*(?:Season\s*(\d+)[,\s]*)?Episode\s*(\d+)(?:\s*(?:-|:)\s*(.+))?$`)
	rxEpNumLead    = regexp.MustCompile(`(?i)^(?:Season\s*(\d+)[,\s]*)?Episode\s*(\d+)\s*[:\-]\s*(.+)$`)
	rxSubEpOnly    = regexp.MustCompile(`(?i)^(?:Season\s*(\d+)[,\s]*)?Episode\s*(\d+)$`)
)

func extractMediaMetadata(meta map[string]any) (title, series, season, episode string) {
	if meta == nil { return "", "", "", "" }

	getAny := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := meta[k]; ok && v != nil {
				if s := parseStringOrInt(v); s != "" { return s }
			}
		}
		return ""
	}

	title = getAny("title", "name", "episodeTitle", "episode_title", "episodeName")
	series = getAny("seriesTitle", "series_title", "seriesName", "series_name", "series", "showTitle", "show_title", "tvShowTitle", "tv_show_title")
	season = getAny("season", "seasonNumber", "season_number", "season_index")
	episode = getAny("episode", "episodeNumber", "episode_number", "episode_index")
	subtitle := getAny("subtitle")

	if series == "" && subtitle != "" && !strings.EqualFold(subtitle, title) {
		if subMatches := rxSubEpOnly.FindStringSubmatch(subtitle); len(subMatches) > 0 {
			if season == "" { season = subMatches[1] }
			if episode == "" { episode = subMatches[2] }
		} else {
			series = subtitle
		}
	}

	applySeriesMatches := func(m []string) {
		if series == "" { series = strings.TrimSpace(m[1]) }
		if season == "" { season = m[2] }
		if episode == "" { episode = m[3] }
		if m[4] != "" { title = strings.TrimSpace(m[4]) }
	}

	if m := rxParenEpisode.FindStringSubmatch(title); len(m) > 0 {
		title = strings.TrimSpace(m[1])
		if season == "" { season = m[2] }
		if episode == "" { episode = m[3] }
	} else if m := rxSeriesSXXEXX.FindStringSubmatch(title); len(m) > 0 {
		applySeriesMatches(m)
	} else if m := rxSeriesEpNum.FindStringSubmatch(title); len(m) > 0 {
		applySeriesMatches(m)
	} else if m := rxEpNumLead.FindStringSubmatch(title); len(m) > 0 {
		if season == "" { season = m[1] }
		if episode == "" { episode = m[2] }
		title = strings.TrimSpace(m[3])
	}
	return title, series, season, episode
}

func (c *CastClient) handleMediaMessage(msg CastMessage) {
	var payload mediaStatusPayload
	if err := json.Unmarshal([]byte(msg.PayloadUTF8), &payload); err != nil || payload.Type != "MEDIA_STATUS" || len(payload.Status) == 0 {
		return
	}

	item := payload.Status[0]
	normalizedState := strings.ToLower(item.PlayerState)

	c.mu.Lock()
	prevMediaSessionID, prevPlayerState, prevTitle := c.mediaSessionID, c.lastReportedPlayerState, c.lastReportedTitle
	c.mediaSessionID = item.MediaSessionID

	title, series, season, episode := extractMediaMetadata(item.Media.Metadata)
	duration, currentTime := item.Media.Duration, item.CurrentTime

	if item.MediaSessionID != 0 && item.MediaSessionID != prevMediaSessionID {
		c.lastTitle, c.lastSeries, c.lastSeason, c.lastEpisode = title, series, season, episode
		c.lastDuration, c.lastPosition = duration, currentTime
		c.lastPositionTime = time.Now()
	} else {
		if title != "" { c.lastTitle = title }
		if series != "" { c.lastSeries = series }
		if season != "" { c.lastSeason = season }
		if episode != "" { c.lastEpisode = episode }
		if duration > 0 { c.lastDuration = duration }
	}

	if normalizedState == "idle" {
		if prevPlayerState != "idle" {
			c.resetMediaStateLocked()
			c.playerState = "idle"
			c.lastReportedPlayerState = "idle"
			c.mu.Unlock()
			c.queueDispatch(func() { c.dispatchPlayerState("idle") })
			return
		}
		c.playerState = "idle"
	} else if normalizedState == "playing" || normalizedState == "paused" || normalizedState == "buffering" {
		c.playerState = normalizedState
		shouldDispatch := normalizedState != prevPlayerState ||
			c.lastTitle != prevTitle || c.lastSeries != c.lastReportedSeries ||
			c.lastSeason != c.lastReportedSeason || c.lastEpisode != c.lastReportedEpisode ||
			(item.MediaSessionID != 0 && item.MediaSessionID != c.lastReportedMediaSessionID)

		if !shouldDispatch {
			var expectedTime float64
			if prevPlayerState == "playing" && !c.lastPositionTime.IsZero() {
				expectedTime = c.lastPosition + time.Since(c.lastPositionTime).Seconds()
			} else {
				expectedTime = c.lastPosition
			}
			if math.Abs(currentTime-expectedTime) > 5.0 {
				shouldDispatch = true
			}
		}

		if shouldDispatch {
			c.lastReportedPlayerState = normalizedState
			c.lastReportedTitle = c.lastTitle
			c.lastReportedSeries, c.lastReportedSeason, c.lastReportedEpisode = c.lastSeries, c.lastSeason, c.lastEpisode
			c.lastReportedMediaSessionID = c.mediaSessionID
			c.lastPosition = currentTime
			c.lastPositionTime = time.Now()
			c.mu.Unlock()
			c.queueDispatch(func() { c.dispatchPlayerState(normalizedState) })
			return
		}
	}
	c.mu.Unlock()
}

func (c *CastClient) sendMessage(msg CastMessage) error {
	return c.sendMessageContext(context.Background(), msg)
}

func (c *CastClient) sendMessageContext(ctx context.Context, msg CastMessage) error {
	if err := ctx.Err(); err != nil { return err }
	frame, err := EncodeCastMessage(msg)
	if err != nil { return err }
	c.mu.RLock(); conn := c.conn; c.mu.RUnlock()
	if conn == nil { return ErrNotConnected }
	c.writeMu.Lock(); defer c.writeMu.Unlock()
	deadline := time.Now().Add(5 * time.Second)
	if d, ok := ctx.Deadline(); ok { deadline = d }
	_ = conn.SetWriteDeadline(deadline) //nolint:errcheck
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
		case <-ctx.Done(): return
		case fn := <-c.dispatchCh: fn()
		}
	}
}

func (c *CastClient) postJSON(endpoint string, payload any, op string) {
	if data, err := json.Marshal(payload); err == nil {
		c.postToCore(strings.TrimRight(c.cfg.CoreURL, "/")+"/api/video/"+endpoint, data, op)
	}
}

func (c *CastClient) dispatchTrigger() {
	streamURL := c.cfg.StreamURL
	if streamURL == "" { streamURL = "http://localhost:1984/api/webrtc?src=cast" }
	controlURL := c.cfg.ControlURL
	if controlURL == "" { controlURL = "http://cast-watcher:8090/action" }
	payload := map[string]any{
		"id": "chromecast", "type": "webrtc", "priority": "persistent",
		"stream_url": streamURL, "controllable": true, "control_url": controlURL,
	}
	c.mu.RLock()
	title, series, season, episode := c.lastTitle, c.lastSeries, c.lastSeason, c.lastEpisode
	duration, position, posTime := c.lastDuration, c.lastPosition, c.lastPositionTime
	c.mu.RUnlock()
	for k, v := range map[string]string{"title": title, "series": series, "season": season, "episode": episode} {
		if v != "" { payload[k] = v }
	}
	if duration > 0 { payload["duration"] = duration }
	if position > 0 {
		posUpdatedAt := time.Now().UnixMilli()
		if !posTime.IsZero() { posUpdatedAt = posTime.UnixMilli() }
		payload["position"] = position
		payload["position_updated_at"] = posUpdatedAt
	}
	c.postJSON("trigger", payload, "trigger")
}

func (c *CastClient) dispatchDismiss() {
	c.postJSON("dismiss", map[string]any{"id": "chromecast"}, "dismiss")
}

func (c *CastClient) dispatchPlayerState(state string) {
	c.mu.RLock()
	title, series, season, episode := c.lastTitle, c.lastSeries, c.lastSeason, c.lastEpisode
	duration, position, posTime := c.lastDuration, c.lastPosition, c.lastPositionTime
	c.mu.RUnlock()
	posUpdatedAt := time.Now().UnixMilli()
	if !posTime.IsZero() { posUpdatedAt = posTime.UnixMilli() }
	c.postJSON("state", map[string]any{
		"id": "chromecast", "player_state": state, "title": title, "series": series,
		"season": season, "episode": episode, "position": position, "duration": duration,
		"position_updated_at": posUpdatedAt,
	}, "state")
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
		c.resetReportedOnFailure(operation)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512)) //nolint:errcheck
		c.logger.Warn("[CastWatcher] Core rejected webhook", "operation", operation, "status", resp.StatusCode, "response", string(body))
		c.resetReportedOnFailure(operation)
	} else {
		c.logger.Debug("[CastWatcher] Core webhook accepted", "operation", operation, "status", resp.StatusCode)
	}
}
