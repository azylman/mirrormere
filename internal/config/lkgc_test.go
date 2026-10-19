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
	if pkg, ok := m.packages[widgetType]; ok {
		return pkg, nil
	}
	return nil, fmt.Errorf("widget package '%s' not found", widgetType)
}

func mkTestPkg(t, src, prov string, c, r, refresh int, schema, respSchema map[string]any, supported ...domain.Dimension) *domain.Package {
	return &domain.Package{
		Type: t, Source: src,
		Manifest: domain.WidgetManifest{
			Name: t, Version: "1.0.0", Provider: prov, DefaultDimensions: domain.NewDimension(c, r),
			Refresh: domain.ManifestRefresh{IntervalSeconds: refresh}, ConfigSchema: schema,
			ResponseSchema: respSchema, SupportedDimensions: supported,
		},
	}
}

func createStandardPackages() map[string]*domain.Package {
	cal := map[string]any{"type": "object", "required": []string{"calendars"}, "properties": map[string]any{"calendars": map[string]any{"type": "array"}}}
	tasks := map[string]any{"type": "object", "properties": map[string]any{"list_id": map[string]any{"type": "string"}, "source": map[string]any{"type": "string"}}}
	return map[string]*domain.Package{
		"calendar-feed":    mkTestPkg("calendar-feed", "builtin", "calendar-feed", 4, 2, 300, cal, nil),
		"calendar-grid":    mkTestPkg("calendar-grid", "builtin", "calendar-feed", 6, 2, 300, nil, nil, domain.NewDimension(4, 2), domain.NewDimension(6, 2)),
		"weather-forecast": mkTestPkg("weather-forecast", "builtin", "weather-forecast", 2, 1, 600, nil, nil),
		"tasks":            mkTestPkg("tasks", "builtin", "tasks", 2, 1, 120, tasks, nil),
		"spacer":           mkTestPkg("spacer", "builtin", "spacer", 2, 1, 0, nil, nil),
		"sensor-http":      mkTestPkg("sensor-http", "custom", "http", 2, 1, 0, nil, map[string]any{"type": "object"}),
	}
}

const validBaseYAML = "timezone: America/New_York\ndisplay:\n  grid:\n    columns: 6\n    rows: 2\n  rotation:\n    interval_seconds: 30\n    transition: slide\n  widgets:\n    - id: cal-hero\n      type: calendar-feed\n      dimensions: [4, 2]\n      pinned: true\n      config: {calendars: [{name: Family}]}\n    - id: weather-1\n      type: weather-forecast\n    - id: tasks-1\n      type: tasks\n      dimensions: [2, 1]\n      config: {list_id: chores, source: gtasks, gtasks: {tasklist_id: list-1}}\n"

func buildFamilyYAML(membersBlock string) string { return buildFamilyWithHoursYAML("", membersBlock) }

func buildFamilyWithHoursYAML(hours, membersBlock string) string {
	h := ""
	if hours != "" {
		h = "        hours: " + hours + "\n"
	}
	return "timezone: America/New_York\ndisplay:\n  widgets:\n    - id: family-cal\n      type: calendar-grid\n      dimensions: [6, 2]\n      config:\n" + h + "        shared_color: \"#3D405B\"\n        calendars:\n          - {name: alex-personal, url: \"https://example.org/alex.ics\"}\n          - {name: school, url: \"https://example.org/school.ics\"}\n        members:\n" + membersBlock + "\n"
}

func mkMemberYAML(name, color, pattern, cals string) string {
	if name == "" && color == "" && pattern == "" && cals == "" {
		return "          - {}\n"
	}
	res := "          - name: " + name + "\n"
	for _, kv := range [][2]string{{color, `color: "` + color + `"`}, {pattern, "pattern: " + pattern}, {cals, "calendars: " + cals}} {
		if kv[0] != "" {
			res += "            " + kv[1] + "\n"
		}
	}
	return res
}

func makeNineMembers() string {
	res := ""
	for i := range 9 {
		res += mkMemberYAML(fmt.Sprintf("Member%c", 'A'+i), "#E07A5F", "solid", "[alex-personal]")
	}
	return res
}

func mkTaskYAML(id, dims, listID, source, extra string) string {
	y := fmt.Sprintf("    - id: %s\n      type: tasks\n      dimensions: %s\n", id, dims)
	if listID == "" && source == "" && extra == "" {
		return y
	}
	y += "      config:\n"
	if listID != "" {
		y += "        list_id: " + listID + "\n"
	}
	if source != "" {
		y += "        source: " + source + "\n"
	}
	if extra != "" {
		y += strings.TrimRight(extra, "\n") + "\n"
	}
	return y
}

func mkWYAML(id, wType, dims, extra string) string {
	return fmt.Sprintf("    - id: %s\n      type: %s\n      dimensions: %s\n%s", id, wType, dims, extra)
}

func withLKGCWidgets(y string) string {
	return "timezone: America/New_York\ndisplay:\n  widgets:\n" + y + "\n"
}

func withPkg(pkgType string, p *domain.Package) map[string]*domain.Package {
	pkgs := createStandardPackages()
	pkgs[pkgType] = p
	return pkgs
}

func mkShellCfg(tz string, hideCursor bool, remoteURL string, video *config.HeaderVideoConfig) *config.Config {
	return &config.Config{Timezone: tz, Display: config.DisplayConfig{HideCursor: hideCursor, RemoteURL: remoteURL, Header: config.HeaderConfig{Video: video}}}
}

func mkVideoCfg(enabled *bool, streamID, streamURL string) *config.HeaderVideoConfig {
	return &config.HeaderVideoConfig{Enabled: enabled, StreamID: streamID, StreamURL: streamURL}
}

func mkW(id, wType string, c, r int, pinned bool, endpoint string, refresh *int, cfg map[string]any) config.WidgetConfig {
	return config.WidgetConfig{ID: id, Type: wType, Dimensions: []int{c, r}, Pinned: pinned, Endpoint: endpoint, RefreshIntervalSeconds: refresh, Config: cfg}
}

func mkCfg(ws ...config.WidgetConfig) *config.Config {
	return &config.Config{Display: config.DisplayConfig{Widgets: ws}}
}

func TestLKGC_ManagerLifecycleAndStatus(t *testing.T) {
	t.Parallel()
	loader := &mockPackageLoader{packages: createStandardPackages()}

	if _, err := config.NewManager([]byte(validBaseYAML), nil, nil, nil); err == nil || !strings.Contains(err.Error(), "package loader cannot be nil") {
		t.Fatalf("expected nil loader error, got %v", err)
	}
	if _, err := config.NewManager([]byte("invalid: yaml: ["), loader, nil, nil); err == nil || !strings.Contains(err.Error(), "initial configuration validation failed") {
		t.Fatalf("expected initial validation error, got %v", err)
	}
	var emptyManager config.Manager
	if emptyManager.Current() != nil || emptyManager.CurrentSnapshot() != nil || emptyManager.Status().ConfigStatus != config.ConfigStatusOK {
		t.Errorf("expected zero-value manager defaults")
	}
	if _, err := config.ValidatePipeline([]byte(validBaseYAML), nil, nil); err == nil || !strings.Contains(err.Error(), "package loader cannot be nil") {
		t.Fatalf("expected nil loader error, got %v", err)
	}
	if snap, err := config.ValidatePipeline([]byte(validBaseYAML), loader, nil); err != nil || snap == nil {
		t.Fatalf("unexpected error with nil getenv: %v", err)
	}

	m, err := config.NewManager([]byte(validBaseYAML), loader, nil, nil)
	if err != nil || m.Current() == nil || m.CurrentSnapshot() == nil {
		t.Fatalf("cold boot failed: %v", err)
	}

	checkStatus := func(want config.ConfigStatus, wantSub string) {
		t.Helper()
		st := m.Status()
		if st.ConfigStatus != want || (wantSub != "" && (st.ConfigError == nil || !strings.Contains(*st.ConfigError, wantSub))) || (want == config.ConfigStatusOK && st.ConfigError != nil) {
			t.Fatalf("checkStatus failed: want (%v, sub %q), got (%v, err %v)", want, wantSub, st.ConfigStatus, st.ConfigError)
		}
	}

	checkStatus(config.ConfigStatusOK, "")
	origSuccess := m.Status().LastSuccess

	if _, _, err := m.Reload([]byte("syntax: [error")); err == nil {
		t.Fatal("expected reload error")
	}
	checkStatus(config.ConfigStatusError, "")
	if st := m.Status(); st.LastSuccess != origSuccess {
		t.Errorf("expected LastSuccess preserved on error, got %v != %v", st.LastSuccess, origSuccess)
	}

	validYAML2 := strings.Replace(validBaseYAML, "America/New_York", "America/Chicago", 1)
	if snap2, _, err := m.Reload([]byte(validYAML2)); err != nil || snap2.Config.Timezone != "America/Chicago" {
		t.Fatalf("recovery reload failed: %v", err)
	}
	checkStatus(config.ConfigStatusOK, "")
	if m.Status().LastSuccess.Before(origSuccess) {
		t.Errorf("expected !LastSuccess.Before(origSuccess)")
	}

	m.SetPackageError("pkg-a", nil)
	checkStatus(config.ConfigStatusOK, "")
	m.SetPackageError("pkg-a", fmt.Errorf("manifest.yaml missing"))
	checkStatus(config.ConfigStatusError, "manifest.yaml missing")
	m.SetPackageError("pkg-b", fmt.Errorf("views/widget.html missing"))
	if m.ClearPackageError("pkg-nonexistent") || !m.ClearPackageError("pkg-a") {
		t.Error("unexpected ClearPackageError results")
	}
	checkStatus(config.ConfigStatusError, "views/widget.html missing")
	if !m.ClearPackageError("pkg-b") {
		t.Error("expected true when clearing pkg-b")
	}
	checkStatus(config.ConfigStatusOK, "")

	m.SetErrorStatus(fmt.Errorf("file read error"))
	checkStatus(config.ConfigStatusError, "file read error")
	m.SetPackageError("pkg-c", fmt.Errorf("pkg-c error"))
	m.ClearPackageError("pkg-c")
	checkStatus(config.ConfigStatusError, "file read error")
	m.SetErrorStatus(nil)
	checkStatus(config.ConfigStatusError, "file read error")

	m.SetPackageError("orphan-widget", fmt.Errorf("views/widget.html missing"))
	checkStatus(config.ConfigStatusError, "")
	if _, _, err := m.Reload([]byte(validBaseYAML)); err != nil {
		t.Fatalf("Reload failed: %v", err)
	}
	checkStatus(config.ConfigStatusOK, "")
}

func TestLKGC_ReloadValidationFailures(t *testing.T) {
	t.Parallel()
	loader := &mockPackageLoader{packages: createStandardPackages()}

	tests := []struct {
		name       string
		yamlData   string
		wantErrSub string
		pkgs       map[string]*domain.Package
	}{
		{"Stage 1: syntax error", "display:\n  widgets: [", "syntax error", nil},
		{"Stage 1: root not mapping", "- item1\n- item2\n", "configuration root must be a YAML mapping", nil},
		{"Stage 1: empty configuration", "   \n\t  \n", "configuration file is empty", nil},
		{"Stage 1: prohibited string interpolation", withLKGCWidgets(mkWYAML("w1", "spacer", "[2, 1]", "      endpoint: \"${TZ}\"\n")), "arbitrary '${...}' string interpolation is prohibited", nil},
		{"Stage 1: disallowed root header key", "header:\n  elements: [clock]\n" + withLKGCWidgets(mkWYAML("w1", "spacer", "[2, 1]", "")), "configuration contains disallowed top-level key 'header'", nil},
		{"Stage 1: disallowed providers top-level key", "providers:\n  my-provider:\n    url: http://example.org\n" + withLKGCWidgets(mkWYAML("w1", "spacer", "[2, 1]", "")), "configuration contains disallowed top-level key 'providers'", nil},
		{"Stage 1: disallowed screens top-level key", "screens:\n  - widgets: [w1]\n" + withLKGCWidgets(mkWYAML("w1", "spacer", "[2, 1]", "")), "configuration contains disallowed top-level key 'screens'", nil},
		{"Stage 2: widget missing ID", withLKGCWidgets("    - type: spacer\n      dimensions: [2, 1]\n"), "missing required field 'id'", nil},
		{"Stage 2: duplicate widget ID", withLKGCWidgets(mkWYAML("dupe", "spacer", "[2, 1]", "") + mkWYAML("dupe", "spacer", "[2, 1]", "")), "duplicate widget id 'dupe'", nil},
		{"Stage 2: invalid endpoint URL", withLKGCWidgets(mkWYAML("w1", "sensor-http", "[2, 1]", "      endpoint: \"://not-a-valid-url\"\n")), "invalid endpoint URL", nil},
		{"Stage 2: unset token_env", withLKGCWidgets(mkWYAML("w1", "sensor-http", "[2, 1]", "      endpoint: http://example.org\n      token_env: UNSET_MIRRORMERE_TEST_SECRET_ENV_VAR\n")), "defined in token_env is unset or empty", nil},
		{"Stage 2: conflicting feed and feed_env", withLKGCWidgets(mkWYAML("w1", "sensor-http", "[2, 1]", "      config:\n        feed: \"http://public\"\n        feed_env: FEED_SECRET_xyz\n")), "cannot specify both 'feed' and 'feed_env'", nil},
		{"Stage 3: nonexistent package", withLKGCWidgets(mkWYAML("w1", "nonexistent-type", "[2, 1]", "")), "widget package 'nonexistent-type' error", nil},
		{"Stage 3: invalid package manifest", withLKGCWidgets(mkWYAML("w1", "bad-manifest-pkg", "[2, 1]", "")), "widget package 'bad-manifest-pkg' manifest invalid", withPkg("bad-manifest-pkg", &domain.Package{Type: "bad-manifest-pkg", Manifest: domain.WidgetManifest{Name: ""}})},
		{"Stage 3: omitted dimensions without manifest default", withLKGCWidgets("    - id: w1\n      type: no-default-pkg\n"), "widget 'w1' omits 'dimensions' and package manifest 'no-default-pkg' has no valid default_dimensions", withPkg("no-default-pkg", mkTestPkg("no-default-pkg", "builtin", "no-default-pkg", 0, 0, 60, nil, nil))},
		{"Stage 3: unsupported dimensions [3, 2]", withLKGCWidgets(mkWYAML("family-cal", "calendar-grid", "[3, 2]", "")), "dimensions [3, 2] not supported by package 'calendar-grid'", nil},
		{"Stage 4: missing required property calendars", withLKGCWidgets(mkWYAML("cal-missing", "calendar-feed", "[4, 2]", "      config: {view: month}\n") + mkWYAML("filler", "spacer", "[2, 2]", "")), "manifest config_schema validation error for widget 'cal-missing'", nil},
		{"Stage 4: calendars type mismatch", withLKGCWidgets(mkWYAML("cal-mismatch", "calendar-feed", "[4, 2]", "      config: {calendars: not-an-array}\n") + mkWYAML("filler", "spacer", "[2, 2]", "")), "manifest config_schema validation error for widget 'cal-mismatch'", nil},
		{"Stage 4: nil config when schema requires properties", withLKGCWidgets(mkWYAML("cal-nil", "calendar-feed", "[4, 2]", "") + mkWYAML("filler", "spacer", "[2, 2]", "")), "manifest config_schema validation error for widget 'cal-nil'", nil},
		{"Stage 5: sensor-http missing endpoint", withLKGCWidgets(mkWYAML("http-1", "sensor-http", "[2, 1]", "")), "widget 'http-1': provider 'http' requires non-empty 'endpoint' URL", nil},
		{"Stage 5: sensor-http invalid endpoint", withLKGCWidgets(mkWYAML("http-1", "sensor-http", "[2, 1]", "      endpoint: \"ftp://not-supported\"\n")), "widget 'http-1': invalid endpoint URL", nil},
		{"Stage 5: sensor-http hostless endpoint", withLKGCWidgets(mkWYAML("http-1", "sensor-http", "[2, 1]", "      endpoint: \"http://\"\n")), "widget 'http-1': invalid endpoint URL", nil},
		{"Stage 5: tasks missing source", withLKGCWidgets(mkTaskYAML("tasks-slave", "[2, 1]", "chores", "", "")), "tasks widget 'tasks-slave' requires non-empty 'source'", nil},
		{"Stage 5: tasks invalid source", withLKGCWidgets(mkTaskYAML("tasks-1", "[2, 1]", "chores", "invalid_source_type", "")), "declares invalid source 'invalid_source_type'", nil},
		{"Stage 5: tasks local source rejected", withLKGCWidgets(mkTaskYAML("tasks-1", "[2, 1]", "chores", "local", "")), "declares invalid source 'local'", nil},
		{"Stage 5: tasks http source rejected", withLKGCWidgets(mkTaskYAML("tasks-1", "[2, 1]", "chores", "http", "")), "declares invalid source 'http'", nil},
		{"Stage 5: tasks gtasks missing tasklist_id", withLKGCWidgets(mkTaskYAML("tasks-1", "[2, 1]", "chores", "gtasks", "        gtasks: {refresh_token: dummy}\n")), "tasks widget 'tasks-1' with source 'gtasks' requires non-empty 'gtasks.tasklist_id'", nil},
		{"Stage 5: tasks todoist missing todoist block", withLKGCWidgets(mkTaskYAML("tasks-1", "[2, 1]", "chores", "todoist", "")), "tasks widget 'tasks-1' with source 'todoist' requires 'todoist' config block", nil},
		{"Stage 5: tasks todoist invalid base_url", withLKGCWidgets(mkTaskYAML("tasks-1", "[2, 1]", "chores", "todoist", "        todoist: {base_url: \"not-a-valid-url\"}\n")), "invalid todoist.base_url 'not-a-valid-url'", nil},
		{"Stage 5: family more than 8 members", buildFamilyYAML(makeNineMembers()), "at most 8 members are supported, found 9", nil},
		{"Stage 5: family duplicate member name", buildFamilyYAML(mkMemberYAML("Alex", "#E07A5F", "solid", "[alex-personal]") + mkMemberYAML("Alex", "#81B29A", "stripes", "[alex-personal]")), "duplicate member name \"Alex\"", nil},
		{"Stage 5: family duplicate pattern", buildFamilyYAML(mkMemberYAML("Alex", "#E07A5F", "dots", "[alex-personal]") + mkMemberYAML("Taylor", "#81B29A", "dots", "[alex-personal]")), "member \"Alex\" and member \"Taylor\" both declare e-ink pattern \"dots\"", nil},
		{"Stage 5: family omitted colliding with explicit solid", buildFamilyYAML(mkMemberYAML("Alex", "#E07A5F", "", "[alex-personal]") + mkMemberYAML("Taylor", "#81B29A", "solid", "[alex-personal]")), "both declare e-ink pattern \"solid\"", nil},
		{"Stage 5: family omitted patterns colliding", buildFamilyYAML(mkMemberYAML("Alex", "#E07A5F", "", "[alex-personal]") + mkMemberYAML("Taylor", "#81B29A", "", "[alex-personal]")), "both declare e-ink pattern \"solid\"", nil},
		{"Stage 5: family unknown calendar reference", buildFamilyYAML(mkMemberYAML("Alex", "#E07A5F", "solid", "[unknown-cal]")), "member \"Alex\" claims unknown calendar \"unknown-cal\"", nil},
		{"Stage 5: family empty calendar reference", buildFamilyYAML(mkMemberYAML("Alex", "#E07A5F", "solid", "[\"\"]")), "member \"Alex\" declares an empty calendar reference", nil},
		{"Stage 5: family missing member name", buildFamilyYAML(mkMemberYAML("", "#E07A5F", "solid", "[alex-personal]")), "requires non-empty 'name'", nil},
		{"Stage 5: family bad hours order", buildFamilyWithHoursYAML("[\"18:00\", \"08:00\"]", mkMemberYAML("Alex", "#E07A5F", "solid", "[alex-personal]")), "'hours' start \"18:00\" must be before end \"08:00\"", nil},
		{"Stage 6: layout cell deficit", withLKGCWidgets("    - id: cal-1\n      type: calendar-feed\n      dimensions: [4, 2]\n      config:\n        calendars: []\n    - id: weather-1\n      type: weather-forecast\n      dimensions: [2, 1]"), "Deficit", nil},
		{"Stage 6: layout geometric conflict", withLKGCWidgets(mkWYAML("w1", "calendar-feed", "[4, 2]", "      pinned: true\n      config: {calendars: [{name: c}]}\n") + mkWYAML("w2", "calendar-grid", "[6, 2]", "      pinned: true\n")), "layout solver error", nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			loaderToUse := loader
			if tc.pkgs != nil {
				loaderToUse = &mockPackageLoader{packages: tc.pkgs}
			}
			m, err := config.NewManager([]byte(tc.yamlData), loaderToUse, nil, nil)
			if err == nil {
				t.Fatalf("expected NewManager validation error for %s, got nil", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.wantErrSub)
			}
			if m != nil {
				t.Fatal("expected nil manager on cold boot failure")
			}
		})
	}
}

func TestLKGC_ReloadValidDomainConfigs(t *testing.T) {
	t.Parallel()
	loader := &mockPackageLoader{packages: createStandardPackages()}

	tests := []struct {
		name     string
		yamlData string
	}{
		{"valid todoist source", withLKGCWidgets("    - id: my-todo\n      type: tasks\n      dimensions: [6, 2]\n      config:\n        list_id: test\n        source: todoist\n        todoist:\n          project_id: proj-123\n")},
		{"matching gtasks source with different show_completed", withLKGCWidgets("    - id: t1\n      type: tasks\n      dimensions: [3, 2]\n      config:\n        list_id: shared\n        source: gtasks\n        gtasks:\n          tasklist_id: list-123\n        show_completed: 5\n    - id: t2\n      type: tasks\n      dimensions: [3, 2]\n      config:\n        list_id: shared\n        source: gtasks\n        gtasks:\n          tasklist_id: list-123\n        show_completed: 1\n")},
		{"valid calendar-grid config", buildFamilyWithHoursYAML("[\"08:00\", \"18:00\"]", mkMemberYAML("Alex", "#E07A5F", "solid", "[alex-personal]")+mkMemberYAML("Taylor", "#81B29A", "dots", "[school]"))},
		{"calendar-grid with non-string hours elements skipped", buildFamilyWithHoursYAML("[7, 21]", mkMemberYAML("Alex", "#E07A5F", "solid", "[alex-personal]"))},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			snap, err := config.ValidatePipeline([]byte(tc.yamlData), loader, nil)
			if err != nil {
				t.Fatalf("expected valid domain config for %s, got: %v", tc.name, err)
			}
			if snap == nil || snap.Config == nil {
				t.Fatal("expected non-nil snapshot and config")
			}
		})
	}
}

func TestLKGC_DiffConfigs(t *testing.T) {
	t.Parallel()
	newInterval := 60
	oldCfg := mkCfg(
		mkW("unchanged-1", "spacer", 2, 1, false, "", nil, nil),
		mkW("removed-1", "spacer", 2, 1, false, "", nil, nil),
		mkW("mod-transport", "spacer", 2, 1, false, "http://old", nil, nil),
		mkW("mod-domain", "tasks", 2, 1, false, "", nil, map[string]any{"list_id": "old"}),
		mkW("replaced-1", "tasks", 2, 1, false, "", nil, nil),
		mkW("resized-only", "spacer", 2, 1, false, "", nil, nil),
		mkW("pinned-only", "spacer", 2, 1, false, "", nil, nil),
	)
	newCfg := mkCfg(
		mkW("unchanged-1", "spacer", 2, 1, false, "", nil, nil),
		mkW("added-1", "spacer", 2, 1, false, "", nil, nil),
		mkW("mod-transport", "spacer", 2, 1, false, "http://new", &newInterval, nil),
		mkW("mod-domain", "tasks", 2, 1, false, "", nil, map[string]any{"list_id": "new"}),
		mkW("replaced-1", "spacer", 2, 1, false, "", nil, nil),
		mkW("resized-only", "spacer", 4, 1, false, "", nil, nil),
		mkW("pinned-only", "spacer", 2, 1, true, "", nil, nil),
	)

	diff := config.DiffConfigs(oldCfg, newCfg)
	if len(diff.Unchanged) != 3 || len(diff.LayoutModified) != 2 || len(diff.Added) != 1 || len(diff.Removed) != 1 || len(diff.Modified) != 3 {
		t.Fatalf("unexpected diff counts: %+v", diff)
	}

	for _, m := range diff.Modified {
		if (m.ID == "mod-transport" && (m.DomainChange || m.Change != config.ChangeModified)) ||
			(m.ID == "mod-domain" && (!m.DomainChange || m.Change != config.ChangeModified)) ||
			(m.ID == "replaced-1" && m.Change != config.ChangeReplaced) ||
			(m.ID == "resized-only" || m.ID == "pinned-only") {
			t.Errorf("unexpected modified instance diff: %+v", m)
		}
	}

	if config.DiffConfigs(oldCfg, oldCfg).TimezoneChanged || !config.DiffConfigs(&config.Config{Timezone: "America/Los_Angeles"}, &config.Config{Timezone: "America/New_York"}).TimezoneChanged {
		t.Error("unexpected TimezoneChanged result")
	}

	trueVal, falseVal := true, false
	for _, tc := range []struct {
		name           string
		oldCfg, newCfg *config.Config
		want           bool
	}{
		{"nil old", nil, mkShellCfg("", false, "", nil), false},
		{"nil new", mkShellCfg("", false, "", nil), nil, false},
		{"empty identical", mkShellCfg("", false, "", nil), mkShellCfg("", false, "", nil), false},
		{"widget only", mkCfg(config.WidgetConfig{ID: "w1", Type: "clock"}), mkCfg(config.WidgetConfig{ID: "w1", Type: "clock"}, config.WidgetConfig{ID: "w2", Type: "weather"}), false},
		{"tz only", mkShellCfg("America/Los_Angeles", false, "", nil), mkShellCfg("America/New_York", false, "", nil), false},
		{"video enabled added", mkShellCfg("", false, "", nil), mkShellCfg("", false, "", mkVideoCfg(&trueVal, "chromecast", "http://localhost:1984/stream")), true},
		{"video enabled flipped", mkShellCfg("", false, "", mkVideoCfg(&trueVal, "", "")), mkShellCfg("", false, "", mkVideoCfg(&falseVal, "", "")), true},
		{"video stream URL changed", mkShellCfg("", false, "", mkVideoCfg(&trueVal, "", "http://localhost:1984/old")), mkShellCfg("", false, "", mkVideoCfg(&trueVal, "", "http://localhost:1984/new")), true},
		{"hide cursor changed", mkShellCfg("", false, "", nil), mkShellCfg("", true, "", nil), true},
		{"remote URL changed", mkShellCfg("", false, "", nil), mkShellCfg("", false, "http://192.168.1.50:8080", nil), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if d := config.DiffConfigs(tc.oldCfg, tc.newCfg); d.ShellChanged != tc.want {
				t.Errorf("got %v, want %v", d.ShellChanged, tc.want)
			}
		})
	}
}

func TestLKGC_ConcurrencyAndRace(t *testing.T) {
	t.Parallel()
	m, err := config.NewManager([]byte(validBaseYAML), &mockPackageLoader{packages: createStandardPackages()}, nil, nil)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	var wg sync.WaitGroup
	stopChan := make(chan struct{})

	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stopChan:
					return
				default:
					if m.Current() == nil || m.CurrentSnapshot() == nil {
						t.Errorf("nil snapshot accessor during concurrent read")
					}
					if st := m.Status(); st.ConfigStatus != config.ConfigStatusOK && st.ConfigStatus != config.ConfigStatusError {
						t.Errorf("invalid status: %v", st.ConfigStatus)
					}
				}
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		zones := []string{"America/New_York", "America/Chicago", "America/Denver", "America/Los_Angeles"}
		for i := 0; i < 20; i++ {
			y := "invalid: yaml: ["
			if i%2 == 0 {
				y = strings.Replace(validBaseYAML, "America/New_York", zones[i%len(zones)], 1)
			}
			_, _, _ = m.Reload([]byte(y))
		}
		close(stopChan)
	}()
	wg.Wait()
}
