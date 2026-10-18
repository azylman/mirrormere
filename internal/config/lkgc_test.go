package config_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"

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
		"calendar-family": {
			Type:   "calendar-family",
			Source: "builtin",
			Manifest: domain.WidgetManifest{
				Name:              "Family Calendar",
				Version:           "1.0.0",
				Provider:          "calendar-agenda",
				DefaultDimensions: domain.NewDimension(6, 2),
				SupportedDimensions: []domain.Dimension{
					domain.NewDimension(4, 2),
					domain.NewDimension(6, 2),
				},
				Refresh: domain.ManifestRefresh{IntervalSeconds: 300},
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

const familyBaseYAMLTemplate = `
timezone: America/New_York
display:
  grid:
    columns: 6
    rows: 2
  rotation:
    interval_seconds: 30
    transition: slide
  widgets:
    - id: family-cal
      type: calendar-family
      dimensions: [6, 2]
      config:
        shared_color: "#3D405B"
        calendars:
          - name: alex-personal
            url: "https://example.org/alex.ics"
          - name: school
            url: "https://example.org/school.ics"
        members:
%s
`

func buildFamilyYAML(membersBlock string) string {
	return strings.Replace(familyBaseYAMLTemplate, "%s", membersBlock, 1)
}

func buildFamilyWithHoursYAML(hours, membersBlock string) string {
	tmpl := `
timezone: America/New_York
display:
  grid:
    columns: 6
    rows: 2
  rotation:
    interval_seconds: 30
    transition: slide
  widgets:
    - id: family-cal
      type: calendar-family
      dimensions: [6, 2]
      config:
        hours: %s
        calendars:
          - name: alex-personal
            url: "https://example.org/alex.ics"
        members:
%s
`
	return fmt.Sprintf(tmpl, hours, membersBlock)
}

func makeNineMembers() string {
	var sb strings.Builder
	for i := 0; i < 9; i++ {
		fmt.Fprintf(&sb, "          - name: Member%c\n            color: \"#E07A5F\"\n            pattern: solid\n            calendars: [alex-personal]\n", 'A'+i)
	}
	return sb.String()
}

func withLKGCWidgets(widgetsYAML string) string {
	return fmt.Sprintf("timezone: America/New_York\ndisplay:\n  widgets:\n%s\n", widgetsYAML)
}

func withPkg(pkgType string, p *domain.Package) map[string]*domain.Package {
	pkgs := createStandardPackages()
	pkgs[pkgType] = p
	return pkgs
}

func mkShellCfg(tz string, hideCursor bool, remoteURL string, video *config.HeaderVideoConfig) *config.Config {
	return &config.Config{
		Timezone: tz,
		Display: config.DisplayConfig{
			HideCursor: hideCursor,
			RemoteURL:  remoteURL,
			Header: config.HeaderConfig{
				Video: video,
			},
		},
	}
}

func mkVideoCfg(enabled *bool, streamID, streamURL string) *config.HeaderVideoConfig {
	return &config.HeaderVideoConfig{
		Enabled:   enabled,
		StreamID:  streamID,
		StreamURL: streamURL,
	}
}

func TestLKGC_ManagerLifecycleAndStatus(t *testing.T) {
	t.Parallel()

	loader := &mockPackageLoader{packages: createStandardPackages()}

	// Cold boot failures
	if _, err := config.NewManager([]byte(validBaseYAML), nil, nil, nil); err == nil || !strings.Contains(err.Error(), "package loader cannot be nil") {
		t.Fatalf("expected nil loader error, got %v", err)
	}
	if _, err := config.NewManager([]byte("invalid: yaml: ["), loader, nil, nil); err == nil || !strings.Contains(err.Error(), "initial configuration validation failed") {
		t.Fatalf("expected initial validation error, got %v", err)
	}

	// Zero-value Manager accessors
	var emptyManager config.Manager
	if emptyManager.Current() != nil {
		t.Error("expected nil Current() on empty manager")
	}
	if emptyManager.CurrentLayout() != nil {
		t.Error("expected nil CurrentLayout() on empty manager")
	}
	if st := emptyManager.Status(); st.ConfigStatus != config.ConfigStatusOK {
		t.Errorf("expected default ok status, got %v", st.ConfigStatus)
	}

	// ValidatePipeline nil args
	if _, err := config.ValidatePipeline([]byte(validBaseYAML), nil, nil); err == nil || !strings.Contains(err.Error(), "package loader cannot be nil") {
		t.Fatalf("expected nil loader error, got %v", err)
	}
	if snap, err := config.ValidatePipeline([]byte(validBaseYAML), loader, nil); err != nil || snap == nil {
		t.Fatalf("unexpected error with nil getenv: %v", err)
	}

	// Cold boot + accessors
	m, err := config.NewManager([]byte(validBaseYAML), loader, nil, nil)
	if err != nil {
		t.Fatalf("unexpected cold boot error: %v", err)
	}
	cfg := m.Current()
	if cfg == nil || cfg.Timezone != "America/New_York" || len(cfg.Display.Widgets) != 3 {
		t.Fatalf("unexpected Current() config: %+v", cfg)
	}
	l := m.CurrentLayout()
	if l == nil || l.TotalScreens != 1 || len(l.Screens[0].Widgets) != 3 {
		t.Fatalf("unexpected CurrentLayout(): %+v", l)
	}
	snap := m.CurrentSnapshot()
	if snap == nil || snap.Config == nil || snap.Layout == nil || snap.Packages == nil || snap.LoadedAt.IsZero() {
		t.Fatalf("expected complete non-nil Snapshot, got %+v", snap)
	}
	st := m.Status()
	if st.ConfigStatus != config.ConfigStatusOK || st.ConfigError != nil || st.LastChecked.IsZero() || st.LastSuccess.IsZero() {
		t.Fatalf("unexpected initial Status(): %+v", st)
	}
	origSuccess := st.LastSuccess

	// Reload error retention and recovery telemetry (ZERO sleep)
	if _, _, err := m.Reload([]byte("syntax: [error")); err == nil {
		t.Fatal("expected reload error")
	}
	st2 := m.Status()
	if st2.ConfigStatus != config.ConfigStatusError || st2.ConfigError == nil {
		t.Fatalf("expected error status, got %+v", st2)
	}
	if st2.LastSuccess != origSuccess {
		t.Errorf("expected LastSuccess preserved on error, got %v != %v", st2.LastSuccess, origSuccess)
	}
	validYAML2 := strings.Replace(validBaseYAML, "America/New_York", "America/Chicago", 1)
	snap2, _, err := m.Reload([]byte(validYAML2))
	if err != nil {
		t.Fatalf("recovery reload failed: %v", err)
	}
	if snap2.Config.Timezone != "America/Chicago" {
		t.Errorf("expected timezone America/Chicago, got %s", snap2.Config.Timezone)
	}
	st3 := m.Status()
	if st3.ConfigStatus != config.ConfigStatusOK || st3.ConfigError != nil {
		t.Fatalf("expected recovered status ok, got %+v", st3)
	}
	if st3.LastSuccess.Before(origSuccess) {
		t.Errorf("expected !st3.LastSuccess.Before(origSuccess): %v vs %v", st3.LastSuccess, origSuccess)
	}

	// Package errors and explicit status lifecycle
	m.SetPackageError("pkg-a", nil)
	if m.Status().ConfigStatus != config.ConfigStatusOK {
		t.Errorf("expected OK after nil error, got %v", m.Status().ConfigStatus)
	}
	m.SetPackageError("pkg-a", fmt.Errorf("manifest.yaml missing"))
	st = m.Status()
	if st.ConfigStatus != config.ConfigStatusError || st.ConfigError == nil || !strings.Contains(*st.ConfigError, "manifest.yaml missing") {
		t.Errorf("expected pkg-a error, got %v", st)
	}
	m.SetPackageError("pkg-b", fmt.Errorf("views/widget.html missing"))
	if m.ClearPackageError("pkg-nonexistent") {
		t.Error("expected false for clearing non-existent package error")
	}
	if !m.ClearPackageError("pkg-a") {
		t.Error("expected true when clearing existing package error")
	}
	st = m.Status()
	if st.ConfigStatus != config.ConfigStatusError || st.ConfigError == nil || !strings.Contains(*st.ConfigError, "views/widget.html missing") {
		t.Errorf("expected pkg-b error to remain, got %v", st)
	}
	if !m.ClearPackageError("pkg-b") {
		t.Error("expected true when clearing pkg-b")
	}
	st = m.Status()
	if st.ConfigStatus != config.ConfigStatusOK || st.ConfigError != nil {
		t.Errorf("expected OK status after clearing all package errors, got %v", st)
	}

	m.SetErrorStatus(fmt.Errorf("file read error"))
	st = m.Status()
	if st.ConfigStatus != config.ConfigStatusError || st.ConfigError == nil || !strings.Contains(*st.ConfigError, "file read error") {
		t.Errorf("expected file read error, got %v", st)
	}
	m.SetPackageError("pkg-c", fmt.Errorf("pkg-c error"))
	m.ClearPackageError("pkg-c")
	st = m.Status()
	if st.ConfigStatus != config.ConfigStatusError || st.ConfigError == nil || !strings.Contains(*st.ConfigError, "file read error") {
		t.Errorf("expected file read error to persist after clearing pkg-c, got %v", st)
	}
	m.ClearErrorStatus()
	st = m.Status()
	if st.ConfigStatus != config.ConfigStatusOK || st.ConfigError != nil {
		t.Errorf("expected OK after ClearErrorStatus, got %v", st)
	}
	m.SetErrorStatus(nil)
	if m.Status().ConfigStatus != config.ConfigStatusOK {
		t.Errorf("expected OK after nil SetErrorStatus, got %v", m.Status().ConfigStatus)
	}

	// Reload clears stale package errors
	m.SetPackageError("orphan-widget", fmt.Errorf("views/widget.html missing"))
	if m.Status().ConfigStatus != config.ConfigStatusError {
		t.Fatalf("expected error status after SetPackageError, got %v", m.Status().ConfigStatus)
	}
	if _, _, err := m.Reload([]byte(validBaseYAML)); err != nil {
		t.Fatalf("Reload failed: %v", err)
	}
	st = m.Status()
	if st.ConfigStatus != config.ConfigStatusOK || st.ConfigError != nil {
		t.Errorf("expected OK status after successful config reload, got %v", st)
	}
}

func TestLKGC_ReloadValidationFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		yaml     string
		wantErr  string
		packages map[string]*domain.Package
	}{
		// Stage 1: Syntax & structure
		{
			name:    "Stage 1: syntax error",
			yaml:    "display: {bad: [yaml",
			wantErr: "YAML syntax error",
		},
		{
			name:    "Stage 1: empty configuration",
			yaml:    "",
			wantErr: "configuration file is empty",
		},
		{
			name:    "Stage 1: root not mapping",
			yaml:    "- item 1\n- item 2",
			wantErr: "configuration root must be a YAML mapping",
		},
		{
			name:    "Stage 1: decode error rotation scalar",
			yaml:    "timezone: UTC\ndisplay:\n  rotation: \"scalar-not-struct\"\n  widgets: []\n",
			wantErr: "failed to decode configuration",
		},
		{
			name:    "Stage 1: disallowed screens top-level key",
			yaml:    "timezone: UTC\nscreens: []\ndisplay:\n  widgets: []\n",
			wantErr: "disallowed top-level key 'screens'",
		},
		{
			name:    "Stage 1: disallowed providers top-level key",
			yaml:    "timezone: UTC\nproviders: []\ndisplay:\n  widgets: []\n",
			wantErr: "disallowed top-level key 'providers'",
		},
		{
			name:    "Stage 1: disallowed root header key",
			yaml:    "timezone: UTC\nheader: {}\ndisplay:\n  widgets: []\n",
			wantErr: "header must be configured under display.header",
		},
		{
			name:    "Stage 1: prohibited string interpolation",
			yaml:    "timezone: ${TZ}\ndisplay:\n  widgets: []\n",
			wantErr: "arbitrary '${...}' string interpolation is prohibited",
		},
		{
			name:    "Stage 1: unknown top-level key",
			yaml:    "timezone: UTC\nbogus_key: 123\ndisplay:\n  widgets: []\n",
			wantErr: "unknown or disallowed top-level key 'bogus_key'",
		},

		// Stage 2: Core instance schema
		{
			name:    "Stage 2: invalid timezone",
			yaml:    "timezone: Invalid/Zone_Name\ndisplay:\n  widgets: []\n",
			wantErr: "unknown time zone",
		},
		{
			name:    "Stage 2: invalid grid dimensions",
			yaml:    "timezone: UTC\ndisplay:\n  grid:\n    columns: 5\n    rows: 2\n  widgets: []\n",
			wantErr: "grid columns must be 6",
		},
		{
			name:    "Stage 2: duplicate widget ID",
			yaml:    withLKGCWidgets("    - id: duplicate-id\n      type: spacer\n      dimensions: [6, 1]\n    - id: duplicate-id\n      type: spacer\n      dimensions: [6, 1]"),
			wantErr: "duplicate widget id 'duplicate-id'",
		},
		{
			name:    "Stage 2: empty widget ID",
			yaml:    withLKGCWidgets("    - id: \"\"\n      type: spacer\n      dimensions: [6, 2]"),
			wantErr: "missing required field 'id'",
		},
		{
			name:    "Stage 2: empty widget type",
			yaml:    withLKGCWidgets("    - id: w1\n      type: \"\"\n      dimensions: [6, 2]"),
			wantErr: "missing required field 'type'",
		},
		{
			name:    "Stage 2: dimensions out of bounds",
			yaml:    withLKGCWidgets("    - id: w1\n      type: spacer\n      dimensions: [7, 1]"),
			wantErr: "invalid dimensions [7, 1]: cols must be 1..6, rows must be 1..2",
		},
		{
			name:    "Stage 2: non-positive refresh interval",
			yaml:    withLKGCWidgets("    - id: w1\n      type: spacer\n      dimensions: [6, 2]\n      refresh_interval_seconds: 0"),
			wantErr: "refresh_interval_seconds must be positive",
		},
		{
			name:    "Stage 2: invalid HTTP method",
			yaml:    withLKGCWidgets("    - id: w1\n      type: spacer\n      dimensions: [6, 2]\n      method: \"PATCH\""),
			wantErr: "invalid http method 'PATCH'",
		},
		{
			name:    "Stage 2: invalid endpoint URL",
			yaml:    withLKGCWidgets("    - id: w1\n      type: spacer\n      dimensions: [6, 2]\n      endpoint: \"ftp://example.com/data\""),
			wantErr: "invalid endpoint URL 'ftp://example.com/data'",
		},
		{
			name:    "Stage 2: unset token_env",
			yaml:    withLKGCWidgets("    - id: w1\n      type: spacer\n      dimensions: [6, 2]\n      token_env: UNSET_TOKEN_VAR_xyz"),
			wantErr: "defined in token_env is unset or empty",
		},
		{
			name:    "Stage 2: conflicting feed and feed_env",
			yaml:    withLKGCWidgets("    - id: w1\n      type: spacer\n      dimensions: [6, 2]\n      config:\n        feed: \"http://public\"\n        feed_env: FEED_SECRET_xyz"),
			wantErr: "cannot specify both 'feed' and 'feed_env'",
		},

		// Stage 3: Package existence & completeness
		{
			name:    "Stage 3: nonexistent package",
			yaml:    withLKGCWidgets("    - id: w-unknown\n      type: non-existent-widget\n      dimensions: [6, 2]"),
			wantErr: "widget package 'non-existent-widget' not found",
		},
		{
			name:     "Stage 3: invalid package manifest",
			packages: withPkg("invalid-manifest", &domain.Package{Type: "invalid-manifest", Manifest: domain.WidgetManifest{}}),
			yaml:     withLKGCWidgets("    - id: w-bad\n      type: invalid-manifest\n      dimensions: [6, 2]"),
			wantErr:  "manifest invalid",
		},
		{
			name:     "Stage 3: omitted dimensions without manifest default",
			packages: withPkg("no-defaults", &domain.Package{Type: "no-defaults", Manifest: domain.WidgetManifest{Name: "No Defaults", Version: "1.0.0", Provider: "tasks"}}),
			yaml:     withLKGCWidgets("    - id: w-nodim\n      type: no-defaults\n      config:\n        source: local"),
			wantErr:  "has no valid default_dimensions",
		},
		{
			name:     "Stage 3: unsupported dimensions [3, 2]",
			packages: withPkg("restricted-dims", &domain.Package{Type: "restricted-dims", Manifest: domain.WidgetManifest{Name: "Restricted Dims", Version: "1.0.0", Provider: "spacer", DefaultDimensions: domain.NewDimension(2, 1), SupportedDimensions: []domain.Dimension{domain.NewDimension(2, 1)}}}),
			yaml:     withLKGCWidgets("    - id: w-res\n      type: restricted-dims\n      dimensions: [3, 2]"),
			wantErr:  "not supported by package 'restricted-dims'",
		},

		// Stage 4: Manifest config schema
		{
			name:    "Stage 4: missing required property calendars",
			yaml:    withLKGCWidgets("    - id: cal-missing\n      type: calendar-agenda\n      dimensions: [4, 2]\n      config:\n        view: month\n    - id: filler\n      type: spacer\n      dimensions: [2, 2]"),
			wantErr: "manifest config_schema validation error for widget 'cal-missing'",
		},
		{
			name:    "Stage 4: calendars type mismatch",
			yaml:    withLKGCWidgets("    - id: cal-mismatch\n      type: calendar-agenda\n      dimensions: [4, 2]\n      config:\n        calendars: not-an-array\n    - id: filler\n      type: spacer\n      dimensions: [2, 2]"),
			wantErr: "manifest config_schema validation error for widget 'cal-mismatch'",
		},
		{
			name:    "Stage 4: nil config when schema requires properties",
			yaml:    withLKGCWidgets("    - id: cal-nil\n      type: calendar-agenda\n      dimensions: [4, 2]\n    - id: filler\n      type: spacer\n      dimensions: [2, 2]"),
			wantErr: "manifest config_schema validation error for widget 'cal-nil'",
		},

		// Stage 5: Domain & source of truth rules (Tasks, HTTP & Family Calendar)
		{
			name:    "Stage 5: tasks no primary source",
			yaml:    withLKGCWidgets("    - id: my-todo\n      type: tasks\n      dimensions: [6, 2]\n      config:\n        list_id: orphaned-tasks"),
			wantErr: "references list_id 'orphaned-tasks' with no primary source definition",
		},
		{
			name:    "Stage 5: tasks invalid source",
			yaml:    withLKGCWidgets("    - id: my-todo\n      type: tasks\n      dimensions: [6, 2]\n      config:\n        list_id: test\n        source: dropbox"),
			wantErr: "declares invalid source 'dropbox'",
		},
		{
			name:    "Stage 5: tasks http missing http block",
			yaml:    withLKGCWidgets("    - id: my-todo\n      type: tasks\n      dimensions: [6, 2]\n      config:\n        list_id: test\n        source: http"),
			wantErr: "requires 'http' config block",
		},
		{
			name:    "Stage 5: tasks http invalid base_url",
			yaml:    withLKGCWidgets("    - id: my-todo\n      type: tasks\n      dimensions: [6, 2]\n      config:\n        list_id: test\n        source: http\n        http:\n          base_url: ftp://example.com"),
			wantErr: "invalid http.base_url",
		},
		{
			name:    "Stage 5: tasks http empty base_url",
			yaml:    withLKGCWidgets("    - id: t1\n      type: tasks\n      dimensions: [6, 2]\n      config:\n        source: http\n        http:\n          base_url: \"\""),
			wantErr: "requires non-empty 'http.base_url'",
		},
		{
			name:    "Stage 5: tasks gtasks missing tasklist_id",
			yaml:    withLKGCWidgets("    - id: my-todo\n      type: tasks\n      dimensions: [6, 2]\n      config:\n        list_id: test\n        source: gtasks\n        gtasks:\n          tasklist_id: \"\""),
			wantErr: "requires non-empty 'gtasks.tasklist_id'",
		},
		{
			name:    "Stage 5: tasks todoist missing todoist block",
			yaml:    withLKGCWidgets("    - id: my-todo\n      type: tasks\n      dimensions: [6, 2]\n      config:\n        list_id: test\n        source: todoist"),
			wantErr: "requires 'todoist' config block",
		},
		{
			name:    "Stage 5: tasks todoist invalid base_url",
			yaml:    withLKGCWidgets("    - id: my-todo\n      type: tasks\n      dimensions: [6, 2]\n      config:\n        list_id: test\n        source: todoist\n        todoist:\n          base_url: ftp://example.com"),
			wantErr: "invalid todoist.base_url",
		},
		{
			name:    "Stage 5: conflicting primary sources type mismatch",
			yaml:    withLKGCWidgets("    - id: tasks-primary-1\n      type: tasks\n      dimensions: [3, 2]\n      config:\n        list_id: shared-chores\n        source: local\n    - id: tasks-primary-2\n      type: tasks\n      dimensions: [3, 2]\n      config:\n        list_id: shared-chores\n        source: gtasks\n        gtasks:\n          tasklist_id: list-123"),
			wantErr: "conflicting source definitions for list_id 'shared-chores'",
		},
		{
			name:    "Stage 5: conflicting gtasks tasklist_id",
			yaml:    withLKGCWidgets("    - id: tasks-primary-1\n      type: tasks\n      dimensions: [3, 2]\n      config:\n        list_id: shared-chores\n        source: gtasks\n        gtasks:\n          tasklist_id: list-123\n    - id: tasks-primary-2\n      type: tasks\n      dimensions: [3, 2]\n      config:\n        list_id: shared-chores\n        source: gtasks\n        gtasks:\n          tasklist_id: list-999"),
			wantErr: "conflicting source definitions for list_id 'shared-chores'",
		},
		{
			name:    "Stage 5: conflicting http base_url",
			yaml:    withLKGCWidgets("    - id: tasks-primary-1\n      type: tasks\n      dimensions: [3, 2]\n      config:\n        list_id: shared-chores\n        source: http\n        http:\n          base_url: https://api1.example.com/lists\n    - id: tasks-primary-2\n      type: tasks\n      dimensions: [3, 2]\n      config:\n        list_id: shared-chores\n        source: http\n        http:\n          base_url: https://api2.example.com/lists"),
			wantErr: "conflicting source definitions for list_id 'shared-chores'",
		},
		{
			name:    "Stage 5: sensor-http missing endpoint",
			yaml:    withLKGCWidgets("    - id: sensor-1\n      type: sensor-http\n      dimensions: [6, 2]"),
			wantErr: "provider 'http' requires non-empty 'endpoint' URL",
		},
		{
			name:    "Stage 5: sensor-http invalid endpoint",
			yaml:    withLKGCWidgets("    - id: sensor-1\n      type: sensor-http\n      dimensions: [6, 2]\n      endpoint: notaurl"),
			wantErr: "invalid endpoint URL 'notaurl'",
		},
		{
			name:    "Stage 5: sensor-http hostless endpoint",
			yaml:    withLKGCWidgets("    - id: sensor-1\n      type: sensor-http\n      dimensions: [6, 2]\n      endpoint: \"http://\""),
			wantErr: "invalid endpoint URL",
		},
		{
			name:    "Stage 5: family unknown calendar reference",
			yaml:    buildFamilyYAML("          - name: Alex\n            pattern: solid\n            calendars: [typo-personal]"),
			wantErr: "unknown calendar",
		},
		{
			name:    "Stage 5: family duplicate member name",
			yaml:    buildFamilyYAML("          - name: Alex\n            pattern: solid\n            calendars: [alex-personal]\n          - name: Alex\n            pattern: hatch\n            calendars: [school]"),
			wantErr: "duplicate member name",
		},
		{
			name:    "Stage 5: family more than 8 members",
			yaml:    buildFamilyYAML(makeNineMembers()),
			wantErr: "at most 8 members",
		},
		{
			name:    "Stage 5: family duplicate pattern",
			yaml:    buildFamilyYAML("          - name: Alex\n            pattern: solid\n            calendars: [alex-personal]\n          - name: Kid\n            pattern: solid\n            calendars: [school]"),
			wantErr: "must be distinct",
		},
		{
			name:    "Stage 5: family omitted patterns colliding",
			yaml:    buildFamilyYAML("          - name: Alex\n            color: \"#E07A5F\"\n            calendars: [alex-personal]\n          - name: Kid\n            color: \"#81B29A\"\n            calendars: [school]"),
			wantErr: "must be distinct",
		},
		{
			name:    "Stage 5: family omitted colliding with explicit solid",
			yaml:    buildFamilyYAML("          - name: Alex\n            calendars: [alex-personal]\n          - name: Kid\n            pattern: solid\n            calendars: [school]"),
			wantErr: "must be distinct",
		},
		{
			name:    "Stage 5: family bad hours order",
			yaml:    buildFamilyWithHoursYAML("[\"21:00\", \"07:00\"]", "          - name: Alex\n            color: \"#E07A5F\"\n            pattern: solid\n            calendars: [alex-personal]"),
			wantErr: "must be before end",
		},
		{
			name:    "Stage 5: family empty calendar reference",
			yaml:    buildFamilyYAML("          - name: Alex\n            pattern: solid\n            calendars: [\"\"]"),
			wantErr: "empty calendar reference",
		},
		{
			name:    "Stage 5: family missing member name",
			yaml:    buildFamilyYAML("          - color: \"#E07A5F\"\n            pattern: solid\n            calendars: [alex-personal]"),
			wantErr: "requires non-empty 'name'",
		},

		// Stage 6: Layout solver
		{
			name:    "Stage 6: layout cell deficit",
			yaml:    withLKGCWidgets("    - id: cal-1\n      type: calendar-agenda\n      dimensions: [4, 2]\n      config:\n        calendars: []\n    - id: weather-1\n      type: weather-forecast\n      dimensions: [2, 1]"),
			wantErr: "deficit",
		},
		{
			name:    "Stage 6: layout geometric conflict",
			yaml:    withLKGCWidgets("    - id: hero-5x2\n      type: spacer\n      dimensions: [5, 2]\n    - id: side-2x1\n      type: spacer\n      dimensions: [2, 1]"),
			wantErr: "layout solver error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pkgs := tc.packages
			if pkgs == nil {
				pkgs = createStandardPackages()
			}
			loader := &mockPackageLoader{packages: pkgs}
			m, err := config.NewManager([]byte(validBaseYAML), loader, nil, nil)
			if err != nil {
				t.Fatalf("setup failed: %v", err)
			}
			_, _, err = m.Reload([]byte(tc.yaml))
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.wantErr)) {
				t.Fatalf("m.Reload() error = %v, want substring %q", err, tc.wantErr)
			}
			// Verify LKGC retention
			if m.Current().Timezone != "America/New_York" {
				t.Errorf("LKGC violated: running timezone changed to %s", m.Current().Timezone)
			}
			st := m.Status()
			if st.ConfigStatus != config.ConfigStatusError {
				t.Errorf("expected status 'error', got %s", st.ConfigStatus)
			}
			if st.ConfigError == nil || !strings.Contains(strings.ToLower(*st.ConfigError), strings.ToLower(tc.wantErr)) {
				t.Errorf("expected ConfigError containing %q, got %v", tc.wantErr, st.ConfigError)
			}
		})
	}
}

func TestLKGC_ReloadValidDomainConfigs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		yaml string
	}{
		{
			name: "valid todoist source",
			yaml: withLKGCWidgets("    - id: my-todo\n      type: tasks\n      dimensions: [6, 2]\n      config:\n        list_id: test\n        source: todoist\n        todoist:\n          project_id: proj-123"),
		},
		{
			name: "matching local source with different show_completed",
			yaml: withLKGCWidgets("    - id: tasks-primary-1\n      type: tasks\n      dimensions: [3, 2]\n      config:\n        list_id: shared-chores\n        source: local\n        show_completed: 3\n    - id: tasks-primary-2\n      type: tasks\n      dimensions: [3, 2]\n      config:\n        list_id: shared-chores\n        source: local\n        show_completed: 0"),
		},
		{
			name: "matching gtasks source with different show_completed",
			yaml: withLKGCWidgets("    - id: tasks-primary-1\n      type: tasks\n      dimensions: [3, 2]\n      config:\n        list_id: shared-chores\n        source: gtasks\n        gtasks:\n          tasklist_id: list-123\n        show_completed: 5\n    - id: tasks-primary-2\n      type: tasks\n      dimensions: [3, 2]\n      config:\n        list_id: shared-chores\n        source: gtasks\n        gtasks:\n          tasklist_id: list-123\n        show_completed: 1"),
		},
		{
			name: "valid secondary tasks consumer without source",
			yaml: withLKGCWidgets("    - id: primary-tasks\n      type: tasks\n      dimensions: [3, 2]\n      config:\n        list_id: chores\n        source: local\n    - id: secondary-tasks\n      type: tasks\n      dimensions: [3, 2]\n      config:\n        list_id: chores"),
		},
		{
			name: "valid calendar-family config",
			yaml: buildFamilyYAML("          - name: Alex\n            color: \"#E07A5F\"\n            pattern: solid\n            calendars: [alex-personal]\n          - name: Kid\n            color: \"#81B29A\"\n            pattern: hatch\n            calendars: [school]"),
		},
		{
			name: "calendar-family with non-string hours elements skipped",
			yaml: buildFamilyWithHoursYAML("[7, 21]", "          - name: Alex\n            color: \"#E07A5F\"\n            pattern: solid\n            calendars: [alex-personal]"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			loader := &mockPackageLoader{packages: createStandardPackages()}
			m, err := config.NewManager([]byte(validBaseYAML), loader, nil, nil)
			if err != nil {
				t.Fatalf("setup failed: %v", err)
			}
			snap, diff, err := m.Reload([]byte(tc.yaml))
			if err != nil {
				t.Fatalf("m.Reload() unexpected error: %v", err)
			}
			if snap == nil || diff == nil {
				t.Fatal("expected non-nil snap and diff")
			}
		})
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
				{ID: "resized-only", Type: "spacer", Dimensions: []int{2, 1}, Pinned: false},
				{ID: "pinned-only", Type: "spacer", Dimensions: []int{2, 1}, Pinned: false},
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
				{ID: "resized-only", Type: "spacer", Dimensions: []int{4, 1}, Pinned: false}, // Layout change: dimensions
				{ID: "pinned-only", Type: "spacer", Dimensions: []int{2, 1}, Pinned: true},   // Layout change: pinned
			},
		},
	}

	diff := config.DiffConfigs(oldCfg, newCfg)
	if len(diff.Unchanged) != 3 {
		t.Fatalf("expected 3 unchanged widgets, got %d: %v", len(diff.Unchanged), diff.Unchanged)
	}
	unchangedMap := make(map[string]bool)
	for _, u := range diff.Unchanged {
		unchangedMap[u.ID] = true
	}
	for _, expectedID := range []string{"unchanged-1", "resized-only", "pinned-only"} {
		if !unchangedMap[expectedID] {
			t.Errorf("expected widget '%s' in diff.Unchanged", expectedID)
		}
	}

	if len(diff.LayoutModified) != 2 {
		t.Fatalf("expected 2 layout-modified widgets, got %d: %v", len(diff.LayoutModified), diff.LayoutModified)
	}
	layoutMap := make(map[string]bool)
	for _, lm := range diff.LayoutModified {
		layoutMap[lm.ID] = true
	}
	for _, expectedID := range []string{"resized-only", "pinned-only"} {
		if !layoutMap[expectedID] {
			t.Errorf("expected widget '%s' in diff.LayoutModified", expectedID)
		}
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
		case "resized-only", "pinned-only":
			t.Errorf("layout-only edit '%s' must not be classified as Modified", m.ID)
		}
	}

	if diff.TimezoneChanged {
		t.Errorf("expected TimezoneChanged to be false when both timezones are empty or identical")
	}

	tzOld := &config.Config{Timezone: "America/Los_Angeles"}
	tzNew := &config.Config{Timezone: "America/New_York"}
	tzDiff := config.DiffConfigs(tzOld, tzNew)
	if !tzDiff.TimezoneChanged {
		t.Errorf("expected TimezoneChanged to be true when timezone changed from LA to NY")
	}

	// Shell changed table
	trueVal := true
	falseVal := false
	shellTests := []struct {
		name         string
		oldCfg       *config.Config
		newCfg       *config.Config
		wantShellMod bool
	}{
		{name: "nil old config produces false", oldCfg: nil, newCfg: mkShellCfg("", false, "", nil), wantShellMod: false},
		{name: "nil new config produces false", oldCfg: mkShellCfg("", false, "", nil), newCfg: nil, wantShellMod: false},
		{name: "identical empty configs produce false", oldCfg: mkShellCfg("", false, "", nil), newCfg: mkShellCfg("", false, "", nil), wantShellMod: false},
		{name: "widget change only produces false", oldCfg: &config.Config{Display: config.DisplayConfig{Widgets: []config.WidgetConfig{{ID: "w1", Type: "clock"}}}}, newCfg: &config.Config{Display: config.DisplayConfig{Widgets: []config.WidgetConfig{{ID: "w1", Type: "clock"}, {ID: "w2", Type: "weather"}}}}, wantShellMod: false},
		{name: "timezone change only produces false", oldCfg: mkShellCfg("America/Los_Angeles", false, "", nil), newCfg: mkShellCfg("America/New_York", false, "", nil), wantShellMod: false},
		{name: "header video enabled added produces true", oldCfg: mkShellCfg("", false, "", nil), newCfg: mkShellCfg("", false, "", mkVideoCfg(&trueVal, "chromecast", "http://localhost:1984/stream")), wantShellMod: true},
		{name: "header video enabled flipped produces true", oldCfg: mkShellCfg("", false, "", mkVideoCfg(&trueVal, "", "")), newCfg: mkShellCfg("", false, "", mkVideoCfg(&falseVal, "", "")), wantShellMod: true},
		{name: "header video stream URL changed produces true", oldCfg: mkShellCfg("", false, "", mkVideoCfg(&trueVal, "", "http://localhost:1984/old")), newCfg: mkShellCfg("", false, "", mkVideoCfg(&trueVal, "", "http://localhost:1984/new")), wantShellMod: true},
		{name: "hide cursor changed produces true", oldCfg: mkShellCfg("", false, "", nil), newCfg: mkShellCfg("", true, "", nil), wantShellMod: true},
		{name: "remote URL changed produces true", oldCfg: mkShellCfg("", false, "", nil), newCfg: mkShellCfg("", false, "http://192.168.1.50:8080", nil), wantShellMod: true},
	}
	for _, tc := range shellTests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := config.DiffConfigs(tc.oldCfg, tc.newCfg)
			if d.ShellChanged != tc.wantShellMod {
				t.Errorf("DiffConfigs() ShellChanged = %v, want %v", d.ShellChanged, tc.wantShellMod)
			}
		})
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
