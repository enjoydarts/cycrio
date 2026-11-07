package main

import (
	"context"
	"time"

	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/telemetry"
)

func queueAnalyticsLoop(ctx context.Context, repo *database.JobTelemetryRepository, reporter *telemetry.Reporter) {
	defer func() { _ = recover() }()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if reporter.PublicConfig(ctx) == (telemetry.Config{}) {
				continue
			}
			bounded, cancel := context.WithTimeout(ctx, time.Second)
			stats, err := repo.Queues(bounded)
			cancel()
			if err != nil {
				continue
			} // Missing snapshots are never reported as zeros.
			for _, s := range stats {
				reporter.TrackQueue(s.Queue, s.Queued, s.Active, s.OldestAgeSeconds)
			}
		}
	}
}
