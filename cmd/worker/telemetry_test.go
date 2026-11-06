package main

import (
	"context"
	"errors"
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
	if err == nil || err.Error() != "worker task unavailable" || len(r.events) != 1 || r.events[0].owner != "cycrio-system" || r.events[0].kind != "outcome_unavailable" {
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
