package main

import (
	"context"
	"time"
)

// External deletion gets a longer deadline than DB-only maintenance. Its
// durable lease survives interruption; no private payload enters Hatchet.
func dispatchClerkErasure(ctx context.Context, drain func(context.Context, int) (int, error), interval time.Duration) {
	defer capturePollerPanic(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		batch, cancel := context.WithTimeout(ctx, 20*time.Second)
		if _, err := drain(batch, 1); err != nil {
			reportWorkerFailure(ctx, "", "", "poller_failed", err)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
