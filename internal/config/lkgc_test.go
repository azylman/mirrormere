package config_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azylman/mirrormere/internal/config"
	"github.com/azylman/mirrormere/internal/domain"
)

type mockPackageLoader struct {
	packages map[string]*domain.Package
	err      error
}

func (m *mockPackageLoader) LoadPackage(widgetType string) (*domain.Package, error) {
	if m.err != nil {
		return nil, m.err
	}
	pkg, ok := m.packages[widgetType]
	if !ok {
		return nil, fmt.Errorf("widget package '%s' not found", widgetType)
	}
	return pkg, nil
}

func createStandardPackages() map[string]*domain.Package {
	return map[string]*domain.Package{
		"calendar-agenda": {
			Type:   "calendar-agenda",
			Source: "builtin",
			Manifest: domain.WidgetManifest{
				Name:              "Calendar Agenda",
				Version:           "1.0.0",
				Provider:          "calendar-agenda",
				DefaultDimensions: domain.NewDimension(4, 2),
				Refresh:           domain.ManifestRefresh{IntervalSeconds: 300},
				ConfigSchema: map[string]any{
					"type":     "object",
					"required": []string{"calendars"},
					"properties": map[string]any{
						"calendars": map[string]any{
							"type": "array",
						},
					},
				},
			},
		},
		"weather-forecast": {
			Type:   "weather-forecast",
			Source: "builtin",
			Manifest: domain.WidgetManifest{
				Name:              "Weather Forecast",
				Version:           "1.0.0",
				Provider:          "weather-forecast",
				DefaultDimensions: domain.NewDimension(2, 1),
				Refresh:           domain.ManifestRefresh{IntervalSeconds: 600},
			},
		},
		"tasks": {
			Type:   "tasks",
			Source: "builtin",
			Manifest: domain.WidgetManifest{
				Name:              "Tasks & Lists",
				Version:           "1.0.0",
				Provider:          "tasks",
				DefaultDimensions: domain.NewDimension(2, 1),
				Refresh:           domain.ManifestRefresh{IntervalSeconds: 120},
				ConfigSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"list_id": map[string]any{"type": "string"},
						"source":  map[string]any{"type": "string"},
					},
				},
			},
		},
		"spacer": {
			Type:   "spacer",
			Source: "builtin",
			Manifest: domain.WidgetManifest{
				Name:              "Spacer Tile",
				Version:           "1.0.0",
				Provider:          "spacer",
				DefaultDimensions: domain.NewDimension(2, 1),
			},
		},
		"sensor-http": {
			Type:   "sensor-http",
			Source: "custom",
			Manifest: domain.WidgetManifest{
				Name:              "HTTP Sensor",
				Version:           "1.0.0",
				Provider:          "http",
				DefaultDimensions: domain.NewDimension(2, 1),
				ResponseSchema: map[string]any{
					"type": "object",
				},
			},
		},
	}
}

const validBaseYAML = `
timezone: America/New_York
display:
  grid:
    columns: 6
    rows: 2
  rotation:
    interval_seconds: 30
    transition: slide
  widgets:
    - id: cal-hero
      type: calendar-agenda
      dimensions: [4, 2]
      pinned: true
      config:
        calendars:
          - name: Family
    - id: weather-1
      type: weather-forecast
    - id: tasks-1
      type: tasks
      dimensions: [2, 1]
      config:
        list_id: chores
        source: local
`

func TestLKGC_ColdBootAndAccessors(t *testing.T) {
	t.Parallel()

	loader := &mockPackageLoader{packages: createStandardPackages()}
	m, err := config.NewManager([]byte(validBaseYAML), loader, nil, nil)
	if err != nil {
		t.Fatalf("unexpected cold boot error: %v", err)
	}

	// Current Config
	cfg := m.Current()
	if cfg == nil {
		t.Fatal("expected non-nil Current() config")
	}
	if cfg.Timezone != "America/New_York" {
		t.Errorf("expected timezone America/New_York, got %s", cfg.Timezone)
	}
	if len(cfg.Display.Widgets) != 3 {
		t.Fatalf("expected 3 widgets, got %d", len(cfg.Display.Widgets))
	}

	// Current Layout
	l := m.CurrentLayout()
	if l == nil {
		t.Fatal("expected non-nil CurrentLayout()")
	}
	if l.TotalScreens != 1 {
		t.Errorf("expected 1 screen, got %d", l.TotalScreens)
	}
	if len(l.Screens[0].Widgets) != 3 {
		t.Errorf("expected 3 placed widgets on screen 0, got %d", len(l.Screens[0].Widgets))
	}

	// Snapshot
	snap := m.CurrentSnapshot()
	if snap == nil || snap.Config == nil || snap.Layout == nil || snap.Packages == nil {
		t.Fatal("expected complete non-nil Snapshot")
	}
	if snap.LoadedAt.IsZero() {
		t.Error("expected non-zero LoadedAt")
	}

	// Status
	st := m.Status()
	if st.ConfigStatus != config.ConfigStatusOK {
		t.Errorf("expected status 'ok', got %s", st.ConfigStatus)
	}
	if st.ConfigError != nil {
		t.Errorf("expected nil ConfigError, got %v", *st.ConfigError)
	}
	if st.LastChecked.IsZero() || st.LastSuccess.IsZero() {
		t.Error("expected non-zero LastChecked and LastSuccess")
	}
}

func TestLKGC_ColdBootFailures(t *testing.T) {
	t.Parallel()

	loader := &mockPackageLoader{packages: createStandardPackages()}

	// 1. Nil loader
	_, err := config.NewManager([]byte(validBaseYAML), nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "package loader cannot be nil") {
		t.Fatalf("expected nil loader error, got %v", err)
	}

	// 2. Invalid initial YAML
	badYAML := []byte("invalid: yaml: [")
	_, err = config.NewManager(badYAML, loader, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "initial configuration validation failed") {
		t.Fatalf("expected initial validation error, got %v", err)
	}
}

func TestLKGC_Stage1_SyntaxAndStructureFailures(t *testing.T) {
	t.Parallel()

	loader := &mockPackageLoader{packages: createStandardPackages()}

	testCases := []struct {
		name        string
		yaml        string
		errContains string
	}{
		{
			name:        "syntax error",
			yaml:        "display: {bad: [yaml",
			errContains: "YAML syntax error",
		},
		{
			name:        "empty configuration",
			yaml:        "",
			errContains: "configuration file is empty",
		},
		{
			name:        "root not mapping",
			yaml:        "- item 1\n- item 2",
			errContains: "configuration root must be a YAML mapping",
		},
		{
			name: "disallowed screens top-level key",
			yaml: `
timezone: UTC
screens: []
display:
  widgets: []
`,
			errContains: "disallowed top-level key 'screens'",
		},
		{
			name: "disallowed providers top-level key",
			yaml: `
timezone: UTC
providers: []
display:
  widgets: []
`,
			errContains: "disallowed top-level key 'providers'",
		},
		{
			name: "disallowed root header key",
			yaml: `
timezone: UTC
header: {}
display:
  widgets: []
`,
			errContains: "header must be configured under display.header",
		},
		{
			name: "prohibited string interpolation",
			yaml: `
timezone: ${TZ}
display:
  widgets: []
`,
			errContains: "arbitrary '${...}' string interpolation is prohibited",
		},
		{
			name: "unknown top-level key",
			yaml: `
timezone: UTC
bogus_key: 123
display:
  widgets: []
`,
			errContains: "unknown or disallowed top-level key 'bogus_key'",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m, err := config.NewManager([]byte(validBaseYAML), loader, nil, nil)
			if err != nil {
				t.Fatalf("setup failed: %v", err)
			}
			_, _, err = m.Reload([]byte(tc.yaml))
			if err == nil || !strings.Contains(err.Error(), tc.errContains) {
				t.Fatalf("expected error containing %q, got %v", tc.errContains, err)
			}
			// Verify LKGC retention
			if m.Current().Timezone != "America/New_York" {
				t.Errorf("LKGC violated: running timezone changed to %s", m.Current().Timezone)
			}
			st := m.Status()
			if st.ConfigStatus != config.ConfigStatusError {
				t.Errorf("expected status 'error', got %s", st.ConfigStatus)
			}
			if st.ConfigError == nil || !strings.Contains(*st.ConfigError, tc.errContains) {
				t.Errorf("expected ConfigError containing %q, got %v", tc.errContains, st.ConfigError)
			}
		})
	}
}

func TestLKGC_Stage2_CoreInstanceSchemaFailures(t *testing.T) {
	t.Parallel()

	loader := &mockPackageLoader{packages: createStandardPackages()}

	testCases := []struct {
		name        string
		yaml        string
		errContains string
	}{
		{
			name: "invalid timezone",
			yaml: `
timezone: Invalid/Zone_Name
display:
  widgets: []
`,
			errContains: "unknown time zone",
		},
		{
			name: "invalid grid dimensions",
			yaml: `
timezone: UTC
display:
  grid:
    columns: 5
    rows: 2
  widgets: []
`,
			errContains: "grid columns must be 6",
		},
		{
			name: "duplicate widget ID",
			yaml: `
timezone: UTC
display:
  widgets:
    - id: duplicate-id
      type: spacer
      dimensions: [6, 1]
    - id: duplicate-id
      type: spacer
      dimensions: [6, 1]
`,
			errContains: "duplicate widget id 'duplicate-id'",
		},
		{
			name: "empty widget ID",
			yaml: `
timezone: UTC
display:
  widgets:
    - id: ""
      type: spacer
      dimensions: [6, 2]
`,
			errContains: "missing required field 'id'",
		},
		{
			name: "empty widget type",
			yaml: `
timezone: UTC
display:
  widgets:
    - id: w1
      type: ""
      dimensions: [6, 2]
`,
			errContains: "missing required field 'type'",
		},
		{
			name: "dimensions out of bounds",
			yaml: `
timezone: UTC
display:
  widgets:
    - id: w1
      type: spacer
      dimensions: [7, 1]
`,
			errContains: "invalid dimensions [7, 1]: cols must be 1..6, rows must be 1..2",
		},
		{
			name: "non-positive refresh interval",
			yaml: `
timezone: UTC
display:
  widgets:
    - id: w1
      type: spacer
      dimensions: [6, 2]
      refresh_interval_seconds: 0
`,
			errContains: "refresh_interval_seconds must be positive",
		},
		{
			name: "invalid HTTP method",
			yaml: `
timezone: UTC
display:
  widgets:
    - id: w1
      type: spacer
      dimensions: [6, 2]
      method: "PATCH"
`,
			errContains: "invalid http method 'PATCH'",
		},
		{
			name: "invalid endpoint URL",
			yaml: `
timezone: UTC
display:
  widgets:
    - id: w1
      type: spacer
      dimensions: [6, 2]
      endpoint: "ftp://example.com/data"
`,
			errContains: "invalid endpoint URL 'ftp://example.com/data'",
		},
		{
			name: "unset token_env",
			yaml: `
timezone: UTC
display:
  widgets:
    - id: w1
      type: spacer
      dimensions: [6, 2]
      token_env: UNSET_TOKEN_VAR_xyz
`,
			errContains: "defined in token_env is unset or empty",
		},
		{
			name: "conflicting key and key_env",
			yaml: `
timezone: UTC
display:
  widgets:
    - id: w1
      type: spacer
      dimensions: [6, 2]
      config:
        feed: "http://public"
        feed_env: FEED_SECRET_xyz
`,
			errContains: "cannot specify both 'feed' and 'feed_env'",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m, err := config.NewManager([]byte(validBaseYAML), loader, nil, nil)
			if err != nil {
				t.Fatalf("setup failed: %v", err)
			}
			_, _, err = m.Reload([]byte(tc.yaml))
			if err == nil || !strings.Contains(err.Error(), tc.errContains) {
				t.Fatalf("expected error containing %q, got %v", tc.errContains, err)
			}
			// Verify LKGC retention
			if m.Current().Timezone != "America/New_York" {
				t.Errorf("LKGC violated: running timezone changed to %s", m.Current().Timezone)
			}
		})
	}
}

func TestLKGC_Stage3_PackageExistenceAndCompletenessFailures(t *testing.T) {
	t.Parallel()

	packages := createStandardPackages()

	// 1. Missing package
	loaderMissing := &mockPackageLoader{packages: packages}
	m, err := config.NewManager([]byte(validBaseYAML), loaderMissing, nil, nil)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	yamlMissingPkg := `
timezone: America/New_York
display:
  widgets:
    - id: w-unknown
      type: non-existent-widget
      dimensions: [6, 2]
`
	_, _, err = m.Reload([]byte(yamlMissingPkg))
	if err == nil || !strings.Contains(err.Error(), "widget package 'non-existent-widget' not found") {
		t.Fatalf("expected missing package error, got %v", err)
	}
	if m.Current().Timezone != "America/New_York" {
		t.Error("LKGC violated")
	}

	// 2. Package manifest invalid
	packages["invalid-manifest"] = &domain.Package{
		Type: "invalid-manifest",
		Manifest: domain.WidgetManifest{
			Name: "", // missing required name
		},
	}
	yamlBadManifest := `
timezone: America/New_York
display:
  widgets:
    - id: w-bad
      type: invalid-manifest
      dimensions: [6, 2]
`
	_, _, err = m.Reload([]byte(yamlBadManifest))
	if err == nil || !strings.Contains(err.Error(), "manifest invalid") {
		t.Fatalf("expected manifest invalid error, got %v", err)
	}

	// 3. Instance omits dimensions and manifest has no default_dimensions
	packages["no-defaults"] = &domain.Package{
		Type: "no-defaults",
		Manifest: domain.WidgetManifest{
			Name:     "No Defaults",
			Version:  "1.0.0",
			Provider: "tasks",
		},
	}
	yamlNoDims := `
timezone: America/New_York
display:
  widgets:
    - id: w-nodim
      type: no-defaults
      config:
        source: local
`
	_, _, err = m.Reload([]byte(yamlNoDims))
	if err == nil || !strings.Contains(err.Error(), "has no valid default_dimensions") {
		t.Fatalf("expected missing default_dimensions error, got %v", err)
	}

	// 4. Instance dimensions not supported by package supported_dimensions
	packages["restricted-dims"] = &domain.Package{
		Type: "restricted-dims",
		Manifest: domain.WidgetManifest{
			Name:                "Restricted Dims",
			Version:             "1.0.0",
			Provider:            "spacer",
			DefaultDimensions:   domain.NewDimension(2, 1),
			SupportedDimensions: []domain.Dimension{domain.NewDimension(2, 1)},
		},
	}
	yamlUnsupportedDims := `
timezone: America/New_York
display:
  widgets:
    - id: w-res
      type: restricted-dims
      dimensions: [4, 2]
`
	_, _, err = m.Reload([]byte(yamlUnsupportedDims))
	if err == nil || !strings.Contains(err.Error(), "not supported by package 'restricted-dims'") {
		t.Fatalf("expected unsupported dimensions error, got %v", err)
	}
}

func TestLKGC_Stage4_ManifestConfigSchemaFailures(t *testing.T) {
	t.Parallel()

	packages := createStandardPackages()
	loader := &mockPackageLoader{packages: packages}
	m, err := config.NewManager([]byte(validBaseYAML), loader, nil, nil)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// 1. Missing required property 'calendars' in calendar-agenda config
	yamlMissingProp := `
timezone: America/New_York
display:
  widgets:
    - id: cal-missing
      type: calendar-agenda
      dimensions: [4, 2]
      config:
        view: "month"
    - id: filler
      type: spacer
      dimensions: [2, 2]
`
	_, _, err = m.Reload([]byte(yamlMissingProp))
	if err == nil || !strings.Contains(err.Error(), "manifest config_schema validation error for widget 'cal-missing'") {
		t.Fatalf("expected schema validation error, got %v", err)
	}
	if m.Current().Timezone != "America/New_York" {
		t.Error("LKGC violated")
	}

	// 2. Type mismatch: calendars is a string instead of an array
	yamlTypeMismatch := `
timezone: America/New_York
display:
  widgets:
    - id: cal-mismatch
      type: calendar-agenda
      dimensions: [4, 2]
      config:
        calendars: "not-an-array"
    - id: filler
      type: spacer
      dimensions: [2, 2]
`
	_, _, err = m.Reload([]byte(yamlTypeMismatch))
	if err == nil || !strings.Contains(err.Error(), "manifest config_schema validation error for widget 'cal-mismatch'") {
		t.Fatalf("expected type mismatch error, got %v", err)
	}

	// 3. Nil config when manifest requires schema properties
	yamlNilConfig := `
timezone: America/New_York
display:
  widgets:
    - id: cal-nil
      type: calendar-agenda
      dimensions: [4, 2]
    - id: filler
      type: spacer
      dimensions: [2, 2]
`
	_, _, err = m.Reload([]byte(yamlNilConfig))
	if err == nil || !strings.Contains(err.Error(), "manifest config_schema validation error for widget 'cal-nil'") {
		t.Fatalf("expected nil config schema error, got %v", err)
	}
}

func TestLKGC_Stage5_DomainAndSourceOfTruthRules(t *testing.T) {
	t.Parallel()

	packages := createStandardPackages()
	loader := &mockPackageLoader{packages: packages}
	m, err := config.NewManager([]byte(validBaseYAML), loader, nil, nil)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// 1. Tasks widget with NO source definition for list_id
	yamlNoSource := `
timezone: America/New_York
display:
  widgets:
    - id: my-todo
      type: tasks
      dimensions: [6, 2]
      config:
        list_id: "orphaned-tasks"
`
	_, _, err = m.Reload([]byte(yamlNoSource))
	expectedErr := "[Mirrormere Config Error] tasks widget 'my-todo' references list_id 'orphaned-tasks' with no primary source definition"
	if err == nil || !strings.Contains(err.Error(), expectedErr) {
		t.Fatalf("expected %q, got %v", expectedErr, err)
	}

	// 2. Tasks widget with invalid source
	yamlBadSource := `
timezone: America/New_York
display:
  widgets:
    - id: my-todo
      type: tasks
      dimensions: [6, 2]
      config:
        list_id: "test"
        source: "dropbox"
`
	_, _, err = m.Reload([]byte(yamlBadSource))
	if err == nil || !strings.Contains(err.Error(), "declares invalid source 'dropbox'") {
		t.Fatalf("expected invalid source error, got %v", err)
	}

	// 3. Tasks source: http missing http block
	yamlHTTPMissingBlock := `
timezone: America/New_York
display:
  widgets:
    - id: my-todo
      type: tasks
      dimensions: [6, 2]
      config:
        list_id: "test"
        source: "http"
`
	_, _, err = m.Reload([]byte(yamlHTTPMissingBlock))
	if err == nil || !strings.Contains(err.Error(), "requires 'http' config block") {
		t.Fatalf("expected missing http block error, got %v", err)
	}

	// 4. Tasks source: http with invalid base_url
	yamlHTTPBadURL := `
timezone: America/New_York
display:
  widgets:
    - id: my-todo
      type: tasks
      dimensions: [6, 2]
      config:
        list_id: "test"
        source: "http"
        http:
          base_url: "ftp://example.com"
`
	_, _, err = m.Reload([]byte(yamlHTTPBadURL))
	if err == nil || !strings.Contains(err.Error(), "invalid http.base_url") {
		t.Fatalf("expected invalid http.base_url error, got %v", err)
	}

	// 5. Tasks source: gtasks missing tasklist_id
	yamlGTasksNoID := `
timezone: America/New_York
display:
  widgets:
    - id: my-todo
      type: tasks
      dimensions: [6, 2]
      config:
        list_id: "test"
        source: "gtasks"
        gtasks:
          tasklist_id: ""
`
	_, _, err = m.Reload([]byte(yamlGTasksNoID))
	if err == nil || !strings.Contains(err.Error(), "requires non-empty 'gtasks.tasklist_id'") {
		t.Fatalf("expected missing tasklist_id error, got %v", err)
	}

	// 6. Conflicting primary sources for same list_id
	yamlConflictingSources := `
timezone: America/New_York
display:
  widgets:
    - id: tasks-primary-1
      type: tasks
      dimensions: [3, 2]
      config:
        list_id: "shared-chores"
        source: "local"
    - id: tasks-primary-2
      type: tasks
      dimensions: [3, 2]
      config:
        list_id: "shared-chores"
        source: "gtasks"
        gtasks:
          tasklist_id: "list-123"
`
	_, _, err = m.Reload([]byte(yamlConflictingSources))
	if err == nil || !strings.Contains(err.Error(), "conflicting source definitions for list_id 'shared-chores'") {
		t.Fatalf("expected conflicting source error, got %v", err)
	}

	// 7. Generic HTTP provider missing endpoint URL
	yamlHTTPNoEndpoint := `
timezone: America/New_York
display:
  widgets:
    - id: sensor-1
      type: sensor-http
      dimensions: [6, 2]
`
	_, _, err = m.Reload([]byte(yamlHTTPNoEndpoint))
	if err == nil || !strings.Contains(err.Error(), "provider 'http' requires non-empty 'endpoint' URL") {
		t.Fatalf("expected missing endpoint error for http provider, got %v", err)
	}

	// 8. Generic HTTP provider with invalid endpoint URL
	yamlHTTPInvalidEndpoint := `
timezone: America/New_York
display:
  widgets:
    - id: sensor-1
      type: sensor-http
      dimensions: [6, 2]
      endpoint: "notaurl"
`
	_, _, err = m.Reload([]byte(yamlHTTPInvalidEndpoint))
	if err == nil || !strings.Contains(err.Error(), "invalid endpoint URL 'notaurl'") {
		t.Fatalf("expected invalid endpoint error, got %v", err)
	}

	// 9. Valid secondary consumer referencing primary tasks widget (must succeed!)
	yamlValidSecondary := `
timezone: America/New_York
display:
  widgets:
    - id: primary-tasks
      type: tasks
      dimensions: [3, 2]
      config:
        list_id: "chores"
        source: "local"
    - id: secondary-tasks
      type: tasks
      dimensions: [3, 2]
      config:
        list_id: "chores"
`
	snap, diff, err := m.Reload([]byte(yamlValidSecondary))
	if err != nil {
		t.Fatalf("unexpected error for valid secondary tasks reference: %v", err)
	}
	if snap == nil || diff == nil {
		t.Fatal("expected non-nil snap and diff")
	}
}

func TestLKGC_Stage6_LayoutSolverFailures(t *testing.T) {
	t.Parallel()

	packages := createStandardPackages()
	loader := &mockPackageLoader{packages: packages}
	m, err := config.NewManager([]byte(validBaseYAML), loader, nil, nil)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// 1. Cell deficit (10 cells total on 6x2 grid)
	yamlDeficit := `
timezone: America/New_York
display:
  widgets:
    - id: cal-1
      type: calendar-agenda
      dimensions: [4, 2]
      config:
        calendars: []
    - id: weather-1
      type: weather-forecast
      dimensions: [2, 1]
`
	_, _, err = m.Reload([]byte(yamlDeficit))
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "deficit") {
		t.Fatalf("expected deficit error from solver, got %v", err)
	}
	if m.Current().Timezone != "America/New_York" {
		t.Error("LKGC violated")
	}

	// 2. Geometric conflict
	yamlGeometric := `
timezone: America/New_York
display:
  widgets:
    - id: hero-5x2
      type: spacer
      dimensions: [5, 2]
    - id: side-2x1
      type: spacer
      dimensions: [2, 1]
`
	_, _, err = m.Reload([]byte(yamlGeometric))
	if err == nil || !strings.Contains(err.Error(), "layout solver error") {
		t.Fatalf("expected layout solver error, got %v", err)
	}
}

func TestLKGC_DiffConfigs(t *testing.T) {
	t.Parallel()

	oldCfg := &config.Config{
		Display: config.DisplayConfig{
			Widgets: []config.WidgetConfig{
				{ID: "unchanged-1", Type: "spacer", Dimensions: []int{2, 1}},
				{ID: "removed-1", Type: "spacer", Dimensions: []int{2, 1}},
				{ID: "mod-transport", Type: "spacer", Dimensions: []int{2, 1}, Endpoint: "http://old"},
				{ID: "mod-domain", Type: "tasks", Dimensions: []int{2, 1}, Config: map[string]any{"list_id": "old"}},
				{ID: "replaced-1", Type: "tasks", Dimensions: []int{2, 1}},
			},
		},
	}

	newInterval := 60
	newCfg := &config.Config{
		Display: config.DisplayConfig{
			Widgets: []config.WidgetConfig{
				{ID: "unchanged-1", Type: "spacer", Dimensions: []int{2, 1}},
				{ID: "added-1", Type: "spacer", Dimensions: []int{2, 1}},
				{ID: "mod-transport", Type: "spacer", Dimensions: []int{2, 1}, Endpoint: "http://new", RefreshIntervalSeconds: &newInterval},
				{ID: "mod-domain", Type: "tasks", Dimensions: []int{2, 1}, Config: map[string]any{"list_id": "new"}},
				{ID: "replaced-1", Type: "spacer", Dimensions: []int{2, 1}}, // Type changed from tasks to spacer
			},
		},
	}

	diff := config.DiffConfigs(oldCfg, newCfg)
	if len(diff.Unchanged) != 1 || diff.Unchanged[0].ID != "unchanged-1" {
		t.Errorf("expected 1 unchanged widget, got %v", diff.Unchanged)
	}
	if len(diff.Added) != 1 || diff.Added[0].ID != "added-1" {
		t.Errorf("expected 1 added widget, got %v", diff.Added)
	}
	if len(diff.Removed) != 1 || diff.Removed[0].ID != "removed-1" {
		t.Errorf("expected 1 removed widget, got %v", diff.Removed)
	}
	if len(diff.Modified) != 3 {
		t.Fatalf("expected 3 modified widgets, got %d", len(diff.Modified))
	}

	for _, m := range diff.Modified {
		switch m.ID {
		case "mod-transport":
			if m.DomainChange {
				t.Error("expected DomainChange=false for transport change")
			}
			if m.Change != config.ChangeModified {
				t.Errorf("expected ChangeModified, got %s", m.Change)
			}
		case "mod-domain":
			if !m.DomainChange {
				t.Error("expected DomainChange=true for domain config change")
			}
			if m.Change != config.ChangeModified {
				t.Errorf("expected ChangeModified, got %s", m.Change)
			}
		case "replaced-1":
			if m.Change != config.ChangeReplaced {
				t.Errorf("expected ChangeReplaced, got %s", m.Change)
			}
		}
	}
}

func TestLKGC_RecoveryAndTelemetry(t *testing.T) {
	t.Parallel()

	loader := &mockPackageLoader{packages: createStandardPackages()}
	m, err := config.NewManager([]byte(validBaseYAML), loader, nil, nil)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// 1. Initial status ok
	st1 := m.Status()
	if st1.ConfigStatus != config.ConfigStatusOK {
		t.Fatalf("expected ok status, got %s", st1.ConfigStatus)
	}
	origSuccess := st1.LastSuccess

	// 2. Trigger failure
	badYAML := "syntax: [error"
	_, _, err = m.Reload([]byte(badYAML))
	if err == nil {
		t.Fatal("expected reload error")
	}

	st2 := m.Status()
	if st2.ConfigStatus != config.ConfigStatusError {
		t.Errorf("expected error status, got %s", st2.ConfigStatus)
	}
	if st2.ConfigError == nil {
		t.Error("expected non-nil ConfigError")
	}
	if st2.LastSuccess != origSuccess {
		t.Errorf("expected LastSuccess preserved on error, got %v != %v", st2.LastSuccess, origSuccess)
	}

	// 3. Subsequent valid reload recovers status to OK
	time.Sleep(10 * time.Millisecond)
	validYAML2 := strings.Replace(validBaseYAML, "America/New_York", "America/Chicago", 1)
	snap, _, err := m.Reload([]byte(validYAML2))
	if err != nil {
		t.Fatalf("recovery reload failed: %v", err)
	}
	if snap.Config.Timezone != "America/Chicago" {
		t.Errorf("expected timezone America/Chicago, got %s", snap.Config.Timezone)
	}

	st3 := m.Status()
	if st3.ConfigStatus != config.ConfigStatusOK {
		t.Errorf("expected recovered status ok, got %s", st3.ConfigStatus)
	}
	if st3.ConfigError != nil {
		t.Errorf("expected nil ConfigError after recovery, got %v", *st3.ConfigError)
	}
	if !st3.LastSuccess.After(origSuccess) {
		t.Errorf("expected LastSuccess updated after recovery: %v vs %v", st3.LastSuccess, origSuccess)
	}
}

func TestLKGC_ConcurrencyAndRace(t *testing.T) {
	t.Parallel()

	loader := &mockPackageLoader{packages: createStandardPackages()}
	m, err := config.NewManager([]byte(validBaseYAML), loader, nil, nil)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	var wg sync.WaitGroup
	stopChan := make(chan struct{})

	// 10 Reader goroutines constantly reading Current(), CurrentLayout(), and Status()
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stopChan:
					return
				default:
					cfg := m.Current()
					if cfg == nil {
						t.Errorf("got nil Current() config during concurrent read")
					}
					l := m.CurrentLayout()
					if l == nil {
						t.Errorf("got nil CurrentLayout() during concurrent read")
					}
					st := m.Status()
					if st.ConfigStatus != config.ConfigStatusOK && st.ConfigStatus != config.ConfigStatusError {
						t.Errorf("invalid status: %v", st.ConfigStatus)
					}
				}
			}
		}()
	}

	// 1 Reload worker alternating valid and invalid reloads
	wg.Add(1)
	go func() {
		defer wg.Done()
		zones := []string{"America/New_York", "America/Chicago", "America/Denver", "America/Los_Angeles"}
		for i := 0; i < 20; i++ {
			if i%2 == 0 {
				newZone := zones[i%len(zones)]
				y := strings.Replace(validBaseYAML, "America/New_York", newZone, 1)
				_, _, _ = m.Reload([]byte(y))
			} else {
				_, _, _ = m.Reload([]byte("invalid: yaml: ["))
			}
		}
		close(stopChan)
	}()

	wg.Wait()
}

func TestLKGC_EmptyManagerAccessors(t *testing.T) {
	t.Parallel()

	var emptyManager config.Manager
	if emptyManager.Current() != nil {
		t.Error("expected nil Current() on empty manager")
	}
	if emptyManager.CurrentLayout() != nil {
		t.Error("expected nil CurrentLayout() on empty manager")
	}
	st := emptyManager.Status()
	if st.ConfigStatus != config.ConfigStatusOK {
		t.Errorf("expected default ok status, got %v", st.ConfigStatus)
	}
}

func TestLKGC_ValidatePipelineNilArgs(t *testing.T) {
	t.Parallel()

	// Nil loader
	_, err := config.ValidatePipeline([]byte(validBaseYAML), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "package loader cannot be nil") {
		t.Fatalf("expected nil loader error, got %v", err)
	}

	// Nil getenv defaults without error
	loader := &mockPackageLoader{packages: createStandardPackages()}
	snap, err := config.ValidatePipeline([]byte(validBaseYAML), loader, nil)
	if err != nil {
		t.Fatalf("unexpected error with nil getenv: %v", err)
	}
	if snap == nil {
		t.Fatal("expected non-nil snap")
	}
}

func TestLKGC_Stage1_DecodeError(t *testing.T) {
	t.Parallel()

	loader := &mockPackageLoader{packages: createStandardPackages()}
	m, err := config.NewManager([]byte(validBaseYAML), loader, nil, nil)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// rotation declared as a scalar string instead of mapping
	yamlDecodeErr := `
timezone: UTC
display:
  rotation: "scalar-not-struct"
  widgets: []
`
	_, _, err = m.Reload([]byte(yamlDecodeErr))
	if err == nil || !strings.Contains(err.Error(), "failed to decode configuration") {
		t.Fatalf("expected decode error, got %v", err)
	}
}

func TestLKGC_Stage5_EdgeCases(t *testing.T) {
	t.Parallel()

	packages := createStandardPackages()
	loader := &mockPackageLoader{packages: packages}
	m, err := config.NewManager([]byte(validBaseYAML), loader, nil, nil)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// 1. Generic HTTP widget endpoint without host
	yamlNoHost := `
timezone: America/New_York
display:
  widgets:
    - id: sensor-1
      type: sensor-http
      dimensions: [6, 2]
      endpoint: "http://"
`
	_, _, err = m.Reload([]byte(yamlNoHost))
	if err == nil || !strings.Contains(err.Error(), "invalid endpoint URL") {
		t.Fatalf("expected invalid endpoint URL error, got %v", err)
	}

	// 2. Tasks with empty base_url in http config
	yamlTasksEmptyBaseURL := `
timezone: America/New_York
display:
  widgets:
    - id: t1
      type: tasks
      dimensions: [6, 2]
      config:
        source: http
        http:
          base_url: ""
`
	_, _, err = m.Reload([]byte(yamlTasksEmptyBaseURL))
	if err == nil || !strings.Contains(err.Error(), "requires non-empty 'http.base_url'") {
		t.Fatalf("expected empty base_url error, got %v", err)
	}
}
