//go:build integration

package database

import (
	"context"
	"testing"
)

func TestScheduledChangeSourceEventsAreImmutableWhileReserved(t *testing.T) {
	f, _, _, schedule, _ := scheduledFixture(t)
	if _, e := f.runtime.pool.Exec(context.Background(), `UPDATE mail_processing_items SET events='[]' WHERE owner_id=$1 AND id=$2`, f.owner, schedule.Preview.Source.ResultID); e == nil {
		t.Fatal("reserved original event was mutable")
	}
}
