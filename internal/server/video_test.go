package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/azylman/mirrormere/internal/api"
	"github.com/azylman/mirrormere/internal/server"
	"github.com/azylman/mirrormere/internal/video"
)

type mockVideoCoordinator struct {
	state         video.VideoState
	err           error
	lastStream    video.VideoStream
	lastDismissID string
	lastActionID  string
	lastActionVal string
}

func (m *mockVideoCoordinator) GetState() video.VideoState { return m.state }
func (m *mockVideoCoordinator) Trigger(s video.VideoStream) (video.VideoState, error) {
	m.lastStream, m.state = s, video.VideoState{Mode: video.ModeVideo, Primary: &s}
	return m.state, m.err
}

func (m *mockVideoCoordinator) Dismiss(id string) (video.VideoState, error) {
	m.lastDismissID, m.state = id, video.VideoState{Mode: video.ModeWidgets}
	return m.state, m.err
}

func (m *mockVideoCoordinator) UpdatePlayerState(id string, u video.VideoStream) (video.VideoState, error) {
	m.lastStream = u
	return m.state, m.err
}

func (m *mockVideoCoordinator) ForwardAction(ctx context.Context, id, act string, val any) error {
	m.lastActionID, m.lastActionVal = id, act
	return m.err
}

type videoTC struct {
	name, method, body string
	nilCoord           bool
	coordErr           error
	wantCode           int
}

// videoStreamTC is videoTC plus the stream the coordinator must receive (nil: not asserted).
type videoStreamTC struct {
	name, method, body string
	nilCoord           bool
	coordErr           error
	wantCode           int
	wantStream         *video.VideoStream
}

func runVideoStreamCase(t *testing.T, tc videoStreamTC, pick func(*server.DefaultVideoHandler) http.HandlerFunc, path string) {
	t.Helper()
	h, mock := videoHandler(tc.nilCoord, tc.coordErr, video.VideoState{})
	rec := doVideoReq(pick(h), tc.method, path, tc.body)
	if rec.Code != tc.wantCode {
		t.Fatalf("code = %d, want %d", rec.Code, tc.wantCode)
	}
	if tc.wantStream != nil && !reflect.DeepEqual(mock.lastStream, *tc.wantStream) {
		t.Fatalf("coordinator stream = %+v, want %+v", mock.lastStream, *tc.wantStream)
	}
}

func doVideoReq(h http.HandlerFunc, m, p, b string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(m, p, strings.NewReader(b)))
	return rec
}

func videoHandler(nilCoord bool, err error, state video.VideoState) (*server.DefaultVideoHandler, *mockVideoCoordinator) {
	if nilCoord {
		return server.NewDefaultVideoHandler(nil), nil
	}
	mock := &mockVideoCoordinator{state: state, err: err}
	return server.NewDefaultVideoHandler(mock), mock
}

func TestDefaultVideoHandler_GetVideoState(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, method  string
		nilCoord      bool
		wantCode      int
		wantPrimaryID string
	}{
		{"ok_get", http.MethodGet, false, http.StatusOK, "cast"},
		{"ok_head", http.MethodHead, false, http.StatusOK, ""},
		{"options_cors", http.MethodOptions, false, http.StatusNoContent, ""},
		{"method_not_allowed", http.MethodPost, false, http.StatusMethodNotAllowed, ""},
		{"nil_coord", http.MethodGet, true, http.StatusInternalServerError, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			state := video.VideoState{
				Mode:    video.ModeVideo,
				Primary: &video.VideoStream{ID: "cast", StreamURL: "http://stream", Type: video.TypeWebRTC},
				Pip:     &video.VideoStream{ID: "pip", StreamURL: "http://pip", Muted: true},
			}
			h, _ := videoHandler(tc.nilCoord, nil, state)
			rec := doVideoReq(h.GetVideoState, tc.method, "/api/video/state", "")
			if rec.Code != tc.wantCode {
				t.Fatalf("code = %d, want %d", rec.Code, tc.wantCode)
			}
			if tc.wantPrimaryID == "" {
				return
			}
			var resp api.VideoStateResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Primary == nil || resp.Primary.ID != tc.wantPrimaryID {
				t.Fatalf("unexpected resp: %v, %+v", err, resp)
			}
		})
	}
}

func TestDefaultVideoHandler_PostVideoTrigger(t *testing.T) {
	t.Parallel()
	bodyValid := `{"id":"cast","stream_url":"http://stream","type":"webrtc","priority":"persistent","title":"Show","series":"S1","position":10,"duration":100}`
	triggered := &video.VideoStream{
		ID: "cast", StreamURL: "http://stream", Type: "webrtc", Priority: "persistent", PlayerState: "playing",
		Title: "Show", Series: "S1", Position: 10, Duration: 100,
	}
	defaulted := &video.VideoStream{ID: "c2", StreamURL: "http://stream", Type: "webrtc", Priority: "persistent", PlayerState: "playing"}
	tests := []videoStreamTC{
		{"ok_trigger", http.MethodPost, bodyValid, false, nil, http.StatusOK, triggered},
		{"default_priority", http.MethodPost, `{"id":"c2","stream_url":"http://stream"}`, false, nil, http.StatusOK, defaulted},
		{"options_cors", http.MethodOptions, "", false, nil, http.StatusNoContent, nil},
		{"method_not_allowed", http.MethodGet, "", false, nil, http.StatusMethodNotAllowed, nil},
		{"bad_json", http.MethodPost, "{invalid", false, nil, http.StatusBadRequest, nil},
		{"missing_id", http.MethodPost, `{"id":"","stream_url":"http://s"}`, false, nil, http.StatusBadRequest, nil},
		{"sentinel_all", http.MethodPost, `{"id":"all","stream_url":"http://s"}`, false, nil, http.StatusBadRequest, nil},
		{"sentinel_wildcard", http.MethodPost, `{"id":"*","stream_url":"http://s"}`, false, nil, http.StatusBadRequest, nil},
		{"coord_err", http.MethodPost, bodyValid, false, video.ErrInvalidPlayerState, http.StatusBadRequest, nil},
		{"nil_coord", http.MethodPost, bodyValid, true, nil, http.StatusInternalServerError, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runVideoStreamCase(t, tc, func(h *server.DefaultVideoHandler) http.HandlerFunc { return h.PostVideoTrigger }, "/api/video/trigger")
		})
	}
}

func TestDefaultVideoHandler_PostVideoDismiss(t *testing.T) {
	t.Parallel()
	tests := []videoTC{
		{"ok_dismiss", http.MethodPost, `{"id":"cast"}`, false, nil, http.StatusOK},
		{"dismiss_all", http.MethodPost, `{"id":"all"}`, false, nil, http.StatusOK},
		{"dismiss_wildcard", http.MethodPost, `{"id":"*"}`, false, nil, http.StatusOK},
		{"options_cors", http.MethodOptions, "", false, nil, http.StatusNoContent},
		{"method_not_allowed", http.MethodGet, "", false, nil, http.StatusMethodNotAllowed},
		{"not_found", http.MethodPost, `{"id":"cast"}`, false, video.ErrStreamNotFound, http.StatusNotFound},
		{"coord_err", http.MethodPost, `{"id":"cast"}`, false, errors.New("err"), http.StatusBadRequest},
		{"bad_json", http.MethodPost, "{bad", false, nil, http.StatusBadRequest},
		{"missing_id", http.MethodPost, `{"id":""}`, false, nil, http.StatusBadRequest},
		{"nil_coord", http.MethodPost, `{"id":"cast"}`, true, nil, http.StatusInternalServerError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h, _ := videoHandler(tc.nilCoord, tc.coordErr, video.VideoState{})
			rec := doVideoReq(h.PostVideoDismiss, tc.method, "/api/video/dismiss", tc.body)
			if rec.Code != tc.wantCode {
				t.Fatalf("code = %d, want %d", rec.Code, tc.wantCode)
			}
		})
	}
}

func TestDefaultVideoHandler_PostVideoAction(t *testing.T) {
	t.Parallel()
	tests := []videoTC{
		{"ok_action", http.MethodPost, `{"id":"cast","action":"toggle_playback"}`, false, nil, http.StatusOK},
		{"ok_seek", http.MethodPost, `{"id":"cast","action":"seek","value":45.5}`, false, nil, http.StatusOK},
		{"options_cors", http.MethodOptions, "", false, nil, http.StatusNoContent},
		{"method_not_allowed", http.MethodGet, "", false, nil, http.StatusMethodNotAllowed},
		{"not_found", http.MethodPost, `{"id":"cast","action":"play"}`, false, video.ErrStreamNotFound, http.StatusNotFound},
		{"not_controllable", http.MethodPost, `{"id":"cast","action":"play"}`, false, video.ErrNotControllable, http.StatusUnprocessableEntity},
		{"unreachable", http.MethodPost, `{"id":"cast","action":"play"}`, false, video.ErrControllerUnreachable, http.StatusBadGateway},
		{"other_err", http.MethodPost, `{"id":"cast","action":"play"}`, false, errors.New("err"), http.StatusBadRequest},
		{"bad_json", http.MethodPost, "{bad", false, nil, http.StatusBadRequest},
		{"missing_fields", http.MethodPost, `{"id":""}`, false, nil, http.StatusBadRequest},
		{"nil_coord", http.MethodPost, `{"id":"cast","action":"play"}`, true, nil, http.StatusInternalServerError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h, _ := videoHandler(tc.nilCoord, tc.coordErr, video.VideoState{})
			rec := doVideoReq(h.PostVideoAction, tc.method, "/api/video/action", tc.body)
			if rec.Code != tc.wantCode {
				t.Fatalf("code = %d, want %d", rec.Code, tc.wantCode)
			}
		})
	}
}

func TestDefaultVideoHandler_PostVideoState(t *testing.T) {
	t.Parallel()
	playing := &video.VideoStream{ID: "cast", PlayerState: "playing", Title: "Ep1", Position: 25}
	tests := []videoStreamTC{
		{"ok_state", http.MethodPost, `{"id":"cast","player_state":"playing","title":"Ep1","position":25.0}`, false, nil, http.StatusOK, playing},
		{"ok_idle", http.MethodPost, `{"id":"cast","player_state":"idle"}`, false, nil, http.StatusOK, &video.VideoStream{ID: "cast", PlayerState: "idle"}},
		{"options_cors", http.MethodOptions, "", false, nil, http.StatusNoContent, nil},
		{"method_not_allowed", http.MethodDelete, "", false, nil, http.StatusMethodNotAllowed, nil},
		{"not_found", http.MethodPost, `{"id":"cast","player_state":"paused"}`, false, video.ErrStreamNotFound, http.StatusNotFound, nil},
		{"coord_err", http.MethodPost, `{"id":"cast","player_state":"paused"}`, false, video.ErrInvalidPlayerState, http.StatusBadRequest, nil},
		{"bad_json", http.MethodPost, "{bad", false, nil, http.StatusBadRequest, nil},
		{"missing_fields", http.MethodPost, `{"id":""}`, false, nil, http.StatusBadRequest, nil},
		{"nil_coord", http.MethodPost, `{"id":"cast","player_state":"paused"}`, true, nil, http.StatusInternalServerError, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runVideoStreamCase(t, tc, func(h *server.DefaultVideoHandler) http.HandlerFunc { return h.PostVideoState }, "/api/video/state")
		})
	}
}
