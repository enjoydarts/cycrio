package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/mailprocessing"
)

func TestMailProcessingDispatchesOnlyIDsWithBoundedCalls(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	want := mailprocessing.Lease{RunID: "run", LeaseID: "lease"}
	n := 0
	dispatchMailProcessing(ctx, func(ctx context.Context) ([]mailprocessing.Lease, error) {
		d, ok := ctx.Deadline()
		if !ok || time.Until(d) > 5*time.Second {
			t.Error("unbounded claim")
		}
		return []mailprocessing.Lease{want, want}, nil
	}, func(ctx context.Context, got mailprocessing.Lease) error {
		d, ok := ctx.Deadline()
		if !ok || time.Until(d) > 15*time.Second {
			t.Error("unbounded submit")
		}
		if got != want {
			t.Error("wrong lease")
		}
		n++
		cancel()
		return nil
	}, time.Millisecond)
	if n != 1 {
		t.Fatal(n)
	}
	data, _ := json.Marshal(want)
	if string(data) != `{"run_id":"run","lease_id":"lease"}` {
		t.Fatal("private payload", string(data))
	}
}

func TestMailProcessingTaskReturnsOnlyFixedStatusAndSanitizedError(t *testing.T) {
	for _, fail := range []bool{false, true} {
		lease := mailprocessing.Lease{RunID: "run", LeaseID: "lease"}
		out, err := executeMailProcessing(context.Background(), lease, func(_ context.Context, got mailprocessing.Lease) error {
			if got != lease {
				t.Fatal("wrong lease")
			}
			if fail {
				return errors.New("synthetic-private-mail-and-key")
			}
			return nil
		})
		want := `{"status":"handled"}`
		if fail {
			want = `{"status":"failed"}`
			if err == nil || err.Error() != "mail processing unavailable" {
				t.Fatal("unsanitized worker error", err)
			}
		} else if err != nil {
			t.Fatal(err)
		}
		data, marshalErr := json.Marshal(out)
		if marshalErr != nil || string(data) != want {
			t.Fatal("private output", string(data), marshalErr)
		}
	}
}
func TestMailProcessingDoesNotSubmitFailedClaimOrCancelledContext(t *testing.T) {
	for _, cancelBefore := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		if cancelBefore {
			cancel()
		}
		calls := 0
		dispatchMailProcessing(ctx, func(context.Context) ([]mailprocessing.Lease, error) {
			calls++
			cancel()
			return []mailprocessing.Lease{{RunID: "private"}}, errors.New("private upstream")
		}, func(context.Context, mailprocessing.Lease) error { t.Error("invalid submission"); return nil }, time.Millisecond)
		cancel()
		if cancelBefore && calls != 0 {
			t.Fatal("claim after shutdown")
		}
	}
}
