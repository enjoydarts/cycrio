package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestHeartbeatTickBudgetIncludesReadinessAndOutboundRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go dispatchHeartbeat(ctx, func(context.Context) error { return nil }, func(tick context.Context) error {
			time.Sleep(6 * time.Second)
			err := tick.Err()
			done <- err
			cancel()
			return err
		})
		if err := <-done; err != nil {
			t.Fatalf("外部送信の期限前にHeartbeat全体が打ち切られた: %v", err)
		}
	})
}

func TestHeartbeatWaitsForInitialReadinessOnly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &failureRecorder{}
		ctx, cancel := context.WithCancel(withWorkerOperation(withWorkerTelemetry(context.Background(), r), "worker_heartbeat"))
		defer cancel()
		var available atomic.Bool
		var ticks atomic.Int32
		ready := func(context.Context) error {
			if !available.Load() {
				return errors.New("worker unavailable")
			}
			return nil
		}
		go dispatchHeartbeat(ctx, ready, func(ctx context.Context) error {
			ticks.Add(1)
			return ready(ctx)
		})
		synctest.Wait()
		time.Sleep(3 * time.Second)
		synctest.Wait()
		if events := r.snapshot(); ticks.Load() != 0 || len(events) != 0 || ready(ctx) == nil {
			t.Fatalf("startup must remain unready without an outbound tick: ticks=%d events=%v", ticks.Load(), events)
		}
		available.Store(true)
		time.Sleep(time.Second)
		synctest.Wait()
		if events := r.snapshot(); ticks.Load() != 1 || len(events) != 0 {
			t.Fatalf("first ready tick: ticks=%d events=%v", ticks.Load(), events)
		}
		available.Store(false)
		time.Sleep(10 * time.Second)
		synctest.Wait()
		if events := r.snapshot(); ticks.Load() != 2 || len(events) != 1 || events[0].operation != "worker_heartbeat" || events[0].kind != "poller_failed" {
			t.Fatalf("post-startup failure must reach normal reporting: ticks=%d events=%v", ticks.Load(), events)
		}
	})
}

func TestHeartbeatInitiallyReadyTicksImmediately(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var ticks atomic.Int32
		go dispatchHeartbeat(ctx, func(context.Context) error { return nil }, func(context.Context) error { ticks.Add(1); return nil })
		synctest.Wait()
		if ticks.Load() != 1 {
			t.Fatalf("initially ready worker should tick immediately, got %d", ticks.Load())
		}
	})
}

func TestHeartbeatStartupFailureIsBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &failureRecorder{}
		ctx, cancel := context.WithCancel(withWorkerOperation(withWorkerTelemetry(context.Background(), r), "worker_heartbeat"))
		defer cancel()
		var ticks atomic.Int32
		unavailable := func(context.Context) error { return errors.New("worker unavailable") }
		go dispatchHeartbeat(ctx, unavailable, func(ctx context.Context) error { ticks.Add(1); return unavailable(ctx) })
		time.Sleep(59 * time.Second)
		synctest.Wait()
		if events := r.snapshot(); ticks.Load() != 0 || len(events) != 0 {
			t.Fatalf("tick before startup limit: ticks=%d events=%v", ticks.Load(), events)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if events := r.snapshot(); ticks.Load() != 1 || len(events) != 1 || events[0].kind != "poller_failed" {
			t.Fatalf("persistent outage must reach normal tick at limit: ticks=%d events=%v", ticks.Load(), events)
		}
	})
}

func TestHeartbeatStartupCancelsWithoutTickOrFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &failureRecorder{}
		ctx, cancel := context.WithCancel(withWorkerTelemetry(context.Background(), r))
		var ticks atomic.Int32
		go dispatchHeartbeat(ctx, func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }, func(context.Context) error { ticks.Add(1); return nil })
		synctest.Wait()
		cancel()
		synctest.Wait()
		if events := r.snapshot(); ticks.Load() != 0 || len(events) != 0 {
			t.Fatalf("cancellation must not tick or report: ticks=%d events=%v", ticks.Load(), events)
		}
	})
}

func TestHeartbeatStartupBlockedProbeIsBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var ticks atomic.Int32
		go dispatchHeartbeat(ctx, func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }, func(context.Context) error { ticks.Add(1); return nil })
		time.Sleep(60 * time.Second)
		synctest.Wait()
		if ticks.Load() != 1 {
			t.Fatalf("blocked probes must not extend startup indefinitely, ticks=%d", ticks.Load())
		}
	})
}

func TestHeartbeatStartupPanicFailsClosed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &failureRecorder{}
		ctx, cancel := withWorkerShutdown(withWorkerOperation(withWorkerTelemetry(context.Background(), r), "worker_heartbeat"))
		defer cancel(nil)
		var ticks atomic.Int32
		go dispatchHeartbeat(ctx, func(context.Context) error { panic("probe panic") }, func(context.Context) error { ticks.Add(1); return nil })
		synctest.Wait()
		if events := r.snapshot(); ctx.Err() == nil || ticks.Load() != 0 || len(events) != 1 || events[0].kind != "poller_panic" {
			t.Fatalf("panic must stop worker and report: err=%v ticks=%d events=%v", ctx.Err(), ticks.Load(), events)
		}
	})
}
