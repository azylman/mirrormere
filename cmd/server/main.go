package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/azylman/mirrormere/internal/server"
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
		_, _ = fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		exitFunc(1)
	}
}

// Run executes the server application with the supplied arguments and context.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	return RunWithReady(ctx, args, stdout, stderr, nil)
}

// RunWithReady executes the server and signals readyChan when the listener is active.
func RunWithReady(ctx context.Context, args []string, stdout, stderr io.Writer, readyChan chan<- struct{}) error {
	fs := flag.NewFlagSet("server", flag.ContinueOnError)
	fs.SetOutput(stderr)

	defaultHost := os.Getenv("HOST")
	if defaultHost == "" {
		defaultHost = server.DefaultHost
	}

	defaultPort := server.DefaultPort
	if envPort := os.Getenv("PORT"); envPort != "" {
		if p, err := strconv.Atoi(envPort); err == nil && p >= 0 {
			defaultPort = p
		}
	}

	hostFlag := fs.String("host", defaultHost, "Bind host address")
	portFlag := fs.Int("port", defaultPort, "Bind port number")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	portVal := *portFlag
	if portVal == 0 {
		portVal = -1
	}

	cfg := server.Config{
		Host: *hostFlag,
		Port: portVal,
	}
	cfg.ApplyDefaults()

	srv := server.New(cfg)

	bindAddr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	listener, err := net.Listen("tcp", bindAddr)
	if err != nil {
		return fmt.Errorf("failed to bind address %s: %w", bindAddr, err)
	}

	if _, err := fmt.Fprintf(stdout, "Mirrormere daemon listening on %s (v%s)\n", listener.Addr().String(), server.Version); err != nil {
		_ = listener.Close()
		return err
	}

	if readyChan != nil {
		readyChan <- struct{}{}
	}

	errChan := make(chan error, 1)
	go func() {
		errChan <- srv.Serve(listener)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errChan:
		return err
	}
}
