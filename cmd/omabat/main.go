package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/nuzair46/omabat/internal/battery"
	"github.com/nuzair46/omabat/internal/collector"
	"github.com/nuzair46/omabat/internal/demo"
	"github.com/nuzair46/omabat/internal/install"
	"github.com/nuzair46/omabat/internal/storage"
	"github.com/nuzair46/omabat/internal/tui"
)

var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "omabat:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "collect":
			return collect(args[1:])
		case "health":
			return health()
		case "waybar":
			return waybar()
		case "version", "--version", "-v":
			fmt.Println(version)
			return nil
		case "demo-data":
			return demoData(args[1:])
		case "install":
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			return install.Service(exe)
		case "help", "--help", "-h":
			usage()
			return nil
		}
	}
	flags := flag.NewFlagSet("omabat", flag.ContinueOnError)
	historyRange := flags.String("range", "24h", "history range: 24h, 3d, or 7d")
	dbPath := flags.String("db", "", "SQLite database path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	duration, err := storage.ValidateRange(*historyRange)
	if err != nil {
		return err
	}
	repo, err := storage.Open(*dbPath)
	if err != nil {
		return err
	}
	defer repo.Close()
	service := collector.Service{Provider: provider(), Repo: repo}
	_, err = tea.NewProgram(tui.New(repo, service, duration), tea.WithAltScreen()).Run()
	return err
}

func collect(args []string) error {
	flags := flag.NewFlagSet("collect", flag.ContinueOnError)
	daemon := flags.Bool("daemon", false, "run continuously")
	interval := flags.Duration("interval", 120*time.Second, "collection interval")
	dbPath := flags.String("db", "", "SQLite database path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	repo, err := storage.Open(*dbPath)
	if err != nil {
		return err
	}
	defer repo.Close()
	service := collector.Service{Provider: provider(), Repo: repo}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if *daemon {
		return service.Run(ctx, *interval)
	}
	s, events, err := service.Collect(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("%s %s %s", s.Timestamp.Format(time.RFC3339), s.DeviceID, value(s.Percentage, "%.1f%%"))
	if s.PowerProfile.Valid {
		fmt.Printf(" profile=%s", s.PowerProfile.String)
	}
	if len(events) > 0 {
		fmt.Printf(" event=%s", events[0].Type)
	}
	fmt.Println()
	return nil
}

func provider() collector.Provider {
	batteryProvider := collector.CompositeProvider{Primary: collector.UPowerProvider{}, Fallback: collector.SysfsProvider{}}
	return collector.PowerProfileProvider{Battery: batteryProvider}
}

func demoData(args []string) error {
	flags := flag.NewFlagSet("demo-data", flag.ContinueOnError)
	dbPath := flags.String("db", "omabat-demo.db", "demo SQLite database path")
	days := flags.Int("days", 7, "number of history days, from 1 to 10")
	seed := flags.Int64("seed", 46, "random seed")
	replace := flags.Bool("replace", false, "replace an existing demo database")
	if err := flags.Parse(args); err != nil {
		return err
	}
	path, err := filepath.Abs(*dbPath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		if !*replace {
			return fmt.Errorf("%s already exists; use --replace to recreate it", path)
		}
		for _, suffix := range []string{"", "-wal", "-shm"} {
			if err := os.Remove(path + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	repo, err := storage.Open(path)
	if err != nil {
		return err
	}
	defer repo.Close()
	baseline, _ := provider().Snapshot(context.Background())
	result, err := demo.Generate(context.Background(), repo, baseline, *days, *seed, time.Now())
	if err != nil {
		return err
	}
	fmt.Printf("Created %s with %d samples and %d suspend periods.\n", path, result.Samples, result.Sleeps)
	fmt.Printf("Open it with: omabat --db %s --range 7d\n", path)
	return nil
}

func health() error {
	ctx := context.Background()
	s, err := provider().Snapshot(ctx)
	if err != nil {
		repo, openErr := storage.Open("")
		if openErr != nil {
			return errors.Join(err, openErr)
		}
		defer repo.Close()
		s, err = repo.Latest(ctx)
	}
	if err != nil {
		return err
	}
	fmt.Print(formatHealth(s))
	return nil
}

func formatHealth(s battery.Snapshot) string {
	lines := []string{
		fmt.Sprintf("Health:                %s", value(s.Capacity, "%.1f%%")),
		fmt.Sprintf("Designed capacity:     %s", capacityValue(s.EnergyDesign, s.ChargeDesign)),
		fmt.Sprintf("Current full capacity: %s", capacityValue(s.EnergyFull, s.ChargeFull)),
	}
	add := func(label, value string) {
		if value != "" {
			lines = append(lines, fmt.Sprintf("%-23s%s", label+":", value))
		}
	}
	addString := func(label string, value sql.NullString) {
		if value.Valid {
			add(label, value.String)
		}
	}
	addFloat := func(label string, value sql.NullFloat64, format string) {
		if value.Valid {
			add(label, fmt.Sprintf(format, value.Float64))
		}
	}
	addString("Manufacturer", s.Manufacturer)
	addString("Model", s.Model)
	addString("Serial", s.Serial)
	addString("Technology", s.Technology)
	if s.CycleCount.Valid && s.CycleCount.Int64 > 0 {
		add("Cycles", fmt.Sprintf("%d", s.CycleCount.Int64))
	}
	add("State", s.State)
	addFloat("Battery level", s.Percentage, "%.1f%%")
	addFloat("Current energy", s.EnergyNow, "%.2f Wh")
	addFloat("Current charge", s.ChargeNow, "%.2f Ah")
	addFloat("Full charge", s.ChargeFull, "%.2f Ah")
	addFloat("Design charge", s.ChargeDesign, "%.2f Ah")
	if s.ACOnline.Valid && s.ACOnline.Bool {
		addFloat("Energy rate", s.EnergyRate, "%.2f W")
	}
	addFloat("Current", s.Current, "%.2f A")
	if s.ACOnline.Valid {
		add("AC available", map[bool]string{true: "yes", false: "no"}[s.ACOnline.Bool])
	}
	addString("Power profile", s.PowerProfile)
	if s.TimeToEmpty.Valid && s.TimeToEmpty.Int64 > 0 {
		add("Time to empty", formatDuration(s.TimeToEmpty.Int64))
	}
	if s.TimeToFull.Valid && s.TimeToFull.Int64 > 0 {
		add("Time to full", formatDuration(s.TimeToFull.Int64))
	}
	add("Native path", s.NativePath)
	return strings.Join(lines, "\n") + "\n"
}

func formatDuration(seconds int64) string {
	duration := time.Duration(seconds) * time.Second
	if duration >= time.Hour {
		return fmt.Sprintf("%dh %02dm", int(duration/time.Hour), int(duration%time.Hour/time.Minute))
	}
	return fmt.Sprintf("%dm", int(duration/time.Minute))
}

type waybarOutput struct {
	Text    string `json:"text"`
	Tooltip string `json:"tooltip"`
	Class   string `json:"class"`
}

func waybar() error {
	active := exec.Command("systemctl", "--user", "is-active", "--quiet", "omabat.service").Run() == nil
	var latest battery.Snapshot
	if repo, err := storage.Open(""); err == nil {
		latest, _ = repo.Latest(context.Background())
		repo.Close()
	}
	return json.NewEncoder(os.Stdout).Encode(formatWaybar(active, latest))
}

func formatWaybar(active bool, s battery.Snapshot) waybarOutput {
	status := "inactive"
	icon := "󰒍"
	if active {
		status = "active"
		icon = "󰒋"
	}
	lines := []string{"Omabat daemon: " + status}
	if s.Percentage.Valid {
		lines = append(lines, fmt.Sprintf("Battery: %.1f%% %s", s.Percentage.Float64, s.State))
	}
	if s.ACOnline.Valid && s.ACOnline.Bool && s.EnergyRate.Valid {
		lines = append(lines, fmt.Sprintf("Energy rate: %.2f W", s.EnergyRate.Float64))
	}
	if !s.Timestamp.IsZero() {
		lines = append(lines, "Last sample: "+s.Timestamp.Format("Jan 02 15:04:05"))
	}
	lines = append(lines, "", "Click to open Omabat")
	return waybarOutput{Text: icon, Tooltip: strings.Join(lines, "\n"), Class: status}
}

func capacityValue(energy, charge sql.NullFloat64) string {
	if energy.Valid && energy.Float64 > 0 {
		return value(energy, "%.2f Wh")
	}
	if charge.Valid && charge.Float64 > 0 {
		return value(charge, "%.2f Ah")
	}
	return "unavailable"
}

func value(v sql.NullFloat64, format string) string {
	if !v.Valid {
		return "unavailable"
	}
	return fmt.Sprintf(format, v.Float64)
}

func usage() {
	fmt.Println(`Usage:
  omabat [--range 24h|3d|7d]  Open history TUI
  omabat collect              Collect one sample
  omabat collect --daemon     Run the collector daemon
  omabat health               Print current battery health
  omabat waybar               Print Waybar daemon status JSON
  omabat version              Print the Omabat version
  omabat demo-data            Create a realistic demo history database
  omabat install              Install and enable the user service`)
}
