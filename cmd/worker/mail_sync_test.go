package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/mailsync"
)

func TestMailDiscoveryDispatchesOnlyLeasedIDsWithBoundedCalls(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	want := mailsync.Lease{RunID: "internal-run", LeaseID: "internal-lease"}
	submitted := 0
	dispatchMailDiscovery(ctx, func(ctx context.Context) ([]mailsync.Lease, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 5*time.Second {
			t.Error("unbounded claim")
		}
		return []mailsync.Lease{want}, nil
	}, func(ctx context.Context, got mailsync.Lease) error {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 15*time.Second {
			t.Error("unbounded delivery")
		}
		if got != want {
			t.Error("wrong lease")
		}
		submitted++
		cancel()
		return nil
	}, time.Millisecond)
	if submitted != 1 {
		t.Fatal("lease not dispatched")
	}
}
func TestMailDiscoveryDoesNotSubmitAfterClaimFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dispatchMailDiscovery(ctx, func(context.Context) ([]mailsync.Lease, error) {
		cancel()
		return []mailsync.Lease{{RunID: "must-not-send"}}, errors.New("synthetic-private")
	}, func(context.Context, mailsync.Lease) error { t.Error("submitted failed claim"); return nil }, time.Millisecond)
}
