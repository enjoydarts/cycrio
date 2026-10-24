package main

import (
	"context"
	"errors"
	"testing"
	"time"

	hatchet "github.com/hatchet-dev/hatchet/sdks/go"
)

func TestAwaitResultHonorsCancellationWhileSDKBlocks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	assertAwaitStops(t, ctx, cancel, context.Canceled)
}

func TestAwaitResultHonorsDeadlineWhileSDKBlocks(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	assertAwaitStops(t, ctx, func() {}, context.DeadlineExceeded)
}

func assertAwaitStops(t *testing.T, ctx context.Context, cancel func(), want error) {
	t.Helper()
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	finished := make(chan error, 1)
	go func() {
		_, err := awaitResult(ctx, func() (*hatchet.WorkflowResult, error) {
			close(started)
			<-release // Reproduce the SDK's non-cancellable result wait.
			return nil, nil
		})
		finished <- err
	}()
	<-started
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, want) {
			t.Fatalf("error = %v, want %v", err, want)
		}
	case <-time.After(time.Second):
		t.Fatal("result wait exceeded cancellation/deadline")
	}
}

func TestAwaitResultPreservesSuccess(t *testing.T) {
	want := &hatchet.WorkflowResult{RunId: "check-run"}
	got, err := awaitResult(context.Background(), func() (*hatchet.WorkflowResult, error) { return want, nil })
	if err != nil || got != want {
		t.Fatalf("result = %v, error = %v", got, err)
	}
}

func TestAwaitResultPreservesSDKFailure(t *testing.T) {
	want := errors.New("SDK failure")
	_, err := awaitResult(context.Background(), func() (*hatchet.WorkflowResult, error) { return nil, want })
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}
