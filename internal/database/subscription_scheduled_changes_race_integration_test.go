//go:build integration

package database

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/google/uuid"
)

func scheduledRaceFixture(t *testing.T) (processingFixture, *SubscriptionRepository, subscriptions.Subscription, subscriptions.ScheduledChange) {
	t.Helper()
	f := processingSetup(t)
	r := NewSubscriptionRepository(f.runtime.pool)
	in := subscriptionInput()
	in.Amount, in.Currency, in.EffectiveDate = autoPtr("10"), autoPtr("JPY"), autoPtr("2026-01-01")
	s, err := r.Create(context.Background(), f.owner, in)
	if err != nil {
		t.Fatal(err)
	}
	day := time.Now().In(time.FixedZone("Asia/Tokyo", 9*3600)).AddDate(0, 0, 1).Format("2006-01-02")
	source := changeSource(t, f, "2026-10-01T00:00:00Z", ai.Event{Service: s.Service, EventType: "change", RecurringAmount: autoPtr("99"), EffectiveDate: &day})
	previewInput := subscriptions.ChangePreviewInput{ExpectedVersion: s.Version, SourceResultID: source}
	p, err := r.ScheduledChangePreview(context.Background(), f.owner, s.ID, previewInput)
	if err != nil {
		t.Fatal(err)
	}
	result, err := r.ScheduleChange(context.Background(), f.owner, s.ID, subscriptions.ScheduleChangeInput{ChangeInput: subscriptions.ChangeInput{ChangePreviewInput: previewInput, RequestID: uuid.NewString(), PreviewToken: p.PreviewToken, SelectedFields: []string{"amount"}, Confirmed: true}, EffectiveOn: day})
	if err != nil {
		t.Fatal(err)
	}
	return f, r, s, result.Schedule
}

func TestScheduledChangesConcurrentWorkersApplyOnce(t *testing.T) {
	f, r, s, schedule := scheduledRaceFixture(t)
	ctx := context.Background()
	var wait sync.WaitGroup
	start := make(chan struct{})
	counts, failures := make(chan int, 2), make(chan error, 2)
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			n, err := r.applyScheduledChangesDue(ctx, 20, schedule.EffectiveOn)
			counts <- n
			failures <- err
		}()
	}
	close(start)
	wait.Wait()
	if total := <-counts + <-counts; total != 1 {
		t.Fatalf("applied %d times", total)
	}
	for range 2 {
		if err := <-failures; err != nil {
			t.Fatal(err)
		}
	}
	var version, receipts int
	var amount, startDate string
	if err := f.runtime.pool.QueryRow(ctx, `SELECT version,amount::text,effective_date::text FROM subscriptions WHERE id=$1`, s.ID).Scan(&version, &amount, &startDate); err != nil {
		t.Fatal(err)
	}
	if err := f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM subscription_change_requests WHERE owner_id=$1 AND subscription_id=$2`, f.owner, s.ID).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if version != int(s.Version)+1 || amount != "99" || startDate != "2026-01-01" || receipts != 1 {
		t.Fatalf("non-atomic or incorrect apply: version=%d amount=%s start=%s receipts=%d", version, amount, startDate, receipts)
	}
}

func TestScheduledChangeCancelRacesApplication(t *testing.T) {
	for range 3 {
		f, r, s, schedule := scheduledRaceFixture(t)
		ctx := context.Background()
		start := make(chan struct{})
		cancelled := make(chan error, 1)
		applied := make(chan error, 1)
		go func() {
			<-start
			_, err := r.CancelScheduledChange(ctx, f.owner, s.ID, schedule.ID, subscriptions.CancelScheduledChangeInput{ExpectedVersion: 1, Confirmed: true})
			cancelled <- err
		}()
		go func() { <-start; _, err := r.applyScheduledChangesDue(ctx, 20, schedule.EffectiveOn); applied <- err }()
		close(start)
		cancelErr, applyErr := <-cancelled, <-applied
		if applyErr != nil {
			t.Fatal(applyErr)
		}
		if cancelErr != nil && !errors.Is(cancelErr, subscriptions.ErrConflict) {
			t.Fatal(cancelErr)
		}
		page, err := r.ScheduledChanges(ctx, f.owner, s.ID, 20, 0)
		if err != nil || len(page.Schedules) != 1 {
			t.Fatal(page, err)
		}
		var count int
		if err = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM subscription_change_requests WHERE owner_id=$1 AND subscription_id=$2`, f.owner, s.ID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		state := page.Schedules[0].State
		if cancelErr == nil && (state != "cancelled" || count != 0) || cancelErr != nil && (state != "applied" || count != 1) {
			t.Fatalf("cancellation/application race inconsistent: cancel=%v state=%s receipts=%d", cancelErr, state, count)
		}
	}
}
