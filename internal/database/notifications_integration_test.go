//go:build integration

package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/enjoydarts/cycrio/internal/notifications"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"io/fs"
	"strings"
	"testing"
	"time"
)

func TestNotificationReanalysisCarriesReadSamePriceWithCurrentSource(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 3, 1, 0, 0, 0, time.UTC)
	r := NewNotificationRepository(f.runtime.pool, "", func() time.Time { return now })
	source := changeSource(t, f, "2026-01-01T00:00:00Z", ai.Event{Service: "Example", EventType: "renewal"})
	in := subscriptionInput()
	in.Service = "Example"
	in.Amount = autoPtr("10")
	in.Currency = autoPtr("USD")
	in.SourceResultID = &source
	in.SourceEventIndex = autoPtr(0)
	if _, e := NewSubscriptionRepository(f.runtime.pool).Create(ctx, f.owner, in); e != nil {
		t.Fatal(e)
	}
	event := ai.Event{Service: "Example", EventType: "change", RecurringAmount: autoPtr("20"), RecurringCurrency: autoPtr("USD")}
	changeSource(t, f, "2026-10-01T00:00:00Z", event)
	if _, e := r.GenerateNotifications(ctx, f.owner); e != nil {
		t.Fatal(e)
	}
	page, e := r.Notifications(ctx, f.owner, notifications.Filter{Read: "all", Limit: 20})
	if e != nil || page.Total != 1 {
		t.Fatal(page, e)
	}
	read, e := r.ReadNotification(ctx, f.owner, page.Notifications[0].ID)
	if e != nil {
		t.Fatal(e)
	}
	for iteration, amount := range []string{"20.00", "21"} {
		request := reanalysisInput(t, f)
		f.in.PolicyVersion = request.PolicyVersion
		batch, e := f.repo.Reanalyze(ctx, f.owner, f.account, request)
		if e != nil || batch.AcceptedCount != 1 {
			t.Fatal(batch, e)
		}
		lease, work := f.start(t)
		event.RecurringAmount = autoPtr(amount)
		label := "subscription_related"
		if e = f.repo.Finish(ctx, lease, mailprocessing.Completion{State: "succeeded", Classification: &label, ReceivedAt: receivedInstant("2026-10-01T00:00:00Z"), Events: []ai.Event{{Service: "Other", EventType: "renewal"}, event}}); e != nil {
			t.Fatal(e)
		}
		if _, e = r.GenerateNotifications(ctx, f.owner); e != nil {
			t.Fatal(e)
		}
		page, e = r.Notifications(ctx, f.owner, notifications.Filter{Read: "all", Limit: 20})
		if e != nil || page.Total != iteration+2 {
			t.Fatal(page, e)
		}
		var current *notifications.Item
		for i := range page.Notifications {
			if page.Notifications[i].Facts.ResultID != nil && *page.Notifications[i].Facts.ResultID == work.AttemptID {
				current = &page.Notifications[i]
				break
			}
		}
		if current == nil || current.Facts.EventIndex == nil || *current.Facts.EventIndex != 1 || current.SupersededAt != nil {
			t.Fatal("action must point to new current canonical ref", current, page)
		}
		if iteration == 0 && (current.ReadAt == nil || !current.ReadAt.Equal(*read.ReadAt) || page.UnreadCount != 0) {
			t.Fatal("same parsed price is already read", current, page)
		}
		if iteration == 1 && (current.ReadAt != nil || page.UnreadCount != 1) {
			t.Fatal("corrected new price may be unread", current, page)
		}
	}
}

func TestNotificationChannelHistoryUsesDeliveryLedgerNotFrozenFlags(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 3, 1, 0, 0, 0, time.UTC)
	r := NewNotificationRepository(f.runtime.pool, "", func() time.Time { return now })
	in := subscriptionInput()
	in.NextBillingDate = autoPtr("2026-11-10")
	c, e := NewSubscriptionRepository(f.runtime.pool).Create(ctx, f.owner, in)
	if e != nil {
		t.Fatal(e)
	}
	prefs := notifications.DefaultPreferences()
	prefs.EmailOptIn = true
	prefs.Kinds[notifications.Renewal] = notifications.Channels{Email: true}
	candidates, e := notifications.Generate(notifications.GenerationInput{Now: now, Contracts: []notifications.Contract{{ID: c.ID, Service: c.Service, Status: "active", ContractKind: "unknown", NextBillingDate: c.NextBillingDate, NextDateSource: "manual"}}}, prefs)
	if e != nil || len(candidates) != 1 {
		t.Fatal(candidates, e)
	}
	facts := candidates[0]
	raw, _ := json.Marshal(facts)
	id := uuid.NewString()
	if _, e = f.runtime.pool.Exec(ctx, `INSERT INTO notification_events(id,owner_id,kind,source_key,facts,contract_id,scheduled_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, id, f.owner, facts.Kind, facts.SourceKey, raw, c.ID, facts.ScheduledAt); e != nil {
		t.Fatal(e)
	}
	insert := func(channel string) {
		t.Helper()
		if _, e = f.runtime.pool.Exec(ctx, `INSERT INTO notification_deliveries(id,owner_id,notification_id,channel,state,scheduled_at) VALUES($1,$2,$3,$4,'accepted',$5)`, uuid.NewString(), f.owner, id, channel, facts.ScheduledAt); e != nil {
			t.Fatal(e)
		}
	}
	insert("email")
	page, e := r.Notifications(ctx, f.owner, notifications.Filter{Read: "all", Limit: 20})
	if e != nil || page.Total != 1 || page.UnreadCount != 0 {
		t.Fatal("email-only history remains owner-accessible", page, e)
	}
	if d, e := r.NotificationDeliveries(ctx, f.owner, id); e != nil || len(d.Deliveries) != 1 {
		t.Fatal(d, e)
	}
	insert("in_app")
	page, e = r.Notifications(ctx, f.owner, notifications.Filter{Read: "unread", Limit: 20})
	if e != nil || page.Total != 1 || page.UnreadCount != 1 || page.Notifications[0].Facts.InApp {
		t.Fatal("late channel must not depend on creation flags", page, e)
	}
	prefs = notifications.DefaultPreferences()
	prefs.Kinds[notifications.Renewal] = notifications.Channels{}
	if _, e = r.SaveNotificationSettings(ctx, f.owner, notifications.SettingsInput{Preferences: prefs, ExpectedVersion: 1, Confirmed: true}); e != nil {
		t.Fatal(e)
	}
	if _, e = r.ReadNotification(ctx, f.owner, id); e != nil {
		t.Fatal("channel OFF must permit own historical read receipt", e)
	}
	page, e = r.Notifications(ctx, f.owner, notifications.Filter{Read: "read", Limit: 20})
	if e != nil || page.Total != 1 || page.UnreadCount != 0 {
		t.Fatal(page, e)
	}
}

func TestNotificationSettingsCASDefaultsAndEmailUnavailable(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewNotificationRepository(f.runtime.pool, "")
	v, e := r.NotificationSettings(ctx, f.owner)
	if e != nil || v.Preferences.Version != 1 || v.Email.Available || v.Email.Reason != "delivery_engine_unavailable" {
		t.Fatal(v, e)
	}
	in := notifications.SettingsInput{ExpectedVersion: 1, Preferences: v.Preferences, Confirmed: true}
	in.LeadDays = 3
	saved, e := r.SaveNotificationSettings(ctx, f.owner, in)
	if e != nil || saved.Preferences.Version != 2 || saved.Preferences.LeadDays != 3 {
		t.Fatal(saved, e)
	}
	if _, e = r.SaveNotificationSettings(ctx, f.owner, in); !errors.Is(e, notifications.ErrConflict) {
		t.Fatal(e)
	}
	in.ExpectedVersion = 2
	in.EmailOptIn = true
	in.Kinds[notifications.Renewal] = notifications.Channels{InApp: true, Email: true}
	if _, e = r.SaveNotificationSettings(ctx, f.owner, in); !errors.Is(e, notifications.ErrNotReady) {
		t.Fatal(e)
	}
	other, e := r.NotificationSettings(ctx, f.other)
	if e != nil || other.Preferences.Version != 1 {
		t.Fatal(other, e)
	}
}

func TestNotificationScheduledHourIsRespectedByAllOwnerReads(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 2, 23, 59, 0, 0, time.UTC)
	r := NewNotificationRepository(f.runtime.pool, "", func() time.Time { return now })
	in := subscriptionInput()
	in.NextBillingDate = autoPtr("2026-11-10")
	if _, e := NewSubscriptionRepository(f.runtime.pool).Create(ctx, f.owner, in); e != nil {
		t.Fatal(e)
	}
	if _, e := r.GenerateNotifications(ctx, f.owner); e != nil {
		t.Fatal(e)
	}
	var id string
	if e := f.runtime.pool.QueryRow(ctx, `SELECT id FROM notification_events WHERE owner_id=$1`, f.owner).Scan(&id); e != nil {
		t.Fatal(e)
	}
	page, e := r.Notifications(ctx, f.owner, notifications.Filter{Read: "all", Limit: 20})
	if e != nil || page.Total != 0 || page.UnreadCount != 0 {
		t.Fatal("before 9 JST", page, e)
	}
	if _, e = r.ReadNotification(ctx, f.owner, id); !errors.Is(e, notifications.ErrNotFound) {
		t.Fatal("future item cannot be read directly", e)
	}
	if _, e = r.NotificationDeliveries(ctx, f.owner, id); !errors.Is(e, notifications.ErrNotFound) {
		t.Fatal("future delivery cannot be queried directly", e)
	}
	now = now.Add(time.Minute)
	page, e = r.Notifications(ctx, f.owner, notifications.Filter{Read: "unread", Limit: 20})
	if e != nil || page.Total != 1 || page.UnreadCount != 1 || !page.Notifications[0].Facts.ScheduledAt.Equal(now) {
		t.Fatal("at 9 JST without regeneration", page, e)
	}
}

func TestNotificationPreferencesResumeSameRelevantOccurrenceWithoutDuplicates(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewNotificationRepository(f.runtime.pool, "", func() time.Time { return time.Date(2026, 11, 3, 1, 0, 0, 0, time.UTC) })
	in := subscriptionInput()
	in.NextBillingDate = autoPtr("2026-11-10")
	if _, e := NewSubscriptionRepository(f.runtime.pool).Create(ctx, f.owner, in); e != nil {
		t.Fatal(e)
	}
	if _, e := r.GenerateNotifications(ctx, f.owner); e != nil {
		t.Fatal(e)
	}
	first, e := r.Notifications(ctx, f.owner, notifications.Filter{Read: "all", Limit: 20})
	if e != nil || first.Total != 1 {
		t.Fatal(first, e)
	}
	prefs := notifications.DefaultPreferences()
	prefs.Kinds[notifications.Renewal] = notifications.Channels{}
	saved, e := r.SaveNotificationSettings(ctx, f.owner, notifications.SettingsInput{Preferences: prefs, ExpectedVersion: 1, Confirmed: true})
	if e != nil {
		t.Fatal(e)
	}
	beforePoll, e := r.Notifications(ctx, f.owner, notifications.Filter{Read: "all", Limit: 20})
	if e != nil || beforePoll.Total != 1 || beforePoll.UnreadCount != 0 {
		t.Fatal("channel OFF must hide unread while preserving history", beforePoll, e)
	}
	unread, e := r.Notifications(ctx, f.owner, notifications.Filter{Read: "unread", Limit: 20})
	if e != nil || unread.Total != 0 {
		t.Fatal(unread, e)
	}
	if _, e = r.NotificationDeliveries(ctx, f.owner, first.Notifications[0].ID); e != nil {
		t.Fatal("OFF must preserve own delivery history", e)
	}
	if _, e = r.GenerateNotifications(ctx, f.owner); e != nil {
		t.Fatal(e)
	}
	prefs = saved.Preferences
	prefs.Kinds[notifications.Renewal] = notifications.Channels{InApp: true}
	if _, e = r.SaveNotificationSettings(ctx, f.owner, notifications.SettingsInput{Preferences: prefs, ExpectedVersion: 2, Confirmed: true}); e != nil {
		t.Fatal(e)
	}
	if _, e = r.GenerateNotifications(ctx, f.owner); e != nil {
		t.Fatal(e)
	}
	page, e := r.Notifications(ctx, f.owner, notifications.Filter{Read: "all", Limit: 20})
	if e != nil || page.Total != 1 || page.UnreadCount != 1 || page.Notifications[0].ID != first.Notifications[0].ID || page.Notifications[0].SupersededAt != nil || !page.Notifications[0].Facts.ScheduledAt.Equal(first.Notifications[0].Facts.ScheduledAt) {
		t.Fatal(page, e)
	}
	var deliveries int
	if e = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM notification_deliveries WHERE owner_id=$1`, f.owner).Scan(&deliveries); e != nil || deliveries != 1 {
		t.Fatal(deliveries, e)
	}
}

func TestNotificationImmutableCoreMismatchCannotReactivateStalePrices(t *testing.T) {
	for _, kind := range []string{"regular", "price"} {
		t.Run(kind, func(t *testing.T) {
			f := processingSetup(t)
			ctx := context.Background()
			r := NewNotificationRepository(f.runtime.pool, "", func() time.Time { return time.Date(2026, 11, 3, 1, 0, 0, 0, time.UTC) })
			in := subscriptionInput()
			in.Service = "Example"
			in.Amount = autoPtr("10")
			in.Currency = autoPtr("USD")
			if kind == "price" {
				source := changeSource(t, f, "2026-01-01T00:00:00Z", ai.Event{Service: "Example", EventType: "renewal"})
				in.SourceResultID = &source
				in.SourceEventIndex = autoPtr(0)
			} else {
				in.NextBillingDate = autoPtr("2026-11-10")
			}
			contracts := NewSubscriptionRepository(f.runtime.pool)
			c, e := contracts.Create(ctx, f.owner, in)
			if e != nil {
				t.Fatal(e)
			}
			if kind == "price" {
				changeSource(t, f, "2026-10-01T00:00:00Z", ai.Event{Service: "Example", EventType: "change", RecurringAmount: autoPtr("20"), RecurringCurrency: autoPtr("USD")})
			}
			if _, e = r.GenerateNotifications(ctx, f.owner); e != nil {
				t.Fatal(e)
			}
			first, e := r.Notifications(ctx, f.owner, notifications.Filter{Read: "all", Limit: 20})
			if e != nil || first.Total != 1 || first.UnreadCount != 1 {
				t.Fatal(first, e)
			}
			values := c.Values
			values.Amount = autoPtr("15")
			c, e = contracts.Update(ctx, f.owner, c.ID, subscriptions.UpdateInput{Values: values, ExpectedVersion: c.Version, Confirmed: true})
			if e != nil {
				t.Fatal(e)
			}
			if _, e = r.GenerateNotifications(ctx, f.owner); e != nil {
				t.Fatal(e)
			}
			stale, e := r.Notifications(ctx, f.owner, notifications.Filter{Read: "all", Limit: 20})
			if e != nil || stale.Total != 1 || stale.UnreadCount != 0 || stale.Notifications[0].SupersededAt == nil {
				t.Fatal("changed core facts must not reactivate first prices", stale, e)
			}
			facts := stale.Notifications[0].Facts
			old := facts.Amount
			if kind == "price" {
				old = facts.OldAmount
			}
			if old == nil || *old != "10" {
				t.Fatal("frozen original facts changed", facts)
			}
			values = c.Values
			values.Amount = autoPtr("10.00")
			if _, e = contracts.Update(ctx, f.owner, c.ID, subscriptions.UpdateInput{Values: values, ExpectedVersion: c.Version, Confirmed: true}); e != nil {
				t.Fatal(e)
			}
			if _, e = r.GenerateNotifications(ctx, f.owner); e != nil {
				t.Fatal(e)
			}
			restored, e := r.Notifications(ctx, f.owner, notifications.Filter{Read: "all", Limit: 20})
			if e != nil || restored.Total != 1 || restored.UnreadCount != 1 || restored.Notifications[0].ID != first.Notifications[0].ID || restored.Notifications[0].SupersededAt != nil || !restored.Notifications[0].Facts.ScheduledAt.Equal(first.Notifications[0].Facts.ScheduledAt) {
				t.Fatal("same semantic facts can restore existing occurrence", restored, e)
			}
		})
	}
}

func TestNotificationGenerationIsolatesOwnerFailureAndBacksOff(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 3, 1, 0, 0, 0, time.UTC)
	r := NewNotificationRepository(f.runtime.pool, "", func() time.Time { return now })
	badOwner, goodOwner := f.owner, f.other
	if strings.Compare(badOwner, goodOwner) > 0 {
		badOwner, goodOwner = goodOwner, badOwner
	}
	bad := subscriptionInput()
	bad.Service = "Legacy\nLabel"
	bad.NextBillingDate = autoPtr("2026-11-10")
	contracts := NewSubscriptionRepository(f.runtime.pool)
	c, e := contracts.Create(ctx, badOwner, bad)
	if e != nil {
		t.Fatal(e)
	}
	good := subscriptionInput()
	good.NextBillingDate = autoPtr("2026-11-10")
	if _, e = contracts.Create(ctx, goodOwner, good); e != nil {
		t.Fatal(e)
	}
	if count, e := r.GenerateDue(ctx, 100); e != nil || count != 1 {
		t.Fatal("one malformed owner must not stop other owners", count, e)
	}
	page, e := r.Notifications(ctx, goodOwner, notifications.Filter{Read: "all", Limit: 20})
	if e != nil || page.Total != 1 {
		t.Fatal(page, e)
	}
	var attempted time.Time
	var generated *time.Time
	var reason string
	if e = f.runtime.pool.QueryRow(ctx, `SELECT last_attempted_at,generated_at,last_error FROM notification_generation_state WHERE owner_id=$1`, badOwner).Scan(&attempted, &generated, &reason); e != nil || generated != nil || reason != "generation_invalid" {
		t.Fatal(attempted, generated, reason, e)
	}
	now = now.Add(30 * time.Second)
	if count, e := r.GenerateDue(ctx, 100); e != nil || count != 0 {
		t.Fatal(count, e)
	}
	var unchanged time.Time
	if e = f.runtime.pool.QueryRow(ctx, `SELECT last_attempted_at FROM notification_generation_state WHERE owner_id=$1`, badOwner).Scan(&unchanged); e != nil || !unchanged.Equal(attempted) {
		t.Fatal("failed owner retried before backoff", unchanged, e)
	}
	values := c.Values
	values.Service = "Valid Label"
	if _, e = contracts.Update(ctx, badOwner, c.ID, subscriptions.UpdateInput{Values: values, ExpectedVersion: c.Version, Confirmed: true}); e != nil {
		t.Fatal(e)
	}
	now = now.Add(31 * time.Second)
	if count, e := r.GenerateDue(ctx, 100); e != nil || count != 1 {
		t.Fatal("repaired owner must retry after backoff", count, e)
	}
	if e = f.runtime.pool.QueryRow(ctx, `SELECT generated_at,last_error FROM notification_generation_state WHERE owner_id=$1`, badOwner).Scan(&generated, &reason); e != nil || generated == nil || reason != "" {
		t.Fatal(generated, reason, e)
	}
}

func TestNotificationFailureRecordingStopsAtInactiveOwnerBoundary(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprint(existing), func(t *testing.T) {
			f := processingSetup(t)
			ctx := context.Background()
			now := time.Date(2026, 11, 3, 1, 0, 0, 0, time.UTC)
			r := NewNotificationRepository(f.runtime.pool, "", func() time.Time { return now })
			if existing {
				if _, e := f.runtime.pool.Exec(ctx, `INSERT INTO notification_generation_state(owner_id,generated_at,last_attempted_at,retry_at,last_error) VALUES($1,$2,$2,$3,'')`, f.owner, now.Add(-time.Hour), now.Add(-time.Minute)); e != nil {
					t.Fatal(e)
				}
			}
			const snapshot = `SELECT coalesce((SELECT to_jsonb(s) FROM notification_generation_state s WHERE s.owner_id=$1),'null'::jsonb)::text`
			var before, after string
			if e := f.runtime.pool.QueryRow(ctx, snapshot, f.owner).Scan(&before); e != nil {
				t.Fatal(e)
			}
			if _, e := f.runtime.pool.Exec(ctx, `UPDATE users SET state='disabled' WHERE id=$1`, f.owner); e != nil {
				t.Fatal(e)
			}
			for _, cause := range []error{notifications.ErrNotFound, notifications.ErrInvalid} {
				if e := r.recordNotificationFailure(ctx, f.owner, cause); e != nil {
					t.Fatal(e)
				}
			}
			if e := f.runtime.pool.QueryRow(ctx, snapshot, f.owner).Scan(&after); e != nil || before != after {
				t.Fatal("inactive owner state changed after generation failure", before, after, e)
			}
		})
	}
}

func TestNotificationPriceTargetNeedsOwnerConfirmedAccountSource(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewNotificationRepository(f.runtime.pool, "", func() time.Time { return time.Date(2026, 11, 3, 1, 0, 0, 0, time.UTC) })
	source := changeSource(t, f, "2026-01-01T00:00:00Z", ai.Event{Service: "Example", EventType: "renewal"})
	in := subscriptionInput()
	in.Service = "Example"
	in.Amount = autoPtr("10")
	in.Currency = autoPtr("USD")
	in.SourceResultID = &source
	in.SourceEventIndex = autoPtr(0)
	contracts := NewSubscriptionRepository(f.runtime.pool)
	c, e := contracts.Create(ctx, f.owner, in)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.runtime.pool.Exec(ctx, `UPDATE subscription_sources SET provenance='automatic' WHERE owner_id=$1 AND result_id=$2`, f.owner, source); e != nil {
		t.Fatal(e)
	}
	changeSource(t, f, "2026-10-03T00:00:00Z", ai.Event{Service: "Example", EventType: "change", RecurringAmount: autoPtr("20"), RecurringCurrency: autoPtr("USD")})
	if _, e = r.GenerateNotifications(ctx, f.owner); e != nil {
		t.Fatal(e)
	}
	page, e := r.Notifications(ctx, f.owner, notifications.Filter{Read: "all", Limit: 20})
	if e != nil || page.Total != 0 {
		t.Fatal("automatic source is not owner-confirmed identity", page, e)
	}
	confirmed := changeSource(t, f, "2026-10-02T00:00:00Z", ai.Event{Service: "Example", EventType: "invoice"})
	if _, e = contracts.AttachSource(ctx, f.owner, c.ID, subscriptions.SourceInput{ExpectedVersion: c.Version, SourceResultID: confirmed, Confirmed: true}); e != nil {
		t.Fatal(e)
	}
	if _, e = r.GenerateNotifications(ctx, f.owner); e != nil {
		t.Fatal(e)
	}
	page, e = r.Notifications(ctx, f.owner, notifications.Filter{Read: "all", Limit: 20})
	if e != nil || page.Total != 1 || page.Notifications[0].MatchBasis != "unique_confirmed_account_service" {
		t.Fatal(page, e)
	}
}

func TestNotificationSyncEpisodeRecoveryUsesActualImportCompletion(t *testing.T) {
	for _, success := range []string{"completed", "limited"} {
		t.Run(success, func(t *testing.T) {
			f := processingSetup(t)
			ctx := context.Background()
			r := NewNotificationRepository(f.runtime.pool, "", func() time.Time { return time.Date(2026, 11, 3, 1, 0, 0, 0, time.UTC) })
			if _, e := f.runtime.pool.Exec(ctx, `UPDATE mail_accounts SET checked_at=NULL WHERE id=$1`, f.account); e != nil {
				t.Fatal(e)
			}
			run := func(state, code, at string) {
				t.Helper()
				_, e := f.runtime.pool.Exec(ctx, `INSERT INTO mail_sync_runs(id,owner_id,account_id,request_id,config_version,credential_id,since,before,max_messages,state,folder_count,error_code,finished_at) SELECT $1,a.owner_id,a.id,$2,a.version,c.id,'2026-01-01','2026-02-01',1,$3,1,$4,$5::timestamptz FROM mail_accounts a JOIN mail_account_credentials c ON c.account_id=a.id AND c.owner_id=a.owner_id AND c.version=a.credential_version WHERE a.id=$6`, uuid.NewString(), uuid.NewString(), state, code, at, f.account)
				if e != nil {
					t.Fatal(e)
				}
			}
			run("failed", "timeout", "2026-10-01T10:00:00Z")
			if _, e := r.GenerateNotifications(ctx, f.owner); e != nil {
				t.Fatal(e)
			}
			first, e := r.Notifications(ctx, f.owner, notifications.Filter{Read: "all", Limit: 20})
			if e != nil || first.Total != 1 {
				t.Fatal(first, e)
			}
			if _, e = f.runtime.pool.Exec(ctx, `UPDATE mail_accounts SET state='ready',last_error='',checked_at='2026-10-01T11:00:00Z' WHERE id=$1`, f.account); e != nil {
				t.Fatal(e)
			}
			if _, e = r.GenerateNotifications(ctx, f.owner); e != nil {
				t.Fatal(e)
			}
			still, e := r.Notifications(ctx, f.owner, notifications.Filter{Read: "all", Limit: 20})
			if e != nil || still.Total != 1 || still.Notifications[0].ID != first.Notifications[0].ID || still.Notifications[0].SupersededAt != nil {
				t.Fatal("connection probe cannot prove import recovery", still, e)
			}
			run(success, "", "2026-10-01T12:00:00Z")
			if _, e = r.GenerateNotifications(ctx, f.owner); e != nil {
				t.Fatal(e)
			}
			recovered, e := r.Notifications(ctx, f.owner, notifications.Filter{Read: "all", Limit: 20})
			if e != nil || recovered.Total != 1 || recovered.UnreadCount != 0 || recovered.Notifications[0].SupersededAt == nil {
				t.Fatal("actual import completion resolves episode", recovered, e)
			}
			run("failed", "timeout", "2026-10-01T13:00:00Z")
			if _, e = r.GenerateNotifications(ctx, f.owner); e != nil {
				t.Fatal(e)
			}
			again, e := r.Notifications(ctx, f.owner, notifications.Filter{Read: "all", Limit: 20})
			if e != nil || again.Total != 2 || again.UnreadCount != 1 {
				t.Fatal("failure after import success is a new episode", again, e)
			}
			run("cancelled", "cancelled", "2026-10-01T14:00:00Z")
			if _, e = r.GenerateNotifications(ctx, f.owner); e != nil {
				t.Fatal(e)
			}
			stopped, e := r.Notifications(ctx, f.owner, notifications.Filter{Read: "all", Limit: 20})
			if e != nil || stopped.Total != 2 || stopped.UnreadCount != 0 {
				t.Fatal("explicit cancellation is not a new import failure", stopped, e)
			}
			if _, e = f.runtime.pool.Exec(ctx, `UPDATE mail_accounts SET state='needs_reauth',last_error='authentication_failed',checked_at='2026-10-01T15:00:00Z' WHERE id=$1`, f.account); e != nil {
				t.Fatal(e)
			}
			if _, e = r.GenerateNotifications(ctx, f.owner); e != nil {
				t.Fatal(e)
			}
			connection, e := r.Notifications(ctx, f.owner, notifications.Filter{Read: "all", Limit: 20})
			if e != nil || connection.Total != 3 || connection.UnreadCount != 1 {
				t.Fatal("an old cancellation cannot suppress a new connection error", connection, e)
			}
		})
	}
}

func TestNotificationPriceWatermarkIgnoresPureEvidenceButHonorsPriceApproval(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewNotificationRepository(f.runtime.pool, "", func() time.Time { return time.Date(2026, 10, 4, 1, 0, 0, 0, time.UTC) })
	source := changeSource(t, f, "2026-10-01T00:00:00Z", ai.Event{Service: "Example", EventType: "renewal"})
	in := subscriptionInput()
	in.Service = "Example"
	in.Amount = autoPtr("10")
	in.Currency = autoPtr("USD")
	in.SourceResultID = &source
	in.SourceEventIndex = autoPtr(0)
	contracts := NewSubscriptionRepository(f.runtime.pool)
	c, e := contracts.Create(ctx, f.owner, in)
	if e != nil {
		t.Fatal(e)
	}
	changeSource(t, f, "2026-10-02T00:00:00Z", ai.Event{Service: "Example", EventType: "change", RecurringAmount: autoPtr("16"), RecurringCurrency: autoPtr("USD"), EffectiveDate: autoPtr("2026-11-01")})
	evidence := changeSource(t, f, "2026-10-03T00:00:00Z", ai.Event{Service: "Example", EventType: "invoice"})
	c, e = contracts.AttachSource(ctx, f.owner, c.ID, subscriptions.SourceInput{ExpectedVersion: c.Version, SourceResultID: evidence, Confirmed: true})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.GenerateNotifications(ctx, f.owner); e != nil {
		t.Fatal(e)
	}
	page, e := r.Notifications(ctx, f.owner, notifications.Filter{Read: "all", Limit: 20})
	if e != nil || page.Total != 1 || page.Notifications[0].SupersededAt != nil {
		t.Fatal("pure evidence must not override price chronology", page, e)
	}
	approved := changeSource(t, f, "2026-10-03T01:00:00Z", ai.Event{Service: "Example", EventType: "change", RecurringAmount: autoPtr("15"), RecurringCurrency: autoPtr("USD")})
	previewIn := subscriptions.ChangePreviewInput{ExpectedVersion: c.Version, SourceResultID: approved}
	preview, e := contracts.ChangePreview(ctx, f.owner, c.ID, previewIn)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = contracts.ApplyChange(ctx, f.owner, c.ID, subscriptions.ChangeInput{ChangePreviewInput: previewIn, RequestID: uuid.NewString(), PreviewToken: preview.PreviewToken, SelectedFields: []string{"amount"}, Confirmed: true}); e != nil {
		t.Fatal(e)
	}
	if _, e = r.GenerateNotifications(ctx, f.owner); e != nil {
		t.Fatal(e)
	}
	page, e = r.Notifications(ctx, f.owner, notifications.Filter{Read: "all", Limit: 20})
	if e != nil || page.Total != 1 || page.Notifications[0].SupersededAt == nil {
		t.Fatal("newer price approval supersedes old evidence", page, e)
	}
}

func TestNotificationImportEpisodesRetryRecoveryAndManualStop(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewNotificationRepository(f.runtime.pool, "", func() time.Time { return time.Date(2026, 11, 3, 1, 0, 0, 0, time.UTC) })
	set := func(state, reason string) {
		t.Helper()
		if _, e := f.runtime.pool.Exec(ctx, `UPDATE mail_accounts SET state=$2,last_error=$3,checked_at=clock_timestamp() WHERE id=$1`, f.account, state, reason); e != nil {
			t.Fatal(e)
		}
	}
	set("needs_reauth", "authentication_failed")
	for i := 0; i < 2; i++ {
		if _, e := r.GenerateNotifications(ctx, f.owner); e != nil {
			t.Fatal(e)
		}
	}
	page, e := r.Notifications(ctx, f.owner, notifications.Filter{Read: "all", Limit: 20})
	if e != nil || page.Total != 1 || page.Notifications[0].Facts.Kind != notifications.ImportError {
		t.Fatal(page, e)
	}
	set("ready", "")
	if _, e = r.GenerateNotifications(ctx, f.owner); e != nil {
		t.Fatal(e)
	}
	set("needs_reauth", "authentication_failed")
	if _, e = r.GenerateNotifications(ctx, f.owner); e != nil {
		t.Fatal(e)
	}
	page, e = r.Notifications(ctx, f.owner, notifications.Filter{Read: "all", Limit: 20})
	if e != nil || page.Total != 2 || page.UnreadCount != 1 {
		t.Fatal(page, e)
	}
	if _, e = f.runtime.pool.Exec(ctx, `INSERT INTO mail_automations(id,owner_id,account_id,version,generation,state,reason) VALUES($1,$2,$3,1,1,'stopped','')`, uuid.NewString(), f.owner, f.account); e != nil {
		t.Fatal(e)
	}
	if _, e = r.GenerateNotifications(ctx, f.owner); e != nil {
		t.Fatal(e)
	}
	page, e = r.Notifications(ctx, f.owner, notifications.Filter{Read: "unread", Limit: 20})
	if e != nil || page.Total != 0 {
		t.Fatal("manual stop is not a new error", page, e)
	}
	var episodes int
	if e = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM notification_import_episodes WHERE owner_id=$1`, f.owner).Scan(&episodes); e != nil || episodes != 2 {
		t.Fatal(episodes, e)
	}
}

func TestNotificationMigrationPreservesContractsAndGuardsHistory(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	contracts := NewSubscriptionRepository(f.runtime.pool)
	in := subscriptionInput()
	in.NextBillingDate = autoPtr("2026-11-10")
	c, e := contracts.Create(ctx, f.owner, in)
	if e != nil {
		t.Fatal(e)
	}
	db, e := sql.Open("pgx", f.runtime.pool.Config().ConnString())
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	files, e := fs.Sub(migrations, "migrations")
	if e != nil {
		t.Fatal(e)
	}
	p, e := goose.NewProvider(goose.DialectPostgres, db, files)
	if e != nil {
		t.Fatal(e)
	}
	var before, after string
	if e = f.runtime.pool.QueryRow(ctx, `SELECT to_jsonb(s)::text FROM subscriptions s WHERE id=$1`, c.ID).Scan(&before); e != nil {
		t.Fatal(e)
	}
	down, e := p.DownTo(ctx, 33)
	if e != nil || len(down) != 1 || down[0].Direction != "down" || down[0].Source.Version != 34 {
		t.Fatal(down, e)
	}
	up, e := p.Up(ctx)
	assertMigrationRange(t, up, e, 34, 34)
	if e = f.runtime.pool.QueryRow(ctx, `SELECT to_jsonb(s)::text FROM subscriptions s WHERE id=$1`, c.ID).Scan(&after); e != nil || before != after {
		t.Fatal("migration changed confirmed contract", before, after, e)
	}
	r := NewNotificationRepository(f.runtime.pool, "", func() time.Time { return time.Date(2026, 11, 3, 1, 0, 0, 0, time.UTC) })
	if _, e = r.GenerateNotifications(ctx, f.owner); e != nil {
		t.Fatal(e)
	}
	if _, e = f.runtime.pool.Exec(ctx, `UPDATE notification_events SET facts='{}' WHERE owner_id=$1`, f.owner); e == nil {
		t.Fatal("facts must be immutable")
	}
	if _, e = p.DownTo(ctx, 33); e == nil {
		t.Fatal("history must block downgrade")
	}
}

func TestNotificationPriceMatchingRejectsUncertainTargets(t *testing.T) {
	for _, mode := range []string{"manual_without_sources", "ambiguous", "ambiguous_newer_other", "different_known_plan", "different_currency", "old_event", "unknown_received"} {
		t.Run(mode, func(t *testing.T) {
			f := processingSetup(t)
			ctx := context.Background()
			r := NewNotificationRepository(f.runtime.pool, "", func() time.Time { return time.Date(2026, 11, 3, 1, 0, 0, 0, time.UTC) })
			in := subscriptionInput()
			in.Service = "Example"
			in.Amount = autoPtr("10")
			in.Currency = autoPtr("USD")
			if mode != "manual_without_sources" {
				source := changeSource(t, f, "2026-10-02T00:00:00Z", ai.Event{Service: "Example", EventType: "renewal"})
				in.SourceResultID = &source
				in.SourceEventIndex = autoPtr(0)
			}
			if mode == "different_known_plan" {
				in.Plan = autoPtr("Gold")
			}
			c, e := NewSubscriptionRepository(f.runtime.pool).Create(ctx, f.owner, in)
			if e != nil {
				t.Fatal(e)
			}
			if mode == "ambiguous" || mode == "ambiguous_newer_other" {
				at := "2026-10-02T00:00:00Z"
				if mode == "ambiguous_newer_other" {
					at = "2026-10-04T00:00:00Z"
				}
				source := changeSource(t, f, at, ai.Event{Service: "Example", EventType: "renewal"})
				second := in
				second.RequestID = uuid.NewString()
				second.SourceResultID = &source
				if _, e = NewSubscriptionRepository(f.runtime.pool).Create(ctx, f.owner, second); e != nil {
					t.Fatal(e)
				}
			}
			event := ai.Event{Service: "example", EventType: "change", RecurringAmount: autoPtr("20"), RecurringCurrency: autoPtr("USD")}
			if mode == "different_known_plan" {
				event.Plan = autoPtr("Silver")
			}
			if mode == "different_currency" {
				event.RecurringCurrency = autoPtr("JPY")
			}
			received := "2026-10-03T00:00:00Z"
			if mode == "old_event" {
				received = "2026-10-01T00:00:00Z"
			}
			if mode == "unknown_received" {
				automationResult(t, f, event)
			} else {
				changeSource(t, f, received, event)
			}
			if _, e = r.GenerateNotifications(ctx, f.owner); e != nil {
				t.Fatal(e)
			}
			page, e := r.Notifications(ctx, f.owner, notifications.Filter{Read: "all", Limit: 20})
			if e != nil || page.Total != 0 {
				t.Fatal(mode, page, e)
			}
			got, e := NewSubscriptionRepository(f.runtime.pool).Get(ctx, f.owner, c.ID)
			if e != nil || got.Version != c.Version || *got.Amount != "10" || len(got.Sources) != len(c.Sources) {
				t.Fatal("notification generation mutated contract", got, e)
			}
		})
	}
}

func TestNotificationAtomicAuditAndConcurrentGeneration(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewNotificationRepository(f.runtime.pool, "", func() time.Time { return time.Date(2026, 11, 3, 1, 0, 0, 0, time.UTC) })
	if _, e := f.runtime.pool.Exec(ctx, `CREATE FUNCTION reject_notification_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic audit failure'; END $$; CREATE TRIGGER reject_notification_audit BEFORE INSERT ON notification_preferences_audit FOR EACH ROW EXECUTE FUNCTION reject_notification_audit()`); e != nil {
		t.Fatal(e)
	}
	prefs := notifications.DefaultPreferences()
	prefs.LeadDays = 3
	if _, e := r.SaveNotificationSettings(ctx, f.owner, notifications.SettingsInput{Preferences: prefs, ExpectedVersion: 1, Confirmed: true}); e == nil {
		t.Fatal("audit must abort preference change")
	}
	settings, e := r.NotificationSettings(ctx, f.owner)
	if e != nil || settings.Preferences.Version != 1 || settings.Preferences.LeadDays != 7 {
		t.Fatal(settings, e)
	}
	in := subscriptionInput()
	in.NextBillingDate = autoPtr("2026-11-10")
	if _, e = NewSubscriptionRepository(f.runtime.pool).Create(ctx, f.owner, in); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { _, e := r.GenerateNotifications(ctx, f.owner); done <- e }()
	}
	for i := 0; i < 2; i++ {
		if e = <-done; e != nil {
			t.Fatal(e)
		}
	}
	var events, deliveries int
	if e = f.runtime.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM notification_events WHERE owner_id=$1),(SELECT count(*) FROM notification_deliveries WHERE owner_id=$1)`, f.owner).Scan(&events, &deliveries); e != nil || events != 1 || deliveries != 1 {
		t.Fatal(events, deliveries, e)
	}
}

func TestNotificationsGenerationReadIdempotencyAndSupersession(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 3, 1, 0, 0, 0, time.UTC)
	r := NewNotificationRepository(f.runtime.pool, "", func() time.Time { return now })
	in := subscriptionInput()
	in.NextBillingDate = autoPtr("2026-11-10")
	contract, e := NewSubscriptionRepository(f.runtime.pool).Create(ctx, f.owner, in)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.GenerateNotifications(ctx, f.owner); e != nil {
		t.Fatal(e)
	}
	page, e := r.Notifications(ctx, f.owner, notifications.Filter{Read: "all", Limit: 20})
	if e != nil || page.Total != 1 || page.UnreadCount != 1 {
		t.Fatal(page, e)
	}
	first := page.Notifications[0]
	now = now.Add(time.Hour)
	if _, e = r.GenerateNotifications(ctx, f.owner); e != nil {
		t.Fatal(e)
	}
	page, e = r.Notifications(ctx, f.owner, notifications.Filter{Read: "all", Limit: 20})
	if e != nil || page.Total != 1 || !page.Notifications[0].Facts.ScheduledAt.Equal(first.Facts.ScheduledAt) {
		t.Fatal(page, e)
	}
	if _, e = r.ReadNotification(ctx, f.other, first.ID); !errors.Is(e, notifications.ErrNotFound) {
		t.Fatal(e)
	}
	read, e := r.ReadNotification(ctx, f.owner, first.ID)
	if e != nil || read.ReadAt == nil {
		t.Fatal(read, e)
	}
	again, e := r.ReadNotification(ctx, f.owner, first.ID)
	if e != nil || !again.ReadAt.Equal(*read.ReadAt) {
		t.Fatal(again, e)
	}
	deliveries, e := r.NotificationDeliveries(ctx, f.owner, first.ID)
	if e != nil || len(deliveries.Deliveries) != 1 || deliveries.Deliveries[0].Channel != "in_app" || deliveries.Deliveries[0].State != "accepted" {
		t.Fatal(deliveries, e)
	}
	values := contract.Values
	values.Status = "ended"
	if _, e = NewSubscriptionRepository(f.runtime.pool).Update(ctx, f.owner, contract.ID, subscriptions.UpdateInput{Values: values, ExpectedVersion: contract.Version, Confirmed: true}); e != nil {
		t.Fatal(e)
	}
	if _, e = r.GenerateNotifications(ctx, f.owner); e != nil {
		t.Fatal(e)
	}
	page, e = r.Notifications(ctx, f.owner, notifications.Filter{Read: "all", Limit: 20})
	if e != nil || page.Notifications[0].SupersededAt == nil {
		t.Fatal(page, e)
	}
}

func TestNotificationTentativePriceRequiresUniqueApprovedAccountAndPendingSource(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 3, 1, 0, 0, 0, time.UTC)
	r := NewNotificationRepository(f.runtime.pool, "", func() time.Time { return now })
	original := changeSource(t, f, "2026-01-01T00:00:00Z", ai.Event{Service: "Example", EventType: "renewal"})
	in := subscriptionInput()
	in.Service = "Example"
	in.Amount = autoPtr("10")
	in.Currency = autoPtr("USD")
	in.SourceResultID = &original
	in.SourceEventIndex = autoPtr(0)
	if _, e := NewSubscriptionRepository(f.runtime.pool).Create(ctx, f.owner, in); e != nil {
		t.Fatal(e)
	}
	result := changeSource(t, f, "2026-11-01T00:00:00Z", ai.Event{Service: "example", EventType: "change", RecurringAmount: autoPtr("20"), RecurringCurrency: autoPtr("USD")})
	if _, e := r.GenerateNotifications(ctx, f.owner); e != nil {
		t.Fatal(e)
	}
	page, e := r.Notifications(ctx, f.owner, notifications.Filter{Read: "all", Limit: 20})
	if e != nil || page.Total != 1 || page.Notifications[0].Facts.Kind != notifications.PriceIncrease || page.Notifications[0].MatchBasis != "unique_confirmed_account_service" {
		t.Fatal(page, e)
	}
	c, e := NewSubscriptionRepository(f.runtime.pool).Candidate(ctx, f.owner, result, 0)
	if e != nil || c.ResultID != result {
		t.Fatal(c, e)
	}
	if e = NewSubscriptionRepository(f.runtime.pool).DismissCandidate(ctx, f.owner, subscriptions.DismissInput{SourceResultID: result, Confirmed: true}); e != nil {
		t.Fatal(e)
	}
	if _, e = NewSubscriptionRepository(f.runtime.pool).Candidate(ctx, f.owner, result, 0); !errors.Is(e, subscriptions.ErrNotFound) {
		t.Fatal(e)
	}
	if _, e = r.GenerateNotifications(ctx, f.owner); e != nil {
		t.Fatal(e)
	}
	page, e = r.Notifications(ctx, f.owner, notifications.Filter{Read: "all", Limit: 20})
	if e != nil || page.Notifications[0].SupersededAt == nil {
		t.Fatal(page, e)
	}
}
