package main

import (
	"context"
	"github.com/enjoydarts/cycrio/internal/healthchecks"
	"time"
)

func dispatchHeartbeat(ctx context.Context, ready func(context.Context) error, tick func(context.Context) error) {
	defer capturePollerPanic(ctx)
	// RunAsync returns before Hatchet registrations and ACTIVE heartbeats are
	// visible. Defer only the first outbound tick during that startup window;
	// HTTP readiness continues to use the real probe throughout. A persistent
	// outage must still reach the normal tick and its failure reporting.
	startup, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	poll := time.NewTicker(time.Second)
	defer poll.Stop()
	for startup.Err() == nil {
		probe, cancelProbe := context.WithTimeout(startup, 5*time.Second)
		err := ready(probe)
		cancelProbe()
		if err == nil {
			break
		}
		select {
		case <-startup.Done():
		case <-poll.C:
		}
	}
	poll.Stop()
	cancel()
	if ctx.Err() != nil {
		return
	}
	// Include readiness probes and credential lookup before the bounded 10s
	// outbound request. Other database-only pollers retain their 5s budget.
	dispatchSubscriptionAutomationBudget(ctx, func(ctx context.Context, _ int) (int, error) { return 0, tick(ctx) }, 10*time.Second, healthchecks.TickTimeout)
}
