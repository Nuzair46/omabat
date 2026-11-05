package storage

import (
	"context"
	"database/sql"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/nuzair46/omabat/internal/battery"
)

func recordReading(t *testing.T, r *Repository, at time.Time, state string, power float64, cadence time.Duration) {
	t.Helper()
	s := snap(at, state, 50)
	s.EnergyRate = battery.Float(power)
	s.SampleInterval = cadence
	if _, err := r.RecordSnapshot(context.Background(), s); err != nil {
		t.Fatal(err)
	}
}

func assertPower(t *testing.T, value sql.NullFloat64, want float64) {
	t.Helper()
	if !value.Valid || math.Abs(value.Float64-want) > 1e-9 {
		t.Fatalf("power = %+v, want %v", value, want)
	}
}

func TestStatisticsIndependentOfSamplingDensity(t *testing.T) {
	start := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	for _, cadence := range []time.Duration{3 * time.Second, 120 * time.Second} {
		for _, withDaemon := range []bool{false, true} {
			t.Run(cadence.String()+"/daemon="+map[bool]string{true: "yes", false: "no"}[withDaemon], func(t *testing.T) {
				r := testRepo(t)
				// Twenty minutes charging, twenty at 6 W, twenty at 12 W.
				for offset := time.Duration(0); offset <= time.Hour; offset += cadence {
					state, power := "charging", 0.0
					if offset >= 40*time.Minute {
						state, power = "discharging", 12
					} else if offset >= 20*time.Minute {
						state, power = "discharging", 6
					}
					recordReading(t, r, start.Add(offset), state, power, cadence)
					if withDaemon && offset%(120*time.Second) == 0 {
						recordReading(t, r, start.Add(offset), state, power, 120*time.Second)
					}
				}
				avg, peak, err := dischargeStats(context.Background(), r.DB, start.Unix(), start.Add(time.Hour).Unix())
				if err != nil {
					t.Fatal(err)
				}
				assertPower(t, avg, 9)
				assertPower(t, peak, 12)
				if err := r.Maintain(context.Background(), start.Add(11*24*time.Hour)); err != nil {
					t.Fatal(err)
				}
				var charging int64
				if err := r.DB.QueryRow(`SELECT charging_seconds,avg_discharge_power,peak_discharge_power FROM hourly_aggregates WHERE device_id='BAT0' AND hour=?`, start.Unix()).Scan(&charging, &avg, &peak); err != nil {
					t.Fatal(err)
				}
				if charging != 1200 {
					t.Fatalf("charging seconds = %d, want 1200", charging)
				}
				assertPower(t, avg, 9)
				assertPower(t, peak, 12)
			})
		}
	}
}

func TestDischargeStatsWeightsUnevenSamplingAndClipsRange(t *testing.T) {
	r := testRepo(t)
	start := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	// The first four minutes are sparse, the last two dense.
	for _, second := range []int{0, 120, 240, 243, 246, 300, 360} {
		power := 6.0
		if second >= 240 {
			power = 12
		}
		recordReading(t, r, start.Add(time.Duration(second)*time.Second), "discharging", power, 120*time.Second)
	}
	avg, peak, err := dischargeStats(context.Background(), r.DB, start.Add(time.Minute).Unix(), start.Add(5*time.Minute).Unix())
	if err != nil {
		t.Fatal(err)
	}
	assertPower(t, avg, 7.5) // 3 minutes at 6 W, 1 minute at 12 W.
	assertPower(t, peak, 12)
}

func TestStatisticsGapPolicy(t *testing.T) {
	start := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		cadence time.Duration
		want    int64
	}{
		{"legacy gap is unknown", 0, 0},
		{"custom cadence is supported", 30 * time.Minute, 1800},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := testRepo(t)
			recordReading(t, r, start, "charging", 0, tc.cadence)
			recordReading(t, r, start.Add(30*time.Minute), "discharging", 9, tc.cadence)
			if err := r.Maintain(context.Background(), start.Add(11*24*time.Hour)); err != nil {
				t.Fatal(err)
			}
			var charging int64
			if err := r.DB.QueryRow(`SELECT charging_seconds FROM hourly_aggregates WHERE hour=?`, start.Unix()).Scan(&charging); err != nil {
				t.Fatal(err)
			}
			if charging != tc.want {
				t.Fatalf("charging = %d, want %d", charging, tc.want)
			}
		})
	}
}

func TestStatisticsDoesNotExtrapolateLastReading(t *testing.T) {
	r := testRepo(t)
	start := time.Now().Add(-time.Hour).Truncate(time.Second)
	recordReading(t, r, start, "discharging", 12, 120*time.Second)
	avg, peak, err := r.DischargeStats(context.Background(), start)
	if err != nil {
		t.Fatal(err)
	}
	if avg.Valid {
		t.Fatalf("one reading must not invent duration: %+v", avg)
	}
	assertPower(t, peak, 12)
}

func TestStatisticsDeduplicatesTimestampsAndSeparatesDevices(t *testing.T) {
	r := testRepo(t)
	start := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	// Insert out of order; the last inserted value wins at the same second.
	recordReading(t, r, start.Add(2*time.Minute), "discharging", 12, 120*time.Second)
	recordReading(t, r, start, "discharging", 90, 120*time.Second)
	recordReading(t, r, start, "discharging", 6, 120*time.Second)
	other := snap(start.Add(time.Minute), "discharging", 40)
	other.DeviceID = "BAT1"
	other.EnergyRate = battery.Float(10)
	if _, err := r.RecordSnapshot(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	avg, peak, err := dischargeStats(context.Background(), r.DB, start.Unix(), start.Add(2*time.Minute).Unix())
	if err != nil {
		t.Fatal(err)
	}
	assertPower(t, avg, 6)   // BAT1 has no interval and must not terminate BAT0's.
	assertPower(t, peak, 90) // Measured peaks are preserved, even at duplicate timestamps.
}

func TestStatisticsSubtractsSleepIncludingRangeOverlap(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()
	start := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	for offset := time.Duration(0); offset <= 10*time.Minute; offset += time.Minute {
		power := 6.0
		if offset >= 5*time.Minute {
			power = 12
		}
		recordReading(t, r, start.Add(offset), "discharging", power, time.Minute)
	}
	// Deliberately include readings inside sleep, as a second collector might.
	if _, err := r.DB.Exec(`INSERT INTO power_events(device_id,type,timestamp,end_timestamp) VALUES('BAT0','sleep',?,?)`, start.Add(2*time.Minute).Unix(), start.Add(7*time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	avg, _, err := dischargeStats(ctx, r.DB, start.Add(3*time.Minute).Unix(), start.Add(9*time.Minute).Unix())
	if err != nil {
		t.Fatal(err)
	}
	assertPower(t, avg, 12)
	if err := r.Maintain(ctx, start.Add(11*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := r.DB.QueryRow(`SELECT avg_discharge_power FROM hourly_aggregates WHERE hour=?`, start.Unix()).Scan(&avg); err != nil {
		t.Fatal(err)
	}
	assertPower(t, avg, 9.6) // 2 active minutes at 6 W, 3 at 12 W.
}

func TestChargingIntervalsSplitHoursAndExcludeSleep(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()
	start := time.Date(2026, 6, 1, 12, 59, 0, 0, time.UTC)
	for offset := time.Duration(0); offset <= 2*time.Minute; offset += 30 * time.Second {
		recordReading(t, r, start.Add(offset), "charging", 0, time.Minute)
	}
	if _, err := r.DB.Exec(`INSERT INTO power_events(device_id,type,timestamp,end_timestamp) VALUES('BAT0','sleep',?,?)`, start.Add(30*time.Second).Unix(), start.Add(90*time.Second).Unix()); err != nil {
		t.Fatal(err)
	}
	if err := r.Maintain(ctx, start.Add(11*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	for _, hour := range []time.Time{start.Truncate(time.Hour), start.Truncate(time.Hour).Add(time.Hour)} {
		var seconds int64
		if err := r.DB.QueryRow(`SELECT charging_seconds FROM hourly_aggregates WHERE hour=?`, hour.Unix()).Scan(&seconds); err != nil {
			t.Fatal(err)
		}
		if seconds != 30 {
			t.Fatalf("hour %s: charging = %d, want 30", hour, seconds)
		}
	}
}

func TestMaintainUsesWholeHoursAndPreservesBoundaryContext(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()
	boundary := time.Date(2026, 6, 1, 13, 0, 0, 0, time.UTC)
	for _, offset := range []time.Duration{-2 * time.Second, time.Second, 2 * time.Second} {
		recordReading(t, r, boundary.Add(offset), "charging", 0, 3*time.Second)
	}
	first := boundary.Add(10*24*time.Hour + 30*time.Minute)
	if err := r.Maintain(ctx, first); err != nil {
		t.Fatal(err)
	}
	var seconds, count int64
	if err := r.DB.QueryRow(`SELECT charging_seconds FROM hourly_aggregates WHERE hour=?`, boundary.Add(-time.Hour).Unix()).Scan(&seconds); err != nil {
		t.Fatal(err)
	}
	if seconds != 2 {
		t.Fatalf("preceding hour = %d seconds, want 2", seconds)
	}
	if err := r.DB.QueryRow(`SELECT COUNT(*) FROM hourly_aggregates WHERE hour=?`, boundary.Unix()).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial hour was aggregated: count=%d err=%v", count, err)
	}
	// The next cleanup must use the predecessor to account for the first
	// second of the new hour, but must not overwrite the preceding hour.
	for i := 0; i < 2; i++ {
		if err := r.Maintain(ctx, first.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	for _, hour := range []time.Time{boundary.Add(-time.Hour), boundary} {
		if err := r.DB.QueryRow(`SELECT charging_seconds FROM hourly_aggregates WHERE hour=?`, hour.Unix()).Scan(&seconds); err != nil {
			t.Fatal(err)
		}
		if seconds != 2 {
			t.Fatalf("hour %s = %d seconds, want 2", hour, seconds)
		}
	}
	if err := r.DB.QueryRow(`SELECT COUNT(*) FROM battery_samples`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("expected one boundary reading: count=%d err=%v", count, err)
	}
}

func TestMaintainPreservesExistingAggregatesAndHealthWeight(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()
	start := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	first, last := snap(start, "charging", 50), snap(start.Add(time.Minute), "charging", 51)
	first.Capacity, last.Capacity = battery.Float(90), battery.Float(94)
	for _, s := range []battery.Snapshot{first, last} {
		if _, err := r.RecordSnapshot(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Maintain(ctx, start.Add(11*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	// Simulate an old aggregate that must not be recalculated from its one
	// retained boundary reading.
	if _, err := r.DB.Exec(`UPDATE hourly_aggregates SET charging_seconds=99`); err != nil {
		t.Fatal(err)
	}
	if err := r.Maintain(ctx, start.Add(12*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var seconds int64
	if err := r.DB.QueryRow(`SELECT charging_seconds FROM hourly_aggregates`).Scan(&seconds); err != nil || seconds != 99 {
		t.Fatalf("existing aggregate changed: seconds=%d err=%v", seconds, err)
	}
	avg, _, _, err := r.LongTermHealth(ctx, "BAT0")
	if err != nil {
		t.Fatal(err)
	}
	assertPower(t, avg, 92) // Boundary context must not count as another hour.
}

func TestSampleIntervalRoundTripAndLegacyMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// Build the actual pre-change v3 layout, with a persisted reading.
	_, err = db.Exec(schema + `
ALTER TABLE battery_samples ADD COLUMN power_profile TEXT;
ALTER TABLE battery_samples ADD COLUMN ac_online INTEGER;
INSERT INTO schema_migrations VALUES(2,0),(3,0);
INSERT INTO battery_devices(id,native_path,first_seen,last_seen) VALUES('BAT0','BAT0',0,0);
INSERT INTO battery_samples(device_id,timestamp,source,present,state) VALUES('BAT0',1,'test',1,'charging');`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	s, err := r.Latest(context.Background())
	if err != nil || s.SampleInterval != battery.DefaultSampleInterval {
		t.Fatalf("legacy cadence: snapshot=%+v err=%v", s, err)
	}
	recordReading(t, r, time.Unix(2, 0), "charging", 0, 30*time.Minute)
	s, err = r.Latest(context.Background())
	if err != nil || s.SampleInterval != 30*time.Minute {
		t.Fatalf("custom cadence: interval=%v err=%v", s.SampleInterval, err)
	}
}

func TestConcurrentCollectorsAndMaintenance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	ctx := context.Background()
	start := time.Now().Truncate(time.Second)
	done := make(chan error, 3)
	for _, r := range []*Repository{first, second} {
		go func() {
			for i := 0; i < 30; i++ {
				if _, err := r.RecordSnapshot(ctx, snap(start.Add(time.Duration(i)*time.Second), "charging", 50)); err != nil {
					done <- err
					return
				}
			}
			done <- nil
		}()
	}
	go func() { done <- first.Maintain(ctx, start) }()
	for i := 0; i < 3; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := first.DB.QueryRow(`SELECT COUNT(*) FROM battery_samples`).Scan(&count); err != nil || count != 60 {
		t.Fatalf("lost collector readings: count=%d err=%v", count, err)
	}
}

func TestOpenEscapesDatabasePathAndSupportsMemory(t *testing.T) {
	for _, path := range []string{filepath.Join(t.TempDir(), "battery ? #.db"), ":memory:"} {
		t.Run(path, func(t *testing.T) {
			r, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			recordReading(t, r, time.Unix(1000, 0), "discharging", 6, 3*time.Second)
			if _, err := r.Latest(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := r.Maintain(context.Background(), time.Unix(1000, 0).Add(11*24*time.Hour)); err != nil {
				t.Fatal(err)
			}
		})
	}
}
