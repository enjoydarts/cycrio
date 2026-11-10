//go:build integration

package database

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/notifications"
	"github.com/google/uuid"
)

func TestNotificationReviewMigrationSeedsBaselineAndPreservesSettings(t *testing.T) {
	f, r, now := notificationReviewFixture(t)
	ctx := context.Background()
	first := notificationReviewResult(t, f, now.Add(-48*time.Hour), ai.Event{Service: "Old", EventType: "renewal", Amount: autoPtr("10.00")})
	provider := automationMigrationProvider(t, r.pool)
	down, err := provider.DownTo(ctx, 50)
	if err != nil || len(down) != 1 || down[0].Direction != "down" || down[0].Source.Version != 51 {
		t.Fatal(down, err)
	}
	p := notifications.DefaultPreferences()
	p.Version = 2
	p.EmailOptIn = true
	p.Kinds[notifications.Renewal] = notifications.Channels{Email: true}
	delete(p.Kinds, notifications.PendingReview)
	raw, _ := json.Marshal(p)
	if _, err = r.pool.Exec(ctx, `INSERT INTO notification_preferences(owner_id,version,settings) VALUES($1,2,$2)`, f.owner, raw); err != nil {
		t.Fatal(err)
	}
	if _, err = r.pool.Exec(ctx, `INSERT INTO notification_preferences_audit(id,owner_id,actor_id,version,snapshot) VALUES($1,$2,$2,2,$3)`, uuid.NewString(), f.owner, raw); err != nil {
		t.Fatal(err)
	}
	var auditBefore string
	if err = r.pool.QueryRow(ctx, `SELECT snapshot::text FROM notification_preferences_audit WHERE owner_id=$1`, f.owner).Scan(&auditBefore); err != nil {
		t.Fatal(err)
	}
	up, err := provider.Up(ctx)
	assertMigrationRange(t, up, err, 51, 51)
	var version int64
	var auditAfter string
	if err = r.pool.QueryRow(ctx, `SELECT (SELECT max(version_id) FROM goose_db_version WHERE is_applied),snapshot::text FROM notification_preferences_audit WHERE owner_id=$1`, f.owner).Scan(&version, &auditAfter); err != nil || version != 51 || auditBefore != auditAfter {
		t.Fatal(version, auditBefore, auditAfter, err)
	}
	settings, err := r.NotificationSettings(ctx, f.owner)
	if err != nil || settings.Preferences.Version != 2 || !settings.Preferences.Kinds[notifications.Renewal].Email || settings.Preferences.Kinds[notifications.PendingReview] != (notifications.Channels{InApp: true}) {
		t.Fatal(settings, err)
	}
	if n, err := r.GenerateNotifications(ctx, f.owner); err != nil || n != 0 {
		t.Fatal("既存候補の一斉配信をしない", n, err)
	}
	cloneNotificationReviewRun(t, f, first, *now, true, 1, ai.Event{Service: "Old", EventType: "renewal", Amount: autoPtr("10")})
	if n, err := r.GenerateNotifications(ctx, f.owner); err != nil || n != 0 {
		t.Fatal("基準集合の再解析も配信しない", n, err)
	}
	cloneNotificationReviewRun(t, f, first, *now, false, 1, ai.Event{Service: "Old", EventType: "renewal", Amount: autoPtr("10")})
	if n, err := r.GenerateNotifications(ctx, f.owner); err != nil || n != 1 {
		t.Fatal("移行後の新メールは通知", n, err)
	}
	if _, err = provider.DownTo(ctx, 50); err == nil {
		t.Fatal("通知履歴を消す移行を拒否")
	}
}

func TestNotificationReviewErasureDeletesSourceLedger(t *testing.T) {
	f, r, now := notificationReviewFixture(t)
	ctx := context.Background()
	notificationReviewResult(t, f, *now, ai.Event{Service: "Private", EventType: "renewal"})
	if _, err := r.GenerateNotifications(ctx, f.owner); err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx, `UPDATE users SET state='deleting' WHERE id=$1`, f.owner); err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx, `INSERT INTO user_erasure_requests(client_request_id,owner_id,state,source) VALUES(gen_random_uuid(),$1,'pending','self')`, f.owner); err != nil {
		t.Fatal(err)
	}
	if n, err := NewErasureRepository(r.pool, f.keys).PurgeDue(ctx, 10); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	for _, table := range []string{"notification_review_pending", "notification_review_sources", "notification_review_seen", "notification_review_runs", "notification_review_state", "notification_events"} {
		var count int
		if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE owner_id=$1`, f.owner).Scan(&count); err != nil || count != 0 {
			t.Fatal(table, count, err)
		}
	}
}
