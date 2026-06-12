package tui

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/nuzair46/omabat/internal/battery"
	"github.com/nuzair46/omabat/internal/storage"
)

type Model struct {
	Repo      *storage.Repository
	Collector LiveCollector
	Range     time.Duration
	Tab       int
	Width     int
	Height    int
	Loading   bool
	Scroll    int
	Latest    battery.Snapshot
	Device    battery.Device
	Samples   []battery.Snapshot
	Events    []battery.Event
	AvgPower  sql.NullFloat64
	PeakPower sql.NullFloat64
	Err       error
	LiveErr   error
}

type LiveCollector interface {
	Collect(context.Context) (battery.Snapshot, []battery.Event, error)
}

type loaded struct {
	historyRange time.Duration
	latest       battery.Snapshot
	device       battery.Device
	samples      []battery.Snapshot
	events       []battery.Event
	avg, peak    sql.NullFloat64
	err          error
}

type liveTick time.Time

type collected struct {
	err error
}

func New(repo *storage.Repository, collector LiveCollector, r time.Duration) Model {
	return Model{Repo: repo, Collector: collector, Range: r, Width: 80, Height: 24, Loading: true}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.collectNow(), m.loadRange(m.Range), liveTickCmd())
}

func (m Model) loadRange(historyRange time.Duration) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		x := loaded{historyRange: historyRange}
		x.latest, x.err = m.Repo.Latest(ctx)
		if x.err != nil {
			return x
		}
		x.device, _ = m.Repo.Device(ctx, x.latest.DeviceID)
		enrichDeviceMetadata(&x.latest, x.device)
		x.samples, x.err = m.Repo.Samples(ctx, time.Now().Add(-historyRange))
		if x.err != nil {
			return x
		}
		x.events, x.err = m.Repo.Events(ctx, time.Now().Add(-historyRange), 1000)
		if x.err != nil {
			return x
		}
		x.avg, x.peak, x.err = m.Repo.DischargeStats(ctx, time.Now().Add(-historyRange))
		return x
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.Width, m.Height = msg.Width, msg.Height
		m.Scroll = min(m.Scroll, m.maxScroll())
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, quitKeys):
			return m, tea.Quit
		case key.Matches(msg, toggleTabKey):
			m.Tab = (m.Tab + 1) % 2
			m.Scroll = 0
		case key.Matches(msg, historyKey):
			m.Tab = 0
			m.Scroll = 0
		case key.Matches(msg, healthKey):
			m.Tab = 1
			m.Scroll = 0
		case key.Matches(msg, scrollUpKey):
			m.Scroll = max(0, m.Scroll-1)
		case key.Matches(msg, scrollDownKey):
			m.Scroll = min(m.maxScroll(), m.Scroll+1)
		case key.Matches(msg, pageUpKey):
			m.Scroll = max(0, m.Scroll-m.bodyHeight())
		case key.Matches(msg, pageDownKey):
			m.Scroll = min(m.maxScroll(), m.Scroll+m.bodyHeight())
		case key.Matches(msg, homeKey):
			m.Scroll = 0
		case key.Matches(msg, endKey):
			m.Scroll = m.maxScroll()
		case key.Matches(msg, refreshKey):
			return m, m.loadRange(m.Range)
		case key.Matches(msg, range24hKey):
			m.Range = 24 * time.Hour
			m.Scroll = 0
			return m, m.loadRange(m.Range)
		case key.Matches(msg, range3dKey):
			m.Range = 3 * 24 * time.Hour
			m.Scroll = 0
			return m, m.loadRange(m.Range)
		case key.Matches(msg, range7dKey):
			m.Range = 7 * 24 * time.Hour
			m.Scroll = 0
			return m, m.loadRange(m.Range)
		case key.Matches(msg, previousRangeKey):
			m.Range = previousRange(m.Range)
			m.Scroll = 0
			return m, m.loadRange(m.Range)
		case key.Matches(msg, nextRangeKey):
			m.Range = nextRange(m.Range)
			m.Scroll = 0
			return m, m.loadRange(m.Range)
		}
	case loaded:
		if msg.historyRange != m.Range {
			return m, nil
		}
		m.Loading = false
		m.Latest, m.Device, m.Samples, m.Events = msg.latest, msg.device, msg.samples, msg.events
		m.AvgPower, m.PeakPower, m.Err = msg.avg, msg.peak, msg.err
		m.Scroll = min(m.Scroll, m.maxScroll())
	case liveTick:
		return m, tea.Batch(m.collectNow(), liveTickCmd())
	case collected:
		m.LiveErr = msg.err
		return m, m.loadRange(m.Range)
	}
	return m, nil
}

func (m Model) collectNow() tea.Cmd {
	if m.Collector == nil {
		return nil
	}
	return func() tea.Msg {
		_, _, err := m.Collector.Collect(context.Background())
		return collected{err: err}
	}
}

func liveTickCmd() tea.Cmd {
	return tea.Tick(3*time.Second, func(at time.Time) tea.Msg {
		return liveTick(at)
	})
}

var (
	titleStyle          = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86"))
	activeStyle         = lipgloss.NewStyle().Bold(true).Underline(true)
	dimStyle            = lipgloss.NewStyle().Faint(true)
	saverStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	balanceStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	performanceStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	unknownProfileStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	acStyle             = lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	sleepStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("141"))
	panelTitleStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86"))
	panelStyle          = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240")).Padding(0, 1)
)

var (
	quitKeys         = key.NewBinding(key.WithKeys("q", "ctrl+c"))
	toggleTabKey     = key.NewBinding(key.WithKeys("tab"))
	historyKey       = key.NewBinding(key.WithKeys("g"))
	healthKey        = key.NewBinding(key.WithKeys("h"))
	refreshKey       = key.NewBinding(key.WithKeys("r"))
	range24hKey      = key.NewBinding(key.WithKeys("1"))
	range3dKey       = key.NewBinding(key.WithKeys("2"))
	range7dKey       = key.NewBinding(key.WithKeys("3"))
	previousRangeKey = key.NewBinding(key.WithKeys("left", "["))
	nextRangeKey     = key.NewBinding(key.WithKeys("right", "]"))
	scrollUpKey      = key.NewBinding(key.WithKeys("up", "k"))
	scrollDownKey    = key.NewBinding(key.WithKeys("down", "j"))
	pageUpKey        = key.NewBinding(key.WithKeys("pgup", "ctrl+u"))
	pageDownKey      = key.NewBinding(key.WithKeys("pgdown", "ctrl+d"))
	homeKey          = key.NewBinding(key.WithKeys("home"))
	endKey           = key.NewBinding(key.WithKeys("end"))
)

func (m Model) View() string {
	header := renderHeader(m.Tab, m.Range, m.Width)
	if m.Loading {
		body := renderPanel(iconHistory+" loading", "Loading battery history...", max(12, m.Width))
		return header + "\n" + body + "\n" + footerHelp(m.Width, false)
	}
	if m.Err != nil {
		return header + "\n" + m.errorView() + "\n" + footerHelp(m.Width, false)
	}
	body := m.fullBody()
	scrollable := lineCount(body) > m.bodyHeight()
	return header + "\n" + scrollView(body, m.Scroll, m.bodyHeight()) + "\n" + footerHelp(m.Width, scrollable)
}

func (m Model) fullBody() string {
	if m.Tab == 1 {
		return m.health()
	}
	return m.history()
}

func (m Model) bodyHeight() int {
	if m.Height <= 0 {
		return 1 << 20
	}
	return max(1, m.Height-2)
}

func (m Model) maxScroll() int {
	if m.Loading || m.Err != nil {
		return 0
	}
	return max(0, lineCount(m.fullBody())-m.bodyHeight())
}

func scrollView(body string, offset, height int) string {
	lines := strings.Split(body, "\n")
	if height <= 0 || height >= len(lines) {
		return body
	}
	offset = min(max(0, offset), max(0, len(lines)-height))
	return strings.Join(lines[offset:min(len(lines), offset+height)], "\n")
}

func (m Model) errorView() string {
	if errors.Is(m.Err, sql.ErrNoRows) {
		return renderPanel(iconHistory+" collecting", "Collecting the first battery sample...\nHistory will appear automatically; live refresh runs every 3 seconds.", max(12, m.Width))
	}
	return renderPanel(iconHistory+" history unavailable", m.Err.Error(), max(12, m.Width))
}

func renderHeader(tab int, historyRange time.Duration, width int) string {
	title := titleStyle.Render(iconApp + " Omabat")
	activeTab := activeStyle.Render("[g] History")
	if tab == 1 {
		activeTab = activeStyle.Render("[h] Health")
	}
	if width < 30 {
		shortcut := "[g]"
		if tab == 1 {
			shortcut = "[h]"
		}
		return title + " " + activeStyle.Render(shortcut+" "+rangeLabel(historyRange))
	}
	if width < 65 {
		return title + "  " + activeTab + "  " + activeStyle.Render("["+rangeLabel(historyRange)+"]")
	}
	tabs := "[g] " + iconHistory + " History  [h] " + iconHealth + " Health"
	if tab == 0 {
		tabs = activeStyle.Render("[g] "+iconHistory+" History") + "  [h] " + iconHealth + " Health"
	} else {
		tabs = "[g] " + iconHistory + " History  " + activeStyle.Render("[h] "+iconHealth+" Health")
	}
	return title + "  " + tabs + "  " + rangeSelector(historyRange)
}

func (m Model) history() string {
	panelWidth := max(12, m.Width)
	graphWidth := max(12, panelWidth-2)
	graphHeight := max(5, min(m.Height-19, 11))
	graph := renderGraph(m.Samples, m.Events, graphWidth, graphHeight, time.Now().Add(-m.Range), time.Now())
	legend := graphLegend(panelWidth - 2)
	lines := []string{
		renderPanel(iconBattery+" live battery", m.historySummary(panelWidth), panelWidth),
		renderPanel(iconHistory+" battery level · "+rangeLabel(m.Range), graph+"\n"+legend, panelWidth),
	}
	maxEvents := min(20, len(m.Events))
	if maxEvents > 0 {
		var recent []string
		for i, e := range m.Events {
			if i >= maxEvents {
				break
			}
			recent = append(recent, formatEvent(e))
		}
		lines = append(lines, renderPanel(iconHistory+" recent events · newest first", strings.Join(recent, "\n"), panelWidth))
	}
	return strings.Join(lines, "\n")
}

func (m Model) historySummary(width int) string {
	status := fmt.Sprintf("%s %s  %s", stateIcon(m.Latest.State), summaryNumber(m.Latest.Percentage, "%.1f%%"), summaryText(m.Latest.State))
	ac := acSummary(m.Latest.ACOnline)
	profile := profileSummary(m.Latest.PowerProfile)
	updated := "updated " + m.Latest.Timestamp.Format("15:04:05")
	rate := currentRateSummary(m.Latest)
	avg := "avg draw " + summaryNumber(m.AvgPower, "%.2f W")
	peak := "peak draw " + summaryNumber(m.PeakPower, "%.2f W")
	stats := avg + "  " + peak
	samples := fmt.Sprintf("%d samples%s", len(m.Samples), liveStatus(m.LiveErr))
	if width >= 75 {
		return status + "   " + ac + "   " + profile + "   " + updated + "\n" +
			rate + "   " + stats + "   " + samples
	}
	if width >= 45 {
		return status + "   " + ac + "\n" +
			profile + "   " + updated + "\n" +
			rate + "\n" +
			stats + "\n" +
			samples
	}
	return strings.Join([]string{status, ac + "  " + profile, rate, avg, peak, samples, updated}, "\n")
}

func graphLegend(width int) string {
	if width >= 70 {
		return acStyle.Render(iconPlugged) + " AC  " + sleepStyle.Render(iconSleep) + " suspend   " +
			profileStyle("power-saver").Render(iconPowerSaver) + " saver  " +
			profileStyle("balanced").Render(iconBalanced) + " balanced  " +
			profileStyle("performance").Render(iconPerformance) + " performance"
	}
	if width < 40 {
		return acStyle.Render(iconPlugged) + " AC  " + sleepStyle.Render(iconSleep) + " Zz  " +
			profileStyle("power-saver").Render(iconPowerSaver) + " S  " +
			profileStyle("balanced").Render(iconBalanced) + " B  " +
			profileStyle("performance").Render(iconPerformance) + " P"
	}
	return acStyle.Render(iconPlugged) + " AC  " + sleepStyle.Render(iconSleep) + " Zz  " +
		profileStyle("power-saver").Render(iconPowerSaver) + " save  " +
		profileStyle("balanced").Render(iconBalanced) + " bal  " +
		profileStyle("performance").Render(iconPerformance) + " perf"
}

func acSummary(ac sql.NullBool) string {
	if !ac.Valid {
		return iconUnknown + " AC n/a"
	}
	if ac.Bool {
		return acStyle.Render(iconPlugged) + " AC"
	}
	return iconUnplugged + " battery"
}

func profileSummary(profile sql.NullString) string {
	if !profile.Valid {
		return iconUnknown + " profile n/a"
	}
	return profileStyle(profile.String).Render(profileIcon(profile.String)) + " " + profile.String
}

func currentRateSummary(s battery.Snapshot) string {
	label := "energy rate"
	switch s.State {
	case "charging":
		label = "charge rate"
	case "discharging":
		label = "power draw"
	}
	return label + " " + summaryNumber(s.EnergyRate, "%.2f W")
}

func formatEvent(e battery.Event) string {
	label := map[string]string{
		"plugged": "AC connected", "unplugged": "AC disconnected", "full": "fully charged",
		"sleep": "suspend", "resume": "resumed",
	}[e.Type]
	if label == "" {
		label = e.Type
	}
	parts := []string{e.Timestamp.Format("Jan 02 15:04"), eventIcon(e.Type), fmt.Sprintf("%-15s", label)}
	if e.Percentage.Valid {
		parts = append(parts, fmt.Sprintf("%.1f%%", e.Percentage.Float64))
	}
	if e.DurationSeconds.Valid && e.DurationSeconds.Int64 > 0 {
		parts = append(parts, shortDuration(e.DurationSeconds.Int64))
	}
	if e.Type == "sleep" && e.PercentageDelta.Valid && e.PercentageDelta.Float64 != 0 {
		change := e.PercentageDelta.Float64
		label := "gain"
		if change < 0 {
			label = "drain"
			change = -change
		}
		parts = append(parts, fmt.Sprintf("%s %.1f%%", label, change))
	}
	return strings.Join(parts, "  ")
}

func shortDuration(seconds int64) string {
	d := time.Duration(seconds) * time.Second
	if d >= time.Hour {
		return fmt.Sprintf("%dh %02dm", int(d/time.Hour), int(d%time.Hour/time.Minute))
	}
	return fmt.Sprintf("%dm", int(d/time.Minute))
}

func lineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func (m Model) health() string {
	width := max(12, m.Width)
	capacity := strings.Join([]string{
		fmt.Sprintf("%-22s %s", "Health", optional(m.Latest.Capacity, "%.1f%%")),
		fmt.Sprintf("%-22s %s", "Designed capacity", capacityValue(m.Latest.EnergyDesign, m.Latest.ChargeDesign)),
		fmt.Sprintf("%-22s %s", "Current full capacity", capacityValue(m.Latest.EnergyFull, m.Latest.ChargeFull)),
	}, "\n")
	panels := []string{renderPanel(iconHealth+" battery health", capacity, width)}
	if rows := availableHardwareRows(m.Latest); len(rows) > 0 {
		panels = append(panels, renderPanel(iconBattery+" battery details", renderRows(rows), width))
	}
	if rows := availableLiveRows(m.Latest); len(rows) > 0 {
		panels = append(panels, renderPanel(iconHistory+" available readings", renderRows(rows), width))
	}
	return strings.Join(panels, "\n")
}

type detailRow struct {
	label string
	value string
}

func availableHardwareRows(s battery.Snapshot) []detailRow {
	var rows []detailRow
	addStringRow := func(label string, value sql.NullString) {
		if value.Valid && value.String != "" {
			rows = append(rows, detailRow{label, value.String})
		}
	}
	addStringRow("Manufacturer", s.Manufacturer)
	addStringRow("Model", s.Model)
	addStringRow("Serial", s.Serial)
	addStringRow("Technology", s.Technology)
	if s.CycleCount.Valid && s.CycleCount.Int64 > 0 {
		rows = append(rows, detailRow{"Cycles", fmt.Sprintf("%d", s.CycleCount.Int64)})
	}
	if s.NativePath != "" {
		rows = append(rows, detailRow{"Native path", s.NativePath})
	}
	return rows
}

func availableLiveRows(s battery.Snapshot) []detailRow {
	var rows []detailRow
	addFloatRow := func(label string, value sql.NullFloat64, format string) {
		if value.Valid {
			rows = append(rows, detailRow{label, fmt.Sprintf(format, value.Float64)})
		}
	}
	if s.State != "" && s.State != "unknown" {
		rows = append(rows, detailRow{"State", s.State})
	}
	addFloatRow("Battery level", s.Percentage, "%.1f%%")
	addFloatRow("Current energy", s.EnergyNow, "%.2f Wh")
	addFloatRow("Current charge", s.ChargeNow, "%.2f Ah")
	addFloatRow("Full charge", s.ChargeFull, "%.2f Ah")
	addFloatRow("Design charge", s.ChargeDesign, "%.2f Ah")
	if s.ACOnline.Valid && s.ACOnline.Bool {
		addFloatRow("Energy rate", s.EnergyRate, "%.2f W")
	}
	addFloatRow("Current", s.Current, "%.2f A")
	if s.ACOnline.Valid {
		rows = append(rows, detailRow{"AC available", yesNo(s.ACOnline.Bool)})
	}
	if s.PowerProfile.Valid {
		rows = append(rows, detailRow{"Power profile", s.PowerProfile.String})
	}
	if s.TimeToEmpty.Valid && s.TimeToEmpty.Int64 > 0 {
		rows = append(rows, detailRow{"Time to empty", shortDuration(s.TimeToEmpty.Int64)})
	}
	if s.TimeToFull.Valid && s.TimeToFull.Int64 > 0 {
		rows = append(rows, detailRow{"Time to full", shortDuration(s.TimeToFull.Int64)})
	}
	return rows
}

func renderRows(rows []detailRow) string {
	var lines []string
	for _, row := range rows {
		lines = append(lines, fmt.Sprintf("%-22s %s", row.label, row.value))
	}
	return strings.Join(lines, "\n")
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func enrichDeviceMetadata(s *battery.Snapshot, d battery.Device) {
	if s.NativePath == "" {
		s.NativePath = d.NativePath
	}
	if !s.Manufacturer.Valid {
		s.Manufacturer = d.Manufacturer
	}
	if !s.Model.Valid {
		s.Model = d.Model
	}
	if !s.Serial.Valid {
		s.Serial = d.Serial
	}
	if !s.Technology.Valid {
		s.Technology = d.Technology
	}
}

func renderGraph(samples []battery.Snapshot, events []battery.Event, width, height int, start, end time.Time) string {
	const axisWidth = 6
	if width <= axisWidth+4 || height < 3 {
		return ""
	}
	chartWidth := width - axisWidth
	columns := bucketSamples(samples, chartWidth, start, end)
	ac := acColumns(samples, chartWidth, start, end)
	sleep := sleepColumns(events, chartWidth, start, end)
	var lines []string
	lines = append(lines, renderSystemTimeline(ac, sleep))
	lines = append(lines, renderProfileTimeline(profileColumns(samples, chartWidth, start, end)))
	for row := 0; row < height; row++ {
		threshold := 100 - float64(row)*100/float64(height-1)
		label := "     "
		switch row {
		case 0:
			label = "100% "
		case height / 4:
			label = " 75% "
		case height / 2:
			label = " 50% "
		case height * 3 / 4:
			label = " 25% "
		case height - 1:
			label = "  0% "
		}
		var line strings.Builder
		line.WriteString(label)
		line.WriteString("│")
		for _, s := range columns {
			if s.Percentage.Valid && s.Percentage.Float64 > 0 && s.Percentage.Float64 >= threshold {
				line.WriteString("▐")
			} else {
				line.WriteByte(' ')
			}
		}
		lines = append(lines, line.String())
	}
	lines = append(lines, strings.Repeat(" ", axisWidth-1)+"└"+strings.Repeat("─", chartWidth))
	left := shortTimeLabel(start, end.Sub(start))
	middle := shortTimeLabel(start.Add(end.Sub(start)/2), end.Sub(start))
	right := shortTimeLabel(end, end.Sub(start))
	labels := placeAxisLabels(chartWidth, left, middle, right)
	lines = append(lines, strings.Repeat(" ", axisWidth)+labels)
	return strings.Join(lines, "\n")
}

func bucketSamples(samples []battery.Snapshot, width int, start, end time.Time) []battery.Snapshot {
	out := make([]battery.Snapshot, width)
	span := end.Sub(start).Seconds()
	if span <= 0 {
		return out
	}
	for _, s := range samples {
		if !s.Percentage.Valid {
			continue
		}
		x := int(s.Timestamp.Sub(start).Seconds() / span * float64(width-1))
		if x >= 0 && x < width {
			out[x] = s
		}
	}
	return out
}

func acColumns(samples []battery.Snapshot, width int, start, end time.Time) []bool {
	out := make([]bool, width)
	if width == 0 || !end.After(start) {
		return out
	}
	index := 0
	state := ""
	acOnline := false
	acKnown := false
	for x := 0; x < width; x++ {
		at := start.Add(time.Duration(float64(end.Sub(start)) * float64(x) / float64(max(1, width-1))))
		for index < len(samples) && !samples[index].Timestamp.After(at) {
			state = samples[index].State
			acOnline = samples[index].ACOnline.Bool
			acKnown = samples[index].ACOnline.Valid
			index++
		}
		if acKnown {
			out[x] = acOnline
		} else {
			out[x] = state == "charging" || state == "full" || state == "pending"
		}
	}
	return out
}

func profileColumns(samples []battery.Snapshot, width int, start, end time.Time) []string {
	out := make([]string, width)
	if width == 0 || !end.After(start) {
		return out
	}
	index := 0
	profile := ""
	for x := 0; x < width; x++ {
		at := start.Add(time.Duration(float64(end.Sub(start)) * float64(x) / float64(max(1, width-1))))
		for index < len(samples) && !samples[index].Timestamp.After(at) {
			if samples[index].PowerProfile.Valid {
				profile = samples[index].PowerProfile.String
			}
			index++
		}
		out[x] = profile
	}
	return out
}

func sleepColumns(events []battery.Event, width int, start, end time.Time) []bool {
	out := make([]bool, width)
	span := end.Sub(start).Seconds()
	if span <= 0 {
		return out
	}
	for _, e := range events {
		if e.Type != "sleep" {
			continue
		}
		from := int(e.Timestamp.Sub(start).Seconds() / span * float64(width-1))
		to := from
		if e.EndTimestamp.Valid {
			to = int(time.Unix(e.EndTimestamp.Int64, 0).Sub(start).Seconds() / span * float64(width-1))
		}
		for x := max(0, from); x <= min(width-1, to); x++ {
			out[x] = true
		}
	}
	return out
}

func renderProfileTimeline(profiles []string) string {
	var line strings.Builder
	line.WriteString("MODE ")
	line.WriteString("│")
	for _, profile := range profiles {
		if profile != "" {
			line.WriteString(profileStyle(profile).Render(profileIcon(profile)))
		} else {
			line.WriteByte(' ')
		}
	}
	return line.String()
}

func renderSystemTimeline(ac, sleep []bool) string {
	var line strings.Builder
	line.WriteString(" AC/S│")
	for x := range ac {
		switch {
		case x < len(sleep) && sleep[x]:
			line.WriteString(sleepStyle.Render(iconSleep))
		case ac[x]:
			line.WriteString(acStyle.Render(iconPlugged))
		default:
			line.WriteByte(' ')
		}
	}
	return line.String()
}

func profileStyle(profile string) lipgloss.Style {
	switch profile {
	case "power-saver":
		return saverStyle
	case "balanced":
		return balanceStyle
	case "performance":
		return performanceStyle
	default:
		return unknownProfileStyle
	}
}

func shortTimeLabel(at time.Time, span time.Duration) string {
	if span <= 24*time.Hour {
		return at.Format("15:04")
	}
	if span <= 72*time.Hour {
		return at.Format("Mon 15h")
	}
	return at.Format("01/02 15h")
}

func placeAxisLabels(width int, left, middle, right string) string {
	row := []rune(strings.Repeat(" ", width))
	writeAt := func(position int, label string) {
		for i, r := range []rune(label) {
			if position+i >= 0 && position+i < len(row) {
				row[position+i] = r
			}
		}
	}
	leftWidth, middleWidth, rightWidth := len([]rune(left)), len([]rune(middle)), len([]rune(right))
	switch {
	case width >= leftWidth+middleWidth+rightWidth+4:
		writeAt(0, left)
		writeAt((width-middleWidth)/2, middle)
		writeAt(width-rightWidth, right)
	case width >= leftWidth+rightWidth+2:
		writeAt(0, left)
		writeAt(width-rightWidth, right)
	default:
		writeAt(max(0, width-rightWidth), right)
	}
	return string(row)
}

func renderPanel(title, body string, width int) string {
	width = max(width, 12)
	inner := width - 2
	title = " " + title + " "
	title = lipgloss.NewStyle().MaxWidth(inner).Render(title)
	titleWidth := lipgloss.Width(title)
	top := "╭" + panelTitleStyle.Render(title) + strings.Repeat("─", max(0, inner-titleWidth)) + "╮"
	bottom := "╰" + strings.Repeat("─", inner) + "╯"
	var lines []string
	for _, raw := range strings.Split(body, "\n") {
		line := lipgloss.NewStyle().MaxWidth(inner).Render(raw)
		lines = append(lines, "│"+line+strings.Repeat(" ", max(0, inner-lipgloss.Width(line)))+"│")
	}
	return top + "\n" + strings.Join(lines, "\n") + "\n" + bottom
}

func rangeSelector(selected time.Duration) string {
	parts := []string{dimStyle.Render("←")}
	for _, duration := range []time.Duration{24 * time.Hour, 3 * 24 * time.Hour, 7 * 24 * time.Hour} {
		label := rangeLabel(duration)
		if duration == selected {
			label = activeStyle.Render("[" + label + "]")
		} else {
			label = dimStyle.Render(" " + label + " ")
		}
		parts = append(parts, label)
	}
	parts = append(parts, dimStyle.Render("→"))
	return strings.Join(parts, " ")
}

func rangeLabel(historyRange time.Duration) string {
	switch historyRange {
	case 24 * time.Hour:
		return "24h"
	case 3 * 24 * time.Hour:
		return "3d"
	case 7 * 24 * time.Hour:
		return "7d"
	default:
		return historyRange.String()
	}
}

func previousRange(current time.Duration) time.Duration {
	switch current {
	case 7 * 24 * time.Hour:
		return 3 * 24 * time.Hour
	default:
		return 24 * time.Hour
	}
}

func nextRange(current time.Duration) time.Duration {
	switch current {
	case 24 * time.Hour:
		return 3 * 24 * time.Hour
	default:
		return 7 * 24 * time.Hour
	}
}

func footerHelp(width int, scrollable bool) string {
	scroll := ""
	if scrollable {
		scroll = "  ↑/↓ scroll"
	}
	if width < 40 {
		return dimStyle.Render("g/h  ←/→" + scroll + "  q")
	}
	if width < 75 {
		return dimStyle.Render("g/h tabs  ←/→ range" + scroll + "  q quit")
	}
	if width < 95 {
		return dimStyle.Render("live 3s  g/h tabs  ←/→ range" + scroll + "  r refresh  q quit")
	}
	return dimStyle.Render("live 3s  g history  h health  ←/→ range" + scroll + "  home/end  r refresh  q quit")
}

func liveStatus(err error) string {
	if err == nil {
		return ""
	}
	return "  live unavailable"
}

func summaryNumber(v sql.NullFloat64, format string) string {
	if !v.Valid {
		return "n/a"
	}
	return fmt.Sprintf(format, v.Float64)
}

func summaryText(value string) string {
	if value == "" || value == "unknown" {
		return "unknown"
	}
	return value
}

func capacityValue(energy, charge sql.NullFloat64) string {
	if positiveFloat(energy) {
		return fmt.Sprintf("%.2f Wh", energy.Float64)
	}
	if positiveFloat(charge) {
		return fmt.Sprintf("%.2f Ah", charge.Float64)
	}
	return "unavailable"
}

func positiveFloat(v sql.NullFloat64) bool {
	return v.Valid && v.Float64 > 0
}

func optional(v sql.NullFloat64, format string) string {
	if !v.Valid {
		return "unavailable"
	}
	return fmt.Sprintf(format, v.Float64)
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
