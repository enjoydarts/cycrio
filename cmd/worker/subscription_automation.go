package main

import (
	"context"
	"time"
)

// A DB-only recovery loop also handles results completed before worker restart.
// No event text or provider credentials are transported through job payloads.
func dispatchSubscriptionAutomation(ctx context.Context, register func(context.Context, int) (int, error), interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		batch, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, _ = register(batch, 20)
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
