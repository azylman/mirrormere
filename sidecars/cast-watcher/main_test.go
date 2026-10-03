package main

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type failWriter struct{}

func (failWriter) Write(p []byte) (n int, err error) {
	return 0, errors.New("simulated stdout write failure")
}

func TestRun_SuccessAndShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	readyChan := make(chan struct{}, 1)
	var stdout, stderr bytes.Buffer

	// Custom client with loopback dummy dialer so it doesn't try to connect to real chromecast
	mockDialer := func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, _ := net.Pipe()
		return c, nil
	}
	client := NewCastClient(ClientConfig{
		ChromecastAddr: "127.0.0.1:8009",
		Dialer:         mockDialer,
	})

	errChan := make(chan error, 1)
	go func() {
		errChan <- RunWithReady(
			ctx,
			[]string{"-port", "0", "-chromecast", "127.0.0.1:8009"},
			&stdout, &stderr,
			readyChan,
			client,
		)
	}()

	select {
	case <-readyChan:
	case err := <-errChan:
		t.Fatalf("RunWithReady failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for readyChan")
	}

	outStr := stdout.String()
	if !strings.Contains(outStr, "Mirrormere Cast Watcher listening on") {
		t.Errorf("unexpected stdout: %s", outStr)
	}

	// Trigger shutdown
	cancel()

	select {
	case err := <-errChan:
		if err != nil {
			t.Fatalf("Run returned error on shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for clean shutdown")
	}
}

func TestRun_HelpFlag(t *testing.T) {
	ctx := context.Background()
	var stdout, stderr bytes.Buffer

	err := Run(ctx, []string{"-help"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("expected nil for -help flag, got %v", err)
	}
}

func TestRun_EnvOverrides(t *testing.T) {
	t.Setenv("CHROMECAST_IP", "127.0.0.1")
	t.Setenv("CONTROL_PORT", "0")
	t.Setenv("CORE_URL", "http://127.0.0.1:8080")
	t.Setenv("STREAM_URL", "http://127.0.0.1:1984/api/webrtc?src=cast")
	t.Setenv("CONTROL_URL", "http://cast-watcher:8090/action")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	readyChan := make(chan struct{}, 1)
	var stdout, stderr bytes.Buffer

	mockDialer := func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, _ := net.Pipe()
		return c, nil
	}
	client := NewCastClient(ClientConfig{
		ChromecastAddr: "127.0.0.1:8009",
		Dialer:         mockDialer,
	})

	errChan := make(chan error, 1)
	go func() {
		errChan <- RunWithReady(ctx, nil, &stdout, &stderr, readyChan, client)
	}()

	select {
	case <-readyChan:
	case err := <-errChan:
		t.Fatalf("RunWithReady failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for readyChan")
	}

	cancel()

	select {
	case err := <-errChan:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for shutdown")
	}
}

func TestRun_CastWatcherControlURLFallback(t *testing.T) {
	t.Setenv("CHROMECAST_IP", "127.0.0.1")
	t.Setenv("CONTROL_PORT", "0")
	t.Setenv("CORE_URL", "http://127.0.0.1:8080")
	t.Setenv("STREAM_URL", "http://127.0.0.1:1984/api/webrtc?src=cast")
	t.Setenv("CAST_WATCHER_CONTROL_URL", "http://legacy-watcher:8090/action")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	readyChan := make(chan struct{}, 1)
	var stdout, stderr bytes.Buffer

	mockDialer := func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, _ := net.Pipe()
		return c, nil
	}
	client := NewCastClient(ClientConfig{
		ChromecastAddr: "127.0.0.1:8009",
		Dialer:         mockDialer,
	})

	errChan := make(chan error, 1)
	go func() {
		errChan <- RunWithReady(ctx, nil, &stdout, &stderr, readyChan, client)
	}()

	select {
	case <-readyChan:
	case err := <-errChan:
		t.Fatalf("RunWithReady failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for readyChan")
	}

	cancel()

	select {
	case err := <-errChan:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for shutdown")
	}
}

func TestRun_StdoutFailure(t *testing.T) {
	ctx := context.Background()
	var stderr bytes.Buffer

	err := RunWithReady(ctx, []string{"-port", "0"}, failWriter{}, &stderr, nil, nil)
	if err == nil {
		t.Fatal("expected error on stdout write failure, got nil")
	}
	if !strings.Contains(err.Error(), "simulated stdout write failure") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestMain_ExecutionWithHelp(t *testing.T) {
	oldArgsHook := argsHook
	argsHook = []string{"-help"}
	defer func() { argsHook = oldArgsHook }()

	main()
}

func TestMain_ExecutionWithError(t *testing.T) {
	oldArgsHook := argsHook
	oldExitFunc := exitFunc
	argsHook = []string{"-invalid-arg-to-trigger-error"}

	exitCode := 0
	exitFunc = func(code int) {
		exitCode = code
	}
	defer func() {
		argsHook = oldArgsHook
		exitFunc = oldExitFunc
	}()

	main()

	if exitCode != 1 {
		t.Errorf("expected exit code 1, got %d", exitCode)
	}
}

func TestRun_YAMLLoading(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "cast-watcher.yaml")
	yamlContent := `
chromecast_addr: "10.0.0.50:8009"
core_url: "http://core.internal:8080"
stream_url: "http://core.internal:1984/stream"
control_port: 0
control_url: "http://control.internal:9999/action"
`
	if err := os.WriteFile(configPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("failed to write test yaml: %v", err)
	}

	cfg, err := loadConfigFile(configPath)
	if err != nil {
		t.Fatalf("failed to load config file: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.ChromecastAddr != "10.0.0.50:8009" {
		t.Errorf("expected chromecast 10.0.0.50:8009, got %s", cfg.ChromecastAddr)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	readyChan := make(chan struct{}, 1)
	var stdout, stderr bytes.Buffer
	mockDialer := func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, _ := net.Pipe()
		return c, nil
	}
	client := NewCastClient(ClientConfig{
		ChromecastAddr: "127.0.0.1:8009",
		Dialer:         mockDialer,
	})

	errChan := make(chan error, 1)
	go func() {
		errChan <- RunWithReady(
			ctx,
			[]string{"-config", configPath, "-port", "0"},
			&stdout, &stderr,
			readyChan,
			client,
		)
	}()

	select {
	case <-readyChan:
	case err := <-errChan:
		t.Fatalf("RunWithReady failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for readyChan")
	}
	cancel()
	<-errChan

	// Test invalid YAML error path
	invalidPath := filepath.Join(tmpDir, "invalid.yaml")
	_ = os.WriteFile(invalidPath, []byte("invalid: yaml: ["), 0644)
	if _, err := loadConfigFile(invalidPath); err == nil {
		t.Error("expected error for invalid YAML, got nil")
	}

	err = RunWithReady(ctx, []string{"-config", invalidPath}, &stdout, &stderr, nil, nil)
	if err == nil {
		t.Error("expected error from RunWithReady with invalid config, got nil")
	}

	// Test RunWithReady loading from CONFIG_PATH env var
	t.Setenv("CONFIG_PATH", configPath)
	t.Setenv("CHROMECAST_ADDR", "")
	t.Setenv("CHROMECAST_IP", "")
	t.Setenv("CORE_URL", "")
	t.Setenv("STREAM_URL", "")
	t.Setenv("CONTROL_URL", "")
	t.Setenv("CAST_WATCHER_CONTROL_URL", "")
	t.Setenv("CONTROL_PORT", "")
	t.Setenv("PORT", "")

	ctxEnv, cancelEnv := context.WithCancel(context.Background())
	defer cancelEnv()

	readyChanEnv := make(chan struct{}, 1)
	var stdoutEnv, stderrEnv bytes.Buffer
	clientEnv := NewCastClient(ClientConfig{
		ChromecastAddr: "127.0.0.1:8009",
		Dialer:         mockDialer,
	})

	errChanEnv := make(chan error, 1)
	go func() {
		errChanEnv <- RunWithReady(
			ctxEnv,
			[]string{"-port", "0"},
			&stdoutEnv, &stderrEnv,
			readyChanEnv,
			clientEnv,
		)
	}()

	select {
	case <-readyChanEnv:
	case err := <-errChanEnv:
		t.Fatalf("RunWithReady with CONFIG_PATH failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for readyChanEnv")
	}
	cancelEnv()
	<-errChanEnv

	// Test RunWithReady error when CONFIG_PATH points to invalid file
	t.Setenv("CONFIG_PATH", invalidPath)
	err = RunWithReady(ctx, nil, &stdout, &stderr, nil, nil)
	if err == nil {
		t.Error("expected error from RunWithReady with invalid CONFIG_PATH, got nil")
	}
}
