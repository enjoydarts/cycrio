//go:build integration

package database

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"

	"github.com/google/uuid"
)

func TestJobTelemetryQueriesMatchCurrentSchema(t *testing.T) {
	_, pool := testStore(t)
	r := NewJobTelemetryRepository(pool)
	for _, kind := range []string{"probe", "sync", "processing", "ai_test", "notification", "research"} {
		t.Run(kind, func(t *testing.T) {
			out, err := r.Outcome(context.Background(), kind, uuid.NewString(), time.Now())
			if err != nil || out != (JobTelemetryOutcome{}) {
				t.Fatal(out, err)
			}
		})
	}
}

func TestJobTelemetryProcessingReadsSelectedAttemptHTTPFailure(t *testing.T) {
	f := processingSetup(t)
	f.add(t, 1)
	ctx := context.Background()
	run, err := f.repo.Request(ctx, f.owner, f.account, f.in)
	if err != nil {
		t.Fatal(err)
	}
	lease, _ := f.start(t)
	since := time.Now()
	call, err := f.repo.BeginCall(ctx, lease, "classification")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.repo.FinishCall(ctx, call.ID, airuntime.CallUpdate{Outcome: "provider_http", HTTPStatus: 422, Cost: ai.Cost{Status: "unknown"}}); err != nil {
		t.Fatal(err)
	}
	if err = f.repo.Finish(ctx, lease, mailprocessing.Completion{State: "failed", ErrorCode: "provider_http"}); err != nil {
		t.Fatal(err)
	}
	out, err := NewJobTelemetryRepository(f.runtime.pool).Outcome(ctx, "processing", run.ID, since)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	var metadata struct {
		HTTPStatus      int
		Stage, Provider string
	}
	if err = json.Unmarshal(raw, &metadata); err != nil {
		t.Fatal(err)
	}
	if out.Code != "provider_http" || metadata.HTTPStatus != 422 || metadata.Stage != "classification" || metadata.Provider != "liquid" {
		t.Fatalf("selected failure metadata lost: %s", raw)
	}
	old, err := NewJobTelemetryRepository(f.runtime.pool).Outcome(ctx, "processing", run.ID, time.Now().Add(time.Hour))
	if err != nil || old != (JobTelemetryOutcome{OwnerID: f.owner}) {
		t.Fatalf("old AI diagnostics replayed: %+v %v", old, err)
	}
}

func TestJobTelemetrySeesPersistedNilFailuresWithoutPrivateData(t *testing.T) {
	_, pool, service, checker, owner, _ := mailFixture(t)
	ctx := context.Background()
	account := uuid.NewString()
	if _, err := service.Save(ctx, owner, account, mailSave(0)); err != nil {
		t.Fatal(err)
	}
	probe := mailProbe(t, service, owner, account, 1)
	since := time.Now()
	checker.err = errors.New("PRIVATE mail body secret@example.test")
	mailRun(t, service) // Failure is persisted and this method returns nil.
	reader := NewJobTelemetryRepository(pool)
	out, err := reader.Outcome(ctx, "probe", probe.ID, since)
	if err != nil || out.OwnerID != owner || out.State != "failed" || out.Code == "" {
		t.Fatal(out, err)
	}
	old, err := reader.Outcome(ctx, "probe", probe.ID, time.Now().Add(time.Hour))
	if err != nil || old.OwnerID != owner || old.State != "" || old.Code != "" {
		t.Fatal("old result replayed", old, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE users SET state='disabled' WHERE id=$1`, owner); err != nil {
		t.Fatal(err)
	}
	disabled, err := reader.Outcome(ctx, "probe", probe.ID, since)
	if err != nil || disabled != (JobTelemetryOutcome{}) {
		t.Fatal("inactive owner exposed", disabled, err)
	}
}
