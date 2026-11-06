package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestSubscriptionAutomationRunsBoundedRecoveryWithoutProviderCalls(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var calls atomic.Int64
		go dispatchSubscriptionAutomation(ctx, func(ctx context.Context, limit int) (int, error) {
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > 5*time.Second || limit != 20 {
				t.Error("unbounded DB-only batch", limit)
			}
			calls.Add(1)
			return 0, nil
		}, time.Second)
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatal(calls.Load())
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if calls.Load() != 2 {
			t.Fatal(calls.Load())
		}
		cancel()
		synctest.Wait()
		if calls.Load() != 2 {
			t.Fatal("work after stop")
		}
	})
}

func TestScheduledChangesRecoveryRetriesDatabaseFailureAndStops(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var calls atomic.Int64
		go dispatchSubscriptionAutomation(ctx, func(batch context.Context, limit int) (int, error) {
			deadline, ok := batch.Deadline()
			if !ok || time.Until(deadline) != 5*time.Second || limit != 20 {
				t.Error("unbounded scheduled change batch", limit)
			}
			calls.Add(1)
			return 0, errors.New("transient database failure")
		}, 2*time.Second)
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatal("startup recovery missing", calls.Load())
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if calls.Load() != 2 {
			t.Fatal("database failure prevented recovery", calls.Load())
		}
		cancel()
		synctest.Wait()
		time.Sleep(2 * time.Second)
		if calls.Load() != 2 {
			t.Fatal("scheduled changes continued after cancellation")
		}
	})
}
