package collector

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nuzair46/omabat/internal/battery"
)

func TestSysfsProviderNormalizesUnits(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "BAT0")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ac := filepath.Join(root, "AC0")
	if err := os.Mkdir(ac, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ac, "type"), []byte("Mains\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ac, "online"), []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"type": "Battery\n", "present": "1\n", "status": "Discharging\n", "capacity": "73\n",
		"energy_now": "31000000\n", "energy_full": "42000000\n", "energy_full_design": "50000000\n", "power_now": "8500000\n",
		"voltage_now": "12000000\n", "current_now": "700000\n", "temp": "315\n",
		"cycle_count": "224\n", "manufacturer": "Example\n", "model_name": "Pack\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Unix(100, 0)
	s, err := (SysfsProvider{Root: root, Now: func() time.Time { return now }}).Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.DeviceID != "BAT0" || s.State != "discharging" || s.Timestamp != now {
		t.Fatalf("unexpected identity/state: %+v", s)
	}
	if !s.EnergyNow.Valid || s.EnergyNow.Float64 != 31 || s.EnergyRate.Float64 != 8.5 || s.Voltage.Float64 != 12 || s.Temperature.Float64 != 31.5 {
		t.Fatalf("units not normalized: %+v", s)
	}
	if s.Capacity.Float64 != 84 || s.TimeToEmpty.Int64 == 0 {
		t.Fatalf("health or time estimate not derived: %+v", s)
	}
	if !s.ACOnline.Valid || !s.ACOnline.Bool {
		t.Fatalf("AC availability not collected: %+v", s.ACOnline)
	}
}

func TestSysfsProviderDerivesEnergyFromChargeLikeBattop(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "BAT0")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"type": "Battery\n", "present": "1\n", "status": "Charging\n", "capacity": "61\n",
		"charge_now": "2500000\n", "charge_full": "3800000\n", "charge_full_design": "4100000\n",
		"voltage_now": "17000000\n", "voltage_min_design": "15200000\n", "current_now": "3000000\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s, err := (SysfsProvider{Root: root}).Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for name, values := range map[string]struct {
		got  float64
		want float64
	}{
		"current energy": {s.EnergyNow.Float64, 42.5},
		"full energy":    {s.EnergyFull.Float64, 57.76},
		"design energy":  {s.EnergyDesign.Float64, 62.32},
		"power":          {s.EnergyRate.Float64, 51},
	} {
		if math.Abs(values.got-values.want) > 0.0001 {
			t.Fatalf("%s = %v, want %v", name, values.got, values.want)
		}
	}
	if s.TimeToFull.Int64 != 1560 {
		t.Fatalf("time to full = %d, want 1560", s.TimeToFull.Int64)
	}
	if s.Capacity.Float64 < 92.68 || s.Capacity.Float64 > 92.69 {
		t.Fatalf("health = %v, want about 92.68", s.Capacity.Float64)
	}
}

type fixedProvider struct {
	s   battery.Snapshot
	err error
}

func (p fixedProvider) Snapshot(context.Context) (battery.Snapshot, error) { return p.s, p.err }

func TestCompositeProviderEnrichesMissingFields(t *testing.T) {
	primary := battery.Snapshot{DeviceID: "BAT0", Source: "upower", Percentage: battery.Float(50)}
	fallback := battery.Snapshot{DeviceID: "BAT0", Source: "sysfs", CycleCount: battery.Int(12), Temperature: battery.Float(30)}
	got, err := (CompositeProvider{Primary: fixedProvider{s: primary}, Fallback: fixedProvider{s: fallback}}).Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != "upower+sysfs" || got.CycleCount.Int64 != 12 || got.Temperature.Float64 != 30 {
		t.Fatalf("snapshot not enriched: %+v", got)
	}
}

func TestCompositeProviderReplacesZeroUPowerCapacities(t *testing.T) {
	primary := battery.Snapshot{
		DeviceID: "BAT0", Source: "upower",
		EnergyFull: battery.Float(0), EnergyDesign: battery.Float(0), Capacity: battery.Float(0),
	}
	fallback := battery.Snapshot{
		DeviceID: "BAT0", Source: "sysfs",
		EnergyFull: battery.Float(57.26), EnergyDesign: battery.Float(62.32), Capacity: battery.Float(91.88),
	}
	got, err := (CompositeProvider{Primary: fixedProvider{s: primary}, Fallback: fixedProvider{s: fallback}}).Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.EnergyFull.Float64 != 57.26 || got.EnergyDesign.Float64 != 62.32 || got.Capacity.Float64 != 91.88 {
		t.Fatalf("zero UPower capacities were not enriched: %+v", got)
	}
}

func TestCompositeProviderFallsBackWhenUPowerUnavailable(t *testing.T) {
	want := battery.Snapshot{DeviceID: "BAT0", Source: "sysfs"}
	got, err := (CompositeProvider{
		Primary: fixedProvider{err: os.ErrNotExist}, Fallback: fixedProvider{s: want},
	}).Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.DeviceID != want.DeviceID || got.Source != want.Source {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
