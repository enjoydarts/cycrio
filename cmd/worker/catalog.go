package main

import (
	"context"
	"time"

	"github.com/enjoydarts/cycrio/internal/modelcatalog"
)

const catalogTaskName = "cycrio-model-catalog-sync"

type CatalogSyncInput struct {
	Provider string `json:"provider"`
	LeaseID  string `json:"lease_id"`
}

type CatalogSyncOutput struct {
	Status string `json:"status"`
}

func dispatchCatalog(ctx context.Context, claim func(context.Context) ([]modelcatalog.SyncLease, error), submit func(context.Context, CatalogSyncInput) error, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		leases, err := claim(ctx)
		if err == nil {
			for _, lease := range leases {
				if ctx.Err() != nil {
					return
				}
				runCtx, cancel := context.WithTimeout(ctx, 75*time.Second)
				// Submission/processing failures remain bounded by the DB lease;
				// never log SDK errors that might contain upstream data.
				_ = submit(runCtx, CatalogSyncInput{Provider: lease.Provider, LeaseID: lease.LeaseID})
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
