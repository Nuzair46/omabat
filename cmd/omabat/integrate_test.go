package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIntegrateChangesPanelWithoutInstallingService(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("PATH", t.TempDir()) // No real panel or service commands can run.
	path := filepath.Join(config, "waybar", "config.jsonc")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{
  "group/tray-expander": {"modules": ["tray"]},
  "battery": {}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"integrate"}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(got), `"custom/omabat"`) {
		t.Fatalf("panel was not integrated: %q, err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(config, "systemd")); !os.IsNotExist(err) {
		t.Fatalf("integration unexpectedly created service configuration: %v", err)
	}
}
