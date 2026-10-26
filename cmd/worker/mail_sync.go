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

func dispatchMailDiscovery(ctx context.Context, claim func(context.Context) ([]mailsync.Lease, error), submit func(context.Context, mailsync.Lease) error, interval time.Duration) {
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
				_ = submit(sendCtx, lease)
				stop()
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
