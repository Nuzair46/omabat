package tui

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/nuzair46/omabat/internal/battery"
)

type liveCollectorStub struct {
	calls int
	err   error
}

func (c *liveCollectorStub) Collect(context.Context) (battery.Snapshot, []battery.Event, error) {
	c.calls++
	return battery.Snapshot{}, nil, c.err
}

func TestModelRendersAtCommonWidths(t *testing.T) {
	for _, width := range []int{30, 80, 140} {
		m := Model{
			Width: width, Height: 24, Range: 24 * time.Hour,
			Latest: battery.Snapshot{DeviceID: "BAT0", Timestamp: time.Now(), State: "discharging",
				Percentage: battery.Float(75), PowerProfile: battery.String("balanced")},
			Samples: []battery.Snapshot{{Timestamp: time.Now().Add(-time.Hour), State: "discharging",
				Percentage: battery.Float(70), PowerProfile: battery.String("power-saver")}},
		}
		m2, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		view := m2.(Model).View()
		if !strings.Contains(view, "History") || !strings.Contains(view, "75.0%") {
			t.Fatalf("width %d rendered unexpected view: %s", width, view)
		}
	}
}

func TestHistoryRendersThemeBarsAndIconTimelines(t *testing.T) {
	now := time.Date(2026, 6, 12, 18, 0, 0, 0, time.UTC)
	view := renderGraph(
		[]battery.Snapshot{
			{Timestamp: now.Add(-50 * time.Minute), State: "discharging", Percentage: battery.Float(50), PowerProfile: battery.String("power-saver")},
			{Timestamp: now.Add(-30 * time.Minute), State: "charging", Percentage: battery.Float(75), PowerProfile: battery.String("balanced")},
			{Timestamp: now.Add(-10 * time.Minute), State: "full", Percentage: battery.Float(100), PowerProfile: battery.String("performance")},
		},
		[]battery.Event{{Type: "sleep", Timestamp: now.Add(-time.Minute), EndTimestamp: battery.Int(now.Unix()), PercentageDelta: battery.Float(-1)}},
		40, 6, now.Add(-time.Hour), now,
	)
	for _, want := range []string{
		" AC/S│", "MODE │", "100%", " 75%", " 50%", " 25%", "  0%",
		"17:00", "17:30", "18:00", "▐",
		iconPowerSaver, iconBalanced, iconPerformance, iconPlugged, iconSleep,
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected %q in graph:\n%s", want, view)
		}
	}
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "100% │") || strings.Contains(line, " 50% │") || strings.Contains(line, "  0% │") {
			if strings.Contains(line, "\x1b[") {
				t.Fatalf("battery bars must use terminal theme color:\n%s", line)
			}
			if strings.Contains(line, "█") {
				t.Fatalf("battery bars must use half-width blocks for spacing:\n%s", line)
			}
		}
	}
}

func TestSystemTimelineAppearsAbovePowerProfileTimeline(t *testing.T) {
	now := time.Now()
	view := renderGraph(nil, nil, 30, 5, now.Add(-time.Hour), now)
	system := strings.Index(view, " AC/S│")
	power := strings.Index(view, "MODE │")
	if system < 0 || power < 0 || system > power {
		t.Fatalf("system timeline must appear above power timeline:\n%s", view)
	}
}

func TestTUILiveCollectionStartsImmediatelyAndRepeats(t *testing.T) {
	collector := &liveCollectorStub{}
	m := New(nil, collector, 24*time.Hour)
	if !m.Loading {
		t.Fatal("new model should show a loading state until history is read")
	}
	cmd := m.collectNow()
	if cmd == nil {
		t.Fatal("expected immediate collection command")
	}
	if msg, ok := cmd().(collected); !ok || msg.err != nil || collector.calls != 1 {
		t.Fatalf("unexpected immediate collection: msg=%#v calls=%d", msg, collector.calls)
	}
	updated, cmd := m.Update(liveTick(time.Now()))
	if cmd == nil {
		t.Fatal("expected collection and next-tick batch")
	}
	if updated.(Model).Range != 24*time.Hour {
		t.Fatal("live tick changed selected range")
	}
}

func TestACTimelineTracksChargingAndFullStates(t *testing.T) {
	start := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	columns := acColumns([]battery.Snapshot{
		{Timestamp: start, State: "discharging"},
		{Timestamp: start.Add(30 * time.Minute), State: "charging"},
		{Timestamp: start.Add(45 * time.Minute), State: "full"},
	}, 5, start, start.Add(time.Hour))
	want := []bool{false, false, true, true, true}
	for i := range want {
		if columns[i] != want[i] {
			t.Fatalf("AC column %d=%t, want %t; all=%v", i, columns[i], want[i], columns)
		}
	}
}

func TestRangeKeysSwitchAndReload(t *testing.T) {
	m := Model{Range: 24 * time.Hour}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	got := updated.(Model)
	if got.Range != 3*24*time.Hour || cmd == nil {
		t.Fatalf("range=%s cmd=%v", got.Range, cmd)
	}
	updated, cmd = got.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	got = updated.(Model)
	if got.Range != 7*24*time.Hour || cmd == nil {
		t.Fatalf("range=%s cmd=%v", got.Range, cmd)
	}
	updated, cmd = got.Update(tea.KeyMsg{Type: tea.KeyLeft})
	got = updated.(Model)
	if got.Range != 3*24*time.Hour || cmd == nil {
		t.Fatalf("left arrow range=%s cmd=%v", got.Range, cmd)
	}
	updated, cmd = got.Update(tea.KeyMsg{Type: tea.KeyRight})
	got = updated.(Model)
	if got.Range != 7*24*time.Hour || cmd == nil {
		t.Fatalf("right arrow range=%s cmd=%v", got.Range, cmd)
	}
}

func TestTabShortcutsSelectViews(t *testing.T) {
	m := Model{Tab: 0}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	if got := updated.(Model).Tab; got != 1 {
		t.Fatalf("h selected tab %d, want Health", got)
	}
	updated, _ = updated.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	if got := updated.(Model).Tab; got != 0 {
		t.Fatalf("g selected tab %d, want History", got)
	}
}

func TestStaleRangeLoadIsIgnored(t *testing.T) {
	m := Model{Range: 7 * 24 * time.Hour, Latest: battery.Snapshot{DeviceID: "current"}}
	updated, _ := m.Update(loaded{
		historyRange: 24 * time.Hour,
		latest:       battery.Snapshot{DeviceID: "stale"},
	})
	if got := updated.(Model).Latest.DeviceID; got != "current" {
		t.Fatalf("stale range load replaced current data: %s", got)
	}
}

func TestShortTimeLabelsIncludeDatesForLongRanges(t *testing.T) {
	start := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)
	view := renderGraph(nil, nil, 40, 6, start, start.Add(7*24*time.Hour))
	for _, want := range []string{"06/05 00h", "06/08 12h", "06/12 00h"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected %q in graph:\n%s", want, view)
		}
	}
}

func TestPowerProfilesUseDistinctIconColors(t *testing.T) {
	saver := profileStyle("power-saver").GetForeground()
	balanced := profileStyle("balanced").GetForeground()
	performance := profileStyle("performance").GetForeground()
	if saver == balanced || balanced == performance || saver == performance {
		t.Fatalf("power profile icon colors must be distinct: saver=%v balanced=%v performance=%v", saver, balanced, performance)
	}
}

func TestSystemTimelinePrioritizesSuspendOverAC(t *testing.T) {
	view := renderSystemTimeline([]bool{true, true, false}, []bool{false, true, true})
	want := " AC/S│" + acStyle.Render(iconPlugged) + sleepStyle.Render(iconSleep) + sleepStyle.Render(iconSleep)
	if view != want {
		t.Fatalf("got %q, want %q", view, want)
	}
}

func TestBarChartFitsNarrowWidth(t *testing.T) {
	start := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)
	view := renderGraph(nil, nil, 20, 5, start, start.Add(7*24*time.Hour))
	for _, line := range strings.Split(view, "\n") {
		if got := lipgloss.Width(line); got > 20 {
			t.Fatalf("line width %d exceeds chart width:\n%s", got, view)
		}
	}
}

func TestHistoryFitsStandardTerminalHeight(t *testing.T) {
	now := time.Now()
	events := make([]battery.Event, 12)
	for i := range events {
		events[i] = battery.Event{Type: "plugged", Timestamp: now.Add(-time.Duration(i) * time.Hour)}
	}
	m := Model{
		Width: 80, Height: 24, Range: 7 * 24 * time.Hour,
		Latest: battery.Snapshot{DeviceID: "BAT0", Timestamp: now, State: "discharging",
			Percentage: battery.Float(75), PowerProfile: battery.String("balanced")},
		Events: events,
	}
	if lines := strings.Count(m.View(), "\n") + 1; lines > 24 {
		t.Fatalf("rendered %d lines into a 24-line terminal", lines)
	}
}

func TestViewUsesBtopStylePanelsAndRangeSelector(t *testing.T) {
	m := Model{
		Width: 80, Height: 24, Range: 3 * 24 * time.Hour,
		Latest: battery.Snapshot{DeviceID: "BAT0", Timestamp: time.Now(), State: "discharging",
			Percentage: battery.Float(75), PowerProfile: battery.String("balanced")},
	}
	view := m.View()
	for _, want := range []string{"╭", "╰", "live battery", "battery level · 3d", "[3d]", "←", "→", "[g]", "[h]", "live 3s", "g/h tabs", "←/→ range"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected %q in view:\n%s", want, view)
		}
	}
}

func TestHistorySummaryPrioritizesLiveAndPowerData(t *testing.T) {
	m := Model{Width: 80, Height: 24, Range: 24 * time.Hour,
		Latest: battery.Snapshot{
			Timestamp: time.Date(2026, 6, 13, 10, 20, 30, 0, time.UTC), State: "charging",
			Percentage: battery.Float(80), ACOnline: battery.Bool(true), PowerProfile: battery.String("balanced"),
			EnergyRate: battery.Float(42), Capacity: battery.Float(91), CycleCount: battery.Int(200),
		},
		AvgPower: battery.Float(8), PeakPower: battery.Float(20),
		Samples: make([]battery.Snapshot, 12),
	}
	view := m.historySummary(80)
	for _, want := range []string{"80.0%", "charging", "AC", "balanced", "updated 10:20:30", "charge rate 42.00 W", "avg draw 8.00 W", "peak draw 20.00 W", "12 samples"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected %q in summary:\n%s", want, view)
		}
	}
	for _, unwanted := range []string{"health", "cycles"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("did not expect %q in summary:\n%s", unwanted, view)
		}
	}
}

func TestRecentEventExplainsSuspendDrainAndDuration(t *testing.T) {
	view := formatEvent(battery.Event{
		Type: "sleep", Timestamp: time.Date(2026, 6, 13, 1, 0, 0, 0, time.UTC),
		Percentage: battery.Float(80), PercentageDelta: battery.Float(-2.5), DurationSeconds: battery.Int(7200),
	})
	for _, want := range []string{"suspend", "80.0%", "2h 00m", "drain 2.5%"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected %q in event: %s", want, view)
		}
	}
}

func TestRecentEventCallsPositiveSuspendChangeAGain(t *testing.T) {
	view := formatEvent(battery.Event{Type: "sleep", PercentageDelta: battery.Float(1.5)})
	if !strings.Contains(view, "gain 1.5%") || strings.Contains(view, "drain") {
		t.Fatalf("unexpected positive suspend change: %s", view)
	}
}

func TestFirstSampleStateUsesFriendlyMessage(t *testing.T) {
	m := Model{Width: 80, Height: 24, Range: 24 * time.Hour, Err: sql.ErrNoRows}
	view := m.View()
	if !strings.Contains(view, "Collecting the first battery sample") || strings.Contains(view, "sql: no rows") {
		t.Fatalf("unexpected first-sample view:\n%s", view)
	}
}

func TestLoadingStateAvoidsFakeUnknownSummary(t *testing.T) {
	m := New(nil, nil, 24*time.Hour)
	view := m.View()
	if !strings.Contains(view, "Loading battery history") || strings.Contains(view, "0 samples") {
		t.Fatalf("unexpected loading view:\n%s", view)
	}
}

func TestMidWidthHistorySummaryAndLegendDoNotClipUsefulText(t *testing.T) {
	m := Model{Width: 50, Height: 24, Range: 7 * 24 * time.Hour,
		Latest: battery.Snapshot{
			Timestamp: time.Now(), State: "charging", Percentage: battery.Float(80),
			ACOnline: battery.Bool(true), PowerProfile: battery.String("balanced"), EnergyRate: battery.Float(40),
		},
		AvgPower: battery.Float(8), PeakPower: battery.Float(20),
	}
	view := m.View()
	for _, want := range []string{"charge rate 40.00 W", "avg draw 8.00 W", "peak draw 20.00 W", "perf"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected %q in mid-width history:\n%s", want, view)
		}
	}
	for _, line := range strings.Split(view, "\n") {
		if got := lipgloss.Width(line); got > 50 {
			t.Fatalf("line width %d exceeds 50:\n%s", got, view)
		}
	}
}

func TestMinimumWidthHistoryKeepsPeakAndLegendVisible(t *testing.T) {
	m := Model{Width: 30, Height: 24, Range: 7 * 24 * time.Hour,
		Latest:   battery.Snapshot{Timestamp: time.Now(), State: "discharging", Percentage: battery.Float(70), EnergyRate: battery.Float(9)},
		AvgPower: battery.Float(8), PeakPower: battery.Float(20),
	}
	view := m.View()
	for _, want := range []string{"avg draw 8.00 W", "peak draw 20.00 W", iconPowerSaver + " S", iconBalanced + " B", iconPerformance + " P"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected %q in minimum-width history:\n%s", want, view)
		}
	}
	if lines := strings.Count(view, "\n") + 1; lines > 24 {
		t.Fatalf("rendered %d lines into a 24-line terminal", lines)
	}
}

func TestHealthTabShowsCapacityAndAvailableDetails(t *testing.T) {
	m := Model{Width: 80, Height: 24, Tab: 1, Latest: battery.Snapshot{
		Capacity: battery.Float(91.88), EnergyDesign: battery.Float(62.32), EnergyFull: battery.Float(57.26),
		EnergyRate: battery.Float(20), Manufacturer: battery.String("Razer"), Model: battery.String("Blade"),
		Serial: battery.String("123"), Voltage: battery.Float(17.2), Temperature: battery.Float(30),
		ACOnline: battery.Bool(true), Source: "upower+sysfs",
	}}
	view := m.View()
	for _, want := range []string{
		"battery health", "Health                 91.9%", "Designed capacity      62.32 Wh", "Current full capacity  57.26 Wh",
		"battery details", "Manufacturer           Razer", "Model                  Blade", "Serial                 123",
		"available readings", "Energy rate            20.00 W", "AC available           yes",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected %q in health view:\n%s", want, view)
		}
	}
	for _, unwanted := range []string{"long-term", "observed", "Temperature", "Voltage", "Source", "Cycles"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("did not expect %q in health view:\n%s", unwanted, view)
		}
	}
}

func TestHealthTabFallsBackToChargeCapacity(t *testing.T) {
	m := Model{Width: 80, Height: 24, Tab: 1, Latest: battery.Snapshot{
		Capacity: battery.Float(90), ChargeDesign: battery.Float(4.1), ChargeFull: battery.Float(3.7),
	}}
	view := m.View()
	for _, want := range []string{"Designed capacity      4.10 Ah", "Current full capacity  3.70 Ah"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected %q in health view:\n%s", want, view)
		}
	}
	if lines := strings.Count(view, "\n") + 1; lines > 24 {
		t.Fatalf("rendered %d lines into a 24-line terminal", lines)
	}
}

func TestViewsFitNarrowTerminalWidth(t *testing.T) {
	for _, width := range []int{30, 80} {
		for _, tab := range []int{0, 1} {
			m := Model{Width: width, Height: 24, Tab: tab, Range: 24 * time.Hour, Latest: battery.Snapshot{
				DeviceID: "BAT0", Timestamp: time.Now(), State: "charging", Percentage: battery.Float(70),
				Manufacturer: battery.String("Long Manufacturer"), Model: battery.String("Long Model Name"),
			}}
			for _, line := range strings.Split(m.View(), "\n") {
				if got := lipgloss.Width(line); got > m.Width {
					t.Fatalf("tab %d line width %d exceeds %d:\n%s", tab, got, m.Width, m.View())
				}
			}
		}
	}
}

func TestHealthFitsShortTerminal(t *testing.T) {
	m := Model{Width: 80, Height: 8, Tab: 1, Latest: battery.Snapshot{
		Capacity: battery.Float(90), EnergyDesign: battery.Float(60), EnergyFull: battery.Float(54),
		Manufacturer: battery.String("Razer"), Model: battery.String("Blade"), Current: battery.Float(2),
	}}
	view := m.View()
	if !strings.Contains(view, "Designed capacity") || !strings.Contains(view, "↑/↓ scroll") || strings.Contains(view, "Current                 2.00 A") {
		t.Fatalf("unexpected short health view:\n%s", view)
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	scrolled := updated.(Model).View()
	if !strings.Contains(scrolled, "Current") || !strings.Contains(scrolled, "2.00 A") {
		t.Fatalf("scrolling to end did not reveal available readings:\n%s", scrolled)
	}
	if lines := strings.Count(scrolled, "\n") + 1; lines > 8 {
		t.Fatalf("rendered %d lines into an 8-line terminal", lines)
	}
}

func TestHealthEnergyRateRequiresAC(t *testing.T) {
	s := battery.Snapshot{EnergyRate: battery.Float(20), ACOnline: battery.Bool(false)}
	if view := renderRows(availableLiveRows(s)); strings.Contains(view, "Energy rate") {
		t.Fatalf("energy rate shown without AC:\n%s", view)
	}
	s.ACOnline = battery.Bool(true)
	if view := renderRows(availableLiveRows(s)); !strings.Contains(view, "Energy rate") {
		t.Fatalf("energy rate hidden with AC:\n%s", view)
	}
}

func TestScrollingKeysAndTabChanges(t *testing.T) {
	m := Model{Width: 80, Height: 8, Tab: 1, Latest: battery.Snapshot{
		Capacity: battery.Float(90), EnergyDesign: battery.Float(60), EnergyFull: battery.Float(54),
		Manufacturer: battery.String("Razer"), Model: battery.String("Blade"), Serial: battery.String("123"),
		Voltage: battery.Float(17), Current: battery.Float(2), Source: "sysfs",
	}}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if updated.(Model).Scroll != 1 {
		t.Fatalf("j did not scroll: %d", updated.(Model).Scroll)
	}
	updated, _ = updated.(Model).Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if updated.(Model).Scroll <= 1 {
		t.Fatalf("page down did not scroll: %d", updated.(Model).Scroll)
	}
	updated, _ = updated.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	if updated.(Model).Scroll != 0 || updated.(Model).Tab != 0 {
		t.Fatalf("tab change did not reset scroll: %+v", updated.(Model))
	}
}

func TestEnrichDeviceMetadataRestoresVendorDetails(t *testing.T) {
	s := battery.Snapshot{}
	enrichDeviceMetadata(&s, battery.Device{
		NativePath: "/sys/class/power_supply/BAT0", Manufacturer: battery.String("Razer"),
		Model: battery.String("Blade"), Serial: battery.String("123"), Technology: battery.String("Unknown"),
	})
	if s.NativePath == "" || s.Manufacturer.String != "Razer" || s.Model.String != "Blade" || s.Serial.String != "123" {
		t.Fatalf("metadata was not restored: %+v", s)
	}
}
