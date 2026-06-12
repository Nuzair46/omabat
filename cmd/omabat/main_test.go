package main

import (
	"strings"
	"testing"
	"time"

	"github.com/nuzair46/omabat/internal/battery"
)

func TestRunRejectsUnsupportedRange(t *testing.T) {
	if err := run([]string{"--range", "8d"}); err == nil || !strings.Contains(err.Error(), "maximum 7 days") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDevelopmentVersionIsSet(t *testing.T) {
	if version == "" {
		t.Fatal("version must not be empty")
	}
}

func TestFormatHealthHandlesMissingFields(t *testing.T) {
	out := formatHealth(battery.Snapshot{})
	if !strings.Contains(out, "Health:                unavailable") || strings.Count(out, "\n") != 3 {
		t.Fatalf("unexpected health output:\n%s", out)
	}
}

func TestFormatHealthIncludesOnlyAvailableDetails(t *testing.T) {
	out := formatHealth(battery.Snapshot{
		Manufacturer: battery.String("Razer"), Model: battery.String("Blade"), Voltage: battery.Float(17.2),
		Temperature: battery.Float(30), Source: "upower+sysfs",
	})
	for _, want := range []string{"Manufacturer:          Razer", "Model:                 Blade"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in health output:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"Temperature:", "Voltage:", "Source:", "Cycles:", "Serial:"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("did not expect %q in health output:\n%s", unwanted, out)
		}
	}
}

func TestFormatHealthEnergyRateRequiresAC(t *testing.T) {
	s := battery.Snapshot{EnergyRate: battery.Float(20), ACOnline: battery.Bool(false)}
	if out := formatHealth(s); strings.Contains(out, "Energy rate:") {
		t.Fatalf("energy rate shown without AC:\n%s", out)
	}
	s.ACOnline = battery.Bool(true)
	if out := formatHealth(s); !strings.Contains(out, "Energy rate:           20.00 W") {
		t.Fatalf("energy rate hidden with AC:\n%s", out)
	}
}

func TestFormatHealthShowsOnlyRequestedCapacityValues(t *testing.T) {
	out := formatHealth(battery.Snapshot{
		Capacity: battery.Float(91.88), EnergyDesign: battery.Float(62.32), EnergyFull: battery.Float(57.26),
	})
	for _, want := range []string{"Health:                91.9%", "Designed capacity:     62.32 Wh", "Current full capacity: 57.26 Wh"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in output:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"Long-term", "State:", "Voltage:", "Cycles:"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("did not expect %q in output:\n%s", unwanted, out)
		}
	}
}

func TestFormatWaybarShowsDaemonAndLatestSample(t *testing.T) {
	out := formatWaybar(true, battery.Snapshot{
		Timestamp: time.Date(2026, 6, 13, 10, 20, 30, 0, time.UTC),
		State:     "charging", Percentage: battery.Float(80), EnergyRate: battery.Float(42), ACOnline: battery.Bool(true),
	})
	if out.Class != "active" || out.Text != "󰂊" {
		t.Fatalf("unexpected waybar state: %+v", out)
	}
	for _, want := range []string{"Omabat daemon: active", "Battery: 80.0% charging", "Energy rate: 42.00 W", "Last sample: Jun 13 10:20:30"} {
		if !strings.Contains(out.Tooltip, want) {
			t.Fatalf("expected %q in tooltip: %s", want, out.Tooltip)
		}
	}
}

func TestWaybarBatteryIconReflectsStateAndLevel(t *testing.T) {
	tests := []struct {
		state      string
		percentage float64
		want       string
	}{
		{state: "discharging", percentage: 5, want: "󰂎"},
		{state: "discharging", percentage: 75, want: "󰂀"},
		{state: "charging", percentage: 75, want: "󰢞"},
		{state: "charging", percentage: 100, want: "󰂅"},
		{state: "full", percentage: 100, want: "󰁹"},
	}
	for _, test := range tests {
		s := battery.Snapshot{State: test.state, Percentage: battery.Float(test.percentage)}
		if got := waybarBatteryIcon(s); got != test.want {
			t.Fatalf("waybarBatteryIcon(%s, %.0f) = %q, want %q", test.state, test.percentage, got, test.want)
		}
	}
	if got := waybarBatteryIcon(battery.Snapshot{}); got != "󰂑" {
		t.Fatalf("missing battery data icon = %q, want unknown battery icon", got)
	}
}
