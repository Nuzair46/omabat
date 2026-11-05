package tui

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/nuzair46/omabat/internal/battery"
	"github.com/nuzair46/omabat/internal/collector"
	"github.com/nuzair46/omabat/internal/storage"
)

type dashboardProvider struct{ at time.Time }

func (p dashboardProvider) Snapshot(context.Context) (battery.Snapshot, error) {
	return battery.Snapshot{DeviceID: "BAT0", Timestamp: p.at, Source: "test", State: "discharging", Present: true,
		Percentage: battery.Float(50), EnergyRate: battery.Float(6)}, nil
}

func TestDashboardCollectionStillWritesHistory(t *testing.T) {
	repo, err := storage.Open(filepath.Join(t.TempDir(), "dashboard.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	start := time.Now().Add(-time.Minute).Truncate(time.Second)
	service := collector.Service{Provider: dashboardProvider{start}, Repo: repo, Interval: LiveInterval}
	m := New(repo, service, 24*time.Hour)
	if msg := m.collectNow()().(collected); msg.err != nil {
		t.Fatal(msg.err)
	}
	service.Provider = dashboardProvider{start.Add(LiveInterval)}
	m.Collector = service
	if msg := m.collectNow()().(collected); msg.err != nil {
		t.Fatal(msg.err)
	}
	samples, err := repo.Samples(context.Background(), start)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 2 {
		t.Fatalf("dashboard persisted %d readings, want 2", len(samples))
	}
	for _, s := range samples {
		if s.SampleInterval != LiveInterval {
			t.Fatalf("dashboard cadence = %v, want %v", s.SampleInterval, LiveInterval)
		}
	}
	avg, peak, err := repo.DischargeStats(context.Background(), start)
	if err != nil || !avg.Valid || avg.Float64 != 6 || !peak.Valid || peak.Float64 != 6 {
		t.Fatalf("dashboard statistics: average=%+v peak=%+v err=%v", avg, peak, err)
	}
}
