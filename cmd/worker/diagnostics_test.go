package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/diagnostics"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
)

func TestCapturedTaskPropagatesOriginalDiagnosticAndKeepsPublicReturn(t *testing.T) {
	r := &detailedFailureRecorder{}
	original := errors.New("provider response failed: detailed cause")
	read := func(context.Context, string, string, time.Time) (database.JobTelemetryOutcome, error) {
		return database.JobTelemetryOutcome{OwnerID: "owner", State: "failed", Code: "provider_http"}, nil
	}
	_, err := captureWorkerTask(withWorkerTelemetry(context.Background(), r), read, "worker_mail_processing", "processing", "run", func(ctx context.Context) (int, error) {
		diagnostics.Record(ctx, original)
		return 1, nil
	})
	if err != nil || len(r.values) != 1 {
		t.Fatalf("return/report changed: %v %+v", err, r.events)
	}
	got, ok := r.values[0].(error)
	if !ok || !errors.Is(got, original) || r.events[0].owner != "owner" {
		t.Fatalf("cause/owner lost: %+v %v", r.events, r.values)
	}
}

func TestCapturedRecordedBusinessOutcomesDoNotBecomeExceptions(t *testing.T) {
	for _, code := range []string{"authentication_failed", "source_missing", "unsupported_content", "cancelled", "refusal"} {
		t.Run(code, func(t *testing.T) {
			r := &detailedFailureRecorder{}
			read := func(context.Context, string, string, time.Time) (database.JobTelemetryOutcome, error) {
				return database.JobTelemetryOutcome{OwnerID: "owner", State: "failed", Code: code}, nil
			}
			_, err := captureWorkerTask(withWorkerTelemetry(context.Background(), r), read, "worker_mail_processing", "processing", "run", func(ctx context.Context) (int, error) {
				if code == "refusal" {
					diagnostics.Record(ctx, &ai.Error{Code: code})
				} else {
					diagnostics.Record(ctx, mailaccounts.AdapterError{Code: code})
				}
				return 1, nil
			})
			if err != nil || len(r.values) != 0 {
				t.Fatalf("business outcome reported: %v %v", err, r.values)
			}
		})
	}
}

func TestCapturedTaskPanicWinsOverRecordedFailureAndScopesStayIsolated(t *testing.T) {
	r := &detailedFailureRecorder{}
	ctx := withWorkerTelemetry(context.Background(), r)
	original := errors.New("earlier failure")
	panicValue := errors.New("original panic")
	_, err := captureWorkerTask(ctx, nil, "worker_mail_processing", "", "", func(ctx context.Context) (int, error) {
		diagnostics.Record(ctx, original)
		panic(panicValue)
	})
	if err == nil || err.Error() != "worker task unavailable" || len(r.values) != 1 || r.values[0] != panicValue {
		t.Fatalf("panic precedence changed: %v %v", err, r.values)
	}
	_, err = captureWorkerTask(ctx, nil, "worker_mail_processing", "", "", func(context.Context) (int, error) { return 1, nil })
	if err != nil || len(r.values) != 1 {
		t.Fatalf("previous task failure leaked: %v %v", err, r.values)
	}
}

func TestCapturedUnexpectedDiagnosticSurvivesExpectedStateAndUnavailableOutcome(t *testing.T) {
	for _, readFails := range []bool{false, true} {
		r := &detailedFailureRecorder{}
		original := errors.New("credential decryption failed")
		read := func(context.Context, string, string, time.Time) (database.JobTelemetryOutcome, error) {
			if readFails {
				return database.JobTelemetryOutcome{}, errors.New("outcome database unavailable")
			}
			return database.JobTelemetryOutcome{OwnerID: "owner", State: "failed", Code: "credentials_unavailable"}, nil
		}
		_, err := captureWorkerTask(withWorkerTelemetry(context.Background(), r), read, "worker_mail_processing", "processing", "run", func(ctx context.Context) (int, error) { diagnostics.RecordUnexpected(ctx, original); return 1, nil })
		want := 1
		if readFails {
			want = 2
		}
		if err != nil || len(r.values) != want || !errors.Is(r.values[len(r.values)-1].(error), original) {
			t.Fatalf("genuine diagnostic lost: %v %+v %v", err, r.events, r.values)
		}
	}
}
