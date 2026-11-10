//go:build integration

package database

import (
	"context"
	"github.com/enjoydarts/cycrio/internal/pushnotifications"
	"github.com/google/uuid"
	"strings"
	"testing"
)

func TestPushMigrationRoundTripVersionAndDestructiveDown(t *testing.T) {
	_, pool := testStore(t)
	ctx := context.Background()
	p := automationMigrationProvider(t, pool)
	down, e := p.DownTo(ctx, 48)
	assertMigrationRange(t, down, e, latestMigrationVersion, 49)
	up, e := p.Up(ctx)
	assertMigrationRange(t, up, e, 49, latestMigrationVersion)
	var version int64
	if e = pool.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); e != nil || version != latestMigrationVersion {
		t.Fatal(version, e)
	}
	f, r, _ := pushSetup(t)
	provider := automationMigrationProvider(t, r.pool)
	sender := pushSenderFunc(func(context.Context, []byte, pushnotifications.Message) (pushnotifications.SendResult, error) {
		return pushnotifications.SendResult{State: "accepted"}, nil
	})
	if _, e = r.RegisterPushDevice(ctx, f.owner, pushnotifications.RegistrationInput{SubscriptionID: uuid.NewString(), RequestID: uuid.NewString()}, sender); e != nil {
		t.Fatal(e)
	}
	if _, e = provider.DownTo(ctx, 48); e == nil || !strings.Contains(e.Error(), "must be preserved") {
		t.Fatal("データを失うdowngrade", e)
	}
	if e = r.pool.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); e != nil || version != 49 {
		t.Fatal(version, e)
	}
}
