package install

import (
	"strings"
	"testing"
)

func TestAddWaybarModuleIsIdempotent(t *testing.T) {
	config := `{
  "modules-right": [
    "cpu",
    "group/tray-expander",
    "battery"
  ],
  "group/tray-expander": {
    "modules": ["custom/expand-icon", "tray"]
  },
  "battery": {}
}`
	got, changed, err := addWaybarModule(config, "/opt/Omabat App/omabat")
	if err != nil {
		t.Fatal(err)
	}
	trayStart, _, ok := trayModulesArray(got)
	if !changed || !ok || !waybarArrayRangeHasModule(got, trayStart, "custom/omabat") ||
		!strings.Contains(got, `'/opt/Omabat App/omabat' waybar`) {
		t.Fatalf("module not added:\n%s", got)
	}
	if waybarArrayHasModule(got, "modules-right", "custom/omabat") {
		t.Fatalf("module added outside tray:\n%s", got)
	}
	_, trayEnd, _ := trayModulesArray(got)
	tray := got[trayStart:trayEnd]
	if strings.Index(tray, `"custom/omabat"`) < strings.Index(tray, `"tray"`) {
		t.Fatalf("module must be a hidden tray drawer item, not its always-visible first item:\n%s", got)
	}
	again, changed, err := addWaybarModule(got, "/opt/Omabat App/omabat")
	if err != nil {
		t.Fatal(err)
	}
	if changed || again != got {
		t.Fatal("second integration changed the config")
	}
}

func TestAddWaybarModuleMovesExistingModuleIntoTray(t *testing.T) {
	config := `{
  "modules-right": ["custom/omabat", "group/tray-expander", "battery"],
  "group/tray-expander": {
    "modules": ["custom/expand-icon", "tray"]
  },
  "custom/omabat": {},
  "battery": {}
}`
	got, changed, err := addWaybarModule(config, "/bin/omabat")
	if err != nil {
		t.Fatal(err)
	}
	trayStart, _, ok := trayModulesArray(got)
	if !changed || !ok || !waybarArrayRangeHasModule(got, trayStart, "custom/omabat") {
		t.Fatalf("module not moved into tray:\n%s", got)
	}
	if waybarArrayHasModule(got, "modules-right", "custom/omabat") {
		t.Fatalf("module left outside tray:\n%s", got)
	}
}

func TestAddWaybarModuleRequiresKnownInsertionPoints(t *testing.T) {
	if _, _, err := addWaybarModule(`{}`, "/bin/omabat"); err == nil {
		t.Fatal("expected invalid waybar config to fail")
	}
	config := `{
  "modules-right": ["group/tray-expander"],
  "group/other": {"modules": ["tray"]}
}`
	if _, _, err := addWaybarModule(config, "/bin/omabat"); err == nil {
		t.Fatal("expected missing tray-expander definition to fail")
	}
}
