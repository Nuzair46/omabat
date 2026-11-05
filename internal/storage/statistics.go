package storage

import (
	"context"
	"database/sql"
	"math"
	"sort"
	"time"

	"github.com/nuzair46/omabat/internal/battery"
)

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type timedReading struct {
	device                    string
	at, cadence               int64
	state                     string
	percentage, power, health sql.NullFloat64
}

type timeSpan struct{ start, end int64 }

func sampleIntervalSeconds(interval time.Duration) int64 {
	if interval <= 0 {
		interval = battery.DefaultSampleInterval
	}
	// Sub-second collectors still store second-resolution timestamps.
	seconds := int64(interval / time.Second)
	if interval%time.Second != 0 {
		seconds++
	}
	return max(1, seconds)
}

// walkReadings uses the last inserted reading at each device/timestamp for
// intervals, but observes every reading to preserve measured extrema. The
// preceding state and power apply until the next reading, with no extrapolation
// after the last reading. A gap over twice the larger adjacent cadence is unknown
// rather than continuous activity (legacy readings use the 120-second cadence).
// Sleep is subtracted even when other collectors wrote samples during it.
func walkReadings(ctx context.Context, q queryer, from, to int64,
	observe func(timedReading), interval func(timedReading, int64, int64),
) error {
	sleeps, err := sleepSpans(ctx, q, from, to)
	if err != nil {
		return err
	}
	// Include one neighbor on either side of the range to clip boundary
	// intervals correctly without loading the entire retained history.
	rows, err := q.QueryContext(ctx, `SELECT device_id,timestamp,state,percentage,energy_rate,capacity,sample_interval_seconds
FROM battery_samples WHERE (timestamp>=? AND timestamp<=?) OR id IN (
 SELECT (SELECT p.id FROM battery_samples p WHERE p.device_id=d.id AND p.timestamp<?
 ORDER BY p.timestamp DESC,p.id DESC LIMIT 1) FROM battery_devices d
) OR id IN (
 SELECT (SELECT p.id FROM battery_samples p WHERE p.device_id=d.id AND p.timestamp>?
 ORDER BY p.timestamp,p.id DESC LIMIT 1) FROM battery_devices d
) ORDER BY device_id,timestamp,id`, from, to, from, to)
	if err != nil {
		return err
	}
	defer rows.Close()

	var previous timedReading
	havePrevious := false
	for rows.Next() {
		var current timedReading
		if err := rows.Scan(&current.device, &current.at, &current.state, &current.percentage, &current.power, &current.health, &current.cadence); err != nil {
			return err
		}
		if current.at >= from && current.at <= to {
			observe(current)
		}
		if havePrevious && current.device == previous.device && current.at == previous.at {
			previous = current
			continue
		}
		if havePrevious {
			if current.device == previous.device {
				cadence := max(previous.cadence, current.cadence, int64(1))
				if current.at-previous.at <= 2*cadence {
					start, end := max(previous.at, from), min(current.at, to)
					for _, active := range activeSpans(start, end, sleeps[previous.device]) {
						interval(previous, active.start, active.end)
					}
				}
			}
		}
		previous, havePrevious = current, true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return nil
}

func sleepSpans(ctx context.Context, q queryer, from, to int64) (map[string][]timeSpan, error) {
	rows, err := q.QueryContext(ctx, `SELECT device_id,timestamp,end_timestamp FROM power_events
WHERE type='sleep' AND timestamp<? AND (end_timestamp IS NULL OR end_timestamp>?) ORDER BY device_id,timestamp`, to, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string][]timeSpan)
	for rows.Next() {
		var device string
		var start int64
		var end sql.NullInt64
		if err := rows.Scan(&device, &start, &end); err != nil {
			return nil, err
		}
		span := timeSpan{max(start, from), to}
		if end.Valid {
			span.end = min(end.Int64, to)
		}
		if span.end <= span.start {
			continue
		}
		spans := out[device]
		if len(spans) > 0 && span.start <= spans[len(spans)-1].end {
			spans[len(spans)-1].end = max(spans[len(spans)-1].end, span.end)
		} else {
			spans = append(spans, span)
		}
		out[device] = spans
	}
	return out, rows.Err()
}

func activeSpans(start, end int64, sleeps []timeSpan) []timeSpan {
	var active []timeSpan
	index := sort.Search(len(sleeps), func(i int) bool { return sleeps[i].end > start })
	for ; index < len(sleeps) && sleeps[index].start < end; index++ {
		span := sleeps[index]
		if start < span.start {
			active = append(active, timeSpan{start, span.start})
		}
		start = max(start, span.end)
	}
	if start < end {
		active = append(active, timeSpan{start, end})
	}
	return active
}

type powerStats struct {
	weightedPower float64
	seconds       int64
	peak          sql.NullFloat64
}

func (p *powerStats) observe(s timedReading) {
	if s.state == "discharging" && s.power.Valid && (!p.peak.Valid || s.power.Float64 > p.peak.Float64) {
		p.peak = s.power
	}
}

func (p *powerStats) add(s timedReading, seconds int64) {
	if s.state == "discharging" && s.power.Valid {
		p.weightedPower += s.power.Float64 * float64(seconds)
		p.seconds += seconds
		p.observe(s)
	}
}

func (p powerStats) average() sql.NullFloat64 {
	if p.seconds == 0 {
		return sql.NullFloat64{}
	}
	return battery.Float(p.weightedPower / float64(p.seconds))
}

func dischargeStats(ctx context.Context, q queryer, from, to int64) (sql.NullFloat64, sql.NullFloat64, error) {
	var stats powerStats
	err := walkReadings(ctx, q, from, to, stats.observe, func(s timedReading, start, end int64) {
		stats.add(s, end-start)
	})
	return stats.average(), stats.peak, err
}

type hourKey struct {
	device string
	hour   int64
}

type hourStats struct {
	powerStats
	minimum, maximum sql.NullFloat64
	charging         int64
	healthSum        float64
	healthCount      int64
}

func aggregateHours(ctx context.Context, tx *sql.Tx, cutoff int64) error {
	hours := make(map[hourKey]*hourStats)
	getHour := func(device string, at int64) *hourStats {
		key := hourKey{device, time.Unix(at, 0).Truncate(time.Hour).Unix()}
		if hours[key] == nil {
			hours[key] = &hourStats{}
		}
		return hours[key]
	}
	err := walkReadings(ctx, tx, math.MinInt64, cutoff, func(s timedReading) {
		if s.at >= cutoff {
			return
		}
		hour := getHour(s.device, s.at)
		hour.observe(s)
		if s.percentage.Valid {
			if !hour.minimum.Valid || s.percentage.Float64 < hour.minimum.Float64 {
				hour.minimum = s.percentage
			}
			if !hour.maximum.Valid || s.percentage.Float64 > hour.maximum.Float64 {
				hour.maximum = s.percentage
			}
		}
		if s.health.Valid {
			hour.healthSum += s.health.Float64
			hour.healthCount++
		}
	}, func(s timedReading, start, end int64) {
		for start < end {
			next := min(time.Unix(start, 0).Truncate(time.Hour).Add(time.Hour).Unix(), end)
			hour := getHour(s.device, start)
			hour.add(s, next-start)
			if s.state == "charging" {
				hour.charging += next - start
			}
			start = next
		}
	})
	if err != nil {
		return err
	}
	for key, hour := range hours {
		health := sql.NullFloat64{}
		if hour.healthCount > 0 {
			health = battery.Float(hour.healthSum / float64(hour.healthCount))
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO hourly_aggregates(device_id,hour,min_percentage,max_percentage,
avg_discharge_power,peak_discharge_power,charging_seconds,suspend_drain_percentage,avg_health_percentage)
VALUES(?,?,?,?,?,?,?,0,?) ON CONFLICT(device_id,hour) DO NOTHING`,
			key.device, key.hour, hour.minimum, hour.maximum, hour.average(), hour.peak, hour.charging, health)
		if err != nil {
			return err
		}
	}
	return nil
}
