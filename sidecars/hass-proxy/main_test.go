package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/testutil"
)

type runWithReadyCase struct {
	name        string
	args        []string
	useListener bool
	probeHealth bool
	mockClient  bool
	failStdout  bool
	wantErrSub  string
}

func probeReadyListener(t *testing.T, listener net.Listener, probeHealth bool, readyCh <-chan struct{}, cancel context.CancelFunc) {
	t.Helper()
	select {
	case <-readyCh:
		if probeHealth {
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
}

func assertRunWithReadyResult(t *testing.T, tc runWithReadyCase, errCh <-chan error) {
	t.Helper()
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
}

func runRunWithReadyCase(t *testing.T, tc runWithReadyCase) {
	t.Helper()
	var stdout io.Writer = &bytes.Buffer{}
	if tc.failStdout {
		stdout = &testutil.FailingFlusher{}
	}
	var stderr bytes.Buffer
	var listener net.Listener
	if tc.useListener {
		var err error
		listener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
	}
	var client HAClient
	if tc.mockClient {
		client = &mockHAClient{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	readyCh, errCh := make(chan struct{}), make(chan error, 1)
	go func() {
		errCh <- RunWithReady(ctx, tc.args, stdout, &stderr, readyCh, listener, client)
	}()
	if tc.useListener {
		probeReadyListener(t, listener, tc.probeHealth, readyCh, cancel)
	} else {
		cancel()
	}
	assertRunWithReadyResult(t, tc, errCh)
}

func TestRunWithReady(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("server:\n  host: 127.0.0.1\n  port: 8095\nhomeassistant:\n  url: http://127.0.0.1:8123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := testutil.As[*net.TCPAddr](t, ln.Addr()).Port
	defer ln.Close()
	collideConfig := filepath.Join(tmpDir, "collide.yaml")
	if err := os.WriteFile(collideConfig, []byte(fmt.Sprintf("server:\n  host: 127.0.0.1\n  port: %d\nhomeassistant:\n  url: http://127.0.0.1:8123\n", port)), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []runWithReadyCase{
		{name: "help", args: []string{"--help"}},
		{name: "config_error", args: []string{"-config", "/nonexistent/config.yaml"}, wantErrSub: "failed to load configuration"},
		{name: "lifecycle_probe", args: []string{"--config=" + configPath}, useListener: true, probeHealth: true},
		{name: "flag_config_equals", args: []string{"-config=" + configPath}, useListener: true, mockClient: true},
		{name: "flag_positional", args: []string{configPath}, useListener: true, mockClient: true},
		{name: "stdout_write_failure_continues", args: []string{configPath}, useListener: true, mockClient: true, failStdout: true},
		{name: "listen_collision", args: []string{"-config", collideConfig}, mockClient: true, wantErrSub: "failed to listen"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runRunWithReadyCase(t, tc)
		})
	}
}

func TestRunMain(t *testing.T) {
	tests := []struct {
		name, wantErr string
		args          []string
	}{
		{name: "help", args: []string{"--help"}},
		{name: "config_error", args: []string{"-config", "/nonexistent/never_exists.yaml"}, wantErr: "failed to load configuration"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := run(tc.args)
			if (err == nil) != (tc.wantErr == "") || (err != nil && !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("run(%q) error = %v, want %q", tc.args, err, tc.wantErr)
			}
		})
	}
}
