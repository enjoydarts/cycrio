package main

import (
	"context"
	"time"

	"github.com/enjoydarts/cycrio/internal/notificationruntime"
)

const notificationTaskName = "cycrio-notification-delivery"

func dispatchNotifications(ctx context.Context, claim func(context.Context) ([]notificationruntime.Task, error), submit func(context.Context, notificationruntime.Task) error, interval time.Duration) {
	defer capturePollerPanic(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		claimCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		tasks, err := claim(claimCtx)
		cancel()
		if err == nil {
			for _, task := range tasks {
				if ctx.Err() != nil {
					return
				}
				submitCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
				// Only abandoned preparations can be reclaimed by the repository.
				// Ambiguous submissions are not retried by this dispatcher.
				if err := submit(submitCtx, task); err != nil {
					reportWorkerFailure(ctx, "", "worker_notification_delivery", "submit_failed", err)
				}
				cancel()
			}
		} else {
			reportWorkerFailure(ctx, "", "worker_notification_delivery", "claim_failed", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
