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
	interactFn func(ctx context.Context, audio io.Reader, nodeID, sessionID string, sink voice.SSEEventSink) error
}

func (m *mockVoiceHub) IsEnabled() bool {
	return m.enabled
}

func (m *mockVoiceHub) Interact(ctx context.Context, audio io.Reader, nodeID, sessionID string, sink voice.SSEEventSink) error {
	if m.interactFn != nil {
		return m.interactFn(ctx, audio, nodeID, sessionID, sink)
	}
	return nil
}

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

	t.Run("invalid state enum returns 400 Bad Request with SPEC-006 error message", func(t *testing.T) {
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
}

func TestDefaultVoiceHandler_PostVoiceInteract(t *testing.T) {
	t.Parallel()

	dummyWAV := append([]byte("RIFF1234WAVEfmt \x10\x00\x00\x00\x01\x00\x01\x00\x80>\x00\x00\x00}\x00\x00\x02\x00\x10\x00data\x00\x00\x00\x00"), make([]byte, 100)...)

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
			interactFn: func(ctx context.Context, audio io.Reader, nodeID, sessionID string, sink voice.SSEEventSink) error {
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
			interactFn: func(ctx context.Context, audio io.Reader, nodeID, sessionID string, sink voice.SSEEventSink) error {
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
			interactFn: func(ctx context.Context, audio io.Reader, nodeID, sessionID string, sink voice.SSEEventSink) error {
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
			interactFn: func(ctx context.Context, audio io.Reader, nodeID, sessionID string, sink voice.SSEEventSink) error {
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
			interactFn: func(ctx context.Context, audio io.Reader, nodeID, sessionID string, sink voice.SSEEventSink) error {
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
				if err := sink("audio_chunk", map[string]any{"chunk_index": 0, "audio": "base64audio"}); err != nil {
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
			interactFn: func(ctx context.Context, audio io.Reader, nodeID, sessionID string, sink voice.SSEEventSink) error {
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
}
