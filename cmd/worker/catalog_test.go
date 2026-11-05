package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/modelcatalog"
)

func TestCatalogExecutionDistinguishesMissingCredentialsFromFailure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status string
		failed bool
	}{
		{name: "success", status: "ok"},
		{name: "missing credentials", err: modelcatalog.ErrCredentialsRequired, status: "credentials_required"},
		{name: "wrapped missing credentials", err: fmt.Errorf("lookup: %w", modelcatalog.ErrCredentialsRequired), status: "credentials_required"},
		{name: "provider unavailable", err: modelcatalog.ErrUnavailable, status: "failed", failed: true},
		{name: "private upstream failure", err: errors.New("private upstream details"), status: "failed", failed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			input := CatalogSyncInput{Provider: "openai", LeaseID: "lease-only"}
			out, err := executeCatalogSync(context.Background(), input, func(_ context.Context, provider, lease string) error {
				calls++
				if provider != input.Provider || lease != input.LeaseID {
					t.Fatal("catalog lease changed")
				}
				return tc.err
			})
			if calls != 1 || out.Status != tc.status || (err != nil) != tc.failed {
				t.Fatalf("calls=%d status=%q error=%v", calls, out.Status, err)
			}
			if err != nil && err.Error() != "catalog synchronization failed" {
				t.Fatal("private upstream error escaped")
			}
		})
	}
}

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
