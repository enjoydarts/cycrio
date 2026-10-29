//go:build integration

package database

import (
	"context"
	"errors"
	"testing"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/google/uuid"
)

func subscriptionInput() subscriptions.CreateInput {
	return subscriptions.CreateInput{Values: subscriptions.Values{Service: "Confirmed service", Status: "active"}, RequestID: uuid.NewString(), Confirmed: true}
}
func subscriptionResult(t *testing.T, f processingFixture, n int) string {
	t.Helper()
	f.add(t, 1)
	in := f.in
	in.RequestID = uuid.NewString()
	in.MaxMessages = 1
	if _, e := f.repo.Request(context.Background(), f.owner, f.account, in); e != nil {
		t.Fatal(e)
	}
	lease, work := f.start(t)
	events := make([]ai.Event, n)
	for i := range events {
		events[i] = ai.Event{Service: "Original service", EventType: "renewal"}
	}
	label := "subscription_related"
	if e := f.repo.Finish(context.Background(), lease, mailprocessing.Completion{State: "succeeded", Classification: &label, Events: events}); e != nil {
		t.Fatal(e)
	}
	return work.AttemptID
}
func TestSubscriptionsTransactionsOwnershipAndSources(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	repo := NewSubscriptionRepository(f.runtime.pool)
	result := subscriptionResult(t, f, 32)
	index := 0
	in := subscriptionInput()
	in.SourceResultID = &result
	in.SourceEventIndex = &index
	first, e := repo.Create(ctx, f.owner, in)
	if e != nil || first.Version != 1 || len(first.Sources) != 1 || first.Sources[0].OriginalEvent.Service != "Original service" {
		t.Fatal(first, e)
	}
	if first.Sources[0].AccountID == nil || *first.Sources[0].AccountID != f.account || first.Sources[0].AccountLabel == nil || *first.Sources[0].AccountLabel != "Synthetic" || first.Sources[0].Provider == nil || *first.Sources[0].Provider != "fastmail" {
		t.Fatal("created source connection metadata", first.Sources[0])
	}
	again, e := repo.Create(ctx, f.owner, in)
	if e != nil || again.ID != first.ID {
		t.Fatal("idempotent", again, e)
	}
	changed := in
	changed.Service = "Changed"
	if _, e = repo.Create(ctx, f.owner, changed); !errors.Is(e, subscriptions.ErrConflict) {
		t.Fatal("changed request", e)
	}
	duplicate := in
	duplicate.RequestID = uuid.NewString()
	if _, e = repo.Create(ctx, f.owner, duplicate); !errors.Is(e, subscriptions.ErrConflict) {
		t.Fatal("duplicate source", e)
	}
	if _, e = repo.Get(ctx, f.other, first.ID); !errors.Is(e, subscriptions.ErrNotFound) {
		t.Fatal("owner read", e)
	}
	update := subscriptions.UpdateInput{Values: subscriptions.Values{Service: "Corrected", Status: "ended"}, ExpectedVersion: 1, Confirmed: true}
	if _, e = repo.Update(ctx, f.other, first.ID, update); !errors.Is(e, subscriptions.ErrNotFound) {
		t.Fatal("owner update", e)
	}
	edited, e := repo.Update(ctx, f.owner, first.ID, update)
	if e != nil || edited.Version != 2 || edited.Service != "Corrected" || edited.Sources[0].OriginalEvent.Service != "Original service" {
		t.Fatal(edited, e)
	}
	if edited.Sources[0].AccountID == nil || *edited.Sources[0].AccountID != f.account {
		t.Fatal("editing must preserve source connection", edited.Sources[0])
	}
	if _, e = repo.Update(ctx, f.owner, first.ID, update); !errors.Is(e, subscriptions.ErrConflict) {
		t.Fatal("stale version", e)
	}
	link := subscriptions.SourceInput{ExpectedVersion: 2, SourceResultID: result, SourceEventIndex: 31, Confirmed: true}
	if _, e = repo.AttachSource(ctx, f.other, first.ID, link); !errors.Is(e, subscriptions.ErrNotFound) {
		t.Fatal("owner link", e)
	}
	linked, e := repo.AttachSource(ctx, f.owner, first.ID, link)
	if e != nil || linked.Version != 3 || len(linked.Sources) != 2 {
		t.Fatal(linked, e)
	}
	if linked.Sources[1].AccountID == nil || *linked.Sources[1].AccountID != f.account {
		t.Fatal("attached source connection metadata", linked.Sources[1])
	}
	link.ExpectedVersion = 3
	if _, e = repo.AttachSource(ctx, f.owner, first.ID, link); !errors.Is(e, subscriptions.ErrConflict) {
		t.Fatal("duplicate attach", e)
	}
	link.SourceEventIndex = 32
	if _, e = repo.AttachSource(ctx, f.owner, first.ID, link); !errors.Is(e, subscriptions.ErrInvalid) {
		t.Fatal("32 out of range", e)
	}
	foreign := subscriptionInput()
	foreign.SourceResultID = &result
	foreign.SourceEventIndex = &index
	if _, e = repo.Create(ctx, f.other, foreign); !errors.Is(e, subscriptions.ErrNotFound) {
		t.Fatal("foreign result", e)
	}
	small := subscriptionResult(t, f, 1)
	out := 1
	invalid := subscriptionInput()
	invalid.SourceResultID = &small
	invalid.SourceEventIndex = &out
	if _, e = repo.Create(ctx, f.owner, invalid); !errors.Is(e, subscriptions.ErrInvalid) {
		t.Fatal("actual event length", e)
	}
	if _, e = f.runtime.pool.Exec(ctx, `UPDATE mail_processing_items SET events='[]' WHERE id=$1`, result); e == nil {
		t.Fatal("linked original event must remain immutable")
	}
	var contracts, audits int
	if e = f.runtime.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM subscriptions),(SELECT count(*) FROM subscription_audit)`).Scan(&contracts, &audits); e != nil || contracts != 1 || audits != 3 {
		t.Fatal("atomic mutation audit", contracts, audits, e)
	}
	if _, e = f.runtime.pool.Exec(ctx, `UPDATE users SET state='disabled' WHERE id=$1`, f.owner); e != nil {
		t.Fatal(e)
	}
	if _, e = repo.Create(ctx, f.owner, subscriptionInput()); !errors.Is(e, subscriptions.ErrNotFound) {
		t.Fatal("disabled writes", e)
	}
}
func TestSubscriptionsPaginationTotalsAndLiteralSearch(t *testing.T) {
	_, pool := testStore(t)
	ctx := context.Background()
	owner, other := uuid.NewString(), uuid.NewString()
	for _, id := range []string{owner, other} {
		if _, e := pool.Exec(ctx, `INSERT INTO users(id,state) VALUES($1,'active')`, id); e != nil {
			t.Fatal(e)
		}
	}
	repo := NewSubscriptionRepository(pool)
	for _, name := range []string{"Alpha", "Beta", "100% service", "under_score"} {
		in := subscriptionInput()
		in.Service = name
		if name == "Beta" {
			in.Status = "ended"
		}
		if _, e := repo.Create(ctx, owner, in); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := repo.Create(ctx, other, subscriptionInput()); e != nil {
		t.Fatal(e)
	}
	page, e := repo.List(ctx, owner, subscriptions.Filter{Status: "active", Sort: "service", Limit: 2, Offset: 1})
	if e != nil || page.Total != 3 || len(page.Subscriptions) != 2 || page.Subscriptions[0].Service != "Alpha" {
		t.Fatal(page, e)
	}
	for _, q := range []string{"%", "_"} {
		p, e := repo.List(ctx, owner, subscriptions.Filter{Query: q, Limit: 20})
		if e != nil || p.Total != 1 {
			t.Fatal(q, p, e)
		}
	}
	page, e = repo.List(ctx, owner, subscriptions.Filter{Status: "all", Limit: 2, Offset: 999})
	if e != nil || page.Total != 4 || len(page.Subscriptions) != 0 || page.Subscriptions == nil {
		t.Fatal(page, e)
	}
}
