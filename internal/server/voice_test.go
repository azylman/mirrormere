package server_test

import (
	"encoding/json"
	"errors"
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

func TestDefaultVoiceHandler_PostVoiceState(t *testing.T) {
	t.Parallel()

	t.Run("OPTIONS returns 204 No Content", func(t *testing.T) {
		t.Parallel()
		coord := &mockVoiceCoord{}
		h := server.NewDefaultVoiceHandler(coord)

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
		h := server.NewDefaultVoiceHandler(coord)

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
		h := server.NewDefaultVoiceHandler(nil)

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
		h := server.NewDefaultVoiceHandler(coord)

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
		h := server.NewDefaultVoiceHandler(coord)

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
		h := server.NewDefaultVoiceHandler(coord)

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
		h := server.NewDefaultVoiceHandler(coord)

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
		h := server.NewDefaultVoiceHandler(coord)

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
