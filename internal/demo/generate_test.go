package demo

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/nuzair46/omabat/internal/battery"
	"github.com/nuzair46/omabat/internal/storage"
)

func TestGenerateCreatesRealisticHistory(t *testing.T) {
	repo, err := storage.Open(filepath.Join(t.TempDir(), "demo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	now := time.Date(2026, 6, 12, 20, 0, 0, 0, time.UTC)
	result, err := Generate(context.Background(), repo, battery.Snapshot{}, 2, 46, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Samples < 900 || result.Sleeps < 2 {
		t.Fatalf("unexpected result: %+v", result)
	}
	samples, err := repo.Samples(context.Background(), now.Add(-48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	profiles := map[string]bool{}
	states := map[string]bool{}
	for _, s := range samples {
		profiles[s.PowerProfile.String] = true
		states[s.State] = true
	}
	for _, want := range []string{"power-saver", "balanced", "performance"} {
		if !profiles[want] {
			t.Fatalf("missing profile %s: %v", want, profiles)
		}
	}
	for _, want := range []string{"charging", "discharging", "full"} {
		if !states[want] {
			t.Fatalf("missing state %s: %v", want, states)
		}
	}
	events, err := repo.Events(context.Background(), now.Add(-48*time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("expected power and sleep events")
	}
}
