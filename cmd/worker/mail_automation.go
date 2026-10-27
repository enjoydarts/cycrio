package main

import (
	"context"
	"time"
)

func wakeMailWork(wakes ...chan<- struct{}) {
	for _, wake := range wakes {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}
func dispatchMailAutomation(ctx context.Context, tick func(context.Context, int) (int, error), interval time.Duration, wake <-chan struct{}, dispatchWakes ...chan<- struct{}) {
	timer := time.NewTicker(interval)
	defer timer.Stop()
	for ctx.Err() == nil {
		work, stop := context.WithTimeout(ctx, 5*time.Second)
		_, err := tick(work, 20)
		stop()
		if err == nil {
			wakeMailWork(dispatchWakes...)
		}
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-wake:
		}
	}
}
