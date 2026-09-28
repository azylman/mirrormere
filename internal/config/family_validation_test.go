package config_test

import (
	"strings"
	"testing"

	"github.com/azylman/mirrormere/internal/config"
	"github.com/azylman/mirrormere/internal/domain"
)

func familyPackages() map[string]*domain.Package {
	pkgs := createStandardPackages()
	pkgs["calendar-family"] = &domain.Package{
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
	}
	return pkgs
}

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

func TestFamilyCalendar_ValidConfigPasses(t *testing.T) {
	members := `          - name: Alex
            color: "#E07A5F"
            pattern: solid
            calendars: [alex-personal]
          - name: Kid
            color: "#81B29A"
            pattern: hatch
            calendars: [school]`

	loader := &mockPackageLoader{packages: familyPackages()}
	_, err := config.ValidatePipeline([]byte(buildFamilyYAML(members)), loader, nil)
	if err != nil {
		t.Fatalf("expected valid calendar-family config to pass, got: %v", err)
	}
}

func TestFamilyCalendar_UnknownMemberCalendarFails(t *testing.T) {
	members := `          - name: Alex
            color: "#E07A5F"
            pattern: solid
            calendars: [typo-personal]`

	loader := &mockPackageLoader{packages: familyPackages()}
	_, err := config.ValidatePipeline([]byte(buildFamilyYAML(members)), loader, nil)
	if err == nil {
		t.Fatal("expected error for member referencing unknown calendar name")
	}
	if !strings.Contains(err.Error(), "unknown calendar") {
		t.Fatalf("expected 'unknown calendar' error, got: %v", err)
	}
}

func TestFamilyCalendar_DuplicateMemberNameFails(t *testing.T) {
	members := `          - name: Alex
            color: "#E07A5F"
            pattern: solid
            calendars: [alex-personal]
          - name: Alex
            color: "#81B29A"
            pattern: hatch
            calendars: [school]`

	loader := &mockPackageLoader{packages: familyPackages()}
	_, err := config.ValidatePipeline([]byte(buildFamilyYAML(members)), loader, nil)
	if err == nil {
		t.Fatal("expected error for duplicate member name")
	}
	if !strings.Contains(err.Error(), "duplicate member name") {
		t.Fatalf("expected 'duplicate member name' error, got: %v", err)
	}
}

func TestFamilyCalendar_TooManyMembersFails(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 9; i++ {
		sb.WriteString("          - name: Member")
		sb.WriteString(string(rune('A' + i)))
		sb.WriteString("\n            color: \"#E07A5F\"\n            pattern: solid\n            calendars: [alex-personal]\n")
	}

	loader := &mockPackageLoader{packages: familyPackages()}
	_, err := config.ValidatePipeline([]byte(buildFamilyYAML(sb.String())), loader, nil)
	if err == nil {
		t.Fatal("expected error for more than 8 members")
	}
	if !strings.Contains(err.Error(), "at most 8 members") {
		t.Fatalf("expected 'at most 8 members' error, got: %v", err)
	}
}

func TestFamilyCalendar_DuplicatePatternFails(t *testing.T) {
	members := `          - name: Alex
            color: "#E07A5F"
            pattern: solid
            calendars: [alex-personal]
          - name: Kid
            color: "#81B29A"
            pattern: solid
            calendars: [school]`

	loader := &mockPackageLoader{packages: familyPackages()}
	_, err := config.ValidatePipeline([]byte(buildFamilyYAML(members)), loader, nil)
	if err == nil {
		t.Fatal("expected error for duplicate e-ink pattern")
	}
	if !strings.Contains(err.Error(), "must be distinct") {
		t.Fatalf("expected pattern-distinctness error, got: %v", err)
	}
}

func TestFamilyCalendar_OmittedPatternDefaultsAndCollides(t *testing.T) {
	// Neither member declares 'pattern'; both default to "solid" (parseFamilyMembers) and must
	// collide exactly as if both had typed "solid" explicitly.
	members := `          - name: Alex
            color: "#E07A5F"
            calendars: [alex-personal]
          - name: Kid
            color: "#81B29A"
            calendars: [school]`

	loader := &mockPackageLoader{packages: familyPackages()}
	_, err := config.ValidatePipeline([]byte(buildFamilyYAML(members)), loader, nil)
	if err == nil {
		t.Fatal("expected error for two members both omitting 'pattern'")
	}
	if !strings.Contains(err.Error(), "must be distinct") {
		t.Fatalf("expected pattern-distinctness error, got: %v", err)
	}
}

func TestFamilyCalendar_OmittedPatternCollidesWithExplicitSolid(t *testing.T) {
	// One member omits 'pattern' (defaults to "solid"), the other sets "solid" explicitly:
	// same collision.
	members := `          - name: Alex
            color: "#E07A5F"
            calendars: [alex-personal]
          - name: Kid
            color: "#81B29A"
            pattern: solid
            calendars: [school]`

	loader := &mockPackageLoader{packages: familyPackages()}
	_, err := config.ValidatePipeline([]byte(buildFamilyYAML(members)), loader, nil)
	if err == nil {
		t.Fatal("expected error for omitted pattern colliding with explicit 'solid'")
	}
	if !strings.Contains(err.Error(), "must be distinct") {
		t.Fatalf("expected pattern-distinctness error, got: %v", err)
	}
}

func TestFamilyCalendar_BadHoursOrderFails(t *testing.T) {
	yaml := `
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
        hours: ["21:00", "07:00"]
        calendars:
          - name: alex-personal
            url: "https://example.org/alex.ics"
        members:
          - name: Alex
            color: "#E07A5F"
            pattern: solid
            calendars: [alex-personal]
`
	loader := &mockPackageLoader{packages: familyPackages()}
	_, err := config.ValidatePipeline([]byte(yaml), loader, nil)
	if err == nil {
		t.Fatal("expected error for hours[0] >= hours[1]")
	}
	if !strings.Contains(err.Error(), "must be before end") {
		t.Fatalf("expected hours-order error, got: %v", err)
	}
}

func TestFamilyCalendar_EmptyCalendarReferenceFails(t *testing.T) {
	members := `          - name: Alex
            color: "#E07A5F"
            pattern: solid
            calendars: [""]`

	loader := &mockPackageLoader{packages: familyPackages()}
	_, err := config.ValidatePipeline([]byte(buildFamilyYAML(members)), loader, nil)
	if err == nil {
		t.Fatal("expected error for empty calendar reference")
	}
	if !strings.Contains(err.Error(), "empty calendar reference") {
		t.Fatalf("expected empty-reference error, got: %v", err)
	}
}

func TestFamilyCalendar_MissingMemberNameFails(t *testing.T) {
	members := `          - color: "#E07A5F"
            pattern: solid
            calendars: [alex-personal]`

	loader := &mockPackageLoader{packages: familyPackages()}
	_, err := config.ValidatePipeline([]byte(buildFamilyYAML(members)), loader, nil)
	if err == nil {
		t.Fatal("expected error for member missing 'name'")
	}
	if !strings.Contains(err.Error(), "requires non-empty 'name'") {
		t.Fatalf("expected missing-name error, got: %v", err)
	}
}

func TestFamilyCalendar_HoursNonStringElementsSkipped(t *testing.T) {
	// Non-string hours entries are ignored by the cross-field check (structural JSON Schema
	// validation on config_schema is responsible for type errors); it must not panic.
	yaml := `
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
        hours: [7, 21]
        calendars:
          - name: alex-personal
            url: "https://example.org/alex.ics"
        members:
          - name: Alex
            color: "#E07A5F"
            pattern: solid
            calendars: [alex-personal]
`
	loader := &mockPackageLoader{packages: familyPackages()}
	if _, err := config.ValidatePipeline([]byte(yaml), loader, nil); err != nil {
		t.Fatalf("expected non-string hours entries to be tolerated, got: %v", err)
	}
}
