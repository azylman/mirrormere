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

	"github.com/azylman/mirrormere/pkg/server"
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
