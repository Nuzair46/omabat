package install

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func Service(executable string) error {
	config, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(config, "systemd", "user")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	unit := fmt.Sprintf(`[Unit]
Description=Omabat battery history collector
After=dbus.service

[Service]
Type=simple
ExecStart=%s collect --daemon
Restart=on-failure
RestartSec=10

[Install]
WantedBy=default.target
`, systemdEscape(executable))
	path := filepath.Join(dir, "omabat.service")
	if err := os.WriteFile(path, []byte(unit), 0o644); err != nil {
		return err
	}
	for _, args := range [][]string{{"--user", "daemon-reload"}, {"--user", "enable", "--now", "omabat.service"}} {
		cmd := exec.Command("systemctl", args...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("systemctl %v: %w", args, err)
		}
	}
	if err := Desktop(executable); err != nil {
		fmt.Fprintf(os.Stderr, "Desktop integration unavailable: %v\n", err)
	}
	fmt.Printf("Installed and started %s\n", path)
	return nil
}

// Desktop prefers Omarchy's shell, including when an old Waybar config remains
// after an Omarchy upgrade. Bar integration is optional for the collector.
func Desktop(executable string) error {
	if handled, err := Shell(executable); handled || err != nil {
		return err
	}
	changed, err := Waybar(executable)
	if err != nil || !changed {
		return err
	}
	return RestartWaybar()
}

func systemdEscape(path string) string {
	out := ""
	for _, r := range path {
		if r == ' ' || r == '\\' || r == '"' {
			out += `\` + string(r)
		} else {
			out += string(r)
		}
	}
	return out
}
