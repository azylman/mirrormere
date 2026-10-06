package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
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
		"/config/config.yaml",
		"/share/aerial-config/services/kiosk/cast-watcher.yaml",
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
	fs := flag.NewFlagSet("cast-watcher", flag.ContinueOnError)
	fs.SetOutput(stderr)

	fileCfg, err := loadConfigFile("")
	if err != nil {
		return err
	}

	defaultChromecast := os.Getenv("CHROMECAST_ADDR")
	if defaultChromecast == "" {
		defaultChromecast = os.Getenv("CHROMECAST_IP")
	}
	if defaultChromecast == "" && fileCfg != nil && fileCfg.ChromecastAddr != "" {
		defaultChromecast = fileCfg.ChromecastAddr
	}
	if defaultChromecast == "" {
		defaultChromecast = "192.168.1.50:8009"
	}
	if !strings.Contains(defaultChromecast, ":") {
		defaultChromecast += ":8009"
	}

	defaultCoreURL := os.Getenv("CORE_URL")
	if defaultCoreURL == "" && fileCfg != nil && fileCfg.CoreURL != "" {
		defaultCoreURL = fileCfg.CoreURL
	}
	if defaultCoreURL == "" {
		defaultCoreURL = "http://mirrormere-core:8080"
	}

	defaultStreamURL := os.Getenv("STREAM_URL")
	if defaultStreamURL == "" && fileCfg != nil && fileCfg.StreamURL != "" {
		defaultStreamURL = fileCfg.StreamURL
	}
	if defaultStreamURL == "" {
		defaultStreamURL = "http://localhost:1984/api/webrtc?src=cast"
	}

	defaultPort := 8090
	if envPort := os.Getenv("CONTROL_PORT"); envPort != "" {
		if p, err := strconv.Atoi(envPort); err == nil && p >= 0 {
			defaultPort = p
		}
	} else if envPort := os.Getenv("PORT"); envPort != "" {
		if p, err := strconv.Atoi(envPort); err == nil && p >= 0 {
			defaultPort = p
		}
	} else if fileCfg != nil && fileCfg.ControlPort != nil {
		defaultPort = *fileCfg.ControlPort
	}

	defaultControlURL := os.Getenv("CONTROL_URL")
	if defaultControlURL == "" {
		defaultControlURL = os.Getenv("CAST_WATCHER_CONTROL_URL")
	}
	if defaultControlURL == "" && fileCfg != nil && fileCfg.ControlURL != "" {
		defaultControlURL = fileCfg.ControlURL
	}
	if defaultControlURL == "" {
		defaultControlURL = fmt.Sprintf("http://cast-watcher:%d/action", defaultPort)
	}

	defaultConfigPath := os.Getenv("CONFIG_PATH")
	configFlag := fs.String("config", defaultConfigPath, "Path to YAML configuration file")
	chromecastFlag := fs.String("chromecast", defaultChromecast, "Chromecast LAN host:port")
	coreURLFlag := fs.String("core-url", defaultCoreURL, "Mirrormere Core API base URL")
	streamURLFlag := fs.String("stream-url", defaultStreamURL, "Stream URL for WebRTC playback")
	controlURLFlag := fs.String("control-url", defaultControlURL, "Webhook control URL forwarded to Core")
	portFlag := fs.Int("port", defaultPort, "HTTP server bind port")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	flagsSet := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) {
		flagsSet[f.Name] = true
	})

	if *configFlag != "" && *configFlag != defaultConfigPath {
		if flagCfg, err := loadConfigFile(*configFlag); err == nil && flagCfg != nil {
			if !flagsSet["chromecast"] && flagCfg.ChromecastAddr != "" {
				*chromecastFlag = flagCfg.ChromecastAddr
			}
			if !flagsSet["core-url"] && flagCfg.CoreURL != "" {
				*coreURLFlag = flagCfg.CoreURL
			}
			if !flagsSet["stream-url"] && flagCfg.StreamURL != "" {
				*streamURLFlag = flagCfg.StreamURL
			}
			if !flagsSet["control-url"] && flagCfg.ControlURL != "" {
				*controlURLFlag = flagCfg.ControlURL
			}
			if !flagsSet["port"] && flagCfg.ControlPort != nil {
				*portFlag = *flagCfg.ControlPort
			}
		} else if err != nil {
			return err
		}
	}

	chromecastAddr := *chromecastFlag
	if !strings.Contains(chromecastAddr, ":") {
		chromecastAddr += ":8009"
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
			CoreURL:        *coreURLFlag,
			StreamURL:      *streamURLFlag,
			ControlURL:     *controlURLFlag,
			Logger:         logger,
		})
	}
	defer client.Close()

	client.Start(ctx)

	serverCfg := ServerConfig{
		Host:   "0.0.0.0",
		Port:   *portFlag,
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
