package install

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func Waybar(executable string) (bool, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return false, err
	}
	path := filepath.Join(configDir, "waybar", "config.jsonc")
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	updated, changed, err := addWaybarModule(string(content), executable)
	if err != nil || !changed {
		return changed, err
	}
	if err := os.WriteFile(path+".omabat.bak", content, 0o644); err != nil {
		return false, err
	}
	temp := path + ".omabat.tmp"
	if err := os.WriteFile(temp, []byte(updated), 0o644); err != nil {
		return false, err
	}
	if err := os.Rename(temp, path); err != nil {
		return false, err
	}
	fmt.Printf("Added Omabat to %s\n", path)
	return true, nil
}

func RestartWaybar() error {
	if _, err := exec.LookPath("waybar"); err != nil {
		return nil
	}
	if _, err := exec.LookPath("omarchy"); err != nil {
		return nil
	}
	cmd := exec.Command("omarchy", "restart", "waybar")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func addWaybarModule(config, executable string) (string, bool, error) {
	changed := false
	if !waybarArrayHasModule(config, "modules-right", "custom/omabat") {
		var err error
		config, err = insertIntoWaybarArray(config, "modules-right", `"custom/omabat"`)
		if err != nil {
			return "", false, err
		}
		changed = true
	}
	if !strings.Contains(config, `"custom/omabat":`) {
		marker := `  "battery": {`
		index := strings.Index(config, marker)
		if index < 0 {
			return "", false, errors.New("waybar config has no battery module insertion point")
		}
		command := shellQuote(executable)
		module := fmt.Sprintf(`  "custom/omabat": {
    "exec": %s,
    "return-type": "json",
    "interval": 5,
    "tooltip": true,
    "on-click": %s
  },
`, strconv.Quote(command+" waybar"), strconv.Quote("omarchy-launch-or-focus-tui "+command))
		config = config[:index] + module + config[index:]
		changed = true
	}
	return config, changed, nil
}

func waybarArrayHasModule(config, arrayName, module string) bool {
	start, end, ok := waybarArray(config, arrayName)
	return ok && strings.Contains(config[start:end], strconv.Quote(module))
}

func insertIntoWaybarArray(config, arrayName, value string) (string, error) {
	start, _, ok := waybarArray(config, arrayName)
	if !ok {
		return "", fmt.Errorf("waybar config has no %s array", arrayName)
	}
	insertion := "\n    " + value + ","
	return config[:start+1] + insertion + config[start+1:], nil
}

func waybarArray(config, name string) (int, int, bool) {
	key := strconv.Quote(name)
	keyIndex := strings.Index(config, key)
	if keyIndex < 0 {
		return 0, 0, false
	}
	start := strings.Index(config[keyIndex+len(key):], "[")
	if start < 0 {
		return 0, 0, false
	}
	start += keyIndex + len(key)
	depth := 0
	inString := false
	escaped := false
	for index := start; index < len(config); index++ {
		switch {
		case escaped:
			escaped = false
		case config[index] == '\\' && inString:
			escaped = true
		case config[index] == '"':
			inString = !inString
		case inString:
		case config[index] == '[':
			depth++
		case config[index] == ']':
			depth--
			if depth == 0 {
				return start, index + 1, true
			}
		}
	}
	return 0, 0, false
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
