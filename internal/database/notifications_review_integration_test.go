//go:build integration

package database

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/autoregistration"
	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/notifications"
	"github.com/enjoydarts/cycrio/internal/pushnotifications"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/google/uuid"
)

func notificationReviewFixture(t *testing.T) (processingFixture, *NotificationRepository, *time.Time) {
	t.Helper()
	f := processingSetup(t)
	now := time.Date(2030, 1, 2, 12, 0, 0, 0, time.UTC)
	if _, err := f.runtime.pool.Exec(context.Background(), `UPDATE users SET created_at=$2 WHERE id=$1`, f.owner, now.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	return f, NewNotificationDeliveryRepository(f.runtime.pool, f.keys, "https://clerk.test.example", func() time.Time { return now }), &now
}

func enableNotificationReviewPush(t *testing.T, f processingFixture, r *NotificationRepository, now *time.Time) *NotificationRepository {
	t.Helper()
	ctx := context.Background()
	if _, err := credentials.NewService(NewCredentialRepository(r.pool), f.keys).Put(ctx, f.admin, "onesignal", 0, `{"app_id":"`+uuid.NewString()+`","api_key":"synthetic-review-key"}`); err != nil {
		t.Fatal(err)
	}
	r = r.WithPush(nil, true)
	devices := NewPushNotificationRepository(r.pool, f.keys, true, func() time.Time { return *now })
	var message pushnotifications.Message
	reg, err := devices.RegisterPushDevice(ctx, f.owner, pushnotifications.RegistrationInput{SubscriptionID: uuid.NewString(), RequestID: uuid.NewString()}, pushSenderFunc(func(_ context.Context, _ []byte, m pushnotifications.Message) (pushnotifications.SendResult, error) {
		message = m
		return pushnotifications.SendResult{State: "accepted"}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = devices.ConfirmPushDevice(ctx, f.owner, pushnotifications.ConfirmationInput{ChallengeID: reg.ChallengeID, Secret: message.ChallengeSecret}); err != nil {
		t.Fatal(err)
	}
	settings, err := r.NotificationSettings(ctx, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	p := settings.Preferences
	p.PushOptIn = true
	p.Kinds[notifications.PendingReview] = notifications.Channels{InApp: true, Push: true}
	if _, err = r.SaveNotificationSettings(ctx, f.owner, notifications.SettingsInput{Preferences: p, ExpectedVersion: p.Version, Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestNotificationReviewPushBulkAndChannelSwitch(t *testing.T) {
	f, r, now := notificationReviewFixture(t)
	ctx := context.Background()
	first := notificationReviewResult(t, f, *now, ai.Event{Service: "Private service", EventType: "renewal"})
	if n, err := r.GenerateNotifications(ctx, f.owner); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	*now = now.Add(time.Minute)
	r = enableNotificationReviewPush(t, f, r, now)
	if n, err := r.GenerateNotifications(ctx, f.owner); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if tasks, err := r.ClaimNotifications(ctx); err != nil || len(tasks) != 0 {
		t.Fatal("後からPushを有効化しても既存集約を配信しない", tasks, err)
	}
	*now = now.Add(time.Minute)
	cloneNotificationReviewRun(t, f, first, *now, false, 60, ai.Event{Service: "Private service", EventType: "renewal"}, ai.Event{Service: "Private service", EventType: "renewal"})
	if n, err := r.GenerateNotifications(ctx, f.owner); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	task := claimNotification(t, r)
	calls := 0
	r = r.WithPush(pushSenderFunc(func(_ context.Context, key []byte, m pushnotifications.Message) (pushnotifications.SendResult, error) {
		calls++
		if string(key) != "synthetic-review-key" || m.Kind != "pending_review" || m.URL != "https://cycrio.com/analysis?tab=attention" || len(m.SubscriptionIDs) != 1 {
			t.Fatal(m)
		}
		return pushnotifications.SendResult{State: "accepted", ProviderID: uuid.NewString()}, nil
	}), true)
	if _, err := r.DeliverNotification(ctx, task.DeliveryID, task.LeaseID, nil); err != nil || calls != 1 {
		t.Fatal(calls, err)
	}
	if _, err := r.GenerateNotifications(ctx, f.owner); err != nil {
		t.Fatal(err)
	}
	if tasks, err := r.ClaimNotifications(ctx); err != nil || len(tasks) != 0 {
		t.Fatal("大量取り込みも通知は1通", tasks, err)
	}
}

func TestNotificationReviewPushSuppressesResolvedAggregate(t *testing.T) {
	for _, action := range []string{"dismissed", "registered", "reserved"} {
		t.Run(action, func(t *testing.T) {
			f, r, now := notificationReviewFixture(t)
			ctx := context.Background()
			old := notificationReviewResult(t, f, now.Add(-48*time.Hour), ai.Event{Service: "Old unrelated", EventType: "renewal"})
			r = enableNotificationReviewPush(t, f, r, now)
			fresh := notificationReviewResult(t, f, *now, ai.Event{Service: "New", EventType: "change", RecurringAmount: autoPtr("99"), EffectiveDate: autoPtr("2030-02-01")})
			if n, err := r.GenerateNotifications(ctx, f.owner); err != nil || n != 1 {
				t.Fatal(n, err)
			}
			task := claimNotification(t, r)
			contracts := NewSubscriptionRepository(r.pool)
			switch action {
			case "dismissed":
				if err := contracts.DismissCandidate(ctx, f.owner, subscriptions.DismissInput{SourceResultID: fresh, Confirmed: true}); err != nil {
					t.Fatal(err)
				}
			case "registered":
				in := subscriptionInput()
				in.Service = "New"
				in.SourceResultID = &fresh
				in.SourceEventIndex = autoPtr(0)
				if _, err := contracts.Create(ctx, f.owner, in); err != nil {
					t.Fatal(err)
				}
			case "reserved":
				in := subscriptionInput()
				in.Service = "New"
				in.Amount = autoPtr("10")
				in.Currency = autoPtr("JPY")
				s, err := contracts.Create(ctx, f.owner, in)
				if err != nil {
					t.Fatal(err)
				}
				previewIn := subscriptions.ChangePreviewInput{ExpectedVersion: s.Version, SourceResultID: fresh}
				preview, err := contracts.ScheduledChangePreview(ctx, f.owner, s.ID, previewIn)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = contracts.ScheduleChange(ctx, f.owner, s.ID, subscriptions.ScheduleChangeInput{ChangeInput: subscriptions.ChangeInput{ChangePreviewInput: previewIn, RequestID: uuid.NewString(), PreviewToken: preview.PreviewToken, SelectedFields: []string{"amount"}, Confirmed: true}, EffectiveOn: "2030-02-01"}); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			r = r.WithPush(pushSenderFunc(func(context.Context, []byte, pushnotifications.Message) (pushnotifications.SendResult, error) {
				calls++
				return pushnotifications.SendResult{State: "accepted"}, nil
			}), true)
			if _, err := r.DeliverNotification(ctx, task.DeliveryID, task.LeaseID, nil); err != nil || calls != 0 {
				t.Fatal("解決済みの集約は送信しない", calls, err)
			}
			var state, reason string
			if err := r.pool.QueryRow(ctx, `SELECT state,reason FROM notification_deliveries WHERE id=$1`, task.DeliveryID).Scan(&state, &reason); err != nil || state != "suppressed" || reason != "source_changed" {
				t.Fatal(state, reason, err)
			}
			if _, err := contracts.Candidate(ctx, f.owner, old, 0); err != nil {
				t.Fatal("別の古い候補は残る", err)
			}
		})
	}
}

func TestNotificationReviewLegacyAutomationRequiresExplicitApproval(t *testing.T) {
	f, r, now := notificationReviewFixture(t)
	ctx := context.Background()
	contracts := NewSubscriptionRepository(r.pool)
	if _, err := contracts.SaveAutomation(ctx, f.owner, autoregistration.PreferenceInput{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	fresh := notificationReviewResult(t, f, *now, ai.Event{Service: "Explicit review", EventType: "renewal"})
	if n, err := contracts.RegisterDue(ctx, 10); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if n, err := r.GenerateNotifications(ctx, f.owner); err != nil || n != 1 {
		t.Fatal("旧enabled設定だけでは無期限待機しない", n, err)
	}
	var ledger int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM subscription_automation_results WHERE result_id=$1`, fresh).Scan(&ledger); err != nil || ledger != 0 {
		t.Fatal(ledger, err)
	}
}

func notificationReviewResult(t *testing.T, f processingFixture, now time.Time, events ...ai.Event) string {
	t.Helper()
	id := changeSource(t, f, now.Add(-time.Hour).Format(time.RFC3339), events...)
	if _, err := f.runtime.pool.Exec(context.Background(), `UPDATE mail_processing_runs SET finished_at=$2 WHERE id=(SELECT run_id FROM mail_processing_items WHERE id=$1)`, id, now); err != nil {
		t.Fatal(err)
	}
	return id
}

func cloneNotificationReviewRun(t *testing.T, f processingFixture, template string, now time.Time, sameMessage bool, n int, events ...ai.Event) string {
	t.Helper()
	ctx := context.Background()
	run := uuid.NewString()
	if _, err := f.runtime.pool.Exec(ctx, `INSERT INTO mail_processing_runs(id,owner_id,account_id,request_id,request_doc,config_version,credential_id,runtime_version,profile_id,policy_version,max_messages,total_count,state,finished_at) SELECT $1,owner_id,account_id,$2,'{}',config_version,credential_id,runtime_version,profile_id,policy_version,100,$3,'completed',$4 FROM mail_processing_runs WHERE id=(SELECT run_id FROM mail_processing_items WHERE id=$5)`, run, uuid.NewString(), n, now, template); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	if sameMessage {
		var id string
		if err := f.runtime.pool.QueryRow(ctx, `SELECT message_id FROM mail_processing_items WHERE id=$1`, template).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	} else {
		ids = f.add(t, n)
	}
	raw, _ := json.Marshal(events)
	for _, id := range ids {
		if _, err := f.runtime.pool.Exec(ctx, `INSERT INTO mail_processing_items(id,owner_id,account_id,run_id,message_id,state,events,created_at,finished_at) VALUES($1,$2,$3,$4,$5,'succeeded',$6,$7,$7)`, uuid.NewString(), f.owner, f.account, run, id, raw, now); err != nil {
			t.Fatal(err)
		}
	}
	return run
}

func TestNotificationReviewCompletionAndExactSourceSuppression(t *testing.T) {
	f, r, now := notificationReviewFixture(t)
	ctx := context.Background()
	old := notificationReviewResult(t, f, now.Add(-48*time.Hour), ai.Event{Service: "Old", EventType: "renewal"})
	if _, err := r.GenerateNotifications(ctx, f.owner); err != nil {
		t.Fatal(err, errors.Unwrap(err))
	}
	first := notificationReviewResult(t, f, *now, ai.Event{Service: "New", EventType: "renewal"})
	for _, state := range []string{"running", "failed", "cancelled"} {
		if _, err := f.runtime.pool.Exec(ctx, `UPDATE mail_processing_runs SET state=$2,finished_at=NULL WHERE id=(SELECT run_id FROM mail_processing_items WHERE id=$1)`, first, state); err != nil {
			t.Fatal(err)
		}
		if n, err := r.GenerateNotifications(ctx, f.owner); err != nil || n != 0 {
			t.Fatal("未完了・失敗・取消", state, n, err)
		}
	}
	if _, err := f.runtime.pool.Exec(ctx, `UPDATE mail_processing_runs SET state='completed',finished_at=$2 WHERE id=(SELECT run_id FROM mail_processing_items WHERE id=$1)`, first, *now); err != nil {
		t.Fatal(err)
	}
	if n, err := r.GenerateNotifications(ctx, f.owner); err != nil || n != 1 {
		t.Fatal("完了後の集約", n, err)
	}
	page, err := r.Notifications(ctx, f.owner, notifications.Filter{Read: "all", Limit: 100})
	if err != nil || page.Total != 1 || page.Notifications[0].Facts.Kind != notifications.PendingReview || *page.Notifications[0].Facts.ReviewCount != 1 {
		t.Fatal(page, err)
	}
	if err := NewSubscriptionRepository(f.runtime.pool).DismissCandidate(ctx, f.owner, subscriptions.DismissInput{SourceResultID: first, Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	tx, err := r.begin(ctx, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	current, err := currentNotificationCandidates(ctx, tx, f.owner, notifications.DefaultPreferences(), *now)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range current {
		if c.Kind == notifications.PendingReview {
			t.Fatal("古い別候補では送信対象を維持しない", c)
		}
	}
	if _, err := NewSubscriptionRepository(f.runtime.pool).Candidate(ctx, f.other, old, 0); err == nil {
		t.Fatal("他所有者")
	}
}

func TestNotificationReviewFingerprintNormalizesNullableAndNumericFacts(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	var a, b, c string
	if err := f.runtime.pool.QueryRow(ctx, `SELECT notification_review_fingerprint($1::jsonb),notification_review_fingerprint($2::jsonb),notification_review_fingerprint($3::jsonb)`, `{"service":"Example","event_type":"renewal","amount":"10.00"}`, `{"service":"Example","event_type":"renewal","amount":"10","auto_renew":null,"trial_end_date":null}`, `{"service":"Example","event_type":"renewal","amount":"11"}`).Scan(&a, &b, &c); err != nil || a != b || a == c {
		t.Fatal(a, b, c, err)
	}
}

func TestNotificationReviewReanalysisNewMailAndBulkGrouping(t *testing.T) {
	f, r, now := notificationReviewFixture(t)
	ctx := context.Background()
	event := ai.Event{Service: "Example", EventType: "renewal", Amount: autoPtr("10.00")}
	first := notificationReviewResult(t, f, *now, event, ai.Event{Service: "Other", EventType: "invoice"})
	if n, err := r.GenerateNotifications(ctx, f.owner); err != nil || n != 1 {
		t.Fatal("初回pollも新規解析を通知する", n, err)
	}
	*now = now.Add(time.Minute)
	event.Amount = autoPtr("10")
	cloneNotificationReviewRun(t, f, first, *now, true, 1, ai.Event{Service: "Other", EventType: "invoice"}, event)
	if n, err := r.GenerateNotifications(ctx, f.owner); err != nil || n != 0 {
		t.Fatal("result_idと配列順序が変わっても再送しない", n, err)
	}
	*now = now.Add(time.Minute)
	bulk := cloneNotificationReviewRun(t, f, first, *now, false, 60, event, event, ai.Event{Service: "Other", EventType: "invoice"})
	if n, err := r.GenerateNotifications(ctx, f.owner); err != nil || n != 1 {
		t.Fatal("大量取り込みは1集約", n, err)
	}
	var count, events, deliveries int
	if err := r.pool.QueryRow(ctx, `SELECT (SELECT review_count FROM notification_review_runs WHERE run_id=$1),(SELECT count(*) FROM notification_events WHERE owner_id=$2 AND kind='pending_review'),(SELECT count(*) FROM notification_deliveries WHERE owner_id=$2)`, bulk, f.owner).Scan(&count, &events, &deliveries); err != nil || count != 2 || events != 2 || deliveries != 2 {
		t.Fatal(count, events, deliveries, err)
	}
	if n, err := r.GenerateNotifications(ctx, f.owner); err != nil || n != 0 {
		t.Fatal("未送信の再生成", n, err)
	}
	page, err := NewSubscriptionRepository(r.pool).Candidates(ctx, f.owner, subscriptions.AnalysisFilter{Limit: 100})
	if err != nil || page.Total != 2 {
		t.Fatal("画面と同じグループ数", page.Total, err)
	}
}

func TestNotificationReviewReanalysisBeforeFirstPollKeepsLatestEvidence(t *testing.T) {
	f, r, now := notificationReviewFixture(t)
	ctx := context.Background()
	event := ai.Event{Service: "New evidence", EventType: "renewal"}
	first := notificationReviewResult(t, f, *now, event)
	*now = now.Add(time.Minute)
	cloneNotificationReviewRun(t, f, first, *now, true, 1, event)
	if n, err := r.GenerateNotifications(ctx, f.owner); err != nil || n != 1 {
		t.Fatal("同じメールを初回poll前に再解析しても最新の新着を捨てない", n, err)
	}
	page, err := r.Notifications(ctx, f.owner, notifications.Filter{Read: "all", Limit: 100})
	var firstRun string
	if err := r.pool.QueryRow(ctx, `SELECT run_id FROM mail_processing_items WHERE id=$1`, first).Scan(&firstRun); err != nil {
		t.Fatal(err)
	}
	if err != nil || page.Total != 1 || *page.Notifications[0].Facts.ReviewRunID != firstRun {
		t.Fatal(page, err)
	}
}

func TestNotificationReviewUnobservedReanalysisHonorsOriginalChannels(t *testing.T) {
	for _, mode := range []string{"off_then_on", "push_added"} {
		t.Run(mode, func(t *testing.T) {
			f, r, now := notificationReviewFixture(t)
			ctx := context.Background()
			if mode == "off_then_on" {
				p := notifications.DefaultPreferences()
				p.Kinds[notifications.PendingReview] = notifications.Channels{}
				if _, err := r.SaveNotificationSettings(ctx, f.owner, notifications.SettingsInput{Preferences: p, ExpectedVersion: 1, Confirmed: true}); err != nil {
					t.Fatal(err)
				}
			}
			event := ai.Event{Service: "Same evidence", EventType: "renewal", Amount: autoPtr("10.00")}
			first := notificationReviewResult(t, f, *now, event)
			*now = now.Add(time.Minute)
			if mode == "push_added" {
				r = enableNotificationReviewPush(t, f, r, now)
			} else {
				s, err := r.NotificationSettings(ctx, f.owner)
				if err != nil {
					t.Fatal(err)
				}
				p := s.Preferences
				p.Kinds[notifications.PendingReview] = notifications.Channels{InApp: true}
				if _, err = r.SaveNotificationSettings(ctx, f.owner, notifications.SettingsInput{Preferences: p, ExpectedVersion: p.Version, Confirmed: true}); err != nil {
					t.Fatal(err)
				}
			}
			*now = now.Add(time.Minute)
			event.Amount = autoPtr("10")
			cloneNotificationReviewRun(t, f, first, *now, true, 1, event)
			want := 0
			if mode == "push_added" {
				want = 1
			}
			if n, err := r.GenerateNotifications(ctx, f.owner); err != nil || n != want {
				t.Fatal("未観測の先行runのチャネルを維持する", n, want, err)
			}
			if tasks, err := r.ClaimNotifications(ctx); err != nil || len(tasks) != 0 {
				t.Fatal("再解析で過去分へPushを追加しない", tasks, err)
			}
		})
	}
}

func TestNotificationReviewCompletionQueueIsConsumed(t *testing.T) {
	f, r, now := notificationReviewFixture(t)
	ctx := context.Background()
	notificationReviewResult(t, f, *now, ai.Event{Service: "New", EventType: "renewal"})
	var count int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM notification_review_pending WHERE owner_id=$1`, f.owner).Scan(&count); err != nil || count != 1 {
		t.Fatal("履歴走査を避ける未処理の完了run", count, err)
	}
	if _, err := r.GenerateNotifications(ctx, f.owner); err != nil {
		t.Fatal(err)
	}
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM notification_review_pending WHERE owner_id=$1`, f.owner).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}

func TestNotificationReviewDisabledAndChannelChangesDoNotReplay(t *testing.T) {
	f, r, now := notificationReviewFixture(t)
	ctx := context.Background()
	save := func(channels notifications.Channels) {
		t.Helper()
		settings, err := r.NotificationSettings(ctx, f.owner)
		if err != nil {
			t.Fatal(err)
		}
		p := settings.Preferences
		p.Kinds[notifications.PendingReview] = channels
		if _, err = r.SaveNotificationSettings(ctx, f.owner, notifications.SettingsInput{Preferences: p, ExpectedVersion: p.Version, Confirmed: true}); err != nil {
			t.Fatal(err)
		}
	}
	save(notifications.Channels{})
	first := notificationReviewResult(t, f, *now, ai.Event{Service: "Existing", EventType: "renewal"})
	*now = now.Add(time.Minute)
	save(notifications.Channels{InApp: true})
	if n, err := r.GenerateNotifications(ctx, f.owner); err != nil || n != 0 {
		t.Fatal("有効化前に完了した未観測run", n, err)
	}
	cloneNotificationReviewRun(t, f, first, *now, true, 1, ai.Event{Service: "Existing", EventType: "renewal"})
	if n, err := r.GenerateNotifications(ctx, f.owner); err != nil || n != 0 {
		t.Fatal("既知情報の再解析", n, err)
	}
	cloneNotificationReviewRun(t, f, first, *now, false, 1, ai.Event{Service: "Existing", EventType: "renewal"})
	if n, err := r.GenerateNotifications(ctx, f.owner); err != nil || n != 1 {
		t.Fatal("同じサービスでも新メール", n, err)
	}
	*now = now.Add(time.Minute)
	save(notifications.Channels{})
	*now = now.Add(time.Minute)
	save(notifications.Channels{InApp: true})
	if n, err := r.GenerateNotifications(ctx, f.owner); err != nil || n != 0 {
		t.Fatal("再有効化", n, err)
	}
	var deliveries int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM notification_deliveries WHERE owner_id=$1`, f.owner).Scan(&deliveries); err != nil || deliveries != 1 {
		t.Fatal(deliveries, err)
	}
}
