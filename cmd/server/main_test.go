package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/config"
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

func writeTestServerYAML(t *testing.T, dir, filename, content string) string {
	t.Helper()
	p := filepath.Join(dir, filename)
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write %s: %v", filename, err)
	}
	return p
}

func TestRun_SuccessAndShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tmpDir := t.TempDir()
	cfgPath := writeTestServerYAML(t, tmpDir, "config.yaml", `
host: "127.0.0.1"
port: 0
timezone: "UTC"
display:
  widgets:
    - id: default-spacer
      type: spacer
      dimensions: [6, 2]
`)

	readyChan := make(chan struct{}, 1)
	addrChan := make(chan string, 1)
	var stdout, stderr bytes.Buffer

	errChan := make(chan error, 1)
	go func() {
		// Bind to ephemeral port 0 on 127.0.0.1 via config file
		errChan <- RunWithReady(ctx, []string{cfgPath}, &stdout, &stderr, readyChan, WithAddrChan(addrChan))
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
	cfgContent := "host: \"127.0.0.1\"\nport: 0\ntimezone: America/New_York\ndisplay:\n  widgets:\n    - id: default-spacer\n      type: spacer\n      dimensions: [6, 2]\n"
	cfgPath := writeTestServerYAML(t, cfgDir, "config.yaml", cfgContent)

	readyChan := make(chan struct{}, 1)
	addrChan := make(chan string, 1)
	var stdout, stderr bytes.Buffer

	errChan := make(chan error, 1)
	go func() {
		errChan <- RunWithReady(ctx, []string{cfgPath}, &stdout, &stderr, readyChan, WithAddrChan(addrChan))
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
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /healthz status = %d, want 200", resp.StatusCode)
	}

	// 2. GET /api/audio -> 200 OK
	resp, err = client.Get(baseURL + "/api/audio")
	if err != nil {
		t.Fatalf("GET /api/audio failed: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
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
	t.Cleanup(func() { _ = resp.Body.Close() })
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
	t.Cleanup(func() { _ = resp.Body.Close() })
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
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /style.css status = %d, want 200", resp.StatusCode)
	}

	// 6. GET /api/lists/test/items -> 404 with structured JSON
	resp, err = client.Get(baseURL + "/api/lists/test/items")
	if err != nil {
		t.Fatalf("GET /api/lists/test/items failed: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
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
	t.Cleanup(func() { _ = resp.Body.Close() })
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

	err := Run(ctx, []string{"/nonexistent/path/to/config.yaml"}, &stdout, &stderr)
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
	err := Run(ctx, []string{cfgPath}, &stdout, &stderr)
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

	tmpDir := t.TempDir()
	cfgPath := writeTestServerYAML(t, tmpDir, "config.yaml", `
host: "127.0.0.1"
port: 0
timezone: "UTC"
display:
  widgets:
    - id: default-spacer
      type: spacer
      dimensions: [6, 2]
`)
	t.Setenv("DB_PATH", "/proc/impossible/dir/lists.db")

	readyChan := make(chan struct{}, 1)
	var stdout, stderr bytes.Buffer

	errChan := make(chan error, 1)
	go func() {
		// Use an impossible path in DB_PATH to trigger fallback to :memory:
		errChan <- RunWithReady(ctx, []string{cfgPath}, &stdout, &stderr, readyChan)
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

	tmpDir := t.TempDir()
	cfgPath := writeTestServerYAML(t, tmpDir, "config.yaml", `
host: "127.0.0.1"
port: 0
timezone: "UTC"
display:
  widgets:
    - id: default-spacer
      type: spacer
      dimensions: [6, 2]
`)

	readyChan := make(chan struct{}, 1)
	var stdout, stderr bytes.Buffer

	errChan := make(chan error, 1)
	go func() {
		errChan <- RunWithReady(ctx, []string{cfgPath}, &stdout, &stderr, readyChan)
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

	for _, flag := range []string{"-h", "-help", "--help"} {
		var stdout, stderr bytes.Buffer
		err := Run(ctx, []string{flag}, &stdout, &stderr)
		if err != nil {
			t.Fatalf("expected nil error on help flag %s, got %v", flag, err)
		}
		if !strings.Contains(stderr.String(), "Usage: mirrormere [config-path]") {
			t.Errorf("expected usage string in stderr for %s, got: %s", flag, stderr.String())
		}
	}

	var stdout bytes.Buffer
	err := Run(ctx, []string{"-h"}, &stdout, failWriter{})
	if err == nil {
		t.Fatal("expected error on failing stderr writer, got nil")
	}
}

func TestRun_ConfigPathEnv_MissingFile(t *testing.T) {
	t.Setenv("CONFIG_PATH", "/nonexistent/test-config.yaml")
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for non-existent CONFIG_PATH, got nil")
	}
	if !strings.Contains(err.Error(), `failed to read configuration file "/nonexistent/test-config.yaml"`) {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestRun_InvalidFlag(t *testing.T) {
	ctx := context.Background()
	var stdout, stderr bytes.Buffer

	err := Run(ctx, []string{"-nonexistent-flag"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error on invalid flag, got nil")
	}
	if !strings.Contains(err.Error(), "flags are not supported; configure via config file") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestRun_FlagsRejected(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"-port", "8080"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for flag, got nil")
	}
	if !strings.Contains(err.Error(), "flags are not supported; configure via config file: -port") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestRun_BindError(t *testing.T) {
	ctx := context.Background()
	var stdout, stderr bytes.Buffer

	tmpDir := t.TempDir()
	cfgPath := writeTestServerYAML(t, tmpDir, "config.yaml", `
host: "999.999.999.999"
port: 8080
timezone: "UTC"
display:
  widgets:
    - id: default-spacer
      type: spacer
      dimensions: [6, 2]
`)

	err := Run(ctx, []string{cfgPath}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected bind error for invalid host, got nil")
	}
}

func TestRun_StdoutWriteError(t *testing.T) {
	ctx := context.Background()
	var stderr bytes.Buffer

	tmpDir := t.TempDir()
	cfgPath := writeTestServerYAML(t, tmpDir, "config.yaml", `
host: "127.0.0.1"
port: 0
timezone: "UTC"
display:
  widgets:
    - id: default-spacer
      type: spacer
      dimensions: [6, 2]
`)

	// Force stdout write to fail
	err := RunWithReady(ctx, []string{cfgPath}, failWriter{}, &stderr, nil)
	if err == nil {
		t.Fatal("expected write error on stdout failure, got nil")
	}
	if !strings.Contains(err.Error(), "simulated stdout write failure") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestRun_EnvHostPortIgnored(t *testing.T) {
	t.Setenv("HOST", "1.2.3.4")
	t.Setenv("PORT", "9999")

	tmpDir := t.TempDir()
	cfgPath := writeTestServerYAML(t, tmpDir, "config.yaml", `
host: "127.0.0.1"
port: 0
timezone: "UTC"
display:
  widgets:
    - id: default-spacer
      type: spacer
      dimensions: [6, 2]
`)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	readyChan := make(chan struct{}, 1)
	addrChan := make(chan string, 1)
	var stdout, stderr bytes.Buffer

	errChan := make(chan error, 1)
	go func() {
		errChan <- RunWithReady(ctx, []string{cfgPath}, &stdout, &stderr, readyChan, WithAddrChan(addrChan))
	}()

	select {
	case <-readyChan:
	case err := <-errChan:
		t.Fatalf("RunWithReady failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for server ready signal")
	}

	addr := <-addrChan
	if !strings.HasPrefix(addr, "127.0.0.1:") {
		t.Errorf("expected server to bind 127.0.0.1 (ignoring HOST env 1.2.3.4), got %s", addr)
	}
	if strings.HasSuffix(addr, ":9999") {
		t.Errorf("expected server to bind ephemeral port 0 (ignoring PORT env 9999), got %s", addr)
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

// fakeSnapshotProvider is a minimal render.StateSnapshotProvider stub carrying only what
// householdNowFunc reads.
type fakeSnapshotProvider struct {
	snap *config.Snapshot
}

func (f *fakeSnapshotProvider) CurrentSnapshot() *config.Snapshot { return f.snap }
func (f *fakeSnapshotProvider) GetWidgetState(string) (any, string, string, bool) {
	return nil, "", "", false
}
func (f *fakeSnapshotProvider) CurrentStatus() config.Status { return config.Status{} }

func TestHouseholdNowFunc_UsesConfiguredTimezoneRegardlessOfProcessLocal(t *testing.T) {
	// Asia/Tokyo (UTC+9, no DST) is very unlikely to match this test process's own local zone,
	// so if householdNowFunc returned time.Now() untouched, the location would not be JST.
	sp := &fakeSnapshotProvider{snap: &config.Snapshot{Config: &config.Config{Timezone: "Asia/Tokyo"}}}
	now := householdNowFunc(sp)()
	if zone, _ := now.Zone(); zone != "JST" {
		t.Fatalf("expected householdNowFunc to report the household's configured zone (JST), got %s", zone)
	}
}

func TestHouseholdNowFunc_FallsBackToProcessLocalWithoutASnapshot(t *testing.T) {
	if got := householdNowFunc(&fakeSnapshotProvider{snap: nil})(); got.IsZero() {
		t.Fatal("expected a non-zero time when no snapshot is available")
	}
	if got := householdNowFunc(nil)(); got.IsZero() {
		t.Fatal("expected a non-zero time for a nil provider")
	}
}

func TestRun_YAMLHostPortConfig(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("HOST", "")
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "mirrormere.yaml")
	yamlContent := `
host: "127.0.0.1"
port: 0
timezone: "UTC"
display:
  widgets:
    - id: spacer
      type: spacer
      dimensions: [6, 2]
`
	if err := os.WriteFile(configPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("failed to write test yaml: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	readyChan := make(chan struct{}, 1)
	addrChan := make(chan string, 1)
	var stdout, stderr bytes.Buffer

	errChan := make(chan error, 1)
	go func() {
		errChan <- RunWithReady(ctx, []string{configPath}, &stdout, &stderr, readyChan, WithAddrChan(addrChan))
	}()

	select {
	case <-readyChan:
	case err := <-errChan:
		t.Fatalf("RunWithReady failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ready signal")
	}

	cancel()
	<-errChan
}

func TestRun_SIGHUP_ValidReload(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "mirrormere.yaml")
	initialYAML := `
host: "127.0.0.1"
port: 0
timezone: "UTC"
display:
  widgets:
    - id: spacer-1
      type: spacer
      dimensions: [6, 2]
`
	if err := os.WriteFile(configPath, []byte(initialYAML), 0644); err != nil {
		t.Fatalf("failed to write initial config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	readyChan := make(chan struct{}, 1)
	hupChan := make(chan os.Signal, 1)
	reloadedChan := make(chan struct{}, 1)
	addrChan := make(chan string, 1)
	var stdout, stderr bytes.Buffer

	errChan := make(chan error, 1)
	go func() {
		errChan <- RunWithReady(
			ctx,
			[]string{configPath},
			&stdout,
			&stderr,
			readyChan,
			WithAddrChan(addrChan),
			WithHupChan(hupChan),
			WithOnReload(func() {
				select {
				case reloadedChan <- struct{}{}:
				default:
				}
			}),
		)
	}()

	select {
	case <-readyChan:
	case err := <-errChan:
		t.Fatalf("RunWithReady failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ready signal")
	}

	// Update config on disk with new timezone
	updatedYAML := `
host: "127.0.0.1"
port: 0
timezone: "America/New_York"
display:
  widgets:
    - id: spacer-1
      type: spacer
      dimensions: [6, 2]
`
	if err := os.WriteFile(configPath, []byte(updatedYAML), 0644); err != nil {
		t.Fatalf("failed to write updated config: %v", err)
	}

	// Send SIGHUP
	hupChan <- syscall.SIGHUP

	select {
	case <-reloadedChan:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SIGHUP reload completion")
	}

	close(hupChan)
	cancel()
	<-errChan
}

func TestRun_SIGHUP_InvalidYAML_RetainsLKGC(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "mirrormere.yaml")
	initialYAML := `
host: "127.0.0.1"
port: 0
timezone: "UTC"
display:
  widgets:
    - id: spacer-1
      type: spacer
      dimensions: [6, 2]
`
	if err := os.WriteFile(configPath, []byte(initialYAML), 0644); err != nil {
		t.Fatalf("failed to write initial config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	readyChan := make(chan struct{}, 1)
	hupChan := make(chan os.Signal, 1)
	reloadedChan := make(chan struct{}, 1)
	addrChan := make(chan string, 1)
	var stdout, stderr bytes.Buffer

	errChan := make(chan error, 1)
	go func() {
		errChan <- RunWithReady(
			ctx,
			[]string{configPath},
			&stdout,
			&stderr,
			readyChan,
			WithAddrChan(addrChan),
			WithHupChan(hupChan),
			WithOnReload(func() {
				select {
				case reloadedChan <- struct{}{}:
				default:
				}
			}),
		)
	}()

	select {
	case <-readyChan:
	case err := <-errChan:
		t.Fatalf("RunWithReady failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ready signal")
	}

	// Overwrite with invalid YAML
	if err := os.WriteFile(configPath, []byte("invalid: yaml: syntax: [unclosed"), 0644); err != nil {
		t.Fatalf("failed to write invalid config: %v", err)
	}

	// Send SIGHUP
	hupChan <- syscall.SIGHUP

	select {
	case <-reloadedChan:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SIGHUP reload error completion")
	}

	// Server should still be running and healthy (LKGC)
	addr := <-addrChan
	resp, err := http.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatalf("expected server to remain running after invalid reload, got err: %v", err)
	}
	_ = resp.Body.Close()

	cancel()
	<-errChan
}

func TestRun_SIGHUP_ReadError_RetainsLKGC(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "mirrormere.yaml")
	initialYAML := `
host: "127.0.0.1"
port: 0
timezone: "UTC"
display:
  widgets:
    - id: spacer-1
      type: spacer
      dimensions: [6, 2]
`
	if err := os.WriteFile(configPath, []byte(initialYAML), 0644); err != nil {
		t.Fatalf("failed to write initial config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	readyChan := make(chan struct{}, 1)
	hupChan := make(chan os.Signal, 1)
	reloadedChan := make(chan struct{}, 1)
	var stdout, stderr bytes.Buffer

	errChan := make(chan error, 1)
	go func() {
		errChan <- RunWithReady(
			ctx,
			[]string{configPath},
			&stdout,
			&stderr,
			readyChan,
			WithHupChan(hupChan),
			WithOnReload(func() {
				select {
				case reloadedChan <- struct{}{}:
				default:
				}
			}),
		)
	}()

	select {
	case <-readyChan:
	case err := <-errChan:
		t.Fatalf("RunWithReady failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ready signal")
	}

	// Delete file
	_ = os.Remove(configPath)

	// Send SIGHUP
	hupChan <- syscall.SIGHUP

	select {
	case <-reloadedChan:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SIGHUP read error completion")
	}

	cancel()
	<-errChan
}

func TestRun_SIGHUP_StyleReload(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	customCSSPath := filepath.Join(tmpDir, "custom.css")

	initialYAML := `
host: "127.0.0.1"
port: 0
timezone: "UTC"
display:
  widgets:
    - id: spacer-1
      type: spacer
      dimensions: [6, 2]
`
	if err := os.WriteFile(configPath, []byte(initialYAML), 0644); err != nil {
		t.Fatalf("failed to write initial config: %v", err)
	}
	if err := os.WriteFile(customCSSPath, []byte("body { background: #000; }"), 0644); err != nil {
		t.Fatalf("failed to write initial custom.css: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	readyChan := make(chan struct{}, 1)
	hupChan := make(chan os.Signal, 1)
	reloadedChan := make(chan struct{}, 1)
	addrChan := make(chan string, 1)
	var stdout, stderr bytes.Buffer

	errChan := make(chan error, 1)
	go func() {
		errChan <- RunWithReady(
			ctx,
			[]string{configPath},
			&stdout,
			&stderr,
			readyChan,
			WithAddrChan(addrChan),
			WithHupChan(hupChan),
			WithOnReload(func() {
				select {
				case reloadedChan <- struct{}{}:
				default:
				}
			}),
		)
	}()

	select {
	case <-readyChan:
	case err := <-errChan:
		t.Fatalf("RunWithReady failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ready signal")
	}

	addr := <-addrChan
	baseURL := "http://" + addr

	// Connect to /api/events SSE
	sseCtx, sseCancel := context.WithCancel(context.Background())
	defer sseCancel()

	req, err := http.NewRequestWithContext(sseCtx, http.MethodGet, baseURL+"/api/events", nil)
	if err != nil {
		t.Fatalf("failed to create SSE request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to connect to /api/events: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	sseEvents := make(chan struct {
		eventType string
		data      string
	}, 20)

	go func() {
		scanner := bufio.NewScanner(resp.Body)
		var currentEvent string
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "event: ") {
				currentEvent = strings.TrimPrefix(line, "event: ")
			} else if strings.HasPrefix(line, "data: ") {
				select {
				case sseEvents <- struct {
					eventType string
					data      string
				}{eventType: currentEvent, data: strings.TrimPrefix(line, "data: ")}:
				case <-sseCtx.Done():
					return
				}
			}
		}
	}()

	// Wait for initial hydration to start flowing
	select {
	case <-sseEvents:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initial SSE event")
	}

	// Update custom.css
	updatedCSS := "body { background: #9b59b6; }"
	if err := os.WriteFile(customCSSPath, []byte(updatedCSS), 0644); err != nil {
		t.Fatalf("failed to update custom.css: %v", err)
	}

	// Send SIGHUP
	hupChan <- syscall.SIGHUP

	select {
	case <-reloadedChan:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SIGHUP reload completion")
	}

	// Verify style.reload SSE event received
	var receivedStyleReload bool
	timeout := time.After(2 * time.Second)
	for !receivedStyleReload {
		select {
		case evt := <-sseEvents:
			if evt.eventType == "style.reload" && strings.Contains(evt.data, "custom.css") {
				receivedStyleReload = true
			}
		case <-timeout:
			t.Fatal("timed out waiting for style.reload SSE event")
		}
	}

	// Verify GET /style.css serves updated stylesheet
	cssResp, err := http.Get(baseURL + "/style.css")
	if err != nil {
		t.Fatalf("failed to fetch /style.css: %v", err)
	}
	cssBody, err := io.ReadAll(cssResp.Body)
	_ = cssResp.Body.Close()
	if err != nil {
		t.Fatalf("failed to read /style.css body: %v", err)
	}
	if string(cssBody) != updatedCSS {
		t.Errorf("expected updated CSS %q, got %q", updatedCSS, string(cssBody))
	}

	sseCancel()
	cancel()
	<-errChan
}

func TestRun_SIGHUP_WidgetReload(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	customWidgetsDir := filepath.Join(tmpDir, "widgets")

	widgetDir := filepath.Join(customWidgetsDir, "custom-clock")
	if err := os.MkdirAll(filepath.Join(widgetDir, "views"), 0755); err != nil {
		t.Fatalf("failed to create custom widget views dir: %v", err)
	}
	manifestContent := `name: custom-clock
version: "1.0.0"
provider: spacer
default_dimensions: [6, 2]
`
	if err := os.WriteFile(filepath.Join(widgetDir, "manifest.yaml"), []byte(manifestContent), 0644); err != nil {
		t.Fatalf("failed to write widget manifest: %v", err)
	}
	viewPath := filepath.Join(widgetDir, "views", "widget.html")
	if err := os.WriteFile(viewPath, []byte("<div>Initial Clock View</div>"), 0644); err != nil {
		t.Fatalf("failed to write widget view: %v", err)
	}

	initialYAML := `
host: "127.0.0.1"
port: 0
timezone: "UTC"
display:
  widgets:
    - id: clock-instance
      type: custom-clock
      dimensions: [6, 2]
`
	if err := os.WriteFile(configPath, []byte(initialYAML), 0644); err != nil {
		t.Fatalf("failed to write initial config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	readyChan := make(chan struct{}, 1)
	hupChan := make(chan os.Signal, 1)
	reloadedChan := make(chan struct{}, 1)
	addrChan := make(chan string, 1)
	var stdout, stderr bytes.Buffer

	errChan := make(chan error, 1)
	go func() {
		errChan <- RunWithReady(
			ctx,
			[]string{configPath},
			&stdout,
			&stderr,
			readyChan,
			WithAddrChan(addrChan),
			WithHupChan(hupChan),
			WithOnReload(func() {
				select {
				case reloadedChan <- struct{}{}:
				default:
				}
			}),
		)
	}()

	select {
	case <-readyChan:
	case err := <-errChan:
		t.Fatalf("RunWithReady failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ready signal")
	}

	addr := <-addrChan
	baseURL := "http://" + addr

	// Connect to /api/events SSE
	sseCtx, sseCancel := context.WithCancel(context.Background())
	defer sseCancel()

	req, err := http.NewRequestWithContext(sseCtx, http.MethodGet, baseURL+"/api/events", nil)
	if err != nil {
		t.Fatalf("failed to create SSE request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to connect to /api/events: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	sseEvents := make(chan struct {
		eventType string
		data      string
	}, 20)

	go func() {
		scanner := bufio.NewScanner(resp.Body)
		var currentEvent string
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "event: ") {
				currentEvent = strings.TrimPrefix(line, "event: ")
			} else if strings.HasPrefix(line, "data: ") {
				select {
				case sseEvents <- struct {
					eventType string
					data      string
				}{eventType: currentEvent, data: strings.TrimPrefix(line, "data: ")}:
				case <-sseCtx.Done():
					return
				}
			}
		}
	}()

	// Wait for initial SSE hydration
	select {
	case <-sseEvents:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initial SSE event")
	}

	// Verify initial render
	renderResp, err := http.Get(baseURL + "/api/widgets/clock-instance/render")
	if err != nil {
		t.Fatalf("failed to render initial widget: %v", err)
	}
	renderBody, err := io.ReadAll(renderResp.Body)
	_ = renderResp.Body.Close()
	if err != nil {
		t.Fatalf("failed to read render body: %v", err)
	}
	if string(renderBody) != "<div>Initial Clock View</div>" {
		t.Fatalf("unexpected initial render: %s", string(renderBody))
	}

	// Update widget template
	if err := os.WriteFile(viewPath, []byte("<div>Hot Reloaded Clock View</div>"), 0644); err != nil {
		t.Fatalf("failed to update widget.html: %v", err)
	}

	// Send SIGHUP
	hupChan <- syscall.SIGHUP

	select {
	case <-reloadedChan:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SIGHUP reload completion")
	}

	// Verify widget.reload SSE event received
	var receivedWidgetReload bool
	timeout := time.After(2 * time.Second)
	for !receivedWidgetReload {
		select {
		case evt := <-sseEvents:
			if evt.eventType == "widget.reload" && strings.Contains(evt.data, "custom-clock") {
				receivedWidgetReload = true
			}
		case <-timeout:
			t.Fatal("timed out waiting for widget.reload SSE event")
		}
	}

	// Verify updated render without server restart
	renderResp2, err := http.Get(baseURL + "/api/widgets/clock-instance/render")
	if err != nil {
		t.Fatalf("failed to render updated widget: %v", err)
	}
	renderBody2, err := io.ReadAll(renderResp2.Body)
	_ = renderResp2.Body.Close()
	if err != nil {
		t.Fatalf("failed to read updated render body: %v", err)
	}
	if string(renderBody2) != "<div>Hot Reloaded Clock View</div>" {
		t.Fatalf("expected hot reloaded HTML, got: %s", string(renderBody2))
	}

	sseCancel()
	cancel()
	<-errChan
}

func TestRun_SIGHUP_HostPortChange_LogsRestartRequired(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "mirrormere.yaml")
	initialYAML := `
host: "127.0.0.1"
port: 0
timezone: "UTC"
display:
  widgets:
    - id: spacer-1
      type: spacer
      dimensions: [6, 2]
`
	if err := os.WriteFile(configPath, []byte(initialYAML), 0644); err != nil {
		t.Fatalf("failed to write initial config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	readyChan := make(chan struct{}, 1)
	hupChan := make(chan os.Signal, 1)
	reloadedChan := make(chan struct{}, 1)
	addrChan := make(chan string, 1)
	var stdout, stderr bytes.Buffer

	errChan := make(chan error, 1)
	go func() {
		errChan <- RunWithReady(
			ctx,
			[]string{configPath},
			&stdout,
			&stderr,
			readyChan,
			WithAddrChan(addrChan),
			WithHupChan(hupChan),
			WithOnReload(func() {
				select {
				case reloadedChan <- struct{}{}:
				default:
				}
			}),
		)
	}()

	select {
	case <-readyChan:
	case err := <-errChan:
		t.Fatalf("RunWithReady failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ready signal")
	}

	addr := <-addrChan

	// Update config on disk with new port and host
	updatedYAML := `
host: "127.0.0.2"
port: 9090
timezone: "UTC"
display:
  widgets:
    - id: spacer-1
      type: spacer
      dimensions: [6, 2]
`
	if err := os.WriteFile(configPath, []byte(updatedYAML), 0644); err != nil {
		t.Fatalf("failed to write updated config: %v", err)
	}

	// Send SIGHUP via injected mock channel
	hupChan <- syscall.SIGHUP

	select {
	case <-reloadedChan:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SIGHUP reload completion")
	}

	// Verify the restart required warning is logged to stderr
	errStr := stderr.String()
	if !strings.Contains(errStr, "host or port changed in configuration; server restart required for changes to take effect") {
		t.Errorf("expected restart required warning in stderr, got: %s", errStr)
	}
	if !strings.Contains(errStr, "bound_host=127.0.0.1") || !strings.Contains(errStr, "new_host=127.0.0.2") {
		t.Errorf("expected bound_host and new_host in warning, got: %s", errStr)
	}
	if !strings.Contains(errStr, "new_port=9090") {
		t.Errorf("expected new_port=9090 in warning, got: %s", errStr)
	}

	// Verify server remains running on the original bound address
	resp, err := http.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatalf("expected server to remain reachable on original address, got: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	close(hupChan)
	cancel()
	<-errChan
}


