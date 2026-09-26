package video

import "errors"

const (
	// ModeWidgets indicates idle dashboard grid mode.
	ModeWidgets = "widgets"
	// ModeVideo indicates dedicated video presentation mode.
	ModeVideo = "video"

	// PriorityPersistent designates media streaming that remains active until stopped (e.g. Chromecast).
	PriorityPersistent = "persistent"
	// PriorityTemporary designates alert streams that auto-dismiss after timeout (e.g. Doorbell).
	PriorityTemporary = "temporary"

	// TypeWebRTC designates WebRTC video streaming.
	TypeWebRTC = "webrtc"
	// TypeHLS designates HTTP Live Streaming.
	TypeHLS = "hls"
	// TypeMJPEG designates Motion JPEG video streaming.
	TypeMJPEG = "mjpeg"

	// PlayerStatePlaying indicates active media playback.
	PlayerStatePlaying = "playing"
	// PlayerStatePaused indicates paused media playback.
	PlayerStatePaused = "paused"
	// PlayerStateBuffering indicates media buffering.
	PlayerStateBuffering = "buffering"

	// ActionTogglePlayback toggles play/pause on controllable streams.
	ActionTogglePlayback = "toggle_playback"
	// ActionPlay resumes playback on controllable streams.
	ActionPlay = "play"
	// ActionPause pauses playback on controllable streams.
	ActionPause = "pause"

	// DefaultTemporaryTimeoutSeconds is the fallback auto-dismiss timeout for temporary streams per SPEC-004.
	DefaultTemporaryTimeoutSeconds = 45
)

var (
	// ErrStreamNotFound indicates the requested stream ID is not active in the video stack.
	ErrStreamNotFound = errors.New("stream not active")
	// ErrNotControllable indicates the stream does not support remote transport actions.
	ErrNotControllable = errors.New("stream is not controllable")
	// ErrControllerUnreachable indicates the upstream stream sidecar is unreachable or returned non-2xx.
	ErrControllerUnreachable = errors.New("stream controller unreachable")
	// ErrInvalidStream indicates missing or malformed stream parameters.
	ErrInvalidStream = errors.New("invalid stream: id and stream_url are required")
	// ErrInvalidAction indicates an unsupported transport action.
	ErrInvalidAction = errors.New("invalid action: must be toggle_playback, play, or pause")
	// ErrInvalidPlayerState indicates an unsupported player transport state.
	ErrInvalidPlayerState = errors.New("invalid player_state: must be playing, paused, or buffering")
)

// VideoStream defines an active video stream within the priority stack.
type VideoStream struct {
	ID             string `json:"id"`
	StreamURL      string `json:"stream_url"`
	Type           string `json:"type"`
	Priority       string `json:"priority,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
	Controllable   bool   `json:"controllable,omitempty"`
	ControlURL     string `json:"control_url,omitempty"`
	PlayerState    string `json:"player_state,omitempty"`
	Muted          bool   `json:"muted,omitempty"`
}

// VideoState defines the authoritative video presentation snapshot matching SPEC-006 §2.E and SPEC-004 §4.
type VideoState struct {
	Mode    string       `json:"mode"`
	Primary *VideoStream `json:"primary"`
	Pip     *VideoStream `json:"pip"`
}

// ActionRequest encapsulates a transport control command forwarded to a sidecar control_url.
type ActionRequest struct {
	ID     string `json:"id"`
	Action string `json:"action"`
	Value  any    `json:"value"`
}
