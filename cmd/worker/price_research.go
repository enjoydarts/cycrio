package main

import (
	"context"
	"github.com/enjoydarts/cycrio/internal/priceresearch"
	"time"
)

const priceResearchTaskName = "cycrio-price-research"

type PriceResearchOutput struct {
	Status string `json:"status"`
}

func dispatchPriceResearch(ctx context.Context, claim func(context.Context) ([]priceresearch.Lease, error), submit func(context.Context, priceresearch.Lease) error, interval time.Duration) {
	defer capturePollerPanic(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		leases, err := claim(ctx)
		if err == nil {
			for _, l := range leases {
				if ctx.Err() != nil {
					return
				}
				sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				if err := submit(sendCtx, l); err != nil {
					reportWorkerFailure(ctx, "", "worker_price_research", "submit_failed")
				}
				cancel()
			}
		} else {
			reportWorkerFailure(ctx, "", "worker_price_research", "claim_failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
