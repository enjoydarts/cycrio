package main

import (
	"context"
	"errors"
	"time"

	"github.com/enjoydarts/cycrio/internal/database"
)

type workerFailureReporter interface{ Report(string, string, string) }
type workerTelemetryKey struct{}
type workerOperationKey struct{}

func withWorkerTelemetry(ctx context.Context, r workerFailureReporter) context.Context {
	return context.WithValue(ctx, workerTelemetryKey{}, r)
}
func withWorkerOperation(ctx context.Context, operation string) context.Context {
	return context.WithValue(ctx, workerOperationKey{}, operation)
}
func reportWorkerFailure(ctx context.Context, owner, operation, kind string) {
	if operation == "" {
		operation, _ = ctx.Value(workerOperationKey{}).(string)
	}
	if owner == "" {
		owner = "cycrio-system"
	}
	if r, ok := ctx.Value(workerTelemetryKey{}).(workerFailureReporter); ok {
		r.Report(owner, operation, kind)
	}
}
func capturePollerPanic(ctx context.Context) {
	if recover() != nil {
		reportWorkerFailure(ctx, "", "", "poller_panic")
	}
}

type jobOutcomeReader func(context.Context, string, string, time.Time) (database.JobTelemetryOutcome, error)

func captureWorkerTask[T any](ctx context.Context, read jobOutcomeReader, operation, jobKind, id string, execute func() (T, error)) (out T, err error) {
	started := time.Now()
	defer func() {
		panicked := recover() != nil
		if panicked {
			err = errors.New("worker task unavailable")
		}
		outcome := database.JobTelemetryOutcome{}
		if read != nil && jobKind != "" {
			bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
			defer cancel()
			var readErr error
			outcome, readErr = read(bounded, jobKind, id, started)
			if readErr != nil {
				reportWorkerFailure(ctx, "", operation, "outcome_unavailable")
				return
			}
		}
		kind := ""
		if panicked || outcome.Code == "execution_panic" {
			kind = "task_panic"
		} else if unexpectedJobFailure(outcome) {
			kind = "task_failed"
		} else if err != nil {
			kind = "task_failed"
		}
		if kind != "" {
			reportWorkerFailure(ctx, outcome.OwnerID, operation, kind)
		}
	}()
	return execute()
}

func unexpectedJobFailure(out database.JobTelemetryOutcome) bool {
	if out.State != "failed" && out.State != "unknown" {
		return false
	}
	// Expected authorization, configuration, source and budget rejections are
	// product outcomes, not unexpected execution failures.
	switch out.Code {
	case "connection_failed", "tls_failed", "timeout", "invalid_response", "execution_panic", "recording_failed", "lease_expired", "transport_error", "response_read", "response_too_large", "invalid_json", "provider_http", "provider_error", "model_mismatch", "invalid_output", "incomplete", "generation_mismatch", "provider_unavailable", "execution_unavailable", "research_failed", "partial_research_failed", "unknown_result", "provider_rejected":
		return true
	}
	return false
}
