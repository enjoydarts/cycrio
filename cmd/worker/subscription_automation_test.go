package main

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

func TestSubscriptionAutomationRunsBoundedRecoveryWithoutProviderCalls(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		calls := 0
		go dispatchSubscriptionAutomation(ctx, func(ctx context.Context, limit int) (int, error) {
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > 5*time.Second || limit != 20 {
				t.Error("unbounded DB-only batch", limit)
			}
			calls++
			return 0, nil
		}, time.Second)
		synctest.Wait()
		if calls != 1 {
			t.Fatal(calls)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if calls != 2 {
			t.Fatal(calls)
		}
		cancel()
		synctest.Wait()
		if calls != 2 {
			t.Fatal("work after stop")
		}
	})
}
