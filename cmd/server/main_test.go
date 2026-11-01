package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/config"
	"github.com/azylman/mirrormere/internal/events"
	"github.com/azylman/mirrormere/internal/render"
)

type failWriter struct{}

type failListener struct {
	net.Listener
}

func (f *failListener) Close() error {
	return errors.New("simulated close listener failure")
}

func TestCloseListenerIgnoreClosed(t *testing.T) {
	t.Parallel()
	closeListenerIgnoreClosed(nil)
	closeListenerIgnoreClosed(&failListener{})
}

func WithAddrChan(ch chan<- string) Option   { return func(rc *runConfig) { rc.addrChan = ch } }
func WithHupChan(ch <-chan os.Signal) Option { return func(rc *runConfig) { rc.hupChan = ch } }
func WithOnReload(fn func()) Option          { return func(rc *runConfig) { rc.onReload = fn } }

func (failWriter) Write(p []byte) (int, error) {
	return 0, errors.New("simulated stdout write failure")
}

type testDaemon struct {
	baseURL, configPath, tmpDir string
	hupChan                     chan os.Signal
	reloadedChan                chan struct{}
	stdout, stderr              *bytes.Buffer
	cancel                      context.CancelFunc
}

func startDaemon(t *testing.T, yamlContent, customCSS string, customWidget map[string]string) *testDaemon {
	t.Helper()
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	if yamlContent == "" {
		yamlContent = "timezone: UTC\ndisplay:\n  widgets:\n    - id: spacer\n      type: spacer\n      dimensions: [6, 2]\n"
	}
	if err := os.WriteFile(configPath, []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if customCSS != "" {
		_ = os.WriteFile(filepath.Join(tmpDir, "custom.css"), []byte(customCSS), 0o644)
	}
	_ = os.MkdirAll(filepath.Join(tmpDir, "web", "static", "ui"), 0o755)
	_ = os.WriteFile(filepath.Join(tmpDir, "web", "static", "ui", "app.js"), []byte("console.log('init');"), 0o644)
	if customWidget != nil {
		wDir := filepath.Join(tmpDir, "widgets", customWidget["name"])
		if m, ok := customWidget["manifest"]; ok {
			_ = os.MkdirAll(wDir, 0o755)
			_ = os.WriteFile(filepath.Join(wDir, "manifest.yaml"), []byte(m), 0o644)
		}
		if v, ok := customWidget["view"]; ok {
			_ = os.MkdirAll(filepath.Join(wDir, "views"), 0o755)
			_ = os.WriteFile(filepath.Join(wDir, "views", "widget.html"), []byte(v), 0o644)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	readyChan, addrChan, hupChan, reloadedChan := make(chan struct{}, 1), make(chan string, 1), make(chan os.Signal, 1), make(chan struct{}, 1)
	var stdout, stderr bytes.Buffer
	errChan := make(chan error, 1)

	go func() {
		errChan <- RunWithReady(ctx, []string{configPath}, &stdout, &stderr, readyChan,
			WithAddrChan(addrChan), WithHupChan(hupChan), WithOnReload(func() {
				select {
				case reloadedChan <- struct{}{}:
				default:
				}
			}))
	}()

	var baseURL string
	select {
	case <-readyChan:
		baseURL = "http://" + <-addrChan
	case err := <-errChan:
		t.Fatalf("RunWithReady failed: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting ready")
	}

	t.Cleanup(func() {
		cancel()
		select {
		case <-errChan:
		case <-time.After(3 * time.Second):
		}
	})

	return &testDaemon{
		baseURL: baseURL, hupChan: hupChan, reloadedChan: reloadedChan,
		stdout: &stdout, stderr: &stderr, tmpDir: tmpDir, configPath: configPath, cancel: cancel,
	}
}

func TestMain_Execution(t *testing.T) {
	tests := []struct {
		name, arg string
		wantExit  int
	}{
		{"help_flag", "-help", 0}, {"invalid_arg", "-invalid-arg", 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			oldArgs, oldExit := argsHook, exitFunc
			argsHook = []string{tc.arg}
			exitCode := 0
			exitFunc = func(c int) { exitCode = c }
			defer func() { argsHook, exitFunc = oldArgs, oldExit }()
			main()
			if exitCode != tc.wantExit {
				t.Fatalf("exitCode = %d, want %d", exitCode, tc.wantExit)
			}
		})
	}
}

type fakeSnapProvider struct {
	snap  *config.Snapshot
	theme string
}

func (f *fakeSnapProvider) CurrentSnapshot() *config.Snapshot { return f.snap }
func (f *fakeSnapProvider) GetWidgetState(string) (any, string, string, bool) {
	return nil, "", "", false
}
func (f *fakeSnapProvider) CurrentStatus() config.Status { return config.Status{} }
func (f *fakeSnapProvider) GetTheme() string             { return f.theme }
func (f *fakeSnapProvider) SetTheme(s string)            { f.theme = s }

func TestHouseholdHelpers(t *testing.T) {
	tests := []struct {
		name, helper, tz, themeMode string
		hasCoords, nilSnap, nilProv bool
		wantTheme                   string
		wantBool                    bool
	}{
		{"now_nil_provider", "now", "", "", false, false, true, "", false},
		{"now_nil_snapshot", "now", "", "", false, true, false, "", false},
		{"now_valid_tz", "now", "Asia/Tokyo", "", false, false, false, "JST", false},
		{"now_invalid_tz", "now", "Invalid/TZ", "", false, false, false, "UTC", false},
		{"theme_nil_provider", "theme", "", "", false, false, true, "day", false},
		{"theme_nil_snapshot", "theme", "", "", false, true, false, "day", false},
		{"theme_day", "theme", "", "day", false, false, false, "day", false},
		{"theme_night", "theme", "", "night", false, false, false, "night", false},
		{"theme_auto_no_coords", "theme", "", "auto", false, false, false, "day", false},
		{"theme_auto_coords", "theme", "", "auto", true, false, false, "auto", false},
		{"trans_nil_fn", "transition", "", "", false, false, false, "", false},
		{"trans_nil_sp", "transition", "", "", false, false, true, "", false},
		{"trans_same", "transition", "", "day", false, false, false, "", false},
		{"trans_changed", "transition", "", "night", false, false, false, "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var sp *fakeSnapProvider
			if !tc.nilProv {
				sp = &fakeSnapProvider{theme: "day"}
				if !tc.nilSnap {
					cfg := &config.Config{Timezone: tc.tz, Display: config.DisplayConfig{Theme: &config.ThemeConfig{Mode: tc.themeMode}}}
					if tc.hasCoords {
						lat, lon := 37.7749, -122.4194
						cfg.LocationConfig = &config.LocationConfig{Latitude: &lat, Longitude: &lon}
					}
					sp.snap = &config.Snapshot{Config: cfg}
				}
			}
			switch tc.helper {
			case "now":
				var prov render.StateSnapshotProvider
				if sp != nil {
					prov = sp
				}
				now := householdNowFunc(prov)()
				if tc.wantTheme != "" {
					if z, _ := now.Zone(); z != tc.wantTheme {
						t.Fatalf("zone = %s, want %s", z, tc.wantTheme)
					}
				} else if now.IsZero() {
					t.Fatal("expected non-zero time")
				}
			case "theme":
				var prov render.StateSnapshotProvider
				if sp != nil {
					prov = sp
				}
				got := householdThemeFunc(prov)()
				if tc.wantTheme == "auto" {
					if got != "day" && got != "night" {
						t.Fatalf("auto theme = %s", got)
					}
				} else if got != tc.wantTheme {
					t.Fatalf("theme = %s, want %s", got, tc.wantTheme)
				}
			case "transition":
				var cur func() string
				if tc.name != "trans_nil_fn" {
					cur = func() string { return tc.themeMode }
				}
				var prov interface {
					GetTheme() string
					SetTheme(theme string)
				}
				if sp != nil {
					prov = sp
				}
				hub := events.NewHub(events.HubConfig{}, nil, nil)
				defer hub.Close()
				if changed := evaluateThemeTransition(cur, prov, hub, slog.Default()); changed != tc.wantBool {
					t.Fatalf("transition = %v, want %v", changed, tc.wantBool)
				}
			}
		})
	}

	h := buildDisplayHandler(&fakeSnapProvider{}, func() string { return "day" })
	_ = h.Timezone()
	_ = h.HideCursor()
	_ = h.RemoteURL()
	_ = h.HeaderVideo()
	_ = h.Theme()
	if h.Grid() != "6x2" {
		t.Fatalf("expected default grid 6x2, got %s", h.Grid())
	}
	hPortrait := buildDisplayHandler(&fakeSnapProvider{snap: &config.Snapshot{Config: &config.Config{Display: config.DisplayConfig{Grid: config.GridConfig{Columns: 2, Rows: 6}}}}}, func() string { return "day" })
	if hPortrait.Grid() != "2x6" {
		t.Fatalf("expected portrait grid 2x6, got %s", hPortrait.Grid())
	}
}

func TestBuildDisplayHandler_VoiceWaveform(t *testing.T) {
	t.Parallel()
	enabled := true
	tests := []struct {
		name    string
		voice   *config.DisplayVoiceConfig
		wantNil bool
	}{
		{name: "no voice section", voice: nil, wantNil: true},
		{name: "voice without waveform", voice: &config.DisplayVoiceConfig{}, wantNil: true},
		{name: "waveform disabled", voice: &config.DisplayVoiceConfig{Waveform: &config.VoiceWaveformConfig{Enabled: new(false)}}, wantNil: true},
		{name: "waveform enabled", voice: &config.DisplayVoiceConfig{Waveform: &config.VoiceWaveformConfig{Enabled: &enabled, LevelsURL: "http://127.0.0.1:9100/levels", Gain: new(1.5)}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sp := &fakeSnapProvider{theme: "day", snap: &config.Snapshot{Config: &config.Config{Timezone: "UTC", Display: config.DisplayConfig{Voice: tc.voice}}}}
			h := buildDisplayHandler(sp, func() string { return "day" })
			wv := h.VoiceWaveform()
			if tc.wantNil {
				if wv != nil {
					t.Fatalf("voice waveform = %+v, want nil", wv)
				}
				return
			}
			if wv == nil || !wv.Enabled || wv.LevelsURL != "http://127.0.0.1:9100/levels" || wv.Gain != 1.5 {
				t.Fatalf("voice waveform = %+v", wv)
			}
		})
	}
}

func TestRun_CLIAndErrors(t *testing.T) {
	tests := []struct {
		name, envCfg, yamlData, wantErr string
		args                            []string
		useFailOut, useFailErr          bool
	}{
		{name: "flag_h", args: []string{"-h"}},
		{name: "flag_help", args: []string{"-help"}},
		{name: "flag_double_dash_help", args: []string{"--help"}},
		{name: "help_fail_stderr", args: []string{"-h"}, useFailErr: true, wantErr: "simulated"},
		{name: "invalid_flag", args: []string{"-nonexistent"}, wantErr: "flags are not supported"},
		{name: "flag_port_rejected", args: []string{"-port", "8080"}, wantErr: "flags are not supported"},
		{name: "missing_config_arg", args: []string{"/nonexistent/missing.yaml"}, wantErr: "failed to read configuration file"},
		{name: "missing_config_env", envCfg: "/nonexistent/from/env.yaml", args: []string{}, wantErr: "failed to read configuration file"},
		{name: "invalid_yaml", yamlData: "timezone: [invalid yaml", wantErr: "failed to initialize configuration manager"},
		{name: "bind_error", yamlData: "timezone: UTC\nhost: \"999.999.999.999\"\nport: 80\ndisplay:\n  widgets:\n    - id: spacer\n      type: spacer\n      dimensions: [6, 2]\n", wantErr: "failed to bind address"},
		{name: "fail_stdout", yamlData: "timezone: UTC\nhost: \"127.0.0.1\"\nport: 0\ndisplay:\n  widgets:\n    - id: spacer\n      type: spacer\n      dimensions: [6, 2]\n", useFailOut: true, wantErr: "simulated stdout write failure"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			args := tc.args
			if tc.yamlData != "" {
				cfgFile := filepath.Join(tmpDir, "config.yaml")
				_ = os.WriteFile(cfgFile, []byte(tc.yamlData), 0o644)
				args = []string{cfgFile}
			}
			t.Setenv("CONFIG_PATH", tc.envCfg)
			var stdout, stderr bytes.Buffer
			var outW, errW io.Writer = &stdout, &stderr
			if tc.useFailOut {
				outW = failWriter{}
			}
			if tc.useFailErr {
				errW = failWriter{}
			}

			err := Run(context.Background(), args, outW, errW)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestRun_DaemonLifecycleAndSmoke(t *testing.T) {
	tests := []struct {
		name, envHost, envPort string
		smokeCheck             bool
	}{
		{name: "composition_smoke", smokeCheck: true},
		{name: "env_host_port_ignored", envHost: "1.2.3.4", envPort: "9999"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.envHost != "" {
				t.Setenv("HOST", tc.envHost)
			}
			if tc.envPort != "" {
				t.Setenv("PORT", tc.envPort)
			}

			yamlContent := "host: \"127.0.0.1\"\nport: 0\ntimezone: America/New_York\ndisplay:\n  header:\n    video:\n      enabled: true\n      stream_id: \"test-cam\"\n      stream_url: \"http://camera.example.com/stream.m3u8\"\n      control_url: \"http://controller.example.com/wake\"\n      type: \"webrtc\"\n  widgets:\n    - id: default-spacer\n      type: spacer\n      dimensions: [6, 2]\n"
			d := startDaemon(t, yamlContent, "", nil)

			if tc.smokeCheck {
				c := &http.Client{Timeout: 5 * time.Second}
				for _, ep := range []struct {
					path, match string
					status      int
				}{
					{"/healthz", "", 200},
					{"/api/audio", `"volume":75`, 200},
					{"/api/video/state", `"mode":"widgets"`, 200},
					{"/display", "static/ui/app.js", 200},
					{"/display?ui=legacy", "mirrormere-app", 200},
					{"/style.css", "", 200},
				} {
					r, err := c.Get(d.baseURL + ep.path)
					if err != nil || r.StatusCode != ep.status {
						t.Fatalf("GET %s = %v, err=%v", ep.path, r.StatusCode, err)
					}
					b, _ := io.ReadAll(r.Body)
					_ = r.Body.Close()
					if ep.match != "" && !strings.Contains(string(b), ep.match) {
						t.Fatalf("GET %s = %s", ep.path, string(b))
					}
				}

				completeReq, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, d.baseURL+"/api/lists/test/items/item-1/complete", nil)
				completeResp, err := c.Do(completeReq)
				if err != nil || completeResp.StatusCode != http.StatusNotFound {
					t.Fatalf("POST /api/lists/test/items/item-1/complete = %v, err=%v", completeResp.StatusCode, err)
				}
				_ = completeResp.Body.Close()

				req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, d.baseURL+"/api/events", nil)
				r, err := http.DefaultClient.Do(req)
				if err != nil || r.StatusCode != http.StatusOK {
					t.Fatalf("events: %v", err)
				}
				buf := make([]byte, 256)
				n, _ := r.Body.Read(buf)
				_ = r.Body.Close()
				if n == 0 {
					t.Fatal("empty events chunk")
				}
			} else if tc.envHost != "" {
				if strings.Contains(d.baseURL, "1.2.3.4") || strings.HasSuffix(d.baseURL, ":9999") {
					t.Fatalf("env host/port not ignored: %s", d.baseURL)
				}
			}
		})
	}
}

func TestRun_SIGHUP(t *testing.T) {
	tests := []struct {
		name, action, wantEvtType, wantLogSub, wantHTTPPath, wantHTTPBody string
	}{
		{name: "valid_yaml", action: "valid_yaml", wantLogSub: "configuration reloaded successfully on SIGHUP"},
		{name: "invalid_yaml_lkgc", action: "invalid_yaml", wantLogSub: "failed to validate configuration on SIGHUP; retaining LKGC"},
		{name: "read_error_lkgc", action: "read_error", wantLogSub: "failed to read configuration file on SIGHUP"},
		{name: "style_reload", action: "style_reload", wantEvtType: events.EventStyleReload, wantHTTPPath: "/style.css", wantHTTPBody: "body { background: #9b59b6; }"},
		{name: "widget_reload", action: "widget_reload", wantEvtType: events.EventWidgetReload, wantHTTPPath: "/api/widgets/clock-instance/render", wantHTTPBody: "<div>Hot Reloaded Clock View</div>"},
		{name: "web_reload", action: "web_reload", wantEvtType: events.EventSystemReload, wantLogSub: "web_reloaded=true"},
		{name: "host_port_change", action: "host_port_change", wantLogSub: "host or port changed in configuration; server restart required"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			initYAML := "host: \"127.0.0.1\"\nport: 0\ntimezone: UTC\ndisplay:\n  widgets:\n    - id: clock-instance\n      type: clock\n      dimensions: [6, 2]\n"
			customWidget := map[string]string{
				"name":     "clock",
				"manifest": "name: clock\nversion: \"1.0.0\"\nprovider: spacer\ndefault_dimensions: [6, 2]\n",
				"view":     "<div>Original Clock View</div>",
			}
			initCSS := "body { background: #000; }"
			d := startDaemon(t, initYAML, initCSS, customWidget)

			var sseEvents chan string
			if tc.wantEvtType != "" {
				sseEvents = make(chan string, 20)
				sseCtx := t.Context()
				req, _ := http.NewRequestWithContext(sseCtx, http.MethodGet, d.baseURL+"/api/events", nil)
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatalf("SSE connect: %v", err)
				}
				t.Cleanup(func() { _ = resp.Body.Close() })
				go func() {
					scanner := bufio.NewScanner(resp.Body)
					for scanner.Scan() {
						line := scanner.Text()
						if after, ok := strings.CutPrefix(line, "event: "); ok {
							sseEvents <- after
						}
					}
				}()
			}

			switch tc.action {
			case "valid_yaml":
				_ = os.WriteFile(d.configPath, []byte("host: \"127.0.0.1\"\nport: 0\ntimezone: UTC\ndisplay:\n  widgets:\n    - id: s\n      type: spacer\n      dimensions: [6, 2]\n"), 0o644)
			case "invalid_yaml":
				_ = os.WriteFile(d.configPath, []byte("timezone: [invalid: -"), 0o644)
			case "read_error":
				_ = os.Remove(d.configPath)
			case "style_reload":
				_ = os.WriteFile(filepath.Join(d.tmpDir, "custom.css"), []byte("body { background: #9b59b6; }"), 0o644)
			case "widget_reload":
				_ = os.WriteFile(filepath.Join(d.tmpDir, "widgets", "clock", "views", "widget.html"), []byte("<div>Hot Reloaded Clock View</div>"), 0o644)
			case "web_reload":
				_ = os.WriteFile(filepath.Join(d.tmpDir, "web", "static", "ui", "app.js"), []byte("console.log('hot-reloaded');"), 0o644)
			case "host_port_change":
				_ = os.WriteFile(d.configPath, []byte("host: \"127.0.0.2\"\nport: 9090\ntimezone: UTC\ndisplay:\n  widgets:\n    - id: s\n      type: spacer\n      dimensions: [6, 2]\n"), 0o644)
			}

			d.hupChan <- syscall.SIGHUP
			select {
			case <-d.reloadedChan:
			case <-time.After(3 * time.Second):
				t.Fatal("timeout waiting for reload")
			}

			if tc.wantEvtType != "" {
				deadline := time.After(3 * time.Second)
				for found := false; !found; {
					select {
					case evt := <-sseEvents:
						if evt == tc.wantEvtType {
							found = true
						}
					case <-deadline:
						t.Fatalf("timeout waiting for SSE event %s", tc.wantEvtType)
					}
				}
			}

			if tc.wantHTTPPath != "" {
				r, err := http.Get(d.baseURL + tc.wantHTTPPath)
				if err != nil || r.StatusCode != http.StatusOK {
					t.Fatalf("GET %s failed: %v", tc.wantHTTPPath, err)
				}
				b, _ := io.ReadAll(r.Body)
				_ = r.Body.Close()
				if !strings.Contains(string(b), tc.wantHTTPBody) {
					t.Fatalf("GET %s = %s, want %s", tc.wantHTTPPath, string(b), tc.wantHTTPBody)
				}
			}

			if tc.wantLogSub != "" {
				if out := d.stdout.String() + d.stderr.String(); !strings.Contains(out, tc.wantLogSub) {
					t.Fatalf("expected log containing %q, got: %s", tc.wantLogSub, out)
				}
			}
			if tc.action == "invalid_yaml" || tc.action == "read_error" {
				if r, err := http.Get(d.baseURL + "/display"); err != nil || r.StatusCode != http.StatusOK {
					t.Fatalf("display LKGC: %v", err)
				} else {
					_ = r.Body.Close()
				}
			}
		})
	}
}
