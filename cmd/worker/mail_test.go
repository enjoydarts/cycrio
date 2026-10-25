package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/mailaccounts"
)

func TestMailProbeDispatchDeliversOnlyInternalIDsAndStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	claimed, sent := 0, 0
	dispatchMailProbes(ctx, func(context.Context) ([]mailaccounts.Lease, error) {
		claimed++
		return []mailaccounts.Lease{{ProbeID: "internal-probe", LeaseID: "internal-lease"}, {ProbeID: "next-probe", LeaseID: "next-lease"}}, nil
	}, func(_ context.Context, lease mailaccounts.Lease) error {
		sent++
		raw, err := json.Marshal(lease)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]string
		if json.Unmarshal(raw, &fields) != nil || len(fields) != 2 || fields["probe_id"] != "internal-probe" || fields["lease_id"] != "internal-lease" {
			t.Fatal("unexpected mail job payload", string(raw))
		}
		cancel()
		return errors.New("synthetic delivery failure")
	}, time.Millisecond)
	if claimed != 1 || sent != 1 {
		t.Fatal("delivery continued after cancellation", claimed, sent)
	}
}

func TestMailProbeDispatcherDoesNotSendAfterClaimFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sent := false
	dispatchMailProbes(ctx, func(context.Context) ([]mailaccounts.Lease, error) {
		cancel()
		return []mailaccounts.Lease{{ProbeID: "ignored"}}, errors.New("synthetic storage failure")
	}, func(context.Context, mailaccounts.Lease) error { sent = true; return nil }, time.Millisecond)
	if sent {
		t.Fatal("submitted work after a failed claim")
	}
}
