package main

import (
	"context"
	"errors"
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

func executeCatalogSync(ctx context.Context, input CatalogSyncInput, sync func(context.Context, string, string) error) (CatalogSyncOutput, error) {
	err := sync(ctx, input.Provider, input.LeaseID)
	if errors.Is(err, modelcatalog.ErrCredentialsRequired) {
		return CatalogSyncOutput{Status: "credentials_required"}, nil
	}
	if err != nil {
		return CatalogSyncOutput{Status: "failed"}, hideWorkerCause("catalog synchronization failed", err)
	}
	return CatalogSyncOutput{Status: "ok"}, nil
}

func dispatchCatalog(ctx context.Context, claim func(context.Context) ([]modelcatalog.SyncLease, error), submit func(context.Context, CatalogSyncInput) error, interval time.Duration) {
	defer capturePollerPanic(ctx)
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
				if err := submit(runCtx, CatalogSyncInput{Provider: lease.Provider, LeaseID: lease.LeaseID}); err != nil {
					reportWorkerFailure(ctx, "", "worker_catalog_sync", "submit_failed", err)
				}
				cancel()
			}
		} else {
			reportWorkerFailure(ctx, "", "worker_catalog_sync", "claim_failed", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
