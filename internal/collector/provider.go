package collector

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nuzair46/omabat/internal/battery"
)

type Provider interface {
	Snapshot(context.Context) (battery.Snapshot, error)
}

type CompositeProvider struct {
	Primary  Provider
	Fallback Provider
}

func (p CompositeProvider) Snapshot(ctx context.Context) (battery.Snapshot, error) {
	var primary, fallback battery.Snapshot
	var primaryErr, fallbackErr error
	if p.Primary != nil {
		primary, primaryErr = p.Primary.Snapshot(ctx)
	}
	if p.Fallback != nil {
		fallback, fallbackErr = p.Fallback.Snapshot(ctx)
	}
	if primaryErr != nil && fallbackErr != nil {
		return battery.Snapshot{}, fmt.Errorf("battery providers failed: %v; %v", primaryErr, fallbackErr)
	}
	if primaryErr != nil {
		return fallback, nil
	}
	if fallbackErr != nil {
		return primary, nil
	}
	return battery.Merge(primary, fallback), nil
}

type Service struct {
	Provider Provider
	Repo     Repository
}

type Repository interface {
	RecordSnapshot(context.Context, battery.Snapshot) ([]battery.Event, error)
	RecordSleep(context.Context, battery.Snapshot) error
	RecordResume(context.Context, battery.Snapshot) error
	Maintain(context.Context, time.Time) error
}

func (s Service) Collect(ctx context.Context) (battery.Snapshot, []battery.Event, error) {
	if s.Provider == nil || s.Repo == nil {
		return battery.Snapshot{}, nil, errors.New("collector is not configured")
	}
	snap, err := s.Provider.Snapshot(ctx)
	if err != nil {
		return battery.Snapshot{}, nil, err
	}
	events, err := s.Repo.RecordSnapshot(ctx, snap)
	return snap, events, err
}
