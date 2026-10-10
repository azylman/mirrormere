package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Default configuration constants.
const (
	DefaultServerHost                = "0.0.0.0"
	DefaultServerPort                = 8095
	DefaultServerReadTimeoutSeconds  = 10
	DefaultServerWriteTimeoutSeconds = 10
	DefaultHATimeoutMS               = 2000
	DefaultHAConversationPath        = "/api/conversation/process"
	DefaultHALanguage                = "en"
)

// Config holds all configuration options for the Home Assistant Fast-Path Proxy sidecar.
type Config struct {
	Server        ServerConfig        `yaml:"server"`
	HomeAssistant HomeAssistantConfig `yaml:"homeassistant"`
}

// ServerConfig configures the inbound HTTP server.
type ServerConfig struct {
	Host                string `yaml:"host"`
	Port                int    `yaml:"port"`
	ReadTimeoutSeconds  int    `yaml:"read_timeout_seconds"`
	WriteTimeoutSeconds int    `yaml:"write_timeout_seconds"`
}

// HomeAssistantConfig configures communication with Home Assistant.
type HomeAssistantConfig struct {
	URL              string `yaml:"url"`
	Token            string `yaml:"token"` // May be loaded from HASS_TOKEN or HOMEASSISTANT_TOKEN env vars
	AgentID          string `yaml:"agent_id"`
	Language         string `yaml:"language"`
	TimeoutMS        int    `yaml:"timeout_ms"`
	ConversationPath string `yaml:"conversation_path"`
}

// String returns a sanitized representation of the configuration, ensuring secrets are never logged.
func (c Config) String() string {
	tokenStatus := "<not set>"
	if c.HomeAssistant.Token != "" {
		tokenStatus = "<redacted>"
	}
	return fmt.Sprintf("Config{Server: %s:%d, HA_URL: %s, HA_Token: %s, AgentID: %s, Language: %s, TimeoutMS: %d, ConversationPath: %s}",
		c.Server.Host,
		c.Server.Port,
		c.HomeAssistant.URL,
		tokenStatus,
		c.HomeAssistant.AgentID,
		c.HomeAssistant.Language,
		c.HomeAssistant.TimeoutMS,
		c.HomeAssistant.ConversationPath,
	)
}

// LoadConfig loads configuration from the specified YAML file path.
// If the path is empty, it attempts default candidate paths.
// Secret tokens are extracted from HASS_TOKEN or HOMEASSISTANT_TOKEN if not explicitly set in YAML.
func LoadConfig(path string) (*Config, error) {
	resolvedPath, err := resolveConfigPath(path)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %q: %w", resolvedPath, err)
	}

	cfg := &Config{
		Server: ServerConfig{
			Host:                DefaultServerHost,
			Port:                DefaultServerPort,
			ReadTimeoutSeconds:  DefaultServerReadTimeoutSeconds,
			WriteTimeoutSeconds: DefaultServerWriteTimeoutSeconds,
		},
		HomeAssistant: HomeAssistantConfig{
			Language:         DefaultHALanguage,
			TimeoutMS:        DefaultHATimeoutMS,
			ConversationPath: DefaultHAConversationPath,
		},
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse YAML config from %q: %w", resolvedPath, err)
	}

	// Secret extraction: all config is in YAML, but secrets may be injected via environment
	if cfg.HomeAssistant.Token == "" {
		if envToken := os.Getenv("HASS_TOKEN"); envToken != "" {
			cfg.HomeAssistant.Token = envToken
		} else if envToken := os.Getenv("HOMEASSISTANT_TOKEN"); envToken != "" {
			cfg.HomeAssistant.Token = envToken
		}
	}

	// Normalize defaults and clean URL
	cfg.HomeAssistant.URL = strings.TrimRight(cfg.HomeAssistant.URL, "/")
	if cfg.HomeAssistant.ConversationPath == "" {
		cfg.HomeAssistant.ConversationPath = DefaultHAConversationPath
	}
	if !strings.HasPrefix(cfg.HomeAssistant.ConversationPath, "/") {
		cfg.HomeAssistant.ConversationPath = "/" + cfg.HomeAssistant.ConversationPath
	}
	if cfg.HomeAssistant.TimeoutMS <= 0 {
		cfg.HomeAssistant.TimeoutMS = DefaultHATimeoutMS
	}
	if cfg.Server.Port == 0 {
		cfg.Server.Port = DefaultServerPort
	}
	if cfg.Server.ReadTimeoutSeconds <= 0 {
		cfg.Server.ReadTimeoutSeconds = DefaultServerReadTimeoutSeconds
	}
	if cfg.Server.WriteTimeoutSeconds <= 0 {
		cfg.Server.WriteTimeoutSeconds = DefaultServerWriteTimeoutSeconds
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return cfg, nil
}

// resolveConfigPath locates an existing config file from explicit path or default candidates.
func resolveConfigPath(path string) (string, error) {
	if path != "" {
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("specified config file does not exist %q: %w", path, err)
		}
		return filepath.Clean(path), nil
	}

	candidates := []string{
		"/config/config.yaml",
		"/config/hass-proxy.yaml",
		"config.yaml",
		"hass-proxy.yaml",
		"config/config.yaml",
	}

	for _, cand := range candidates {
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
			return filepath.Clean(cand), nil
		}
	}

	return "", fmt.Errorf("no config file found in candidate paths: %s", strings.Join(candidates, ", "))
}

// Validate checks required fields and value ranges.
func (c *Config) Validate() error {
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port must be between 1 and 65535, got %d", c.Server.Port)
	}
	if strings.TrimSpace(c.HomeAssistant.URL) == "" {
		return fmt.Errorf("homeassistant.url must not be empty")
	}
	return nil
}
