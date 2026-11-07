package main

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func TestHeartbeatWaitsForInitialReadinessOnly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &failureRecorder{}
		ctx, cancel := context.WithCancel(withWorkerOperation(withWorkerTelemetry(context.Background(), r), "worker_heartbeat"))
		defer cancel()
		available, ticks := false, 0
		ready := func(context.Context) error {
			if !available {
				return errors.New("worker unavailable")
			}
			return nil
		}
		go dispatchHeartbeat(ctx, ready, func(ctx context.Context) error {
			ticks++
			return ready(ctx)
		})
		synctest.Wait()
		time.Sleep(3 * time.Second)
		if ticks != 0 || len(r.events) != 0 || ready(ctx) == nil {
			t.Fatalf("startup must remain unready without an outbound tick: ticks=%d events=%v", ticks, r.events)
		}
		available = true
		time.Sleep(time.Second)
		synctest.Wait()
		if ticks != 1 || len(r.events) != 0 {
			t.Fatalf("first ready tick: ticks=%d events=%v", ticks, r.events)
		}
		available = false
		time.Sleep(10 * time.Second)
		synctest.Wait()
		if ticks != 2 || len(r.events) != 1 || r.events[0].operation != "worker_heartbeat" || r.events[0].kind != "poller_failed" {
			t.Fatalf("post-startup failure must reach normal reporting: ticks=%d events=%v", ticks, r.events)
		}
	})
}

func TestHeartbeatInitiallyReadyTicksImmediately(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ticks := 0
		go dispatchHeartbeat(ctx, func(context.Context) error { return nil }, func(context.Context) error { ticks++; return nil })
		synctest.Wait()
		if ticks != 1 {
			t.Fatalf("initially ready worker should tick immediately, got %d", ticks)
		}
	})
}

func TestHeartbeatStartupFailureIsBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &failureRecorder{}
		ctx, cancel := context.WithCancel(withWorkerOperation(withWorkerTelemetry(context.Background(), r), "worker_heartbeat"))
		defer cancel()
		ticks := 0
		unavailable := func(context.Context) error { return errors.New("worker unavailable") }
		go dispatchHeartbeat(ctx, unavailable, func(ctx context.Context) error { ticks++; return unavailable(ctx) })
		time.Sleep(59 * time.Second)
		synctest.Wait()
		if ticks != 0 || len(r.events) != 0 {
			t.Fatalf("tick before startup limit: ticks=%d events=%v", ticks, r.events)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if ticks != 1 || len(r.events) != 1 || r.events[0].kind != "poller_failed" {
			t.Fatalf("persistent outage must reach normal tick at limit: ticks=%d events=%v", ticks, r.events)
		}
	})
}

func TestHeartbeatStartupCancelsWithoutTickOrFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &failureRecorder{}
		ctx, cancel := context.WithCancel(withWorkerTelemetry(context.Background(), r))
		ticks := 0
		go dispatchHeartbeat(ctx, func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }, func(context.Context) error { ticks++; return nil })
		synctest.Wait()
		cancel()
		synctest.Wait()
		if ticks != 0 || len(r.events) != 0 {
			t.Fatalf("cancellation must not tick or report: ticks=%d events=%v", ticks, r.events)
		}
	})
}

func TestHeartbeatStartupBlockedProbeIsBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ticks := 0
		go dispatchHeartbeat(ctx, func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }, func(context.Context) error { ticks++; return nil })
		time.Sleep(60 * time.Second)
		synctest.Wait()
		if ticks != 1 {
			t.Fatalf("blocked probes must not extend startup indefinitely, ticks=%d", ticks)
		}
	})
}

func TestHeartbeatStartupPanicFailsClosed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &failureRecorder{}
		ctx, cancel := withWorkerShutdown(withWorkerOperation(withWorkerTelemetry(context.Background(), r), "worker_heartbeat"))
		defer cancel(nil)
		ticks := 0
		go dispatchHeartbeat(ctx, func(context.Context) error { panic("probe panic") }, func(context.Context) error { ticks++; return nil })
		synctest.Wait()
		if ctx.Err() == nil || ticks != 0 || len(r.events) != 1 || r.events[0].kind != "poller_panic" {
			t.Fatalf("panic must stop worker and report: err=%v ticks=%d events=%v", ctx.Err(), ticks, r.events)
		}
	})
}
