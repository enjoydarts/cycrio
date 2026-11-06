package main

import (
	"context"
	"errors"
)

var errWorkerPollerFailed = errors.New("worker poller failed")

type workerShutdownKey struct{}

// A required poller cannot silently disappear while the Hatchet groups remain
// healthy. Its fixed cancellation cause follows the ordinary shutdown path.
func withWorkerShutdown(parent context.Context) (context.Context, context.CancelCauseFunc) {
	ctx, cancel := context.WithCancelCause(parent)
	return context.WithValue(ctx, workerShutdownKey{}, cancel), cancel
}

func workerShutdownError(ctx context.Context) error {
	if errors.Is(context.Cause(ctx), errWorkerPollerFailed) {
		return errWorkerPollerFailed
	}
	return nil
}

func workerReadiness(root context.Context, check func(context.Context) error) func(context.Context) error {
	return func(ctx context.Context) error {
		if root.Err() == nil {
			if err := check(ctx); err != nil {
				return err
			}
			if root.Err() == nil {
				return nil
			}
		}
		if err := workerShutdownError(root); err != nil {
			return err
		}
		return errors.New("worker unavailable")
	}
}
