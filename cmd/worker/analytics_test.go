package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/database"
)

type jobAnalyticsFixture struct {
	failureRecorder
	state string
	count int
}

func (f *jobAnalyticsFixture) TrackJob(_, _, state string, _ time.Duration) {
	f.state = state
	f.count++
}

func TestTaskAnalyticsRecordsPersistedFailureAndUnknownRead(t *testing.T) {
	for _, tc := range []struct {
		state   string
		readErr error
		want    string
	}{
		{"failed", nil, "failed"}, {"unknown", nil, "unknown"}, {"", errors.New("DB unavailable"), "unknown"}, {"", nil, "returned"}, {"succeeded", nil, "succeeded"},
	} {
		f := &jobAnalyticsFixture{}
		read := func(context.Context, string, string, time.Time) (database.JobTelemetryOutcome, error) {
			return database.JobTelemetryOutcome{State: tc.state}, tc.readErr
		}
		_, err := captureWorkerTask(withWorkerTelemetry(context.Background(), f), read, "worker_mail_discovery", "sync", "run", func(context.Context) (int, error) { return 1, nil })
		if err != nil || f.count != 1 || f.state != tc.want {
			t.Fatalf("state=%s read=%v: %+v %v", tc.state, tc.readErr, f, err)
		}
	}
}

func TestTaskAnalyticsPanicRemainsFailure(t *testing.T) {
	f := &jobAnalyticsFixture{}
	_, err := captureWorkerTask(withWorkerTelemetry(context.Background(), f), nil, "worker_mail_processing", "", "", func(context.Context) (int, error) { panic("private") })
	if err == nil || f.count != 1 || f.state != "failed" {
		t.Fatal(f, err)
	}
}
