package main

import (
	"context"
	"encoding/json"
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
	"github.com/azylman/mirrormere/internal/solar"
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

type (
	// Option configures RunWithReady execution.
	Option    func(*runConfig)
	runConfig struct {
		addrChan chan<- string
		hupChan  <-chan os.Signal
		onReload func()
	}
)

func chatLogWidgetIDs(snap *config.Snapshot) (ids []string) {
	if snap != nil && snap.Config != nil {
		for _, w := range snap.Config.Display.Widgets {
			if w.Type == "chat-log" {
				ids = append(ids, w.ID)
			}
		}
	}
	return ids
}

type chatLogPusher interface {
	PushUpdate(widgetID string, data any) (provider.WidgetPayload, error)
}

func newChatLogHook(sp render.StateSnapshotProvider, pusher func() chatLogPusher, store *chatlog.Store) func() {
	return func() { pushChatLogUpdates(sp, pusher(), store) }
}

func pushChatLogUpdates(sp render.StateSnapshotProvider, pusher chatLogPusher, store *chatlog.Store) {
	for _, id := range chatLogWidgetIDs(sp.CurrentSnapshot()) {
		if _, err := pusher.PushUpdate(id, store.View(id, "")); err != nil {
			slog.Warn("chat-log widget update failed", "widget_id", id, "error", err)
		}
	}
}

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
		if sp != nil && sp.CurrentSnapshot() != nil && sp.CurrentSnapshot().Config != nil {
			return now.In(sp.CurrentSnapshot().Config.Location())
		}
		return now
	}
}

func householdThemeFunc(sp render.StateSnapshotProvider) func() string {
	return func() string {
		if sp == nil || sp.CurrentSnapshot() == nil || sp.CurrentSnapshot().Config == nil {
			return "day"
		}
		snap := sp.CurrentSnapshot()
		if mode := snap.Config.Display.Theme.GetMode(); mode == "night" || mode == "day" {
			return mode
		}
		if loc := snap.Config.GetLocationConfig(); loc != nil && loc.Latitude != nil && loc.Longitude != nil {
			now := time.Now()
			if snap.Config.Location() != nil {
				now = now.In(snap.Config.Location())
			}
			if solar.IsNight(loc.GetLatitude(), loc.GetLongitude(), now) {
				return "night"
			}
		}
		return "day"
	}
}

func evaluateThemeTransition(currentTheme func() string, sp interface {
	GetTheme() string
	SetTheme(string)
}, hub *events.Hub, logger *slog.Logger,
) bool {
	if currentTheme == nil || sp == nil {
		return false
	}
	newTheme := currentTheme()
	if sp.GetTheme() != newTheme {
		if logger != nil {
			logger.Info("theme mode transition detected", "previous", sp.GetTheme(), "new", newTheme)
		}
		sp.SetTheme(newTheme)
		if hub != nil {
			if data, err := json.Marshal(events.ThemeUpdateData{Theme: newTheme}); err == nil {
				hub.Publish(events.EventThemeUpdate, data)
			}
		}
		return true
	}
	return false
}

func buildDisplayHandler(stateProvider render.StateSnapshotProvider, currentTheme func() string) *display.Handler {
	cfg := func() *config.Config {
		if snap := stateProvider.CurrentSnapshot(); snap != nil {
			return snap.Config
		}
		return nil
	}
	return display.NewHandler(
		display.WithThemeProvider(currentTheme),
		display.WithGridProvider(func() string {
			if c := cfg(); c != nil && c.Display.Grid.Columns == 2 && c.Display.Grid.Rows == 6 {
				return "2x6"
			}
			return "6x2"
		}),
		display.WithTimezoneProvider(func() string {
			if c := cfg(); c != nil && c.Timezone != "" {
				return c.Timezone
			}
			return "UTC"
		}),
		display.WithHideCursorProvider(func() bool { return cfg() != nil && cfg().Display.HideCursor }),
		display.WithRemoteURLProvider(func() string {
			if c := cfg(); c != nil {
				return c.Display.RemoteURL
			}
			return ""
		}),
		display.WithHeaderVideoProvider(func() *display.HeaderVideoTemplateData {
			if c := cfg(); c != nil {
				if v := c.Display.Header.Video; v != nil && v.IsEnabled() {
					return &display.HeaderVideoTemplateData{
						Enabled: true, StreamID: v.GetStreamID(), StreamURL: v.StreamURL,
						ControlURL: v.ControlURL, Type: v.GetType(),
					}
				}
			}
			return nil
		}),
		display.WithVoiceWaveformProvider(func() *display.VoiceWaveformTemplateData {
			if c := cfg(); c != nil && c.Display.Voice != nil && c.Display.Voice.Waveform.IsEnabled() {
				wv := c.Display.Voice.Waveform
				return &display.VoiceWaveformTemplateData{
					Enabled: true, LevelsURL: wv.GetLevelsURL(), Gain: wv.GetGain(),
				}
			}
			return nil
		}),
	)
}

func handleSIGHUP(
	configPath string, configManager *config.Manager, hub *events.Hub,
	stateProvider *events.InMemoryStateProvider, currentTheme func() string,
	initialHost string, initialPort int, assetReloader *AssetReloader,
	renderHandler *render.Handler, voiceHub *voice.Hub, onReload func(), logger *slog.Logger,
) {
	logger.Info("received SIGHUP, reloading configuration and assets", "path", configPath)
	if configPath != "" {
		if data, err := os.ReadFile(configPath); err != nil {
			logger.Error("failed to read configuration file on SIGHUP", "path", configPath, "error", err)
			configManager.SetErrorStatus(err)
			dispatchStatus(hub, configManager.Status(), logger)
		} else if snap, diff, err := configManager.Reload(data); err != nil {
			logger.Error("failed to validate configuration on SIGHUP; retaining LKGC", "error", err)
			dispatchStatus(hub, configManager.Status(), logger)
		} else {
			if err := hub.DispatchConfigReload(snap, diff); err != nil {
				logger.Error("failed to dispatch reload", "error", err)
			}
			dispatchStatus(hub, configManager.Status(), logger)
			logger.Info("configuration reloaded successfully on SIGHUP", "widgets", len(snap.Config.Display.Widgets))
			evaluateThemeTransition(currentTheme, stateProvider, hub, logger)

			if snap != nil && snap.Config != nil {
				voiceHub.ReloadSpeakerID(snap.Config.VoiceHub)
				newHost, newPort := server.DefaultHost, server.DefaultPort
				if snap.Config.Host != "" {
					newHost = snap.Config.Host
				}
				if snap.Config.Port != nil {
					newPort = *snap.Config.Port
				}
				if newHost != initialHost || newPort != initialPort {
					logger.Warn("host or port changed in configuration; server restart required for changes to take effect",
						"bound_host", initialHost, "bound_port", initialPort, "new_host", newHost, "new_port", newPort)
				}
			}
		}
	}
	if assetReloader != nil {
		cssReloaded, widgetsReloaded := assetReloader.Reload()
		logger.Info("asset reload on SIGHUP complete", "css_reloaded", cssReloaded, "widgets_reloaded", widgetsReloaded)
		renderHandler.SetCSSPath(resolveCSSPath())
	}
	if onReload != nil {
		onReload()
	}
}

func dispatchStatus(hub *events.Hub, st config.Status, logger *slog.Logger) {
	if err := hub.DispatchStatus(st); err != nil {
		logger.Error("failed to dispatch status", "error", err)
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

	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	var configPath string
	for _, arg := range args {
		if arg == "-h" || arg == "-help" || arg == "--help" {
			_, err := fmt.Fprintln(stderr, "Usage: mirrormere [config-path]")
			return err
		}
		if strings.HasPrefix(arg, "-") {
			return fmt.Errorf("flags are not supported; configure via config file: %s", arg)
		}
		if configPath == "" {
			configPath = arg
		}
	}

	if configPath == "" {
		if configPath = os.Getenv("CONFIG_PATH"); configPath == "" {
			configPath = config.DefaultConfigPath
		}
	}

	builtinDir := widget.DefaultBuiltinDir
	for _, c := range []string{"/app/widgets", "widgets", "../../widgets"} {
		if fi, err := os.Stat(c); err == nil && fi.IsDir() {
			builtinDir = c
			break
		}
	}
	customDir := widget.DefaultCustomDir
	if configPath != "" {
		if cDir := filepath.Join(filepath.Dir(configPath), "widgets"); cDir != "" {
			if fi, err := os.Stat(cDir); err == nil && fi.IsDir() {
				customDir = cDir
			}
		}
	}
	loader := widget.NewLoader(builtinDir, customDir)

	var initialYAML []byte
	if configPath != "" {
		if data, err := os.ReadFile(configPath); err == nil {
			initialYAML = data
		} else if (len(args) > 0 && configPath == args[0]) || (os.Getenv("CONFIG_PATH") != "" && configPath == os.Getenv("CONFIG_PATH")) {
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

	configManager, err := config.NewManager(initialYAML, loader, os.Getenv, logger)
	if err != nil {
		return fmt.Errorf("failed to initialize configuration manager: %w", err)
	}
	snapshot := configManager.CurrentSnapshot()

	initialHost, initialPort := server.DefaultHost, server.DefaultPort
	if snapshot != nil && snapshot.Config != nil {
		if snapshot.Config.Host != "" {
			initialHost = snapshot.Config.Host
		}
		if snapshot.Config.Port != nil {
			initialPort = *snapshot.Config.Port
		}
	}

	stateProvider := events.NewInMemoryStateProvider(configManager)
	currentTheme := householdThemeFunc(stateProvider)
	stateProvider.SetTheme(currentTheme())
	hub := events.NewHub(events.HubConfig{}, stateProvider, logger)
	defer hub.Close()

	var chatLogCfg *config.ChatLogConfig
	var voiceHubCfg *config.VoiceHubConfig
	if snapshot != nil && snapshot.Config != nil {
		chatLogCfg, voiceHubCfg = snapshot.Config.ChatLog, snapshot.Config.VoiceHub
	}
	chatStore := chatlog.NewStore(chatlog.WithTTL(chatLogCfg.GetTTL()), chatlog.WithMaxPerKey(chatLogCfg.GetMaxMessagesPerNode()))

	reg := provider.NewRegistry()

	videoCoord := video.NewCoordinator(hub, video.WithSink(stateProvider))
	defer videoCoord.Close()

	var providerCoord *provider.ProviderCoordinator
	voiceHub := voice.NewHub(voiceHubCfg, voice.NewCoordinator(hub, stateProvider), voice.WithChatLog(chatStore, newChatLogHook(stateProvider, func() chatLogPusher { return providerCoord }, chatStore)))

	rotationCoord := rotation.NewCoordinator(rotation.Config{Broadcaster: hub, Logger: logger}, snapshot)
	defer rotationCoord.Stop()
	hub.SetRotationCoordinator(rotationCoord)

	providerCoord = provider.NewCoordinator(provider.CoordinatorConfig{
		Registry: reg, Broadcaster: hub, StateSink: stateProvider, Logger: logger, ChatLog: chatStore,
	}, snapshot)
	defer func() {
		if err := providerCoord.Stop(); err != nil {
			logger.Warn("failed to stop provider coordinator", "error", err)
		}
	}()
	hub.SetProviderCoordinator(providerCoord)

	listsHandler := server.NewDefaultListsHandler(server.WithItemCompleter(providerCoord))

	renderEngine := render.NewEngine(loader, stateProvider,
		render.WithNowFunc(householdNowFunc(stateProvider)),
		render.WithNodeDataFunc(chatLogNodeData(stateProvider, chatStore)),
	)
	renderHandler := render.NewHandler(renderEngine, loader, render.WithCSSPath(resolveCSSPath()), render.WithCustomCSSPath(customCSSPath))
	assetReloader := NewAssetReloader(customCSSPath, loader, builtinDir, customDir, renderEngine, configManager, hub, logger)

	themeTicker := time.NewTicker(30 * time.Second)
	defer themeTicker.Stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-themeTicker.C:
				evaluateThemeTransition(currentTheme, stateProvider, hub, logger)
			}
		}
	}()

	var hupChan <-chan os.Signal
	if rcfg.hupChan != nil {
		hupChan = rcfg.hupChan
	} else {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGHUP)
		hupChan = ch
		defer signal.Stop(ch)
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
					handleSIGHUP(configPath, configManager, hub, stateProvider, currentTheme, initialHost, initialPort, assetReloader, renderHandler, voiceHub, rcfg.onReload, logger)
				}
			}
		}
	}()

	portVal := initialPort
	if portVal == 0 {
		portVal = -1
	}
	cfg := server.Config{
		Host:           initialHost,
		Port:           portVal,
		EventsHandler:  events.Handler(hub),
		ScreenHandler:  rotation.NewHandler(rotationCoord),
		RenderHandler:  renderHandler,
		PushHandler:    provider.NewPushHandler(providerCoord, logger),
		DisplayHandler: buildDisplayHandler(stateProvider, currentTheme),
		ListsHandler:   listsHandler,
		AudioHandler:   server.NewDefaultAudioHandler(audio.NewCoordinator(hub, stateProvider)),
		VideoHandler:   server.NewDefaultVideoHandler(videoCoord),
		VoiceHandler:   server.NewDefaultVoiceHandler(voice.NewCoordinator(hub, stateProvider), voiceHub, server.WithVoiceMetrics(voice.DefaultMetrics())),
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
		closeListenerIgnoreClosed(listener)
		return err
	}

	if rcfg.addrChan != nil {
		rcfg.addrChan <- listener.Addr().String()
	}
	if readyChan != nil {
		readyChan <- struct{}{}
	}

	errChan := make(chan error, 1)
	go func() { errChan <- srv.Serve(listener) }()

	select {
	case <-ctx.Done():
		hub.Close()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errChan:
		return err
	}
}

func resolveCSSPath() string {
	for _, c := range []string{render.DefaultCSSPath, filepath.Join("..", "..", render.DefaultCSSPath)} {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c
		}
	}
	return render.DefaultCSSPath
}

func closeListenerIgnoreClosed(l net.Listener) {
	if l != nil {
		_ = l.Close()
	}
}
