package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

var (
	exitFunc = os.Exit
	argsHook []string
)

func main() {
	args := os.Args[1:]
	if argsHook != nil {
		args = argsHook
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := Run(ctx, args, os.Stdout, os.Stderr); err != nil && !errors.Is(err, http.ErrServerClosed) {
		_, _ = fmt.Fprintf(os.Stderr, "cast-watcher error: %v\n", err)
		exitFunc(1)
	}
}

// Run executes the cast-watcher sidecar application with args and context.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	return RunWithReady(ctx, args, stdout, stderr, nil, nil)
}

// Config represents declarative configuration for cast-watcher.
type Config struct {
	ChromecastAddr string `yaml:"chromecast_addr"`
	CoreURL        string `yaml:"core_url"`
	StreamURL      string `yaml:"stream_url"`
	ControlPort    *int   `yaml:"control_port"`
	ControlURL     string `yaml:"control_url"`
}

func loadConfigFile(path string) (*Config, error) {
	var candidates []string
	if path != "" {
		candidates = append(candidates, path)
	}
	if envPath := os.Getenv("CONFIG_PATH"); envPath != "" {
		candidates = append(candidates, envPath)
	}
	candidates = append(candidates,
		"/config/cast-watcher.yaml",
		"/config/cast-watcher.yml",
		"/app/config.yaml",
	)

	for _, c := range candidates {
		data, err := os.ReadFile(c)
		if err == nil {
			var cfg Config
			if err := yaml.Unmarshal(data, &cfg); err != nil {
				return nil, fmt.Errorf("failed to parse config %q: %w", c, err)
			}
			return &cfg, nil
		}
	}
	return nil, nil
}

// RunWithReady executes cast-watcher and signals readyChan when the HTTP listener is active.
func RunWithReady(
	ctx context.Context,
	args []string,
	stdout, stderr io.Writer,
	readyChan chan<- struct{},
	clientOverride *CastClient,
) error {
	var configPath string
	for _, arg := range args {
		if arg == "-h" || arg == "-help" || arg == "--help" {
			if _, err := fmt.Fprintln(stderr, "Usage: cast-watcher [config-path]"); err != nil {
				return err
			}
			return nil
		}
		if strings.HasPrefix(arg, "-") {
			return fmt.Errorf("flags are not supported; configure via config file: %s", arg)
		}
		if configPath == "" {
			configPath = arg
		}
	}

	cfg, err := loadConfigFile(configPath)
	if err != nil {
		return err
	}

	if cfg == nil || strings.TrimSpace(cfg.ControlURL) == "" {
		return errors.New("control_url is required in configuration")
	}

	chromecastAddr := strings.TrimSpace(cfg.ChromecastAddr)
	if chromecastAddr == "" {
		chromecastAddr = "10.0.0.50:8009"
	}
	if !strings.Contains(chromecastAddr, ":") {
		chromecastAddr += ":8009"
	}

	coreURL := strings.TrimSpace(cfg.CoreURL)
	if coreURL == "" {
		coreURL = "http://mirrormere-core:8080"
	}

	streamURL := strings.TrimSpace(cfg.StreamURL)
	if streamURL == "" {
		streamURL = "http://localhost:1984/api/webrtc?src=cast"
	}

	controlPort := 8090
	if cfg.ControlPort != nil {
		controlPort = *cfg.ControlPort
	}

	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	var client *CastClient
	if clientOverride != nil {
		client = clientOverride
	} else {
		client = NewCastClient(ClientConfig{
			ChromecastAddr: chromecastAddr,
			CoreURL:        coreURL,
			StreamURL:      streamURL,
			ControlURL:     cfg.ControlURL,
			Logger:         logger,
		})
	}
	defer client.Close()

	client.Start(ctx)

	serverCfg := ServerConfig{
		Host:   "0.0.0.0",
		Port:   controlPort,
		Logger: logger,
	}
	server := NewActionServer(serverCfg, client)

	bindAddr := fmt.Sprintf("%s:%d", serverCfg.Host, serverCfg.Port)
	var lc net.ListenConfig
	listener, err := lc.Listen(context.Background(), "tcp", bindAddr)
	if err != nil {
		return fmt.Errorf("failed to bind address %s: %w", bindAddr, err)
	}
	defer listener.Close()

	if _, err := fmt.Fprintf(stdout, "Mirrormere Cast Watcher listening on %s (Chromecast: %s)\n", listener.Addr().String(), chromecastAddr); err != nil {
		return err
	}

	if readyChan != nil {
		readyChan <- struct{}{}
	}

	errChan := make(chan error, 1)
	go func() {
		errChan <- server.Serve(listener)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errChan:
		return err
	}
}
