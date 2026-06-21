package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const serviceName = "SuperProjectSyncer"

func Install(exePath, configPath string) error {
	switch runtime.GOOS {
	case "windows":
		return installWindows(exePath, configPath)
	case "linux":
		return installLinux(exePath, configPath)
	default:
		return fmt.Errorf("service install not supported on %s", runtime.GOOS)
	}
}

func Uninstall() error {
	switch runtime.GOOS {
	case "windows":
		return uninstallWindows()
	case "linux":
		return uninstallLinux()
	default:
		return fmt.Errorf("service uninstall not supported on %s", runtime.GOOS)
	}
}

func installLinux(exePath, configPath string) error {
	unitPath := "/etc/systemd/system/superprojectsyncer.service"
	unit := fmt.Sprintf(`[Unit]
Description=SuperProjectSyncer
After=network.target

[Service]
Type=simple
ExecStart=%s run --config %s
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
`, exePath, configPath)

	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		return fmt.Errorf("write unit file (need sudo): %w", err)
	}
	for _, args := range [][]string{
		{"systemctl", "daemon-reload"},
		{"systemctl", "enable", "superprojectsyncer"},
		{"systemctl", "start", "superprojectsyncer"},
	} {
		out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s: %s: %w", args[0], strings.TrimSpace(string(out)), err)
		}
	}
	return nil
}

func uninstallLinux() error {
	_ = exec.Command("systemctl", "stop", "superprojectsyncer").Run()
	_ = exec.Command("systemctl", "disable", "superprojectsyncer").Run()
	_ = os.Remove("/etc/systemd/system/superprojectsyncer.service")
	out, err := exec.Command("systemctl", "daemon-reload").CombinedOutput()
	if err != nil {
		return fmt.Errorf("daemon-reload: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func ExecutablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Abs(exe)
}
