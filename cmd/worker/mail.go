package main

import (
	"context"
	"time"

	"github.com/enjoydarts/cycrio/internal/mailaccounts"
)

const mailProbeTaskName = "cycrio-mail-connection-check"

type MailProbeOutput struct {
	Status string `json:"status"`
}

func dispatchMailProbes(ctx context.Context, claim func(context.Context) ([]mailaccounts.Lease, error), submit func(context.Context, mailaccounts.Lease) error, interval time.Duration) {
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
				// Undelivered leases expire in the DB. Running checks are never retried here.
				if err := submit(sendCtx, lease); err != nil {
					reportWorkerFailure(ctx, "", "worker_mail_probe", "submit_failed")
				}
				sendCancel()
			}
		} else {
			reportWorkerFailure(ctx, "", "worker_mail_probe", "claim_failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
