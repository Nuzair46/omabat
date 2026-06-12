package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"github.com/nuzair46/omabat/internal/battery"
)

const currentSchema = 3

type Repository struct {
	DB *sql.DB
}

func DefaultPath() (string, error) {
	dir := os.Getenv("XDG_DATA_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dir, "omabat", "omabat.db"), nil
}

func Open(path string) (*Repository, error) {
	if path == "" {
		var err error
		path, err = DefaultPath()
		if err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	r := &Repository{DB: db}
	if err := r.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return r, nil
}

func (r *Repository) Close() error { return r.DB.Close() }

func (r *Repository) migrate(ctx context.Context) error {
	if _, err := r.DB.ExecContext(ctx, `PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;`); err != nil {
		return err
	}
	if _, err := r.DB.ExecContext(ctx, schema); err != nil {
		return err
	}
	var version int
	if err := r.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version > currentSchema {
		return fmt.Errorf("database schema version %d is newer than supported version %d", version, currentSchema)
	}
	if version < 2 {
		tx, err := r.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx, `ALTER TABLE battery_samples ADD COLUMN power_profile TEXT`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(2, unixepoch())`); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		version = 2
	}
	if version < 3 {
		tx, err := r.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx, `ALTER TABLE battery_samples ADD COLUMN ac_online INTEGER`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(3, unixepoch())`); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

const schema = `
CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL);
INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES (1, unixepoch());
CREATE TABLE IF NOT EXISTS battery_devices (
 id TEXT PRIMARY KEY, native_path TEXT NOT NULL, manufacturer TEXT, model TEXT, serial TEXT, technology TEXT,
 first_seen INTEGER NOT NULL, last_seen INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS battery_samples (
 id INTEGER PRIMARY KEY, device_id TEXT NOT NULL REFERENCES battery_devices(id), timestamp INTEGER NOT NULL,
 source TEXT NOT NULL, present INTEGER NOT NULL, state TEXT NOT NULL, percentage REAL,
 energy_now REAL, energy_full REAL, energy_design REAL, charge_now REAL, charge_full REAL, charge_design REAL,
 energy_rate REAL, voltage REAL, current REAL, temperature REAL, cycle_count INTEGER, capacity REAL,
 time_to_empty INTEGER, time_to_full INTEGER
);
CREATE INDEX IF NOT EXISTS samples_device_time ON battery_samples(device_id, timestamp);
CREATE TABLE IF NOT EXISTS power_events (
 id INTEGER PRIMARY KEY, device_id TEXT NOT NULL REFERENCES battery_devices(id), type TEXT NOT NULL,
 timestamp INTEGER NOT NULL, end_timestamp INTEGER, percentage REAL, end_percentage REAL,
 percentage_delta REAL, duration_seconds INTEGER
);
CREATE INDEX IF NOT EXISTS events_device_time ON power_events(device_id, timestamp);
CREATE TABLE IF NOT EXISTS hourly_aggregates (
 device_id TEXT NOT NULL REFERENCES battery_devices(id), hour INTEGER NOT NULL, min_percentage REAL,
 max_percentage REAL, avg_discharge_power REAL, peak_discharge_power REAL, charging_seconds INTEGER NOT NULL DEFAULT 0,
 suspend_drain_percentage REAL NOT NULL DEFAULT 0, avg_health_percentage REAL,
 PRIMARY KEY(device_id, hour)
);`

func (r *Repository) RecordSnapshot(ctx context.Context, s battery.Snapshot) ([]battery.Event, error) {
	if s.DeviceID == "" {
		return nil, errors.New("snapshot has no device identity")
	}
	if s.Timestamp.IsZero() {
		s.Timestamp = time.Now()
	}
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := upsertDevice(ctx, tx, s.Device()); err != nil {
		return nil, err
	}
	var previousState string
	err = tx.QueryRowContext(ctx, `SELECT state FROM battery_samples WHERE device_id=? ORDER BY timestamp DESC LIMIT 1`, s.DeviceID).Scan(&previousState)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err := insertSnapshot(ctx, tx, s); err != nil {
		return nil, err
	}
	var events []battery.Event
	eventType := transition(previousState, s.State)
	if eventType != "" {
		ev := battery.Event{DeviceID: s.DeviceID, Type: eventType, Timestamp: s.Timestamp, Percentage: s.Percentage}
		res, err := tx.ExecContext(ctx, `INSERT INTO power_events(device_id,type,timestamp,percentage) VALUES(?,?,?,?)`,
			ev.DeviceID, ev.Type, ev.Timestamp.Unix(), ev.Percentage)
		if err != nil {
			return nil, err
		}
		ev.ID, _ = res.LastInsertId()
		events = append(events, ev)
	}
	return events, tx.Commit()
}

func transition(previous, current string) string {
	if previous == "" || previous == current {
		return ""
	}
	if current == "charging" && previous != "charging" {
		return "plugged"
	}
	if (previous == "charging" || previous == "full" || previous == "pending") && current == "discharging" {
		return "unplugged"
	}
	if current == "full" && previous != "full" {
		return "full"
	}
	return ""
}

func upsertDevice(ctx context.Context, tx *sql.Tx, d battery.Device) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO battery_devices(id,native_path,manufacturer,model,serial,technology,first_seen,last_seen)
VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET native_path=excluded.native_path,
manufacturer=COALESCE(excluded.manufacturer,manufacturer), model=COALESCE(excluded.model,model),
serial=COALESCE(excluded.serial,serial), technology=COALESCE(excluded.technology,technology), last_seen=excluded.last_seen`,
		d.ID, d.NativePath, d.Manufacturer, d.Model, d.Serial, d.Technology, d.FirstSeen.Unix(), d.LastSeen.Unix())
	return err
}

func insertSnapshot(ctx context.Context, tx *sql.Tx, s battery.Snapshot) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO battery_samples(device_id,timestamp,source,present,state,percentage,
energy_now,energy_full,energy_design,charge_now,charge_full,charge_design,energy_rate,voltage,current,temperature,
cycle_count,capacity,time_to_empty,time_to_full,power_profile,ac_online) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		s.DeviceID, s.Timestamp.Unix(), s.Source, s.Present, s.State, s.Percentage,
		s.EnergyNow, s.EnergyFull, s.EnergyDesign, s.ChargeNow, s.ChargeFull, s.ChargeDesign, s.EnergyRate,
		s.Voltage, s.Current, s.Temperature, s.CycleCount, s.Capacity, s.TimeToEmpty, s.TimeToFull, s.PowerProfile, s.ACOnline)
	return err
}

func (r *Repository) RecordSleep(ctx context.Context, s battery.Snapshot) error {
	if _, err := r.RecordSnapshot(ctx, s); err != nil {
		return err
	}
	_, err := r.DB.ExecContext(ctx, `INSERT INTO power_events(device_id,type,timestamp,percentage) VALUES(?,?,?,?)`,
		s.DeviceID, "sleep", s.Timestamp.Unix(), s.Percentage)
	return err
}

func (r *Repository) RecordResume(ctx context.Context, s battery.Snapshot) error {
	if _, err := r.RecordSnapshot(ctx, s); err != nil {
		return err
	}
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id, started int64
	var pct sql.NullFloat64
	err = tx.QueryRowContext(ctx, `SELECT id,timestamp,percentage FROM power_events
WHERE device_id=? AND type='sleep' AND end_timestamp IS NULL ORDER BY timestamp DESC LIMIT 1`, s.DeviceID).Scan(&id, &started, &pct)
	if errors.Is(err, sql.ErrNoRows) {
		return tx.Commit()
	}
	if err != nil {
		return err
	}
	delta := sql.NullFloat64{}
	if pct.Valid && s.Percentage.Valid {
		delta = battery.Float(s.Percentage.Float64 - pct.Float64)
	}
	_, err = tx.ExecContext(ctx, `UPDATE power_events SET end_timestamp=?,end_percentage=?,percentage_delta=?,duration_seconds=? WHERE id=?`,
		s.Timestamp.Unix(), s.Percentage, delta, s.Timestamp.Unix()-started, id)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO power_events(device_id,type,timestamp,percentage,percentage_delta,duration_seconds)
VALUES(?,?,?,?,?,?)`, s.DeviceID, "resume", s.Timestamp.Unix(), s.Percentage, delta, s.Timestamp.Unix()-started)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Repository) Maintain(ctx context.Context, now time.Time) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	cutoff := now.Add(-10 * 24 * time.Hour).Unix()
	_, err = tx.ExecContext(ctx, `INSERT INTO hourly_aggregates(device_id,hour,min_percentage,max_percentage,
avg_discharge_power,peak_discharge_power,charging_seconds,suspend_drain_percentage,avg_health_percentage)
SELECT device_id,(timestamp/3600)*3600,MIN(percentage),MAX(percentage),
AVG(CASE WHEN state='discharging' THEN energy_rate END),MAX(CASE WHEN state='discharging' THEN energy_rate END),
SUM(CASE WHEN state='charging' THEN 120 ELSE 0 END),0,AVG(capacity)
FROM battery_samples WHERE timestamp < ? GROUP BY device_id,(timestamp/3600)
ON CONFLICT(device_id,hour) DO UPDATE SET min_percentage=excluded.min_percentage,max_percentage=excluded.max_percentage,
avg_discharge_power=excluded.avg_discharge_power,peak_discharge_power=excluded.peak_discharge_power,
charging_seconds=excluded.charging_seconds,avg_health_percentage=excluded.avg_health_percentage`, cutoff)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE hourly_aggregates SET suspend_drain_percentage=COALESCE((
SELECT SUM(-percentage_delta) FROM power_events e WHERE e.device_id=hourly_aggregates.device_id
AND e.type='sleep' AND e.timestamp>=hourly_aggregates.hour AND e.timestamp<hourly_aggregates.hour+3600
AND e.percentage_delta<0),0) WHERE hour < ?`, cutoff)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM battery_samples WHERE timestamp < ?`, cutoff); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Repository) Latest(ctx context.Context) (battery.Snapshot, error) {
	row := r.DB.QueryRowContext(ctx, `SELECT device_id,timestamp,source,present,state,percentage,energy_now,energy_full,
energy_design,charge_now,charge_full,charge_design,energy_rate,voltage,current,temperature,cycle_count,capacity,
time_to_empty,time_to_full,power_profile,ac_online FROM battery_samples ORDER BY timestamp DESC LIMIT 1`)
	return scanSnapshot(row)
}

type scanner interface{ Scan(...any) error }

func scanSnapshot(row scanner) (battery.Snapshot, error) {
	var s battery.Snapshot
	var ts int64
	err := row.Scan(&s.DeviceID, &ts, &s.Source, &s.Present, &s.State, &s.Percentage, &s.EnergyNow, &s.EnergyFull,
		&s.EnergyDesign, &s.ChargeNow, &s.ChargeFull, &s.ChargeDesign, &s.EnergyRate, &s.Voltage, &s.Current,
		&s.Temperature, &s.CycleCount, &s.Capacity, &s.TimeToEmpty, &s.TimeToFull, &s.PowerProfile, &s.ACOnline)
	s.Timestamp = time.Unix(ts, 0)
	return s, err
}

func (r *Repository) Samples(ctx context.Context, since time.Time) ([]battery.Snapshot, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT device_id,timestamp,source,present,state,percentage,energy_now,energy_full,
energy_design,charge_now,charge_full,charge_design,energy_rate,voltage,current,temperature,cycle_count,capacity,
time_to_empty,time_to_full,power_profile,ac_online FROM battery_samples WHERE timestamp>=? ORDER BY timestamp`, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []battery.Snapshot
	for rows.Next() {
		s, err := scanSnapshot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *Repository) Events(ctx context.Context, since time.Time, limit int) ([]battery.Event, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT id,device_id,type,timestamp,end_timestamp,percentage,end_percentage,
percentage_delta,duration_seconds FROM power_events WHERE timestamp>=? ORDER BY timestamp DESC LIMIT ?`, since.Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []battery.Event
	for rows.Next() {
		var e battery.Event
		var ts int64
		if err := rows.Scan(&e.ID, &e.DeviceID, &e.Type, &ts, &e.EndTimestamp, &e.Percentage, &e.EndPercentage, &e.PercentageDelta, &e.DurationSeconds); err != nil {
			return nil, err
		}
		e.Timestamp = time.Unix(ts, 0)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *Repository) Device(ctx context.Context, id string) (battery.Device, error) {
	var d battery.Device
	var first, last int64
	err := r.DB.QueryRowContext(ctx, `SELECT id,native_path,manufacturer,model,serial,technology,first_seen,last_seen
FROM battery_devices WHERE id=?`, id).Scan(&d.ID, &d.NativePath, &d.Manufacturer, &d.Model, &d.Serial, &d.Technology, &first, &last)
	d.FirstSeen, d.LastSeen = time.Unix(first, 0), time.Unix(last, 0)
	return d, err
}

func (r *Repository) DischargeStats(ctx context.Context, since time.Time) (sql.NullFloat64, sql.NullFloat64, error) {
	var avg, peak sql.NullFloat64
	err := r.DB.QueryRowContext(ctx, `SELECT AVG(energy_rate),MAX(energy_rate) FROM battery_samples
WHERE timestamp>=? AND state='discharging'`, since.Unix()).Scan(&avg, &peak)
	return avg, peak, err
}

func (r *Repository) LongTermHealth(ctx context.Context, deviceID string) (sql.NullFloat64, sql.NullFloat64, sql.NullFloat64, error) {
	var avg, min, max sql.NullFloat64
	err := r.DB.QueryRowContext(ctx, `SELECT AVG(health),MIN(health),MAX(health) FROM (
SELECT avg_health_percentage AS health FROM hourly_aggregates
WHERE device_id=? AND avg_health_percentage IS NOT NULL
UNION ALL
SELECT AVG(capacity) AS health FROM battery_samples
WHERE device_id=? AND capacity IS NOT NULL GROUP BY timestamp/3600
)`, deviceID, deviceID).Scan(&avg, &min, &max)
	return avg, min, max, err
}

func ValidateRange(v string) (time.Duration, error) {
	switch v {
	case "", "24h":
		return 24 * time.Hour, nil
	case "3d":
		return 72 * time.Hour, nil
	case "7d":
		return 7 * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("unsupported range %q; choose 24h, 3d, or 7d (maximum 7 days)", v)
	}
}
