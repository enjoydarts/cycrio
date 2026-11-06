package database

import (
	"context"
	"testing"
	"time"
)

func TestJobTelemetryRejectsUntrustedKindsAndIdentifiers(t *testing.T) {
	// A nil pool verifies rejected input cannot perform any query, including a
	// caller-supplied table name or non-UUID identifier.
	r := NewJobTelemetryRepository(nil)
	for _, input := range []struct{ kind, id string }{{"mail_account_credentials", "788bd216-e440-46fd-aef9-20c381957aa0"}, {"probe", "private@example.test"}, {"sync", ""}, {"processing", "not-a-uuid"}} {
		out, err := r.Outcome(context.Background(), input.kind, input.id, time.Now())
		if err != nil || out != (JobTelemetryOutcome{}) {
			t.Fatalf("rejected input produced outcome: %v %v", out, err)
		}
	}
}
