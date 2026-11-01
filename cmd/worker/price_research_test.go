package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/enjoydarts/cycrio/internal/priceresearch"
	"sync/atomic"
	"testing"
	"time"
)

func TestResearchDispatcherContainsOnlyLeaseMetadata(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var got priceresearch.Lease
	dispatchPriceResearch(ctx, func(context.Context) ([]priceresearch.Lease, error) {
		return []priceresearch.Lease{{RunID: "run", LeaseID: "lease"}}, nil
	}, func(_ context.Context, in priceresearch.Lease) error { got = in; cancel(); return nil }, time.Hour)
	raw, err := json.Marshal(got)
	if err != nil || string(raw) != `{"run_id":"run","lease_id":"lease"}` {
		t.Fatal("invalid workflow metadata")
	}
}
func TestResearchDispatcherWaitsAfterClaimFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var calls atomic.Int64
	dispatchPriceResearch(ctx, func(context.Context) ([]priceresearch.Lease, error) {
		calls.Add(1)
		return nil, errors.New("private upstream")
	}, func(context.Context, priceresearch.Lease) error { t.Fatal("unexpected submission"); return nil }, time.Hour)
	if calls.Load() != 1 {
		t.Fatal("claim failure caused busy loop")
	}
}
