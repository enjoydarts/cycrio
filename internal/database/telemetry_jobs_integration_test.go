//go:build integration

package database

import (
	"context"
	"errors"
	"testing"
	"time"

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
