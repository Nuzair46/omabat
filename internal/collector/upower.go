package collector

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nuzair46/omabat/internal/battery"
)

type UPowerProvider struct {
	Connect func() (*dbus.Conn, error)
	Now     func() time.Time
}

func (p UPowerProvider) Snapshot(ctx context.Context) (battery.Snapshot, error) {
	connect := p.Connect
	if connect == nil {
		connect = dbus.SystemBus
	}
	conn, err := connect()
	if err != nil {
		return battery.Snapshot{}, err
	}
	upower := conn.Object("org.freedesktop.UPower", "/org/freedesktop/UPower")
	var path dbus.ObjectPath
	if err := upower.CallWithContext(ctx, "org.freedesktop.UPower.GetDisplayDevice", 0).Store(&path); err != nil {
		return battery.Snapshot{}, err
	}
	if !path.IsValid() {
		return battery.Snapshot{}, errors.New("UPower returned no display device")
	}
	var props map[string]dbus.Variant
	obj := conn.Object("org.freedesktop.UPower", path)
	if err := obj.CallWithContext(ctx, "org.freedesktop.DBus.Properties.GetAll", 0, "org.freedesktop.UPower.Device").Store(&props); err != nil {
		return battery.Snapshot{}, err
	}
	if kind := uintProp(props, "Type"); kind != 0 && kind != 2 {
		return battery.Snapshot{}, fmt.Errorf("UPower display device is not a battery (type %d)", kind)
	}
	now := time.Now()
	if p.Now != nil {
		now = p.Now()
	}
	s := snapshotFromUPower(props, path, now)
	var onBattery dbus.Variant
	if err := upower.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, "org.freedesktop.UPower", "OnBattery").Store(&onBattery); err == nil {
		if value, ok := onBattery.Value().(bool); ok {
			s.ACOnline = battery.Bool(!value)
		}
	}
	return s, nil
}

func snapshotFromUPower(props map[string]dbus.Variant, path dbus.ObjectPath, now time.Time) battery.Snapshot {
	native := stringProp(props, "NativePath")
	s := battery.Snapshot{
		DeviceID: deviceID(native, string(path)), NativePath: native, Timestamp: now, Source: "upower",
		Present: boolProp(props, "IsPresent"), State: upowerState(uintProp(props, "State")),
		Percentage: floatProp(props, "Percentage"), EnergyNow: floatProp(props, "Energy"),
		EnergyFull: floatProp(props, "EnergyFull"), EnergyDesign: floatProp(props, "EnergyFullDesign"),
		EnergyRate: floatProp(props, "EnergyRate"), Voltage: floatProp(props, "Voltage"),
		Temperature: floatProp(props, "Temperature"), Capacity: floatProp(props, "Capacity"),
		TimeToEmpty: intProp(props, "TimeToEmpty"), TimeToFull: intProp(props, "TimeToFull"),
		Technology:   battery.String(upowerTechnology(uintProp(props, "Technology"))),
		Manufacturer: battery.String(stringProp(props, "Vendor")), Model: battery.String(stringProp(props, "Model")),
		Serial: battery.String(stringProp(props, "Serial")),
	}
	return s
}

func deviceID(native, path string) string {
	if native != "" {
		return filepath.Base(native)
	}
	return filepath.Base(path)
}

func stringProp(p map[string]dbus.Variant, key string) string {
	if v, ok := p[key]; ok {
		if out, ok := v.Value().(string); ok {
			return out
		}
	}
	return ""
}

func boolProp(p map[string]dbus.Variant, key string) bool {
	if v, ok := p[key]; ok {
		out, _ := v.Value().(bool)
		return out
	}
	return false
}

func uintProp(p map[string]dbus.Variant, key string) uint32 {
	if v, ok := p[key]; ok {
		switch n := v.Value().(type) {
		case uint32:
			return n
		case uint64:
			return uint32(n)
		}
	}
	return 0
}

func floatProp(p map[string]dbus.Variant, key string) sql.NullFloat64 {
	if v, ok := p[key]; ok {
		switch n := v.Value().(type) {
		case float64:
			return battery.Float(n)
		case float32:
			return battery.Float(float64(n))
		}
	}
	return sql.NullFloat64{}
}

func intProp(p map[string]dbus.Variant, key string) sql.NullInt64 {
	if v, ok := p[key]; ok {
		switch n := v.Value().(type) {
		case int64:
			return battery.Int(n)
		case uint64:
			return battery.Int(int64(n))
		}
	}
	return sql.NullInt64{}
}

func upowerState(v uint32) string {
	return map[uint32]string{1: "charging", 2: "discharging", 3: "empty", 4: "full", 5: "pending", 6: "pending"}[v]
}

func upowerTechnology(v uint32) string {
	return strings.TrimSpace(map[uint32]string{1: "lithium-ion", 2: "lithium-polymer", 3: "lithium-iron-phosphate", 4: "lead-acid", 5: "nickel-cadmium", 6: "nickel-metal-hydride"}[v])
}
