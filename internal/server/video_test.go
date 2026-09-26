package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azylman/mirrormere/internal/api"
	"github.com/azylman/mirrormere/internal/video"
)

type mockVideoCoordinator struct {
	state          video.VideoState
	triggerErr     error
	dismissErr     error
	playerStateErr error
	actionErr      error
	lastStream     video.VideoStream
	lastDismissID  string
	lastStateID    string
	lastStateVal   string
	lastActionID   string
	lastActionVal  string
}

func (m *mockVideoCoordinator) GetState() video.VideoState {
	return m.state
}

func (m *mockVideoCoordinator) Trigger(stream video.VideoStream) (video.VideoState, error) {
	if m.triggerErr != nil {
		return video.VideoState{}, m.triggerErr
	}
	m.lastStream = stream
	m.state = video.VideoState{
		Mode:    video.ModeVideo,
		Primary: &stream,
	}
	return m.state, nil
}

func (m *mockVideoCoordinator) Dismiss(id string) (video.VideoState, error) {
	if m.dismissErr != nil {
		return video.VideoState{}, m.dismissErr
	}
	m.lastDismissID = id
	m.state = video.VideoState{
		Mode: video.ModeWidgets,
	}
	return m.state, nil
}

func (m *mockVideoCoordinator) SetPlayerState(id string, playerState string) (video.VideoState, error) {
	if m.playerStateErr != nil {
		return video.VideoState{}, m.playerStateErr
	}
	m.lastStateID = id
	m.lastStateVal = playerState
	return m.state, nil
}

func (m *mockVideoCoordinator) ForwardAction(ctx context.Context, id, action string, value any) error {
	m.lastActionID = id
	m.lastActionVal = action
	return m.actionErr
}

func TestDefaultVideoHandler_GetVideoState(t *testing.T) {
	t.Parallel()

	// 1. Success GET
	mock := &mockVideoCoordinator{
		state: video.VideoState{
			Mode: video.ModeVideo,
			Primary: &video.VideoStream{
				ID:             "cast",
				StreamURL:      "http://stream",
				Type:           video.TypeWebRTC,
				Priority:       video.PriorityPersistent,
				TimeoutSeconds: 0,
				Controllable:   true,
				ControlURL:     "http://ctrl",
				PlayerState:    video.PlayerStatePlaying,
				Muted:          false,
			},
			Pip: &video.VideoStream{
				ID:             "doorbell",
				StreamURL:      "http://doorbell",
				Type:           video.TypeWebRTC,
				Priority:       video.PriorityTemporary,
				TimeoutSeconds: 45,
				Controllable:   false,
				Muted:          true,
			},
		},
	}
	h := NewDefaultVideoHandler(mock)

	req := httptest.NewRequest(http.MethodGet, "/api/video/state", nil)
	rec := httptest.NewRecorder()
	h.GetVideoState(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp api.VideoStateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Mode != api.Video || resp.Primary == nil || resp.Primary.Id != "cast" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if resp.Pip == nil || resp.Pip.Id != "doorbell" || !*resp.Pip.Muted {
		t.Fatalf("unexpected pip in response: %+v", resp.Pip)
	}

	// 2. HEAD method
	reqHead := httptest.NewRequest(http.MethodHead, "/api/video/state", nil)
	recHead := httptest.NewRecorder()
	h.GetVideoState(recHead, reqHead)
	if recHead.Code != http.StatusOK {
		t.Fatalf("expected 200 for HEAD, got %d", recHead.Code)
	}
	if recHead.Body.Len() != 0 {
		t.Fatalf("expected empty body for HEAD, got %d bytes", recHead.Body.Len())
	}

	// 3. OPTIONS preflight
	reqOptions := httptest.NewRequest(http.MethodOptions, "/api/video/state", nil)
	recOptions := httptest.NewRecorder()
	h.GetVideoState(recOptions, reqOptions)
	if recOptions.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for OPTIONS, got %d", recOptions.Code)
	}

	// 4. Method Not Allowed
	reqPost := httptest.NewRequest(http.MethodPost, "/api/video/state", nil)
	recPost := httptest.NewRecorder()
	h.GetVideoState(recPost, reqPost)
	if recPost.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", recPost.Code)
	}

	// 5. Nil coordinator
	hNil := NewDefaultVideoHandler(nil)
	recNil := httptest.NewRecorder()
	hNil.GetVideoState(recNil, req)
	if recNil.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when coord is nil, got %d", recNil.Code)
	}
}

func TestDefaultVideoHandler_PostVideoTrigger(t *testing.T) {
	t.Parallel()

	mock := &mockVideoCoordinator{}
	h := NewDefaultVideoHandler(mock)

	// 1. Success POST
	body := `{"id":"chromecast","stream_url":"http://127.0.0.1:1984/cast","type":"webrtc","priority":"persistent","controllable":true,"control_url":"http://ctrl","player_state":"playing"}`
	req := httptest.NewRequest(http.MethodPost, "/api/video/trigger", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.PostVideoTrigger(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if mock.lastStream.ID != "chromecast" || mock.lastStream.Priority != video.PriorityPersistent {
		t.Fatalf("unexpected stream triggered: %+v", mock.lastStream)
	}

	// 2. OPTIONS preflight
	reqOptions := httptest.NewRequest(http.MethodOptions, "/api/video/trigger", nil)
	recOptions := httptest.NewRecorder()
	h.PostVideoTrigger(recOptions, reqOptions)
	if recOptions.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for OPTIONS, got %d", recOptions.Code)
	}

	// 3. Method Not Allowed
	reqGet := httptest.NewRequest(http.MethodGet, "/api/video/trigger", nil)
	recGet := httptest.NewRecorder()
	h.PostVideoTrigger(recGet, reqGet)
	if recGet.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", recGet.Code)
	}

	// 4. Bad JSON
	reqBad := httptest.NewRequest(http.MethodPost, "/api/video/trigger", strings.NewReader(`not-json`))
	recBad := httptest.NewRecorder()
	h.PostVideoTrigger(recBad, reqBad)
	if recBad.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad json, got %d", recBad.Code)
	}

	// 5. Missing required fields
	reqMissing := httptest.NewRequest(http.MethodPost, "/api/video/trigger", strings.NewReader(`{"id":""}`))
	recMissing := httptest.NewRecorder()
	h.PostVideoTrigger(recMissing, reqMissing)
	if recMissing.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing fields, got %d", recMissing.Code)
	}

	// 6. Coordinator Trigger Error
	mock.triggerErr = video.ErrInvalidPlayerState
	reqErr := httptest.NewRequest(http.MethodPost, "/api/video/trigger", strings.NewReader(body))
	recErr := httptest.NewRecorder()
	h.PostVideoTrigger(recErr, reqErr)
	if recErr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on coordinator error, got %d", recErr.Code)
	}

	// 7. Nil coordinator
	hNil := NewDefaultVideoHandler(nil)
	recNil := httptest.NewRecorder()
	hNil.PostVideoTrigger(recNil, req)
	if recNil.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when coord is nil, got %d", recNil.Code)
	}
}

func TestDefaultVideoHandler_PostVideoDismiss(t *testing.T) {
	t.Parallel()

	mock := &mockVideoCoordinator{}
	h := NewDefaultVideoHandler(mock)

	// 1. Success POST
	req := httptest.NewRequest(http.MethodPost, "/api/video/dismiss", strings.NewReader(`{"id":"chromecast"}`))
	rec := httptest.NewRecorder()
	h.PostVideoDismiss(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if mock.lastDismissID != "chromecast" {
		t.Fatalf("expected dismiss ID 'chromecast', got %s", mock.lastDismissID)
	}

	// 2. Stream not found (404)
	mock.dismissErr = video.ErrStreamNotFound
	req404 := httptest.NewRequest(http.MethodPost, "/api/video/dismiss", strings.NewReader(`{"id":"missing"}`))
	rec404 := httptest.NewRecorder()
	h.PostVideoDismiss(rec404, req404)
	if rec404.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec404.Code)
	}

	// 3. Other coordinator error (400)
	mock.dismissErr = errors.New("other dismiss error")
	reqOther := httptest.NewRequest(http.MethodPost, "/api/video/dismiss", strings.NewReader(`{"id":"other"}`))
	recOther := httptest.NewRecorder()
	h.PostVideoDismiss(recOther, reqOther)
	if recOther.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", recOther.Code)
	}

	// 4. Bad JSON
	reqBad := httptest.NewRequest(http.MethodPost, "/api/video/dismiss", strings.NewReader(`not-json`))
	recBad := httptest.NewRecorder()
	h.PostVideoDismiss(recBad, reqBad)
	if recBad.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad json, got %d", recBad.Code)
	}

	// 5. Missing ID
	reqNoID := httptest.NewRequest(http.MethodPost, "/api/video/dismiss", strings.NewReader(`{"id":""}`))
	recNoID := httptest.NewRecorder()
	h.PostVideoDismiss(recNoID, reqNoID)
	if recNoID.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty id, got %d", recNoID.Code)
	}

	// 6. OPTIONS preflight
	reqOptions := httptest.NewRequest(http.MethodOptions, "/api/video/dismiss", nil)
	recOptions := httptest.NewRecorder()
	h.PostVideoDismiss(recOptions, reqOptions)
	if recOptions.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for OPTIONS, got %d", recOptions.Code)
	}

	// 7. Method Not Allowed
	reqGet := httptest.NewRequest(http.MethodGet, "/api/video/dismiss", nil)
	recGet := httptest.NewRecorder()
	h.PostVideoDismiss(recGet, reqGet)
	if recGet.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", recGet.Code)
	}

	// 8. Nil coordinator
	hNil := NewDefaultVideoHandler(nil)
	recNil := httptest.NewRecorder()
	hNil.PostVideoDismiss(recNil, req)
	if recNil.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when coord is nil, got %d", recNil.Code)
	}
}

func TestDefaultVideoHandler_PostVideoAction(t *testing.T) {
	t.Parallel()

	mock := &mockVideoCoordinator{}
	h := NewDefaultVideoHandler(mock)

	// 1. Success POST
	req := httptest.NewRequest(http.MethodPost, "/api/video/action", strings.NewReader(`{"id":"chromecast","action":"toggle_playback"}`))
	rec := httptest.NewRecorder()
	h.PostVideoAction(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if mock.lastActionID != "chromecast" || mock.lastActionVal != "toggle_playback" {
		t.Fatalf("unexpected action: id=%s action=%s", mock.lastActionID, mock.lastActionVal)
	}

	// 2. Stream not found (404)
	mock.actionErr = video.ErrStreamNotFound
	req404 := httptest.NewRequest(http.MethodPost, "/api/video/action", strings.NewReader(`{"id":"missing","action":"play"}`))
	rec404 := httptest.NewRecorder()
	h.PostVideoAction(rec404, req404)
	if rec404.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec404.Code)
	}

	// 3. Not controllable (422)
	mock.actionErr = video.ErrNotControllable
	req422 := httptest.NewRequest(http.MethodPost, "/api/video/action", strings.NewReader(`{"id":"doorbell","action":"play"}`))
	rec422 := httptest.NewRecorder()
	h.PostVideoAction(rec422, req422)
	if rec422.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", rec422.Code)
	}

	// 4. Controller unreachable (502)
	mock.actionErr = video.ErrControllerUnreachable
	req502 := httptest.NewRequest(http.MethodPost, "/api/video/action", strings.NewReader(`{"id":"chromecast","action":"play"}`))
	rec502 := httptest.NewRecorder()
	h.PostVideoAction(rec502, req502)
	if rec502.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", rec502.Code)
	}

	// 5. Other error (400)
	mock.actionErr = errors.New("other action error")
	reqOther := httptest.NewRequest(http.MethodPost, "/api/video/action", strings.NewReader(`{"id":"chromecast","action":"play"}`))
	recOther := httptest.NewRecorder()
	h.PostVideoAction(recOther, reqOther)
	if recOther.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", recOther.Code)
	}

	// 6. Bad JSON
	reqBad := httptest.NewRequest(http.MethodPost, "/api/video/action", strings.NewReader(`not-json`))
	recBad := httptest.NewRecorder()
	h.PostVideoAction(recBad, reqBad)
	if recBad.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", recBad.Code)
	}

	// 7. Missing fields
	reqMissing := httptest.NewRequest(http.MethodPost, "/api/video/action", strings.NewReader(`{"id":""}`))
	recMissing := httptest.NewRecorder()
	h.PostVideoAction(recMissing, reqMissing)
	if recMissing.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", recMissing.Code)
	}

	// 8. OPTIONS preflight
	reqOptions := httptest.NewRequest(http.MethodOptions, "/api/video/action", nil)
	recOptions := httptest.NewRecorder()
	h.PostVideoAction(recOptions, reqOptions)
	if recOptions.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", recOptions.Code)
	}

	// 9. Method Not Allowed
	reqGet := httptest.NewRequest(http.MethodGet, "/api/video/action", nil)
	recGet := httptest.NewRecorder()
	h.PostVideoAction(recGet, reqGet)
	if recGet.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", recGet.Code)
	}

	// 10. Nil coordinator
	hNil := NewDefaultVideoHandler(nil)
	recNil := httptest.NewRecorder()
	hNil.PostVideoAction(recNil, req)
	if recNil.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when coord is nil, got %d", recNil.Code)
	}
}

func TestDefaultVideoHandler_PostVideoState(t *testing.T) {
	t.Parallel()

	mock := &mockVideoCoordinator{}
	h := NewDefaultVideoHandler(mock)

	// 1. Success POST
	req := httptest.NewRequest(http.MethodPost, "/api/video/state", strings.NewReader(`{"id":"chromecast","player_state":"playing"}`))
	rec := httptest.NewRecorder()
	h.PostVideoState(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if mock.lastStateID != "chromecast" || mock.lastStateVal != "playing" {
		t.Fatalf("unexpected state update: id=%s val=%s", mock.lastStateID, mock.lastStateVal)
	}

	// 2. Stream not found (404)
	mock.playerStateErr = video.ErrStreamNotFound
	req404 := httptest.NewRequest(http.MethodPost, "/api/video/state", strings.NewReader(`{"id":"missing","player_state":"paused"}`))
	rec404 := httptest.NewRecorder()
	h.PostVideoState(rec404, req404)
	if rec404.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec404.Code)
	}

	// 3. Other error (400)
	mock.playerStateErr = video.ErrInvalidPlayerState
	reqErr := httptest.NewRequest(http.MethodPost, "/api/video/state", strings.NewReader(`{"id":"chromecast","player_state":"invalid"}`))
	recErr := httptest.NewRecorder()
	h.PostVideoState(recErr, reqErr)
	if recErr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", recErr.Code)
	}

	// 4. Bad JSON
	reqBad := httptest.NewRequest(http.MethodPost, "/api/video/state", strings.NewReader(`not-json`))
	recBad := httptest.NewRecorder()
	h.PostVideoState(recBad, reqBad)
	if recBad.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", recBad.Code)
	}

	// 5. Missing fields
	reqMissing := httptest.NewRequest(http.MethodPost, "/api/video/state", strings.NewReader(`{"id":""}`))
	recMissing := httptest.NewRecorder()
	h.PostVideoState(recMissing, reqMissing)
	if recMissing.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", recMissing.Code)
	}

	// 6. OPTIONS preflight
	reqOptions := httptest.NewRequest(http.MethodOptions, "/api/video/state", nil)
	recOptions := httptest.NewRecorder()
	h.PostVideoState(recOptions, reqOptions)
	if recOptions.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", recOptions.Code)
	}

	// 7. Method Not Allowed
	reqDelete := httptest.NewRequest(http.MethodDelete, "/api/video/state", nil)
	recDelete := httptest.NewRecorder()
	h.PostVideoState(recDelete, reqDelete)
	if recDelete.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", recDelete.Code)
	}

	// 8. Nil coordinator
	hNil := NewDefaultVideoHandler(nil)
	recNil := httptest.NewRecorder()
	hNil.PostVideoState(recNil, req)
	if recNil.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when coord is nil, got %d", recNil.Code)
	}
}
