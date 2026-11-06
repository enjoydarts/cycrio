package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
)

type recordedFailure struct{ owner, operation, kind string }
type failureRecorder struct{ events []recordedFailure }

func (r *failureRecorder) Report(owner, operation, kind string) {
	r.events = append(r.events, recordedFailure{owner, operation, kind})
}

func TestCapturedTaskReportsPersistedFailureOnce(t *testing.T) {
	r := &failureRecorder{}
	ctx := withWorkerTelemetry(context.Background(), r)
	read := func(context.Context, string, string, time.Time) (database.JobTelemetryOutcome, error) {
		return database.JobTelemetryOutcome{OwnerID: "owner", State: "unknown", Code: "execution_panic"}, nil
	}
	_, err := captureWorkerTask(ctx, read, "worker_mail_processing", "processing", "run", func() (int, error) { return 0, errors.New("PRIVATE") })
	if err == nil || len(r.events) != 1 || r.events[0].owner != "owner" || r.events[0].kind != "task_panic" {
		t.Fatalf("unexpected capture: %v", r.events)
	}
}

func TestCapturedTaskSanitizesPanicAndOutcomeFailure(t *testing.T) {
	r := &failureRecorder{}
	ctx := withWorkerTelemetry(context.Background(), r)
	read := func(context.Context, string, string, time.Time) (database.JobTelemetryOutcome, error) {
		return database.JobTelemetryOutcome{}, errors.New("PRIVATE")
	}
	_, err := captureWorkerTask(ctx, read, "worker_mail_probe", "probe", "run", func() (int, error) { panic("PRIVATE") })
	if err == nil || err.Error() != "worker task unavailable" || len(r.events) != 2 || r.events[0].owner != "cycrio-system" || r.events[0].kind != "outcome_unavailable" || r.events[1].kind != "task_panic" {
		t.Fatalf("unexpected capture: %v %v", r.events, err)
	}
}

func TestCapturedTaskIgnoresExpectedRejectionAndSuccess(t *testing.T) {
	for _, code := range []string{"", "cancelled", "authentication_failed", "budget_exceeded", "configuration_unavailable"} {
		r := &failureRecorder{}
		ctx := withWorkerTelemetry(context.Background(), r)
		read := func(context.Context, string, string, time.Time) (database.JobTelemetryOutcome, error) {
			return database.JobTelemetryOutcome{OwnerID: "owner", State: "failed", Code: code}, nil
		}
		_, _ = captureWorkerTask(ctx, read, "worker_mail_probe", "probe", "run", func() (int, error) { return 1, nil })
		if len(r.events) != 0 {
			t.Fatalf("reported expected result %q: %v", code, r.events)
		}
	}
}

func TestPollerReportsClaimAndSubmissionFailures(t *testing.T) {
	r := &failureRecorder{}
	ctx, cancel := context.WithCancel(withWorkerTelemetry(context.Background(), r))
	dispatchSubscriptionAutomation(withWorkerOperation(ctx, "worker_billing_record"), func(context.Context, int) (int, error) { cancel(); return 0, errors.New("PRIVATE") }, time.Hour)
	if len(r.events) != 1 || r.events[0].kind != "poller_failed" {
		t.Fatalf("unexpected capture: %v", r.events)
	}
}

func TestCapturedTaskReportsFailureWithoutPrivateError(t *testing.T) {
	r := &failureRecorder{}
	ctx := withWorkerTelemetry(context.Background(), r)
	_, err := captureWorkerTask(ctx, nil, "worker_catalog_sync", "", "", func() (int, error) {
		return 0, errors.New("PRIVATE provider response")
	})
	if err == nil || len(r.events) != 1 || r.events[0] != (recordedFailure{"cycrio-system", "worker_catalog_sync", "task_failed"}) {
		t.Fatalf("unexpected capture: %v", r.events)
	}
}

func TestMailProbeDispatcherReportsClaimAndSubmissionSeparately(t *testing.T) {
	for _, claimFails := range []bool{false, true} {
		r := &failureRecorder{}
		ctx, cancel := context.WithCancel(withWorkerTelemetry(context.Background(), r))
		dispatchMailProbes(ctx, func(context.Context) ([]mailaccounts.Lease, error) {
			if claimFails {
				cancel()
				return nil, errors.New("PRIVATE database failure")
			}
			return []mailaccounts.Lease{{ProbeID: "private-identifier"}}, nil
		}, func(context.Context, mailaccounts.Lease) error {
			cancel()
			return errors.New("PRIVATE submission failure")
		}, time.Hour)
		kind := "submit_failed"
		if claimFails {
			kind = "claim_failed"
		}
		if len(r.events) != 1 || r.events[0] != (recordedFailure{"cycrio-system", "worker_mail_probe", kind}) {
			t.Fatalf("unexpected capture: %v", r.events)
		}
	}
}

func TestPollerPanicReportsOnceAndStops(t *testing.T) {
	r := &failureRecorder{}
	ctx := withWorkerOperation(withWorkerTelemetry(context.Background(), r), "worker_billing_record")
	calls := 0
	dispatchSubscriptionAutomation(ctx, func(context.Context, int) (int, error) { calls++; panic("PRIVATE") }, time.Hour)
	if calls != 1 || len(r.events) != 1 || r.events[0].kind != "poller_panic" {
		t.Fatalf("unexpected panic capture: %d %v", calls, r.events)
	}
}

type panickingFailureReporter struct{}

func (panickingFailureReporter) Report(string, string, string) { panic("PRIVATE reporter failure") }

func TestPollerPanicCancelsWorkerEvenWhenReportingPanics(t *testing.T) {
	for _, reporter := range []workerFailureReporter{&failureRecorder{}, panickingFailureReporter{}} {
		ctx, cancel := withWorkerShutdown(context.Background())
		defer cancel(nil)
		ctx = withWorkerOperation(withWorkerTelemetry(ctx, reporter), "worker_billing_record")
		calls := 0
		dispatchSubscriptionAutomation(ctx, func(context.Context, int) (int, error) {
			calls++
			panic("PRIVATE poller failure")
		}, time.Hour)
		if calls != 1 || ctx.Err() == nil || !errors.Is(context.Cause(ctx), errWorkerPollerFailed) {
			t.Fatalf("poller panic did not cancel worker: calls=%d cause=%v", calls, context.Cause(ctx))
		}
		if err := workerShutdownError(ctx); err != errWorkerPollerFailed || err.Error() != "worker poller failed" {
			t.Fatalf("worker must exit with a fixed failure: %v", err)
		}
	}
}

func TestReporterPanicDoesNotCancelHealthyWorker(t *testing.T) {
	ctx, cancel := withWorkerShutdown(context.Background())
	defer cancel(nil)
	ctx = withWorkerTelemetry(ctx, panickingFailureReporter{})
	value, err := captureWorkerTask(ctx, nil, "worker_catalog_sync", "", "", func() (int, error) {
		return 7, errors.New("task failure")
	})
	if value != 7 || err == nil || err.Error() != "task failure" || ctx.Err() != nil {
		t.Fatalf("reporter panic changed task/lifecycle: value=%d err=%v cause=%v", value, err, context.Cause(ctx))
	}
}

func TestWorkerReadinessStopsImmediatelyAfterPollerPanic(t *testing.T) {
	ctx, cancel := withWorkerShutdown(context.Background())
	defer cancel(nil)
	checks := 0
	ready := workerReadiness(ctx, func(context.Context) error { checks++; return nil })
	if err := ready(context.Background()); err != nil || checks != 1 {
		t.Fatalf("healthy readiness failed: checks=%d err=%v", checks, err)
	}
	dispatchSubscriptionAutomation(ctx, func(context.Context, int) (int, error) { panic("PRIVATE") }, time.Hour)
	if err := ready(context.Background()); err != errWorkerPollerFailed || checks != 1 {
		t.Fatalf("dead poller remained ready: checks=%d err=%v", checks, err)
	}
}

func TestSignalShutdownIsNotWorkerFailure(t *testing.T) {
	signalCtx, signalStop := context.WithCancel(context.Background())
	ctx, cancel := withWorkerShutdown(signalCtx)
	defer cancel(nil)
	signalStop()
	if ctx.Err() == nil || workerShutdownError(ctx) != nil {
		t.Fatalf("normal cancellation became worker failure: %v", context.Cause(ctx))
	}
	ready := workerReadiness(ctx, func(context.Context) error { t.Fatal("dependency checked during shutdown"); return nil })
	if ready(context.Background()) == nil {
		t.Fatal("shutting down worker stayed ready")
	}
}

func TestWorkerReadinessRejectsFailureDuringDependencyCheck(t *testing.T) {
	ctx, cancel := withWorkerShutdown(context.Background())
	defer cancel(nil)
	ready := workerReadiness(ctx, func(context.Context) error {
		cancel(errWorkerPollerFailed)
		return nil
	})
	if err := ready(context.Background()); err != errWorkerPollerFailed {
		t.Fatalf("worker became ready after poller failure: %v", err)
	}
}

type detailedFailureRecorder struct {
	failureRecorder
	values []any
}

func (r *detailedFailureRecorder) ReportException(owner, operation, kind string, value any) {
	r.Report(owner, operation, kind)
	r.values = append(r.values, value)
}

func TestWorkerTelemetryPreservesTaskErrorPanicAndStoredReason(t *testing.T) {
	for _, mode := range []string{"error", "panic", "stored"} {
		t.Run(mode, func(t *testing.T) {
			r := &detailedFailureRecorder{}
			ctx := withWorkerTelemetry(context.Background(), r)
			original := errors.New("database transaction serialization failure")
			var read jobOutcomeReader
			if mode == "stored" {
				read = func(context.Context, string, string, time.Time) (database.JobTelemetryOutcome, error) {
					return database.JobTelemetryOutcome{State: "failed", Code: "invalid_json"}, nil
				}
			}
			_, _ = captureWorkerTask(ctx, read, "worker_mail_processing", "processing", "run", func() (int, error) {
				if mode == "panic" {
					panic(original)
				}
				if mode == "error" {
					return 0, original
				}
				return 0, nil
			})
			if len(r.values) != 1 {
				t.Fatalf("detail missing: %+v", r.events)
			}
			if mode == "stored" {
				if r.values[0] != "invalid_json" {
					t.Fatalf("stored reason lost: %v", r.values)
				}
			} else if r.values[0] != original {
				t.Fatalf("original cause lost: %v", r.values)
			}
		})
	}
}

func TestWorkerTelemetryPreservesPollerErrorAndPanic(t *testing.T) {
	for _, panics := range []bool{false, true} {
		r := &detailedFailureRecorder{}
		ctx, cancel := context.WithCancel(withWorkerTelemetry(context.Background(), r))
		original := errors.New("scheduler database unavailable")
		dispatchSubscriptionAutomation(ctx, func(context.Context, int) (int, error) {
			cancel()
			if panics {
				panic(original)
			}
			return 0, original
		}, time.Hour)
		if len(r.values) != 1 || r.values[0] != original {
			t.Fatalf("poller cause lost: %+v %v", r.events, r.values)
		}
	}
}

func TestPrivateWorkerCauseStaysOutOfFormattingAndFrameworkReturn(t *testing.T) {
	cause := errors.New("database connection refused private@example.com")
	err := hideWorkerCause("mail discovery unavailable", cause)
	if errors.Unwrap(err) != cause {
		t.Fatal("telemetry cause lost")
	}
	for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
		if got := fmt.Sprintf(format, err); strings.Contains(got, "private") || strings.Contains(got, "database connection refused") {
			t.Fatalf("cause leaked via %s: %s", format, got)
		}
	}
	encoded, _ := json.Marshal(err)
	if strings.Contains(string(encoded), "private") {
		t.Fatal("cause leaked in JSON")
	}
	r := &detailedFailureRecorder{}
	_, returned := captureWorkerTask(withWorkerTelemetry(context.Background(), r), nil, "worker_mail_discovery", "", "", func() (int, error) { return 0, err })
	if returned.Error() != "mail discovery unavailable" || errors.Unwrap(returned) != nil {
		t.Fatal("framework received private cause")
	}
	if len(r.values) != 1 || errors.Unwrap(r.values[0].(error)) != cause {
		t.Fatal("telemetry did not receive cause")
	}
}

func TestWorkerTelemetryRetainsOriginalFailureWhenOutcomeReadFails(t *testing.T) {
	r := &detailedFailureRecorder{}
	original := errors.New("upstream TLS handshake failed")
	read := func(context.Context, string, string, time.Time) (database.JobTelemetryOutcome, error) {
		return database.JobTelemetryOutcome{}, errors.New("outcome database connection refused")
	}
	_, _ = captureWorkerTask(withWorkerTelemetry(context.Background(), r), read, "worker_mail_processing", "processing", "run", func() (int, error) { return 0, original })
	if len(r.values) != 2 || r.values[1] != original || r.events[1].kind != "task_failed" {
		t.Fatalf("original failure was replaced by outcome read: %+v %v", r.events, r.values)
	}
}
