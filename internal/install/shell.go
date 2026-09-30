package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type omarchyRunner func(...string) ([]byte, error)

type shellPlugin struct {
	ID         string `json:"id"`
	Enabled    bool   `json:"enabled"`
	FirstParty bool   `json:"firstParty"`
	ClonedFrom string `json:"clonedFrom"`
}

// Shell adds history access to the existing power widget. The bool reports
// whether Omarchy's shell was detected, not whether any files changed.
func Shell(executable string) (bool, error) {
	if _, err := exec.LookPath("omarchy-shell"); err != nil {
		return false, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return true, err
	}
	root := os.Getenv("OMARCHY_PATH")
	if root == "" {
		root = "/usr/share/omarchy"
	}
	// Omarchy's shell and plugin CLI use ~/.config directly, independently of
	// XDG_CONFIG_HOME. Match their location for clones and the layout backup.
	dir := filepath.Join(home, ".config", "omarchy")
	return true, installPowerHistory(executable, dir, root, runOmarchy)
}

func runOmarchy(args ...string) ([]byte, error) {
	out, err := exec.Command("omarchy", args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("omarchy %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func activePowerPlugin(run omarchyRunner) (shellPlugin, error) {
	out, err := run("plugin", "list", "--json")
	if err != nil {
		return shellPlugin{}, err
	}
	var plugins []shellPlugin
	if err := json.Unmarshal(out, &plugins); err != nil {
		return shellPlugin{}, fmt.Errorf("read Omarchy plugins: %w", err)
	}
	var active shellPlugin
	for _, plugin := range plugins {
		if !plugin.Enabled || (plugin.ID != "omarchy.power" && plugin.ClonedFrom != "omarchy.power") {
			continue
		}
		if active.ID != "" {
			return shellPlugin{}, errors.New("multiple power widgets are enabled; cannot choose one to integrate")
		}
		active = plugin
	}
	return active, nil
}

func installPowerHistory(executable, configDir, omarchyRoot string, run omarchyRunner) error {
	plugin, err := activePowerPlugin(run)
	if err != nil {
		return err
	}
	if plugin.ID == "" {
		fmt.Println("Omarchy power widget is disabled; skipped battery history integration")
		return nil
	}
	if plugin.ID == "omarchy.power" {
		// Check compatibility before cloning changes the user's layout.
		path := filepath.Join(omarchyRoot, "shell", "plugins", "panels", "power", "Panel.qml")
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if _, err := addPowerHistory(string(content), executable); err != nil {
			return err
		}
		if err := backupConfig(filepath.Join(configDir, "shell.json")); err != nil {
			return err
		}
		if _, err := run("plugin", "clone", "omarchy.power"); err != nil {
			return err
		}
		plugin, err = activePowerPlugin(run)
		if err != nil {
			return err
		}
	}
	if plugin.FirstParty || plugin.ClonedFrom != "omarchy.power" {
		return errors.New("Omarchy did not activate a user-owned power widget clone")
	}
	path, err := powerPanelPath(configDir, plugin.ID)
	if err != nil {
		return err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	updated, err := addPowerHistory(string(content), executable)
	if err != nil {
		return err
	}
	if updated == string(content) {
		return nil
	}
	if err := backupConfig(path); err != nil {
		return err
	}
	if err := replaceConfig(path, []byte(updated)); err != nil {
		return err
	}
	fmt.Printf("Added Omabat history to the battery widget in %s\n", path)
	// Omarchy 4.0.1 can retain the old QML component after a plugin rescan.
	// Restart only when the panel changed to ensure the new actions are live.
	if _, err := run("restart", "shell"); err != nil {
		return fmt.Errorf("history installed, but shell restart failed (retry with omarchy restart shell): %w", err)
	}
	return nil
}

func powerPanelPath(configDir, id string) (string, error) {
	if !filepath.IsLocal(id) || filepath.Base(id) != id || id == "." {
		return "", fmt.Errorf("invalid power plugin id: %q", id)
	}
	dir := filepath.Join(configDir, "plugins", id)
	content, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return "", err
	}
	var manifest struct {
		ID          string            `json:"id"`
		EntryPoints map[string]string `json:"entryPoints"`
		Omarchy     struct {
			ClonedFrom string `json:"clonedFrom"`
		} `json:"omarchy"`
	}
	if err := json.Unmarshal(content, &manifest); err != nil {
		return "", err
	}
	entry := manifest.EntryPoints["barWidget"]
	if manifest.ID != id || manifest.Omarchy.ClonedFrom != "omarchy.power" || !filepath.IsLocal(entry) {
		return "", errors.New("invalid user-owned power widget manifest")
	}
	path, err := filepath.EvalSymlinks(filepath.Join(dir, entry))
	if err != nil {
		return "", err
	}
	// A symlinked plugin must not redirect an edit into the packaged source.
	rel, err := filepath.Rel(dir, path)
	if err != nil || !filepath.IsLocal(rel) {
		return "", errors.New("power widget entry point is outside its user plugin directory")
	}
	return path, nil
}

func addPowerHistory(panel, executable string) (string, error) {
	// Pass the quoted launch command as one argument to or-focus. The shorter
	// or-focus-tui helper reconstructs $@, losing executable-path quoting.
	launch := "omarchy launch tui --app-id=org.omarchy.omabat " + shellQuote(executable)
	command := "omarchy launch or-focus org.omarchy.omabat " + shellQuote(launch)
	quoted, err := json.Marshal(command)
	if err != nil {
		return "", err
	}
	blocks := []struct{ name, anchor, body string }{
		{"action", "  function togglePercentage() {", fmt.Sprintf(`  function omabatHistory() {
    root.close()
    if (root.bar) root.bar.run(%s)
  }
`, quoted)},
		{"middle-click", "      if (b === Qt.RightButton) root.togglePercentage()", `      if (b === Qt.MiddleButton) {
        root.omabatHistory()
        return
      }
`},
		{"button", "        // ---------- Power profile picker ----------", `        Button {
          width: parent.width
          text: "Battery history"
          tooltipText: "Open Omabat history and battery health"
          foreground: root.bar.foreground
          fontFamily: root.bar.fontFamily
          fontSize: Style.font.bodySmall
          bordered: true
          onClicked: root.omabatHistory()
        }
`},
	}
	for _, block := range blocks {
		panel, err = replacePowerBlock(panel, block.name, block.anchor, block.body)
		if err != nil {
			return "", err
		}
	}
	return panel, nil
}

func replacePowerBlock(panel, name, anchor, body string) (string, error) {
	indent := anchor[:len(anchor)-len(strings.TrimLeft(anchor, " "))]
	start := indent + "// omabat:" + name + ":start\n"
	end := indent + "// omabat:" + name + ":end\n"
	block := start + body + end
	starts, ends := strings.Count(panel, start), strings.Count(panel, end)
	if starts == 1 && ends == 1 {
		from, to := strings.Index(panel, start), strings.Index(panel, end)
		if to > from {
			return panel[:from] + block + panel[to+len(end):], nil
		}
	}
	// Match a whole line: the IPC handler also declares togglePercentage(),
	// but at a different nesting level and with its body on the same line.
	anchor = "\n" + anchor + "\n"
	if starts != 0 || ends != 0 || strings.Count(panel, anchor) != 1 {
		return "", fmt.Errorf("unsupported Omarchy power widget (%s); left existing files unchanged", name)
	}
	return strings.Replace(panel, anchor, "\n"+block+anchor[1:], 1), nil
}

// Preserve the original on repeated installs so users can undo the integration.
func backupConfig(path string) error {
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path+".omabat.bak", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = file.Write(content)
	closeErr := file.Close()
	return errors.Join(err, closeErr)
}

func replaceConfig(path string, content []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".omabat-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(info.Mode().Perm()); err != nil {
		file.Close()
		return err
	}
	_, err = file.Write(content)
	closeErr := file.Close()
	if err := errors.Join(err, closeErr); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
