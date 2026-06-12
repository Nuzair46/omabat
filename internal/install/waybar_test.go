package install

import (
	"strings"
	"testing"
)

func TestAddWaybarModuleIsIdempotent(t *testing.T) {
	config := `{
  "modules-right": [
    "cpu",
    "battery"
  ],
  "battery": {}
}`
	got, changed, err := addWaybarModule(config, "/opt/Omabat App/omabat")
	if err != nil {
		t.Fatal(err)
	}
	if !changed || !strings.Contains(got, `"custom/omabat"`) || !strings.Contains(got, `'/opt/Omabat App/omabat' waybar`) {
		t.Fatalf("module not added:\n%s", got)
	}
	again, changed, err := addWaybarModule(got, "/opt/Omabat App/omabat")
	if err != nil {
		t.Fatal(err)
	}
	if changed || again != got {
		t.Fatal("second integration changed the config")
	}
}

func TestAddWaybarModuleRequiresKnownInsertionPoints(t *testing.T) {
	if _, _, err := addWaybarModule(`{}`, "/bin/omabat"); err == nil {
		t.Fatal("expected invalid waybar config to fail")
	}
}
