package main

import (
	"context"
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
