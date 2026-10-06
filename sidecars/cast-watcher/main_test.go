package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

func writeTestYAML(t *testing.T, dir, filename, content string) string {
	t.Helper()
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write %s: %v", filename, err)
	}
	return path
}

func TestRun_SuccessAndShutdown(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := writeTestYAML(t, tmpDir, "cast-watcher.yaml", `
chromecast_addr: "127.0.0.1:8009"
core_url: "http://127.0.0.1:8080"
stream_url: "http://127.0.0.1:1984/api/webrtc?src=cast"
control_port: 0
control_url: "http://cast-watcher:8090/action"
`)

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
			[]string{configPath},
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

	for _, flag := range []string{"-help", "--help", "-h"} {
		stderr.Reset()
		err := Run(ctx, []string{flag}, &stdout, &stderr)
		if err != nil {
			t.Fatalf("expected nil for %s flag, got %v", flag, err)
		}
		if !strings.Contains(stderr.String(), "Usage: cast-watcher") {
			t.Errorf("expected usage message in stderr for %s, got: %s", flag, stderr.String())
		}
	}
}

func TestRun_FlagsRejected(t *testing.T) {
	ctx := context.Background()
	var stdout, stderr bytes.Buffer

	flags := []string{"-port", "--port=8090", "-chromecast", "-unknown"}
	for _, f := range flags {
		err := Run(ctx, []string{f}, &stdout, &stderr)
		if err == nil {
			t.Fatalf("expected error when passing flag %s, got nil", f)
		}
		if !strings.Contains(err.Error(), "flags are not supported; configure via config file") {
			t.Errorf("unexpected error message for flag %s: %v", f, err)
		}
	}
}

func TestRun_MissingControlURL(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := writeTestYAML(t, tmpDir, "cast-watcher.yaml", `
chromecast_addr: "127.0.0.1:8009"
control_port: 0
`)

	ctx := context.Background()
	var stdout, stderr bytes.Buffer

	err := Run(ctx, []string{configPath}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when control_url is omitted, got nil")
	}
	if !strings.Contains(err.Error(), "control_url is required in configuration") {
		t.Errorf("unexpected error: %v", err)
	}

	// Also test whitespace-only control_url
	blankURLPath := writeTestYAML(t, tmpDir, "blank.yaml", `
control_url: "   "
`)
	err = Run(ctx, []string{blankURLPath}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when control_url is blank, got nil")
	}
	if !strings.Contains(err.Error(), "control_url is required in configuration") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRun_NoConfigFile(t *testing.T) {
	t.Setenv("CONFIG_PATH", "")

	ctx := context.Background()
	var stdout, stderr bytes.Buffer

	err := Run(ctx, nil, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when no config file exists, got nil")
	}
	if !strings.Contains(err.Error(), "control_url is required in configuration") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRun_StdoutFailure(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := writeTestYAML(t, tmpDir, "cast-watcher.yaml", `
control_url: "http://cast-watcher:8090/action"
control_port: 0
`)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var stderr bytes.Buffer
	err := RunWithReady(ctx, []string{configPath}, failWriter{}, &stderr, nil, nil)
	if err == nil {
		t.Fatal("expected error on stdout write failure, got nil")
	}
	if !strings.Contains(err.Error(), "simulated stdout write failure") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRun_DefaultsAppliedAndClientInit(t *testing.T) {
	tmpDir := t.TempDir()
	// Minimal config with chromecast without port and minimal control_url
	configPath := writeTestYAML(t, tmpDir, "cast-watcher.yaml", `
chromecast_addr: "10.0.0.5"
control_url: "http://cast-watcher:8090/action"
control_port: 0
`)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	readyChan := make(chan struct{}, 1)
	var stdout, stderr bytes.Buffer

	errChan := make(chan error, 1)
	go func() {
		// Run without clientOverride so NewCastClient branch executes
		errChan <- RunWithReady(ctx, []string{configPath}, &stdout, &stderr, readyChan, nil)
	}()

	select {
	case <-readyChan:
	case err := <-errChan:
		t.Fatalf("RunWithReady failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for readyChan")
	}

	out := stdout.String()
	if !strings.Contains(out, "Chromecast: 10.0.0.5:8009") {
		t.Errorf("expected appended default port :8009, got stdout: %s", out)
	}

	cancel()
	<-errChan
}

func TestRun_DefaultChromecastAddress(t *testing.T) {
	tmpDir := t.TempDir()
	// Config with empty chromecast_addr to exercise defaultChromecast
	configPath := writeTestYAML(t, tmpDir, "cast-watcher.yaml", `
control_url: "http://cast-watcher:8090/action"
control_port: 0
`)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	readyChan := make(chan struct{}, 1)
	var stdout, stderr bytes.Buffer

	errChan := make(chan error, 1)
	go func() {
		errChan <- RunWithReady(ctx, []string{configPath}, &stdout, &stderr, readyChan, nil)
	}()

	select {
	case <-readyChan:
	case err := <-errChan:
		t.Fatalf("RunWithReady failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for readyChan")
	}

	out := stdout.String()
	if !strings.Contains(out, "Chromecast: 10.0.0.50:8009") {
		t.Errorf("expected default chromecast 10.0.0.50:8009, got: %s", out)
	}

	cancel()
	<-errChan
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
	configPath := writeTestYAML(t, tmpDir, "cast-watcher.yaml", `
chromecast_addr: "10.0.0.50:8009"
core_url: "http://core.internal:8080"
stream_url: "http://core.internal:1984/stream"
control_port: 0
control_url: "http://control.internal:9999/action"
`)

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

	// Test invalid YAML error path
	invalidPath := writeTestYAML(t, tmpDir, "invalid.yaml", "invalid: yaml: [")
	if _, err := loadConfigFile(invalidPath); err == nil {
		t.Error("expected error for invalid YAML, got nil")
	}

	var stdout, stderr bytes.Buffer
	err = RunWithReady(context.Background(), []string{invalidPath}, &stdout, &stderr, nil, nil)
	if err == nil {
		t.Error("expected error from RunWithReady with invalid config, got nil")
	}

	// Test RunWithReady loading from CONFIG_PATH env var
	t.Setenv("CONFIG_PATH", configPath)

	ctxEnv, cancelEnv := context.WithCancel(context.Background())
	defer cancelEnv()

	readyChanEnv := make(chan struct{}, 1)
	var stdoutEnv, stderrEnv bytes.Buffer

	mockDialer := func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, _ := net.Pipe()
		return c, nil
	}
	clientEnv := NewCastClient(ClientConfig{
		ChromecastAddr: "127.0.0.1:8009",
		Dialer:         mockDialer,
	})

	errChanEnv := make(chan error, 1)
	go func() {
		errChanEnv <- RunWithReady(
			ctxEnv,
			nil,
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
	err = RunWithReady(context.Background(), nil, &stdout, &stderr, nil, nil)
	if err == nil {
		t.Error("expected error from RunWithReady with invalid CONFIG_PATH, got nil")
	}
}

func TestRun_BindFailure(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer l.Close()

	port := l.Addr().(*net.TCPAddr).Port

	tmpDir := t.TempDir()
	configPath := writeTestYAML(t, tmpDir, "cast-watcher.yaml", fmt.Sprintf(`
control_url: "http://cast-watcher:8090/action"
control_port: %d
`, port))

	var stdout, stderr bytes.Buffer
	err = RunWithReady(context.Background(), []string{configPath}, &stdout, &stderr, nil, nil)
	if err == nil {
		t.Fatal("expected bind error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to bind address") {
		t.Errorf("unexpected error: %v", err)
	}
}
