package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/nuzair46/omabat/internal/battery"
)

func testRepo(t *testing.T) *Repository {
	t.Helper()
	r, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func snap(at time.Time, state string, pct float64) battery.Snapshot {
	return battery.Snapshot{
		DeviceID: "BAT0", NativePath: "BAT0", Timestamp: at, Source: "test", Present: true,
		State: state, Percentage: battery.Float(pct), EnergyRate: battery.Float(8), Capacity: battery.Float(91),
		PowerProfile: battery.String("balanced"), ACOnline: battery.Bool(state == "charging" || state == "full"),
	}
}

func TestTransitionEventsAreNotDuplicated(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()
	now := time.Now()
	states := []string{"discharging", "charging", "charging", "full", "full", "discharging"}
	var types []string
	for i, state := range states {
		events, err := r.RecordSnapshot(ctx, snap(now.Add(time.Duration(i)*time.Minute), state, 50+float64(i)))
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range events {
			types = append(types, event.Type)
		}
	}
	want := []string{"plugged", "full", "unplugged"}
	if len(types) != len(want) {
		t.Fatalf("got event types %v, want %v", types, want)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("got event types %v, want %v", types, want)
		}
	}
}

func TestSleepResumeCalculatesDrain(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()
	start := time.Unix(1000, 0)
	if err := r.RecordSleep(ctx, snap(start, "discharging", 80)); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordResume(ctx, snap(start.Add(2*time.Hour), "discharging", 77.5)); err != nil {
		t.Fatal(err)
	}
	events, err := r.Events(ctx, time.Unix(0, 0), 10)
	if err != nil {
		t.Fatal(err)
	}
	var sleep battery.Event
	for _, event := range events {
		if event.Type == "sleep" {
			sleep = event
		}
	}
	if !sleep.PercentageDelta.Valid || sleep.PercentageDelta.Float64 != -2.5 || sleep.DurationSeconds.Int64 != 7200 {
		t.Fatalf("unexpected sleep event: %+v", sleep)
	}
}

func TestMaintainAggregatesPrunesSamplesAndKeepsEvents(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Hour)
	old := now.Add(-11 * 24 * time.Hour)
	if _, err := r.RecordSnapshot(ctx, snap(old, "discharging", 70)); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordSleep(ctx, snap(old.Add(time.Minute), "discharging", 70)); err != nil {
		t.Fatal(err)
	}
	if err := r.Maintain(ctx, now); err != nil {
		t.Fatal(err)
	}
	var samples, aggregates, events int
	for query, target := range map[string]*int{
		"SELECT COUNT(*) FROM battery_samples":   &samples,
		"SELECT COUNT(*) FROM hourly_aggregates": &aggregates,
		"SELECT COUNT(*) FROM power_events":      &events,
	} {
		if err := r.DB.QueryRow(query).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if samples != 0 || aggregates == 0 || events == 0 {
		t.Fatalf("samples=%d aggregates=%d events=%d", samples, aggregates, events)
	}
}

func TestValidateRange(t *testing.T) {
	for _, valid := range []string{"", "24h", "3d", "7d"} {
		if _, err := ValidateRange(valid); err != nil {
			t.Fatalf("%s should be valid: %v", valid, err)
		}
	}
	if _, err := ValidateRange("8d"); err == nil {
		t.Fatal("expected ranges longer than seven days to be rejected")
	}
}

func TestConcurrentReaderAndCollector(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()
	now := time.Now()
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 20; i++ {
			if _, err := r.RecordSnapshot(ctx, snap(now.Add(time.Duration(i)*time.Second), "discharging", float64(i))); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	for i := 0; i < 20; i++ {
		_, err := r.Latest(ctx)
		if err != nil && err != sql.ErrNoRows {
			t.Fatal(err)
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSchemaMigrationRecorded(t *testing.T) {
	r := testRepo(t)
	var version int
	if err := r.DB.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != currentSchema {
		t.Fatalf("got schema version %d, want %d", version, currentSchema)
	}
}

func TestPowerProfilePersistsWithSample(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()
	want := snap(time.Now(), "discharging", 50)
	want.PowerProfile = battery.String("performance")
	if _, err := r.RecordSnapshot(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := r.Latest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !got.PowerProfile.Valid || got.PowerProfile.String != "performance" {
		t.Fatalf("got profile %+v", got.PowerProfile)
	}
}

func TestACAvailabilityPersistsWithSample(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()
	want := snap(time.Now(), "full", 100)
	want.ACOnline = battery.Bool(true)
	if _, err := r.RecordSnapshot(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := r.Latest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !got.ACOnline.Valid || !got.ACOnline.Bool {
		t.Fatalf("got AC availability %+v", got.ACOnline)
	}
}

func TestLongTermHealthIncludesRecentRawSamples(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()
	first := snap(time.Now().Add(-time.Hour), "discharging", 50)
	first.Capacity = battery.Float(90)
	second := snap(time.Now(), "charging", 60)
	second.Capacity = battery.Float(94)
	for _, sample := range []battery.Snapshot{first, second} {
		if _, err := r.RecordSnapshot(ctx, sample); err != nil {
			t.Fatal(err)
		}
	}
	avg, low, high, err := r.LongTermHealth(ctx, "BAT0")
	if err != nil {
		t.Fatal(err)
	}
	if !avg.Valid || avg.Float64 != 92 || low.Float64 != 90 || high.Float64 != 94 {
		t.Fatalf("unexpected long-term health: avg=%+v low=%+v high=%+v", avg, low, high)
	}
}
