package main

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestClerkErasureDispatchBoundsNetworkWorkAndStopsOnCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var calls atomic.Int64
		go dispatchClerkErasure(ctx, func(ctx context.Context, limit int) (int, error) {
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) != 20*time.Second || limit != 1 {
				t.Error("unbounded external erasure pass", limit)
			}
			calls.Add(1)
			return 0, nil
		}, 10*time.Second)
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatal(calls.Load())
		}
		time.Sleep(10 * time.Second)
		synctest.Wait()
		if calls.Load() != 2 {
			t.Fatal(calls.Load())
		}
		cancel()
		synctest.Wait()
		if calls.Load() != 2 {
			t.Fatal("network work after cancellation")
		}
	})
}
