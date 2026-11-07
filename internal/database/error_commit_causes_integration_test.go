//go:build integration

package database

import (
	"context"
	"errors"
	"testing"

	"github.com/enjoydarts/cycrio/internal/diagnostics"
	"github.com/enjoydarts/cycrio/internal/mailsync"
	"github.com/enjoydarts/cycrio/internal/notificationruntime"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestMailSyncCommitFailurePreservesCause(t *testing.T) {
	_, pool, _, repo, owner, _, account := syncFixture(t)
	ctx := context.Background()
	if _, err := repo.Request(ctx, owner, account, syncInput()); err != nil {
		t.Fatal(err)
	}
	jobs, err := repo.Claim(ctx)
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	// Fail only at commit, after the running-state UPDATE itself succeeds.
	if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_sync_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic deferred sync failure' USING ERRCODE='XX001'; END $$; CREATE CONSTRAINT TRIGGER reject_sync_commit AFTER UPDATE ON mail_sync_runs DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_sync_commit()`); err != nil {
		t.Fatal(err)
	}
	_, err = repo.Start(ctx, jobs[0])
	var pg *pgconn.PgError
	if !errors.Is(err, mailsync.ErrUnavailable) || !errors.As(err, &pg) || pg.Code != "XX001" {
		t.Fatalf("commit failure diagnostic lost: %T", err)
	}
	var state string
	if err := pool.QueryRow(ctx, `SELECT state FROM mail_sync_runs WHERE id=$1`, jobs[0].RunID).Scan(&state); err != nil || state != "leased" {
		t.Fatal("failed commit changed persisted state", state, err)
	}
}

func TestNotificationPostSendDatabaseFailureIsRecordedWithoutReplay(t *testing.T) {
	for _, stage := range []string{"update", "commit"} {
		t.Run(stage, func(t *testing.T) {
			r, _, _, _ := notificationDeliverySetup(t)
			task := claimNotification(t, r)
			ctx, scope := diagnostics.WithScope(context.Background())
			trigger := `CREATE TRIGGER reject_delivery_result BEFORE UPDATE ON notification_deliveries FOR EACH ROW WHEN (NEW.state='accepted') EXECUTE FUNCTION reject_delivery_result()`
			if stage == "commit" {
				trigger = `CREATE CONSTRAINT TRIGGER reject_delivery_result AFTER UPDATE ON notification_deliveries DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN (NEW.state='accepted') EXECUTE FUNCTION reject_delivery_result()`
			}
			if _, err := r.pool.Exec(ctx, `CREATE FUNCTION reject_delivery_result() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic post-send persistence failure' USING ERRCODE='XX001'; END $$; `+trigger); err != nil {
				t.Fatal(err)
			}
			calls := 0
			sender := notificationSenderFunc(func(context.Context, []byte, notificationruntime.Delivery) notificationruntime.SendResult {
				calls++
				return notificationruntime.SendResult{State: notificationruntime.Accepted, ProviderID: uuid.NewString()}
			})
			result, err := r.DeliverNotification(ctx, task.DeliveryID, task.LeaseID, sender)
			if err != nil || result.Status != notificationruntime.TaskRecorded || calls != 1 {
				t.Fatal("delivery behavior changed", result, err, calls)
			}
			var pg *pgconn.PgError
			if !errors.As(scope.Error(), &pg) || pg.Code != "XX001" || !diagnostics.IsUnexpected(scope.Error()) {
				t.Fatal("post-send failure was not recorded")
			}
			var state string
			if err := r.pool.QueryRow(ctx, `SELECT state FROM notification_deliveries WHERE id=$1`, task.DeliveryID).Scan(&state); err != nil || state != "unknown" {
				t.Fatal("failed result write changed state", state, err)
			}
			if result, err := r.DeliverNotification(ctx, task.DeliveryID, task.LeaseID, sender); err != nil || result.Status != notificationruntime.TaskIgnored || calls != 1 {
				t.Fatal("delivery replayed", result, err, calls)
			}
		})
	}
}
