package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/enjoydarts/cycrio/internal/mailprocessing"
)

func TestMailProcessingCompletionDispatchesNextMessageWithoutPollingDelay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		wake := make(chan struct{}, 1)
		queued, completed, claims := true, 0, 0
		start := time.Now()
		go dispatchMailProcessing(ctx, func(context.Context) ([]mailprocessing.Lease, error) {
			claims++
			if !queued {
				return nil, nil
			}
			queued = false
			return []mailprocessing.Lease{{RunID: "run", LeaseID: "lease"}}, nil
		}, func(ctx context.Context, lease mailprocessing.Lease) error {
			go func() {
				_, _ = executeMailProcessing(ctx, lease, func(context.Context, mailprocessing.Lease) error {
					completed++
					// Emulate the authoritative Finish transaction committing the next item.
					queued = completed < 3
					return nil
				}, wake)
			}()
			return nil
		}, time.Hour, wake)
		synctest.Wait()
		if completed != 3 {
			t.Fatalf("processed %d/3 messages before the next recovery poll; completion must wake the dispatcher", completed)
		}
		if elapsed := time.Since(start); elapsed != 0 {
			t.Fatalf("queued messages waited for polling: %v", elapsed)
		}
		if claims != 4 {
			t.Fatalf("expected three claims and an idle check, got %d", claims)
		}
		cancel()
		synctest.Wait()
	})
}

func TestMailProcessingCompletionWakeIsNonBlockingAndKeepsSanitizedOutcome(t *testing.T) {
	for _, failed := range []bool{false, true} {
		for _, full := range []bool{false, true} {
			t.Run(fmt.Sprintf("failed=%v/full=%v", failed, full), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					wake := make(chan struct{}, 1)
					if full {
						wake <- struct{}{}
					}
					out, err := executeMailProcessing(context.Background(), mailprocessing.Lease{}, func(context.Context, mailprocessing.Lease) error {
						if failed {
							return errors.New("private upstream detail")
						}
						return nil
					}, wake)
					if len(wake) != 1 {
						t.Fatal("completion wake missing")
					}
					if failed {
						if out.Status != "failed" || err == nil || err.Error() != "mail processing unavailable" {
							t.Fatal("unsafe failure", out, err)
						}
					} else if out.Status != "handled" || err != nil {
						t.Fatal("changed success", out, err)
					}
				})
			})
		}
	}
}

func TestMailProcessingRecoveryPollStillFindsWorkWithoutCompletionWake(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		queued, submitted := false, 0
		go dispatchMailProcessing(ctx, func(context.Context) ([]mailprocessing.Lease, error) {
			if !queued {
				return nil, nil
			}
			queued = false
			return []mailprocessing.Lease{{RunID: "run", LeaseID: "lease"}}, nil
		}, func(context.Context, mailprocessing.Lease) error { submitted++; return nil }, 10*time.Second, nil)
		synctest.Wait()
		queued = true
		time.Sleep(10 * time.Second)
		synctest.Wait()
		if submitted != 1 {
			t.Fatalf("recovery poll submitted %d times", submitted)
		}
		cancel()
		synctest.Wait()
	})
}

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
	}, time.Millisecond, nil)
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
		}, nil)
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
		}, func(context.Context, mailprocessing.Lease) error { t.Error("invalid submission"); return nil }, time.Millisecond, nil)
		cancel()
		if cancelBefore && calls != 0 {
			t.Fatal("claim after shutdown")
		}
	}
}
