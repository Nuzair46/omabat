package collector

import (
	"context"
	"errors"
	"testing"

	"github.com/nuzair46/omabat/internal/battery"
)

func TestPowerProfileProviderCollectsCurrentProfile(t *testing.T) {
	provider := PowerProfileProvider{
		Battery: fixedProvider{s: battery.Snapshot{DeviceID: "BAT0"}},
		Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name != "powerprofilesctl" || len(args) != 1 || args[0] != "get" {
				t.Fatalf("unexpected command: %s %v", name, args)
			}
			return []byte("performance\n"), nil
		},
	}
	s, err := provider.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !s.PowerProfile.Valid || s.PowerProfile.String != "performance" {
		t.Fatalf("unexpected profile: %+v", s.PowerProfile)
	}
}

func TestPowerProfileProviderDoesNotFailBatteryCollection(t *testing.T) {
	provider := PowerProfileProvider{
		Battery: fixedProvider{s: battery.Snapshot{DeviceID: "BAT0"}},
		Run: func(context.Context, string, ...string) ([]byte, error) {
			return nil, errors.New("power profiles unavailable")
		},
	}
	s, err := provider.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.PowerProfile.Valid {
		t.Fatalf("expected unavailable profile, got %+v", s.PowerProfile)
	}
}

func TestNormalizePowerProfile(t *testing.T) {
	for input, want := range map[string]string{
		"power-saver\n": "power-saver",
		"powersaving":   "power-saver",
		"balanced":      "balanced",
		"performance":   "performance",
		"custom":        "",
	} {
		if got := normalizePowerProfile(input); got != want {
			t.Fatalf("normalizePowerProfile(%q)=%q, want %q", input, got, want)
		}
	}
}
