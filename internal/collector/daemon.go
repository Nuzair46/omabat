package collector

import (
	"context"
	"log"
	"time"

	"github.com/godbus/dbus/v5"
)

const logindMatch = "type='signal',sender='org.freedesktop.login1',interface='org.freedesktop.login1.Manager',member='PrepareForSleep'"

func (s Service) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = 120 * time.Second
	}
	if _, _, err := s.Collect(ctx); err != nil {
		log.Printf("initial collection failed: %v", err)
	}
	if err := s.Repo.Maintain(ctx, time.Now()); err != nil {
		log.Printf("initial database maintenance failed: %v", err)
	}

	signals := make(chan *dbus.Signal, 8)
	conn, err := dbus.SystemBus()
	if err != nil {
		log.Printf("sleep/resume monitoring unavailable: %v", err)
	} else if call := conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, logindMatch); call.Err != nil {
		log.Printf("sleep/resume monitoring unavailable: %v", call.Err)
	} else {
		conn.Signal(signals)
		defer conn.RemoveSignal(signals)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	maintenance := time.NewTicker(6 * time.Hour)
	defer maintenance.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if _, _, err := s.Collect(ctx); err != nil {
				log.Printf("collection failed: %v", err)
			}
		case <-maintenance.C:
			if err := s.Repo.Maintain(ctx, time.Now()); err != nil {
				log.Printf("database maintenance failed: %v", err)
			}
		case signal := <-signals:
			if signal == nil || signal.Name != "org.freedesktop.login1.Manager.PrepareForSleep" || len(signal.Body) == 0 {
				continue
			}
			sleeping, ok := signal.Body[0].(bool)
			if !ok {
				continue
			}
			snap, err := s.Provider.Snapshot(ctx)
			if err != nil {
				log.Printf("sleep transition collection failed: %v", err)
				continue
			}
			if sleeping {
				err = s.Repo.RecordSleep(ctx, snap)
			} else {
				err = s.Repo.RecordResume(ctx, snap)
			}
			if err != nil {
				log.Printf("sleep transition recording failed: %v", err)
			}
		}
	}
}
