package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunWithReady(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	_ = os.WriteFile(configPath, []byte("server:\n  host: 127.0.0.1\n  port: 8095\nhomeassistant:\n  url: http://127.0.0.1:8123\n"), 0600)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	defer ln.Close()
	collideConfig := filepath.Join(tmpDir, "collide.yaml")
	_ = os.WriteFile(collideConfig, []byte(fmt.Sprintf("server:\n  host: 127.0.0.1\n  port: %d\nhomeassistant:\n  url: http://127.0.0.1:8123\n", port)), 0600)

	tests := []struct {
		name        string
		args        []string
		useListener bool
		probeHealth bool
		mockClient  bool
		wantErrSub  string
	}{
		{name: "help", args: []string{"--help"}},
		{name: "config_error", args: []string{"-config", "/nonexistent/config.yaml"}, wantErrSub: "failed to load configuration"},
		{name: "lifecycle_probe", args: []string{"--config=" + configPath}, useListener: true, probeHealth: true},
		{name: "flag_config_equals", args: []string{"-config=" + configPath}, useListener: true, mockClient: true},
		{name: "flag_positional", args: []string{configPath}, useListener: true, mockClient: true},
		{name: "listen_collision", args: []string{"-config", collideConfig}, mockClient: true, wantErrSub: "failed to listen"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			var listener net.Listener
			if tc.useListener {
				listener, _ = net.Listen("tcp", "127.0.0.1:0")
			}
			var client HAClient
			if tc.mockClient {
				client = &mockHAClient{}
			}
			ctx, cancel := context.WithCancel(context.Background())
			readyCh, errCh := make(chan struct{}), make(chan error, 1)
			go func() {
				errCh <- RunWithReady(ctx, tc.args, &stdout, &stderr, readyCh, listener, client)
			}()
			if tc.useListener {
				select {
				case <-readyCh:
					if tc.probeHealth {
						resp, err := http.Get("http://" + listener.Addr().String() + "/healthz")
						if err != nil || resp.StatusCode != http.StatusOK {
							t.Fatalf("probe /healthz err=%v status=%d", err, resp.StatusCode)
						}
						_ = resp.Body.Close()
					}
					cancel()
				case <-time.After(3 * time.Second):
					cancel()
					t.Fatal("timed out waiting for readyCh")
				}
			} else {
				cancel()
			}
			select {
			case err := <-errCh:
				if tc.wantErrSub != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErrSub)) {
					t.Fatalf("err=%v, want substring %q", err, tc.wantErrSub)
				} else if tc.wantErrSub == "" && err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("timed out waiting for RunWithReady to exit")
			}
		})
	}
}

func TestMain_Execution(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode int
	}{
		{"help_success", []string{"--help"}, 0},
		{"config_error", []string{"-config", "/nonexistent/never_exists.yaml"}, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			oldArgs, oldExit := argsHook, exitFunc
			defer func() { argsHook = oldArgs; exitFunc = oldExit }()
			argsHook = tc.args
			exitCode := 0
			exitFunc = func(code int) { exitCode = code }
			main()
			if exitCode != tc.wantCode {
				t.Errorf("exitCode=%d, want %d", exitCode, tc.wantCode)
			}
		})
	}
}
