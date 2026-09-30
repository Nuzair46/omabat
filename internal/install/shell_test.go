package install

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The integration points from Omarchy 4.0.4's power panel. Unrelated UI is
// omitted; the upstream widget supplies the battery display and power controls.
const powerPanel = `import QtQuick
import Quickshell
import qs.Commons
import qs.Ui
Panel {
  id: root
  moduleName: "omarchy.power"
  function togglePercentage() {
    root.settings = Object.assign({}, root.settings, { showPercentage: true })
  }
  IpcHandler {
    function togglePercentage() { root.togglePercentage() }
  }
  BarIconButton {
    onPressed: function(b) {
      if (!root.batteryPresent) return
      if (b === Qt.RightButton) root.togglePercentage()
      else root.toggle()
    }
  }
  Column {
        // ---------- Power profile picker ----------
        Button { onClicked: root.setProfile("balanced") }
  }
}
`

func TestPowerHistoryPreservesControlsAndIsIdempotent(t *testing.T) {
	got, err := addPowerHistory(powerPanel, "/opt/Omabat App/omabat")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`text: "Battery history"`, `b === Qt.MiddleButton`,
		`if (b === Qt.RightButton) root.togglePercentage()`, `else root.toggle()`,
		`root.setProfile("balanced")`, `moduleName: "omarchy.power"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in integrated panel", want)
		}
	}
	again, err := addPowerHistory(got, "/opt/Omabat App/omabat")
	if err != nil || again != got {
		t.Fatalf("reinstall changed panel: %v", err)
	}
	updated, err := addPowerHistory(got, "/new/location/omabat")
	if err != nil || strings.Contains(updated, "/opt/Omabat App/omabat") || !strings.Contains(updated, "/new/location/omabat") {
		t.Fatalf("executable relocation failed: %v", err)
	}
	if strings.Count(updated, `text: "Battery history"`) != 1 {
		t.Fatal("reinstall duplicated history button")
	}
}

func TestPowerHistoryRejectsUnknownOrDamagedPanel(t *testing.T) {
	for _, panel := range []string{
		`Panel {}`,
		powerPanel + "  // omabat:action:start\n",
		strings.Replace(powerPanel, "function togglePercentage()", "function renamed()", 1),
		powerPanel + "        // ---------- Power profile picker ----------\n",
	} {
		if _, err := addPowerHistory(panel, "/bin/omabat"); err == nil {
			t.Fatal("expected unsupported widget to fail")
		}
	}
}

func TestPowerHistoryLaunchQuotesExecutable(t *testing.T) {
	dir := t.TempDir()
	// Simulate the two Omarchy launcher stages, without opening a terminal.
	writeTestFile(t, filepath.Join(dir, "omarchy"), `#!/bin/bash
case "$2" in
  or-focus) exec bash -c "$4" ;;
  tui) printf '%s' "$4" ;;
  *) exit 1 ;;
esac
`, 0o755)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	path := "/opt/Omabat's App/$(false);`false`/omabat"
	panel, err := addPowerHistory(powerPanel, path)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(panel, "\n") {
		const prefix = "    if (root.bar) root.bar.run("
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		var command string
		if err := json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(line, prefix), ")")), &command); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command("bash", "-c", command).CombinedOutput()
		if err != nil || string(out) != path {
			t.Fatalf("launch changed executable argument: %q, %v", out, err)
		}
		return
	}
	t.Fatal("missing launch command")
}

func TestInstallPowerHistoryClonesOnceAndPreservesBackups(t *testing.T) {
	config, upstream := t.TempDir(), t.TempDir()
	stock := filepath.Join(upstream, "shell", "plugins", "panels", "power", "Panel.qml")
	writeTestFile(t, stock, powerPanel, 0o644)
	const layout = `{"version":1,"bar":{"layout":{"right":[{"id":"omarchy.power","showPercentage":true}]}},"idle":{"lock":600}}`
	writeTestFile(t, filepath.Join(config, "shell.json"), layout, 0o644)
	cloned := false
	var calls []string
	run := func(args ...string) ([]byte, error) {
		command := strings.Join(args, " ")
		calls = append(calls, command)
		switch command {
		case "plugin list --json":
			if cloned {
				return []byte(`[{"id":"test.power","enabled":true,"clonedFrom":"omarchy.power"}]`), nil
			}
			return []byte(`[{"id":"omarchy.power","enabled":true,"firstParty":true}]`), nil
		case "plugin clone omarchy.power":
			cloned = true
			makePowerClone(t, config, powerPanel)
			return nil, nil
		case "restart shell":
			return nil, nil
		default:
			return nil, fmt.Errorf("unexpected command: %s", command)
		}
	}
	if err := installPowerHistory("/bin/omabat", config, upstream, run); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config, "plugins", "test.power", "Panel.qml")
	if err := installPowerHistory("/new/omabat", config, upstream, run); err != nil {
		t.Fatal(err)
	}
	if err := installPowerHistory("/new/omabat", config, upstream, run); err != nil {
		t.Fatal(err)
	}
	if strings.Count(strings.Join(calls, "\n"), "plugin clone omarchy.power") != 1 {
		t.Fatalf("unexpected clone calls: %v", calls)
	}
	if strings.Count(strings.Join(calls, "\n"), "restart shell") != 2 {
		t.Fatalf("must restart after changes but not on a no-op reinstall: %v", calls)
	}
	for file, want := range map[string]string{
		stock: powerPanel, path + ".omabat.bak": powerPanel,
		filepath.Join(config, "shell.json.omabat.bak"): layout,
	} {
		got, err := os.ReadFile(file)
		if err != nil || string(got) != want {
			t.Fatalf("original file changed: %s (%v)", file, err)
		}
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("clone permissions not preserved: %v", err)
	}
}

func TestInstallPowerHistoryLeavesUnsupportedCloneUnchanged(t *testing.T) {
	config := t.TempDir()
	makePowerClone(t, config, "// customized beyond known insertion points\n")
	err := installPowerHistory("/bin/omabat", config, t.TempDir(), func(args ...string) ([]byte, error) {
		if strings.Join(args, " ") != "plugin list --json" {
			t.Fatal("unexpected clone command")
		}
		return []byte(`[{"id":"test.power","enabled":true,"clonedFrom":"omarchy.power"}]`), nil
	})
	if err == nil {
		t.Fatal("expected unsupported clone to fail")
	}
	path := filepath.Join(config, "plugins", "test.power", "Panel.qml")
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "// customized beyond known insertion points\n" {
		t.Fatal("custom widget was overwritten")
	}
}

func TestInstallPowerHistoryPreservesExistingCustomizations(t *testing.T) {
	config := t.TempDir()
	custom := strings.Replace(powerPanel, `showPercentage: true`, `showPercentage: false`, 1)
	makePowerClone(t, config, custom)
	run := func(args ...string) ([]byte, error) {
		if strings.Join(args, " ") == "restart shell" {
			return nil, nil
		}
		if strings.Join(args, " ") != "plugin list --json" {
			t.Fatal("must reuse the existing clone")
		}
		return []byte(`[{"id":"test.power","enabled":true,"clonedFrom":"omarchy.power"}]`), nil
	}
	if err := installPowerHistory("/bin/omabat", config, t.TempDir(), run); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(config, "plugins", "test.power", "Panel.qml"))
	if err != nil || !strings.Contains(string(got), `showPercentage: false`) || !strings.Contains(string(got), `text: "Battery history"`) {
		t.Fatalf("customized clone was not extended: %v", err)
	}
}

func TestInstallPowerHistoryChecksUpstreamBeforeCloning(t *testing.T) {
	upstream := t.TempDir()
	writeTestFile(t, filepath.Join(upstream, "shell", "plugins", "panels", "power", "Panel.qml"), "Panel {}", 0o644)
	err := installPowerHistory("/bin/omabat", t.TempDir(), upstream, func(args ...string) ([]byte, error) {
		if strings.Join(args, " ") != "plugin list --json" {
			t.Fatal("must not clone an unsupported upstream panel")
		}
		return []byte(`[{"id":"omarchy.power","enabled":true,"firstParty":true}]`), nil
	})
	if err == nil {
		t.Fatal("expected unsupported upstream to fail")
	}
}

func TestDesktopDoesNotFallBackToStaleWaybarConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	writeTestFile(t, filepath.Join(dir, "omarchy-shell"), "#!/bin/sh\nexit 1\n", 0o755)
	writeTestFile(t, filepath.Join(dir, "omarchy"), "#!/bin/sh\nexit 1\n", 0o755)
	config := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "waybar", "config.jsonc")
	writeTestFile(t, config, `{"battery":{}}`, 0o644)
	if err := Desktop("/bin/omabat"); err == nil {
		t.Fatal("expected shell failure to be reported")
	}
	if _, err := os.Stat(config + ".omabat.bak"); !os.IsNotExist(err) {
		t.Fatal("stale Waybar config was modified")
	}
}

func TestDesktopSupportsWaybarWithoutShell(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	config := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "waybar", "config.jsonc")
	writeTestFile(t, config, `{
  "group/tray-expander": {"modules": ["custom/expand-icon", "tray"]},
  "battery": {}
}`, 0o644)
	if err := Desktop("/bin/omabat"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(config)
	if err != nil || !strings.Contains(string(got), `"custom/omabat"`) {
		t.Fatalf("legacy Waybar integration failed: %v", err)
	}
}

func TestInstallPowerHistorySkipsDisabledWidget(t *testing.T) {
	err := installPowerHistory("/bin/omabat", t.TempDir(), t.TempDir(), func(args ...string) ([]byte, error) {
		if strings.Join(args, " ") != "plugin list --json" {
			t.Fatal("disabled power widget should not be enabled")
		}
		return []byte(`[{"id":"omarchy.power","enabled":false,"firstParty":true}]`), nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPowerPanelPathRejectsEscapingSymlink(t *testing.T) {
	config := t.TempDir()
	makePowerClone(t, config, powerPanel)
	dir := filepath.Join(config, "plugins", "test.power")
	out := filepath.Join(t.TempDir(), "Panel.qml")
	writeTestFile(t, out, powerPanel, 0o644)
	if err := os.Symlink(out, filepath.Join(dir, "Linked.qml")); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(dir, "manifest.json"), `{"id":"test.power","omarchy":{"clonedFrom":"omarchy.power"},"entryPoints":{"barWidget":"Linked.qml"}}`, 0o644)
	if _, err := powerPanelPath(config, "test.power"); err == nil {
		t.Fatal("expected symlink outside plugin directory to fail")
	}
}

func makePowerClone(t *testing.T, config, panel string) {
	t.Helper()
	dir := filepath.Join(config, "plugins", "test.power")
	writeTestFile(t, filepath.Join(dir, "manifest.json"), `{"id":"test.power","omarchy":{"clonedFrom":"omarchy.power"},"entryPoints":{"barWidget":"Panel.qml"}}`, 0o644)
	writeTestFile(t, filepath.Join(dir, "Panel.qml"), panel, 0o640)
}

func writeTestFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}
