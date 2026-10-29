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
)

type failWriter struct{}

func (failWriter) Write(p []byte) (n int, err error) {
	return 0, errors.New("simulated stdout write failure")
}

func writeTestYAML(t *testing.T, dir, filename, content string) string {
	t.Helper()
	p := filepath.Join(dir, filename)
	if err := os.WriteFile(p, []byte(strings.TrimSpace(content)), 0o600); err != nil {
		t.Fatalf("failed to write test yaml: %v", err)
	}
	return p
}

func TestRun_ConfigAndValidation(t *testing.T) {
	tmpDir := t.TempDir()
	validCfg := writeTestYAML(t, tmpDir, "valid.yaml", `
chromecast_addr: "10.0.0.50:8009"
core_url: "http://core.internal:8080"
stream_url: "http://core.internal:1984/stream"
control_port: 0
control_url: "http://control.internal:9999/action"
`)
	invalidYAML := writeTestYAML(t, tmpDir, "invalid.yaml", "invalid: yaml: [")
	noControlURL := writeTestYAML(t, tmpDir, "no_ctrl.yaml", `chromecast_addr: "10.0.0.50:8009"`)

	for _, arg := range []string{"-h", "-help", "--help"} {
		var stderr bytes.Buffer
		if err := Run(context.Background(), []string{arg}, &bytes.Buffer{}, &stderr); err != nil || !strings.Contains(stderr.String(), "Usage:") {
			t.Fatalf("expected usage message for %s, err: %v", arg, err)
		}
	}
	if err := Run(context.Background(), []string{"-invalid-flag"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "flags are not supported") {
		t.Fatalf("expected unsupported flag error, got: %v", err)
	}
	if err := Run(context.Background(), nil, &bytes.Buffer{}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "control_url is required") {
		t.Fatalf("expected error when no config file exists, got: %v", err)
	}
	if _, err := loadConfigFile(invalidYAML); err == nil {
		t.Fatal("expected error loading invalid YAML")
	}
	if err := RunWithReady(context.Background(), []string{invalidYAML}, &bytes.Buffer{}, &bytes.Buffer{}, nil, nil); err == nil {
		t.Fatal("expected RunWithReady error for invalid YAML")
	}
	if err := RunWithReady(context.Background(), []string{noControlURL}, &bytes.Buffer{}, &bytes.Buffer{}, nil, nil); err == nil || !strings.Contains(err.Error(), "control_url is required") {
		t.Fatalf("expected control_url required error, got: %v", err)
	}
	if err := RunWithReady(context.Background(), []string{validCfg}, failWriter{}, &bytes.Buffer{}, nil, nil); err == nil || !strings.Contains(err.Error(), "simulated stdout write failure") {
		t.Fatalf("expected stdout write error, got: %v", err)
	}
	for _, ex := range []string{"cast-watcher.yaml", "cast-watcher.client.yaml"} {
		cfgPath := filepath.Join("..", "..", "deploy", "examples", ex)
		cfg, err := loadConfigFile(cfgPath)
		if err != nil || cfg == nil || cfg.ControlURL == "" {
			t.Fatalf("example config %s invalid: err=%v cfg=%+v", ex, err, cfg)
		}
	}
}

func TestRun_Lifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := writeTestYAML(t, tmpDir, "life.yaml", "control_url: \"http://cast-watcher:8090/action\"\ncontrol_port: 0\n")
	ctx, cancel := context.WithCancel(context.Background())
	readyCh := make(chan struct{}, 1)
	var stdout, stderr bytes.Buffer
	client := NewCastClient(ClientConfig{
		ChromecastAddr: "127.0.0.1:8009",
		Dialer: func(ctx context.Context, network, addr string) (net.Conn, error) {
			c, _ := net.Pipe()
			return c, nil
		},
	})
	errCh := make(chan error, 1)
	go func() {
		errCh <- RunWithReady(ctx, []string{cfgPath}, &stdout, &stderr, readyCh, client)
	}()
	<-readyCh
	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("expected clean shutdown, got %v", err)
	}

	for _, tc := range []struct {
		yaml string
		want string
	}{
		{"chromecast_addr: \"10.0.0.5\"\ncontrol_url: \"http://cast-watcher:8090/action\"\ncontrol_port: 0\n", "10.0.0.5:8009"},
		{"control_url: \"http://cast-watcher:8090/action\"\ncontrol_port: 0\n", "10.0.0.50:8009"},
	} {
		p := writeTestYAML(t, tmpDir, "sub.yaml", tc.yaml)
		ctxSub, cancelSub := context.WithCancel(context.Background())
		var subOut, subErr bytes.Buffer
		subDone := make(chan error, 1)
		rCh := make(chan struct{}, 1)
		go func() {
			subDone <- RunWithReady(ctxSub, []string{p}, &subOut, &subErr, rCh, nil)
		}()
		<-rCh
		cancelSub()
		<-subDone
		if !strings.Contains(subOut.String(), tc.want) {
			t.Fatalf("expected %s in output, got %s", tc.want, subOut.String())
		}
	}

	t.Setenv("CONFIG_PATH", cfgPath)
	ctxEnv, cancelEnv := context.WithCancel(context.Background())
	rChEnv := make(chan struct{}, 1)
	doneEnv := make(chan error, 1)
	go func() {
		doneEnv <- RunWithReady(ctxEnv, nil, &bytes.Buffer{}, &bytes.Buffer{}, rChEnv, client)
	}()
	<-rChEnv
	cancelEnv()
	<-doneEnv

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer l.Close()
	cfgBindFail := writeTestYAML(t, tmpDir, "bindfail.yaml", fmt.Sprintf("control_url: \"http://cast-watcher:8090/action\"\ncontrol_port: %d\n", l.Addr().(*net.TCPAddr).Port))
	if err := RunWithReady(context.Background(), []string{cfgBindFail}, &bytes.Buffer{}, &bytes.Buffer{}, nil, nil); err == nil || !strings.Contains(err.Error(), "failed to bind address") {
		t.Fatalf("expected bind failure error, got: %v", err)
	}
}

func TestMain_Execution(t *testing.T) {
	oldArgsHook, oldExitFunc := argsHook, exitFunc
	defer func() {
		argsHook = oldArgsHook
		exitFunc = oldExitFunc
	}()

	argsHook = []string{"-help"}
	main()

	argsHook = []string{"-invalid-arg"}
	exitCode := 0
	exitFunc = func(code int) { exitCode = code }
	main()
	if exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", exitCode)
	}
}
