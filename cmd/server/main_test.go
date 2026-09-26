package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func init() {
	if os.Getenv("DB_PATH") == "" {
		_ = os.Setenv("DB_PATH", ":memory:")
	}
}

type failWriter struct{}

func (failWriter) Write(p []byte) (n int, err error) {
	return 0, errors.New("simulated stdout write failure")
}

func TestRun_SuccessAndShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	readyChan := make(chan struct{}, 1)
	addrChan := make(chan string, 1)
	var stdout, stderr bytes.Buffer

	errChan := make(chan error, 1)
	go func() {
		// Bind to ephemeral port 0 on 127.0.0.1
		errChan <- RunWithReady(ctx, []string{"-host", "127.0.0.1", "-port", "0"}, &stdout, &stderr, readyChan, WithAddrChan(addrChan))
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

	addr := <-addrChan
	resp, err := http.Get("http://" + addr + "/display")
	if err == nil {
		_ = resp.Body.Close()
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

func TestRun_CompositionRootSmoke(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "config.yaml")
	cfgContent := "timezone: America/New_York\ndisplay:\n  widgets:\n    - id: default-spacer\n      type: spacer\n      dimensions: [6, 2]\n"
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	readyChan := make(chan struct{}, 1)
	addrChan := make(chan string, 1)
	var stdout, stderr bytes.Buffer

	errChan := make(chan error, 1)
	go func() {
		errChan <- RunWithReady(ctx, []string{
			"-host", "127.0.0.1",
			"-port", "0",
			"-config", cfgPath,
			"-db", ":memory:",
		}, &stdout, &stderr, readyChan, WithAddrChan(addrChan))
	}()

	select {
	case <-readyChan:
	case err := <-errChan:
		t.Fatalf("RunWithReady failed: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for ready signal")
	}

	var baseURL string
	select {
	case addr := <-addrChan:
		baseURL = fmt.Sprintf("http://%s", addr)
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for bound address")
	}

	client := &http.Client{Timeout: 5 * time.Second}

	// 1. GET /healthz -> 200 OK
	resp, err := client.Get(baseURL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /healthz status = %d, want 200", resp.StatusCode)
	}

	// 2. GET /api/audio -> 200 OK
	resp, err = client.Get(baseURL + "/api/audio")
	if err != nil {
		t.Fatalf("GET /api/audio failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /api/audio status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"volume":75`) || !strings.Contains(string(body), `"muted":false`) {
		t.Errorf("GET /api/audio body = %s, expected volume 75 unmuted", string(body))
	}

	// 3. GET /api/video/state -> 200 OK
	resp, err = client.Get(baseURL + "/api/video/state")
	if err != nil {
		t.Fatalf("GET /api/video/state failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /api/video/state status = %d, want 200", resp.StatusCode)
	}
	body, _ = io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"mode":"widgets"`) {
		t.Errorf("GET /api/video/state body = %s, expected mode widgets", string(body))
	}

	// 4. GET /display -> 200 OK
	resp, err = client.Get(baseURL + "/display")
	if err != nil {
		t.Fatalf("GET /display failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /display status = %d, want 200", resp.StatusCode)
	}
	body, _ = io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "mirrormere-app") {
		t.Errorf("GET /display body does not contain mirrormere-app: %s", string(body))
	}

	// 5. GET /style.css -> 200 OK
	resp, err = client.Get(baseURL + "/style.css")
	if err != nil {
		t.Fatalf("GET /style.css failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /style.css status = %d, want 200", resp.StatusCode)
	}

	// 6. GET /api/lists/test/items -> 404 with structured JSON
	resp, err = client.Get(baseURL + "/api/lists/test/items")
	if err != nil {
		t.Fatalf("GET /api/lists/test/items failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /api/lists/test/items status = %d, want 404", resp.StatusCode)
	}
	body, _ = io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "list 'test' not found") {
		t.Errorf("GET /api/lists/test/items body = %s, expected list not found error", string(body))
	}

	// 7. GET /api/events -> 200 OK with text/event-stream, reads initial hydration chunk
	reqCtx, reqCancel := context.WithCancel(context.Background())
	defer reqCancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, baseURL+"/api/events", nil)
	if err != nil {
		t.Fatalf("failed to create /api/events request: %v", err)
	}
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/events failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /api/events status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("GET /api/events Content-Type = %s, want text/event-stream", ct)
	}

	buf := make([]byte, 256)
	n, err := resp.Body.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("failed to read from /api/events stream: %v", err)
	}
	if n == 0 {
		t.Error("expected non-zero bytes read from /api/events stream")
	}
	reqCancel()

	// Trigger server shutdown
	cancel()

	select {
	case err := <-errChan:
		if err != nil {
			t.Fatalf("server shutdown error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server failed to shutdown in time")
	}
}

func TestRun_ConfigReadError(t *testing.T) {
	ctx := context.Background()
	var stdout, stderr bytes.Buffer

	err := Run(ctx, []string{"-config", "/nonexistent/path/to/config.yaml"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error on nonexistent config file, got nil")
	}
	if !strings.Contains(err.Error(), "failed to read configuration file") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestRun_ConfigManagerError(t *testing.T) {
	ctx := context.Background()
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "invalid.yaml")
	_ = os.WriteFile(cfgPath, []byte("display: [unclosed"), 0644)

	var stdout, stderr bytes.Buffer
	err := Run(ctx, []string{"-config", cfgPath}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error on invalid yaml config, got nil")
	}
	if !strings.Contains(err.Error(), "failed to initialize configuration manager") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestRun_TasksStoreFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	readyChan := make(chan struct{}, 1)
	var stdout, stderr bytes.Buffer

	errChan := make(chan error, 1)
	go func() {
		// Use an impossible path to trigger fallback to :memory:
		errChan <- RunWithReady(ctx, []string{
			"-host", "127.0.0.1",
			"-port", "0",
			"-db", "/proc/impossible/dir/lists.db",
		}, &stdout, &stderr, readyChan)
	}()

	select {
	case <-readyChan:
	case err := <-errChan:
		t.Fatalf("RunWithReady failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ready signal")
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

	if !strings.Contains(stderr.String(), "falling back to in-memory store") {
		t.Errorf("expected warning about fallback to in-memory store in stderr: %s", stderr.String())
	}
}

func TestRun_EmptyEnvDB(t *testing.T) {
	t.Setenv("DB_PATH", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	readyChan := make(chan struct{}, 1)
	var stdout, stderr bytes.Buffer

	errChan := make(chan error, 1)
	go func() {
		errChan <- RunWithReady(ctx, []string{"-host", "127.0.0.1", "-port", "0", "-db", ":memory:"}, &stdout, &stderr, readyChan)
	}()

	select {
	case <-readyChan:
	case err := <-errChan:
		t.Fatalf("RunWithReady failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ready signal")
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
	t.Setenv("CONFIG_PATH", "")

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
