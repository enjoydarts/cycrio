package main

import (
	"context"

	hatchet "github.com/hatchet-dev/hatchet/sdks/go"
)

func awaitResult(ctx context.Context, wait func() (*hatchet.WorkflowResult, error)) (*hatchet.WorkflowResult, error) {
	type result struct {
		value *hatchet.WorkflowResult
		err   error
	}
	// The SDK's result wait ignores cancellation. Buffer the response so a late
	// completion can exit after this short-lived CLI has stopped waiting.
	completed := make(chan result, 1)
	go func() {
		value, err := wait()
		completed <- result{value: value, err: err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-completed:
		return result.value, result.err
	}
}
