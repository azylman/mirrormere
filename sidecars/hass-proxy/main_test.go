package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRun_Help(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"--help"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error on --help: %v", err)
	}
	if !strings.Contains(stderr.String(), "Usage: hass-proxy") {
		t.Errorf("expected usage message, got %q", stderr.String())
	}
}

func TestRun_ConfigError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"-config", "/nonexistent/config.yaml"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error on nonexistent config, got nil")
	}
}

func TestRunWithReady_Lifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	content := `
server:
  host: "127.0.0.1"
  port: 8095

homeassistant:
  url: "http://127.0.0.1:8123"
`
	if err := os.WriteFile(configPath, []byte(content), 0600); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind test listener: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	readyCh := make(chan struct{})

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- RunWithReady(
			ctx,
			[]string{"--config=" + configPath},
			&bytes.Buffer{},
			&bytes.Buffer{},
			readyCh,
			listener,
			nil, // tests clientOverride == nil branch
		)
	}()

	select {
	case <-readyCh:
		// Server is running and listening! Let's probe /healthz
		resp, err := http.Get("http://" + listener.Addr().String() + "/healthz")
		if err != nil {
			t.Fatalf("failed to query /healthz on active server: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200 from /healthz, got %d", resp.StatusCode)
		}

		// Trigger shutdown
		cancel()
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("timed out waiting for server to be ready")
	}

	select {
	case err := <-runErrCh:
		if err != nil {
			t.Fatalf("unexpected error during RunWithReady shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for RunWithReady to exit after cancellation")
	}
}

func TestRunWithReady_FlagVariants(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	content := `
server:
  host: "127.0.0.1"
  port: 8095
homeassistant:
  url: "http://127.0.0.1:8123"
`
	if err := os.WriteFile(configPath, []byte(content), 0600); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	// Test -config=path variant
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind listener: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	readyCh := make(chan struct{})

	go func() {
		_ = RunWithReady(
			ctx,
			[]string{"-config=" + configPath},
			&bytes.Buffer{},
			&bytes.Buffer{},
			readyCh,
			listener,
			&mockHAClient{},
		)
	}()

	<-readyCh
	cancel()

	// Test positional arg variant
	listener2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind listener: %v", err)
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	readyCh2 := make(chan struct{})

	go func() {
		_ = RunWithReady(
			ctx2,
			[]string{configPath},
			&bytes.Buffer{},
			&bytes.Buffer{},
			readyCh2,
			listener2,
			&mockHAClient{},
		)
	}()

	<-readyCh2
	cancel2()
}

func TestRunWithReady_ListenDefaultAndFailure(t *testing.T) {
	tmpDir := t.TempDir()

	// First bind to an address to force port collision
	origListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind listener: %v", err)
	}
	defer origListener.Close()

	port := origListener.Addr().(*net.TCPAddr).Port

	collidingConfig := filepath.Join(tmpDir, "collide.yaml")
	content := `
server:
  host: "127.0.0.1"
  port: ` + string(rune('0'+port/10000)) + `
homeassistant:
  url: "http://127.0.0.1:8123"
`
	// Actually write exact port number
	content = strings.Replace(content, string(rune('0'+port/10000)), strings.TrimSpace(origListener.Addr().String()[strings.LastIndex(origListener.Addr().String(), ":")+1:]), 1)
	if err := os.WriteFile(collidingConfig, []byte(content), 0600); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	err = RunWithReady(
		context.Background(),
		[]string{"-config", collidingConfig},
		&bytes.Buffer{},
		&bytes.Buffer{},
		nil,
		nil, // tests listenerOverride == nil failure branch
		&mockHAClient{},
	)

	if err == nil {
		t.Fatal("expected error on port collision, got nil")
	}
	if !strings.Contains(err.Error(), "failed to listen") {
		t.Errorf("expected failed to listen error, got %v", err)
	}
}

func TestMain_Execution_Success(t *testing.T) {
	oldArgs := argsHook
	oldExit := exitFunc
	defer func() {
		argsHook = oldArgs
		exitFunc = oldExit
	}()

	argsHook = []string{"--help"}
	exitCode := 0
	exitFunc = func(code int) {
		exitCode = code
	}

	main()

	if exitCode != 0 {
		t.Errorf("expected exit code 0 for --help, got %d", exitCode)
	}
}

func TestMain_Execution_Error(t *testing.T) {
	oldArgs := argsHook
	oldExit := exitFunc
	defer func() {
		argsHook = oldArgs
		exitFunc = oldExit
	}()

	argsHook = []string{"-config", "/nonexistent/path/never_exists.yaml"}
	exitCode := 0
	exitFunc = func(code int) {
		exitCode = code
	}

	main()

	if exitCode != 1 {
		t.Errorf("expected exit code 1 on config error, got %d", exitCode)
	}
}
