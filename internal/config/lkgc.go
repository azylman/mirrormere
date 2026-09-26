package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/azylman/mirrormere/internal/domain"
	"github.com/azylman/mirrormere/internal/layout"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

// PackageLoader discovers and resolves widget packages by type.
type PackageLoader interface {
	LoadPackage(widgetType string) (*domain.Package, error)
}

// ConfigStatus indicates the operational validity of the running configuration.
type ConfigStatus string

const (
	// ConfigStatusOK indicates the running configuration is valid and active.
	ConfigStatusOK ConfigStatus = "ok"
	// ConfigStatusError indicates a validation error occurred during candidate reload, retaining LKGC.
	ConfigStatusError ConfigStatus = "error"
)

// Status provides telemetry and health state for system.status reporting.
type Status struct {
	ConfigStatus ConfigStatus `json:"config_status"`
	ConfigError  *string      `json:"config_error"`
	LastChecked  time.Time    `json:"last_checked"`
	LastSuccess  time.Time    `json:"last_success,omitempty"`
}

// Snapshot encapsulates an immutable, synchronized state of runtime configuration, layout, and packages.
type Snapshot struct {
	Config   *Config
	Layout   *layout.Layout
	Packages map[string]*domain.Package
	LoadedAt time.Time
}

// InstanceChangeType categorizes the diff between running and new widget instances per SPEC-012 §3.
type InstanceChangeType string

const (
	ChangeUnchanged InstanceChangeType = "unchanged"
	ChangeAdded     InstanceChangeType = "added"
	ChangeRemoved   InstanceChangeType = "removed"
	ChangeModified  InstanceChangeType = "modified"
	ChangeReplaced  InstanceChangeType = "replaced" // Type change on same ID: Removed + Added
)

// InstanceDiff details modifications to an existing widget instance.
type InstanceDiff struct {
	ID           string
	Type         string
	Change       InstanceChangeType
	OldInstance  *WidgetConfig
	NewInstance  *WidgetConfig
	DomainChange bool // Target domain parameters changed (requires cache purge/empty loading state)
}

// ConfigDiff summarizes all instance-level diffs between running and new configurations.
type ConfigDiff struct {
	Unchanged      []WidgetConfig
	Added          []WidgetConfig
	Removed        []WidgetConfig
	Modified       []InstanceDiff
	LayoutModified []WidgetConfig // Instances whose dimensions or pinned status changed without worker modifications
}

// Manager coordinates live configuration reloads and enforces LKGC resilience per SPEC-012.
type Manager struct {
	reloadMu        sync.Mutex
	current         atomic.Pointer[Snapshot]
	status          atomic.Pointer[Status]
	loader          PackageLoader
	getenv          func(string) string
	logger          *slog.Logger
	configReloadErr error
	packageErrors   map[string]string
}

// NewManager creates and initializes an LKGC Manager with the initial configuration.
// If the initial configuration fails validation, NewManager fails fast with an error.
func NewManager(initialYAML []byte, loader PackageLoader, getenv func(string) string, logger *slog.Logger) (*Manager, error) {
	if loader == nil {
		return nil, fmt.Errorf("package loader cannot be nil")
	}
	if getenv == nil {
		getenv = os.Getenv
	}
	if logger == nil {
		logger = slog.Default()
	}

	m := &Manager{
		loader:        loader,
		getenv:        getenv,
		logger:        logger,
		packageErrors: make(map[string]string),
	}

	snapshot, err := ValidatePipeline(initialYAML, loader, getenv)
	if err != nil {
		errMsg := err.Error()
		m.status.Store(&Status{
			ConfigStatus: ConfigStatusError,
			ConfigError:  &errMsg,
			LastChecked:  time.Now(),
		})
		return nil, fmt.Errorf("initial configuration validation failed: %w", err)
	}

	m.current.Store(snapshot)
	m.status.Store(&Status{
		ConfigStatus: ConfigStatusOK,
		ConfigError:  nil,
		LastChecked:  snapshot.LoadedAt,
		LastSuccess:  snapshot.LoadedAt,
	})

	return m, nil
}

// Current returns the active running configuration snapshot (lock-free).
func (m *Manager) Current() *Config {
	snap := m.current.Load()
	if snap == nil {
		return nil
	}
	return snap.Config
}

// CurrentLayout returns the active 6x2 grid layout (lock-free).
func (m *Manager) CurrentLayout() *layout.Layout {
	snap := m.current.Load()
	if snap == nil {
		return nil
	}
	return snap.Layout
}

// CurrentSnapshot returns the complete running snapshot including packages and layout (lock-free).
func (m *Manager) CurrentSnapshot() *Snapshot {
	return m.current.Load()
}

// Status returns the current configuration telemetry status (lock-free).
func (m *Manager) Status() Status {
	st := m.status.Load()
	if st == nil {
		return Status{ConfigStatus: ConfigStatusOK}
	}
	return *st
}

func (m *Manager) updateStatusLocked() {
	now := time.Now()
	prev := m.status.Load()
	var lastSuccess time.Time
	if prev != nil {
		lastSuccess = prev.LastSuccess
	}

	if m.configReloadErr != nil {
		errMsg := m.configReloadErr.Error()
		m.status.Store(&Status{
			ConfigStatus: ConfigStatusError,
			ConfigError:  &errMsg,
			LastChecked:  now,
			LastSuccess:  lastSuccess,
		})
		return
	}

	if len(m.packageErrors) > 0 {
		var firstType string
		for t := range m.packageErrors {
			if firstType == "" || t < firstType {
				firstType = t
			}
		}
		errMsg := m.packageErrors[firstType]
		m.status.Store(&Status{
			ConfigStatus: ConfigStatusError,
			ConfigError:  &errMsg,
			LastChecked:  now,
			LastSuccess:  lastSuccess,
		})
		return
	}

	m.status.Store(&Status{
		ConfigStatus: ConfigStatusOK,
		ConfigError:  nil,
		LastChecked:  now,
		LastSuccess:  now,
	})
}

// SetPackageError records a package incompleteness error in Manager's status per SPEC-003 §1.
func (m *Manager) SetPackageError(widgetType string, err error) {
	if err == nil {
		return
	}
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()

	if m.packageErrors == nil {
		m.packageErrors = make(map[string]string)
	}
	m.packageErrors[widgetType] = err.Error()
	m.updateStatusLocked()
}

// ClearPackageError clears an incomplete package error for widgetType.
// If all package errors are resolved and no active config error exists, status returns to OK and returns true.
func (m *Manager) ClearPackageError(widgetType string) bool {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()

	if m.packageErrors == nil {
		return false
	}
	if _, ok := m.packageErrors[widgetType]; !ok {
		return false
	}
	delete(m.packageErrors, widgetType)
	m.updateStatusLocked()
	return true
}

// SetErrorStatus explicitly records an error status (e.g. for configuration read errors).
func (m *Manager) SetErrorStatus(err error) {
	if err == nil {
		return
	}
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()

	m.configReloadErr = err
	m.updateStatusLocked()
}

// ClearErrorStatus resets the error status back to OK if it was in error state.
func (m *Manager) ClearErrorStatus() {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()

	m.configReloadErr = nil
	m.packageErrors = make(map[string]string)
	m.updateStatusLocked()
}

// Reload executes the 6-stage validation pipeline on candidate YAML data.
// If validation succeeds, it atomically swaps the running configuration and returns the diff.
// If validation fails, it retains running LKGC, logs structured diagnostics, and updates telemetry.
func (m *Manager) Reload(data []byte) (*Snapshot, *ConfigDiff, error) {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()

	candidate, err := ValidatePipeline(data, m.loader, m.getenv)
	if err != nil {
		errMsg := err.Error()
		m.logger.Error(fmt.Sprintf("[config.reloader] error=\"live config validation failed: %s; retaining LKGC\"", errMsg))

		m.configReloadErr = err
		m.updateStatusLocked()
		return nil, nil, err
	}

	oldSnap := m.current.Load()
	var diff *ConfigDiff
	if oldSnap != nil && oldSnap.Config != nil {
		diff = DiffConfigs(oldSnap.Config, candidate.Config)
	} else {
		diff = &ConfigDiff{}
		diff.Added = append(diff.Added, candidate.Config.Display.Widgets...)
	}

	m.configReloadErr = nil
	m.current.Store(candidate)
	m.updateStatusLocked()

	return candidate, diff, nil
}

// ValidatePipeline executes the 6-stage LKGC validation pipeline per SPEC-012 §2.
func ValidatePipeline(data []byte, loader PackageLoader, getenv func(string) string) (*Snapshot, error) {
	if loader == nil {
		return nil, fmt.Errorf("package loader cannot be nil")
	}
	if getenv == nil {
		getenv = os.Getenv
	}

	// Stage 1: YAML Syntax & AST Structure
	cfg, err := parseYAMLStage(data)
	if err != nil {
		return nil, fmt.Errorf("stage 1 (syntax & structure): %w", err)
	}

	// Stage 2: Core Instance Schemas & Env Resolution
	if err := validateCoreInstancesStage(cfg, getenv); err != nil {
		return nil, fmt.Errorf("stage 2 (instance schemas): %w", err)
	}

	// Stage 3: Package Existence & Completeness
	packages, err := validatePackagesStage(cfg, loader)
	if err != nil {
		return nil, fmt.Errorf("stage 3 (package existence & completeness): %w", err)
	}

	// Stage 4: Manifest config_schema Validation
	if err := validateManifestConfigSchemasStage(cfg, packages); err != nil {
		return nil, fmt.Errorf("stage 4 (manifest config_schema): %w", err)
	}

	// Stage 5: Domain & Source-of-Truth Rules
	if err := validateDomainRulesStage(cfg, packages); err != nil {
		return nil, fmt.Errorf("stage 5 (domain rules): %w", err)
	}

	// Stage 6: 6x2 Bin-Packing Layout Solver
	l, err := validateLayoutSolverStage(cfg)
	if err != nil {
		return nil, fmt.Errorf("stage 6 (layout solver): %w", err)
	}

	return &Snapshot{
		Config:   cfg,
		Layout:   l,
		Packages: packages,
		LoadedAt: time.Now(),
	}, nil
}

func parseYAMLStage(data []byte) (*Config, error) {
	if len(data) == 0 || len(strings.TrimSpace(string(data))) == 0 {
		return nil, fmt.Errorf("configuration file is empty")
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("YAML syntax error: %w", err)
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
	return &cfg, nil
}

func validateCoreInstancesStage(cfg *Config, getenv func(string) string) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := cfg.ResolveEnv(getenv); err != nil {
		return err
	}
	return nil
}

func validatePackagesStage(cfg *Config, loader PackageLoader) (map[string]*domain.Package, error) {
	registry := make(map[string]*domain.Package)

	for _, w := range cfg.Display.Widgets {
		if _, exists := registry[w.Type]; exists {
			continue
		}
		pkg, err := loader.LoadPackage(w.Type)
		if err != nil {
			return nil, fmt.Errorf("widget package '%s' error: %w", w.Type, err)
		}
		if err := pkg.Manifest.Validate(); err != nil {
			return nil, fmt.Errorf("widget package '%s' manifest invalid: %w", w.Type, err)
		}
		registry[w.Type] = pkg
	}

	// Apply manifest defaults (default_dimensions, refresh_interval_seconds)
	for i := range cfg.Display.Widgets {
		w := &cfg.Display.Widgets[i]
		pkg := registry[w.Type]

		// Fallback dimensions if omitted
		if len(w.Dimensions) == 0 {
			if !pkg.Manifest.DefaultDimensions.IsValid() {
				return nil, fmt.Errorf("[Mirrormere Config Error] widget '%s' omits 'dimensions' and package manifest '%s' has no valid default_dimensions", w.ID, w.Type)
			}
			w.Dimensions = pkg.Manifest.DefaultDimensions.Slice()
		}

		// Fallback refresh interval if omitted
		if w.RefreshIntervalSeconds == nil && pkg.Manifest.Refresh.IntervalSeconds > 0 {
			interval := pkg.Manifest.Refresh.IntervalSeconds
			w.RefreshIntervalSeconds = &interval
		}
	}

	// Verify supported_dimensions if declared in manifest per SPEC-003 §107
	for _, w := range cfg.Display.Widgets {
		pkg := registry[w.Type]
		if len(pkg.Manifest.SupportedDimensions) > 0 {
			dim, err := domain.DimensionFromSlice(w.Dimensions)
			if err != nil {
				return nil, err
			}
			supported := false
			for _, s := range pkg.Manifest.SupportedDimensions {
				if s.Cols == dim.Cols && s.Rows == dim.Rows {
					supported = true
					break
				}
			}
			if !supported {
				return nil, fmt.Errorf("widget '%s': dimensions [%d, %d] not supported by package '%s' (supported: %v)",
					w.ID, dim.Cols, dim.Rows, w.Type, pkg.Manifest.SupportedDimensions)
			}
		}
	}

	return registry, nil
}

func validateManifestConfigSchemasStage(cfg *Config, registry map[string]*domain.Package) error {
	compiledSchemas := make(map[string]*jsonschema.Schema)

	for _, w := range cfg.Display.Widgets {
		pkg := registry[w.Type]
		if len(pkg.Manifest.ConfigSchema) == 0 {
			continue
		}

		sch, compiled := compiledSchemas[w.Type]
		if !compiled {
			compiler := jsonschema.NewCompiler()
			compiler.DefaultDraft(jsonschema.Draft2020)

			schemaJSON, err := json.Marshal(pkg.Manifest.ConfigSchema)
			if err != nil {
				return fmt.Errorf("widget '%s': failed to marshal manifest config_schema: %w", w.ID, err)
			}
			doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaJSON))
			if err != nil {
				return fmt.Errorf("widget '%s': failed to unmarshal manifest config_schema: %w", w.ID, err)
			}
			schemaURL := fmt.Sprintf("schema://widgets/%s/config_schema.json", w.Type)
			if err := compiler.AddResource(schemaURL, doc); err != nil {
				return fmt.Errorf("widget '%s': failed to add schema resource: %w", w.ID, err)
			}
			s, err := compiler.Compile(schemaURL)
			if err != nil {
				return fmt.Errorf("widget '%s': failed to compile manifest config_schema: %w", w.ID, err)
			}
			compiledSchemas[w.Type] = s
			sch = s
		}

		instanceConfig := w.Config
		if instanceConfig == nil {
			instanceConfig = make(map[string]any)
		}
		valJSON, err := json.Marshal(instanceConfig)
		if err != nil {
			return fmt.Errorf("widget '%s': failed to marshal instance config: %w", w.ID, err)
		}
		valDoc, err := jsonschema.UnmarshalJSON(bytes.NewReader(valJSON))
		if err != nil {
			return fmt.Errorf("widget '%s': failed to unmarshal instance config: %w", w.ID, err)
		}

		if err := sch.Validate(valDoc); err != nil {
			return fmt.Errorf("manifest config_schema validation error for widget '%s': %w", w.ID, err)
		}
	}

	return nil
}

func validateDomainRulesStage(cfg *Config, registry map[string]*domain.Package) error {
	// 1. Generic HTTP widgets check (SPEC-003)
	for _, w := range cfg.Display.Widgets {
		pkg := registry[w.Type]
		if pkg != nil && pkg.Manifest.Provider == "http" {
			if w.Endpoint == "" {
				return fmt.Errorf("widget '%s': provider 'http' requires non-empty 'endpoint' URL per SPEC-003", w.ID)
			}
		}
	}

	// 2. Tasks & Lists Source-of-Truth Rules (SPEC-008 §Shared List State & Source Ownership Rules)
	type primarySourceDef struct {
		widgetID  string
		source    string
		sourceCfg any
	}
	primarySources := make(map[string]primarySourceDef)
	allListIDs := make(map[string][]string) // list_id -> []widgetID

	for _, w := range cfg.Display.Widgets {
		if w.Type != "tasks" {
			continue
		}

		listID := w.ID
		if w.Config != nil {
			if customListID, ok := w.Config["list_id"].(string); ok && customListID != "" {
				listID = customListID
			}
		}
		allListIDs[listID] = append(allListIDs[listID], w.ID)

		var source string
		if w.Config != nil {
			if src, ok := w.Config["source"].(string); ok {
				source = src
			}
		}

		if source != "" {
			switch source {
			case "local", "gtasks", "http":
				// Valid source adapter
			default:
				return fmt.Errorf("[Mirrormere Config Error] tasks widget '%s' declares invalid source '%s' (must be 'local', 'gtasks', or 'http')", w.ID, source)
			}

			var sourceCfg any
			if source == "http" {
				httpCfg, ok := w.Config["http"].(map[string]any)
				if !ok || httpCfg == nil {
					return fmt.Errorf("[Mirrormere Config Error] tasks widget '%s' with source 'http' requires 'http' config block", w.ID)
				}
				baseURL, ok := httpCfg["base_url"].(string)
				if !ok || baseURL == "" {
					return fmt.Errorf("[Mirrormere Config Error] tasks widget '%s' with source 'http' requires non-empty 'http.base_url'", w.ID)
				}
				u, err := url.ParseRequestURI(baseURL)
				if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
					return fmt.Errorf("[Mirrormere Config Error] tasks widget '%s': invalid http.base_url '%s' (must be http:// or https:// with host)", w.ID, baseURL)
				}
				sourceCfg = httpCfg
			} else if source == "gtasks" {
				gtasksCfg, ok := w.Config["gtasks"].(map[string]any)
				if !ok || gtasksCfg == nil {
					return fmt.Errorf("[Mirrormere Config Error] tasks widget '%s' with source 'gtasks' requires 'gtasks' config block", w.ID)
				}
				taskListID, ok := gtasksCfg["tasklist_id"].(string)
				if !ok || taskListID == "" {
					return fmt.Errorf("[Mirrormere Config Error] tasks widget '%s' with source 'gtasks' requires non-empty 'gtasks.tasklist_id'", w.ID)
				}
				sourceCfg = gtasksCfg
			}

			if existing, exists := primarySources[listID]; exists {
				if existing.source != source || !reflect.DeepEqual(existing.sourceCfg, sourceCfg) {
					return fmt.Errorf("[Mirrormere Config Error] conflicting source definitions for list_id '%s' between widget '%s' and widget '%s'",
						listID, existing.widgetID, w.ID)
				}
			} else {
				primarySources[listID] = primarySourceDef{
					widgetID:  w.ID,
					source:    source,
					sourceCfg: sourceCfg,
				}
			}
		}
	}

	for listID, widgetIDs := range allListIDs {
		if _, hasPrimary := primarySources[listID]; !hasPrimary {
			firstWidget := widgetIDs[0]
			return fmt.Errorf("[Mirrormere Config Error] tasks widget '%s' references list_id '%s' with no primary source definition",
				firstWidget, listID)
		}
	}

	return nil
}

func validateLayoutSolverStage(cfg *Config) (*layout.Layout, error) {
	inputs := make([]layout.WidgetInput, len(cfg.Display.Widgets))
	for i, w := range cfg.Display.Widgets {
		dim, err := domain.DimensionFromSlice(w.Dimensions)
		if err != nil {
			return nil, err
		}
		inputs[i] = layout.WidgetInput{
			ID:         w.ID,
			Type:       w.Type,
			Dimensions: dim,
			Pinned:     w.Pinned,
		}
	}

	l, err := layout.Solve(inputs)
	if err != nil {
		return nil, fmt.Errorf("layout solver error: %w", err)
	}
	return l, nil
}

// DiffConfigs computes instance-level differences between old and new configurations per SPEC-012 §3.
func DiffConfigs(oldCfg, newCfg *Config) *ConfigDiff {
	diff := &ConfigDiff{}
	oldMap := make(map[string]WidgetConfig)
	if oldCfg != nil {
		for _, w := range oldCfg.Display.Widgets {
			oldMap[w.ID] = w
		}
	}

	newMap := make(map[string]WidgetConfig)
	if newCfg != nil {
		for _, w := range newCfg.Display.Widgets {
			newMap[w.ID] = w
		}
	}

	if newCfg != nil {
		for _, newW := range newCfg.Display.Widgets {
			oldW, exists := oldMap[newW.ID]
			if !exists {
				diff.Added = append(diff.Added, newW)
				continue
			}

			if oldW.Type != newW.Type {
				// Type change on same ID is composite Removed + Added (Replacement)
				diff.Modified = append(diff.Modified, InstanceDiff{
					ID:           newW.ID,
					Type:         newW.Type,
					Change:       ChangeReplaced,
					OldInstance:  &oldW,
					NewInstance:  &newW,
					DomainChange: true,
				})
				continue
			}

			domainChanged := !reflect.DeepEqual(oldW.Config, newW.Config)
			transportChanged := oldW.Endpoint != newW.Endpoint ||
				oldW.Method != newW.Method ||
				oldW.TokenEnv != newW.TokenEnv ||
				!equalIntPtr(oldW.RefreshIntervalSeconds, newW.RefreshIntervalSeconds)
			layoutChanged := !reflect.DeepEqual(oldW.Dimensions, newW.Dimensions) ||
				oldW.Pinned != newW.Pinned

			if !domainChanged && !transportChanged {
				diff.Unchanged = append(diff.Unchanged, newW)
				if layoutChanged {
					diff.LayoutModified = append(diff.LayoutModified, newW)
				}
			} else {
				diff.Modified = append(diff.Modified, InstanceDiff{
					ID:           newW.ID,
					Type:         newW.Type,
					Change:       ChangeModified,
					OldInstance:  &oldW,
					NewInstance:  &newW,
					DomainChange: domainChanged,
				})
			}
		}
	}

	if oldCfg != nil {
		for _, oldW := range oldCfg.Display.Widgets {
			if _, exists := newMap[oldW.ID]; !exists {
				diff.Removed = append(diff.Removed, oldW)
			}
		}
	}

	return diff
}

func equalIntPtr(a, b *int) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}
