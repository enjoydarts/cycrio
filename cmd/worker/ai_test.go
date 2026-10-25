package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/airuntime"
)

func TestAIJobPayloadContainsOnlyInternalLeaseIdentifiers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	count := 0
	dispatchAITests(ctx, func(context.Context) ([]airuntime.Lease, error) {
		return []airuntime.Lease{{TestRunID: "internal-test", LeaseID: "internal-lease"}}, nil
	}, func(_ context.Context, in airuntime.Lease) error {
		count++
		b, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]string
		if json.Unmarshal(b, &fields) != nil || len(fields) != 2 || fields["test_run_id"] != "internal-test" || fields["lease_id"] != "internal-lease" {
			t.Fatal(string(b))
		}
		cancel()
		return nil
	}, time.Millisecond)
	if count != 1 {
		t.Fatal("missing or duplicate delivery", count)
	}
}
