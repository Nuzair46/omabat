package demo

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"time"

	"github.com/nuzair46/omabat/internal/battery"
)

type Repository interface {
	RecordSnapshot(context.Context, battery.Snapshot) ([]battery.Event, error)
	RecordSleep(context.Context, battery.Snapshot) error
	RecordResume(context.Context, battery.Snapshot) error
}

type Result struct {
	Samples int
	Sleeps  int
	Start   time.Time
	End     time.Time
}

func Generate(ctx context.Context, repo Repository, baseline battery.Snapshot, days int, seed int64, now time.Time) (Result, error) {
	if days < 1 || days > 10 {
		return Result{}, fmt.Errorf("demo history must be between 1 and 10 days")
	}
	if now.IsZero() {
		now = time.Now()
	}
	now = now.Truncate(2 * time.Minute)
	start := now.Add(-time.Duration(days) * 24 * time.Hour)
	rng := rand.New(rand.NewSource(seed))
	base := normalizeBaseline(baseline)
	percentage := 76.0
	sleeping := false
	forcedCharging := false
	result := Result{Start: start, End: now}

	for at := start; !at.After(now); at = at.Add(2 * time.Minute) {
		shouldSleep := at.Hour() < 7
		profile := profileAt(at)
		if percentage <= 12 {
			forcedCharging = true
		} else if percentage >= 85 {
			forcedCharging = false
		}
		state := stateAt(at, percentage, forcedCharging)
		rate := rateAt(rng, state, profile)

		if shouldSleep {
			rate = 0.10 + rng.Float64()*0.15
			percentage = clamp(percentage-rate*(2.0/60.0)/base.EnergyFull.Float64*100, 5, 100)
			if !sleeping {
				s := snapshot(base, at, percentage, "discharging", "power-saver", rate, rng)
				if err := repo.RecordSleep(ctx, s); err != nil {
					return result, err
				}
				result.Sleeps++
				sleeping = true
			}
			continue
		}

		if sleeping {
			s := snapshot(base, at, percentage, state, profile, rate, rng)
			if err := repo.RecordResume(ctx, s); err != nil {
				return result, err
			}
			result.Samples++
			sleeping = false
			continue
		}

		if state == "charging" {
			percentage += rate * (2.0 / 60.0) / base.EnergyFull.Float64 * 100
		} else if state == "discharging" {
			percentage -= rate * (2.0 / 60.0) / base.EnergyFull.Float64 * 100
		}
		percentage = clamp(percentage, 5, 100)
		if percentage >= 99.8 && state == "charging" {
			state = "full"
			percentage = 100
			rate = 0
		}
		s := snapshot(base, at, percentage, state, profile, rate, rng)
		if _, err := repo.RecordSnapshot(ctx, s); err != nil {
			return result, err
		}
		result.Samples++
	}
	return result, nil
}

func normalizeBaseline(s battery.Snapshot) battery.Snapshot {
	if s.DeviceID == "" {
		s.DeviceID = "BAT0"
	}
	if s.NativePath == "" {
		s.NativePath = "/sys/class/power_supply/" + s.DeviceID
	}
	if s.Source == "" {
		s.Source = "demo"
	} else {
		s.Source += "+demo"
	}
	s.Present = true
	if !s.Voltage.Valid {
		s.Voltage = battery.Float(15.2)
	}
	if !s.ChargeDesign.Valid {
		s.ChargeDesign = battery.Float(4.1)
	}
	if !s.ChargeFull.Valid {
		s.ChargeFull = battery.Float(s.ChargeDesign.Float64 * 0.92)
	}
	if !s.EnergyDesign.Valid || s.EnergyDesign.Float64 <= 0 {
		s.EnergyDesign = battery.Float(s.ChargeDesign.Float64 * s.Voltage.Float64)
	}
	if !s.EnergyFull.Valid || s.EnergyFull.Float64 <= 0 {
		s.EnergyFull = battery.Float(s.ChargeFull.Float64 * s.Voltage.Float64)
	}
	if !s.Capacity.Valid || s.Capacity.Float64 <= 0 {
		s.Capacity = battery.Float(s.EnergyFull.Float64 / s.EnergyDesign.Float64 * 100)
	}
	if !s.CycleCount.Valid || s.CycleCount.Int64 == 0 {
		s.CycleCount = battery.Int(184)
	}
	if !s.Technology.Valid || s.Technology.String == "Unknown" {
		s.Technology = battery.String("lithium-ion")
	}
	if !s.Manufacturer.Valid {
		s.Manufacturer = battery.String("Demo Battery Co.")
	}
	if !s.Model.Valid {
		s.Model = battery.String("DemoPack 62")
	}
	if !s.Serial.Valid {
		s.Serial = battery.String("OMABAT-DEMO-001")
	}
	return s
}

func snapshot(base battery.Snapshot, at time.Time, percentage float64, state, profile string, rate float64, rng *rand.Rand) battery.Snapshot {
	s := base
	voltage := 14.7 + percentage/100*1.1 + (rng.Float64()-0.5)*0.08
	energyNow := base.EnergyFull.Float64 * percentage / 100
	chargeNow := base.ChargeFull.Float64 * percentage / 100
	s.Timestamp = at
	s.State = state
	s.ACOnline = battery.Bool(state == "charging" || state == "full" || state == "pending")
	s.Percentage = battery.Float(percentage)
	s.PowerProfile = battery.String(profile)
	s.EnergyNow = battery.Float(energyNow)
	s.ChargeNow = battery.Float(chargeNow)
	s.EnergyRate = battery.Float(rate)
	s.Voltage = battery.Float(voltage)
	if rate > 0 {
		s.Current = battery.Float(rate / voltage)
	} else {
		s.Current = battery.Float(0)
	}
	temp := 28.0 + rate*0.32 + (rng.Float64()-0.5)*1.5
	if state == "charging" {
		temp += 3
	}
	s.Temperature = battery.Float(temp)
	s.TimeToEmpty = battery.Int(0)
	s.TimeToFull = battery.Int(0)
	switch {
	case state == "discharging" && rate > 0:
		s.TimeToEmpty = battery.Int(int64(energyNow / rate * 3600))
	case state == "charging" && rate > 0:
		s.TimeToFull = battery.Int(int64((base.EnergyFull.Float64 - energyNow) / rate * 3600))
	}
	return s
}

func stateAt(at time.Time, percentage float64, forcedCharging bool) string {
	hour := at.Hour()
	scheduledCharging := (hour >= 10 && hour < 12) || (hour >= 18 && hour < 20)
	if percentage >= 99.8 && (scheduledCharging || forcedCharging) {
		return "full"
	}
	if scheduledCharging || forcedCharging {
		return "charging"
	}
	return "discharging"
}

func profileAt(at time.Time) string {
	switch {
	case at.Hour() < 9 || at.Hour() >= 22:
		return "power-saver"
	case at.Hour() >= 14 && at.Hour() < 16 && at.Weekday() != time.Saturday && at.Weekday() != time.Sunday:
		return "performance"
	default:
		return "balanced"
	}
}

func rateAt(rng *rand.Rand, state, profile string) float64 {
	if state == "full" {
		return 0
	}
	if state == "charging" {
		return 38 + rng.Float64()*22
	}
	base := map[string]float64{"power-saver": 5.5, "balanced": 10.5, "performance": 22}[profile]
	return math.Max(2.5, base+(rng.Float64()-0.5)*base*0.55)
}

func clamp(v, low, high float64) float64 {
	return math.Max(low, math.Min(high, v))
}
