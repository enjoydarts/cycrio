package main

import (
	"context"
	"time"
)

// A DB-only recovery loop also handles results completed before worker restart.
// No event text or provider credentials are transported through job payloads.
func dispatchSubscriptionAutomation(ctx context.Context, register func(context.Context, int) (int, error), interval time.Duration) {
	dispatchSubscriptionAutomationBudget(ctx, register, interval, 5*time.Second)
}

func dispatchSubscriptionAutomationBudget(ctx context.Context, register func(context.Context, int) (int, error), interval, budget time.Duration) {
	defer capturePollerPanic(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		batch, cancel := context.WithTimeout(ctx, budget)
		if _, err := register(batch, 20); err != nil {
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
