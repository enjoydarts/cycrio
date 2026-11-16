//go:build integration

package database

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/google/uuid"
)

func recurringSourceFixture(t *testing.T) (processingFixture, *SubscriptionRepository, subscriptions.Subscription, ai.Event) {
	t.Helper()
	f := processingSetup(t)
	r := NewSubscriptionRepository(f.runtime.pool)
	event := ai.Event{Service: "Example", Plan: autoPtr("Pro"), EventType: "payment", Amount: autoPtr("100"), Currency: autoPtr("USD"), IntervalUnit: autoPtr("month"), IntervalCount: autoPtr(int64(1)), ContractKind: autoPtr("recurring"), BilledAt: autoPtr("2026-01-01")}
	result := changeSource(t, f, "2026-01-01T00:00:00Z", event)
	index := 0
	in := subscriptionInput()
	in.Values = subscriptions.Values{Service: event.Service, Plan: event.Plan, Status: "active", Amount: event.Amount, Currency: event.Currency, IntervalUnit: event.IntervalUnit, IntervalCount: autoPtr(1), ContractKind: "recurring"}
	in.SourceResultID, in.SourceEventIndex = &result, &index
	s, err := r.Create(context.Background(), f.owner, in)
	if err != nil {
		t.Fatal(err)
	}
	return f, r, s, event
}

func TestRecurringSourcesKeepChangedOrUnknownFactsPending(t *testing.T) {
	for name, change := range map[string]func(*ai.Event){
		"price":           func(e *ai.Event) { e.Amount = autoPtr("101") },
		"currency":        func(e *ai.Event) { e.Currency = autoPtr("JPY") },
		"plan":            func(e *ai.Event) { e.Plan = autoPtr("Other") },
		"cycle":           func(e *ai.Event) { e.IntervalUnit = autoPtr("year") },
		"cycle_count":     func(e *ai.Event) { e.IntervalCount = autoPtr(int64(2)) },
		"unknown_cycle":   func(e *ai.Event) { e.IntervalCount = nil },
		"unknown_price":   func(e *ai.Event) { e.Amount = nil },
		"unknown_date":    func(e *ai.Event) { e.BilledAt = nil },
		"refund":          func(e *ai.Event) { e.EventType = "refund" },
		"cancellation":    func(e *ai.Event) { e.EventType = "cancellation" },
		"trial":           func(e *ai.Event) { e.TrialEndDate = autoPtr("2026-02-10") },
		"renewal_setting": func(e *ai.Event) { e.AutoRenew = autoPtr(false) },
		"end_date":        func(e *ai.Event) { e.AccessEndDate = autoPtr("2026-03-01") },
	} {
		t.Run(name, func(t *testing.T) {
			f, r, s, event := recurringSourceFixture(t)
			change(&event)
			automationResult(t, f, event)
			if n, err := r.RegisterDue(context.Background(), 20); err != nil || n != 0 {
				t.Fatal("changed facts auto-attached", n, err)
			}
			got, err := r.Get(context.Background(), f.owner, s.ID)
			if err != nil || len(got.Sources) != 1 || got.Version != s.Version {
				t.Fatal("contract changed", got, err)
			}
		})
	}
}

func TestRecurringSourcesRespectCurrentContractAndDecisions(t *testing.T) {
	for _, action := range []string{"price_changed", "plan_changed", "cycle_changed", "ended", "deleted", "dismissed", "account_stopped", "owner_stopped", "automatic_seed", "ambiguous", "ambiguous_price", "ambiguous_ended"} {
		t.Run(action, func(t *testing.T) {
			f, r, s, event := recurringSourceFixture(t)
			ctx := context.Background()
			result := automationResult(t, f, event)
			var err error
			switch action {
			case "price_changed":
				_, err = f.runtime.pool.Exec(ctx, `UPDATE subscriptions SET amount='200' WHERE id=$1`, s.ID)
			case "plan_changed":
				_, err = f.runtime.pool.Exec(ctx, `UPDATE subscriptions SET plan='Other' WHERE id=$1`, s.ID)
			case "cycle_changed":
				_, err = f.runtime.pool.Exec(ctx, `UPDATE subscriptions SET interval_unit='year' WHERE id=$1`, s.ID)
			case "ended":
				_, err = f.runtime.pool.Exec(ctx, `UPDATE subscriptions SET status='ended' WHERE id=$1`, s.ID)
			case "deleted":
				err = r.Delete(ctx, f.owner, s.ID, subscriptions.DeleteInput{ExpectedVersion: s.Version, Confirmed: true})
			case "dismissed":
				err = r.DismissCandidate(ctx, f.owner, subscriptions.DismissInput{SourceResultID: result, SourceEventIndex: 0, Confirmed: true})
			case "account_stopped":
				_, err = f.runtime.pool.Exec(ctx, `UPDATE mail_accounts SET state='disconnected' WHERE id=$1`, f.account)
			case "owner_stopped":
				_, err = f.runtime.pool.Exec(ctx, `UPDATE users SET state='disabled' WHERE id=$1`, f.owner)
			case "automatic_seed":
				_, err = f.runtime.pool.Exec(ctx, `UPDATE subscription_sources SET provenance='automatic' WHERE subscription_id=$1`, s.ID)
			case "ambiguous", "ambiguous_price", "ambiguous_ended":
				index := 0
				in := subscriptionInput()
				in.Values = s.Values
				in.SourceResultID, in.SourceEventIndex = &result, &index
				_, err = r.Create(ctx, f.owner, in)
				if err == nil && action == "ambiguous_price" {
					_, err = f.runtime.pool.Exec(ctx, `UPDATE subscriptions SET amount='200' WHERE id=$1`, s.ID)
				}
				if err == nil && action == "ambiguous_ended" {
					_, err = f.runtime.pool.Exec(ctx, `UPDATE subscriptions SET status='ended' WHERE id=$1`, s.ID)
				}
				automationResult(t, f, event)
			}
			if err != nil {
				t.Fatal(err)
			}
			if n, err := r.RegisterDue(ctx, 20); err != nil || n != 0 {
				t.Fatal("protected evidence was attached", n, err)
			}
		})
	}
}

func TestRecurringSourcesConcurrentAndAuditRollback(t *testing.T) {
	f, r, original, event := recurringSourceFixture(t)
	ctx := context.Background()
	result := automationResult(t, f, event)
	if _, err := f.runtime.pool.Exec(ctx, `CREATE FUNCTION reject_recurring_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='auto_attached' THEN RAISE EXCEPTION 'synthetic audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_recurring_audit BEFORE INSERT ON subscription_audit FOR EACH ROW EXECUTE FUNCTION reject_recurring_audit()`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RegisterDue(ctx, 20); err == nil {
		t.Fatal("audit failure must roll back attachment")
	}
	s, err := r.Get(ctx, f.owner, original.ID)
	if err != nil || len(s.Sources) != 1 || s.Version != original.Version {
		t.Fatal("partial mutation escaped", s, err)
	}
	if _, err = f.runtime.pool.Exec(ctx, `DROP TRIGGER reject_recurring_audit ON subscription_audit; DROP FUNCTION reject_recurring_audit()`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			if _, e := r.RegisterDue(ctx, 20); e != nil {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	var count int
	if err = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM subscription_sources WHERE result_id=$1`, result).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate evidence", count, err)
	}
	if err = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM subscription_audit WHERE subscription_id=$1 AND action='auto_attached'`, s.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate audit", count, err)
	}
}

func TestRecurringSourcesAreLinkedBeforeReviewNotification(t *testing.T) {
	f, r, s, event := recurringSourceFixture(t)
	ctx := context.Background()
	now := time.Now().Add(time.Minute)
	event.BilledAt = autoPtr("2026-02-01")
	notificationReviewResult(t, f, now, event)
	n := NewNotificationDeliveryRepository(f.runtime.pool, f.keys, "https://clerk.test.example", func() time.Time { return now })
	if count, err := n.GenerateNotifications(ctx, f.owner); err != nil || count != 0 {
		t.Fatal("unchanged monthly payment must not send a review notification", count, err)
	}
	got, err := r.Get(ctx, f.owner, s.ID)
	if err != nil || len(got.Sources) != 2 {
		t.Fatal("notification observation must attach first", got, err)
	}
}

func TestRecurringSourcesSeparateOwnersAndMailAccounts(t *testing.T) {
	for _, otherOwner := range []bool{false, true} {
		f, r, s, event := recurringSourceFixture(t)
		second := f
		second.account, second.folder = uuid.NewString(), uuid.NewString()
		if otherOwner {
			second.owner = f.other
		}
		credential := mailaccounts.CredentialVersion{ID: uuid.NewString(), OwnerID: second.owner, AccountID: second.account, Version: 1}
		var err error
		credential.Envelope, err = f.keys.Seal([]byte(`{"login":"c3ludGhldGlj","password":"c2VjcmV0"}`), mailaccounts.CredentialAAD(credential))
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		if _, err = NewMailAccountRepository(f.runtime.pool).Save(ctx, second.owner, second.account, 0, "Other", credential); err != nil {
			t.Fatal(err)
		}
		if _, err = f.runtime.pool.Exec(ctx, `UPDATE mail_accounts SET state='ready' WHERE id=$1`, second.account); err != nil {
			t.Fatal(err)
		}
		if _, err = f.runtime.pool.Exec(ctx, `INSERT INTO mail_account_folders(id,account_id,name,delimiter,selectable,available,selected) VALUES($1,$2,'INBOX','/',true,true,true)`, second.folder, second.account); err != nil {
			t.Fatal(err)
		}
		automationResult(t, second, event)
		if n, err := r.RegisterDue(ctx, 20); err != nil || n != 0 {
			t.Fatal("approval crossed owner/account boundary", otherOwner, n, err)
		}
		got, err := r.Get(ctx, f.owner, s.ID)
		if err != nil || len(got.Sources) != 1 {
			t.Fatal(got, err)
		}
	}
}

func TestRecurringSourcesIgnoreSupersededResultsAndNullSchemaDifferences(t *testing.T) {
	f, r, s, event := recurringSourceFixture(t)
	ctx := context.Background()
	old := automationResult(t, f, event)
	run := cloneNotificationReviewRun(t, f, old, time.Now().Add(time.Second), true, 1, event)
	if _, err := f.runtime.pool.Exec(ctx, `UPDATE mail_processing_items SET state='not_related',events='[]' WHERE run_id=$1`, run); err != nil {
		t.Fatal(err)
	}
	if n, err := r.RegisterDue(ctx, 20); err != nil || n != 0 {
		t.Fatal("superseded successful extraction attached", n, err)
	}
	current := automationResult(t, f, event)
	if _, err := f.runtime.pool.Exec(ctx, `UPDATE mail_processing_items SET events=jsonb_set(events,'{0}',events->0 || '{"trial_end_date":null,"auto_renew":null}'::jsonb) WHERE id=$1`, current); err != nil {
		t.Fatal(err)
	}
	if n, err := r.RegisterDue(ctx, 20); err != nil || n != 1 {
		t.Fatal("missing and null fields must be equivalent", n, err)
	}
	got, err := r.Get(ctx, f.owner, s.ID)
	if err != nil || len(got.Sources) != 2 {
		t.Fatal(got, err)
	}
}

func TestRecurringSourcesDoNotInvalidateScheduledContractChanges(t *testing.T) {
	f, r, s, event := recurringSourceFixture(t)
	ctx := context.Background()
	date := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	change := changeSource(t, f, time.Now().Format(time.RFC3339), ai.Event{Service: s.Service, EventType: "change", RecurringAmount: autoPtr("200"), RecurringCurrency: autoPtr("USD"), EffectiveDate: &date})
	in := subscriptions.ChangePreviewInput{ExpectedVersion: s.Version, SourceResultID: change}
	preview, err := r.ScheduledChangePreview(ctx, f.owner, s.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	scheduled, err := r.ScheduleChange(ctx, f.owner, s.ID, subscriptions.ScheduleChangeInput{ChangeInput: subscriptions.ChangeInput{ChangePreviewInput: in, RequestID: uuid.NewString(), PreviewToken: preview.PreviewToken, SelectedFields: []string{"amount"}, Confirmed: true}, EffectiveOn: date})
	if err != nil {
		t.Fatal(err)
	}
	automationResult(t, f, event)
	if n, err := r.RegisterDue(ctx, 20); err != nil || n != 0 {
		t.Fatal("monthly evidence must not invalidate an approved schedule", n, err)
	}
	got, err := r.Get(ctx, f.owner, s.ID)
	if err != nil || got.Version != scheduled.Schedule.BaseVersion || len(got.Sources) != 1 {
		t.Fatal("approved schedule's contract version changed", got, err)
	}
}

func TestRecurringSourcesLegacyRenewalMetadataDoesNotRepeatApproval(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		f, r, original, event := recurringSourceFixture(t)
		ctx := context.Background()
		if disabled {
			if _, err := f.runtime.pool.Exec(ctx, `UPDATE subscriptions SET auto_renew=false WHERE id=$1`, original.ID); err != nil {
				t.Fatal(err)
			}
		}
		event.AutoRenew = autoPtr(true)
		event.BilledAt = autoPtr("2026-02-01")
		automationResult(t, f, event)
		want := 1
		if disabled {
			want = 0
		}
		if n, err := r.RegisterDue(ctx, 20); err != nil || n != want {
			t.Fatal("new affirmative metadata must not hide an explicit disabled renewal", disabled, n, err)
		}
		got, err := r.Get(ctx, f.owner, original.ID)
		if err != nil || disabled && (got.AutoRenew == nil || *got.AutoRenew) || !disabled && got.AutoRenew != nil {
			t.Fatal("linking cannot change renewal settings", got, err)
		}
	}
}

func TestRecurringSourcesLegacyRenewalCompatibilityPreservesApprovedStop(t *testing.T) {
	for _, kind := range []string{"payment", "cancellation", "change", "different_price"} {
		t.Run(kind, func(t *testing.T) {
			f, r, s, event := recurringSourceFixture(t)
			ctx := context.Background()
			stop := event
			stop.AutoRenew = autoPtr(false)
			if kind == "different_price" {
				stop.Amount = autoPtr("200")
			} else {
				stop.EventType = kind
			}
			stopped := automationResult(t, f, stop)
			if _, err := r.AttachSource(ctx, f.owner, s.ID, subscriptions.SourceInput{ExpectedVersion: s.Version, SourceResultID: stopped, SourceEventIndex: 0, Confirmed: true}); err != nil {
				t.Fatal(err)
			}
			event.AutoRenew = autoPtr(true)
			automationResult(t, f, event)
			if n, err := r.RegisterDue(ctx, 20); err != nil || n != 0 {
				t.Fatal("older unknown flag cannot override an approved renewal stop", n, err)
			}
		})
	}
}

func TestRecurringSourcesAttachOnlyUnchangedApprovedPayments(t *testing.T) {
	f, r, original, event := recurringSourceFixture(t)
	ctx := context.Background()
	event.BilledAt = autoPtr("2026-02-01")
	event.PeriodStart = autoPtr("2026-02-01")
	event.PeriodEnd = autoPtr("2026-02-28")
	event.NextBillingAt = autoPtr("2026-03-01")
	event.Amount = autoPtr("100.00")
	result := automationResult(t, f, event)
	if n, err := r.RegisterDue(ctx, 20); err != nil || n != 1 {
		t.Fatalf("same payment should attach automatically: n=%d err=%v", n, err)
	}
	saved, err := r.Get(ctx, f.owner, original.ID)
	if err != nil || len(saved.Sources) != 2 || saved.Version != original.Version+1 || !reflect.DeepEqual(saved.Values, original.Values) {
		t.Fatal("only evidence and version may change", saved, err)
	}
	var provenance string
	var anchor bool
	if err = f.runtime.pool.QueryRow(ctx, `SELECT provenance,affects_billing_anchor FROM subscription_sources WHERE result_id=$1`, result).Scan(&provenance, &anchor); err != nil || provenance != "automatic" || anchor {
		t.Fatal("automatic evidence must preserve billing anchor", provenance, anchor, err)
	}
	page, err := r.Candidates(ctx, f.owner, subscriptions.AnalysisFilter{Limit: 100})
	if err != nil || page.Total != 0 {
		t.Fatal("monthly confirmation remains", page, err)
	}
	if n, err := r.RegisterDue(ctx, 20); err != nil || n != 0 {
		t.Fatal("repeat must be idempotent", n, err)
	}
}
