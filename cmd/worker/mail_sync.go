package main

import (
	"context"
	"time"

	"github.com/enjoydarts/cycrio/internal/mailsync"
)

const mailDiscoverTaskName = "cycrio-mail-discover"

type MailDiscoverOutput struct {
	Status string `json:"status"`
}

func executeMailDiscovery(ctx context.Context, input mailsync.Lease, execute func(context.Context, mailsync.Lease) error, wakes ...chan<- struct{}) (MailDiscoverOutput, error) {
	defer wakeMailWork(wakes...)
	if err := execute(ctx, input); err != nil {
		return MailDiscoverOutput{Status: "failed"}, hideWorkerCause("mail discovery unavailable", err)
	}
	return MailDiscoverOutput{Status: "handled"}, nil
}
func dispatchMailDiscovery(ctx context.Context, claim func(context.Context) ([]mailsync.Lease, error), submit func(context.Context, mailsync.Lease) error, interval time.Duration, wakes ...<-chan struct{}) {
	defer capturePollerPanic(ctx)
	var wake <-chan struct{}
	if len(wakes) > 0 {
		wake = wakes[0]
	}
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
				sendCtx, stop := context.WithTimeout(ctx, 15*time.Second)
				if err := submit(sendCtx, lease); err != nil {
					reportWorkerFailure(ctx, "", "worker_mail_discovery", "submit_failed", err)
				}
				stop()
			}
		} else {
			reportWorkerFailure(ctx, "", "worker_mail_discovery", "claim_failed", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-wake:
		}
	}
}
