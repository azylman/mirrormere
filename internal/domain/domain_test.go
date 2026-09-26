package domain_test

import (
	"strings"
	"testing"

	"github.com/azylman/mirrormere/internal/domain"
	"gopkg.in/yaml.v3"
)

func TestDimension_ConstructorsAndMethods(t *testing.T) {
	t.Parallel()

	d := domain.NewDimension(4, 2)
	if d.Cols != 4 || d.Rows != 2 {
		t.Errorf("expected 4x2, got %dx%d", d.Cols, d.Rows)
	}
	if d.Area() != 8 {
		t.Errorf("expected area 8, got %d", d.Area())
	}
	if !d.IsValid() {
		t.Error("expected 4x2 to be valid")
	}
	slice := d.Slice()
	if len(slice) != 2 || slice[0] != 4 || slice[1] != 2 {
		t.Errorf("expected slice [4, 2], got %v", slice)
	}
	if d.String() != "[4, 2]" {
		t.Errorf("expected string [4, 2], got %s", d.String())
	}

	// DimensionFromSlice valid
	d2, err := domain.DimensionFromSlice([]int{2, 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d2.Cols != 2 || d2.Rows != 1 {
		t.Errorf("expected 2x1, got %dx%d", d2.Cols, d2.Rows)
	}

	// DimensionFromSlice invalid length
	_, err = domain.DimensionFromSlice([]int{2})
	if err == nil || !strings.Contains(err.Error(), "must contain exactly 2 integers") {
		t.Fatalf("expected length error, got %v", err)
	}

	// DimensionFromSlice out of bounds
	_, err = domain.DimensionFromSlice([]int{7, 2})
	if err == nil || !strings.Contains(err.Error(), "violates 6x2 grid bounds") {
		t.Fatalf("expected bounds error, got %v", err)
	}
}

func TestDimension_YAML(t *testing.T) {
	t.Parallel()

	// 1. Sequence unmarshaling [4, 2]
	var d1 domain.Dimension
	if err := yaml.Unmarshal([]byte("[4, 2]"), &d1); err != nil {
		t.Fatalf("unexpected error unmarshaling sequence: %v", err)
	}
	if d1.Cols != 4 || d1.Rows != 2 {
		t.Errorf("expected 4x2, got %dx%d", d1.Cols, d1.Rows)
	}

	// 2. Mapping unmarshaling
	var d2 domain.Dimension
	if err := yaml.Unmarshal([]byte("cols: 3\nrows: 1"), &d2); err != nil {
		t.Fatalf("unexpected error unmarshaling mapping: %v", err)
	}
	if d2.Cols != 3 || d2.Rows != 1 {
		t.Errorf("expected 3x1, got %dx%d", d2.Cols, d2.Rows)
	}

	// 3. Marshaling
	marshaled, err := yaml.Marshal(d1)
	if err != nil {
		t.Fatalf("unexpected marshal error: %v", err)
	}
	if !strings.Contains(string(marshaled), "- 4\n- 2") && !strings.Contains(string(marshaled), "[4, 2]") {
		t.Errorf("unexpected marshaled output: %s", string(marshaled))
	}

	// 4. Invalid YAML cases
	var dBad domain.Dimension
	if err := yaml.Unmarshal([]byte("[4]"), &dBad); err == nil {
		t.Fatal("expected error for 1-element sequence, got nil")
	}
	if err := yaml.Unmarshal([]byte(`["invalid", 2]`), &dBad); err == nil {
		t.Fatal("expected error for non-integer col, got nil")
	}
	if err := yaml.Unmarshal([]byte(`[4, "invalid"]`), &dBad); err == nil {
		t.Fatal("expected error for non-integer row, got nil")
	}
	if err := yaml.Unmarshal([]byte("cols: invalid\nrows: 1"), &dBad); err == nil {
		t.Fatal("expected error for invalid mapping, got nil")
	}
	if err := yaml.Unmarshal([]byte(`"scalar"`), &dBad); err == nil {
		t.Fatal("expected error for scalar node, got nil")
	}
}

func TestWidgetManifest_Validation(t *testing.T) {
	t.Parallel()

	validYAML := `
name: Family Calendar
version: "1.0.0"
provider: calendar-agenda
capabilities:
  - ambient-static
  - touch-interactive
default_dimensions: [4, 2]
supported_dimensions:
  - [4, 2]
  - [2, 1]
refresh:
  interval_seconds: 300
config_schema:
  type: object
  properties:
    view:
      type: string
`
	var m domain.WidgetManifest
	if err := yaml.Unmarshal([]byte(validYAML), &m); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("unexpected validate error: %v", err)
	}
	if !m.HasCapability("ambient-static") {
		t.Error("expected ambient-static capability")
	}
	if !m.HasCapability("TOUCH-INTERACTIVE") {
		t.Error("expected case-insensitive capability match")
	}
	if m.HasCapability("video-capture") {
		t.Error("did not expect video-capture capability")
	}

	// Missing name
	mNoName := m
	mNoName.Name = ""
	if err := mNoName.Validate(); err == nil || !strings.Contains(err.Error(), "missing required field 'name'") {
		t.Fatalf("expected missing name error, got %v", err)
	}

	// Missing version
	mNoVersion := m
	mNoVersion.Version = ""
	if err := mNoVersion.Validate(); err == nil || !strings.Contains(err.Error(), "missing required field 'version'") {
		t.Fatalf("expected missing version error, got %v", err)
	}

	// Missing provider
	mNoProvider := m
	mNoProvider.Provider = ""
	if err := mNoProvider.Validate(); err == nil || !strings.Contains(err.Error(), "missing required field 'provider'") {
		t.Fatalf("expected missing provider error, got %v", err)
	}

	// Invalid default_dimensions
	mBadDefDim := m
	mBadDefDim.DefaultDimensions = domain.NewDimension(7, 2)
	if err := mBadDefDim.Validate(); err == nil || !strings.Contains(err.Error(), "default_dimensions [7, 2] violates 6x2 grid bounds") {
		t.Fatalf("expected default_dimensions bounds error, got %v", err)
	}

	// Invalid supported_dimensions
	mBadSupDim := m
	mBadSupDim.SupportedDimensions = []domain.Dimension{domain.NewDimension(4, 3)}
	if err := mBadSupDim.Validate(); err == nil || !strings.Contains(err.Error(), "supported_dimensions[0] [4, 3] violates 6x2 grid bounds") {
		t.Fatalf("expected supported_dimensions bounds error, got %v", err)
	}

	// Disallowed default keyword in config_schema
	mDefaultKey := m
	mDefaultKey.ConfigSchema = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"view": map[string]any{
				"type":    "string",
				"default": "month",
			},
		},
	}
	if err := mDefaultKey.Validate(); err == nil || !strings.Contains(err.Error(), "declares disallowed keyword 'default'") {
		t.Fatalf("expected disallowed default error in config_schema, got %v", err)
	}

	// Disallowed default keyword in map[any]any and slice
	mDefaultNested := m
	mDefaultNested.ConfigSchema = map[string]any{
		"items": []any{
			map[any]any{
				"default": "value",
			},
		},
	}
	if err := mDefaultNested.Validate(); err == nil || !strings.Contains(err.Error(), "declares disallowed keyword 'default'") {
		t.Fatalf("expected disallowed default error in nested schema, got %v", err)
	}

	// Disallowed default in response_schema
	mDefaultResp := m
	mDefaultResp.ResponseSchema = map[string]any{
		"default": "foo",
	}
	if err := mDefaultResp.Validate(); err == nil || !strings.Contains(err.Error(), "declares disallowed keyword 'default'") {
		t.Fatalf("expected disallowed default error in response_schema, got %v", err)
	}

	// Provider: http missing response_schema
	mHTTP := m
	mHTTP.Provider = "http"
	mHTTP.ResponseSchema = nil
	if err := mHTTP.Validate(); err == nil || !strings.Contains(err.Error(), "provider 'http' requires non-empty response_schema") {
		t.Fatalf("expected missing response_schema error for http provider, got %v", err)
	}

	// Provider: http with valid response_schema
	mHTTP.ResponseSchema = map[string]any{
		"type": "object",
		"required": []any{"temperature"},
		"properties": map[string]any{
			"temperature": map[string]any{"type": "number"},
		},
	}
	if err := mHTTP.Validate(); err != nil {
		t.Fatalf("unexpected error for valid http provider: %v", err)
	}
}

func TestPackage_Methods(t *testing.T) {
	t.Parallel()

	pkg := domain.Package{
		Type:         "spacer",
		Source:       "builtin",
		Dir:          "/app/widgets/spacer",
		ManifestPath: "/app/widgets/spacer/manifest.yaml",
		ViewPath:     "/app/widgets/spacer/views/widget.html",
		AssetsDir:    "/app/widgets/spacer/assets",
	}

	if !pkg.HasAssets() {
		t.Error("expected HasAssets to be true")
	}
	if !pkg.IsBuiltin() {
		t.Error("expected IsBuiltin to be true")
	}
	if pkg.IsCustom() {
		t.Error("expected IsCustom to be false")
	}

	pkgCustom := domain.Package{
		Type:   "sensor-card",
		Source: "custom",
	}
	if !pkgCustom.IsCustom() {
		t.Error("expected IsCustom to be true")
	}
	if pkgCustom.IsBuiltin() {
		t.Error("expected IsBuiltin to be false")
	}
	if pkgCustom.HasAssets() {
		t.Error("expected HasAssets to be false when AssetsDir is empty")
	}
}
