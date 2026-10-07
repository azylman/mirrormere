package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azylman/mirrormere/internal/api"
	"github.com/azylman/mirrormere/internal/events"
	"github.com/azylman/mirrormere/internal/server"
	"github.com/azylman/mirrormere/internal/voice"
	"github.com/prometheus/client_golang/prometheus"
)

type mockVoiceCoord struct {
	state      string
	transcript *string
	reply      *string
	ttsEngine  *string
	setErr     error
}

func (m *mockVoiceCoord) GetState() events.VoiceStateData {
	return events.VoiceStateData{
		State:      m.state,
		Transcript: m.transcript,
		Reply:      m.reply,
		TTSEngine:  m.ttsEngine,
	}
}

func (m *mockVoiceCoord) SetState(state string, transcript, reply, ttsEngine *string) (events.VoiceStateData, error) {
	if m.setErr != nil {
		return events.VoiceStateData{}, m.setErr
	}
	m.state = state
	m.transcript = transcript
	m.reply = reply
	m.ttsEngine = ttsEngine
	return events.VoiceStateData{
		State:      m.state,
		Transcript: m.transcript,
		Reply:      m.reply,
		TTSEngine:  m.ttsEngine,
	}, nil
}

type mockVoiceHub struct {
	enabled    bool
	interactFn func(ctx context.Context, audio io.Reader, nodeID, sessionID string, sink voice.SSEEventSink, timings voice.EdgeTimings) error
}

func (m *mockVoiceHub) IsEnabled() bool {
	return m.enabled
}

func (m *mockVoiceHub) Interact(ctx context.Context, audio io.Reader, nodeID, sessionID string, sink voice.SSEEventSink, timings voice.EdgeTimings) error {
	if m.interactFn != nil {
		return m.interactFn(ctx, audio, nodeID, sessionID, sink, timings)
	}
	return nil
}

var dummyWAV = append([]byte("RIFF1234WAVEfmt \x10\x00\x00\x00\x01\x00\x01\x00\x80>\x00\x00\x00}\x00\x00\x02\x00\x10\x00data\x00\x00\x00\x00"), make([]byte, 100)...)

func createMultipartAudioRequest(method, url string, fieldName, fileName string, fileContent []byte, extraFields map[string]string) (*http.Request, error) {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	if fieldName != "" {
		part, err := w.CreateFormFile(fieldName, fileName)
		if err != nil {
			return nil, err
		}
		if _, err := part.Write(fileContent); err != nil {
			return nil, err
		}
	}
	for k, v := range extraFields {
		if err := w.WriteField(k, v); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	req := httptest.NewRequest(method, url, &b)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req, nil
}

type nonFlusherResponseWriter struct {
	header http.Header
	code   int
	body   bytes.Buffer
}

func newNonFlusherResponseWriter() *nonFlusherResponseWriter {
	return &nonFlusherResponseWriter{header: make(http.Header)}
}

func (w *nonFlusherResponseWriter) Header() http.Header { return w.header }
func (w *nonFlusherResponseWriter) Write(b []byte) (int, error) { return w.body.Write(b) }
func (w *nonFlusherResponseWriter) WriteHeader(code int) {
	if w.code == 0 {
		w.code = code
	}
}

func TestDefaultVoiceHandler_PostVoiceState(t *testing.T) {
	t.Parallel()

	t.Run("OPTIONS returns 204 No Content", func(t *testing.T) {
		t.Parallel()
		coord := &mockVoiceCoord{}
		h := server.NewDefaultVoiceHandler(coord, nil)

		req := httptest.NewRequest(http.MethodOptions, "/api/voice/state", nil)
		rec := httptest.NewRecorder()
		h.PostVoiceState(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected 204 No Content, got %d", rec.Code)
		}
		if allow := rec.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(allow, "POST") {
			t.Errorf("expected Access-Control-Allow-Methods containing POST, got %q", allow)
		}
	})

	t.Run("GET returns 405 Method Not Allowed", func(t *testing.T) {
		t.Parallel()
		coord := &mockVoiceCoord{}
		h := server.NewDefaultVoiceHandler(coord, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/voice/state", nil)
		rec := httptest.NewRecorder()
		h.PostVoiceState(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405 Method Not Allowed, got %d", rec.Code)
		}
		if allow := rec.Header().Get("Allow"); allow != "POST, OPTIONS" {
			t.Errorf("expected Allow: POST, OPTIONS, got %q", allow)
		}
	})

	t.Run("nil coordinator returns 500 Internal Server Error", func(t *testing.T) {
		t.Parallel()
		h := server.NewDefaultVoiceHandler(nil, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/voice/state", strings.NewReader(`{"state":"listening"}`))
		rec := httptest.NewRecorder()
		h.PostVoiceState(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d", rec.Code)
		}
		var errResp api.ErrorResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
			t.Fatalf("failed to decode error response: %v", err)
		}
		if errResp.Error != "voice coordinator not configured" {
			t.Fatalf("unexpected error message: %q", errResp.Error)
		}
	})

	t.Run("invalid JSON body returns 400 Bad Request", func(t *testing.T) {
		t.Parallel()
		coord := &mockVoiceCoord{}
		h := server.NewDefaultVoiceHandler(coord, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/voice/state", strings.NewReader(`{invalid`))
		rec := httptest.NewRecorder()
		h.PostVoiceState(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request, got %d", rec.Code)
		}
	})

	t.Run("invalid state enum returns 400 Bad Request with error message", func(t *testing.T) {
		t.Parallel()
		coord := &mockVoiceCoord{}
		h := server.NewDefaultVoiceHandler(coord, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/voice/state", strings.NewReader(`{"state":"invalid_state"}`))
		rec := httptest.NewRecorder()
		h.PostVoiceState(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request, got %d", rec.Code)
		}
		var errResp api.ErrorResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
			t.Fatalf("failed to decode error response: %v", err)
		}
		if errResp.Error != voice.ErrInvalidState.Error() {
			t.Fatalf("expected error %q, got %q", voice.ErrInvalidState.Error(), errResp.Error)
		}
	})

	t.Run("coordinator SetState error returns 400 Bad Request", func(t *testing.T) {
		t.Parallel()
		coord := &mockVoiceCoord{setErr: errors.New("simulated error")}
		h := server.NewDefaultVoiceHandler(coord, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/voice/state", strings.NewReader(`{"state":"listening"}`))
		rec := httptest.NewRecorder()
		h.PostVoiceState(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request, got %d", rec.Code)
		}
	})

	t.Run("valid voice state returns 200 OK", func(t *testing.T) {
		t.Parallel()
		coord := &mockVoiceCoord{}
		h := server.NewDefaultVoiceHandler(coord, nil)

		body := `{"state":"thinking","transcript":"What is the time?","reply":null,"tts_engine":null}`
		req := httptest.NewRequest(http.MethodPost, "/api/voice/state", strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.PostVoiceState(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
		}
		var resp api.VoiceStateResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if resp.Status != "ok" {
			t.Fatalf("expected status ok, got %q", resp.Status)
		}

		if coord.state != "thinking" {
			t.Fatalf("expected coord state 'thinking', got %q", coord.state)
		}
		if coord.transcript == nil || *coord.transcript != "What is the time?" {
			t.Fatalf("expected transcript 'What is the time?', got %+v", coord.transcript)
		}
		if coord.reply != nil {
			t.Fatalf("expected nil reply, got %+v", coord.reply)
		}
		if coord.ttsEngine != nil {
			t.Fatalf("expected nil ttsEngine, got %+v", coord.ttsEngine)
		}
	})

	t.Run("speaking state with reply and tts_engine", func(t *testing.T) {
		t.Parallel()
		coord := &mockVoiceCoord{}
		h := server.NewDefaultVoiceHandler(coord, nil)

		body := `{"state":"speaking","transcript":"Alex's calendar","reply":"Meeting at 2pm","tts_engine":"kokoro"}`
		req := httptest.NewRequest(http.MethodPost, "/api/voice/state", strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.PostVoiceState(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", rec.Code)
		}
		if coord.state != "speaking" {
			t.Fatalf("expected coord state 'speaking', got %q", coord.state)
		}
		if coord.reply == nil || *coord.reply != "Meeting at 2pm" {
			t.Fatalf("expected reply 'Meeting at 2pm', got %+v", coord.reply)
		}
		if coord.ttsEngine == nil || *coord.ttsEngine != "kokoro" {
			t.Fatalf("expected tts_engine 'kokoro', got %+v", coord.ttsEngine)
		}
	})

	t.Run("post idle transitions coordinator from listening to idle", func(t *testing.T) {
		t.Parallel()
		coord := voice.NewCoordinator(nil)
		h := server.NewDefaultVoiceHandler(coord, nil)

		q := "hey mirrormere"
		if _, err := coord.SetState(voice.StateListening, &q, nil, nil); err != nil {
			t.Fatalf("failed to set listening state: %v", err)
		}

		req := httptest.NewRequest(http.MethodPost, "/api/voice/state", strings.NewReader(`{"state":"idle"}`))
		rec := httptest.NewRecorder()
		h.PostVoiceState(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", rec.Code)
		}
		if coord.GetState().State != voice.StateIdle {
			t.Fatalf("expected coordinator state 'idle', got %q", coord.GetState().State)
		}
	})
}

func TestDefaultVoiceHandler_PostVoiceInteract(t *testing.T) {
	t.Parallel()

	t.Run("OPTIONS returns 204 No Content", func(t *testing.T) {
		t.Parallel()
		hub := &mockVoiceHub{enabled: true}
		h := server.NewDefaultVoiceHandler(nil, hub)

		req := httptest.NewRequest(http.MethodOptions, "/api/voice/interact", nil)
		rec := httptest.NewRecorder()
		h.PostVoiceInteract(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected 204 No Content, got %d", rec.Code)
		}
		if allow := rec.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(allow, "POST") {
			t.Errorf("expected Access-Control-Allow-Methods containing POST, got %q", allow)
		}
	})

	t.Run("GET returns 405 Method Not Allowed", func(t *testing.T) {
		t.Parallel()
		hub := &mockVoiceHub{enabled: true}
		h := server.NewDefaultVoiceHandler(nil, hub)

		req := httptest.NewRequest(http.MethodGet, "/api/voice/interact", nil)
		rec := httptest.NewRecorder()
		h.PostVoiceInteract(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405 Method Not Allowed, got %d", rec.Code)
		}
		if allow := rec.Header().Get("Allow"); allow != "POST, OPTIONS" {
			t.Errorf("expected Allow: POST, OPTIONS, got %q", allow)
		}
	})

	t.Run("nil hub returns 503 Service Unavailable", func(t *testing.T) {
		t.Parallel()
		h := server.NewDefaultVoiceHandler(nil, nil)

		req, err := createMultipartAudioRequest(http.MethodPost, "/api/voice/interact", "audio", "sample.wav", dummyWAV, nil)
		if err != nil {
			t.Fatalf("failed to create req: %v", err)
		}
		rec := httptest.NewRecorder()
		h.PostVoiceInteract(rec, req)

		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503, got %d", rec.Code)
		}
		var errResp api.ErrorResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
			t.Fatalf("failed to unmarshal error: %v", err)
		}
		if errResp.Error != "voice hub is disabled" {
			t.Errorf("unexpected error message: %q", errResp.Error)
		}
	})

	t.Run("disabled hub returns 503 Service Unavailable", func(t *testing.T) {
		t.Parallel()
		hub := &mockVoiceHub{enabled: false}
		h := server.NewDefaultVoiceHandler(nil, hub)

		req, err := createMultipartAudioRequest(http.MethodPost, "/api/voice/interact", "audio", "sample.wav", dummyWAV, nil)
		if err != nil {
			t.Fatalf("failed to create req: %v", err)
		}
		rec := httptest.NewRecorder()
		h.PostVoiceInteract(rec, req)

		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503, got %d", rec.Code)
		}
	})

	t.Run("non-flusher response writer returns 500", func(t *testing.T) {
		t.Parallel()
		hub := &mockVoiceHub{enabled: true}
		h := server.NewDefaultVoiceHandler(nil, hub)

		req, err := createMultipartAudioRequest(http.MethodPost, "/api/voice/interact", "audio", "sample.wav", dummyWAV, nil)
		if err != nil {
			t.Fatalf("failed to create req: %v", err)
		}
		rec := newNonFlusherResponseWriter()
		h.PostVoiceInteract(rec, req)

		if rec.code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d", rec.code)
		}
	})

	t.Run("invalid multipart payload returns 400 Bad Request", func(t *testing.T) {
		t.Parallel()
		hub := &mockVoiceHub{enabled: true}
		h := server.NewDefaultVoiceHandler(nil, hub)

		req := httptest.NewRequest(http.MethodPost, "/api/voice/interact", strings.NewReader("not a multipart"))
		req.Header.Set("Content-Type", "multipart/form-data; boundary=invalid_boundary")
		rec := httptest.NewRecorder()
		h.PostVoiceInteract(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request, got %d", rec.Code)
		}
	})

	t.Run("missing audio field returns 400 Bad Request", func(t *testing.T) {
		t.Parallel()
		hub := &mockVoiceHub{enabled: true}
		h := server.NewDefaultVoiceHandler(nil, hub)

		req, err := createMultipartAudioRequest(http.MethodPost, "/api/voice/interact", "other_field", "sample.wav", dummyWAV, nil)
		if err != nil {
			t.Fatalf("failed to create req: %v", err)
		}
		rec := httptest.NewRecorder()
		h.PostVoiceInteract(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request, got %d", rec.Code)
		}
		var errResp api.ErrorResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}
		if errResp.Error != "missing required 'audio' form field" {
			t.Errorf("unexpected error message: %q", errResp.Error)
		}
	})

	t.Run("hub returns ErrInvalidAudio returns 400 Bad Request", func(t *testing.T) {
		t.Parallel()
		hub := &mockVoiceHub{
			enabled: true,
			interactFn: func(ctx context.Context, audio io.Reader, nodeID, sessionID string, sink voice.SSEEventSink, timings voice.EdgeTimings) error {
				return voice.ErrInvalidAudio
			},
		}
		h := server.NewDefaultVoiceHandler(nil, hub)

		req, err := createMultipartAudioRequest(http.MethodPost, "/api/voice/interact", "audio", "sample.wav", dummyWAV, nil)
		if err != nil {
			t.Fatalf("failed to create req: %v", err)
		}
		rec := httptest.NewRecorder()
		h.PostVoiceInteract(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("hub returns ErrInteractionBusy returns 409 Conflict", func(t *testing.T) {
		t.Parallel()
		hub := &mockVoiceHub{
			enabled: true,
			interactFn: func(ctx context.Context, audio io.Reader, nodeID, sessionID string, sink voice.SSEEventSink, timings voice.EdgeTimings) error {
				return voice.ErrInteractionBusy
			},
		}
		h := server.NewDefaultVoiceHandler(nil, hub)

		req, err := createMultipartAudioRequest(http.MethodPost, "/api/voice/interact", "audio", "sample.wav", dummyWAV, nil)
		if err != nil {
			t.Fatalf("failed to create req: %v", err)
		}
		rec := httptest.NewRecorder()
		h.PostVoiceInteract(rec, req)

		if rec.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d", rec.Code)
		}
	})

	t.Run("hub returns ErrHubDisabled returns 503 Service Unavailable", func(t *testing.T) {
		t.Parallel()
		hub := &mockVoiceHub{
			enabled: true,
			interactFn: func(ctx context.Context, audio io.Reader, nodeID, sessionID string, sink voice.SSEEventSink, timings voice.EdgeTimings) error {
				return voice.ErrHubDisabled
			},
		}
		h := server.NewDefaultVoiceHandler(nil, hub)

		req, err := createMultipartAudioRequest(http.MethodPost, "/api/voice/interact", "audio", "sample.wav", dummyWAV, nil)
		if err != nil {
			t.Fatalf("failed to create req: %v", err)
		}
		rec := httptest.NewRecorder()
		h.PostVoiceInteract(rec, req)

		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503, got %d", rec.Code)
		}
	})

	t.Run("hub returns generic error before header returns 500 Internal Server Error", func(t *testing.T) {
		t.Parallel()
		hub := &mockVoiceHub{
			enabled: true,
			interactFn: func(ctx context.Context, audio io.Reader, nodeID, sessionID string, sink voice.SSEEventSink, timings voice.EdgeTimings) error {
				return errors.New("boom")
			},
		}
		h := server.NewDefaultVoiceHandler(nil, hub)

		req, err := createMultipartAudioRequest(http.MethodPost, "/api/voice/interact", "audio", "sample.wav", dummyWAV, nil)
		if err != nil {
			t.Fatalf("failed to create req: %v", err)
		}
		rec := httptest.NewRecorder()
		h.PostVoiceInteract(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d", rec.Code)
		}
	})

	t.Run("successful interaction streams SSE events with 200 OK", func(t *testing.T) {
		t.Parallel()
		var capturedNodeID, capturedSessionID string
		hub := &mockVoiceHub{
			enabled: true,
			interactFn: func(ctx context.Context, audio io.Reader, nodeID, sessionID string, sink voice.SSEEventSink, timings voice.EdgeTimings) error {
				capturedNodeID = nodeID
				capturedSessionID = sessionID

				if err := sink("state", map[string]string{"state": "transcribing"}); err != nil {
					return err
				}
				if err := sink("status", map[string]string{"status": "Thinking..."}); err != nil {
					return err
				}
				if err := sink("reply", map[string]string{"reply": "Hello Alex!"}); err != nil {
					return err
				}
				if err := sink("audio_chunk", map[string]any{"chunk_index": 0, "data": "base64audio"}); err != nil {
					return err
				}
				return sink("done", map[string]any{"duration_ms": 150})
			},
		}
		h := server.NewDefaultVoiceHandler(nil, hub)

		req, err := createMultipartAudioRequest(http.MethodPost, "/api/voice/interact", "audio", "sample.wav", dummyWAV, map[string]string{
			"node_id":    "kiosk-1",
			"session_id": "sess-xyz",
		})
		if err != nil {
			t.Fatalf("failed to create req: %v", err)
		}
		rec := httptest.NewRecorder()
		h.PostVoiceInteract(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
			t.Errorf("expected Content-Type text/event-stream, got %q", ct)
		}
		if accel := rec.Header().Get("X-Accel-Buffering"); accel != "no" {
			t.Errorf("expected X-Accel-Buffering no, got %q", accel)
		}
		if capturedNodeID != "kiosk-1" {
			t.Errorf("expected node_id kiosk-1, got %q", capturedNodeID)
		}
		if capturedSessionID != "sess-xyz" {
			t.Errorf("expected session_id sess-xyz, got %q", capturedSessionID)
		}

		body := rec.Body.String()
		if !strings.Contains(body, "event: state\ndata: {\"state\":\"transcribing\"}\n\n") {
			t.Errorf("missing state event in body: %q", body)
		}
		if !strings.Contains(body, "event: status\ndata: {\"status\":\"Thinking...\"}\n\n") {
			t.Errorf("missing status event in body: %q", body)
		}
		if !strings.Contains(body, "event: reply\ndata: {\"reply\":\"Hello Alex!\"}\n\n") {
			t.Errorf("missing reply event in body: %q", body)
		}
		if !strings.Contains(body, "event: audio_chunk") {
			t.Errorf("missing audio_chunk event in body: %q", body)
		}
		if !strings.Contains(body, "event: done") {
			t.Errorf("missing done event in body: %q", body)
		}
	})

	t.Run("hub returns error after header already written", func(t *testing.T) {
		t.Parallel()
		hub := &mockVoiceHub{
			enabled: true,
			interactFn: func(ctx context.Context, audio io.Reader, nodeID, sessionID string, sink voice.SSEEventSink, timings voice.EdgeTimings) error {
				if err := sink("state", map[string]string{"state": "transcribing"}); err != nil {
					return err
				}
				return errors.New("aborted mid stream")
			},
		}
		h := server.NewDefaultVoiceHandler(nil, hub)

		req, err := createMultipartAudioRequest(http.MethodPost, "/api/voice/interact", "audio", "sample.wav", dummyWAV, nil)
		if err != nil {
			t.Fatalf("failed to create req: %v", err)
		}
		rec := httptest.NewRecorder()
		h.PostVoiceInteract(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", rec.Code)
		}
	})

	t.Run("node_id and session_id forwarded to hub", func(t *testing.T) {
		t.Parallel()
		var capturedNodeID, capturedSessionID string
		hub := &mockVoiceHub{
			enabled: true,
			interactFn: func(ctx context.Context, audio io.Reader, nodeID, sessionID string, sink voice.SSEEventSink, timings voice.EdgeTimings) error {
				capturedNodeID = nodeID
				capturedSessionID = sessionID
				return sink("done", map[string]any{"duration_ms": 100})
			},
		}
		h := server.NewDefaultVoiceHandler(nil, hub)

		req, err := createMultipartAudioRequest(http.MethodPost, "/api/voice/interact", "audio", "sample.wav", dummyWAV, map[string]string{
			"node_id":    "touch-kiosk-kitchen",
			"session_id": "custom-sess-123",
		})
		if err != nil {
			t.Fatalf("failed to create req: %v", err)
		}
		rec := httptest.NewRecorder()
		h.PostVoiceInteract(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
		}
		if capturedNodeID != "touch-kiosk-kitchen" {
			t.Errorf("expected node_id touch-kiosk-kitchen, got %q", capturedNodeID)
		}
		if capturedSessionID != "custom-sess-123" {
			t.Errorf("expected session_id custom-sess-123, got %q", capturedSessionID)
		}
	})
}

func TestDefaultVoiceHandler_PostVoiceHeartbeat(t *testing.T) {
	t.Parallel()

	t.Run("valid payload records metrics and returns 200 OK", func(t *testing.T) {
		t.Parallel()
		reg := prometheus.NewRegistry()
		m := voice.NewMetrics(reg)
		h := server.NewDefaultVoiceHandler(nil, nil, server.WithVoiceMetrics(m))

		payload := `{"node_id":"kitchen-display","ambient_rms_dbfs":-42.3,"false_wakes":2,"last_playback_sec":1.5}`
		req := httptest.NewRequest(http.MethodPost, "/api/voice/heartbeat", strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		h.PostVoiceHeartbeat(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
		}
		if origin := rec.Header().Get("Access-Control-Allow-Origin"); origin != "*" {
			t.Errorf("expected CORS origin '*', got %q", origin)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected Content-Type application/json, got %q", ct)
		}

		var resp map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if resp["status"] != "ok" {
			t.Errorf("expected status 'ok', got %q", resp["status"])
		}

		mfs, err := reg.Gather()
		if err != nil {
			t.Fatalf("failed to gather metrics: %v", err)
		}
		var foundRMS, foundHeartbeat, foundFalseWakes, foundPlayback bool
		for _, mf := range mfs {
			switch mf.GetName() {
			case "mirrormere_voice_ambient_rms_dbfs":
				foundRMS = true
				if val := mf.GetMetric()[0].GetGauge().GetValue(); val != -42.3 {
					t.Errorf("expected ambient RMS -42.3, got %f", val)
				}
			case "mirrormere_voice_edge_last_seen_timestamp_seconds":
				foundHeartbeat = true
				if val := mf.GetMetric()[0].GetGauge().GetValue(); val <= 0 {
					t.Errorf("expected positive heartbeat timestamp, got %f", val)
				}
			case "mirrormere_voice_false_wakes_total":
				foundFalseWakes = true
				if val := mf.GetMetric()[0].GetCounter().GetValue(); val != 2 {
					t.Errorf("expected 2 false wakes, got %f", val)
				}
			case "mirrormere_voice_playback_duration_seconds":
				foundPlayback = true
				if count := mf.GetMetric()[0].GetHistogram().GetSampleCount(); count != 1 {
					t.Errorf("expected 1 playback observation, got %d", count)
				}
			}
		}
		if !foundRMS || !foundHeartbeat || !foundFalseWakes || !foundPlayback {
			t.Errorf("missing expected metrics: rms=%v, heartbeat=%v, falseWakes=%v, playback=%v",
				foundRMS, foundHeartbeat, foundFalseWakes, foundPlayback)
		}
	})

	t.Run("missing or empty node_id returns 400 Bad Request", func(t *testing.T) {
		t.Parallel()
		h := server.NewDefaultVoiceHandler(nil, nil)

		cases := []string{
			`{}`,
			`{"node_id":""}`,
			`{"node_id":"   "}`,
		}

		for _, payload := range cases {
			req := httptest.NewRequest(http.MethodPost, "/api/voice/heartbeat", strings.NewReader(payload))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			h.PostVoiceHeartbeat(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 for payload %q, got %d: %s", payload, rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "missing required 'node_id' field") {
				t.Errorf("expected missing node_id message, got %s", rec.Body.String())
			}
		}
	})

	t.Run("malformed JSON body returns 400 Bad Request", func(t *testing.T) {
		t.Parallel()
		h := server.NewDefaultVoiceHandler(nil, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/voice/heartbeat", strings.NewReader(`{invalid-json`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		h.PostVoiceHeartbeat(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "invalid heartbeat payload") {
			t.Errorf("expected invalid payload error, got %s", rec.Body.String())
		}
	})

	t.Run("method not allowed returns 405 with Allow header", func(t *testing.T) {
		t.Parallel()
		h := server.NewDefaultVoiceHandler(nil, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/voice/heartbeat", nil)
		rec := httptest.NewRecorder()

		h.PostVoiceHeartbeat(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405, got %d", rec.Code)
		}
		if allow := rec.Header().Get("Allow"); !strings.Contains(allow, "POST") {
			t.Errorf("expected Allow header containing POST, got %q", allow)
		}
	})

	t.Run("OPTIONS returns 204 No Content with CORS headers", func(t *testing.T) {
		t.Parallel()
		h := server.NewDefaultVoiceHandler(nil, nil)

		req := httptest.NewRequest(http.MethodOptions, "/api/voice/heartbeat", nil)
		rec := httptest.NewRecorder()

		h.PostVoiceHeartbeat(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected 204, got %d", rec.Code)
		}
		if origin := rec.Header().Get("Access-Control-Allow-Origin"); origin != "*" {
			t.Errorf("expected origin '*', got %q", origin)
		}
	})

	t.Run("nil metrics in handler does not panic", func(t *testing.T) {
		t.Parallel()
		h := server.NewDefaultVoiceHandler(nil, nil)

		payload := `{"node_id":"kitchen-display","ambient_rms_dbfs":-40.0,"false_wakes":1,"last_playback_sec":2.0}`
		req := httptest.NewRequest(http.MethodPost, "/api/voice/heartbeat", strings.NewReader(payload))
		rec := httptest.NewRecorder()

		h.PostVoiceHeartbeat(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", rec.Code)
		}
	})

	t.Run("omitted or zero/negative optional fields do not record false wakes or playback", func(t *testing.T) {
		t.Parallel()
		reg := prometheus.NewRegistry()
		m := voice.NewMetrics(reg)
		h := server.NewDefaultVoiceHandler(nil, nil, server.WithVoiceMetrics(m))

		negFalseWakes := -1
		zeroPlayback := 0.0
		reqPayload, _ := json.Marshal(server.VoiceHeartbeatRequest{
			NodeID:          "kitchen-display",
			FalseWakes:      &negFalseWakes,
			LastPlaybackSec: &zeroPlayback,
		})
		req := httptest.NewRequest(http.MethodPost, "/api/voice/heartbeat", bytes.NewReader(reqPayload))
		rec := httptest.NewRecorder()

		h.PostVoiceHeartbeat(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", rec.Code)
		}

		mfs, err := reg.Gather()
		if err != nil {
			t.Fatalf("failed to gather: %v", err)
		}
		for _, mf := range mfs {
			if mf.GetName() == "mirrormere_voice_false_wakes_total" {
				t.Fatalf("expected no false_wakes recorded for negative value")
			}
			if mf.GetName() == "mirrormere_voice_playback_duration_seconds" {
				t.Fatalf("expected no playback recorded for zero duration")
			}
		}
	})
}

func TestDefaultVoiceHandler_PostVoiceInteract_TimingFields(t *testing.T) {
	t.Parallel()

	t.Run("parses valid timing fields in milliseconds into seconds", func(t *testing.T) {
		t.Parallel()
		var capturedTimings voice.EdgeTimings
		hub := &mockVoiceHub{
			enabled: true,
			interactFn: func(ctx context.Context, audio io.Reader, nodeID, sessionID string, sink voice.SSEEventSink, timings voice.EdgeTimings) error {
				capturedTimings = timings
				return sink("done", map[string]any{"status": "ok"})
			},
		}
		h := server.NewDefaultVoiceHandler(nil, hub)

		req, err := createMultipartAudioRequest(http.MethodPost, "/api/voice/interact", "audio", "sample.wav", dummyWAV, map[string]string{
			"node_id":             "node-1",
			"session_id":          "s-1",
			"wake_eval_ms":        "45.5",
			"speech_duration_ms":  "1250.0",
			"silence_duration_ms": "400.0",
		})
		if err != nil {
			t.Fatalf("failed to create req: %v", err)
		}
		rec := httptest.NewRecorder()
		h.PostVoiceInteract(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
		}
		if capturedTimings.WakeEvalSec != 0.0455 {
			t.Errorf("expected WakeEvalSec 0.0455, got %f", capturedTimings.WakeEvalSec)
		}
		if capturedTimings.UtteranceSpeechSec != 1.25 {
			t.Errorf("expected UtteranceSpeechSec 1.25, got %f", capturedTimings.UtteranceSpeechSec)
		}
		if capturedTimings.UtteranceSilenceSec != 0.4 {
			t.Errorf("expected UtteranceSilenceSec 0.4, got %f", capturedTimings.UtteranceSilenceSec)
		}
	})

	t.Run("malformed and out of range timing fields default to zero without crashing", func(t *testing.T) {
		t.Parallel()
		var capturedTimings voice.EdgeTimings
		hub := &mockVoiceHub{
			enabled: true,
			interactFn: func(ctx context.Context, audio io.Reader, nodeID, sessionID string, sink voice.SSEEventSink, timings voice.EdgeTimings) error {
				capturedTimings = timings
				return sink("done", map[string]any{"status": "ok"})
			},
		}
		h := server.NewDefaultVoiceHandler(nil, hub)

		req, err := createMultipartAudioRequest(http.MethodPost, "/api/voice/interact", "audio", "sample.wav", dummyWAV, map[string]string{
			"node_id":             "node-1",
			"session_id":          "s-1",
			"wake_eval_ms":        "not-a-number",
			"speech_duration_ms":  "-50",
			"silence_duration_ms": "999999",
		})
		if err != nil {
			t.Fatalf("failed to create req: %v", err)
		}
		rec := httptest.NewRecorder()
		h.PostVoiceInteract(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
		}
		if capturedTimings.WakeEvalSec != 0 {
			t.Errorf("expected WakeEvalSec 0 for NaN, got %f", capturedTimings.WakeEvalSec)
		}
		if capturedTimings.UtteranceSpeechSec != 0 {
			t.Errorf("expected UtteranceSpeechSec 0 for negative, got %f", capturedTimings.UtteranceSpeechSec)
		}
		if capturedTimings.UtteranceSilenceSec != 0 {
			t.Errorf("expected UtteranceSilenceSec 0 for >60s, got %f", capturedTimings.UtteranceSilenceSec)
		}

		// Also verify parse error on speech and silence, and out-of-range on wake
		req2, err := createMultipartAudioRequest(http.MethodPost, "/api/voice/interact", "audio", "sample.wav", dummyWAV, map[string]string{
			"node_id":             "node-1",
			"session_id":          "s-1",
			"wake_eval_ms":        "-10",
			"speech_duration_ms":  "invalid",
			"silence_duration_ms": "bad",
		})
		if err != nil {
			t.Fatalf("failed to create req2: %v", err)
		}
		rec2 := httptest.NewRecorder()
		h.PostVoiceInteract(rec2, req2)
		if rec2.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", rec2.Code, rec2.Body.String())
		}
		if capturedTimings != (voice.EdgeTimings{}) {
			t.Errorf("expected zero EdgeTimings, got %+v", capturedTimings)
		}
	})

	t.Run("missing timing fields leave EdgeTimings zeroed", func(t *testing.T) {
		t.Parallel()
		var capturedTimings voice.EdgeTimings
		hub := &mockVoiceHub{
			enabled: true,
			interactFn: func(ctx context.Context, audio io.Reader, nodeID, sessionID string, sink voice.SSEEventSink, timings voice.EdgeTimings) error {
				capturedTimings = timings
				return sink("done", map[string]any{"status": "ok"})
			},
		}
		h := server.NewDefaultVoiceHandler(nil, hub)

		req, err := createMultipartAudioRequest(http.MethodPost, "/api/voice/interact", "audio", "sample.wav", dummyWAV, map[string]string{
			"node_id":    "node-1",
			"session_id": "s-1",
		})
		if err != nil {
			t.Fatalf("failed to create req: %v", err)
		}
		rec := httptest.NewRecorder()
		h.PostVoiceInteract(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
		}
		if capturedTimings != (voice.EdgeTimings{}) {
			t.Errorf("expected zero EdgeTimings, got %+v", capturedTimings)
		}
	})
}


