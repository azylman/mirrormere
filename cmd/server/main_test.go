package main

import (
	"bytes"
	"context"
	"errors"
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

	errChan := make(chan error, 1)
	go func() {
		// Bind to ephemeral port 0 on 127.0.0.1
		errChan <- RunWithReady(ctx, []string{"-host", "127.0.0.1", "-port", "0"}, &stdout, &stderr, readyChan)
	}()

	select {
	case <-readyChan:
	case err := <-errChan:
		t.Fatalf("RunWithReady failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for server ready signal")
	}

	outStr := stdout.String()
	if !strings.Contains(outStr, "Mirrormere daemon listening on") {
		t.Errorf("unexpected stdout: %s", outStr)
	}

	// Trigger shutdown
	cancel()

	select {
	case err := <-errChan:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server failed to shutdown in time")
	}
}

func TestRun_HelpFlag(t *testing.T) {
	ctx := context.Background()
	var stdout, stderr bytes.Buffer

	err := Run(ctx, []string{"-help"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("expected nil error on help flag, got %v", err)
	}
}

func TestRun_InvalidFlag(t *testing.T) {
	ctx := context.Background()
	var stdout, stderr bytes.Buffer

	err := Run(ctx, []string{"-nonexistent-flag"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error on invalid flag, got nil")
	}
}

func TestRun_BindError(t *testing.T) {
	ctx := context.Background()
	var stdout, stderr bytes.Buffer

	err := Run(ctx, []string{"-host", "999.999.999.999", "-port", "8080"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected bind error for invalid host, got nil")
	}
}

func TestRun_StdoutWriteError(t *testing.T) {
	ctx := context.Background()
	var stderr bytes.Buffer

	// Force stdout write to fail
	err := RunWithReady(ctx, []string{"-host", "127.0.0.1", "-port", "0"}, failWriter{}, &stderr, nil)
	if err == nil {
		t.Fatal("expected write error on stdout failure, got nil")
	}
	if !strings.Contains(err.Error(), "simulated stdout write failure") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestRun_EnvOverrides(t *testing.T) {
	t.Setenv("HOST", "127.0.0.1")
	t.Setenv("PORT", "0")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	readyChan := make(chan struct{}, 1)
	var stdout, stderr bytes.Buffer

	errChan := make(chan error, 1)
	go func() {
		errChan <- RunWithReady(ctx, nil, &stdout, &stderr, readyChan)
	}()

	select {
	case <-readyChan:
	case err := <-errChan:
		t.Fatalf("RunWithReady failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for server ready signal")
	}

	cancel()

	select {
	case err := <-errChan:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server failed to shutdown")
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
