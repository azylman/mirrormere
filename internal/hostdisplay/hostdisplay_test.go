package hostdisplay_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azylman/mirrormere/internal/hostdisplay"
)

func TestParseSystemdUnit(t *testing.T) {
	t.Parallel()

	valid := `
# Comment
; Another comment

[Unit]
Description=Test Unit
After=network.target

[Service]
ExecStart=/bin/true
Restart=always
`
	unit, err := hostdisplay.ParseSystemdUnit(valid)
	if err != nil {
		t.Fatalf("unexpected error parsing valid unit: %v", err)
	}

	if unit.Sections["Unit"]["Description"] != "Test Unit" {
		t.Errorf("expected Description='Test Unit', got %q", unit.Sections["Unit"]["Description"])
	}
	if unit.Sections["Service"]["Restart"] != "always" {
		t.Errorf("expected Restart='always', got %q", unit.Sections["Service"]["Restart"])
	}

	// Empty section header error
	_, err = hostdisplay.ParseSystemdUnit("[]\nFoo=Bar")
	if err == nil {
		t.Error("expected error for empty section header, got nil")
	}

	// Directive before section header error
	_, err = hostdisplay.ParseSystemdUnit("Foo=Bar\n[Unit]\nDesc=Test")
	if err == nil {
		t.Error("expected error for directive before section header, got nil")
	}

	// Malformed directive (missing =)
	_, err = hostdisplay.ParseSystemdUnit("[Unit]\nMalformedLineWithoutEquals")
	if err == nil {
		t.Error("expected error for malformed directive, got nil")
	}
}

func TestParseSystemdUnitFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	unitPath := filepath.Join(dir, "test.service")

	content := "[Unit]\nDescription=Sample\n\n[Service]\nExecStart=/usr/bin/sample\n"
	if err := os.WriteFile(unitPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test unit file: %v", err)
	}

	unit, err := hostdisplay.ParseSystemdUnitFile(unitPath)
	if err != nil {
		t.Fatalf("unexpected error parsing unit file: %v", err)
	}
	if unit.Path != unitPath {
		t.Errorf("expected Path=%s, got %s", unitPath, unit.Path)
	}

	// Non-existent file error
	_, err = hostdisplay.ParseSystemdUnitFile(filepath.Join(dir, "non-existent.service"))
	if err == nil {
		t.Error("expected error for non-existent file, got nil")
	}

	// Corrupt content file error
	corruptPath := filepath.Join(dir, "corrupt.service")
	if err := os.WriteFile(corruptPath, []byte("DirectiveBeforeSection=1"), 0644); err != nil {
		t.Fatalf("failed to write corrupt unit file: %v", err)
	}
	_, err = hostdisplay.ParseSystemdUnitFile(corruptPath)
	if err == nil {
		t.Error("expected error parsing corrupt unit file, got nil")
	}
}

func TestValidateDisplayService(t *testing.T) {
	t.Parallel()

	// Nil unit
	if err := hostdisplay.ValidateDisplayService(nil); err == nil {
		t.Error("expected error on nil unit, got nil")
	}

	validUnit := &hostdisplay.SystemdUnit{
		Sections: map[string]map[string]string{
			"Unit": {
				"Description": "Mirrormere Wayland Touch Display",
				"Conflicts":   "getty@tty1.service",
			},
			"Service": {
				"User":                "display",
				"Group":               "display",
				"SupplementaryGroups": "video input audio render",
				"PAMName":             "login",
				"TTYPath":             "/dev/tty1",
				"Restart":             "always",
				"ExecStart":           "/opt/mirrormere/display/launch.sh",
			},
		},
	}

	if err := hostdisplay.ValidateDisplayService(validUnit); err != nil {
		t.Fatalf("unexpected error on valid display unit: %v", err)
	}

	// Fallback kiosk user also valid
	kioskUnit := cloneUnit(validUnit)
	kioskUnit.Sections["Service"]["User"] = "kiosk"
	kioskUnit.Sections["Service"]["Group"] = "kiosk"
	if err := hostdisplay.ValidateDisplayService(kioskUnit); err != nil {
		t.Fatalf("unexpected error on valid kiosk fallback user: %v", err)
	}

	// Backward compat alias check
	if err := hostdisplay.ValidateKioskService(validUnit); err != nil {
		t.Fatalf("unexpected error calling ValidateKioskService alias: %v", err)
	}

	// Missing Unit section
	bad := &hostdisplay.SystemdUnit{Sections: map[string]map[string]string{}}
	if err := hostdisplay.ValidateDisplayService(bad); err == nil {
		t.Error("expected error on missing Unit section")
	}

	// Missing Conflicts
	bad = &hostdisplay.SystemdUnit{Sections: map[string]map[string]string{"Unit": {}}}
	if err := hostdisplay.ValidateDisplayService(bad); err == nil {
		t.Error("expected error on missing Conflicts")
	}

	// Missing Service section
	bad = &hostdisplay.SystemdUnit{Sections: map[string]map[string]string{"Unit": {"Conflicts": "getty@tty1.service"}}}
	if err := hostdisplay.ValidateDisplayService(bad); err == nil {
		t.Error("expected error on missing Service section")
	}

	// Wrong User
	bad = cloneUnit(validUnit)
	bad.Sections["Service"]["User"] = "root"
	if err := hostdisplay.ValidateDisplayService(bad); err == nil {
		t.Error("expected error on User=root")
	}

	// Wrong Group
	bad = cloneUnit(validUnit)
	bad.Sections["Service"]["Group"] = "root"
	if err := hostdisplay.ValidateDisplayService(bad); err == nil {
		t.Error("expected error on Group=root")
	}

	// Missing SupplementaryGroups
	bad = cloneUnit(validUnit)
	bad.Sections["Service"]["SupplementaryGroups"] = "video input"
	if err := hostdisplay.ValidateDisplayService(bad); err == nil {
		t.Error("expected error on missing audio/render groups")
	}

	// Wrong PAMName
	bad = cloneUnit(validUnit)
	bad.Sections["Service"]["PAMName"] = "other"
	if err := hostdisplay.ValidateDisplayService(bad); err == nil {
		t.Error("expected error on PAMName=other")
	}

	// Wrong TTYPath
	bad = cloneUnit(validUnit)
	bad.Sections["Service"]["TTYPath"] = "/dev/tty2"
	if err := hostdisplay.ValidateDisplayService(bad); err == nil {
		t.Error("expected error on TTYPath=/dev/tty2")
	}

	// Wrong Restart
	bad = cloneUnit(validUnit)
	bad.Sections["Service"]["Restart"] = "on-failure"
	if err := hostdisplay.ValidateDisplayService(bad); err == nil {
		t.Error("expected error on Restart=on-failure")
	}

	// Wrong ExecStart
	bad = cloneUnit(validUnit)
	bad.Sections["Service"]["ExecStart"] = "/opt/mirrormere/display/wrong.sh"
	if err := hostdisplay.ValidateDisplayService(bad); err == nil {
		t.Error("expected error on non-launch.sh ExecStart")
	}
}

func TestValidateTimer(t *testing.T) {
	t.Parallel()

	if err := hostdisplay.ValidateTimer(nil, "*-*-* 23:00:00"); err == nil {
		t.Error("expected error on nil unit")
	}

	bad := &hostdisplay.SystemdUnit{Sections: map[string]map[string]string{}}
	if err := hostdisplay.ValidateTimer(bad, "*-*-* 23:00:00"); err == nil {
		t.Error("expected error on missing Timer section")
	}

	bad = &hostdisplay.SystemdUnit{Sections: map[string]map[string]string{"Timer": {"OnCalendar": "*-*-* 12:00:00", "Persistent": "true"}}}
	if err := hostdisplay.ValidateTimer(bad, "*-*-* 23:00:00"); err == nil {
		t.Error("expected error on mismatched OnCalendar")
	}

	bad = &hostdisplay.SystemdUnit{Sections: map[string]map[string]string{"Timer": {"OnCalendar": "*-*-* 23:00:00", "Persistent": "false"}}}
	if err := hostdisplay.ValidateTimer(bad, "*-*-* 23:00:00"); err == nil {
		t.Error("expected error on Persistent=false")
	}

	good := &hostdisplay.SystemdUnit{Sections: map[string]map[string]string{"Timer": {"OnCalendar": "*-*-* 23:00:00", "Persistent": "true"}}}
	if err := hostdisplay.ValidateTimer(good, "*-*-* 23:00:00"); err != nil {
		t.Errorf("unexpected error on valid timer: %v", err)
	}
}

func TestValidateDPMSHelperService(t *testing.T) {
	t.Parallel()

	if err := hostdisplay.ValidateDPMSHelperService(nil, "off"); err == nil {
		t.Error("expected error on nil unit")
	}

	bad := &hostdisplay.SystemdUnit{Sections: map[string]map[string]string{}}
	if err := hostdisplay.ValidateDPMSHelperService(bad, "off"); err == nil {
		t.Error("expected error on missing Service section")
	}

	bad = &hostdisplay.SystemdUnit{Sections: map[string]map[string]string{"Service": {"ExecStart": "/bin/true"}}}
	if err := hostdisplay.ValidateDPMSHelperService(bad, "off"); err == nil {
		t.Error("expected error on missing dpms.sh")
	}

	bad = &hostdisplay.SystemdUnit{Sections: map[string]map[string]string{"Service": {"ExecStart": "/opt/mirrormere/display/dpms.sh on"}}}
	if err := hostdisplay.ValidateDPMSHelperService(bad, "off"); err == nil {
		t.Error("expected error on wrong subcommand")
	}

	good := &hostdisplay.SystemdUnit{Sections: map[string]map[string]string{"Service": {"ExecStart": "/opt/mirrormere/display/dpms.sh off"}}}
	if err := hostdisplay.ValidateDPMSHelperService(good, "off"); err != nil {
		t.Errorf("unexpected error on valid dpms service: %v", err)
	}
}

func TestCheckShellScriptSyntax(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Non-existent script
	if err := hostdisplay.CheckShellScriptSyntax(filepath.Join(dir, "missing.sh")); err == nil {
		t.Error("expected error for non-existent script")
	}

	// Invalid bash syntax
	badScript := filepath.Join(dir, "bad.sh")
	if err := os.WriteFile(badScript, []byte("#!/usr/bin/env bash\nif [ a == b ]; then\n"), 0755); err != nil {
		t.Fatalf("failed to write bad script: %v", err)
	}
	if err := hostdisplay.CheckShellScriptSyntax(badScript); err == nil {
		t.Error("expected syntax error on unclosed if block")
	}

	// Valid script
	goodScript := filepath.Join(dir, "good.sh")
	if err := os.WriteFile(goodScript, []byte("#!/usr/bin/env bash\nset -euo pipefail\necho \"hello world\"\n"), 0755); err != nil {
		t.Fatalf("failed to write good script: %v", err)
	}
	if err := hostdisplay.CheckShellScriptSyntax(goodScript); err != nil {
		t.Errorf("unexpected error on valid script: %v", err)
	}
}

// TestDeployDisplayFiles performs end-to-end hermetic verification of the actual display assets in deploy/display/.
func TestDeployDisplayFiles(t *testing.T) {
	displayDir := filepath.Join("..", "..", "deploy", "display")

	scripts := []string{"launch.sh", "session.sh", "dpms.sh", "install.sh"}
	for _, s := range scripts {
		path := filepath.Join(displayDir, s)
		t.Run("ScriptSyntax/"+s, func(t *testing.T) {
			if err := hostdisplay.CheckShellScriptSyntax(path); err != nil {
				t.Fatalf("script verification failed for %s: %v", s, err)
			}
		})
	}

	t.Run("DisplayServiceUnit", func(t *testing.T) {
		unit, err := hostdisplay.ParseSystemdUnitFile(filepath.Join(displayDir, "mirrormere-display.service"))
		if err != nil {
			t.Fatalf("failed to parse mirrormere-display.service: %v", err)
		}
		if err := hostdisplay.ValidateDisplayService(unit); err != nil {
			t.Fatalf("mirrormere-display.service validation failed: %v", err)
		}
	})

	t.Run("SleepTimerAndService", func(t *testing.T) {
		timer, err := hostdisplay.ParseSystemdUnitFile(filepath.Join(displayDir, "mirrormere-display-sleep.timer"))
		if err != nil {
			t.Fatalf("failed to parse sleep.timer: %v", err)
		}
		if err := hostdisplay.ValidateTimer(timer, "*-*-* 23:00:00"); err != nil {
			t.Fatalf("sleep.timer validation failed: %v", err)
		}

		svc, err := hostdisplay.ParseSystemdUnitFile(filepath.Join(displayDir, "mirrormere-display-sleep.service"))
		if err != nil {
			t.Fatalf("failed to parse sleep.service: %v", err)
		}
		if err := hostdisplay.ValidateDPMSHelperService(svc, "off"); err != nil {
			t.Fatalf("sleep.service validation failed: %v", err)
		}
	})

	t.Run("WakeTimerAndService", func(t *testing.T) {
		timer, err := hostdisplay.ParseSystemdUnitFile(filepath.Join(displayDir, "mirrormere-display-wake.timer"))
		if err != nil {
			t.Fatalf("failed to parse wake.timer: %v", err)
		}
		if err := hostdisplay.ValidateTimer(timer, "*-*-* 06:00:00"); err != nil {
			t.Fatalf("wake.timer validation failed: %v", err)
		}

		svc, err := hostdisplay.ParseSystemdUnitFile(filepath.Join(displayDir, "mirrormere-display-wake.service"))
		if err != nil {
			t.Fatalf("failed to parse wake.service: %v", err)
		}
		if err := hostdisplay.ValidateDPMSHelperService(svc, "on"); err != nil {
			t.Fatalf("wake.service validation failed: %v", err)
		}
	})

	t.Run("RestartTimerAndService", func(t *testing.T) {
		timer, err := hostdisplay.ParseSystemdUnitFile(filepath.Join(displayDir, "mirrormere-display-restart.timer"))
		if err != nil {
			t.Fatalf("failed to parse restart.timer: %v", err)
		}
		if err := hostdisplay.ValidateTimer(timer, "*-*-* 03:00:00"); err != nil {
			t.Fatalf("restart.timer validation failed: %v", err)
		}

		svc, err := hostdisplay.ParseSystemdUnitFile(filepath.Join(displayDir, "mirrormere-display-restart.service"))
		if err != nil {
			t.Fatalf("failed to parse restart.service: %v", err)
		}
		if err := hostdisplay.ValidateDPMSHelperService(svc, "night-restart"); err != nil {
			t.Fatalf("restart.service validation failed: %v", err)
		}
	})

	t.Run("DPMSScriptDirectives", func(t *testing.T) {
		data, err := os.ReadFile(filepath.Join(displayDir, "dpms.sh"))
		if err != nil {
			t.Fatalf("failed to read dpms.sh: %v", err)
		}
		if err := hostdisplay.ValidateDPMSScript(string(data)); err != nil {
			t.Fatalf("dpms.sh validation failed: %v", err)
		}
	})

	t.Run("SessionScriptDirectives", func(t *testing.T) {
		data, err := os.ReadFile(filepath.Join(displayDir, "session.sh"))
		if err != nil {
			t.Fatalf("failed to read session.sh: %v", err)
		}
		if err := hostdisplay.ValidateSessionScript(string(data)); err != nil {
			t.Fatalf("session.sh validation failed: %v", err)
		}
	})

	t.Run("WlrootsCompositorStabilityFlags", func(t *testing.T) {
		defaultData, err := os.ReadFile(filepath.Join(displayDir, "default-mirrormere-display"))
		if err != nil {
			t.Fatalf("failed to read default-mirrormere-display: %v", err)
		}
		defaults := string(defaultData)
		if !strings.Contains(defaults, "WLR_SCENE_DISABLE_DIRECT_SCANOUT=1") {
			t.Error("expected WLR_SCENE_DISABLE_DIRECT_SCANOUT=1 in default-mirrormere-display")
		}
		if !strings.Contains(defaults, "WLR_DRM_NO_ATOMIC=1") {
			t.Error("expected WLR_DRM_NO_ATOMIC=1 in default-mirrormere-display")
		}

		launchData, err := os.ReadFile(filepath.Join(displayDir, "launch.sh"))
		if err != nil {
			t.Fatalf("failed to read launch.sh: %v", err)
		}
		launch := string(launchData)
		if !strings.Contains(launch, "export WLR_SCENE_DISABLE_DIRECT_SCANOUT=") {
			t.Error("expected export WLR_SCENE_DISABLE_DIRECT_SCANOUT in launch.sh")
		}
		if !strings.Contains(launch, "export WLR_DRM_NO_ATOMIC=") {
			t.Error("expected export WLR_DRM_NO_ATOMIC in launch.sh")
		}

		serviceData, err := os.ReadFile(filepath.Join(displayDir, "mirrormere-display.service"))
		if err != nil {
			t.Fatalf("failed to read mirrormere-display.service: %v", err)
		}
		svc := string(serviceData)
		if !strings.Contains(svc, `Environment="WLR_SCENE_DISABLE_DIRECT_SCANOUT=1"`) {
			t.Error("expected WLR_SCENE_DISABLE_DIRECT_SCANOUT=1 in mirrormere-display.service")
		}
		if !strings.Contains(svc, `Environment="WLR_DRM_NO_ATOMIC=1"`) {
			t.Error("expected WLR_DRM_NO_ATOMIC=1 in mirrormere-display.service")
		}
	})
}

func TestValidateDPMSScript(t *testing.T) {
	t.Parallel()

	valid := `
trigger_swayidle_idle
reset_swayidle_active
SIGUSR1
SIGTERM
night-restart
`
	if err := hostdisplay.ValidateDPMSScript(valid); err != nil {
		t.Fatalf("unexpected error on valid dpms script: %v", err)
	}

	bad := `
trigger_swayidle_idle
reset_swayidle_active
`
	if err := hostdisplay.ValidateDPMSScript(bad); err == nil {
		t.Error("expected error on missing directives")
	}
}

func TestValidateSessionScript(t *testing.T) {
	t.Parallel()

	valid := `
swayidle
run_swayidle
cleanup
trap cleanup
`
	if err := hostdisplay.ValidateSessionScript(valid); err != nil {
		t.Fatalf("unexpected error on valid session script: %v", err)
	}

	bad := `
swayidle
`
	if err := hostdisplay.ValidateSessionScript(bad); err == nil {
		t.Error("expected error on missing directives")
	}
}

func cloneUnit(u *hostdisplay.SystemdUnit) *hostdisplay.SystemdUnit {
	res := &hostdisplay.SystemdUnit{
		Path:     u.Path,
		Sections: make(map[string]map[string]string),
	}
	for sName, sMap := range u.Sections {
		res.Sections[sName] = make(map[string]string)
		for k, v := range sMap {
			res.Sections[sName][k] = v
		}
	}
	return res
}
