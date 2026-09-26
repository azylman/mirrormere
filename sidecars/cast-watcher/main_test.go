package main

import (
	"bytes"
	"context"
	"errors"
	"net"
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
	t.Setenv("STREAM_URL", "http://127.0.0.1:1984/cast")

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
