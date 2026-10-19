package hostdisplay_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azylman/mirrormere/internal/hostdisplay"
)

func testUnit(sec, k, v string) *hostdisplay.SystemdUnit {
	if sec == "" {
		return &hostdisplay.SystemdUnit{Sections: map[string]map[string]string{}}
	}
	u := &hostdisplay.SystemdUnit{Sections: map[string]map[string]string{
		"Unit":    {"Conflicts": "getty@tty1.service"},
		"Service": {"User": "display", "Group": "display", "SupplementaryGroups": "video input audio render", "PAMName": "login", "TTYPath": "/dev/tty1", "Restart": "always", "ExecStart": "launch.sh"},
	}}
	if k == "" {
		delete(u.Sections, sec)
	} else if v == "" {
		delete(u.Sections[sec], k)
	} else if u.Sections[sec] != nil {
		u.Sections[sec][k] = v
	}
	return u
}

func TestParseSystemdUnit(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, content, wantDesc string; wantErr bool }{
		{"valid", "[Unit]\nDescription=Test\n[Service]\nRestart=always\n", "Test", false},
		{"empty_header", "[]\nFoo=Bar", "", true},
		{"directive_before_header", "Foo=Bar\n[Unit]\nDesc=Test", "", true},
		{"malformed_directive", "[Unit]\nBadLine", "", true},
		{"token_too_long", strings.Repeat("x", 70000), "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			u, err := hostdisplay.ParseSystemdUnit(tc.content)
			if (err != nil) != tc.wantErr || (!tc.wantErr && u.Sections["Unit"]["Description"] != tc.wantDesc) {
				t.Fatalf("%s failed", tc.name)
			}
		})
	}
}

func TestParseSystemdUnitFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	uPath, cPath := filepath.Join(dir, "t.service"), filepath.Join(dir, "c.service")
	_ = os.WriteFile(uPath, []byte("[Unit]\nDescription=Sample\n"), 0644)
	_ = os.WriteFile(cPath, []byte("x=1"), 0644)
	for _, tc := range []struct{ name, path string; wantErr bool }{
		{"valid", uPath, false},
		{"missing", filepath.Join(dir, "m.service"), true},
		{"corrupt", cPath, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			u, err := hostdisplay.ParseSystemdUnitFile(tc.path)
			if (err != nil) != tc.wantErr || (!tc.wantErr && u.Path != tc.path) {
				t.Fatalf("%s failed", tc.name)
			}
		})
	}
}

func TestValidateDisplayService(t *testing.T) {
	t.Parallel()
	kiosk := testUnit("Service", "User", "kiosk")
	kiosk.Sections["Service"]["Group"] = "kiosk"
	for _, tc := range []struct{ name string; unit *hostdisplay.SystemdUnit; want bool }{
		{"nil", nil, true},
		{"valid_display", testUnit("ok", "", ""), false},
		{"valid_kiosk", kiosk, false},
		{"missing_unit", testUnit("Unit", "", ""), true},
		{"missing_conflicts", testUnit("Unit", "Conflicts", ""), true},
		{"missing_service", testUnit("Service", "", ""), true},
		{"wrong_user", testUnit("Service", "User", "root"), true},
		{"wrong_group", testUnit("Service", "Group", "root"), true},
		{"missing_groups", testUnit("Service", "SupplementaryGroups", "video"), true},
		{"wrong_pam", testUnit("Service", "PAMName", "other"), true},
		{"wrong_tty", testUnit("Service", "TTYPath", "/dev/tty2"), true},
		{"wrong_restart", testUnit("Service", "Restart", "on-failure"), true},
		{"wrong_execstart", testUnit("Service", "ExecStart", "bad.sh"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if (hostdisplay.ValidateDisplayService(tc.unit) != nil) != tc.want || (!tc.want && hostdisplay.ValidateKioskService(tc.unit) != nil) {
				t.Fatalf("%s failed", tc.name)
			}
		})
	}
}

func TestValidateTimer(t *testing.T) {
	t.Parallel()
	good := &hostdisplay.SystemdUnit{Sections: map[string]map[string]string{"Timer": {"OnCalendar": "*-*-* 23:00:00", "Persistent": "true"}}}
	bad := &hostdisplay.SystemdUnit{Sections: map[string]map[string]string{"Timer": {"OnCalendar": "*-*-* 23:00:00", "Persistent": "false"}}}
	for _, tc := range []struct{ name, cal string; unit *hostdisplay.SystemdUnit; want bool }{
		{"nil", "*-*-* 23:00:00", nil, true},
		{"missing", "*-*-* 23:00:00", testUnit("", "", ""), true},
		{"mismatch", "*-*-* 12:00:00", good, true},
		{"not_persist", "*-*-* 23:00:00", bad, true},
		{"valid", "*-*-* 23:00:00", good, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if (hostdisplay.ValidateTimer(tc.unit, tc.cal) != nil) != tc.want {
				t.Fatalf("%s failed", tc.name)
			}
		})
	}
}

func TestValidateDPMSHelperService(t *testing.T) {
	t.Parallel()
	good := &hostdisplay.SystemdUnit{Sections: map[string]map[string]string{"Service": {"ExecStart": "dpms.sh off"}}}
	bad := &hostdisplay.SystemdUnit{Sections: map[string]map[string]string{"Service": {"ExecStart": "true"}}}
	for _, tc := range []struct{ name, sub string; unit *hostdisplay.SystemdUnit; want bool }{
		{"nil", "off", nil, true},
		{"missing", "off", testUnit("", "", ""), true},
		{"no_dpms", "off", bad, true},
		{"wrong_sub", "on", good, true},
		{"valid", "off", good, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if (hostdisplay.ValidateDPMSHelperService(tc.unit, tc.sub) != nil) != tc.want {
				t.Fatalf("%s failed", tc.name)
			}
		})
	}
}

func TestCheckShellScriptSyntax(t *testing.T) {
	t.Parallel()
	bad := filepath.Join(t.TempDir(), "b.sh")
	_ = os.WriteFile(bad, []byte("if ["), 0755)
	for _, tc := range []struct{ name, path string; want bool }{
		{"missing", filepath.Join(t.TempDir(), "m.sh"), true},
		{"bad", bad, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if (hostdisplay.CheckShellScriptSyntax(tc.path) != nil) != tc.want {
				t.Fatalf("%s failed", tc.name)
			}
		})
	}
}

func TestDeployDisplayFiles(t *testing.T) {
	dir := filepath.Join("..", "..", "deploy", "display")
	for _, s := range []string{"launch.sh", "session.sh", "dpms.sh", "install.sh"} {
		if err := hostdisplay.CheckShellScriptSyntax(filepath.Join(dir, s)); err != nil {
			t.Fatalf("syntax failed for %s: %v", s, err)
		}
	}
	u, _ := hostdisplay.ParseSystemdUnitFile(filepath.Join(dir, "mirrormere-display.service"))
	if err := hostdisplay.ValidateDisplayService(u); err != nil {
		t.Fatalf("validation failed: %v", err)
	}
	for _, tc := range []struct{ prefix, cal, sub string }{
		{"mirrormere-display-sleep", "*-*-* 23:00:00", "off"},
		{"mirrormere-display-wake", "*-*-* 06:00:00", "on"},
		{"mirrormere-display-restart", "*-*-* 03:00:00", "night-restart"},
	} {
		tm, _ := hostdisplay.ParseSystemdUnitFile(filepath.Join(dir, tc.prefix+".timer"))
		sv, _ := hostdisplay.ParseSystemdUnitFile(filepath.Join(dir, tc.prefix+".service"))
		if hostdisplay.ValidateTimer(tm, tc.cal) != nil || hostdisplay.ValidateDPMSHelperService(sv, tc.sub) != nil {
			t.Fatalf("timer/svc failed for %s", tc.prefix)
		}
	}
	readF := func(f string) string { b, _ := os.ReadFile(filepath.Join(dir, f)); return string(b) }
	if hostdisplay.ValidateDPMSScript(readF("dpms.sh")) != nil || hostdisplay.ValidateSessionScript(readF("session.sh")) != nil {
		t.Error("script validation failed")
	}
	for _, f := range []string{"default-mirrormere-display", "launch.sh", "mirrormere-display.service"} {
		if s := readF(f); !strings.Contains(s, "WLR_SCENE_DISABLE_DIRECT_SCANOUT") || !strings.Contains(s, "WLR_DRM_NO_ATOMIC") {
			t.Errorf("missing flags in %s", f)
		}
	}
}

func TestValidateDPMSScript(t *testing.T) {
	t.Parallel()
	if hostdisplay.ValidateDPMSScript("trigger_swayidle_idle\nreset_swayidle_active\nSIGUSR1\nSIGTERM\nnight-restart\n") != nil ||
		hostdisplay.ValidateDPMSScript("x") == nil ||
		hostdisplay.ValidateSessionScript("swayidle\nrun_swayidle\ncleanup\ntrap cleanup\n") != nil ||
		hostdisplay.ValidateSessionScript("x") == nil {
		t.Fatal("ValidateDPMSScript or ValidateSessionScript failed")
	}
}
