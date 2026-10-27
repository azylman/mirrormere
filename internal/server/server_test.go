package server_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/server"
)

func doServerReq(srv *server.Server, method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	return rec
}

func TestConfig_ApplyDefaults(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		in       server.Config
		wantHost string
		wantPort int
		wantRead time.Duration
	}{
		{"defaults", server.Config{}, server.DefaultHost, server.DefaultPort, server.DefaultReadTimeout},
		{"negative_port", server.Config{Port: -1}, server.DefaultHost, 0, server.DefaultReadTimeout},
		{"custom", server.Config{Host: "127.0.0.1", Port: 9090, ReadTimeout: time.Second, ReadHeaderTimeout: 2 * time.Second, WriteTimeout: 3 * time.Second, IdleTimeout: 4 * time.Second}, "127.0.0.1", 9090, time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := tc.in
			cfg.ApplyDefaults()
			if cfg.Host != tc.wantHost || cfg.Port != tc.wantPort || cfg.ReadTimeout != tc.wantRead {
				t.Fatalf("ApplyDefaults() = %+v, want host=%s port=%d read=%v", cfg, tc.wantHost, tc.wantPort, tc.wantRead)
			}
			if tc.name == "custom" && (cfg.ReadHeaderTimeout != 2*time.Second || cfg.WriteTimeout != 3*time.Second || cfg.IdleTimeout != 4*time.Second) {
				t.Fatal("custom timeouts not preserved")
			}
		})
	}
}

func TestServer_CoreEndpoints(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		method      string
		path        string
		wantCode    int
		wantAllow   string
		wantBodySub string
	}{
		{"healthz_get", http.MethodGet, "/healthz", http.StatusOK, "", `"status":"ok"`},
		{"healthz_head", http.MethodHead, "/healthz", http.StatusOK, "", ""},
		{"healthz_options", http.MethodOptions, "/healthz", http.StatusNoContent, "", ""},
		{"healthz_post_405", http.MethodPost, "/healthz", http.StatusMethodNotAllowed, "GET, HEAD, OPTIONS", ""},
		{"health_get", http.MethodGet, "/health", http.StatusOK, "", `"status":"ok"`},
		{"health_head", http.MethodHead, "/health", http.StatusOK, "", ""},
		{"health_options", http.MethodOptions, "/health", http.StatusNoContent, "", ""},
		{"health_post_405", http.MethodPost, "/health", http.StatusMethodNotAllowed, "GET, HEAD, OPTIONS", ""},
		{"root_get", http.MethodGet, "/", http.StatusOK, "", `"service":"mirrormere"`},
		{"root_head", http.MethodHead, "/", http.StatusOK, "", ""},
		{"root_options", http.MethodOptions, "/", http.StatusNoContent, "", ""},
		{"root_post_405", http.MethodPost, "/", http.StatusMethodNotAllowed, "GET, HEAD, OPTIONS", ""},
		{"root_unknown_404", http.MethodGet, "/unknown/path", http.StatusNotFound, "", ""},
		{"metrics_get", http.MethodGet, "/metrics", http.StatusOK, "", "# HELP"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := server.New(server.Config{})
			rec := doServerReq(srv, tc.method, tc.path)
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantCode)
			}
			if tc.wantAllow != "" && rec.Header().Get("Allow") != tc.wantAllow {
				t.Errorf("Allow = %q, want %q", rec.Header().Get("Allow"), tc.wantAllow)
			}
			if tc.wantBodySub != "" && !strings.Contains(rec.Body.String(), tc.wantBodySub) {
				t.Errorf("body = %q, want %q", rec.Body.String(), tc.wantBodySub)
			}
			if tc.method == http.MethodHead && rec.Body.Len() != 0 {
				t.Errorf("expected empty body for HEAD, got %d bytes", rec.Body.Len())
			}
		})
	}
}

func TestCoverageFlushEndpoint(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		writeErr    error
		wantCode    int
		wantBodySub string
	}{
		{name: "method_not_allowed", method: http.MethodDelete, wantCode: http.StatusMethodNotAllowed},
		{name: "valid_flush_tar", method: http.MethodPost, wantCode: http.StatusOK, wantBodySub: "covcounters.test"},
		{name: "write_error_500", method: http.MethodGet, writeErr: errors.New("simulated flush error"), wantCode: http.StatusInternalServerError, wantBodySub: "simulated flush error"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defer server.SetWriteCountersDirForTest(func(dir string) error {
				if tc.writeErr != nil {
					return tc.writeErr
				}
				return os.WriteFile(filepath.Join(dir, "covcounters.test"), []byte("covdata"), 0o600)
			})()

			srv := server.New(server.Config{})
			rec := doServerReq(srv, tc.method, "/debug/coverage/flush")
			if rec.Code != tc.wantCode {
				t.Fatalf("code = %d, want %d", rec.Code, tc.wantCode)
			}
			if tc.wantBodySub != "" && !strings.Contains(rec.Body.String(), tc.wantBodySub) {
				t.Fatalf("body = %q, want %q", rec.Body.String(), tc.wantBodySub)
			}
		})
	}
}

func TestServer_Lifecycle(t *testing.T) {
	t.Parallel()
	srv := server.New(server.Config{Host: "127.0.0.1", Port: -1})
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen failed: %v", err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(l) }()

	select {
	case <-srv.Ready():
	case err := <-errCh:
		t.Fatalf("server failed to start: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("server start timed out")
	}

	if srv.Addr() == "" {
		t.Fatal("expected non-empty bound address")
	}

	resp, err := http.Get("http://" + srv.Addr() + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown failed: %v", err)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("server stopped with error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server shutdown timed out")
	}
}

type mockAll struct{ status int }

func (m *mockAll) w(w http.ResponseWriter, s string) {
	w.WriteHeader(m.status)
	_, _ = w.Write([]byte(s))
}

func (m *mockAll) ServeHTTP(w http.ResponseWriter, _ *http.Request)        { m.w(w, "events") }
func (m *mockAll) PostScreenSelect(w http.ResponseWriter, _ *http.Request) { m.w(w, "screen_select") }

func (m *mockAll) PostScreenAdvance(w http.ResponseWriter, _ *http.Request) { m.w(w, "screen_advance") }

func (m *mockAll) PostScreenPause(w http.ResponseWriter, _ *http.Request) { m.w(w, "screen_pause") }

func (m *mockAll) GetWidgetRender(w http.ResponseWriter, _ *http.Request, id string) {
	m.w(w, "render:"+id)
}

func (m *mockAll) GetWidgetAsset(w http.ResponseWriter, _ *http.Request, t, p string) {
	m.w(w, "asset:"+t+":"+p)
}
func (m *mockAll) ServeStyle(w http.ResponseWriter, _ *http.Request) { m.w(w, "style") }
func (m *mockAll) PostWidgetPush(w http.ResponseWriter, _ *http.Request, id string) {
	m.w(w, "push:"+id)
}
func (m *mockAll) GetDisplay(w http.ResponseWriter, _ *http.Request)          { m.w(w, "display") }
func (m *mockAll) GetStatic(w http.ResponseWriter, _ *http.Request, p string) { m.w(w, "static:"+p) }

func (m *mockAll) CompleteListItem(w http.ResponseWriter, _ *http.Request, l, i string) {
	m.w(w, "complete:"+l+":"+i)
}
func (m *mockAll) GetAudio(w http.ResponseWriter, _ *http.Request)         { m.w(w, "audio") }
func (m *mockAll) PostAudioVolume(w http.ResponseWriter, _ *http.Request)  { m.w(w, "volume") }
func (m *mockAll) PostAudioMute(w http.ResponseWriter, _ *http.Request)    { m.w(w, "mute") }
func (m *mockAll) PostVideoTrigger(w http.ResponseWriter, _ *http.Request) { m.w(w, "v_trigger") }
func (m *mockAll) PostVideoDismiss(w http.ResponseWriter, _ *http.Request) { m.w(w, "v_dismiss") }
func (m *mockAll) PostVideoAction(w http.ResponseWriter, _ *http.Request)  { m.w(w, "v_action") }
func (m *mockAll) PostVideoState(w http.ResponseWriter, _ *http.Request)   { m.w(w, "v_post_state") }

func (m *mockAll) GetVideoState(w http.ResponseWriter, _ *http.Request) { m.w(w, "v_get_state") }

func (m *mockAll) PostVoiceState(w http.ResponseWriter, _ *http.Request) { m.w(w, "voice_state") }

func (m *mockAll) PostVoiceInteract(w http.ResponseWriter, _ *http.Request) { m.w(w, "voice_interact") }

func (m *mockAll) PostVoiceHeartbeat(w http.ResponseWriter, _ *http.Request) {
	m.w(w, "voice_heartbeat")
}

func testHandlerFamily(fam string, m1 *mockAll) *server.Server {
	var c server.Config
	switch fam {
	case "events":
		c.EventsHandler = m1
	case "screen":
		c.ScreenHandler = m1
	case "render":
		c.RenderHandler = m1
	case "display":
		c.DisplayHandler = m1
	case "push":
		c.PushHandler = m1
	case "lists":
		c.ListsHandler = m1
	case "audio":
		c.AudioHandler = m1
	case "video":
		c.VideoHandler = m1
	case "voice":
		c.VoiceHandler = m1
	}
	return server.New(c)
}

func TestServer_HandlerDelegation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		family   string
		method   string
		path     string
		wantBody string
	}{
		{"events", "events", http.MethodGet, "/api/events", "events"},
		{"screen_select", "screen", http.MethodPost, "/api/screen/select", "screen_select"},
		{"screen_advance", "screen", http.MethodPost, "/api/screen/advance", "screen_advance"},
		{"screen_pause", "screen", http.MethodPost, "/api/screen/pause", "screen_pause"},
		{"render_widget", "render", http.MethodGet, "/api/widgets/sensor-1/render", "render:sensor-1"},
		{"render_asset", "render", http.MethodGet, "/widget-types/clock/assets/icon.png", "asset:clock:icon.png"},
		{"render_style", "render", http.MethodGet, "/style.css", "style"},
		{"display_main", "display", http.MethodGet, "/display", "display"},
		{"display_static", "display", http.MethodGet, "/static/bundle.js", "static:bundle.js"},
		{"push_webhook", "push", http.MethodPost, "/api/widgets/weather/push", "push:weather"},
		{"lists_complete", "lists", http.MethodPost, "/api/lists/groceries/items/i42/complete", "complete:groceries:i42"},
		{"audio_get", "audio", http.MethodGet, "/api/audio", "audio"},
		{"audio_volume", "audio", http.MethodPost, "/api/audio/volume", "volume"},
		{"audio_mute", "audio", http.MethodPost, "/api/audio/mute", "mute"},
		{"video_trigger", "video", http.MethodPost, "/api/video/trigger", "v_trigger"},
		{"video_dismiss", "video", http.MethodPost, "/api/video/dismiss", "v_dismiss"},
		{"video_action", "video", http.MethodPost, "/api/video/action", "v_action"},
		{"video_state_get", "video", http.MethodGet, "/api/video/state", "v_get_state"},
		{"video_state_post", "video", http.MethodPost, "/api/video/state", "v_post_state"},
		{"voice_state", "voice", http.MethodPost, "/api/voice/state", "voice_state"},
		{"voice_interact", "voice", http.MethodPost, "/api/voice/interact", "voice_interact"},
		{"voice_heartbeat", "voice", http.MethodPost, "/api/voice/heartbeat", "voice_heartbeat"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srvEmpty := server.New(server.Config{})
			if rec := doServerReq(srvEmpty, tc.method, tc.path); rec.Code != http.StatusNotFound {
				t.Fatalf("unset handler returned %d, want 404", rec.Code)
			}

			srvCfg := testHandlerFamily(tc.family, &mockAll{status: http.StatusOK})
			rec200 := doServerReq(srvCfg, tc.method, tc.path)
			if rec200.Code != http.StatusOK || !strings.Contains(rec200.Body.String(), tc.wantBody) {
				t.Fatalf("configured handler code=%d body=%q, want 200 with %q", rec200.Code, rec200.Body.String(), tc.wantBody)
			}
		})
	}
}
