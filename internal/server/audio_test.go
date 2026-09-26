package server_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azylman/mirrormere/internal/api"
	"github.com/azylman/mirrormere/internal/server"
)

type mockAudioCoord struct {
	volume    int
	muted     bool
	setVolErr error
	setMutErr error
}

func (m *mockAudioCoord) GetState() (int, bool) {
	return m.volume, m.muted
}

func (m *mockAudioCoord) SetVolume(vol int) (int, bool, error) {
	if m.setVolErr != nil {
		return 0, false, m.setVolErr
	}
	m.volume = vol
	return m.volume, m.muted, nil
}

func (m *mockAudioCoord) SetMute(muted *bool) (int, bool, error) {
	if m.setMutErr != nil {
		return 0, false, m.setMutErr
	}
	if muted == nil {
		m.muted = !m.muted
	} else {
		m.muted = *muted
	}
	return m.volume, m.muted, nil
}

func (m *mockAudioCoord) ToggleMute() (int, bool, error) {
	return m.SetMute(nil)
}

func TestDefaultAudioHandler_GetAudio(t *testing.T) {
	t.Parallel()

	coord := &mockAudioCoord{volume: 75, muted: false}
	h := server.NewDefaultAudioHandler(coord)

	// 200 OK
	req := httptest.NewRequest(http.MethodGet, "/api/audio", nil)
	rec := httptest.NewRecorder()
	h.GetAudio(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp api.AudioStateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Volume != 75 || resp.Muted != false || resp.Status != "ok" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("missing CORS header")
	}

	// HEAD method
	reqHead := httptest.NewRequest(http.MethodHead, "/api/audio", nil)
	recHead := httptest.NewRecorder()
	h.GetAudio(recHead, reqHead)
	if recHead.Code != http.StatusOK {
		t.Fatalf("expected 200 for HEAD, got %d", recHead.Code)
	}
	if recHead.Body.Len() != 0 {
		t.Fatalf("expected empty body for HEAD, got %d bytes", recHead.Body.Len())
	}

	// OPTIONS method
	reqOpt := httptest.NewRequest(http.MethodOptions, "/api/audio", nil)
	recOpt := httptest.NewRecorder()
	h.GetAudio(recOpt, reqOpt)
	if recOpt.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for OPTIONS, got %d", recOpt.Code)
	}

	// 405 Method Not Allowed
	reqPost := httptest.NewRequest(http.MethodPost, "/api/audio", nil)
	recPost := httptest.NewRecorder()
	h.GetAudio(recPost, reqPost)
	if recPost.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", recPost.Code)
	}

	// 500 Nil Coordinator
	hNil := server.NewDefaultAudioHandler(nil)
	reqNil := httptest.NewRequest(http.MethodGet, "/api/audio", nil)
	recNil := httptest.NewRecorder()
	hNil.GetAudio(recNil, reqNil)
	if recNil.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for nil coordinator, got %d", recNil.Code)
	}
}

func TestDefaultAudioHandler_PostAudioVolume(t *testing.T) {
	t.Parallel()

	coord := &mockAudioCoord{volume: 75, muted: false}
	h := server.NewDefaultAudioHandler(coord)

	// 200 OK
	req := httptest.NewRequest(http.MethodPost, "/api/audio/volume", strings.NewReader(`{"volume":80}`))
	rec := httptest.NewRecorder()
	h.PostAudioVolume(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp api.AudioStateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Volume != 80 {
		t.Fatalf("expected volume 80, got %d", resp.Volume)
	}

	// OPTIONS method
	reqOpt := httptest.NewRequest(http.MethodOptions, "/api/audio/volume", nil)
	recOpt := httptest.NewRecorder()
	h.PostAudioVolume(recOpt, reqOpt)
	if recOpt.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for OPTIONS, got %d", recOpt.Code)
	}

	// 405 Method Not Allowed
	reqGet := httptest.NewRequest(http.MethodGet, "/api/audio/volume", nil)
	recGet := httptest.NewRecorder()
	h.PostAudioVolume(recGet, reqGet)
	if recGet.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", recGet.Code)
	}

	// 500 Nil Coordinator
	hNil := server.NewDefaultAudioHandler(nil)
	reqNil := httptest.NewRequest(http.MethodPost, "/api/audio/volume", strings.NewReader(`{"volume":50}`))
	recNil := httptest.NewRecorder()
	hNil.PostAudioVolume(recNil, reqNil)
	if recNil.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for nil coordinator, got %d", recNil.Code)
	}

	// 400 Bad Request: missing volume (empty JSON)
	reqEmpty := httptest.NewRequest(http.MethodPost, "/api/audio/volume", strings.NewReader(`{}`))
	recEmpty := httptest.NewRecorder()
	h.PostAudioVolume(recEmpty, reqEmpty)
	if recEmpty.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty body, got %d", recEmpty.Code)
	}

	// 400 Bad Request: negative volume
	reqNeg := httptest.NewRequest(http.MethodPost, "/api/audio/volume", strings.NewReader(`{"volume":-10}`))
	recNeg := httptest.NewRecorder()
	h.PostAudioVolume(recNeg, reqNeg)
	if recNeg.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for negative volume, got %d", recNeg.Code)
	}

	// 400 Bad Request: volume > 100
	reqOOB := httptest.NewRequest(http.MethodPost, "/api/audio/volume", strings.NewReader(`{"volume":105}`))
	recOOB := httptest.NewRecorder()
	h.PostAudioVolume(recOOB, reqOOB)
	if recOOB.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for volume > 100, got %d", recOOB.Code)
	}

	// 400 Bad Request: float volume
	reqFloat := httptest.NewRequest(http.MethodPost, "/api/audio/volume", strings.NewReader(`{"volume":75.5}`))
	recFloat := httptest.NewRecorder()
	h.PostAudioVolume(recFloat, reqFloat)
	if recFloat.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for float volume, got %d", recFloat.Code)
	}

	// 400 Bad Request: invalid JSON
	reqInvalid := httptest.NewRequest(http.MethodPost, "/api/audio/volume", strings.NewReader(`{invalid`))
	recInvalid := httptest.NewRecorder()
	h.PostAudioVolume(recInvalid, reqInvalid)
	if recInvalid.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid JSON, got %d", recInvalid.Code)
	}

	// 400 Bad Request: coordinator returns error
	coordErr := &mockAudioCoord{setVolErr: errors.New("underlying failure")}
	hCoordErr := server.NewDefaultAudioHandler(coordErr)
	reqCoordErr := httptest.NewRequest(http.MethodPost, "/api/audio/volume", strings.NewReader(`{"volume":50}`))
	recCoordErr := httptest.NewRecorder()
	hCoordErr.PostAudioVolume(recCoordErr, reqCoordErr)
	if recCoordErr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for coordinator error, got %d", recCoordErr.Code)
	}
}

func TestDefaultAudioHandler_PostAudioMute(t *testing.T) {
	t.Parallel()

	coord := &mockAudioCoord{volume: 75, muted: false}
	h := server.NewDefaultAudioHandler(coord)

	// 200 OK: Toggle mute with empty JSON
	reqToggle := httptest.NewRequest(http.MethodPost, "/api/audio/mute", strings.NewReader(`{}`))
	recToggle := httptest.NewRecorder()
	h.PostAudioMute(recToggle, reqToggle)
	if recToggle.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recToggle.Code)
	}
	var respToggle api.AudioStateResponse
	if err := json.Unmarshal(recToggle.Body.Bytes(), &respToggle); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !respToggle.Muted {
		t.Fatalf("expected muted true after toggle, got false")
	}

	// 200 OK: Toggle mute with empty body (EOF)
	reqEOF := httptest.NewRequest(http.MethodPost, "/api/audio/mute", strings.NewReader(``))
	recEOF := httptest.NewRecorder()
	h.PostAudioMute(recEOF, reqEOF)
	if recEOF.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recEOF.Code)
	}
	var respEOF api.AudioStateResponse
	_ = json.Unmarshal(recEOF.Body.Bytes(), &respEOF)
	if respEOF.Muted {
		t.Fatalf("expected muted false after second toggle, got true")
	}

	// 200 OK: Explicit mute true
	reqTrue := httptest.NewRequest(http.MethodPost, "/api/audio/mute", strings.NewReader(`{"muted":true}`))
	recTrue := httptest.NewRecorder()
	h.PostAudioMute(recTrue, reqTrue)
	if recTrue.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recTrue.Code)
	}
	var respTrue api.AudioStateResponse
	_ = json.Unmarshal(recTrue.Body.Bytes(), &respTrue)
	if !respTrue.Muted {
		t.Fatalf("expected muted true, got false")
	}

	// 200 OK: Explicit mute false
	reqFalse := httptest.NewRequest(http.MethodPost, "/api/audio/mute", strings.NewReader(`{"muted":false}`))
	recFalse := httptest.NewRecorder()
	h.PostAudioMute(recFalse, reqFalse)
	if recFalse.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recFalse.Code)
	}
	var respFalse api.AudioStateResponse
	_ = json.Unmarshal(recFalse.Body.Bytes(), &respFalse)
	if respFalse.Muted {
		t.Fatalf("expected muted false, got true")
	}

	// OPTIONS method
	reqOpt := httptest.NewRequest(http.MethodOptions, "/api/audio/mute", nil)
	recOpt := httptest.NewRecorder()
	h.PostAudioMute(recOpt, reqOpt)
	if recOpt.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for OPTIONS, got %d", recOpt.Code)
	}

	// 405 Method Not Allowed
	reqGet := httptest.NewRequest(http.MethodGet, "/api/audio/mute", nil)
	recGet := httptest.NewRecorder()
	h.PostAudioMute(recGet, reqGet)
	if recGet.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", recGet.Code)
	}

	// 500 Nil Coordinator
	hNil := server.NewDefaultAudioHandler(nil)
	reqNil := httptest.NewRequest(http.MethodPost, "/api/audio/mute", strings.NewReader(`{"muted":true}`))
	recNil := httptest.NewRecorder()
	hNil.PostAudioMute(recNil, reqNil)
	if recNil.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for nil coordinator, got %d", recNil.Code)
	}

	// 400 Bad Request: string muted
	reqString := httptest.NewRequest(http.MethodPost, "/api/audio/mute", strings.NewReader(`{"muted":"true"}`))
	recString := httptest.NewRecorder()
	h.PostAudioMute(recString, reqString)
	if recString.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for string muted, got %d", recString.Code)
	}

	// 400 Bad Request: null muted
	reqNull := httptest.NewRequest(http.MethodPost, "/api/audio/mute", strings.NewReader(`{"muted":null}`))
	recNull := httptest.NewRecorder()
	h.PostAudioMute(recNull, reqNull)
	if recNull.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for null muted, got %d", recNull.Code)
	}

	// 400 Bad Request: invalid JSON
	reqInvalid := httptest.NewRequest(http.MethodPost, "/api/audio/mute", strings.NewReader(`{invalid`))
	recInvalid := httptest.NewRecorder()
	h.PostAudioMute(recInvalid, reqInvalid)
	if recInvalid.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid JSON, got %d", recInvalid.Code)
	}

	// 500 InternalServerError: coordinator failure
	coordErr := &mockAudioCoord{setMutErr: errors.New("underlying failure")}
	hCoordErr := server.NewDefaultAudioHandler(coordErr)
	reqCoordErr := httptest.NewRequest(http.MethodPost, "/api/audio/mute", strings.NewReader(`{}`))
	recCoordErr := httptest.NewRecorder()
	hCoordErr.PostAudioMute(recCoordErr, reqCoordErr)
	if recCoordErr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for coordinator error, got %d", recCoordErr.Code)
	}
}

func TestServer_AudioRoutes(t *testing.T) {
	t.Parallel()

	coord := &mockAudioCoord{volume: 75, muted: false}
	h := server.NewDefaultAudioHandler(coord)

	srv := server.New(server.Config{
		AudioHandler: h,
	})

	if srv.AudioHandler() != h {
		t.Fatalf("expected srv.AudioHandler() to return registered handler")
	}

	// GET /api/audio
	reqGet := httptest.NewRequest(http.MethodGet, "/api/audio", nil)
	recGet := httptest.NewRecorder()
	srv.Routes().ServeHTTP(recGet, reqGet)
	if recGet.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recGet.Code)
	}

	// POST /api/audio/volume
	reqVol := httptest.NewRequest(http.MethodPost, "/api/audio/volume", strings.NewReader(`{"volume":85}`))
	recVol := httptest.NewRecorder()
	srv.Routes().ServeHTTP(recVol, reqVol)
	if recVol.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recVol.Code)
	}

	// POST /api/audio/mute
	reqMute := httptest.NewRequest(http.MethodPost, "/api/audio/mute", strings.NewReader(`{"muted":true}`))
	recMute := httptest.NewRecorder()
	srv.Routes().ServeHTTP(recMute, reqMute)
	if recMute.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recMute.Code)
	}

	// Dynamic registration
	coord2 := &mockAudioCoord{volume: 40, muted: true}
	h2 := server.NewDefaultAudioHandler(coord2)
	srv.RegisterAudioHandler(h2)
	if srv.AudioHandler() != h2 {
		t.Fatalf("expected srv.AudioHandler() to return h2")
	}

	// 404 when AudioHandler is nil
	srvNil := server.New(server.Config{})
	recNil := httptest.NewRecorder()
	srvNil.Routes().ServeHTTP(recNil, reqGet)
	if recNil.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when audioHandler is nil, got %d", recNil.Code)
	}
	recNilVol := httptest.NewRecorder()
	srvNil.Routes().ServeHTTP(recNilVol, reqVol)
	if recNilVol.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when audioHandler is nil, got %d", recNilVol.Code)
	}
	recNilMute := httptest.NewRecorder()
	srvNil.Routes().ServeHTTP(recNilMute, reqMute)
	if recNilMute.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when audioHandler is nil, got %d", recNilMute.Code)
	}
}
