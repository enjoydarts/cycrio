package main

import (
	"context"
	"time"

	"github.com/enjoydarts/cycrio/internal/notificationruntime"
)

const notificationTaskName = "cycrio-notification-delivery"

func dispatchNotifications(ctx context.Context, claim func(context.Context) ([]notificationruntime.Task, error), submit func(context.Context, notificationruntime.Task) error, interval time.Duration) {
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
				_ = submit(submitCtx, task)
				cancel()
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
