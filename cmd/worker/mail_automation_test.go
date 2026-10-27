package main

import (
	"context"
	"github.com/enjoydarts/cycrio/internal/mailsync"
	"testing"
	"time"
)

func TestDiscoveryCompletionCoalescesWakeWithoutWaitingForPoll(t *testing.T) {
	wake := make(chan struct{}, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for i := 0; i < 2; i++ {
		if _, e := executeMailDiscovery(ctx, mailsync.Lease{}, func(context.Context, mailsync.Lease) error { return nil }, wake); e != nil {
			t.Fatal(e)
		}
	}
	if len(wake) != 1 {
		t.Fatal("wake not coalesced")
	}
	calls := 0
	done := make(chan struct{})
	go func() {
		dispatchMailDiscovery(ctx, func(context.Context) ([]mailsync.Lease, error) {
			calls++
			if calls == 2 {
				cancel()
			}
			return nil, nil
		}, func(context.Context, mailsync.Lease) error { return nil }, time.Hour, wake)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("completion waited for poll")
	}
	if calls != 2 {
		t.Fatal(calls)
	}
}
func TestMailAutomationTickHasDeadlineAndCompletionWake(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	wake := make(chan struct{}, 1)
	wake <- struct{}{}
	calls := 0
	dispatchMailAutomation(ctx, func(c context.Context, limit int) (int, error) {
		calls++
		deadline, ok := c.Deadline()
		if !ok || time.Until(deadline) > 5*time.Second || limit != 20 {
			t.Error("unbounded coordinator")
		}
		if calls == 2 {
			cancel()
		}
		return 0, nil
	}, time.Hour, wake)
	if calls != 2 {
		t.Fatal(calls)
	}
}
