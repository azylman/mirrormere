package kiosk

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SystemdUnit represents a parsed systemd configuration file.
type SystemdUnit struct {
	Path     string
	Sections map[string]map[string]string
}

// ParseSystemdUnit parses the contents of a systemd unit file (INI format).
func ParseSystemdUnit(content string) (*SystemdUnit, error) {
	unit := &SystemdUnit{
		Sections: make(map[string]map[string]string),
	}

	scanner := bufio.NewScanner(strings.NewReader(content))
	currentSection := ""

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
			if _, exists := unit.Sections[currentSection]; !exists {
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

		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		unit.Sections[currentSection][key] = val
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed reading unit content: %w", err)
	}

	return unit, nil
}

// ParseSystemdUnitFile reads and parses a systemd unit from disk.
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

// ValidateKioskService checks that the main kiosk service adheres to SPEC-010 requirements.
func ValidateKioskService(unit *SystemdUnit) error {
	if unit == nil {
		return fmt.Errorf("unit is nil")
	}

	unitSec := unit.Sections["Unit"]
	if unitSec == nil {
		return fmt.Errorf("missing [Unit] section")
	}
	if unitSec["Conflicts"] != "getty@tty1.service" {
		return fmt.Errorf("kiosk unit must declare Conflicts=getty@tty1.service, got %q", unitSec["Conflicts"])
	}

	svcSec := unit.Sections["Service"]
	if svcSec == nil {
		return fmt.Errorf("missing [Service] section")
	}

	if svcSec["User"] != "kiosk" {
		return fmt.Errorf("expected User=kiosk, got %q", svcSec["User"])
	}
	if svcSec["Group"] != "kiosk" {
		return fmt.Errorf("expected Group=kiosk, got %q", svcSec["Group"])
	}

	supp := svcSec["SupplementaryGroups"]
	requiredGroups := []string{"video", "input", "audio", "render"}
	for _, req := range requiredGroups {
		if !strings.Contains(supp, req) {
			return fmt.Errorf("missing required supplementary group %q in %q", req, supp)
		}
	}

	if svcSec["PAMName"] != "login" {
		return fmt.Errorf("expected PAMName=login, got %q", svcSec["PAMName"])
	}
	if svcSec["TTYPath"] != "/dev/tty1" {
		return fmt.Errorf("expected TTYPath=/dev/tty1, got %q", svcSec["TTYPath"])
	}
	if svcSec["Restart"] != "always" {
		return fmt.Errorf("expected Restart=always, got %q", svcSec["Restart"])
	}
	if !strings.HasSuffix(svcSec["ExecStart"], "launch.sh") {
		return fmt.Errorf("ExecStart must invoke launch.sh, got %q", svcSec["ExecStart"])
	}

	return nil
}

// ValidateTimer checks that a systemd timer unit declares a valid OnCalendar pattern and persistence.
func ValidateTimer(unit *SystemdUnit, expectedCalendar string) error {
	if unit == nil {
		return fmt.Errorf("unit is nil")
	}

	timerSec := unit.Sections["Timer"]
	if timerSec == nil {
		return fmt.Errorf("missing [Timer] section")
	}

	cal := timerSec["OnCalendar"]
	if cal != expectedCalendar {
		return fmt.Errorf("expected OnCalendar=%q, got %q", expectedCalendar, cal)
	}

	if timerSec["Persistent"] != "true" {
		return fmt.Errorf("expected Persistent=true, got %q", timerSec["Persistent"])
	}

	return nil
}

// ValidateDPMSHelperService checks that a service unit executes dpms.sh with the expected subcommand.
func ValidateDPMSHelperService(unit *SystemdUnit, expectedSubcmd string) error {
	if unit == nil {
		return fmt.Errorf("unit is nil")
	}

	svcSec := unit.Sections["Service"]
	if svcSec == nil {
		return fmt.Errorf("missing [Service] section")
	}

	execStart := svcSec["ExecStart"]
	if !strings.Contains(execStart, "dpms.sh") {
		return fmt.Errorf("expected ExecStart to call dpms.sh, got %q", execStart)
	}
	if !strings.Contains(execStart, expectedSubcmd) {
		return fmt.Errorf("expected ExecStart to include subcommand %q, got %q", expectedSubcmd, execStart)
	}

	return nil
}

// CheckShellScriptSyntax runs bash -n and optionally shellcheck on a shell script.
func CheckShellScriptSyntax(scriptPath string) error {
	if _, err := os.Stat(scriptPath); err != nil {
		return fmt.Errorf("script does not exist: %w", err)
	}

	// 1. Check syntax via bash -n
	cmd := exec.Command("bash", "-n", scriptPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("bash syntax validation failed for %s: %s (%w)", filepath.Base(scriptPath), string(out), err)
	}

	// 2. Run shellcheck if installed on the host
	if shellcheckPath, err := exec.LookPath("shellcheck"); err == nil && shellcheckPath != "" {
		scCmd := exec.Command(shellcheckPath, scriptPath)
		if out, scErr := scCmd.CombinedOutput(); scErr != nil {
			return fmt.Errorf("shellcheck failed for %s: %s (%w)", filepath.Base(scriptPath), string(out), scErr)
		}
	}

	return nil
}

// ValidateDPMSScript verifies that dpms.sh implements swayidle idle and reset coordination.
func ValidateDPMSScript(content string) error {
	required := []string{
		"trigger_swayidle_idle",
		"reset_swayidle_active",
		"SIGUSR1",
		"SIGTERM",
		"night-restart",
	}
	for _, req := range required {
		if !strings.Contains(content, req) {
			return fmt.Errorf("dpms.sh missing required coordination directive %q", req)
		}
	}
	return nil
}

// ValidateSessionScript verifies that session.sh implements swayidle process supervision and signal cleanup.
func ValidateSessionScript(content string) error {
	required := []string{
		"swayidle",
		"run_swayidle",
		"cleanup",
		"trap cleanup",
	}
	for _, req := range required {
		if !strings.Contains(content, req) {
			return fmt.Errorf("session.sh missing required supervisor directive %q", req)
		}
	}
	return nil
}

