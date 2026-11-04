package main

import (
	"context"
	"time"
)

// External deletion gets a longer deadline than DB-only maintenance. Its
// durable lease survives interruption; no private payload enters Hatchet.
func dispatchClerkErasure(ctx context.Context, drain func(context.Context, int) (int, error), interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		batch, cancel := context.WithTimeout(ctx, 20*time.Second)
		_, _ = drain(batch, 1)
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
