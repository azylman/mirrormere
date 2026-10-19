package hostdisplay

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type SystemdUnit struct {
	Path     string
	Sections map[string]map[string]string
}

func checkSection(unit *SystemdUnit, name string) (map[string]string, error) {
	if unit == nil {
		return nil, fmt.Errorf("unit is nil")
	}
	if s := unit.Sections[name]; s != nil {
		return s, nil
	}
	return nil, fmt.Errorf("missing [%s] section", name)
}

func ParseSystemdUnit(content string) (*SystemdUnit, error) {
	unit := &SystemdUnit{Sections: make(map[string]map[string]string)}
	scanner, currentSection := bufio.NewScanner(strings.NewReader(content)), ""
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentSection = strings.TrimSpace(line[1 : len(line)-1])
			if currentSection == "" {
				return nil, fmt.Errorf("empty section header")
			}
			if unit.Sections[currentSection] == nil {
				unit.Sections[currentSection] = make(map[string]string)
			}
			continue
		}
		if currentSection == "" {
			return nil, fmt.Errorf("directive before any section header: %s", line)
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("malformed directive in section [%s]: %s", currentSection, line)
		}
		unit.Sections[currentSection][strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed reading unit content: %w", err)
	}
	return unit, nil
}

func ParseSystemdUnitFile(filePath string) (*SystemdUnit, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read unit file %s: %w", filePath, err)
	}
	unit, err := ParseSystemdUnit(string(data))
	if err != nil {
		return nil, fmt.Errorf("failed to parse unit file %s: %w", filePath, err)
	}
	unit.Path = filePath
	return unit, nil
}

func ValidateDisplayService(unit *SystemdUnit) error {
	u, err := checkSection(unit, "Unit")
	if err != nil {
		return err
	}
	if u["Conflicts"] != "getty@tty1.service" {
		return fmt.Errorf("display unit must declare Conflicts=getty@tty1.service, got %q", u["Conflicts"])
	}
	s, err := checkSection(unit, "Service")
	if err != nil {
		return err
	}
	for _, c := range []struct{ k, v string }{{"User", s["User"]}, {"Group", s["Group"]}} {
		if c.v != "display" && c.v != "kiosk" {
			return fmt.Errorf("expected %s=display or kiosk, got %q", c.k, c.v)
		}
	}
	for _, req := range []string{"video", "input", "audio", "render"} {
		if !strings.Contains(s["SupplementaryGroups"], req) {
			return fmt.Errorf("missing required supplementary group %q in %q", req, s["SupplementaryGroups"])
		}
	}
	for _, c := range []struct{ k, w string }{{"PAMName", "login"}, {"TTYPath", "/dev/tty1"}, {"Restart", "always"}} {
		if s[c.k] != c.w {
			return fmt.Errorf("expected %s=%s, got %q", c.k, c.w, s[c.k])
		}
	}
	if !strings.HasSuffix(s["ExecStart"], "launch.sh") {
		return fmt.Errorf("ExecStart must invoke launch.sh, got %q", s["ExecStart"])
	}
	return nil
}

func ValidateKioskService(unit *SystemdUnit) error { return ValidateDisplayService(unit) }

func ValidateTimer(unit *SystemdUnit, expectedCalendar string) error {
	t, err := checkSection(unit, "Timer")
	if err != nil {
		return err
	}
	if t["OnCalendar"] != expectedCalendar {
		return fmt.Errorf("expected OnCalendar=%q, got %q", expectedCalendar, t["OnCalendar"])
	}
	if t["Persistent"] != "true" {
		return fmt.Errorf("expected Persistent=true, got %q", t["Persistent"])
	}
	return nil
}

func ValidateDPMSHelperService(unit *SystemdUnit, expectedSubcmd string) error {
	s, err := checkSection(unit, "Service")
	if err != nil {
		return err
	}
	for _, c := range []struct{ sub, err string }{{"dpms.sh", "call dpms.sh"}, {expectedSubcmd, fmt.Sprintf("include subcommand %q", expectedSubcmd)}} {
		if !strings.Contains(s["ExecStart"], c.sub) {
			return fmt.Errorf("expected ExecStart to %s, got %q", c.err, s["ExecStart"])
		}
	}
	return nil
}

func CheckShellScriptSyntax(scriptPath string) error {
	if _, err := os.Stat(scriptPath); err != nil {
		return fmt.Errorf("script does not exist: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if out, err := exec.CommandContext(ctx, "bash", "-n", scriptPath).CombinedOutput(); err != nil {
		return fmt.Errorf("bash syntax validation failed for %s: %s (%w)", filepath.Base(scriptPath), string(out), err)
	}
	if scPath, err := exec.LookPath("shellcheck"); err == nil && scPath != "" {
		if out, scErr := exec.CommandContext(ctx, scPath, scriptPath).CombinedOutput(); scErr != nil {
			return fmt.Errorf("shellcheck failed for %s: %s (%w)", filepath.Base(scriptPath), string(out), scErr)
		}
	}
	return nil
}

func requireDirectives(label, content string, required ...string) error {
	for _, req := range required {
		if !strings.Contains(content, req) {
			return fmt.Errorf("%s %q", label, req)
		}
	}
	return nil
}

func ValidateDPMSScript(content string) error {
	return requireDirectives("dpms.sh missing required coordination directive", content,
		"trigger_swayidle_idle", "reset_swayidle_active", "SIGUSR1", "SIGTERM", "night-restart")
}

func ValidateSessionScript(content string) error {
	return requireDirectives("session.sh missing required supervisor directive", content,
		"swayidle", "run_swayidle", "cleanup", "trap cleanup")
}
