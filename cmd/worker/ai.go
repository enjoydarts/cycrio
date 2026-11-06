package main

import (
	"context"
	"time"

	"github.com/enjoydarts/cycrio/internal/airuntime"
)

const aiTestTaskName = "cycrio-ai-connection-test"

type AITestOutput struct {
	Status string `json:"status"`
}

func dispatchAITests(ctx context.Context, claim func(context.Context) ([]airuntime.Lease, error), submit func(context.Context, airuntime.Lease) error, interval time.Duration) {
	defer capturePollerPanic(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		claimCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		leases, err := claim(claimCtx)
		cancel()
		if err == nil {
			for _, lease := range leases {
				if ctx.Err() != nil {
					return
				}
				sendCtx, sendCancel := context.WithTimeout(ctx, 15*time.Second)
				// Failed delivery remains leased and can be recovered without repeating inference.
				if err := submit(sendCtx, lease); err != nil {
					reportWorkerFailure(ctx, "", "worker_ai_test", "submit_failed", err)
				}
				sendCancel()
			}
		} else {
			reportWorkerFailure(ctx, "", "worker_ai_test", "claim_failed", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
