package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type mockMediaActionSender struct {
	sendFunc   func(ctx context.Context, action string) error
	wakeFunc   func(ctx context.Context) error
	statusFunc func() StatusSnapshot
}

func (m *mockMediaActionSender) SendMediaAction(ctx context.Context, action string) error {
	if m.sendFunc != nil {
		return m.sendFunc(ctx, action)
	}
	return nil
}

func (m *mockMediaActionSender) Wake(ctx context.Context) error {
	if m.wakeFunc != nil {
		return m.wakeFunc(ctx)
	}
	return nil
}

func (m *mockMediaActionSender) GetStatus() StatusSnapshot {
	if m.statusFunc != nil {
		return m.statusFunc()
	}
	return StatusSnapshot{
		Connected:   true,
		AppID:       "test-app",
		PlayerState: "playing",
	}
}

func TestActionServer_HandleAction_Success(t *testing.T) {
	var receivedAction string
	mockClient := &mockMediaActionSender{
		sendFunc: func(ctx context.Context, action string) error {
			receivedAction = action
			return nil
		},
	}

	server := NewActionServer(ServerConfig{}, mockClient)

	payload := `{"id":"chromecast","action":"toggle_playback"}`
	req := httptest.NewRequest(http.MethodPost, "/action", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}
	if receivedAction != "toggle_playback" {
		t.Errorf("expected action 'toggle_playback', got '%s'", receivedAction)
	}

	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("expected status 'ok', got '%s'", resp["status"])
	}
}

func TestActionServer_HandleAction_Wake(t *testing.T) {
	var wakeCalled bool
	mockClient := &mockMediaActionSender{
		wakeFunc: func(ctx context.Context) error {
			wakeCalled = true
			return nil
		},
	}

	server := NewActionServer(ServerConfig{}, mockClient)

	payload := `{"id":"chromecast","action":"wake"}`
	req := httptest.NewRequest(http.MethodPost, "/action", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}
	if !wakeCalled {
		t.Error("expected Wake to be called on client")
	}

	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("expected status 'ok', got '%s'", resp["status"])
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("expected CORS header, got %s", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestActionServer_HandleAction_WakeErrors(t *testing.T) {
	mockClient := &mockMediaActionSender{
		wakeFunc: func(ctx context.Context) error {
			return ErrNotConnected
		},
	}

	server := NewActionServer(ServerConfig{}, mockClient)

	// ErrNotConnected -> 502 Bad Gateway
	req := httptest.NewRequest(http.MethodPost, "/action", bytes.NewBufferString(`{"id":"chromecast","action":"wake"}`))
	rec := httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("expected 502 Bad Gateway, got %d", rec.Code)
	}

	// Internal error -> 500
	mockClient.wakeFunc = func(ctx context.Context) error {
		return errors.New("wake internal error")
	}
	req2 := httptest.NewRequest(http.MethodPost, "/action", bytes.NewBufferString(`{"id":"chromecast","action":"wake"}`))
	rec2 := httptest.NewRecorder()
	server.mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 Internal Server Error, got %d", rec2.Code)
	}
}

func TestActionServer_HandleAction_OptionsPreflight(t *testing.T) {
	mockClient := &mockMediaActionSender{}
	server := NewActionServer(ServerConfig{}, mockClient)

	req := httptest.NewRequest(http.MethodOptions, "/action", nil)
	rec := httptest.NewRecorder()

	server.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for OPTIONS, got %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("expected Access-Control-Allow-Origin: *, got '%s'", rec.Header().Get("Access-Control-Allow-Origin"))
	}
	if rec.Header().Get("Access-Control-Allow-Methods") != "POST, OPTIONS" {
		t.Errorf("expected Access-Control-Allow-Methods: POST, OPTIONS, got '%s'", rec.Header().Get("Access-Control-Allow-Methods"))
	}
	if rec.Header().Get("Access-Control-Allow-Headers") != "Content-Type" {
		t.Errorf("expected Access-Control-Allow-Headers: Content-Type, got '%s'", rec.Header().Get("Access-Control-Allow-Headers"))
	}
}

func TestActionServer_HandleAction_Errors(t *testing.T) {
	mockClient := &mockMediaActionSender{
		sendFunc: func(ctx context.Context, action string) error {
			switch action {
			case "invalid":
				return ErrInvalidAction
			case "not_connected":
				return ErrNotConnected
			case "no_session":
				return ErrNoActiveMediaSession
			case "fail":
				return errors.New("boom")
			default:
				return nil
			}
		},
	}

	server := NewActionServer(ServerConfig{}, mockClient)

	// 1. Method Not Allowed
	req := httptest.NewRequest(http.MethodGet, "/action", nil)
	rec := httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}

	// 2. Malformed JSON
	req = httptest.NewRequest(http.MethodPost, "/action", bytes.NewBufferString("{invalid"))
	rec = httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for malformed json, got %d", rec.Code)
	}

	// 3. Missing ID / Action
	req = httptest.NewRequest(http.MethodPost, "/action", bytes.NewBufferString(`{"id":""}`))
	rec = httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing fields, got %d", rec.Code)
	}

	// 4. Stream Not Active (id != "chromecast")
	req = httptest.NewRequest(http.MethodPost, "/action", bytes.NewBufferString(`{"id":"doorbell","action":"pause"}`))
	rec = httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown stream, got %d", rec.Code)
	}

	// 5. Invalid action
	req = httptest.NewRequest(http.MethodPost, "/action", bytes.NewBufferString(`{"id":"chromecast","action":"invalid"}`))
	rec = httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid action, got %d", rec.Code)
	}

	// 6. Not Connected -> 502
	req = httptest.NewRequest(http.MethodPost, "/action", bytes.NewBufferString(`{"id":"chromecast","action":"not_connected"}`))
	rec = httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("expected 502 for not connected, got %d", rec.Code)
	}

	// 7. No Media Session -> 502
	req = httptest.NewRequest(http.MethodPost, "/action", bytes.NewBufferString(`{"id":"chromecast","action":"no_session"}`))
	rec = httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("expected 502 for no media session, got %d", rec.Code)
	}

	// 8. Other internal error -> 500
	req = httptest.NewRequest(http.MethodPost, "/action", bytes.NewBufferString(`{"id":"chromecast","action":"fail"}`))
	rec = httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 for internal error, got %d", rec.Code)
	}
}

func TestActionServer_HandleHealthz(t *testing.T) {
	mockClient := &mockMediaActionSender{
		statusFunc: func() StatusSnapshot {
			return StatusSnapshot{
				Connected:   true,
				AppID:       "CC1AD845",
				PlayerState: "buffering",
			}
		},
	}

	server := NewActionServer(ServerConfig{}, mockClient)

	// GET /healthz
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}

	var snap map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatalf("failed to decode healthz response: %v", err)
	}
	if snap["status"] != "ok" || snap["chromecast_connected"] != true || snap["app_id"] != "CC1AD845" || snap["player_state"] != "buffering" {
		t.Errorf("unexpected healthz payload: %+v", snap)
	}

	// HEAD /healthz
	reqHead := httptest.NewRequest(http.MethodHead, "/healthz", nil)
	recHead := httptest.NewRecorder()
	server.mux.ServeHTTP(recHead, reqHead)
	if recHead.Code != http.StatusOK {
		t.Errorf("expected 200 for HEAD, got %d", recHead.Code)
	}

	// POST /healthz -> 405
	reqPost := httptest.NewRequest(http.MethodPost, "/healthz", nil)
	recPost := httptest.NewRecorder()
	server.mux.ServeHTTP(recPost, reqPost)
	if recPost.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for POST /healthz, got %d", recPost.Code)
	}
}

type errResponseWriter struct {
	header http.Header
}

func (e *errResponseWriter) Header() http.Header         { return e.header }
func (e *errResponseWriter) Write([]byte) (int, error)   { return 0, errors.New("simulated write error") }
func (e *errResponseWriter) WriteHeader(statusCode int) {}

func TestWriteJSON_Error(t *testing.T) {
	rw := &errResponseWriter{header: make(http.Header)}
	writeJSON(rw, http.StatusOK, map[string]string{"key": "val"})
}

func TestActionServer_HandleCoverageFlush(t *testing.T) {
	mockClient := &mockMediaActionSender{}
	server := NewActionServer(ServerConfig{}, mockClient)

	// 1. Method Not Allowed (e.g. DELETE)
	t.Run("MethodNotAllowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/debug/coverage/flush", nil)
		rec := httptest.NewRecorder()
		server.mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405 Method Not Allowed, got %d", rec.Code)
		}
	})

	// 2. Unset GOCOVERDIR -> 200 OK
	t.Run("UnsetGOCOVERDIR", func(t *testing.T) {
		t.Setenv("GOCOVERDIR", "")
		req := httptest.NewRequest(http.MethodGet, "/debug/coverage/flush", nil)
		rec := httptest.NewRecorder()
		server.mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "\"status\":\"ok\"") {
			t.Fatalf("expected status ok in body, got %q", rec.Body.String())
		}
	})

	// 3. Set GOCOVERDIR with success and debounce
	t.Run("SuccessWithGOCOVERDIR_And_Debounce", func(t *testing.T) {
		simulatedTime := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
		restoreTime := SetNowFnForTest(func() time.Time {
			return simulatedTime
		})
		defer restoreTime()

		tempDir := t.TempDir()
		t.Setenv("GOCOVERDIR", tempDir)

		var capturedDir string
		restoreWriter := SetWriteCountersDirForTest(func(dir string) error {
			capturedDir = dir
			return nil
		})
		defer restoreWriter()

		req := httptest.NewRequest(http.MethodPost, "/debug/coverage/flush", nil)
		rec := httptest.NewRecorder()
		server.mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", rec.Code)
		}
		if capturedDir != tempDir {
			t.Fatalf("expected capturedDir %q, got %q", tempDir, capturedDir)
		}
		if !strings.Contains(rec.Body.String(), "\"status\":\"ok\"") {
			t.Fatalf("expected status ok in body, got %q", rec.Body.String())
		}

		// Immediate second call should be debounced
		rec2 := httptest.NewRecorder()
		server.mux.ServeHTTP(rec2, req)
		if rec2.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for debounced flush, got %d", rec2.Code)
		}
		if !strings.Contains(rec2.Body.String(), "\"status\":\"debounced\"") {
			t.Fatalf("expected debounced status in body, got %q", rec2.Body.String())
		}
	})

	// 4. Set GOCOVERDIR with error -> 500
	t.Run("ErrorWithGOCOVERDIR", func(t *testing.T) {
		simulatedTime := time.Date(2026, 10, 4, 13, 0, 0, 0, time.UTC)
		restoreTime := SetNowFnForTest(func() time.Time {
			return simulatedTime
		})
		defer restoreTime()

		tempDir := t.TempDir()
		t.Setenv("GOCOVERDIR", tempDir)

		restoreWriter := SetWriteCountersDirForTest(func(dir string) error {
			return errors.New("simulated flush error")
		})
		defer restoreWriter()

		req := httptest.NewRequest(http.MethodGet, "/debug/coverage/flush", nil)
		rec := httptest.NewRecorder()
		server.mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500 Internal Server Error, got %d", rec.Code)
		}
	})
}
