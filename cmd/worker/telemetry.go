package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/enjoydarts/cycrio/internal/database"
)

type workerFailureReporter interface{ Report(string, string, string) }
type workerTelemetryKey struct{}
type workerOperationKey struct{}

// privateWorkerCause is inspected only at the telemetry boundary. Formatting
// and the error returned to Hatchet keep the existing public message.
type privateWorkerCause struct {
	message string
	cause   error
}

func (e *privateWorkerCause) Error() string                  { return e.message }
func (e *privateWorkerCause) Unwrap() error                  { return e.cause }
func (e *privateWorkerCause) Format(state fmt.State, _ rune) { _, _ = io.WriteString(state, e.message) }
func hideWorkerCause(message string, cause error) error {
	return &privateWorkerCause{message: message, cause: cause}
}

func withWorkerTelemetry(ctx context.Context, r workerFailureReporter) context.Context {
	return context.WithValue(ctx, workerTelemetryKey{}, r)
}
func withWorkerOperation(ctx context.Context, operation string) context.Context {
	return context.WithValue(ctx, workerOperationKey{}, operation)
}
func reportWorkerFailure(ctx context.Context, owner, operation, kind string, values ...any) {
	defer func() { _ = recover() }()
	if operation == "" {
		operation, _ = ctx.Value(workerOperationKey{}).(string)
	}
	if owner == "" {
		owner = "cycrio-system"
	}
	if r, ok := ctx.Value(workerTelemetryKey{}).(workerFailureReporter); ok {
		if detailed, ok := r.(interface {
			ReportException(string, string, string, any)
		}); ok && len(values) > 0 && values[0] != nil {
			detailed.ReportException(owner, operation, kind, values[0])
		} else {
			r.Report(owner, operation, kind)
		}
	}
}
func capturePollerPanic(ctx context.Context) {
	if value := recover(); value != nil {
		reportWorkerFailure(ctx, "", "", "poller_panic", value)
		if cancel, ok := ctx.Value(workerShutdownKey{}).(context.CancelCauseFunc); ok {
			cancel(errWorkerPollerFailed)
		}
	}
}

type jobOutcomeReader func(context.Context, string, string, time.Time) (database.JobTelemetryOutcome, error)

func captureWorkerTask[T any](ctx context.Context, read jobOutcomeReader, operation, jobKind, id string, execute func() (T, error)) (out T, err error) {
	started := time.Now()
	defer func() {
		panicValue := recover()
		panicked := panicValue != nil
		var failure any = err
		if private, ok := err.(*privateWorkerCause); ok {
			err = errors.New(private.message)
		}
		if panicked {
			failure = panicValue
			err = errors.New("worker task unavailable")
		}
		outcome := database.JobTelemetryOutcome{}
		if read != nil && jobKind != "" {
			bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
			defer cancel()
			var readErr error
			outcome, readErr = read(bounded, jobKind, id, started)
			if readErr != nil {
				reportWorkerFailure(ctx, "", operation, "outcome_unavailable", readErr)
				if panicked {
					reportWorkerFailure(ctx, "", operation, "task_panic", failure)
				} else if failure != nil {
					reportWorkerFailure(ctx, "", operation, "task_failed", failure)
				}
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
			if failure == nil {
				failure = outcome.Code
			}
			reportWorkerFailure(ctx, outcome.OwnerID, operation, kind, failure)
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
