package kiosk_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azylman/mirrormere/internal/kiosk"
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
	unit, err := kiosk.ParseSystemdUnit(valid)
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
	_, err = kiosk.ParseSystemdUnit("[]\nFoo=Bar")
	if err == nil {
		t.Error("expected error for empty section header, got nil")
	}

	// Directive before section header error
	_, err = kiosk.ParseSystemdUnit("Foo=Bar\n[Unit]\nDesc=Test")
	if err == nil {
		t.Error("expected error for directive before section header, got nil")
	}

	// Malformed directive (missing =)
	_, err = kiosk.ParseSystemdUnit("[Unit]\nMalformedLineWithoutEquals")
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

	unit, err := kiosk.ParseSystemdUnitFile(unitPath)
	if err != nil {
		t.Fatalf("unexpected error parsing unit file: %v", err)
	}
	if unit.Path != unitPath {
		t.Errorf("expected Path=%s, got %s", unitPath, unit.Path)
	}

	// Non-existent file error
	_, err = kiosk.ParseSystemdUnitFile(filepath.Join(dir, "non-existent.service"))
	if err == nil {
		t.Error("expected error for non-existent file, got nil")
	}

	// Corrupt content file error
	corruptPath := filepath.Join(dir, "corrupt.service")
	if err := os.WriteFile(corruptPath, []byte("DirectiveBeforeSection=1"), 0644); err != nil {
		t.Fatalf("failed to write corrupt unit file: %v", err)
	}
	_, err = kiosk.ParseSystemdUnitFile(corruptPath)
	if err == nil {
		t.Error("expected error parsing corrupt unit file, got nil")
	}
}

func TestValidateKioskService(t *testing.T) {
	t.Parallel()

	// Nil unit
	if err := kiosk.ValidateKioskService(nil); err == nil {
		t.Error("expected error on nil unit, got nil")
	}

	validUnit := &kiosk.SystemdUnit{
		Sections: map[string]map[string]string{
			"Unit": {
				"Description": "Mirrormere Wayland Touch Kiosk",
				"Conflicts":   "getty@tty1.service",
			},
			"Service": {
				"User":                "kiosk",
				"Group":               "kiosk",
				"SupplementaryGroups": "video input audio render",
				"PAMName":             "login",
				"TTYPath":             "/dev/tty1",
				"Restart":             "always",
				"ExecStart":           "/opt/mirrormere/kiosk/launch.sh",
			},
		},
	}

	if err := kiosk.ValidateKioskService(validUnit); err != nil {
		t.Fatalf("unexpected error on valid kiosk unit: %v", err)
	}

	// Missing Unit section
	bad := &kiosk.SystemdUnit{Sections: map[string]map[string]string{}}
	if err := kiosk.ValidateKioskService(bad); err == nil {
		t.Error("expected error on missing Unit section")
	}

	// Missing Conflicts
	bad = &kiosk.SystemdUnit{Sections: map[string]map[string]string{"Unit": {}}}
	if err := kiosk.ValidateKioskService(bad); err == nil {
		t.Error("expected error on missing Conflicts")
	}

	// Missing Service section
	bad = &kiosk.SystemdUnit{Sections: map[string]map[string]string{"Unit": {"Conflicts": "getty@tty1.service"}}}
	if err := kiosk.ValidateKioskService(bad); err == nil {
		t.Error("expected error on missing Service section")
	}

	// Wrong User
	bad = cloneUnit(validUnit)
	bad.Sections["Service"]["User"] = "root"
	if err := kiosk.ValidateKioskService(bad); err == nil {
		t.Error("expected error on User=root")
	}

	// Wrong Group
	bad = cloneUnit(validUnit)
	bad.Sections["Service"]["Group"] = "root"
	if err := kiosk.ValidateKioskService(bad); err == nil {
		t.Error("expected error on Group=root")
	}

	// Missing SupplementaryGroups
	bad = cloneUnit(validUnit)
	bad.Sections["Service"]["SupplementaryGroups"] = "video input"
	if err := kiosk.ValidateKioskService(bad); err == nil {
		t.Error("expected error on missing audio/render groups")
	}

	// Wrong PAMName
	bad = cloneUnit(validUnit)
	bad.Sections["Service"]["PAMName"] = "other"
	if err := kiosk.ValidateKioskService(bad); err == nil {
		t.Error("expected error on PAMName=other")
	}

	// Wrong TTYPath
	bad = cloneUnit(validUnit)
	bad.Sections["Service"]["TTYPath"] = "/dev/tty2"
	if err := kiosk.ValidateKioskService(bad); err == nil {
		t.Error("expected error on TTYPath=/dev/tty2")
	}

	// Wrong Restart
	bad = cloneUnit(validUnit)
	bad.Sections["Service"]["Restart"] = "on-failure"
	if err := kiosk.ValidateKioskService(bad); err == nil {
		t.Error("expected error on Restart=on-failure")
	}

	// Wrong ExecStart
	bad = cloneUnit(validUnit)
	bad.Sections["Service"]["ExecStart"] = "/opt/mirrormere/kiosk/wrong.sh"
	if err := kiosk.ValidateKioskService(bad); err == nil {
		t.Error("expected error on non-launch.sh ExecStart")
	}
}

func TestValidateTimer(t *testing.T) {
	t.Parallel()

	if err := kiosk.ValidateTimer(nil, "*-*-* 23:00:00"); err == nil {
		t.Error("expected error on nil unit")
	}

	bad := &kiosk.SystemdUnit{Sections: map[string]map[string]string{}}
	if err := kiosk.ValidateTimer(bad, "*-*-* 23:00:00"); err == nil {
		t.Error("expected error on missing Timer section")
	}

	bad = &kiosk.SystemdUnit{Sections: map[string]map[string]string{"Timer": {"OnCalendar": "*-*-* 12:00:00", "Persistent": "true"}}}
	if err := kiosk.ValidateTimer(bad, "*-*-* 23:00:00"); err == nil {
		t.Error("expected error on mismatched OnCalendar")
	}

	bad = &kiosk.SystemdUnit{Sections: map[string]map[string]string{"Timer": {"OnCalendar": "*-*-* 23:00:00", "Persistent": "false"}}}
	if err := kiosk.ValidateTimer(bad, "*-*-* 23:00:00"); err == nil {
		t.Error("expected error on Persistent=false")
	}

	good := &kiosk.SystemdUnit{Sections: map[string]map[string]string{"Timer": {"OnCalendar": "*-*-* 23:00:00", "Persistent": "true"}}}
	if err := kiosk.ValidateTimer(good, "*-*-* 23:00:00"); err != nil {
		t.Errorf("unexpected error on valid timer: %v", err)
	}
}

func TestValidateDPMSHelperService(t *testing.T) {
	t.Parallel()

	if err := kiosk.ValidateDPMSHelperService(nil, "off"); err == nil {
		t.Error("expected error on nil unit")
	}

	bad := &kiosk.SystemdUnit{Sections: map[string]map[string]string{}}
	if err := kiosk.ValidateDPMSHelperService(bad, "off"); err == nil {
		t.Error("expected error on missing Service section")
	}

	bad = &kiosk.SystemdUnit{Sections: map[string]map[string]string{"Service": {"ExecStart": "/bin/true"}}}
	if err := kiosk.ValidateDPMSHelperService(bad, "off"); err == nil {
		t.Error("expected error on missing dpms.sh")
	}

	bad = &kiosk.SystemdUnit{Sections: map[string]map[string]string{"Service": {"ExecStart": "/opt/mirrormere/kiosk/dpms.sh on"}}}
	if err := kiosk.ValidateDPMSHelperService(bad, "off"); err == nil {
		t.Error("expected error on wrong subcommand")
	}

	good := &kiosk.SystemdUnit{Sections: map[string]map[string]string{"Service": {"ExecStart": "/opt/mirrormere/kiosk/dpms.sh off"}}}
	if err := kiosk.ValidateDPMSHelperService(good, "off"); err != nil {
		t.Errorf("unexpected error on valid dpms service: %v", err)
	}
}

func TestCheckShellScriptSyntax(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Non-existent script
	if err := kiosk.CheckShellScriptSyntax(filepath.Join(dir, "missing.sh")); err == nil {
		t.Error("expected error for non-existent script")
	}

	// Invalid bash syntax
	badScript := filepath.Join(dir, "bad.sh")
	if err := os.WriteFile(badScript, []byte("#!/usr/bin/env bash\nif [ a == b ]; then\n"), 0755); err != nil {
		t.Fatalf("failed to write bad script: %v", err)
	}
	if err := kiosk.CheckShellScriptSyntax(badScript); err == nil {
		t.Error("expected syntax error on unclosed if block")
	}

	// Valid script
	goodScript := filepath.Join(dir, "good.sh")
	if err := os.WriteFile(goodScript, []byte("#!/usr/bin/env bash\nset -euo pipefail\necho \"hello world\"\n"), 0755); err != nil {
		t.Fatalf("failed to write good script: %v", err)
	}
	if err := kiosk.CheckShellScriptSyntax(goodScript); err != nil {
		t.Errorf("unexpected error on valid script: %v", err)
	}
}

// TestDeployKioskFiles performs end-to-end hermetic verification of the actual kiosk assets in deploy/kiosk/.
func TestDeployKioskFiles(t *testing.T) {
	kioskDir := filepath.Join("..", "..", "deploy", "kiosk")

	scripts := []string{"launch.sh", "session.sh", "dpms.sh", "install.sh"}
	for _, s := range scripts {
		path := filepath.Join(kioskDir, s)
		t.Run("ScriptSyntax/"+s, func(t *testing.T) {
			if err := kiosk.CheckShellScriptSyntax(path); err != nil {
				t.Fatalf("script verification failed for %s: %v", s, err)
			}
		})
	}

	t.Run("KioskServiceUnit", func(t *testing.T) {
		unit, err := kiosk.ParseSystemdUnitFile(filepath.Join(kioskDir, "mirrormere-kiosk.service"))
		if err != nil {
			t.Fatalf("failed to parse mirrormere-kiosk.service: %v", err)
		}
		if err := kiosk.ValidateKioskService(unit); err != nil {
			t.Fatalf("mirrormere-kiosk.service validation failed: %v", err)
		}
	})

	t.Run("SleepTimerAndService", func(t *testing.T) {
		timer, err := kiosk.ParseSystemdUnitFile(filepath.Join(kioskDir, "mirrormere-kiosk-sleep.timer"))
		if err != nil {
			t.Fatalf("failed to parse sleep.timer: %v", err)
		}
		if err := kiosk.ValidateTimer(timer, "*-*-* 23:00:00"); err != nil {
			t.Fatalf("sleep.timer validation failed: %v", err)
		}

		svc, err := kiosk.ParseSystemdUnitFile(filepath.Join(kioskDir, "mirrormere-kiosk-sleep.service"))
		if err != nil {
			t.Fatalf("failed to parse sleep.service: %v", err)
		}
		if err := kiosk.ValidateDPMSHelperService(svc, "off"); err != nil {
			t.Fatalf("sleep.service validation failed: %v", err)
		}
	})

	t.Run("WakeTimerAndService", func(t *testing.T) {
		timer, err := kiosk.ParseSystemdUnitFile(filepath.Join(kioskDir, "mirrormere-kiosk-wake.timer"))
		if err != nil {
			t.Fatalf("failed to parse wake.timer: %v", err)
		}
		if err := kiosk.ValidateTimer(timer, "*-*-* 06:00:00"); err != nil {
			t.Fatalf("wake.timer validation failed: %v", err)
		}

		svc, err := kiosk.ParseSystemdUnitFile(filepath.Join(kioskDir, "mirrormere-kiosk-wake.service"))
		if err != nil {
			t.Fatalf("failed to parse wake.service: %v", err)
		}
		if err := kiosk.ValidateDPMSHelperService(svc, "on"); err != nil {
			t.Fatalf("wake.service validation failed: %v", err)
		}
	})

	t.Run("RestartTimerAndService", func(t *testing.T) {
		timer, err := kiosk.ParseSystemdUnitFile(filepath.Join(kioskDir, "mirrormere-kiosk-restart.timer"))
		if err != nil {
			t.Fatalf("failed to parse restart.timer: %v", err)
		}
		if err := kiosk.ValidateTimer(timer, "*-*-* 03:00:00"); err != nil {
			t.Fatalf("restart.timer validation failed: %v", err)
		}

		svc, err := kiosk.ParseSystemdUnitFile(filepath.Join(kioskDir, "mirrormere-kiosk-restart.service"))
		if err != nil {
			t.Fatalf("failed to parse restart.service: %v", err)
		}
		if err := kiosk.ValidateDPMSHelperService(svc, "night-restart"); err != nil {
			t.Fatalf("restart.service validation failed: %v", err)
		}
	})
}

func cloneUnit(u *kiosk.SystemdUnit) *kiosk.SystemdUnit {
	res := &kiosk.SystemdUnit{
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
