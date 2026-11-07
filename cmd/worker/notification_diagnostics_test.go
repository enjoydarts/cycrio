package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/diagnostics"
	"github.com/enjoydarts/cycrio/internal/notificationruntime"
	"github.com/enjoydarts/cycrio/internal/notifications"
	"github.com/enjoydarts/cycrio/internal/telemetry"
)

type notificationDiagnosticTransport func(*http.Request) (*http.Response, error)

func (f notificationDiagnosticTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type notificationSDKRecorder struct {
	*telemetry.Reporter
	calls int
}

func (r *notificationSDKRecorder) ReportException(owner, operation, kind string, value any) {
	r.calls++
	r.Reporter.ReportException(owner, operation, kind, value)
}

type notificationDiagnosticRepository struct {
	delivery   notificationruntime.Delivery
	outcome    database.JobTelemetryOutcome
	suppressed bool
}

func (r *notificationDiagnosticRepository) DeliverNotification(ctx context.Context, _, _ string, sender notificationruntime.Sender) (notificationruntime.Result, error) {
	if r.suppressed {
		// A recorded business diagnostic alone must never become an exception.
		diagnostics.Record(ctx, errors.New("source changed before delivery"))
		return notificationruntime.Result{Status: notificationruntime.TaskRecorded}, nil
	}
	result := sender.Send(ctx, []byte("re_syntheticnotificationkey123456"), r.delivery)
	r.outcome.State, r.outcome.Code = "unknown", "unknown_result"
	if result.State == notificationruntime.Rejected || result.State == notificationruntime.Invalid {
		r.outcome.State, r.outcome.Code = "failed", "provider_rejected"
	}
	if result.State == notificationruntime.Accepted {
		r.outcome.State, r.outcome.Code = "accepted", ""
	}
	return notificationruntime.Result{Status: notificationruntime.TaskRecorded}, nil
}

func TestNotificationDiagnosticsReachSDKAfterRecordedNilResult(t *testing.T) {
	for _, test := range []struct {
		name         string
		status       int
		transportErr bool
		suppressed   bool
		state, code  string
		wantEvents   int
		detail       string
	}{
		{"rejected", 422, false, false, "", "", 1, "validation_error"},
		{"rejected_outcome_lookup_failure", 422, false, false, "", "", 2, "validation_error"},
		{"server_unknown", 503, false, false, "", "", 1, "validation_error"},
		{"transport_unknown", 0, true, false, "", "", 1, "connection refused"},
		{"accepted", 200, false, false, "", "", 0, ""},
		{"source_suppressed", 0, false, true, "suppressed", "source_changed", 0, ""},
		{"consent_disabled", 0, false, true, "suppressed", "email_disabled", 0, ""},
		{"cancelled", 0, false, true, "failed", "cancelled", 0, ""},
		{"obsolete_lease", 0, false, true, "", "", 0, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			wire := make(chan []byte, 4)
			previous := http.DefaultTransport
			http.DefaultTransport = notificationDiagnosticTransport(func(r *http.Request) (*http.Response, error) {
				body, _ := io.ReadAll(r.Body)
				wire <- body
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
			})
			defer func() { http.DefaultTransport = previous }()
			config := telemetry.Config{ProjectToken: "phc_syntheticnotification", Host: "https://us.i.posthog.com"}
			reporter := &notificationSDKRecorder{Reporter: telemetry.New(context.Background(), true, func(context.Context) (telemetry.Config, error) { return config, nil }, func(ctx context.Context, _ string, send func(context.Context) error) error { return send(ctx) }, "worker")}
			defer reporter.Close()
			deadline := time.Now().Add(3 * time.Second)
			for reporter.PublicConfig(context.Background()) != config {
				if time.Now().After(deadline) {
					t.Fatal("reporter config not ready")
				}
				time.Sleep(time.Millisecond)
			}
			id, due := "33333333-3333-4333-8333-333333333333", "2026-11-04"
			repo := &notificationDiagnosticRepository{suppressed: test.suppressed, outcome: database.JobTelemetryOutcome{OwnerID: id, State: test.state, Code: test.code}, delivery: notificationruntime.Delivery{ID: id, From: "sender@example.test", To: "recipient@example.test", Candidate: notifications.Candidate{Kind: notifications.Renewal, SourceKey: "renewal:" + id + ":" + due, ContractID: &id, Service: "PRIVATE_MERCHANT_IDENTIFIER", DueDate: &due, DateSource: "receipt_estimate", ScheduledAt: time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC), Email: true}}}
			sends := 0
			sender := notificationruntime.NewResendSender(&http.Client{Transport: notificationDiagnosticTransport(func(*http.Request) (*http.Response, error) {
				sends++
				if test.transportErr {
					return nil, errors.New("connection refused recipient@example.test re_syntheticnotificationkey123456")
				}
				body := `{"name":"validation_error","message":"rate limit exceeded PRIVATE_MERCHANT recipient@example.test"}`
				if test.status == 200 {
					body = `{"id":"22222222-2222-4222-8222-222222222222"}`
				}
				return &http.Response{StatusCode: test.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})})
			service := notificationruntime.NewService(repo, sender)
			read := func(context.Context, string, string, time.Time) (database.JobTelemetryOutcome, error) {
				if test.name == "rejected_outcome_lookup_failure" {
					return database.JobTelemetryOutcome{}, errors.New("notification outcome database unavailable")
				}
				return repo.outcome, nil
			}
			result, err := captureWorkerTask(withWorkerTelemetry(context.Background(), reporter), read, "worker_notification_delivery", "notification", id, func(ctx context.Context) (notificationruntime.Result, error) {
				return service.Execute(ctx, notificationruntime.Task{DeliveryID: id, LeaseID: id})
			})
			if err != nil || result.Status != notificationruntime.TaskRecorded || reporter.calls != test.wantEvents {
				t.Fatalf("recorded result/report count changed: %+v %v calls=%d", result, err, reporter.calls)
			}
			wantSends := 1
			if test.suppressed {
				wantSends = 0
			}
			if sends != wantSends {
				t.Fatalf("send/retry count changed: %d", sends)
			}
			if test.wantEvents > 0 {
				var text string
				for range test.wantEvents {
					select {
					case body := <-wire:
						text += string(body)
					case <-time.After(3 * time.Second):
						t.Fatal("notification diagnostic never reached SDK transport")
					}
				}
				if !strings.Contains(text, test.detail) || strings.Contains(text, "PRIVATE_MERCHANT") || strings.Contains(text, "recipient@example.test") || strings.Contains(text, "re_syntheticnotificationkey123456") {
					t.Fatalf("SDK details missing or private: %s", text)
				}
			}
		})
	}
}
