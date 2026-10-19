package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type mockServer struct {
	lastWidgetID, lastType, lastPath, videoMode, lastActionID, lastAction, lastDismissID, lastTriggerID, lastVoiceState string
	healthCalls, healthzCalls, audioVolume                                                                              int
	audioVolumeSet, audioMuted                                                                                          bool
	videoPrimary, videoPip                                                                                              *VideoStream
}

func writeMockJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeMockErr(w http.ResponseWriter, code int, msg string) {
	writeMockJSON(w, code, ErrorResponse{Status: "error", Error: msg})
}

func decodeMockBody[T any](w http.ResponseWriter, r *http.Request, errMsg string) (T, bool) {
	var req T
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeMockErr(w, http.StatusBadRequest, errMsg)
		return req, false
	}
	return req, true
}

func (m *mockServer) PostWidgetPush(w http.ResponseWriter, r *http.Request, widgetID string) {
	m.lastWidgetID = widgetID
	if widgetID == "not-found" {
		writeMockErr(w, http.StatusNotFound, fmt.Sprintf("widget '%s' not found in active configuration", widgetID))
	} else if widgetID == "conflict-tasks" {
		writeMockErr(w, http.StatusConflict, "cannot push state to list-backed widget")
	} else if _, ok := decodeMockBody[map[string]any](w, r, "invalid json"); ok {
		writeMockJSON(w, http.StatusOK, WidgetPushResponse{Status: "ok", WidgetId: widgetID, UpdatedAt: time.Date(2026, 9, 26, 6, 0, 0, 0, time.UTC)})
	}
}

func (m *mockServer) GetHealth(w http.ResponseWriter, r *http.Request) {
	m.healthCalls++
	writeMockJSON(w, http.StatusOK, HealthResponse{Status: "ok"})
}

func (m *mockServer) GetHealthz(w http.ResponseWriter, r *http.Request) {
	m.healthzCalls++
	writeMockJSON(w, http.StatusOK, HealthResponse{Status: "ok"})
}

func (m *mockServer) GetWidgetRender(w http.ResponseWriter, r *http.Request, widgetID string) {
	m.lastWidgetID = widgetID
	if widgetID == "not-found" {
		writeMockErr(w, http.StatusNotFound, fmt.Sprintf("widget '%s' not found in active configuration", widgetID))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(fmt.Sprintf("<div id=\"%s\">rendered</div>", widgetID)))
}

func (m *mockServer) GetWidgetAsset(w http.ResponseWriter, r *http.Request, pType, path string) {
	m.lastType, m.lastPath = pType, path
	if pType == "missing" {
		writeMockErr(w, http.StatusNotFound, "asset not found")
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("<svg></svg>"))
}

func (m *mockServer) PostScreenAdvance(w http.ResponseWriter, r *http.Request) {
	writeMockJSON(w, http.StatusOK, ScreenNavigationResponse{Status: "ok", CurrentScreen: 1, TotalScreens: 2})
}

func (m *mockServer) PostScreenPause(w http.ResponseWriter, r *http.Request) {
	writeMockJSON(w, http.StatusOK, ScreenPauseResponse{Status: "ok", Paused: true, CurrentScreen: 0})
}

func (m *mockServer) PostScreenSelect(w http.ResponseWriter, r *http.Request) {
	writeMockJSON(w, http.StatusOK, ScreenNavigationResponse{Status: "ok", CurrentScreen: 1, TotalScreens: 2})
}

func (m *mockServer) CompleteListItem(w http.ResponseWriter, r *http.Request, listId, itemId string) {
	if listId == "not-found" || itemId == "not-found" {
		writeMockErr(w, http.StatusNotFound, "not found")
		return
	}
	writeMockJSON(w, http.StatusOK, ActionResponse{Status: "ok"})
}

func (m *mockServer) GetAudio(w http.ResponseWriter, r *http.Request) {
	vol := m.audioVolume
	if vol == 0 && !m.audioVolumeSet {
		vol = 75
	}
	writeMockJSON(w, http.StatusOK, AudioStateResponse{Status: "ok", Volume: vol, Muted: m.audioMuted})
}

func (m *mockServer) PostAudioVolume(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeMockBody[map[string]any](w, r, "invalid json")
	if !ok {
		return
	}
	floatVol, isFloat := body["volume"].(float64)
	if !isFloat || floatVol != float64(int(floatVol)) || floatVol < 0 || floatVol > 100 {
		writeMockErr(w, http.StatusBadRequest, "invalid volume: must be an integer between 0 and 100")
		return
	}
	m.audioVolume, m.audioVolumeSet = int(floatVol), true
	writeMockJSON(w, http.StatusOK, AudioStateResponse{Status: "ok", Volume: m.audioVolume, Muted: m.audioMuted})
}

func (m *mockServer) PostAudioMute(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	if rawMuted, exists := body["muted"]; exists {
		b, ok := rawMuted.(bool)
		if !ok {
			writeMockErr(w, http.StatusBadRequest, "invalid payload: muted must be a boolean")
			return
		}
		m.audioMuted = b
	} else {
		m.audioMuted = !m.audioMuted
	}
	vol := m.audioVolume
	if vol == 0 && !m.audioVolumeSet {
		vol = 75
	}
	writeMockJSON(w, http.StatusOK, AudioStateResponse{Status: "ok", Volume: vol, Muted: m.audioMuted})
}

func (m *mockServer) GetVideoState(w http.ResponseWriter, r *http.Request) {
	mode := m.videoMode
	if mode == "" {
		mode = "widgets"
	}
	writeMockJSON(w, http.StatusOK, VideoStateResponse{Status: "ok", Mode: VideoStateResponseMode(mode), Primary: m.videoPrimary, Pip: m.videoPip})
}

func (m *mockServer) PostVideoTrigger(w http.ResponseWriter, r *http.Request) {
	if req, ok := decodeMockBody[VideoTriggerRequest](w, r, "invalid trigger payload"); ok {
		m.lastTriggerID = req.Id
		vType := VideoStreamTypeWebrtc
		if req.Type != nil {
			vType = VideoStreamType(*req.Type)
		}
		m.videoPrimary = &VideoStream{Id: req.Id, StreamUrl: req.StreamUrl, Type: vType}
		m.videoMode = "video"
		writeMockJSON(w, http.StatusOK, VideoStateResponse{Status: "ok", Mode: "video", Primary: m.videoPrimary})
	}
}

func (m *mockServer) PostVideoDismiss(w http.ResponseWriter, r *http.Request) {
	if req, ok := decodeMockBody[VideoDismissRequest](w, r, "invalid dismiss payload"); ok {
		if req.Id == "not-found" {
			writeMockErr(w, http.StatusNotFound, "stream not active")
			return
		}
		m.lastDismissID, m.videoPrimary, m.videoPip, m.videoMode = req.Id, nil, nil, "widgets"
		writeMockJSON(w, http.StatusOK, VideoStateResponse{Status: "ok", Mode: "widgets"})
	}
}

func (m *mockServer) PostVideoAction(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeMockBody[VideoActionRequest](w, r, "invalid action payload")
	if !ok {
		return
	}
	switch req.Id {
	case "not-found":
		writeMockErr(w, http.StatusNotFound, "stream not active")
	case "not-controllable":
		writeMockErr(w, http.StatusUnprocessableEntity, "stream is not controllable")
	case "unreachable":
		writeMockErr(w, http.StatusBadGateway, "stream controller unreachable")
	default:
		m.lastActionID, m.lastAction = req.Id, string(req.Action)
		writeMockJSON(w, http.StatusOK, ActionResponse{Status: "ok"})
	}
}

func (m *mockServer) PostVideoState(w http.ResponseWriter, r *http.Request) {
	if req, ok := decodeMockBody[VideoPlayerStateRequest](w, r, "invalid player state payload"); ok {
		if req.Id == "not-found" {
			writeMockErr(w, http.StatusNotFound, "stream not active")
			return
		}
		writeMockJSON(w, http.StatusOK, VideoPlayerStateResponse{Status: "ok", Id: req.Id, PlayerState: VideoPlayerStateResponsePlayerState(req.PlayerState)})
	}
}

func (m *mockServer) PostVoiceState(w http.ResponseWriter, r *http.Request) {
	if req, ok := decodeMockBody[VoiceStateRequest](w, r, "invalid voice state payload"); ok {
		m.lastVoiceState = string(req.State)
		writeMockJSON(w, http.StatusOK, VoiceStateResponse{Status: "ok"})
	}
}

func (m *mockServer) PostVoiceInteract(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("event: state\ndata: {\"state\":\"transcribing\"}\n\n"))
}

func TestHandler_Endpoints(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, method, path, body                                                           string
		initMuted                                                                          bool
		wantCode                                                                           int
		wantSub, wantWidgetID, wantType, wantPath, wantTriggerID, wantDismissID, wantActionID, wantAction, wantVoiceState string
	}{
		{"audio_get", http.MethodGet, "/api/audio", "", false, http.StatusOK, `"volume":75`, "", "", "", "", "", "", "", ""},
		{"audio_volume_200", http.MethodPost, "/api/audio/volume", `{"volume":80}`, false, http.StatusOK, `"volume":80`, "", "", "", "", "", "", "", ""},
		{"audio_volume_empty", http.MethodPost, "/api/audio/volume", `{}`, false, http.StatusBadRequest, "between 0 and 100", "", "", "", "", "", "", "", ""},
		{"audio_volume_oob", http.MethodPost, "/api/audio/volume", `{"volume":150}`, false, http.StatusBadRequest, "between 0 and 100", "", "", "", "", "", "", "", ""},
		{"audio_volume_bad_json", http.MethodPost, "/api/audio/volume", `{invalid`, false, http.StatusBadRequest, "invalid json", "", "", "", "", "", "", "", ""},
		{"audio_mute_toggle", http.MethodPost, "/api/audio/mute", `{}`, false, http.StatusOK, `"muted":true`, "", "", "", "", "", "", "", ""},
		{"audio_mute_explicit_false", http.MethodPost, "/api/audio/mute", `{"muted":false}`, true, http.StatusOK, `"muted":false`, "", "", "", "", "", "", "", ""},
		{"audio_mute_bad_type", http.MethodPost, "/api/audio/mute", `{"muted":"notabool"}`, false, http.StatusBadRequest, "must be a boolean", "", "", "", "", "", "", "", ""},
		{"healthz", http.MethodGet, "/healthz", "", false, http.StatusOK, `"status":"ok"`, "", "", "", "", "", "", "", ""},
		{"health", http.MethodGet, "/health", "", false, http.StatusOK, `"status":"ok"`, "", "", "", "", "", "", "", ""},
		{"widget_render_200", http.MethodGet, "/api/widgets/clock-primary/render", "", false, http.StatusOK, "clock-primary", "clock-primary", "", "", "", "", "", "", ""},
		{"widget_render_404", http.MethodGet, "/api/widgets/not-found/render", "", false, http.StatusNotFound, "not found", "", "", "", "", "", "", "", ""},
		{"widget_asset_200", http.MethodGet, "/widget-types/weather/assets/sun.svg", "", false, http.StatusOK, "<svg></svg>", "", "weather", "sun.svg", "", "", "", "", ""},
		{"widget_asset_404", http.MethodGet, "/widget-types/missing/assets/sun.svg", "", false, http.StatusNotFound, "asset not found", "", "", "", "", "", "", "", ""},
		{"screen_advance", http.MethodPost, "/api/screen/advance", `{"direction":"next"}`, false, http.StatusOK, `"current_screen":1`, "", "", "", "", "", "", "", ""},
		{"screen_pause", http.MethodPost, "/api/screen/pause", `{"paused":true,"duration_seconds":120}`, false, http.StatusOK, `"paused":true`, "", "", "", "", "", "", "", ""},
		{"screen_select", http.MethodPost, "/api/screen/select", `{"screen_index":1}`, false, http.StatusOK, `"current_screen":1`, "", "", "", "", "", "", "", ""},
		{"widget_push_200", http.MethodPost, "/api/widgets/sensor-card/push", `{"co2_ppm":640}`, false, http.StatusOK, `"widget_id":"sensor-card"`, "sensor-card", "", "", "", "", "", "", ""},
		{"widget_push_404", http.MethodPost, "/api/widgets/not-found/push", `{"co2_ppm":640}`, false, http.StatusNotFound, "not found", "", "", "", "", "", "", "", ""},
		{"widget_push_409", http.MethodPost, "/api/widgets/conflict-tasks/push", `{"items":[]}`, false, http.StatusConflict, "list-backed widget", "", "", "", "", "", "", "", ""},
		{"widget_push_400", http.MethodPost, "/api/widgets/sensor-card/push", `not-json`, false, http.StatusBadRequest, "invalid json", "", "", "", "", "", "", "", ""},
		{"video_state_get", http.MethodGet, "/api/video/state", "", false, http.StatusOK, `"mode":"widgets"`, "", "", "", "", "", "", "", ""},
		{"video_trigger_200", http.MethodPost, "/api/video/trigger", `{"id":"chromecast","stream_url":"http://127.0.0.1:1984/cast","type":"webrtc"}`, false, http.StatusOK, `"mode":"video"`, "", "", "", "chromecast", "", "", "", ""},
		{"video_trigger_400", http.MethodPost, "/api/video/trigger", `not-json`, false, http.StatusBadRequest, "invalid trigger payload", "", "", "", "", "", "", "", ""},
		{"video_dismiss_200", http.MethodPost, "/api/video/dismiss", `{"id":"chromecast"}`, false, http.StatusOK, `"mode":"widgets"`, "", "", "", "", "chromecast", "", "", ""},
		{"video_dismiss_404", http.MethodPost, "/api/video/dismiss", `{"id":"not-found"}`, false, http.StatusNotFound, "stream not active", "", "", "", "", "", "", "", ""},
		{"video_dismiss_400", http.MethodPost, "/api/video/dismiss", `not-json`, false, http.StatusBadRequest, "invalid dismiss payload", "", "", "", "", "", "", "", ""},
		{"video_action_200", http.MethodPost, "/api/video/action", `{"id":"chromecast","action":"toggle_playback"}`, false, http.StatusOK, `"status":"ok"`, "", "", "", "", "", "chromecast", "toggle_playback", ""},
		{"video_action_404", http.MethodPost, "/api/video/action", `{"id":"not-found","action":"play"}`, false, http.StatusNotFound, "stream not active", "", "", "", "", "", "", "", ""},
		{"video_action_422", http.MethodPost, "/api/video/action", `{"id":"not-controllable","action":"play"}`, false, http.StatusUnprocessableEntity, "stream is not controllable", "", "", "", "", "", "", "", ""},
		{"video_action_502", http.MethodPost, "/api/video/action", `{"id":"unreachable","action":"play"}`, false, http.StatusBadGateway, "stream controller unreachable", "", "", "", "", "", "", "", ""},
		{"video_action_400", http.MethodPost, "/api/video/action", `not-json`, false, http.StatusBadRequest, "invalid action payload", "", "", "", "", "", "", "", ""},
		{"video_state_post_200", http.MethodPost, "/api/video/state", `{"id":"chromecast","player_state":"playing"}`, false, http.StatusOK, `"player_state":"playing"`, "", "", "", "", "", "", "", ""},
		{"video_state_post_404", http.MethodPost, "/api/video/state", `{"id":"not-found","player_state":"paused"}`, false, http.StatusNotFound, "stream not active", "", "", "", "", "", "", "", ""},
		{"video_state_post_400", http.MethodPost, "/api/video/state", `not-json`, false, http.StatusBadRequest, "invalid player state payload", "", "", "", "", "", "", "", ""},
		{"voice_state_200", http.MethodPost, "/api/voice/state", `{"state":"thinking","transcript":"What time is it?"}`, false, http.StatusOK, `"status":"ok"`, "", "", "", "", "", "", "", "thinking"},
		{"voice_state_400", http.MethodPost, "/api/voice/state", `not-json`, false, http.StatusBadRequest, "invalid voice state payload", "", "", "", "", "", "", "", ""},
		{"voice_interact_200", http.MethodPost, "/api/voice/interact", "--boundary\r\nContent-Disposition: form-data; name=\"audio\"; filename=\"test.wav\"\r\nContent-Type: audio/wav\r\n\r\nRIFF....\r\n--boundary--\r\n", false, http.StatusOK, "transcribing", "", "", "", "", "", "", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mock := &mockServer{audioMuted: tc.initMuted}
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			if tc.body != "" {
				if strings.HasPrefix(tc.body, "--boundary") {
					req.Header.Set("Content-Type", "multipart/form-data; boundary=boundary")
				} else {
					req.Header.Set("Content-Type", "application/json")
				}
			}
			rec := httptest.NewRecorder()
			Handler(mock).ServeHTTP(rec, req)
			if rec.Code != tc.wantCode || (tc.wantSub != "" && !strings.Contains(rec.Body.String(), tc.wantSub)) {
				t.Fatalf("got (%d, %q), want (%d, sub %q)", rec.Code, rec.Body.String(), tc.wantCode, tc.wantSub)
			}
			for _, check := range [][2]string{
				{tc.wantWidgetID, mock.lastWidgetID}, {tc.wantType, mock.lastType}, {tc.wantPath, mock.lastPath},
				{tc.wantTriggerID, mock.lastTriggerID}, {tc.wantDismissID, mock.lastDismissID},
				{tc.wantActionID, mock.lastActionID}, {tc.wantAction, mock.lastAction}, {tc.wantVoiceState, mock.lastVoiceState},
			} {
				if check[0] != "" && check[1] != check[0] {
					t.Fatalf("got %q, want %q", check[1], check[0])
				}
			}
		})
	}
}

func TestHandler_OptionsAndMuxConstructors(t *testing.T) {
	t.Parallel()
	mock := &mockServer{}
	mwCalls := 0
	hOpts := HandlerWithOptions(mock, StdHTTPServerOptions{
		BaseURL: "/v1", BaseRouter: http.NewServeMux(),
		Middlewares: []MiddlewareFunc{func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mwCalls++
				next.ServeHTTP(w, r)
			})
		}},
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "custom: "+err.Error(), http.StatusBadRequest)
		},
	})
	endpoints := []struct{ method, path, body string }{
		{http.MethodGet, "/v1/healthz", ""}, {http.MethodGet, "/v1/health", ""},
		{http.MethodGet, "/v1/api/audio", ""}, {http.MethodGet, "/v1/api/widgets/widget-1/render", ""},
		{http.MethodGet, "/v1/widget-types/clock/assets/icon.svg", ""},
		{http.MethodPost, "/v1/api/audio/volume", `{"volume": 50}`}, {http.MethodPost, "/v1/api/audio/mute", `{}`},
		{http.MethodPost, "/v1/api/screen/advance", `{"direction": "next"}`},
		{http.MethodPost, "/v1/api/screen/pause", `{"paused": true}`},
		{http.MethodPost, "/v1/api/screen/select", `{"screen_index": 0}`},
	}
	for _, ep := range endpoints {
		rec := httptest.NewRecorder()
		hOpts.ServeHTTP(rec, httptest.NewRequest(ep.method, ep.path, strings.NewReader(ep.body)))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s expected 200, got %d", ep.method, ep.path, rec.Code)
		}
	}
	if mwCalls != len(endpoints) {
		t.Fatalf("mwCalls = %d, want %d", mwCalls, len(endpoints))
	}
	for _, tc := range []struct {
		h    http.Handler
		path string
	}{
		{HandlerFromMux(mock, http.NewServeMux()), "/health"},
		{HandlerFromMuxWithBaseURL(mock, http.NewServeMux(), "/prefix"), "/prefix/healthz"},
	} {
		rec := httptest.NewRecorder()
		tc.h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s expected 200, got %d", tc.path, rec.Code)
		}
	}
}

func TestServerInterfaceWrapper_ParameterErrors(t *testing.T) {
	t.Parallel()
	if h := HandlerWithOptions(&mockServer{}, StdHTTPServerOptions{}); h == nil {
		t.Fatal("expected non-nil handler")
	}
	tests := []struct {
		name, target, method, url string
		pathParams               [][2]string
		withMW                   bool
		wantCode                 int
	}{
		{"render_missing_widget_id", "get_widget_render", http.MethodGet, "/api/widgets//render", nil, false, http.StatusBadRequest},
		{"push_missing_widget_id", "post_widget_push", http.MethodPost, "/api/widgets//push", nil, false, http.StatusBadRequest},
		{"push_with_mw", "post_widget_push", http.MethodPost, "/api/widgets/w1/push", [][2]string{{"widget_id", "w1"}}, true, http.StatusOK},
		{"voice_interact_mw", "post_voice_interact", http.MethodPost, "/api/voice/interact", nil, true, http.StatusOK},
		{"voice_state_mw", "post_voice_state", http.MethodPost, "/api/voice/state", nil, true, http.StatusOK},
		{"video_action_mw", "post_video_action", http.MethodPost, "/api/video/action", nil, true, http.StatusOK},
		{"video_dismiss_mw", "post_video_dismiss", http.MethodPost, "/api/video/dismiss", nil, true, http.StatusOK},
		{"video_state_get_mw", "get_video_state", http.MethodGet, "/api/video/state", nil, true, http.StatusOK},
		{"video_state_post_mw", "post_video_state", http.MethodPost, "/api/video/state", nil, true, http.StatusOK},
		{"video_trigger_mw", "post_video_trigger", http.MethodPost, "/api/video/trigger", nil, true, http.StatusOK},
		{"asset_missing_type", "get_widget_asset", http.MethodGet, "/widget-types//assets/icon.svg", nil, false, http.StatusBadRequest},
		{"asset_missing_path", "get_widget_asset", http.MethodGet, "/widget-types/clock/assets/", [][2]string{{"type", "clock"}}, false, http.StatusBadRequest},
		{"complete_item_ok", "complete_list_item", http.MethodPost, "/api/lists/g/items/i1/complete", [][2]string{{"list_id", "g"}, {"item_id", "i1"}}, false, http.StatusOK},
		{"complete_item_404", "complete_list_item", http.MethodPost, "/api/lists/not-found/items/i1/complete", [][2]string{{"list_id", "not-found"}, {"item_id", "i1"}}, false, http.StatusNotFound},
		{"complete_item_missing_list", "complete_list_item", http.MethodPost, "/api/lists//items/i1/complete", [][2]string{{"item_id", "i1"}}, false, http.StatusBadRequest},
		{"complete_item_missing_item", "complete_list_item", http.MethodPost, "/api/lists/g/items//complete", [][2]string{{"list_id", "g"}}, false, http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mwCalled, wrapper := false, ServerInterfaceWrapper{
				Handler: &mockServer{},
				ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
					http.Error(w, err.Error(), http.StatusBadRequest)
				},
			}
			if tc.withMW {
				wrapper.HandlerMiddlewares = []MiddlewareFunc{func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						mwCalled = true
						next.ServeHTTP(w, r)
					})
				}}
			}
			var body string
			if strings.Contains(tc.target, "video") || strings.Contains(tc.target, "voice") || strings.Contains(tc.target, "push") {
				body = `{"state":"idle","status":"ok","id":"chromecast","stream_url":"http://127.0.0.1/cast","action":"play","player_state":"playing"}`
			}
			req := httptest.NewRequest(tc.method, tc.url, strings.NewReader(body))
			for _, p := range tc.pathParams {
				req.SetPathValue(p[0], p[1])
			}
			rec := httptest.NewRecorder()
			dispatchWrapper(&wrapper, tc.target, rec, req)
			if rec.Code != tc.wantCode || (tc.withMW && !mwCalled) {
				t.Fatalf("got code %d (mw %v), want code %d (mw %v)", rec.Code, mwCalled, tc.wantCode, tc.withMW)
			}
		})
	}
}

func dispatchWrapper(w *ServerInterfaceWrapper, target string, rec http.ResponseWriter, req *http.Request) {
	switch target {
	case "get_widget_render": w.GetWidgetRender(rec, req)
	case "post_widget_push": w.PostWidgetPush(rec, req)
	case "get_widget_asset": w.GetWidgetAsset(rec, req)
	case "complete_list_item": w.CompleteListItem(rec, req)
	case "post_voice_interact": w.PostVoiceInteract(rec, req)
	case "post_voice_state": w.PostVoiceState(rec, req)
	case "post_video_action": w.PostVideoAction(rec, req)
	case "post_video_dismiss": w.PostVideoDismiss(rec, req)
	case "get_video_state": w.GetVideoState(rec, req)
	case "post_video_state": w.PostVideoState(rec, req)
	case "post_video_trigger": w.PostVideoTrigger(rec, req)
	}
}

func TestErrorTypes(t *testing.T) {
	t.Parallel()
	rootErr := errors.New("underlying cause")
	tests := []struct {
		name       string
		err        error
		wantSub    string
		wantUnwrap bool
	}{
		{"UnescapedCookieParamError", &UnescapedCookieParamError{ParamName: "session", Err: rootErr}, "session", true},
		{"UnmarshalingParamError", &UnmarshalingParamError{ParamName: "filter", Err: rootErr}, "filter", true},
		{"RequiredParamError", &RequiredParamError{ParamName: "id"}, "id", false},
		{"RequiredHeaderError", &RequiredHeaderError{ParamName: "Authorization", Err: rootErr}, "Authorization", true},
		{"InvalidParamFormatError", &InvalidParamFormatError{ParamName: "count", Err: rootErr}, "count", true},
		{"TooManyValuesForParamError", &TooManyValuesForParamError{ParamName: "tag", Count: 3}, "tag", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if !strings.Contains(tc.err.Error(), tc.wantSub) {
				t.Fatalf("expected error string to contain %q, got %s", tc.wantSub, tc.err.Error())
			}
			if tc.wantUnwrap && !errors.Is(tc.err, rootErr) {
				t.Fatalf("expected Unwrap to return rootErr")
			}
		})
	}
}
