package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/notificationruntime"
)

func TestNotificationDispatcherCancelsWithoutReplayingFailedSubmission(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	dispatchNotifications(ctx, func(context.Context) ([]notificationruntime.Task, error) {
		return []notificationruntime.Task{{DeliveryID: "first"}, {DeliveryID: "second"}}, nil
	}, func(_ context.Context, task notificationruntime.Task) error {
		calls++
		if task.DeliveryID != "first" {
			t.Fatal("submitted after cancellation", task)
		}
		cancel()
		return errors.New("submission uncertain")
	}, time.Millisecond)
	if calls != 1 {
		t.Fatal(calls)
	}
}

func TestNotificationDispatcherDoesNotSubmitFailedClaims(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dispatchNotifications(ctx, func(context.Context) ([]notificationruntime.Task, error) {
		cancel()
		return []notificationruntime.Task{{DeliveryID: "ignored"}}, errors.New("claim failed")
	}, func(context.Context, notificationruntime.Task) error { t.Fatal("submitted failed claim"); return nil }, time.Millisecond)
}
