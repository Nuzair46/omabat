package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServiceDoesNotIntegrateOrRestartPanels(t *testing.T) {
	for _, panel := range []string{"omarchy", "waybar"} {
		t.Run(panel, func(t *testing.T) {
			config, bin := t.TempDir(), t.TempDir()
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", config)
			t.Setenv("PATH", bin)
			log := filepath.Join(t.TempDir(), "commands")
			t.Setenv("TEST_COMMAND_LOG", log)
			writeTestFile(t, filepath.Join(bin, "systemctl"), "#!/bin/sh\nprintf 'systemctl %s\\n' \"$*\" >>\"$TEST_COMMAND_LOG\"\n", 0o755)
			for _, command := range []string{"omarchy", "waybar"} {
				writeTestFile(t, filepath.Join(bin, command), "#!/bin/sh\nprintf 'unexpected panel command\\n' >>\"$TEST_COMMAND_LOG\"\nexit 1\n", 0o755)
			}
			if panel == "omarchy" {
				writeTestFile(t, filepath.Join(bin, "omarchy-shell"), "#!/bin/sh\nexit 0\n", 0o755)
			}
			path := filepath.Join(config, "waybar", "config.jsonc")
			original := `{
  "group/tray-expander": {"modules": ["tray", "custom/omabat"]},
  "custom/omabat": {"exec": "existing-custom-command"},
  "battery": {}
}`
			writeTestFile(t, path, original, 0o644)
			// Reinstalling must also leave an existing integration untouched.
			for i := 0; i < 2; i++ {
				if err := Service("/test/omabat"); err != nil {
					t.Fatal(err)
				}
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != original {
				t.Fatalf("panel configuration changed: %q, err=%v", got, err)
			}
			if _, err := os.Stat(path + ".omabat.bak"); !os.IsNotExist(err) {
				t.Fatalf("service install created a panel backup: %v", err)
			}
			commands, err := os.ReadFile(log)
			want := strings.Repeat("systemctl --user daemon-reload\nsystemctl --user enable --now omabat.service\n", 2)
			if err != nil || string(commands) != want {
				t.Fatalf("unexpected install commands: %q, err=%v", commands, err)
			}
			unit, err := os.ReadFile(filepath.Join(config, "systemd", "user", "omabat.service"))
			if err != nil || !strings.Contains(string(unit), "ExecStart=/test/omabat collect --daemon") {
				t.Fatalf("service was not installed: %q, err=%v", unit, err)
			}
		})
	}
}
