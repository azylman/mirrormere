package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/server"
)

func TestConfig_ApplyDefaults(t *testing.T) {
	t.Parallel()

	// Case 1: Empty config applies all defaults
	var emptyCfg server.Config
	emptyCfg.ApplyDefaults()

	if emptyCfg.Host != server.DefaultHost {
		t.Errorf("expected Host %s, got %s", server.DefaultHost, emptyCfg.Host)
	}
	if emptyCfg.Port != server.DefaultPort {
		t.Errorf("expected Port %d, got %d", server.DefaultPort, emptyCfg.Port)
	}
	if emptyCfg.ReadTimeout != server.DefaultReadTimeout {
		t.Errorf("expected ReadTimeout %v, got %v", server.DefaultReadTimeout, emptyCfg.ReadTimeout)
	}
	if emptyCfg.ReadHeaderTimeout != server.DefaultReadHeaderTimeout {
		t.Errorf("expected ReadHeaderTimeout %v, got %v", server.DefaultReadHeaderTimeout, emptyCfg.ReadHeaderTimeout)
	}
	if emptyCfg.WriteTimeout != server.DefaultWriteTimeout {
		t.Errorf("expected WriteTimeout %v, got %v", server.DefaultWriteTimeout, emptyCfg.WriteTimeout)
	}
	if emptyCfg.IdleTimeout != server.DefaultIdleTimeout {
		t.Errorf("expected IdleTimeout %v, got %v", server.DefaultIdleTimeout, emptyCfg.IdleTimeout)
	}

	// Case 2: Negative port maps to ephemeral 0
	ephemeralCfg := server.Config{Port: -1}
	ephemeralCfg.ApplyDefaults()
	if ephemeralCfg.Port != 0 {
		t.Errorf("expected Port 0 for ephemeral, got %d", ephemeralCfg.Port)
	}

	// Case 3: Custom config preserves existing values
	customCfg := server.Config{
		Host:              "127.0.0.1",
		Port:              9090,
		ReadTimeout:       1 * time.Second,
		ReadHeaderTimeout: 2 * time.Second,
		WriteTimeout:      3 * time.Second,
		IdleTimeout:       4 * time.Second,
	}
	customCfg.ApplyDefaults()

	if customCfg.Host != "127.0.0.1" || customCfg.Port != 9090 {
		t.Errorf("expected custom host/port preserved, got %s:%d", customCfg.Host, customCfg.Port)
	}
	if customCfg.ReadTimeout != 1*time.Second || customCfg.ReadHeaderTimeout != 2*time.Second {
		t.Errorf("expected custom timeouts preserved")
	}
}

func TestHealthEndpoints(t *testing.T) {
	t.Parallel()

	srv := server.New(server.Config{})
	handler := srv.Routes()

	paths := []string{"/healthz", "/health"}

	for _, path := range paths {
		t.Run("GET_"+path, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d", rec.Code)
			}
			if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
				t.Errorf("expected CORS origin '*', got %q", rec.Header().Get("Access-Control-Allow-Origin"))
			}

			var resp server.HealthResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("failed to parse JSON response: %v", err)
			}
			if resp.Status != "ok" {
				t.Errorf("expected status 'ok', got %q", resp.Status)
			}
		})

		t.Run("HEAD_"+path, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodHead, path, nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d", rec.Code)
			}
			if rec.Body.Len() != 0 {
				t.Errorf("expected empty body for HEAD, got %d bytes", rec.Body.Len())
			}
		})

		t.Run("OPTIONS_"+path, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodOptions, path, nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusNoContent {
				t.Fatalf("expected status 204, got %d", rec.Code)
			}
			if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
				t.Errorf("expected CORS origin '*'")
			}
			if rec.Header().Get("Access-Control-Allow-Methods") == "" {
				t.Errorf("expected CORS methods set")
			}
		})

		t.Run("POST_MethodNotAllowed_"+path, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodPost, path, nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("expected status 405, got %d", rec.Code)
			}
		})
	}
}

func TestRootEndpoint(t *testing.T) {
	t.Parallel()

	srv := server.New(server.Config{})
	handler := srv.Routes()

	t.Run("GET_Root", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}

		var resp server.RootResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if resp.Service != "mirrormere" || resp.Status != "running" || resp.Version != server.Version {
			t.Errorf("unexpected root payload: %+v", resp)
		}
	})

	t.Run("HEAD_Root", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodHead, "/", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
		if rec.Body.Len() != 0 {
			t.Errorf("expected empty body for HEAD")
		}
	})

	t.Run("OPTIONS_Root", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodOptions, "/", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected status 204, got %d", rec.Code)
		}
	})

	t.Run("POST_Root_MethodNotAllowed", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected status 405, got %d", rec.Code)
		}
	})

	t.Run("NotFound_UnknownPath", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "/unknown/path", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected status 404, got %d", rec.Code)
		}
	})
}

func TestServer_ServeAndShutdown(t *testing.T) {
	t.Parallel()

	srv := server.New(server.Config{
		Host: "127.0.0.1",
		Port: -1,
	})

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}

	errChan := make(chan error, 1)
	go func() {
		errChan <- srv.Serve(l)
	}()

	select {
	case <-srv.Ready():
	case err := <-errChan:
		t.Fatalf("Serve failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("server timed out waiting to become ready")
	}

	addr := srv.Addr()
	if addr == "" {
		t.Fatal("expected non-empty bound address")
	}

	resp, err := http.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatalf("HTTP GET failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", resp.StatusCode, string(body))
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("server shutdown failed: %v", err)
	}

	select {
	case serveErr := <-errChan:
		if serveErr != nil {
			t.Fatalf("server returned error on shutdown: %v", serveErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for server to stop")
	}
}

func TestServer_ListenAndServe_BindError(t *testing.T) {
	t.Parallel()

	// Use an invalid host IP to trigger net.Listen error deterministically
	srv := server.New(server.Config{
		Host: "999.999.999.999",
		Port: 8080,
	})

	err := srv.ListenAndServe()
	if err == nil {
		t.Fatal("expected error binding to invalid IP address, got nil")
	}
}

func TestServer_ListenAndServe_Success(t *testing.T) {
	t.Parallel()

	srv := server.New(server.Config{
		Host: "127.0.0.1",
		Port: -1, // Negative port binds ephemeral port 0
	})

	errChan := make(chan error, 1)
	go func() {
		errChan <- srv.ListenAndServe()
	}()

	select {
	case <-srv.Ready():
	case err := <-errChan:
		t.Fatalf("ListenAndServe failed immediately: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("server timed out waiting to become ready")
	}

	addr := srv.Addr()
	if addr == "" {
		t.Fatal("expected bound address to be populated")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("server shutdown failed: %v", err)
	}

	select {
	case err := <-errChan:
		if err != nil {
			t.Fatalf("ListenAndServe returned unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not shut down cleanly")
	}
}

func TestServer_EventsHandler(t *testing.T) {
	t.Parallel()

	// Case 1: Unset EventsHandler returns 404
	srv := server.New(server.Config{})
	if srv.EventsHandler() != nil {
		t.Fatal("expected nil EventsHandler initially")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when EventsHandler unset, got %d", rec.Code)
	}

	// Case 2: Config.EventsHandler set at creation
	mockHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: test\ndata: ok\n\n"))
	})

	srvWithHandler := server.New(server.Config{EventsHandler: mockHandler})
	if srvWithHandler.EventsHandler() == nil {
		t.Fatal("expected non-nil EventsHandler")
	}

	rec2 := httptest.NewRecorder()
	srvWithHandler.Routes().ServeHTTP(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 from configured EventsHandler, got %d", rec2.Code)
	}
	if rec2.Body.String() != "event: test\ndata: ok\n\n" {
		t.Fatalf("unexpected body: %q", rec2.Body.String())
	}

	// Case 3: RegisterEventsHandler dynamically updates handler
	mockHandler2 := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	srv.RegisterEventsHandler(mockHandler2)
	if srv.EventsHandler() == nil {
		t.Fatal("expected non-nil EventsHandler after registration")
	}

	rec3 := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec3, req)
	if rec3.Code != http.StatusTeapot {
		t.Fatalf("expected 418 from dynamically registered EventsHandler, got %d", rec3.Code)
	}
}

type mockScreenHandler struct {
	selectCalls  int
	advanceCalls int
	pauseCalls   int
}

func (m *mockScreenHandler) PostScreenSelect(w http.ResponseWriter, r *http.Request) {
	m.selectCalls++
	w.WriteHeader(http.StatusOK)
}

func (m *mockScreenHandler) PostScreenAdvance(w http.ResponseWriter, r *http.Request) {
	m.advanceCalls++
	w.WriteHeader(http.StatusOK)
}

func (m *mockScreenHandler) PostScreenPause(w http.ResponseWriter, r *http.Request) {
	m.pauseCalls++
	w.WriteHeader(http.StatusOK)
}

func TestServer_ScreenHandler(t *testing.T) {
	t.Parallel()

	// Case 1: Unset ScreenHandler returns 404
	srv := server.New(server.Config{})
	if srv.ScreenHandler() != nil {
		t.Fatal("expected nil ScreenHandler initially")
	}

	endpoints := []string{"/api/screen/select", "/api/screen/advance", "/api/screen/pause"}
	for _, ep := range endpoints {
		req := httptest.NewRequest(http.MethodPost, ep, nil)
		rec := httptest.NewRecorder()
		srv.Routes().ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for %s when ScreenHandler unset, got %d", ep, rec.Code)
		}
	}

	// Case 2: Config.ScreenHandler set at creation
	mockH := &mockScreenHandler{}
	srvWithHandler := server.New(server.Config{ScreenHandler: mockH})
	if srvWithHandler.ScreenHandler() == nil {
		t.Fatal("expected non-nil ScreenHandler")
	}

	for _, ep := range endpoints {
		req := httptest.NewRequest(http.MethodPost, ep, nil)
		rec := httptest.NewRecorder()
		srvWithHandler.Routes().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 for %s, got %d", ep, rec.Code)
		}
	}

	if mockH.selectCalls != 1 || mockH.advanceCalls != 1 || mockH.pauseCalls != 1 {
		t.Errorf("unexpected call counts: select=%d advance=%d pause=%d",
			mockH.selectCalls, mockH.advanceCalls, mockH.pauseCalls)
	}

	// Case 3: RegisterScreenHandler dynamically updates handler
	mockH2 := &mockScreenHandler{}
	srv.RegisterScreenHandler(mockH2)
	if srv.ScreenHandler() == nil {
		t.Fatal("expected non-nil ScreenHandler after registration")
	}

	req := httptest.NewRequest(http.MethodPost, "/api/screen/select", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 after dynamic registration, got %d", rec.Code)
	}
	if mockH2.selectCalls != 1 {
		t.Errorf("expected mockH2 to receive call, got %d", mockH2.selectCalls)
	}
}
