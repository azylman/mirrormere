package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfig_ValidFile(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	content := `
server:
  host: "127.0.0.1"
  port: 8099
  read_timeout_seconds: 15
  write_timeout_seconds: 20

homeassistant:
  url: "http://ha.local:8123"
  token: "my-secret-token"
  agent_id: "conversation.home_assistant"
  language: "fr"
  timeout_ms: 3500
  conversation_path: "/api/conversation/process"
`
	if err := os.WriteFile(configPath, []byte(content), 0600); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("unexpected error loading valid config: %v", err)
	}

	if cfg.Server.Host != "127.0.0.1" {
		t.Errorf("expected host 127.0.0.1, got %q", cfg.Server.Host)
	}
	if cfg.Server.Port != 8099 {
		t.Errorf("expected port 8099, got %d", cfg.Server.Port)
	}
	if cfg.Server.ReadTimeoutSeconds != 15 {
		t.Errorf("expected read timeout 15, got %d", cfg.Server.ReadTimeoutSeconds)
	}
	if cfg.Server.WriteTimeoutSeconds != 20 {
		t.Errorf("expected write timeout 20, got %d", cfg.Server.WriteTimeoutSeconds)
	}
	if cfg.HomeAssistant.URL != "http://ha.local:8123" {
		t.Errorf("expected URL http://ha.local:8123, got %q", cfg.HomeAssistant.URL)
	}
	if cfg.HomeAssistant.Token != "my-secret-token" {
		t.Errorf("expected token my-secret-token, got %q", cfg.HomeAssistant.Token)
	}
	if cfg.HomeAssistant.AgentID != "conversation.home_assistant" {
		t.Errorf("expected agent_id conversation.home_assistant, got %q", cfg.HomeAssistant.AgentID)
	}
	if cfg.HomeAssistant.Language != "fr" {
		t.Errorf("expected language fr, got %q", cfg.HomeAssistant.Language)
	}
	if cfg.HomeAssistant.TimeoutMS != 3500 {
		t.Errorf("expected timeout_ms 3500, got %d", cfg.HomeAssistant.TimeoutMS)
	}
}

func TestLoadConfig_Defaults(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	content := `
homeassistant:
  url: "http://ha.local:8123/"
  conversation_path: "api/conversation/process"
  timeout_ms: -10
server:
  read_timeout_seconds: -1
  write_timeout_seconds: -2
`
	if err := os.WriteFile(configPath, []byte(content), 0600); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("unexpected error loading config: %v", err)
	}

	if cfg.Server.Host != DefaultServerHost {
		t.Errorf("expected default host %q, got %q", DefaultServerHost, cfg.Server.Host)
	}
	if cfg.Server.Port != DefaultServerPort {
		t.Errorf("expected default port %d, got %d", DefaultServerPort, cfg.Server.Port)
	}
	if cfg.Server.ReadTimeoutSeconds != DefaultServerReadTimeoutSeconds {
		t.Errorf("expected default read timeout %d, got %d", DefaultServerReadTimeoutSeconds, cfg.Server.ReadTimeoutSeconds)
	}
	if cfg.Server.WriteTimeoutSeconds != DefaultServerWriteTimeoutSeconds {
		t.Errorf("expected default write timeout %d, got %d", DefaultServerWriteTimeoutSeconds, cfg.Server.WriteTimeoutSeconds)
	}
	if cfg.HomeAssistant.URL != "http://ha.local:8123" {
		t.Errorf("expected trimmed trailing slash in URL, got %q", cfg.HomeAssistant.URL)
	}
	if cfg.HomeAssistant.ConversationPath != "/api/conversation/process" {
		t.Errorf("expected normalized leading slash %q, got %q", "/api/conversation/process", cfg.HomeAssistant.ConversationPath)
	}
	if cfg.HomeAssistant.Language != DefaultHALanguage {
		t.Errorf("expected default language %q, got %q", DefaultHALanguage, cfg.HomeAssistant.Language)
	}
	if cfg.HomeAssistant.TimeoutMS != DefaultHATimeoutMS {
		t.Errorf("expected default timeout %d, got %d", DefaultHATimeoutMS, cfg.HomeAssistant.TimeoutMS)
	}
}

func TestLoadConfig_EmptyConversationPath(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	content := `
homeassistant:
  url: "http://ha.local:8123"
  conversation_path: ""
`
	if err := os.WriteFile(configPath, []byte(content), 0600); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("unexpected error loading config: %v", err)
	}

	if cfg.HomeAssistant.ConversationPath != DefaultHAConversationPath {
		t.Errorf("expected default conversation path, got %q", cfg.HomeAssistant.ConversationPath)
	}
}

func TestLoadConfig_SecretFromEnv(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	content := `
homeassistant:
  url: "http://ha.local:8123"
`
	if err := os.WriteFile(configPath, []byte(content), 0600); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	// Test HASS_TOKEN fallback
	t.Setenv("HASS_TOKEN", "env-secret-token")
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("failed to load config with HASS_TOKEN: %v", err)
	}
	if cfg.HomeAssistant.Token != "env-secret-token" {
		t.Errorf("expected token from HASS_TOKEN, got %q", cfg.HomeAssistant.Token)
	}

	// Test HOMEASSISTANT_TOKEN fallback
	t.Setenv("HASS_TOKEN", "")
	t.Setenv("HOMEASSISTANT_TOKEN", "homeassistant-env-token")
	cfg2, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("failed to load config with HOMEASSISTANT_TOKEN: %v", err)
	}
	if cfg2.HomeAssistant.Token != "homeassistant-env-token" {
		t.Errorf("expected token from HOMEASSISTANT_TOKEN, got %q", cfg2.HomeAssistant.Token)
	}
}

func TestLoadConfig_ValidationErrors(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name: "missing HA url",
			content: `
server:
  port: 8095
`,
			wantErr: "homeassistant.url must not be empty",
		},
		{
			name: "invalid port negative",
			content: `
server:
  port: -1
homeassistant:
  url: "http://ha.local:8123"
`,
			wantErr: "server.port must be between 1 and 65535",
		},
		{
			name: "invalid port too high",
			content: `
server:
  port: 70000
homeassistant:
  url: "http://ha.local:8123"
`,
			wantErr: "server.port must be between 1 and 65535",
		},
		{
			name:    "invalid yaml syntax",
			content: `this: is not: [valid: yaml`,
			wantErr: "failed to parse YAML config",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := filepath.Join(tmpDir, "config_"+strings.ReplaceAll(tt.name, " ", "_")+".yaml")
			if err := os.WriteFile(p, []byte(tt.content), 0600); err != nil {
				t.Fatalf("failed to write test file: %v", err)
			}
			_, err := LoadConfig(p)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected error containing %q, got %q", tt.wantErr, err.Error())
			}
		})
	}
}

func TestLoadConfig_FileNotFound(t *testing.T) {
	_, err := LoadConfig("/nonexistent/path/to/config.yaml")
	if err == nil {
		t.Fatalf("expected error for nonexistent file, got nil")
	}
}

func TestConfig_StringRedaction(t *testing.T) {
	cfg := Config{
		Server: ServerConfig{
			Host: "0.0.0.0",
			Port: 8095,
		},
		HomeAssistant: HomeAssistantConfig{
			URL:       "http://ha.local:8123",
			Token:     "super-secret-bearer-token",
			TimeoutMS: 2000,
		},
	}

	s := cfg.String()
	if strings.Contains(s, "super-secret-bearer-token") {
		t.Fatalf("config string leaked plaintext token: %s", s)
	}
	if !strings.Contains(s, "<redacted>") {
		t.Errorf("expected <redacted> token status, got %s", s)
	}

	cfgNoToken := Config{
		Server: ServerConfig{Host: "0.0.0.0", Port: 8095},
		HomeAssistant: HomeAssistantConfig{
			URL: "http://ha.local:8123",
		},
	}
	sNoToken := cfgNoToken.String()
	if !strings.Contains(sNoToken, "<not set>") {
		t.Errorf("expected <not set> token status, got %s", sNoToken)
	}
}

func TestResolveConfigPath_Candidates(t *testing.T) {
	// Calling with empty path when candidates don't exist
	_, err := resolveConfigPath("")
	if err == nil {
		t.Fatal("expected error when no candidate files exist")
	}

	// Test candidate discovery when config.yaml exists in cwd
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	tmpDir := t.TempDir()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to chdir to tmpDir: %v", err)
	}
	defer func() { _ = os.Chdir(origDir) }()

	candidatePath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(candidatePath, []byte("homeassistant:\n  url: http://localhost:8123\n"), 0600); err != nil {
		t.Fatalf("failed to write candidate config: %v", err)
	}

	resolved, err := resolveConfigPath("")
	if err != nil {
		t.Fatalf("unexpected error finding candidate config.yaml: %v", err)
	}
	if !strings.HasSuffix(resolved, "config.yaml") {
		t.Errorf("expected resolved path ending with config.yaml, got %q", resolved)
	}
}
