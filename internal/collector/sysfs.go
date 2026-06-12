package collector

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nuzair46/omabat/internal/battery"
)

type SysfsProvider struct {
	Root string
	Now  func() time.Time
}

func (p SysfsProvider) Snapshot(context.Context) (battery.Snapshot, error) {
	root := p.Root
	if root == "" {
		root = "/sys/class/power_supply"
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return battery.Snapshot{}, err
	}
	for _, entry := range entries {
		dir := filepath.Join(root, entry.Name())
		if strings.EqualFold(read(dir, "type"), "Battery") {
			s := p.readBattery(dir, entry.Name())
			s.ACOnline = readACOnline(root, entries)
			return s, nil
		}
	}
	return battery.Snapshot{}, errors.New("no battery found in sysfs")
}

func readACOnline(root string, entries []os.DirEntry) (out sql.NullBool) {
	for _, entry := range entries {
		dir := filepath.Join(root, entry.Name())
		switch strings.ToLower(read(dir, "type")) {
		case "mains", "usb", "usb_c":
			online := read(dir, "online")
			if online == "" {
				continue
			}
			out.Valid = true
			out.Bool = out.Bool || online == "1"
		}
	}
	return out
}

func (p SysfsProvider) readBattery(dir, name string) battery.Snapshot {
	now := time.Now()
	if p.Now != nil {
		now = p.Now()
	}
	s := battery.Snapshot{
		DeviceID: name, NativePath: dir, Timestamp: now, Source: "sysfs",
		Present: read(dir, "present") != "0", State: normalizeState(read(dir, "status")),
		Percentage: value(dir, "capacity", 1), EnergyNow: value(dir, "energy_now", 1e6),
		EnergyFull: value(dir, "energy_full", 1e6), EnergyDesign: value(dir, "energy_full_design", 1e6),
		ChargeNow: value(dir, "charge_now", 1e6), ChargeFull: value(dir, "charge_full", 1e6),
		ChargeDesign: value(dir, "charge_full_design", 1e6), EnergyRate: value(dir, "power_now", 1e6),
		Voltage: value(dir, "voltage_now", 1e6), Current: value(dir, "current_now", 1e6),
		Temperature: value(dir, "temp", 10), CycleCount: intValue(dir, "cycle_count"),
		Technology:   stringValue(dir, "technology"),
		Manufacturer: stringValue(dir, "manufacturer"), Model: stringValue(dir, "model_name"),
		Serial: stringValue(dir, "serial_number"),
	}
	deriveSysfsValues(&s, value(dir, "voltage_min_design", 1e6), value(dir, "voltage_max_design", 1e6))
	s.Capacity = health(s)
	return s
}

func deriveSysfsValues(s *battery.Snapshot, voltageMinDesign, voltageMaxDesign sql.NullFloat64) {
	designVoltage := voltageMinDesign
	if !designVoltage.Valid {
		designVoltage = voltageMaxDesign
	}
	if !designVoltage.Valid {
		designVoltage = s.Voltage
	}
	if !s.EnergyNow.Valid && s.ChargeNow.Valid && s.Voltage.Valid {
		s.EnergyNow = battery.Float(s.ChargeNow.Float64 * s.Voltage.Float64)
	}
	if !s.EnergyFull.Valid && s.ChargeFull.Valid && designVoltage.Valid {
		s.EnergyFull = battery.Float(s.ChargeFull.Float64 * designVoltage.Float64)
	}
	if !s.EnergyDesign.Valid && s.ChargeDesign.Valid && designVoltage.Valid {
		s.EnergyDesign = battery.Float(s.ChargeDesign.Float64 * designVoltage.Float64)
	}
	if !s.EnergyRate.Valid && s.Voltage.Valid && s.Current.Valid {
		s.EnergyRate = battery.Float(math.Abs(s.Voltage.Float64 * s.Current.Float64))
	} else if s.EnergyRate.Valid {
		s.EnergyRate.Float64 = math.Abs(s.EnergyRate.Float64)
	}
	current := math.Abs(s.Current.Float64)
	if s.Current.Valid && current > 0 {
		switch {
		case s.State == "discharging" && s.ChargeNow.Valid:
			s.TimeToEmpty = battery.Int(int64(math.Round(s.ChargeNow.Float64 / current * 3600)))
		case s.State == "charging" && s.ChargeNow.Valid && s.ChargeFull.Valid && s.ChargeFull.Float64 > s.ChargeNow.Float64:
			s.TimeToFull = battery.Int(int64(math.Round((s.ChargeFull.Float64 - s.ChargeNow.Float64) / current * 3600)))
		}
	}
	if s.EnergyRate.Valid && s.EnergyRate.Float64 > 0 {
		switch {
		case s.State == "discharging" && !s.TimeToEmpty.Valid && s.EnergyNow.Valid:
			s.TimeToEmpty = battery.Int(int64(math.Round(s.EnergyNow.Float64 / s.EnergyRate.Float64 * 3600)))
		case s.State == "charging" && !s.TimeToFull.Valid && s.EnergyNow.Valid && s.EnergyFull.Valid && s.EnergyFull.Float64 > s.EnergyNow.Float64:
			s.TimeToFull = battery.Int(int64(math.Round((s.EnergyFull.Float64 - s.EnergyNow.Float64) / s.EnergyRate.Float64 * 3600)))
		}
	}
}

func health(s battery.Snapshot) (out sql.NullFloat64) {
	switch {
	case s.EnergyFull.Valid && s.EnergyDesign.Valid && s.EnergyDesign.Float64 > 0:
		return battery.Float(s.EnergyFull.Float64 / s.EnergyDesign.Float64 * 100)
	case s.ChargeFull.Valid && s.ChargeDesign.Valid && s.ChargeDesign.Float64 > 0:
		return battery.Float(s.ChargeFull.Float64 / s.ChargeDesign.Float64 * 100)
	default:
		return out
	}
}

func read(dir, name string) string {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func value(dir, name string, divisor float64) (out sql.NullFloat64) {
	v, err := strconv.ParseFloat(read(dir, name), 64)
	if err == nil {
		out.Float64, out.Valid = v/divisor, true
	}
	return
}

func intValue(dir, name string) (out sql.NullInt64) {
	v, err := strconv.ParseInt(read(dir, name), 10, 64)
	if err == nil {
		out.Int64, out.Valid = v, true
	}
	return
}

func stringValue(dir, name string) (out sql.NullString) {
	out.String = read(dir, name)
	out.Valid = out.String != ""
	return
}

func normalizeState(s string) string {
	switch strings.ToLower(s) {
	case "charging":
		return "charging"
	case "discharging":
		return "discharging"
	case "full":
		return "full"
	case "not charging":
		return "pending"
	default:
		return "unknown"
	}
}
