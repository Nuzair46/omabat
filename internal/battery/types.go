package battery

import (
	"database/sql"
	"time"
)

type Snapshot struct {
	DeviceID     string
	Timestamp    time.Time
	Source       string
	NativePath   string
	Present      bool
	ACOnline     sql.NullBool
	State        string
	Percentage   sql.NullFloat64
	EnergyNow    sql.NullFloat64
	EnergyFull   sql.NullFloat64
	EnergyDesign sql.NullFloat64
	ChargeNow    sql.NullFloat64
	ChargeFull   sql.NullFloat64
	ChargeDesign sql.NullFloat64
	EnergyRate   sql.NullFloat64
	Voltage      sql.NullFloat64
	Current      sql.NullFloat64
	Temperature  sql.NullFloat64
	CycleCount   sql.NullInt64
	Capacity     sql.NullFloat64
	TimeToEmpty  sql.NullInt64
	TimeToFull   sql.NullInt64
	PowerProfile sql.NullString
	Technology   sql.NullString
	Manufacturer sql.NullString
	Model        sql.NullString
	Serial       sql.NullString
}

type Device struct {
	ID           string
	NativePath   string
	Manufacturer sql.NullString
	Model        sql.NullString
	Serial       sql.NullString
	Technology   sql.NullString
	FirstSeen    time.Time
	LastSeen     time.Time
}

type Event struct {
	ID              int64
	DeviceID        string
	Type            string
	Timestamp       time.Time
	EndTimestamp    sql.NullInt64
	Percentage      sql.NullFloat64
	EndPercentage   sql.NullFloat64
	PercentageDelta sql.NullFloat64
	DurationSeconds sql.NullInt64
}

type HourlyAggregate struct {
	DeviceID               string
	Hour                   time.Time
	MinPercentage          sql.NullFloat64
	MaxPercentage          sql.NullFloat64
	AvgDischargePower      sql.NullFloat64
	PeakDischargePower     sql.NullFloat64
	ChargingSeconds        int64
	SuspendDrainPercentage float64
	AvgHealthPercentage    sql.NullFloat64
}

func Float(v float64) sql.NullFloat64 { return sql.NullFloat64{Float64: v, Valid: true} }
func Int(v int64) sql.NullInt64       { return sql.NullInt64{Int64: v, Valid: true} }
func String(v string) sql.NullString  { return sql.NullString{String: v, Valid: v != ""} }
func Bool(v bool) sql.NullBool        { return sql.NullBool{Bool: v, Valid: true} }

func Merge(primary, fallback Snapshot) Snapshot {
	out := primary
	if out.DeviceID == "" {
		out.DeviceID = fallback.DeviceID
	}
	if out.NativePath == "" {
		out.NativePath = fallback.NativePath
	}
	if out.Source == "" {
		out.Source = fallback.Source
	} else if fallback.Source != "" && out.Source != fallback.Source {
		out.Source += "+" + fallback.Source
	}
	if out.Timestamp.IsZero() {
		out.Timestamp = fallback.Timestamp
	}
	out.Present = out.Present || fallback.Present
	if !out.ACOnline.Valid {
		out.ACOnline = fallback.ACOnline
	}
	if out.State == "" || out.State == "unknown" {
		out.State = fallback.State
	}
	if !out.Percentage.Valid {
		out.Percentage = fallback.Percentage
	}
	if missingPositive(out.EnergyNow) && positive(fallback.EnergyNow) {
		out.EnergyNow = fallback.EnergyNow
	}
	if missingPositive(out.EnergyFull) && positive(fallback.EnergyFull) {
		out.EnergyFull = fallback.EnergyFull
	}
	if missingPositive(out.EnergyDesign) && positive(fallback.EnergyDesign) {
		out.EnergyDesign = fallback.EnergyDesign
	}
	if missingPositive(out.ChargeNow) && positive(fallback.ChargeNow) {
		out.ChargeNow = fallback.ChargeNow
	}
	if missingPositive(out.ChargeFull) && positive(fallback.ChargeFull) {
		out.ChargeFull = fallback.ChargeFull
	}
	if missingPositive(out.ChargeDesign) && positive(fallback.ChargeDesign) {
		out.ChargeDesign = fallback.ChargeDesign
	}
	if !out.EnergyRate.Valid {
		out.EnergyRate = fallback.EnergyRate
	}
	if !out.Voltage.Valid {
		out.Voltage = fallback.Voltage
	}
	if !out.Current.Valid {
		out.Current = fallback.Current
	}
	if !out.Temperature.Valid {
		out.Temperature = fallback.Temperature
	}
	if !out.CycleCount.Valid {
		out.CycleCount = fallback.CycleCount
	}
	if missingPositive(out.Capacity) && positive(fallback.Capacity) {
		out.Capacity = fallback.Capacity
	}
	if !out.TimeToEmpty.Valid {
		out.TimeToEmpty = fallback.TimeToEmpty
	}
	if !out.TimeToFull.Valid {
		out.TimeToFull = fallback.TimeToFull
	}
	if !out.PowerProfile.Valid {
		out.PowerProfile = fallback.PowerProfile
	}
	if !out.Technology.Valid {
		out.Technology = fallback.Technology
	}
	if !out.Manufacturer.Valid {
		out.Manufacturer = fallback.Manufacturer
	}
	if !out.Model.Valid {
		out.Model = fallback.Model
	}
	if !out.Serial.Valid {
		out.Serial = fallback.Serial
	}
	return out
}

func missingPositive(v sql.NullFloat64) bool {
	return !v.Valid || v.Float64 <= 0
}

func positive(v sql.NullFloat64) bool {
	return v.Valid && v.Float64 > 0
}

func (s Snapshot) Device() Device {
	return Device{
		ID: s.DeviceID, NativePath: s.NativePath, Manufacturer: s.Manufacturer,
		Model: s.Model, Serial: s.Serial, Technology: s.Technology,
		FirstSeen: s.Timestamp, LastSeen: s.Timestamp,
	}
}
