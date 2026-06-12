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
	fmt.Printf("Added Omabat to the Waybar tray in %s\n", path)
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

	if waybarArrayHasModule(config, "modules-right", "custom/omabat") {
		var removed bool
		config, removed = removeFromWaybarArray(config, "modules-right", "custom/omabat")
		changed = changed || removed
	}

	trayStart, trayEnd, ok := trayModulesArray(config)
	if !ok {
		return "", false, errors.New("waybar config has no group/tray-expander modules array")
	}
	if !waybarArrayRangeHasModule(config, trayStart, "custom/omabat") {
		config = appendToWaybarArray(config, trayStart, trayEnd, `"custom/omabat"`)
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
    "format": " {text}",
    "return-type": "json",
    "interval": 5,
    "tooltip": true,
    "on-click": %s
  },
`, strconv.Quote(command+" waybar"), strconv.Quote("omarchy-launch-or-focus-tui "+command))
		config = config[:index] + module + config[index:]
		changed = true
	}

	var formatChanged bool
	config, formatChanged, err := ensureWaybarObjectString(config, "custom/omabat", "format", " {text}")
	if err != nil {
		return "", false, err
	}
	changed = changed || formatChanged
	return config, changed, nil
}

func ensureWaybarObjectString(config, object, key, value string) (string, bool, error) {
	start, end, ok := waybarObject(config, object)
	if !ok {
		return "", false, fmt.Errorf("waybar config has no %s object", object)
	}
	if strings.Contains(config[start:end], strconv.Quote(key)) {
		return config, false, nil
	}
	insertion := "\n    " + strconv.Quote(key) + ": " + strconv.Quote(value) + ","
	return config[:start+1] + insertion + config[start+1:], true, nil
}

func waybarArrayHasModule(config, arrayName, module string) bool {
	start, end, ok := waybarArray(config, arrayName)
	return ok && waybarArrayRangeHasModule(config[:end], start, module)
}

func waybarArrayRangeHasModule(config string, start int, module string) bool {
	_, end, ok := waybarArrayBounds(config, start)
	return ok && strings.Contains(config[start:end], strconv.Quote(module))
}

func appendToWaybarArray(config string, start, end int, value string) string {
	last := end - 2
	for last > start && (config[last] == ' ' || config[last] == '\t' || config[last] == '\n' || config[last] == '\r') {
		last--
	}
	separator := ","
	if config[last] == '[' || config[last] == ',' {
		separator = ""
	}
	insertion := separator + "\n    " + value + "\n"
	return config[:end-1] + insertion + config[end-1:]
}

func removeFromWaybarArray(config, arrayName, module string) (string, bool) {
	start, end, ok := waybarArray(config, arrayName)
	if !ok {
		return config, false
	}
	token := strconv.Quote(module)
	index := strings.Index(config[start:end], token)
	if index < 0 {
		return config, false
	}
	removeStart := start + index
	removeEnd := removeStart + len(token)

	after := removeEnd
	for after < end && (config[after] == ' ' || config[after] == '\t') {
		after++
	}
	if after < end && config[after] == ',' {
		removeEnd = after + 1
	} else {
		before := removeStart - 1
		for before > start && (config[before] == ' ' || config[before] == '\t') {
			before--
		}
		if before > start && config[before] == ',' {
			removeStart = before
		}
	}
	return config[:removeStart] + config[removeEnd:], true
}

func trayModulesArray(config string) (int, int, bool) {
	groupStart, groupEnd, ok := waybarObject(config, "group/tray-expander")
	if !ok {
		return 0, 0, false
	}
	start, end, ok := waybarArrayFrom(config, "modules", groupStart)
	if !ok || end > groupEnd {
		return 0, 0, false
	}
	return start, end, true
}

func waybarObject(config, name string) (int, int, bool) {
	token := strconv.Quote(name)
	for from := 0; from < len(config); {
		index := strings.Index(config[from:], token)
		if index < 0 {
			return 0, 0, false
		}
		index += from
		cursor := index + len(token)
		for cursor < len(config) && (config[cursor] == ' ' || config[cursor] == '\t' || config[cursor] == '\n' || config[cursor] == '\r') {
			cursor++
		}
		if cursor >= len(config) || config[cursor] != ':' {
			from = index + len(token)
			continue
		}
		cursor++
		for cursor < len(config) && (config[cursor] == ' ' || config[cursor] == '\t' || config[cursor] == '\n' || config[cursor] == '\r') {
			cursor++
		}
		if cursor < len(config) && config[cursor] == '{' {
			return waybarObjectBounds(config, cursor)
		}
		from = cursor
	}
	return 0, 0, false
}

func waybarArray(config, name string) (int, int, bool) {
	return waybarArrayFrom(config, name, 0)
}

func waybarArrayFrom(config, name string, from int) (int, int, bool) {
	key := strconv.Quote(name)
	keyIndex := strings.Index(config[from:], key)
	if keyIndex < 0 {
		return 0, 0, false
	}
	keyIndex += from
	start := strings.Index(config[keyIndex+len(key):], "[")
	if start < 0 {
		return 0, 0, false
	}
	start += keyIndex + len(key)
	return waybarArrayBounds(config, start)
}

func waybarArrayBounds(config string, start int) (int, int, bool) {
	return waybarDelimitedBounds(config, start, '[', ']')
}

func waybarObjectBounds(config string, start int) (int, int, bool) {
	return waybarDelimitedBounds(config, start, '{', '}')
}

func waybarDelimitedBounds(config string, start int, open, close byte) (int, int, bool) {
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
		case config[index] == open:
			depth++
		case config[index] == close:
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
