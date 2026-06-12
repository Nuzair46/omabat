package collector

import (
	"context"
	"os/exec"
	"strings"

	"github.com/nuzair46/omabat/internal/battery"
)

type CommandRunner func(context.Context, string, ...string) ([]byte, error)

type PowerProfileProvider struct {
	Battery Provider
	Run     CommandRunner
}

func (p PowerProfileProvider) Snapshot(ctx context.Context) (battery.Snapshot, error) {
	s, err := p.Battery.Snapshot(ctx)
	if err != nil {
		return battery.Snapshot{}, err
	}
	run := p.Run
	if run == nil {
		run = runCommand
	}
	output, err := run(ctx, "powerprofilesctl", "get")
	if err == nil {
		s.PowerProfile = battery.String(normalizePowerProfile(string(output)))
	}
	return s, nil
}

func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

func normalizePowerProfile(profile string) string {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "power-saver", "powersave", "power-saving", "powersaving":
		return "power-saver"
	case "balanced":
		return "balanced"
	case "performance":
		return "performance"
	default:
		return ""
	}
}
