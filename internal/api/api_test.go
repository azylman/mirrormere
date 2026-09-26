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
	lastWidgetID   string
	lastType       string
	lastPath       string
	healthCalls    int
	healthzCalls   int
	audioVolume    int
	audioVolumeSet bool
	audioMuted     bool
	videoMode      string
	videoPrimary   *VideoStream
	videoPip       *VideoStream
	lastActionID   string
	lastAction     string
	lastDismissID  string
	lastTriggerID  string
	lastVoiceState string
}

func (m *mockServer) PostWidgetPush(w http.ResponseWriter, r *http.Request, widgetID string) {
	m.lastWidgetID = widgetID
	if widgetID == "not-found" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(ErrorResponse{
			Status: "error",
			Error:  fmt.Sprintf("widget '%s' not found in active configuration", widgetID),
		})
		return
	}
	if widgetID == "conflict-tasks" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(ErrorResponse{
			Status: "error",
			Error:  "cannot push state to list-backed widget",
		})
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(ErrorResponse{
			Status: "error",
			Error:  "invalid json",
		})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(WidgetPushResponse{
		Status:    "ok",
		WidgetId:  widgetID,
		UpdatedAt: time.Date(2026, 9, 26, 6, 0, 0, 0, time.UTC),
	})
}

func (m *mockServer) GetHealth(w http.ResponseWriter, r *http.Request) {
	m.healthCalls++
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(HealthResponse{Status: "ok"})
}

func (m *mockServer) GetHealthz(w http.ResponseWriter, r *http.Request) {
	m.healthzCalls++
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(HealthResponse{Status: "ok"})
}

func (m *mockServer) GetWidgetRender(w http.ResponseWriter, r *http.Request, widgetID string) {
	m.lastWidgetID = widgetID
	if widgetID == "not-found" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(ErrorResponse{
			Status: "error",
			Error:  fmt.Sprintf("widget '%s' not found in active configuration", widgetID),
		})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(fmt.Sprintf("<div id=\"%s\">rendered</div>", widgetID)))
}

func (m *mockServer) GetWidgetAsset(w http.ResponseWriter, r *http.Request, pType string, path string) {
	m.lastType = pType
	m.lastPath = path
	if pType == "missing" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(ErrorResponse{
			Status: "error",
			Error:  "asset not found",
		})
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("<svg></svg>"))
}

func (m *mockServer) PostScreenAdvance(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(ScreenNavigationResponse{
		Status:        "ok",
		CurrentScreen: 1,
		TotalScreens:  2,
	})
}

func (m *mockServer) PostScreenPause(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(ScreenPauseResponse{
		Status:        "ok",
		Paused:        true,
		CurrentScreen: 0,
	})
}

func (m *mockServer) PostScreenSelect(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(ScreenNavigationResponse{
		Status:        "ok",
		CurrentScreen: 1,
		TotalScreens:  2,
	})
}

func (m *mockServer) GetListItems(w http.ResponseWriter, r *http.Request, listId string, params GetListItemsParams) {
	if listId == "not-found" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(ErrorResponse{
			Status: "error",
			Error:  fmt.Sprintf("list '%s' not found", listId),
		})
		return
	}
	section := "Dairy"
	assignee := "Alex"
	dueDate := "2026-09-26"
	items := []ListItem{
		{
			Id:        "i1",
			ListId:    listId,
			Title:     "Oat milk",
			Done:      false,
			Section:   &section,
			Position:  0,
			Assignee:  &assignee,
			DueDate:   &dueDate,
			CreatedAt: time.Date(2026, 9, 24, 22, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2026, 9, 24, 22, 30, 0, 0, time.UTC),
		},
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(items)
}

func (m *mockServer) GetAudio(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	vol := m.audioVolume
	if vol == 0 && !m.audioVolumeSet {
		vol = 75
	}
	_ = json.NewEncoder(w).Encode(AudioStateResponse{
		Status: "ok",
		Volume: vol,
		Muted:  m.audioMuted,
	})
}

func (m *mockServer) PostAudioVolume(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(ErrorResponse{
			Status: "error",
			Error:  "invalid json",
		})
		return
	}
	rawVol, exists := body["volume"]
	if !exists {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(ErrorResponse{
			Status: "error",
			Error:  "invalid volume: must be an integer between 0 and 100",
		})
		return
	}
	floatVol, ok := rawVol.(float64)
	if !ok || floatVol != float64(int(floatVol)) || floatVol < 0 || floatVol > 100 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(ErrorResponse{
			Status: "error",
			Error:  "invalid volume: must be an integer between 0 and 100",
		})
		return
	}
	m.audioVolume = int(floatVol)
	m.audioVolumeSet = true
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(AudioStateResponse{
		Status: "ok",
		Volume: m.audioVolume,
		Muted:  m.audioMuted,
	})
}

func (m *mockServer) PostAudioMute(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	if rawMuted, exists := body["muted"]; exists {
		b, ok := rawMuted.(bool)
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(ErrorResponse{
				Status: "error",
				Error:  "invalid payload: muted must be a boolean",
			})
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
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(AudioStateResponse{
		Status: "ok",
		Volume: vol,
		Muted:  m.audioMuted,
	})
}

func (m *mockServer) GetVideoState(w http.ResponseWriter, r *http.Request) {
	mode := m.videoMode
	if mode == "" {
		mode = "widgets"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(VideoStateResponse{
		Status:  "ok",
		Mode:    VideoStateResponseMode(mode),
		Primary: m.videoPrimary,
		Pip:     m.videoPip,
	})
}

func (m *mockServer) PostVideoTrigger(w http.ResponseWriter, r *http.Request) {
	var req VideoTriggerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(ErrorResponse{
			Status: "error",
			Error:  "invalid trigger payload",
		})
		return
	}
	m.lastTriggerID = req.Id
	vType := VideoStreamTypeWebrtc
	if req.Type != nil {
		vType = VideoStreamType(*req.Type)
	}
	m.videoPrimary = &VideoStream{
		Id:        req.Id,
		StreamUrl: req.StreamUrl,
		Type:      vType,
	}
	m.videoMode = "video"
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(VideoStateResponse{
		Status:  "ok",
		Mode:    VideoStateResponseMode("video"),
		Primary: m.videoPrimary,
	})
}

func (m *mockServer) PostVideoDismiss(w http.ResponseWriter, r *http.Request) {
	var req VideoDismissRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(ErrorResponse{
			Status: "error",
			Error:  "invalid dismiss payload",
		})
		return
	}
	if req.Id == "not-found" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(ErrorResponse{
			Status: "error",
			Error:  "stream not active",
		})
		return
	}
	m.lastDismissID = req.Id
	m.videoPrimary = nil
	m.videoPip = nil
	m.videoMode = "widgets"
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(VideoStateResponse{
		Status: "ok",
		Mode:   VideoStateResponseMode("widgets"),
	})
}

func (m *mockServer) PostVideoAction(w http.ResponseWriter, r *http.Request) {
	var req VideoActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(ErrorResponse{
			Status: "error",
			Error:  "invalid action payload",
		})
		return
	}
	if req.Id == "not-found" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(ErrorResponse{
			Status: "error",
			Error:  "stream not active",
		})
		return
	}
	if req.Id == "not-controllable" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(ErrorResponse{
			Status: "error",
			Error:  "stream is not controllable",
		})
		return
	}
	if req.Id == "unreachable" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(ErrorResponse{
			Status: "error",
			Error:  "stream controller unreachable",
		})
		return
	}
	m.lastActionID = req.Id
	m.lastAction = string(req.Action)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(ActionResponse{Status: "ok"})
}

func (m *mockServer) PostVideoState(w http.ResponseWriter, r *http.Request) {
	var req VideoPlayerStateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(ErrorResponse{
			Status: "error",
			Error:  "invalid player state payload",
		})
		return
	}
	if req.Id == "not-found" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(ErrorResponse{
			Status: "error",
			Error:  "stream not active",
		})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(VideoPlayerStateResponse{
		Status:      "ok",
		Id:          req.Id,
		PlayerState: VideoPlayerStateResponsePlayerState(req.PlayerState),
	})
}

func (m *mockServer) PostVoiceState(w http.ResponseWriter, r *http.Request) {
	var req VoiceStateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(ErrorResponse{
			Status: "error",
			Error:  "invalid voice state payload",
		})
		return
	}
	m.lastVoiceState = string(req.State)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(VoiceStateResponse{
		Status: "ok",
	})
}

func TestHandler_Endpoints(t *testing.T) {
	t.Parallel()

	t.Run("GET /api/audio", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		handler := Handler(mock)

		req := httptest.NewRequest(http.MethodGet, "/api/audio", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		var resp AudioStateResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if resp.Volume != 75 || resp.Muted != false || resp.Status != "ok" {
			t.Fatalf("unexpected audio state: %+v", resp)
		}
	})

	t.Run("POST /api/audio/volume", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		handler := Handler(mock)

		// 200 OK
		req := httptest.NewRequest(http.MethodPost, "/api/audio/volume", strings.NewReader(`{"volume":80}`))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		var resp AudioStateResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if resp.Volume != 80 {
			t.Fatalf("expected volume 80, got %d", resp.Volume)
		}

		// 400 Bad Request: missing volume
		reqBad := httptest.NewRequest(http.MethodPost, "/api/audio/volume", strings.NewReader(`{}`))
		recBad := httptest.NewRecorder()
		handler.ServeHTTP(recBad, reqBad)
		if recBad.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for empty volume, got %d", recBad.Code)
		}

		// 400 Bad Request: out of range
		reqOOB := httptest.NewRequest(http.MethodPost, "/api/audio/volume", strings.NewReader(`{"volume":150}`))
		recOOB := httptest.NewRecorder()
		handler.ServeHTTP(recOOB, reqOOB)
		if recOOB.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for out of range volume, got %d", recOOB.Code)
		}

		// 400 Bad Request: invalid json
		reqInvalid := httptest.NewRequest(http.MethodPost, "/api/audio/volume", strings.NewReader(`{invalid`))
		recInvalid := httptest.NewRecorder()
		handler.ServeHTTP(recInvalid, reqInvalid)
		if recInvalid.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for invalid json, got %d", recInvalid.Code)
		}
	})

	t.Run("POST /api/audio/mute", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		handler := Handler(mock)

		// Toggle mute from false to true with empty body
		reqToggle := httptest.NewRequest(http.MethodPost, "/api/audio/mute", strings.NewReader(`{}`))
		recToggle := httptest.NewRecorder()
		handler.ServeHTTP(recToggle, reqToggle)

		if recToggle.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", recToggle.Code)
		}
		var respToggle AudioStateResponse
		if err := json.Unmarshal(recToggle.Body.Bytes(), &respToggle); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if !respToggle.Muted {
			t.Fatalf("expected muted true, got false")
		}

		// Set explicit mute state false
		reqExplicit := httptest.NewRequest(http.MethodPost, "/api/audio/mute", strings.NewReader(`{"muted":false}`))
		recExplicit := httptest.NewRecorder()
		handler.ServeHTTP(recExplicit, reqExplicit)
		if recExplicit.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", recExplicit.Code)
		}
		var respExplicit AudioStateResponse
		if err := json.Unmarshal(recExplicit.Body.Bytes(), &respExplicit); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if respExplicit.Muted {
			t.Fatalf("expected muted false, got true")
		}

		// 400 Bad Request: invalid muted type
		reqBad := httptest.NewRequest(http.MethodPost, "/api/audio/mute", strings.NewReader(`{"muted":"notabool"}`))
		recBad := httptest.NewRecorder()
		handler.ServeHTTP(recBad, reqBad)
		if recBad.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for bad muted type, got %d", recBad.Code)
		}
	})

	t.Run("GET /api/lists/{list_id}/items", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		handler := Handler(mock)

		// 200 OK
		req := httptest.NewRequest(http.MethodGet, "/api/lists/groceries/items?include_done=false", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		var items []ListItem
		if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
			t.Fatalf("failed to decode items: %v", err)
		}
		if len(items) != 1 || items[0].Id != "i1" {
			t.Fatalf("expected item i1, got %+v", items)
		}

		// 404 Not Found
		req404 := httptest.NewRequest(http.MethodGet, "/api/lists/not-found/items", nil)
		rec404 := httptest.NewRecorder()
		handler.ServeHTTP(rec404, req404)

		if rec404.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec404.Code)
		}
	})

	t.Run("GET /healthz", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		handler := Handler(mock)

		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		var resp HealthResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if resp.Status != "ok" {
			t.Fatalf("expected status 'ok', got '%s'", resp.Status)
		}
		if mock.healthzCalls != 1 {
			t.Fatalf("expected 1 healthz call, got %d", mock.healthzCalls)
		}
	})

	t.Run("GET /health", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		handler := Handler(mock)

		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		var resp HealthResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if resp.Status != "ok" {
			t.Fatalf("expected status 'ok', got '%s'", resp.Status)
		}
		if mock.healthCalls != 1 {
			t.Fatalf("expected 1 health call, got %d", mock.healthCalls)
		}
	})

	t.Run("GET /api/widgets/{widget_id}/render success", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		handler := Handler(mock)

		req := httptest.NewRequest(http.MethodGet, "/api/widgets/clock-primary/render", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if mock.lastWidgetID != "clock-primary" {
			t.Fatalf("expected widget_id 'clock-primary', got '%s'", mock.lastWidgetID)
		}
		if !strings.Contains(rec.Body.String(), "clock-primary") {
			t.Fatalf("expected body to contain 'clock-primary', got %s", rec.Body.String())
		}
	})

	t.Run("GET /api/widgets/{widget_id}/render not found", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		handler := Handler(mock)

		req := httptest.NewRequest(http.MethodGet, "/api/widgets/not-found/render", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}
		var errResp ErrorResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
			t.Fatalf("failed to decode error response: %v", err)
		}
		if errResp.Status != "error" {
			t.Fatalf("expected status 'error', got '%s'", errResp.Status)
		}
	})

	t.Run("GET /widget-types/{type}/assets/{path} success", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		handler := Handler(mock)

		req := httptest.NewRequest(http.MethodGet, "/widget-types/weather/assets/sun.svg", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if mock.lastType != "weather" {
			t.Fatalf("expected type 'weather', got '%s'", mock.lastType)
		}
		if mock.lastPath != "sun.svg" {
			t.Fatalf("expected path 'sun.svg', got '%s'", mock.lastPath)
		}
	})

	t.Run("GET /widget-types/{type}/assets/{path} not found", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		handler := Handler(mock)

		req := httptest.NewRequest(http.MethodGet, "/widget-types/missing/assets/sun.svg", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}
	})

	t.Run("POST /api/screen/advance", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		handler := Handler(mock)

		req := httptest.NewRequest(http.MethodPost, "/api/screen/advance", strings.NewReader(`{"direction":"next"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("POST /api/screen/pause", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		handler := Handler(mock)

		req := httptest.NewRequest(http.MethodPost, "/api/screen/pause", strings.NewReader(`{"paused":true,"duration_seconds":120}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("POST /api/screen/select", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		handler := Handler(mock)

		req := httptest.NewRequest(http.MethodPost, "/api/screen/select", strings.NewReader(`{"screen_index":1}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("POST /api/widgets/{widget_id}/push success", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		handler := Handler(mock)

		req := httptest.NewRequest(http.MethodPost, "/api/widgets/sensor-card/push", strings.NewReader(`{"co2_ppm":640}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if mock.lastWidgetID != "sensor-card" {
			t.Fatalf("expected widget_id 'sensor-card', got '%s'", mock.lastWidgetID)
		}
	})

	t.Run("POST /api/widgets/{widget_id}/push not found", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		handler := Handler(mock)

		req := httptest.NewRequest(http.MethodPost, "/api/widgets/not-found/push", strings.NewReader(`{"co2_ppm":640}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}
	})

	t.Run("POST /api/widgets/{widget_id}/push conflict task widget", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		handler := Handler(mock)

		req := httptest.NewRequest(http.MethodPost, "/api/widgets/conflict-tasks/push", strings.NewReader(`{"items":[]}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d", rec.Code)
		}
	})

	t.Run("POST /api/widgets/{widget_id}/push bad json", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		handler := Handler(mock)

		req := httptest.NewRequest(http.MethodPost, "/api/widgets/sensor-card/push", strings.NewReader(`not-json`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("GET /api/video/state", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		handler := Handler(mock)

		req := httptest.NewRequest(http.MethodGet, "/api/video/state", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		var resp VideoStateResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if resp.Mode != Widgets {
			t.Fatalf("expected widgets, got %s", resp.Mode)
		}
	})

	t.Run("POST /api/video/trigger", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		handler := Handler(mock)

		req := httptest.NewRequest(http.MethodPost, "/api/video/trigger", strings.NewReader(`{"id":"chromecast","stream_url":"http://127.0.0.1:1984/cast","type":"webrtc"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		var resp VideoStateResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if resp.Mode != Video {
			t.Fatalf("expected video mode, got %s", resp.Mode)
		}
		if mock.lastTriggerID != "chromecast" {
			t.Fatalf("expected trigger id 'chromecast', got '%s'", mock.lastTriggerID)
		}

		// Bad JSON
		reqBad := httptest.NewRequest(http.MethodPost, "/api/video/trigger", strings.NewReader(`not-json`))
		reqBad.Header.Set("Content-Type", "application/json")
		recBad := httptest.NewRecorder()
		handler.ServeHTTP(recBad, reqBad)
		if recBad.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for bad json, got %d", recBad.Code)
		}
	})

	t.Run("POST /api/video/dismiss", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		handler := Handler(mock)

		req := httptest.NewRequest(http.MethodPost, "/api/video/dismiss", strings.NewReader(`{"id":"chromecast"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if mock.lastDismissID != "chromecast" {
			t.Fatalf("expected dismiss id 'chromecast', got '%s'", mock.lastDismissID)
		}

		// 404 Not Found
		req404 := httptest.NewRequest(http.MethodPost, "/api/video/dismiss", strings.NewReader(`{"id":"not-found"}`))
		req404.Header.Set("Content-Type", "application/json")
		rec404 := httptest.NewRecorder()
		handler.ServeHTTP(rec404, req404)
		if rec404.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec404.Code)
		}

		// Bad JSON
		reqBad := httptest.NewRequest(http.MethodPost, "/api/video/dismiss", strings.NewReader(`not-json`))
		reqBad.Header.Set("Content-Type", "application/json")
		recBad := httptest.NewRecorder()
		handler.ServeHTTP(recBad, reqBad)
		if recBad.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", recBad.Code)
		}
	})

	t.Run("POST /api/video/action", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		handler := Handler(mock)

		// 200 OK
		req := httptest.NewRequest(http.MethodPost, "/api/video/action", strings.NewReader(`{"id":"chromecast","action":"toggle_playback"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if mock.lastActionID != "chromecast" || mock.lastAction != "toggle_playback" {
			t.Fatalf("expected action chromecast toggle_playback, got %s %s", mock.lastActionID, mock.lastAction)
		}

		// 404 Not Found
		req404 := httptest.NewRequest(http.MethodPost, "/api/video/action", strings.NewReader(`{"id":"not-found","action":"play"}`))
		req404.Header.Set("Content-Type", "application/json")
		rec404 := httptest.NewRecorder()
		handler.ServeHTTP(rec404, req404)
		if rec404.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec404.Code)
		}

		// 422 Unprocessable Entity
		req422 := httptest.NewRequest(http.MethodPost, "/api/video/action", strings.NewReader(`{"id":"not-controllable","action":"play"}`))
		req422.Header.Set("Content-Type", "application/json")
		rec422 := httptest.NewRecorder()
		handler.ServeHTTP(rec422, req422)
		if rec422.Code != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422, got %d", rec422.Code)
		}

		// 502 Bad Gateway
		req502 := httptest.NewRequest(http.MethodPost, "/api/video/action", strings.NewReader(`{"id":"unreachable","action":"play"}`))
		req502.Header.Set("Content-Type", "application/json")
		rec502 := httptest.NewRecorder()
		handler.ServeHTTP(rec502, req502)
		if rec502.Code != http.StatusBadGateway {
			t.Fatalf("expected 502, got %d", rec502.Code)
		}

		// Bad JSON
		reqBad := httptest.NewRequest(http.MethodPost, "/api/video/action", strings.NewReader(`not-json`))
		reqBad.Header.Set("Content-Type", "application/json")
		recBad := httptest.NewRecorder()
		handler.ServeHTTP(recBad, reqBad)
		if recBad.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", recBad.Code)
		}
	})

	t.Run("POST /api/video/state", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		handler := Handler(mock)

		// 200 OK
		req := httptest.NewRequest(http.MethodPost, "/api/video/state", strings.NewReader(`{"id":"chromecast","player_state":"playing"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		var resp VideoPlayerStateResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if resp.Id != "chromecast" || resp.PlayerState != VideoPlayerStateResponsePlayerStatePlaying {
			t.Fatalf("expected chromecast playing, got %s %s", resp.Id, resp.PlayerState)
		}

		// 404 Not Found
		req404 := httptest.NewRequest(http.MethodPost, "/api/video/state", strings.NewReader(`{"id":"not-found","player_state":"paused"}`))
		req404.Header.Set("Content-Type", "application/json")
		rec404 := httptest.NewRecorder()
		handler.ServeHTTP(rec404, req404)
		if rec404.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec404.Code)
		}

		// Bad JSON
		reqBad := httptest.NewRequest(http.MethodPost, "/api/video/state", strings.NewReader(`not-json`))
		reqBad.Header.Set("Content-Type", "application/json")
		recBad := httptest.NewRecorder()
		handler.ServeHTTP(recBad, reqBad)
		if recBad.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", recBad.Code)
		}
	})

	t.Run("POST /api/voice/state", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		handler := Handler(mock)

		// 200 OK
		req := httptest.NewRequest(http.MethodPost, "/api/voice/state", strings.NewReader(`{"state":"thinking","transcript":"What time is it?"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		var resp VoiceStateResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if resp.Status != "ok" {
			t.Fatalf("expected status ok, got %q", resp.Status)
		}
		if mock.lastVoiceState != "thinking" {
			t.Fatalf("expected lastVoiceState 'thinking', got %q", mock.lastVoiceState)
		}

		// Bad JSON
		reqBad := httptest.NewRequest(http.MethodPost, "/api/voice/state", strings.NewReader(`not-json`))
		reqBad.Header.Set("Content-Type", "application/json")
		recBad := httptest.NewRecorder()
		handler.ServeHTTP(recBad, reqBad)
		if recBad.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", recBad.Code)
		}
	})
}

func TestHandlerWithOptions_CustomMuxAndBaseURL(t *testing.T) {
	t.Parallel()

	mock := &mockServer{}
	customMux := http.NewServeMux()

	mwCalls := make(map[string]int)
	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mwCalls[r.URL.Path]++
			next.ServeHTTP(w, r)
		})
	}

	handler := HandlerWithOptions(mock, StdHTTPServerOptions{
		BaseURL:     "/v1",
		BaseRouter:  customMux,
		Middlewares: []MiddlewareFunc{mw},
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "custom: "+err.Error(), http.StatusBadRequest)
		},
	})

	endpoints := []string{
		"/v1/healthz",
		"/v1/health",
		"/v1/api/audio",
		"/v1/api/widgets/widget-1/render",
		"/v1/widget-types/clock/assets/icon.svg",
	}

	for _, ep := range endpoints {
		req := httptest.NewRequest(http.MethodGet, ep, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("endpoint %s expected 200, got %d", ep, rec.Code)
		}
		if mwCalls[ep] != 1 {
			t.Fatalf("expected middleware call for %s, got %d", ep, mwCalls[ep])
		}
	}

	postEndpoints := []struct {
		path string
		body string
	}{
		{"/v1/api/audio/volume", `{"volume": 50}`},
		{"/v1/api/audio/mute", `{}`},
		{"/v1/api/screen/advance", `{"direction": "next"}`},
		{"/v1/api/screen/pause", `{"paused": true}`},
		{"/v1/api/screen/select", `{"screen_index": 0}`},
	}

	for _, ep := range postEndpoints {
		req := httptest.NewRequest(http.MethodPost, ep.path, strings.NewReader(ep.body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("endpoint %s expected 200, got %d", ep.path, rec.Code)
		}
		if mwCalls[ep.path] != 1 {
			t.Fatalf("expected middleware call for %s, got %d", ep.path, mwCalls[ep.path])
		}
	}
}

func TestHandlerFromMux(t *testing.T) {
	t.Parallel()

	mock := &mockServer{}
	mux := http.NewServeMux()
	handler := HandlerFromMux(mock, mux)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestHandlerFromMuxWithBaseURL(t *testing.T) {
	t.Parallel()

	mock := &mockServer{}
	mux := http.NewServeMux()
	handler := HandlerFromMuxWithBaseURL(mock, mux, "/prefix")

	req := httptest.NewRequest(http.MethodGet, "/prefix/healthz", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestServerInterfaceWrapper_ParameterErrors(t *testing.T) {
	t.Parallel()

	t.Run("Default error handler triggers on missing widget_id", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		wrapper := ServerInterfaceWrapper{
			Handler: mock,
			ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
				http.Error(w, err.Error(), http.StatusBadRequest)
			},
		}

		req := httptest.NewRequest(http.MethodGet, "/api/widgets//render", nil)
		rec := httptest.NewRecorder()
		wrapper.GetWidgetRender(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("Default error handler triggers on missing widget_id for PostWidgetPush", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		wrapper := ServerInterfaceWrapper{
			Handler: mock,
			ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
				http.Error(w, err.Error(), http.StatusBadRequest)
			},
		}

		req := httptest.NewRequest(http.MethodPost, "/api/widgets//push", nil)
		rec := httptest.NewRecorder()
		wrapper.PostWidgetPush(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("Middlewares executed for PostWidgetPush", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		var mwCalled bool
		wrapper := ServerInterfaceWrapper{
			Handler: mock,
			HandlerMiddlewares: []MiddlewareFunc{
				func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						mwCalled = true
						next.ServeHTTP(w, r)
					})
				},
			},
		}

		req := httptest.NewRequest(http.MethodPost, "/api/widgets/widget-1/push", strings.NewReader(`{"status":"ok"}`))
		req.SetPathValue("widget_id", "widget-1")
		rec := httptest.NewRecorder()
		wrapper.PostWidgetPush(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if !mwCalled {
			t.Fatal("expected middleware to be called")
		}
	})

	t.Run("Default error handler triggers on missing asset type", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		wrapper := ServerInterfaceWrapper{
			Handler: mock,
			ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
				http.Error(w, err.Error(), http.StatusBadRequest)
			},
		}

		req := httptest.NewRequest(http.MethodGet, "/widget-types//assets/icon.svg", nil)
		rec := httptest.NewRecorder()
		wrapper.GetWidgetAsset(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("Default error handler triggers on missing asset path", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		wrapper := ServerInterfaceWrapper{
			Handler: mock,
			ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
				http.Error(w, err.Error(), http.StatusBadRequest)
			},
		}

		req := httptest.NewRequest(http.MethodGet, "/widget-types/clock/assets/", nil)
		req.SetPathValue("type", "clock")
		rec := httptest.NewRecorder()
		wrapper.GetWidgetAsset(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("Default error handler triggers on invalid include_done query parameter", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		wrapper := ServerInterfaceWrapper{
			Handler: mock,
			ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
				http.Error(w, err.Error(), http.StatusBadRequest)
			},
		}

		req := httptest.NewRequest(http.MethodGet, "/api/lists/groceries/items?include_done=not-a-bool", nil)
		req.SetPathValue("list_id", "groceries")
		rec := httptest.NewRecorder()
		wrapper.GetListItems(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("Default error handler triggers on missing list_id", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		wrapper := ServerInterfaceWrapper{
			Handler: mock,
			ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
				http.Error(w, err.Error(), http.StatusBadRequest)
			},
		}

		req := httptest.NewRequest(http.MethodGet, "/api/lists//items", nil)
		rec := httptest.NewRecorder()
		wrapper.GetListItems(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("Default error handler func in HandlerWithOptions", func(t *testing.T) {
		t.Parallel()
		mock := &mockServer{}
		h := HandlerWithOptions(mock, StdHTTPServerOptions{})
		if h == nil {
			t.Fatalf("expected non-nil handler")
		}
	})
}

func TestErrorTypes(t *testing.T) {
	t.Parallel()

	rootErr := errors.New("underlying cause")

	t.Run("UnescapedCookieParamError", func(t *testing.T) {
		t.Parallel()
		err := &UnescapedCookieParamError{ParamName: "session", Err: rootErr}
		if !strings.Contains(err.Error(), "session") {
			t.Fatalf("expected error string to contain 'session', got %s", err.Error())
		}
		if !errors.Is(err, rootErr) {
			t.Fatalf("expected Unwrap to return rootErr")
		}
	})

	t.Run("UnmarshalingParamError", func(t *testing.T) {
		t.Parallel()
		err := &UnmarshalingParamError{ParamName: "filter", Err: rootErr}
		if !strings.Contains(err.Error(), "filter") {
			t.Fatalf("expected error string to contain 'filter', got %s", err.Error())
		}
		if !errors.Is(err, rootErr) {
			t.Fatalf("expected Unwrap to return rootErr")
		}
	})

	t.Run("RequiredParamError", func(t *testing.T) {
		t.Parallel()
		err := &RequiredParamError{ParamName: "id"}
		if !strings.Contains(err.Error(), "id") {
			t.Fatalf("expected error string to contain 'id', got %s", err.Error())
		}
	})

	t.Run("RequiredHeaderError", func(t *testing.T) {
		t.Parallel()
		err := &RequiredHeaderError{ParamName: "Authorization", Err: rootErr}
		if !strings.Contains(err.Error(), "Authorization") {
			t.Fatalf("expected error string to contain 'Authorization', got %s", err.Error())
		}
		if !errors.Is(err, rootErr) {
			t.Fatalf("expected Unwrap to return rootErr")
		}
	})

	t.Run("InvalidParamFormatError", func(t *testing.T) {
		t.Parallel()
		err := &InvalidParamFormatError{ParamName: "count", Err: rootErr}
		if !strings.Contains(err.Error(), "count") {
			t.Fatalf("expected error string to contain 'count', got %s", err.Error())
		}
		if !errors.Is(err, rootErr) {
			t.Fatalf("expected Unwrap to return rootErr")
		}
	})

	t.Run("TooManyValuesForParamError", func(t *testing.T) {
		t.Parallel()
		err := &TooManyValuesForParamError{ParamName: "tag", Count: 3}
		if !strings.Contains(err.Error(), "tag") || !strings.Contains(err.Error(), "3") {
			t.Fatalf("expected error string to contain 'tag' and '3', got %s", err.Error())
		}
	})
}
