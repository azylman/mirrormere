package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type mockMediaActionSender struct {
	sendFunc   func(ctx context.Context, action string) error
	statusFunc func() StatusSnapshot
}

func (m *mockMediaActionSender) SendMediaAction(ctx context.Context, action string) error {
	if m.sendFunc != nil {
		return m.sendFunc(ctx, action)
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
