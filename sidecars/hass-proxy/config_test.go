package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	tmpDir := t.TempDir()
	tests := []struct {
		name       string
		yaml       string
		envKey     string
		envVal     string
		nonExist   bool
		wantErrSub string
		wantPort   int
		wantURL    string
		wantToken  string
		wantLang   string
		wantDevID  string
		wantNodeCt int
	}{
		{
			name: "valid_full",
			yaml: "server:\n  host: 127.0.0.1\n  port: 8099\n  read_timeout_seconds: 15\n  write_timeout_seconds: 20\nhomeassistant:\n  url: http://ha.local:8123\n  token: secret-token\n  agent_id: ag\n  language: fr\n  timeout_ms: 3500\n  conversation_path: /api/conv\n  device_id: fallback-dev-999\n  node_devices:\n    kiosk: dev-1\n    office: dev-2\n",
			wantPort: 8099, wantURL: "http://ha.local:8123", wantToken: "secret-token", wantLang: "fr", wantDevID: "fallback-dev-999", wantNodeCt: 2,
		},
		{
			name: "defaults_and_normalization",
			yaml: "homeassistant:\n  url: http://ha.local:8123/\n  conversation_path: api/conv\n  timeout_ms: -10\nserver:\n  read_timeout_seconds: -1\n  write_timeout_seconds: -2\n",
			wantPort: DefaultServerPort, wantURL: "http://ha.local:8123", wantLang: DefaultHALanguage,
		},
		{
			name: "empty_conversation_path",
			yaml: "homeassistant:\n  url: http://ha.local:8123\n  conversation_path: ''\n",
			wantPort: DefaultServerPort, wantURL: "http://ha.local:8123", wantLang: DefaultHALanguage,
		},
		{
			name: "secret_from_hass_token_env",
			yaml: "homeassistant:\n  url: http://ha.local:8123\n",
			envKey: "HASS_TOKEN", envVal: "env-hass-token",
			wantPort: DefaultServerPort, wantURL: "http://ha.local:8123", wantToken: "env-hass-token", wantLang: DefaultHALanguage,
		},
		{
			name: "secret_from_homeassistant_token_env",
			yaml: "homeassistant:\n  url: http://ha.local:8123\n",
			envKey: "HOMEASSISTANT_TOKEN", envVal: "env-ha-token",
			wantPort: DefaultServerPort, wantURL: "http://ha.local:8123", wantToken: "env-ha-token", wantLang: DefaultHALanguage,
		},
		{name: "missing_ha_url", yaml: "server:\n  port: 8095\n", wantErrSub: "homeassistant.url must not be empty"},
		{name: "negative_port", yaml: "server:\n  port: -1\nhomeassistant:\n  url: http://ha.local:8123\n", wantErrSub: "server.port must be between"},
		{name: "port_too_high", yaml: "server:\n  port: 70000\nhomeassistant:\n  url: http://ha.local:8123\n", wantErrSub: "server.port must be between"},
		{name: "invalid_yaml", yaml: "bad: [yaml", wantErrSub: "failed to parse YAML"},
		{name: "file_not_found", nonExist: true, wantErrSub: "specified config file does not exist"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(tmpDir, tc.name+".yaml")
			if tc.nonExist {
				path = filepath.Join(tmpDir, "does-not-exist.yaml")
			} else if err := os.WriteFile(path, []byte(tc.yaml), 0600); err != nil {
				t.Fatalf("failed to write test yaml: %v", err)
			}

			if tc.envKey != "" {
				t.Setenv("HASS_TOKEN", "")
				t.Setenv("HOMEASSISTANT_TOKEN", "")
				t.Setenv(tc.envKey, tc.envVal)
			}

			cfg, err := LoadConfig(path)
			if tc.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Fatalf("err=%v, want substring %q", err, tc.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.Server.Port != tc.wantPort {
				t.Errorf("port=%d, want %d", cfg.Server.Port, tc.wantPort)
			}
			if cfg.HomeAssistant.URL != tc.wantURL {
				t.Errorf("url=%q, want %q", cfg.HomeAssistant.URL, tc.wantURL)
			}
			if tc.wantToken != "" && cfg.HomeAssistant.Token != tc.wantToken {
				t.Errorf("token=%q, want %q", cfg.HomeAssistant.Token, tc.wantToken)
			}
			if tc.wantLang != "" && cfg.HomeAssistant.Language != tc.wantLang {
				t.Errorf("lang=%q, want %q", cfg.HomeAssistant.Language, tc.wantLang)
			}
			if tc.wantDevID != "" && cfg.HomeAssistant.DeviceID != tc.wantDevID {
				t.Errorf("deviceID=%q, want %q", cfg.HomeAssistant.DeviceID, tc.wantDevID)
			}
			if tc.wantNodeCt > 0 && len(cfg.HomeAssistant.NodeDevices) != tc.wantNodeCt {
				t.Errorf("nodeDevices=%d, want %d", len(cfg.HomeAssistant.NodeDevices), tc.wantNodeCt)
			}
		})
	}
}

func TestResolveConfigPath_Candidates(t *testing.T) {
	if _, err := resolveConfigPath(""); err == nil {
		t.Fatal("expected error when no candidate files exist")
	}
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	tmpDir := t.TempDir()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to chdir to tmpDir: %v", err)
	}
	defer func() { _ = os.Chdir(origDir) }()

	if err := os.WriteFile(filepath.Join(tmpDir, "config.yaml"), []byte("homeassistant:\n  url: http://localhost:8123\n"), 0600); err != nil {
		t.Fatalf("failed to write config.yaml: %v", err)
	}
	resolved, err := resolveConfigPath("")
	if err != nil || !strings.HasSuffix(resolved, "config.yaml") {
		t.Fatalf("resolved=%q, err=%v", resolved, err)
	}
}

func TestConfig_StringRedaction(t *testing.T) {
	cfg := Config{
		Server:        ServerConfig{Host: "0.0.0.0", Port: 8095},
		HomeAssistant: HomeAssistantConfig{URL: "http://ha.local:8123", Token: "secret-token", DeviceID: "dev1", NodeDevices: map[string]string{"k": "v"}},
	}
	s := cfg.String()
	if strings.Contains(s, "secret-token") || !strings.Contains(s, "<redacted>") || !strings.Contains(s, "DeviceID: dev1") || !strings.Contains(s, "NodeDevices: 1 entries") {
		t.Fatalf("unexpected redacted string: %s", s)
	}
	sNoToken := Config{Server: ServerConfig{Host: "0.0.0.0", Port: 8095}, HomeAssistant: HomeAssistantConfig{URL: "http://ha.local:8123"}}.String()
	if !strings.Contains(sNoToken, "<not set>") {
		t.Fatalf("expected <not set>, got %s", sNoToken)
	}
}
