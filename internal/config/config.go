package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultConfigPath is the canonical container filesystem path to the user configuration.
const DefaultConfigPath = "/config/config.yaml"

var interpolationRegex = regexp.MustCompile(`\$\{([^}]+)\}`)

// Config represents the root configuration schema of Mirrormere.
type Config struct {
	Timezone string        `yaml:"timezone"`
	Display  DisplayConfig `yaml:"display"`

	location *time.Location
}

// DisplayConfig specifies visual presentation, rotation parameters, and active widget instances.
type DisplayConfig struct {
	Rotation RotationConfig `yaml:"rotation"`
	Header   HeaderConfig   `yaml:"header"`
	Grid     GridConfig     `yaml:"grid"`
	Widgets  []WidgetConfig `yaml:"widgets"`
}

// RotationConfig configures screen advance intervals and transition visual hints.
type RotationConfig struct {
	IntervalSeconds      *int   `yaml:"interval_seconds,omitempty"`
	Transition           string `yaml:"transition,omitempty"`
	PauseOnTouch         *bool  `yaml:"pause_on_touch,omitempty"`
	PauseDurationSeconds *int   `yaml:"pause_duration_seconds,omitempty"`
}

// HeaderConfig configures the persistent glance banner at the top of the display.
type HeaderConfig struct {
	Enabled  *bool                `yaml:"enabled,omitempty"`
	Elements []string             `yaml:"elements,omitempty"`
	Weather  *HeaderWeatherConfig `yaml:"weather,omitempty"`
}

// HeaderWeatherConfig configures the autonomous top-banner weather poller.
type HeaderWeatherConfig struct {
	RefreshIntervalSeconds *int    `yaml:"refresh_interval_seconds,omitempty"`
	Latitude               float64 `yaml:"latitude"`
	Longitude              float64 `yaml:"longitude"`
	Units                  string  `yaml:"units,omitempty"`
}

// GetRefreshIntervalSeconds returns the weather polling cadence in seconds (default: 900).
func (hw *HeaderWeatherConfig) GetRefreshIntervalSeconds() int {
	if hw == nil || hw.RefreshIntervalSeconds == nil {
		return 900
	}
	return *hw.RefreshIntervalSeconds
}

// GridConfig defines the discrete tile geometry (strictly 6x2 per SPEC-005).
type GridConfig struct {
	Columns int `yaml:"columns,omitempty"`
	Rows    int `yaml:"rows,omitempty"`
}

// WidgetConfig declares a self-contained widget instance with placement and domain settings.
type WidgetConfig struct {
	ID                     string            `yaml:"id"`
	Type                   string            `yaml:"type"`
	Dimensions             []int             `yaml:"dimensions,omitempty"`
	Pinned                 bool              `yaml:"pinned"`
	RefreshIntervalSeconds *int              `yaml:"refresh_interval_seconds,omitempty"`
	Endpoint               string            `yaml:"endpoint,omitempty"`
	Method                 string            `yaml:"method,omitempty"`
	TokenEnv               string            `yaml:"token_env,omitempty"`
	Token                  string            `yaml:"-"` // Resolved secret populated from TokenEnv
	Secrets                map[string]string `yaml:"-"` // Resolved secrets populated from *_env keys in Config
	Config                 map[string]any    `yaml:"config,omitempty"`
}

// Location returns the validated IANA time.Location pointer for the household.
func (c *Config) Location() *time.Location {
	if c.location != nil {
		return c.location
	}
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		return time.UTC
	}
	c.location = loc
	return loc
}

// GetIntervalSeconds returns rotation interval in seconds (default: 30).
func (r *RotationConfig) GetIntervalSeconds() int {
	if r.IntervalSeconds == nil {
		return 30
	}
	return *r.IntervalSeconds
}

// GetTransition returns the transition visual hint (default: "slide").
func (r *RotationConfig) GetTransition() string {
	if r.Transition == "" {
		return "slide"
	}
	return r.Transition
}

// GetPauseOnTouch returns whether interaction pauses rotation (default: true).
func (r *RotationConfig) GetPauseOnTouch() bool {
	if r.PauseOnTouch == nil {
		return true
	}
	return *r.PauseOnTouch
}

// GetPauseDurationSeconds returns touch interaction pause duration in seconds (default: 120).
func (r *RotationConfig) GetPauseDurationSeconds() int {
	if r.PauseDurationSeconds == nil {
		return 120
	}
	return *r.PauseDurationSeconds
}

// IsEnabled returns whether the top banner header is active (default: true).
func (h *HeaderConfig) IsEnabled() bool {
	if h.Enabled == nil {
		return true
	}
	return *h.Enabled
}

// DefaultHeaderElements returns the standard header elements if none specified.
func DefaultHeaderElements() []string {
	return []string{"clock", "date", "weather_badge", "sync_status"}
}

// Load reads and parses the configuration file from path with system environment resolution.
// If path is empty, DefaultConfigPath is used.
func Load(path string) (*Config, error) {
	return LoadWithEnv(path, os.Getenv)
}

// LoadWithEnv reads and parses the configuration file from path using the supplied environment resolver.
// If path is empty, DefaultConfigPath is used.
func LoadWithEnv(path string, getenv func(string) string) (*Config, error) {
	if path == "" {
		path = DefaultConfigPath
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read configuration file %q: %w", path, err)
	}
	return ParseWithEnv(data, getenv)
}

// Parse parses raw YAML bytes with system environment resolution.
func Parse(data []byte) (*Config, error) {
	return ParseWithEnv(data, os.Getenv)
}

// ParseWithEnv parses raw configuration YAML bytes, applies defaults, validates constraints,
// and resolves secrets through the provided environment lookup function.
func ParseWithEnv(data []byte, getenv func(string) string) (*Config, error) {
	if len(data) == 0 || len(strings.TrimSpace(string(data))) == 0 {
		return nil, fmt.Errorf("configuration is empty")
	}

	if getenv == nil {
		getenv = os.Getenv
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("yaml syntax error: %w", err)
	}

	if err := checkNodeForInterpolation(&root); err != nil {
		return nil, err
	}

	if err := validateStructuralKeys(&root); err != nil {
		return nil, err
	}

	var cfg Config
	if err := root.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("failed to decode configuration: %w", err)
	}

	cfg.applyDefaults()

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	if err := cfg.ResolveEnv(getenv); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func checkNodeForInterpolation(node *yaml.Node) error {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.ScalarNode {
		if interpolationRegex.MatchString(node.Value) {
			return fmt.Errorf("line %d: arbitrary '${...}' string interpolation is prohibited in '%s'; use explicit '*_env' keys instead (e.g. token_env, url_env)", node.Line, node.Value)
		}
	}
	for _, child := range node.Content {
		if err := checkNodeForInterpolation(child); err != nil {
			return err
		}
	}
	return nil
}

func validateStructuralKeys(root *yaml.Node) error {
	if len(root.Content) == 0 {
		return fmt.Errorf("configuration is empty")
	}

	topNode := root.Content[0]
	if topNode.Kind != yaml.MappingNode {
		return fmt.Errorf("configuration root must be a YAML mapping")
	}

	for i := 0; i < len(topNode.Content); i += 2 {
		keyNode := topNode.Content[i]
		valNode := topNode.Content[i+1]
		key := keyNode.Value

		switch key {
		case "screens":
			return fmt.Errorf("line %d: configuration contains disallowed top-level key 'screens': screens are dynamically computed by the 6x2 layout solver", keyNode.Line)
		case "providers":
			return fmt.Errorf("line %d: configuration contains disallowed top-level key 'providers': widget instances configure their own data sources under display.widgets", keyNode.Line)
		case "header":
			return fmt.Errorf("line %d: configuration contains disallowed top-level key 'header': header must be configured under display.header", keyNode.Line)
		case "timezone":
			// Canonical top-level key
		case "display":
			if valNode.Kind == yaml.MappingNode {
				if err := validateDisplayKeys(valNode); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("line %d: unknown or disallowed top-level key '%s'", keyNode.Line, key)
		}
	}

	return nil
}

func validateDisplayKeys(displayNode *yaml.Node) error {
	for i := 0; i < len(displayNode.Content); i += 2 {
		keyNode := displayNode.Content[i]
		key := keyNode.Value

		switch key {
		case "screens":
			return fmt.Errorf("line %d: display contains disallowed key 'screens': screens are dynamically computed by the 6x2 layout solver", keyNode.Line)
		case "providers":
			return fmt.Errorf("line %d: display contains disallowed key 'providers': widget instances configure their own data sources under display.widgets", keyNode.Line)
		case "rotation", "grid", "header", "widgets":
			// Canonical keys
		default:
			return fmt.Errorf("line %d: unknown key '%s' under display", keyNode.Line, key)
		}
	}
	return nil
}

func (c *Config) applyDefaults() {
	// Rotation defaults
	if c.Display.Rotation.Transition == "" {
		c.Display.Rotation.Transition = "slide"
	}

	// Grid defaults
	if c.Display.Grid.Columns == 0 {
		c.Display.Grid.Columns = 6
	}
	if c.Display.Grid.Rows == 0 {
		c.Display.Grid.Rows = 2
	}

	// Header defaults
	if c.Display.Header.Elements == nil {
		c.Display.Header.Elements = DefaultHeaderElements()
	}

	// Header Weather defaults if configured
	if c.Display.Header.Weather != nil {
		if c.Display.Header.Weather.RefreshIntervalSeconds == nil {
			defaultInterval := 900
			c.Display.Header.Weather.RefreshIntervalSeconds = &defaultInterval
		}
		if c.Display.Header.Weather.Units == "" {
			c.Display.Header.Weather.Units = "imperial"
		}
	}
}

// Validate checks the semantic correctness of the parsed configuration.
func (c *Config) Validate() error {
	// 1. Timezone
	if strings.TrimSpace(c.Timezone) == "" {
		return fmt.Errorf("missing required configuration field 'timezone'")
	}
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		return fmt.Errorf("invalid timezone '%s': %w", c.Timezone, err)
	}
	c.location = loc

	// 2. Grid validation (SPEC-005: strictly 6 columns by 2 rows)
	if c.Display.Grid.Columns != 6 {
		return fmt.Errorf("grid columns must be 6 (got %d)", c.Display.Grid.Columns)
	}
	if c.Display.Grid.Rows != 2 {
		return fmt.Errorf("grid rows must be 2 (got %d)", c.Display.Grid.Rows)
	}

	// 3. Rotation validation
	if c.Display.Rotation.IntervalSeconds != nil && *c.Display.Rotation.IntervalSeconds < 0 {
		return fmt.Errorf("rotation interval_seconds must be non-negative (got %d)", *c.Display.Rotation.IntervalSeconds)
	}
	if c.Display.Rotation.PauseDurationSeconds != nil && *c.Display.Rotation.PauseDurationSeconds < 0 {
		return fmt.Errorf("rotation pause_duration_seconds must be non-negative (got %d)", *c.Display.Rotation.PauseDurationSeconds)
	}
	switch c.Display.Rotation.Transition {
	case "slide", "fade", "instant", "none":
		// valid
	default:
		return fmt.Errorf("invalid rotation transition '%s': must be one of slide, fade, instant, none", c.Display.Rotation.Transition)
	}

	// 4. Header Weather validation
	if hw := c.Display.Header.Weather; hw != nil {
		if hw.RefreshIntervalSeconds != nil && *hw.RefreshIntervalSeconds <= 0 {
			return fmt.Errorf("header weather refresh_interval_seconds must be positive (got %d)", *hw.RefreshIntervalSeconds)
		}
		if hw.Latitude < -90.0 || hw.Latitude > 90.0 {
			return fmt.Errorf("header weather latitude must be between -90 and 90 (got %f)", hw.Latitude)
		}
		if hw.Longitude < -180.0 || hw.Longitude > 180.0 {
			return fmt.Errorf("header weather longitude must be between -180 and 180 (got %f)", hw.Longitude)
		}
		switch strings.ToLower(hw.Units) {
		case "imperial", "metric":
			// valid
		default:
			return fmt.Errorf("invalid header weather units '%s': must be 'imperial' or 'metric'", hw.Units)
		}
	}

	// 5. Widgets validation
	seenIDs := make(map[string]int, len(c.Display.Widgets))
	for i := range c.Display.Widgets {
		w := &c.Display.Widgets[i]
		if strings.TrimSpace(w.ID) == "" {
			return fmt.Errorf("widget at index %d is missing required field 'id'", i)
		}
		if prevIdx, exists := seenIDs[w.ID]; exists {
			return fmt.Errorf("duplicate widget id '%s' found in display.widgets (indices %d and %d)", w.ID, prevIdx, i)
		}
		seenIDs[w.ID] = i

		if strings.TrimSpace(w.Type) == "" {
			return fmt.Errorf("widget '%s' is missing required field 'type'", w.ID)
		}

		if len(w.Dimensions) > 0 {
			if len(w.Dimensions) != 2 {
				return fmt.Errorf("widget '%s': dimensions must contain exactly 2 integers: [cols, rows]", w.ID)
			}
			cols, rows := w.Dimensions[0], w.Dimensions[1]
			if cols < 1 || cols > 6 || rows < 1 || rows > 2 {
				return fmt.Errorf("widget '%s': invalid dimensions [%d, %d]: cols must be 1..6, rows must be 1..2", w.ID, cols, rows)
			}
		}

		if w.RefreshIntervalSeconds != nil && *w.RefreshIntervalSeconds <= 0 {
			return fmt.Errorf("widget '%s': refresh_interval_seconds must be positive (got %d)", w.ID, *w.RefreshIntervalSeconds)
		}

		if w.Method != "" {
			upper := strings.ToUpper(strings.TrimSpace(w.Method))
			if upper != "GET" && upper != "POST" {
				return fmt.Errorf("widget '%s': invalid http method '%s': must be GET or POST", w.ID, w.Method)
			}
			w.Method = upper
		} else if w.Endpoint != "" {
			w.Method = "POST"
		}
	}

	return nil
}

// ResolveEnv resolves secret environment variables for all widget instances into isolated Secrets maps.
func (c *Config) ResolveEnv(getenv func(string) string) error {
	if getenv == nil {
		getenv = os.Getenv
	}

	for i := range c.Display.Widgets {
		w := &c.Display.Widgets[i]
		if w.Secrets == nil {
			w.Secrets = make(map[string]string)
		}

		if w.TokenEnv != "" {
			envVar := strings.TrimSpace(w.TokenEnv)
			val := getenv(envVar)
			if val == "" {
				return fmt.Errorf("widget '%s': environment variable '%s' defined in token_env is unset or empty", w.ID, envVar)
			}
			w.Token = val
			w.Secrets["token"] = val
		}

		if w.Config != nil {
			if err := resolveEnvInMap(w.ID, w.Config, "", w.Secrets, getenv); err != nil {
				return err
			}
		}
	}

	return nil
}

func resolveEnvInMap(widgetID string, m map[string]any, prefix string, secrets map[string]string, getenv func(string) string) error {
	for k, v := range m {
		if strings.HasSuffix(k, "_env") {
			baseKey := strings.TrimSuffix(k, "_env")
			if _, exists := m[baseKey]; exists {
				return fmt.Errorf("widget '%s': cannot specify both '%s' and '%s'", widgetID, baseKey, k)
			}

			envVarName, ok := v.(string)
			if !ok || strings.TrimSpace(envVarName) == "" {
				return fmt.Errorf("widget '%s': key '%s' must specify a non-empty environment variable name", widgetID, k)
			}
			envVarName = strings.TrimSpace(envVarName)
			val := getenv(envVarName)
			if val == "" {
				return fmt.Errorf("widget '%s': environment variable '%s' defined in %s is unset or empty", widgetID, envVarName, k)
			}

			secretPath := baseKey
			if prefix != "" {
				secretPath = prefix + "." + baseKey
			}
			secrets[secretPath] = val
		}

		childPrefix := k
		if prefix != "" {
			childPrefix = prefix + "." + k
		}

		switch val := v.(type) {
		case map[string]any:
			if err := resolveEnvInMap(widgetID, val, childPrefix, secrets, getenv); err != nil {
				return err
			}
		case map[any]any:
			converted := make(map[string]any, len(val))
			for subK, subV := range val {
				converted[fmt.Sprint(subK)] = subV
			}
			m[k] = converted
			if err := resolveEnvInMap(widgetID, converted, childPrefix, secrets, getenv); err != nil {
				return err
			}
		case []any:
			if err := resolveEnvInSlice(widgetID, val, childPrefix, secrets, getenv); err != nil {
				return err
			}
		}
	}
	return nil
}

func resolveEnvInSlice(widgetID string, s []any, prefix string, secrets map[string]string, getenv func(string) string) error {
	for i, item := range s {
		childPrefix := fmt.Sprintf("%s[%d]", prefix, i)
		switch elem := item.(type) {
		case map[string]any:
			if err := resolveEnvInMap(widgetID, elem, childPrefix, secrets, getenv); err != nil {
				return err
			}
		case map[any]any:
			converted := make(map[string]any, len(elem))
			for subK, subV := range elem {
				converted[fmt.Sprint(subK)] = subV
			}
			s[i] = converted
			if err := resolveEnvInMap(widgetID, converted, childPrefix, secrets, getenv); err != nil {
				return err
			}
		case []any:
			if err := resolveEnvInSlice(widgetID, elem, childPrefix, secrets, getenv); err != nil {
				return err
			}
		}
	}
	return nil
}
