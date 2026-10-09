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
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/azylman/mirrormere/internal/audio"
	"github.com/azylman/mirrormere/internal/chatlog"
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
	hupChan  <-chan os.Signal
	onReload func()
}

// WithAddrChan supplies a channel that receives the bound listener address.
func WithAddrChan(ch chan<- string) Option {
	return func(rc *runConfig) {
		rc.addrChan = ch
	}
}

// WithHupChan injects a custom SIGHUP signal channel for testing.
func WithHupChan(ch <-chan os.Signal) Option {
	return func(rc *runConfig) {
		rc.hupChan = ch
	}
}

// WithOnReload injects a callback invoked after a SIGHUP reload attempt completes.
func WithOnReload(fn func()) Option {
	return func(rc *runConfig) {
		rc.onReload = fn
	}
}

// householdNowFunc returns a clock for the render engine that reports the current instant in
// the household's configured timezone rather than the server process's own zone, which is
// UTC in the production container. Without this, BuildFamilyView's t.In(now.Location()) calls
// in internal/render/family.go are a no-op (now.Location() would already be UTC), so a UTC
// event near local midnight would still land on the wrong day.
// chatLogWidgetIDs lists the chat-log widget instances in the active configuration.
func chatLogWidgetIDs(snap *config.Snapshot) []string {
	if snap == nil || snap.Config == nil {
		return nil
	}
	var ids []string
	for _, w := range snap.Config.Display.Widgets {
		if w.Type == "chat-log" {
			ids = append(ids, w.ID)
		}
	}
	return ids
}

// chatLogPusher is the slice of the provider coordinator the chat-log hook needs.
type chatLogPusher interface {
	PushUpdate(widgetID string, data any) (provider.WidgetPayload, error)
}

// newChatLogHook returns the voice hub's on-change hook. The pusher is looked up
// lazily because the provider coordinator is built after the hub.
func newChatLogHook(sp render.StateSnapshotProvider, pusher func() chatLogPusher, store *chatlog.Store) func() {
	return func() { pushChatLogUpdates(sp, pusher(), store) }
}

// pushChatLogUpdates runs every chat-log widget through the normal push path
// (cache update + widget.update broadcast) after the voice hub records a turn.
// Each display then re-renders through GET /api/widgets/{id}/render?node=<id>
// and gets its own conversation. The cached copy is the most-recent-node view.
func pushChatLogUpdates(sp render.StateSnapshotProvider, pusher chatLogPusher, store *chatlog.Store) {
	for _, id := range chatLogWidgetIDs(sp.CurrentSnapshot()) {
		if _, err := pusher.PushUpdate(id, store.View(id, "")); err != nil {
			slog.Warn("chat-log widget update failed", "widget_id", id, "error", err)
		}
	}
}

// chatLogNodeData supplies per-display render data for chat-log widgets: the
// requesting display's own conversation, chosen from the ?node= it passed.
func chatLogNodeData(sp render.StateSnapshotProvider, store *chatlog.Store) func(widgetID, nodeID string) (any, bool) {
	return func(widgetID, nodeID string) (any, bool) {
		for _, id := range chatLogWidgetIDs(sp.CurrentSnapshot()) {
			if id == widgetID {
				return store.View(widgetID, nodeID), true
			}
		}
		return nil, false
	}
}

func householdNowFunc(sp render.StateSnapshotProvider) func() time.Time {
	return func() time.Time {
		now := time.Now()
		if sp == nil {
			return now
		}
		if snap := sp.CurrentSnapshot(); snap != nil && snap.Config != nil {
			return now.In(snap.Config.Location())
		}
		return now
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

	var configPath string
	for _, arg := range args {
		if arg == "-h" || arg == "-help" || arg == "--help" {
			if _, err := fmt.Fprintln(stderr, "Usage: mirrormere [config-path]"); err != nil {
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

	if configPath == "" {
		configPath = os.Getenv("CONFIG_PATH")
	}
	if configPath == "" {
		configPath = config.DefaultConfigPath
	}

	// 1. Resolve widget directories and initialize loader
	builtinDir := widget.DefaultBuiltinDir
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

	customDir := widget.DefaultCustomDir
	if configPath != "" {
		candidateDir := filepath.Join(filepath.Dir(configPath), "widgets")
		if fi, err := os.Stat(candidateDir); err == nil && fi.IsDir() {
			customDir = candidateDir
		}
	}

	loader := widget.NewLoader(builtinDir, customDir)

	// 2. Resolve configuration YAML
	var initialYAML []byte
	if configPath != "" {
		if data, err := os.ReadFile(configPath); err == nil {
			initialYAML = data
		} else if len(args) > 0 && configPath == args[0] {
			return fmt.Errorf("failed to read configuration file %q: %w", configPath, err)
		} else if envPath := os.Getenv("CONFIG_PATH"); envPath != "" && configPath == envPath {
			return fmt.Errorf("failed to read configuration file %q: %w", configPath, err)
		}
	}
	if initialYAML == nil {
		initialYAML = []byte("timezone: UTC\ndisplay:\n  widgets:\n    - id: default-spacer\n      type: spacer\n      dimensions: [6, 2]\n")
		configPath = ""
	}

	customCSSPath := render.DefaultCustomCSSPath
	if configPath != "" {
		customCSSPath = filepath.Join(filepath.Dir(configPath), "custom.css")
	}

	// 3. Initialize LKGC Configuration Manager
	configManager, err := config.NewManager(initialYAML, loader, os.Getenv, logger)
	if err != nil {
		return fmt.Errorf("failed to initialize configuration manager: %w", err)
	}
	snapshot := configManager.CurrentSnapshot()

	initialHost := server.DefaultHost
	initialPort := server.DefaultPort
	if snapshot != nil && snapshot.Config != nil {
		if snapshot.Config.Host != "" {
			initialHost = snapshot.Config.Host
		}
		if snapshot.Config.Port != nil {
			initialPort = *snapshot.Config.Port
		}
	}

	// 4. State Provider & Event Hub
	stateProvider := events.NewInMemoryStateProvider(configManager)
	hub := events.NewHub(events.HubConfig{}, stateProvider, logger)
	defer hub.Close()
	eventsHandler := events.Handler(hub)

	// 5. Initialize Tasks SQLite Store & Provider Registry
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "/data/lists.db"
	}
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

	// In-memory conversation store shared by the voice hub (writer) and the
	// chat-log provider (reader). Nothing is persisted.
	var chatLogCfg *config.ChatLogConfig
	if snapshot != nil && snapshot.Config != nil {
		chatLogCfg = snapshot.Config.ChatLog
	}
	chatStore := chatlog.NewStore(
		chatlog.WithTTL(chatLogCfg.GetTTL()),
		chatlog.WithMaxPerKey(chatLogCfg.GetMaxMessagesPerNode()),
	)

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
	var providerCoord *provider.ProviderCoordinator // assigned in step 10; the chat-log hook only runs once turns arrive
	var voiceHubCfg *config.VoiceHubConfig
	if snapshot != nil && snapshot.Config != nil {
		voiceHubCfg = snapshot.Config.VoiceHub
	}
	voiceHub := voice.NewHub(voiceHubCfg, voiceCoord, voice.WithChatLog(chatStore, newChatLogHook(stateProvider, func() chatLogPusher { return providerCoord }, chatStore)))
	voiceHandler := server.NewDefaultVoiceHandler(voiceCoord, voiceHub, server.WithVoiceMetrics(voice.DefaultMetrics()))

	// 9. Rotation Coordinator & Screen Handler
	rotationCoord := rotation.NewCoordinator(rotation.Config{
		Broadcaster: hub,
		Logger:      logger,
	}, snapshot)
	defer rotationCoord.Stop()
	hub.SetRotationCoordinator(rotationCoord)
	screenHandler := rotation.NewHandler(rotationCoord)

	// 10. Provider Coordinator & Push Handler
	providerCoord = provider.NewCoordinator(provider.CoordinatorConfig{
		Registry:    reg,
		Broadcaster: hub,
		StateSink:   stateProvider,
		Logger:      logger,
		ChatLog:     chatStore,
	}, snapshot)
	defer func() {
		if err := providerCoord.Stop(); err != nil {
			logger.Warn("failed to stop provider coordinator", "error", err)
		}
	}()
	hub.SetProviderCoordinator(providerCoord)
	pushHandler := provider.NewPushHandler(providerCoord, logger)

	// 11. Render & Display Handlers
	renderEngine := render.NewEngine(loader, stateProvider,
		render.WithNowFunc(householdNowFunc(stateProvider)),
		render.WithNodeDataFunc(chatLogNodeData(stateProvider, chatStore)),
	)
	renderHandler := render.NewHandler(renderEngine, loader, render.WithCustomCSSPath(customCSSPath))
	displayHandler := display.NewHandler(
		display.WithTimezoneProvider(func() string {
			if snap := stateProvider.CurrentSnapshot(); snap != nil && snap.Config != nil {
				return snap.Config.Timezone
			}
			return "UTC"
		}),
		display.WithHideCursorProvider(func() bool {
			if snap := stateProvider.CurrentSnapshot(); snap != nil && snap.Config != nil {
				return snap.Config.Display.HideCursor
			}
			return false
		}),
		display.WithRemoteURLProvider(func() string {
			if snap := stateProvider.CurrentSnapshot(); snap != nil && snap.Config != nil {
				return snap.Config.Display.RemoteURL
			}
			return ""
		}),
	)
	assetReloader := NewAssetReloader(customCSSPath, loader, builtinDir, customDir, renderEngine, configManager, hub, logger)

	// 12. SIGHUP Signal Listener for Live Configuration Reloads
	var hupChan <-chan os.Signal
	var stopHup func()
	if rcfg.hupChan != nil {
		hupChan = rcfg.hupChan
	} else {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGHUP)
		hupChan = ch
		stopHup = func() {
			signal.Stop(ch)
		}
	}
	if stopHup != nil {
		defer stopHup()
	}

	hupCtx, hupCancel := context.WithCancel(ctx)
	defer hupCancel()
	go func() {
		for {
			select {
			case <-hupCtx.Done():
				return
			case sig, ok := <-hupChan:
				if !ok {
					return
				}
				if sig == syscall.SIGHUP {
					logger.Info("received SIGHUP, reloading configuration and assets", "path", configPath)
					if configPath != "" {
						data, err := os.ReadFile(configPath)
						if err != nil {
							logger.Error("failed to read configuration file on SIGHUP", "path", configPath, "error", err)
							configManager.SetErrorStatus(err)
							if serr := hub.DispatchStatus(configManager.Status()); serr != nil {
								logger.Error("failed to dispatch status on SIGHUP read failure", "error", serr)
							}
						} else {
							snap, diff, err := configManager.Reload(data)
							if err != nil {
								logger.Error("failed to validate configuration on SIGHUP; retaining LKGC", "error", err)
								if serr := hub.DispatchStatus(configManager.Status()); serr != nil {
									logger.Error("failed to dispatch status on SIGHUP validation failure", "error", serr)
								}
							} else {
								if err := hub.DispatchConfigReload(snap, diff); err != nil {
									logger.Error("failed to dispatch config reload on SIGHUP", "error", err)
								}
								if err := hub.DispatchStatus(configManager.Status()); err != nil {
									logger.Error("failed to dispatch status on SIGHUP", "error", err)
								}
								logger.Info("configuration reloaded successfully on SIGHUP", "widgets", len(snap.Config.Display.Widgets))

								if snap != nil && snap.Config != nil {
									newHost := server.DefaultHost
									if snap.Config.Host != "" {
										newHost = snap.Config.Host
									}
									newPort := server.DefaultPort
									if snap.Config.Port != nil {
										newPort = *snap.Config.Port
									}
									if newHost != initialHost || newPort != initialPort {
										logger.Warn("host or port changed in configuration; server restart required for changes to take effect",
											"bound_host", initialHost,
											"bound_port", initialPort,
											"new_host", newHost,
											"new_port", newPort,
										)
									}
								}
							}
						}
					}
					if assetReloader != nil {
						cssReloaded, widgetsReloaded := assetReloader.Reload()
						logger.Info("asset reload on SIGHUP complete", "css_reloaded", cssReloaded, "widgets_reloaded", widgetsReloaded)
					}
					if rcfg.onReload != nil {
						rcfg.onReload()
					}
				}
			}
		}
	}()

	// 13. Assemble Server Configuration
	hostVal := initialHost
	portVal := initialPort
	if portVal == 0 {
		portVal = -1
	}

	cfg := server.Config{
		Host:           hostVal,
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
	var lc net.ListenConfig
	listener, err := lc.Listen(context.Background(), "tcp", bindAddr)
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
