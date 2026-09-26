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
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/azylman/mirrormere/internal/audio"
	"github.com/azylman/mirrormere/internal/config"
	"github.com/azylman/mirrormere/internal/display"
	"github.com/azylman/mirrormere/internal/events"
	"github.com/azylman/mirrormere/internal/provider"
	"github.com/azylman/mirrormere/internal/render"
	"github.com/azylman/mirrormere/internal/rotation"
	"github.com/azylman/mirrormere/internal/server"
	"github.com/azylman/mirrormere/internal/tasks"
	"github.com/azylman/mirrormere/internal/video"
	"github.com/azylman/mirrormere/internal/voice"
	"github.com/azylman/mirrormere/internal/watcher"
	"github.com/azylman/mirrormere/internal/widget"
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

// Option configures RunWithReady execution.
type Option func(*runConfig)

type runConfig struct {
	addrChan chan<- string
}

// WithAddrChan supplies a channel that receives the bound listener address.
func WithAddrChan(ch chan<- string) Option {
	return func(rc *runConfig) {
		rc.addrChan = ch
	}
}

// Run executes the server application with the supplied arguments and context.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	return RunWithReady(ctx, args, stdout, stderr, nil)
}

// RunWithReady executes the server and signals readyChan when the listener is active.
func RunWithReady(ctx context.Context, args []string, stdout, stderr io.Writer, readyChan chan<- struct{}, opts ...Option) error {
	var rcfg runConfig
	for _, opt := range opts {
		opt(&rcfg)
	}

	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

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

	defaultDB := os.Getenv("DB_PATH")
	if defaultDB == "" {
		defaultDB = "/data/lists.db"
	}

	defaultConfig := os.Getenv("CONFIG_PATH")
	if defaultConfig == "" {
		defaultConfig = config.DefaultConfigPath
	}

	hostFlag := fs.String("host", defaultHost, "Bind host address")
	portFlag := fs.Int("port", defaultPort, "Bind port number")
	configFlag := fs.String("config", defaultConfig, "Path to configuration file")
	builtinFlag := fs.String("builtin-widgets", "", "Path to builtin widgets directory (default: auto-detected)")
	customFlag := fs.String("custom-widgets", "", "Path to custom widgets directory (default: /config/widgets)")
	dbFlag := fs.String("db", defaultDB, "Path to SQLite tasks database")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	// 1. Resolve widget directories and initialize loader
	builtinDir := *builtinFlag
	if builtinDir == "" {
		builtinDir = widget.DefaultBuiltinDir
		candidates := []string{
			"/app/widgets",
			"widgets",
			"../../widgets",
		}
		for _, c := range candidates {
			if fi, err := os.Stat(c); err == nil && fi.IsDir() {
				builtinDir = c
				break
			}
		}
	}

	customDir := *customFlag
	if customDir == "" {
		customDir = widget.DefaultCustomDir
	}

	loader := widget.NewLoader(builtinDir, customDir)

	// 2. Resolve configuration YAML
	configPath := *configFlag
	var initialYAML []byte
	if configPath != "" {
		if data, err := os.ReadFile(configPath); err == nil {
			initialYAML = data
		} else if *configFlag != defaultConfig {
			return fmt.Errorf("failed to read configuration file %q: %w", configPath, err)
		}
	}
	if initialYAML == nil {
		initialYAML = []byte("timezone: UTC\ndisplay:\n  widgets:\n    - id: default-spacer\n      type: spacer\n      dimensions: [6, 2]\n")
		configPath = ""
	}

	// 3. Initialize LKGC Configuration Manager
	configManager, err := config.NewManager(initialYAML, loader, os.Getenv, logger)
	if err != nil {
		return fmt.Errorf("failed to initialize configuration manager: %w", err)
	}
	snapshot := configManager.CurrentSnapshot()

	// 4. State Provider & Event Hub
	stateProvider := events.NewInMemoryStateProvider(configManager)
	hub := events.NewHub(events.HubConfig{}, stateProvider, logger)
	defer hub.Close()
	eventsHandler := events.Handler(hub)

	// 5. Initialize Tasks SQLite Store & Provider Registry
	dbPath := *dbFlag
	tasksStore, err := tasks.NewSQLiteStore(dbPath)
	if err != nil {
		logger.Warn("failed to initialize persistent tasks store; falling back to in-memory store", "path", dbPath, "error", err)
		fallbackStore, fallbackErr := tasks.NewSQLiteStore(":memory:")
		if fallbackErr != nil {
			return fmt.Errorf("failed to initialize fallback in-memory tasks store: %w", fallbackErr)
		}
		tasksStore = fallbackStore
	}
	defer func() {
		if err := tasksStore.Close(); err != nil {
			logger.Warn("failed to close tasks store", "error", err)
		}
	}()
	listsHandler := server.NewDefaultListsHandler(tasksStore)

	reg := provider.NewRegistry()
	reg.Register("tasks", func() provider.Provider {
		return provider.NewTasksProviderWithStore(tasksStore)
	})

	// 6. Audio Coordinator & Handler
	audioCoord := audio.NewCoordinator(hub, stateProvider)
	audioHandler := server.NewDefaultAudioHandler(audioCoord)

	// 7. Video Coordinator & Handler
	videoCoord := video.NewCoordinator(hub, video.WithSink(stateProvider))
	defer videoCoord.Close()
	videoHandler := server.NewDefaultVideoHandler(videoCoord)

	// 8. Voice Coordinator, Hub & Handler
	voiceCoord := voice.NewCoordinator(hub, stateProvider)
	var voiceHubCfg *config.VoiceHubConfig
	if snapshot != nil && snapshot.Config != nil {
		voiceHubCfg = snapshot.Config.VoiceHub
	}
	voiceHub := voice.NewHub(voiceHubCfg, voiceCoord)
	voiceHandler := server.NewDefaultVoiceHandler(voiceCoord, voiceHub)

	// 9. Rotation Coordinator & Screen Handler
	rotationCoord := rotation.NewCoordinator(rotation.Config{
		Broadcaster: hub,
		Logger:      logger,
	}, snapshot)
	defer rotationCoord.Stop()
	hub.SetRotationCoordinator(rotationCoord)
	screenHandler := rotation.NewHandler(rotationCoord)

	// 10. Provider Coordinator & Push Handler
	providerCoord := provider.NewCoordinator(provider.CoordinatorConfig{
		Registry:    reg,
		Broadcaster: hub,
		StateSink:   stateProvider,
		Logger:      logger,
	}, snapshot)
	defer func() {
		if err := providerCoord.Stop(); err != nil {
			logger.Warn("failed to stop provider coordinator", "error", err)
		}
	}()
	hub.SetProviderCoordinator(providerCoord)
	pushHandler := provider.NewPushHandler(providerCoord, logger)

	// 11. Render & Display Handlers
	renderEngine := render.NewEngine(loader, stateProvider)
	renderHandler := render.NewHandler(renderEngine, loader)
	displayHandler := display.NewHandler(
		display.WithTimezoneProvider(func() string {
			if snap := stateProvider.CurrentSnapshot(); snap != nil && snap.Config != nil {
				return snap.Config.Timezone
			}
			return "UTC"
		}),
	)

	// 12. Optional Filesystem Watcher
	if configPath != "" {
		configDir := filepath.Dir(configPath)
		if fi, err := os.Stat(configDir); err == nil && fi.IsDir() {
			w := watcher.New(watcher.Config{
				ConfigDir:         configDir,
				ConfigFileName:    filepath.Base(configPath),
				BuiltinWidgetsDir: builtinDir,
				CustomWidgetsDir:  customDir,
			}, configManager, loader, hub, logger)
			if err := w.Start(ctx); err == nil {
				defer func() {
					if err := w.Close(); err != nil {
						logger.Warn("failed to close watcher", "error", err)
					}
				}()
			}
		}
	}

	// 13. Assemble Server Configuration
	portVal := *portFlag
	if portVal == 0 {
		portVal = -1
	}

	cfg := server.Config{
		Host:           *hostFlag,
		Port:           portVal,
		EventsHandler:  eventsHandler,
		ScreenHandler:  screenHandler,
		RenderHandler:  renderHandler,
		PushHandler:    pushHandler,
		DisplayHandler: displayHandler,
		ListsHandler:   listsHandler,
		AudioHandler:   audioHandler,
		VideoHandler:   videoHandler,
		VoiceHandler:   voiceHandler,
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

	if rcfg.addrChan != nil {
		rcfg.addrChan <- listener.Addr().String()
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
		// Close hub first to close subscriber channels and immediately unblock active SSE streaming loops
		hub.Close()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errChan:
		return err
	}
}
