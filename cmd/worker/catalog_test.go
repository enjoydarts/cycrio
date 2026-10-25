package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/modelcatalog"
)

func TestCatalogDispatcherUsesOnlyProviderAndLease(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var submitted CatalogSyncInput
	dispatchCatalog(ctx, func(context.Context) ([]modelcatalog.SyncLease, error) {
		return []modelcatalog.SyncLease{{Provider: "openrouter", LeaseID: "lease-only"}}, nil
	}, func(_ context.Context, input CatalogSyncInput) error { submitted = input; cancel(); return nil }, time.Hour)
	data, err := json.Marshal(submitted)
	if err != nil || string(data) != `{"provider":"openrouter","lease_id":"lease-only"}` {
		t.Fatal("invalid workflow payload")
	}
	if strings.Contains(string(data), "secret") {
		t.Fatal("workflow contains secret data")
	}
}

func TestCatalogDispatcherWaitsAfterClaimFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var calls atomic.Int64
	dispatchCatalog(ctx, func(context.Context) ([]modelcatalog.SyncLease, error) {
		calls.Add(1)
		return nil, errors.New("private upstream value")
	}, func(context.Context, CatalogSyncInput) error { t.Fatal("failure must not submit job"); return nil }, time.Hour)
	if calls.Load() != 1 {
		t.Fatal("claim failure caused a busy loop")
	}
}

func TestCatalogDispatcherCancelsPendingSubmission(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		dispatchCatalog(ctx, func(context.Context) ([]modelcatalog.SyncLease, error) {
			return []modelcatalog.SyncLease{{Provider: "openai", LeaseID: "lease"}}, nil
		}, func(ctx context.Context, _ CatalogSyncInput) error { close(started); <-ctx.Done(); return ctx.Err() }, time.Hour)
		close(done)
	}()
	<-started
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("dispatcher did not stop")
	}
}
