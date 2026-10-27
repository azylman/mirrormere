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
		_, _ = fmt.Fprintf(os.Stderr, "hass-proxy error: %v\n", err)
		exitFunc(1)
	}
}

// Run executes the Home Assistant fast-path proxy sidecar.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	return RunWithReady(ctx, args, stdout, stderr, nil, nil, nil)
}

// RunWithReady executes the sidecar and signals readyChan when the HTTP listener is active.
func RunWithReady(
	ctx context.Context,
	args []string,
	stdout, stderr io.Writer,
	readyChan chan<- struct{},
	listenerOverride net.Listener,
	clientOverride HAClient,
) error {
	var configPath string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-h" || arg == "-help" || arg == "--help" {
			_, err := fmt.Fprintln(stderr, "Usage: hass-proxy [-config <path>]")
			return err
		}
		if (arg == "-config" || arg == "--config") && i+1 < len(args) {
			configPath = args[i+1]
			i++
		} else if strings.HasPrefix(arg, "-config=") {
			configPath = strings.TrimPrefix(arg, "-config=")
		} else if strings.HasPrefix(arg, "--config=") {
			configPath = strings.TrimPrefix(arg, "--config=")
		} else if !strings.HasPrefix(arg, "-") && configPath == "" {
			configPath = arg
		}
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	logger.Info("starting home assistant fast-path proxy", "config", cfg.String())

	var client HAClient
	if clientOverride != nil {
		client = clientOverride
	} else {
		client = NewHAClient(cfg.HomeAssistant, logger)
	}

	httpServer := BuildHTTPServer(cfg.Server, NewServer(client, logger))
	logStdoutInit(stdout, logger)

	var listener net.Listener
	if listenerOverride != nil {
		listener = listenerOverride
	} else if l, err := (&net.ListenConfig{}).Listen(ctx, "tcp", httpServer.Addr); err != nil {
		return fmt.Errorf("failed to listen on %s: %w", httpServer.Addr, err)
	} else {
		listener = l
	}
	defer closeListenerSafe(listener, logger)

	if readyChan != nil {
		close(readyChan)
	}

	errChan := make(chan error, 1)
	go func() {
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errChan <- err
		}
		close(errChan)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("error during server shutdown: %w", err)
		}
		return nil
	case err := <-errChan:
		return err
	}
}

func closeListenerSafe(l net.Listener, logger *slog.Logger) {
	if l == nil {
		return
	}
	if err := l.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		logger.Debug("listener close", "error", err)
	}
}

func logStdoutInit(stdout io.Writer, logger *slog.Logger) {
	if stdout != nil {
		if _, err := fmt.Fprintln(stdout, "hass-proxy initialized"); err != nil {
			logger.Warn("stdout write failed", "error", err)
		}
	}
}
