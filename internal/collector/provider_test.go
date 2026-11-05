package collector

import (
	"context"
	"testing"
	"time"

	"github.com/nuzair46/omabat/internal/battery"
)

type recordingRepository struct {
	snapshot battery.Snapshot
}

func (r *recordingRepository) RecordSnapshot(_ context.Context, s battery.Snapshot) ([]battery.Event, error) {
	r.snapshot = s
	return nil, nil
}

func (*recordingRepository) RecordSleep(context.Context, battery.Snapshot) error  { return nil }
func (*recordingRepository) RecordResume(context.Context, battery.Snapshot) error { return nil }
func (*recordingRepository) Maintain(context.Context, time.Time) error            { return nil }

func TestServicePersistsCollectionCadence(t *testing.T) {
	for _, interval := range []time.Duration{0, 3 * time.Second, 30 * time.Minute} {
		t.Run(interval.String(), func(t *testing.T) {
			repo := &recordingRepository{}
			service := Service{Provider: fixedProvider{s: battery.Snapshot{DeviceID: "BAT0"}}, Repo: repo, Interval: interval}
			s, _, err := service.Collect(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			want := interval
			if want <= 0 {
				want = battery.DefaultSampleInterval
			}
			if s.SampleInterval != want || repo.snapshot.SampleInterval != want {
				t.Fatalf("cadence: returned=%v persisted=%v want=%v", s.SampleInterval, repo.snapshot.SampleInterval, want)
			}
		})
	}
}
