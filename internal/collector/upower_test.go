package collector

import (
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func TestSnapshotFromUPowerParsesProperties(t *testing.T) {
	props := map[string]dbus.Variant{
		"NativePath":  dbus.MakeVariant("BAT0"),
		"IsPresent":   dbus.MakeVariant(true),
		"State":       dbus.MakeVariant(uint32(2)),
		"Percentage":  dbus.MakeVariant(62.5),
		"Energy":      dbus.MakeVariant(30.0),
		"EnergyRate":  dbus.MakeVariant(7.25),
		"Capacity":    dbus.MakeVariant(88.0),
		"Technology":  dbus.MakeVariant(uint32(1)),
		"TimeToEmpty": dbus.MakeVariant(int64(1234)),
		"Vendor":      dbus.MakeVariant("Example"),
	}
	now := time.Unix(42, 0)
	s := snapshotFromUPower(props, "/org/freedesktop/UPower/devices/DisplayDevice", now)
	if s.DeviceID != "BAT0" || s.State != "discharging" || s.Timestamp != now || !s.Present {
		t.Fatalf("unexpected snapshot: %+v", s)
	}
	if s.Percentage.Float64 != 62.5 || s.EnergyRate.Float64 != 7.25 || s.Technology.String != "lithium-ion" {
		t.Fatalf("properties not parsed: %+v", s)
	}
}
