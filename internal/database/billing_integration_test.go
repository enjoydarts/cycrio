//go:build integration

package database

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"strings"
	"sync"
	"testing"

	"github.com/enjoydarts/cycrio/internal/billing"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
)

func billingInput(kind, amount, date string) billing.CreateInput {
	return billing.CreateInput{Values: billing.Values{Kind: kind, Amount: amount, Currency: "JPY", OccurredOn: date, State: "active"}, RequestID: uuid.NewString(), Confirmed: true}
}
func TestBillingCRUDSourcesAndRequests(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	contracts := NewSubscriptionRepository(f.runtime.pool)
	r := NewBillingRepository(f.runtime.pool)
	result := subscriptionResult(t, f, 2)
	zero := 0
	sin := subscriptionInput()
	sin.SourceResultID = &result
	sin.SourceEventIndex = &zero
	s, e := contracts.Create(ctx, f.owner, sin)
	if e != nil {
		t.Fatal(e)
	}
	in := billingInput("payment", "01.00", "2024-02-29")
	in.SourceResultID = &result
	in.SourceEventIndex = &zero
	first, e := r.Create(ctx, f.owner, s.ID, in)
	if e != nil || first.Version != 1 || len(first.Sources) != 1 || first.Sources[0].OriginalEvent.Service != "Original service" {
		t.Fatal(first, e)
	}
	if _, e = r.Get(ctx, f.other, s.ID, first.ID); !errors.Is(e, billing.ErrNotFound) {
		t.Fatal("foreign owner", e)
	}
	if _, e = r.List(ctx, f.other, s.ID, billing.Filter{Limit: 20}); !errors.Is(e, billing.ErrNotFound) {
		t.Fatal("foreign list", e)
	}
	other, e := contracts.Create(ctx, f.owner, subscriptionInput())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.Create(ctx, f.owner, other.ID, in); !errors.Is(e, billing.ErrConflict) {
		t.Fatal("cross contract request", e)
	}
	alien := in
	alien.RequestID = uuid.NewString()
	if _, e = r.Create(ctx, f.owner, other.ID, alien); !errors.Is(e, billing.ErrNotFound) {
		t.Fatal("foreign contract source", e)
	}
	u := billing.UpdateInput{Values: first.Values, ExpectedVersion: 1, Confirmed: true}
	u.Amount = "2.00"
	edited, e := r.Update(ctx, f.owner, s.ID, first.ID, u)
	if e != nil || edited.Version != 2 {
		t.Fatal(edited, e)
	}
	replay, e := r.Create(ctx, f.owner, s.ID, in)
	if e != nil || replay.Version != 2 || replay.Amount != "2.00" {
		t.Fatal("current replay", replay, e)
	}
	changed := in
	changed.Amount = "1"
	if _, e = r.Create(ctx, f.owner, s.ID, changed); !errors.Is(e, billing.ErrConflict) {
		t.Fatal("request immutable", e)
	}
	if _, e = r.Update(ctx, f.owner, s.ID, first.ID, u); !errors.Is(e, billing.ErrConflict) {
		t.Fatal("CAS", e)
	}
	source := billing.SourceInput{ExpectedVersion: 2, SourceResultID: result, SourceEventIndex: 0, Confirmed: true}
	same, e := r.AttachSource(ctx, f.owner, s.ID, first.ID, source)
	if e != nil || same.Version != 2 {
		t.Fatal("attach retry", same, e)
	}
	detached, e := r.DetachSource(ctx, f.owner, s.ID, first.ID, source)
	if e != nil || detached.Version != 3 || len(detached.Sources) != 0 {
		t.Fatal(detached, e)
	}
	same, e = r.DetachSource(ctx, f.owner, s.ID, first.ID, source)
	if e != nil || same.Version != 3 {
		t.Fatal("detach retry", same, e)
	}
	source.ExpectedVersion = 3
	attached, e := r.AttachSource(ctx, f.owner, s.ID, first.ID, source)
	if e != nil || attached.Version != 4 {
		t.Fatal(attached, e)
	}
	void := billing.UpdateInput{Values: attached.Values, ExpectedVersion: 4, Confirmed: true}
	void.State = "voided"
	voided, e := r.Update(ctx, f.owner, s.ID, first.ID, void)
	if e != nil || voided.Version != 5 {
		t.Fatal(voided, e)
	}
	duplicate := in
	duplicate.RequestID = uuid.NewString()
	duplicate.OccurredOn = "2024-03-01"
	if _, e = r.Create(ctx, f.owner, s.ID, duplicate); !errors.Is(e, billing.ErrConflict) {
		t.Fatal("void source remains reserved", e)
	}
	void.Values.State = "active"
	void.ExpectedVersion = 5
	if _, e = r.Update(ctx, f.owner, s.ID, first.ID, void); e != nil {
		t.Fatal("restore", e)
	}
	page, e := r.List(ctx, f.owner, s.ID, billing.Filter{Month: "2024-02", SourceResultID: &result, SourceEventIndex: &zero, Limit: 1, Offset: 10})
	if e != nil || page.Total != 1 || len(page.Events) != 0 || page.Events == nil {
		t.Fatal(page, e)
	}
	var revisions, audits int
	if e = f.runtime.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM billing_event_revisions),(SELECT count(*) FROM billing_event_audit)`).Scan(&revisions, &audits); e != nil || revisions != 6 || audits != 6 {
		t.Fatal(revisions, audits, e)
	}
	var snapshotSources int
	if e = f.runtime.pool.QueryRow(ctx, `SELECT jsonb_array_length(snapshot->'sources') FROM billing_event_revisions WHERE entry_id=$1 AND version=1`, first.ID).Scan(&snapshotSources); e != nil || snapshotSources != 1 {
		t.Fatal(snapshotSources, e)
	}
	if _, e = f.runtime.pool.Exec(ctx, `UPDATE mail_processing_items SET events='[]' WHERE id=$1`, result); e == nil {
		t.Fatal("immutable source")
	}
	if _, e = f.runtime.pool.Exec(ctx, `UPDATE billing_event_revisions SET snapshot='{}' WHERE entry_id=$1`, first.ID); e == nil {
		t.Fatal("immutable revisions")
	}
	if _, e = f.runtime.pool.Exec(ctx, `UPDATE users SET state='disabled' WHERE id=$1`, f.owner); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Get(ctx, f.owner, s.ID, first.ID); !errors.Is(e, billing.ErrNotFound) {
		t.Fatal("disabled", e)
	}
}
func TestBillingDuplicatesAndRefundConcurrency(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	s, e := NewSubscriptionRepository(f.runtime.pool).Create(ctx, f.owner, subscriptionInput())
	if e != nil {
		t.Fatal(e)
	}
	r := NewBillingRepository(f.runtime.pool)
	in := billingInput("payment", "1.00", "2024-01-01")
	p, e := r.Create(ctx, f.owner, s.ID, in)
	if e != nil {
		t.Fatal(e)
	}
	in.RequestID = uuid.NewString()
	in.Amount = "1"
	in.DuplicateConfirmed = true
	if _, e = r.Create(ctx, f.owner, s.ID, in); !errors.Is(e, billing.ErrConflict) {
		t.Fatal("boolean bypass", e)
	}
	filter := billing.DuplicateFilter{Kind: "payment", OccurredOn: in.OccurredOn, Amount: "0001.000", Currency: "JPY", Limit: 1, Offset: 100}
	d, e := r.Duplicates(ctx, f.owner, s.ID, filter)
	if e != nil || d.Total != 1 || len(d.Events) != 0 {
		t.Fatal(d, e)
	}
	in.DuplicateToken = &d.Token
	// Both requests present the same preview; only the first can commit.
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			copy := in
			copy.RequestID = uuid.NewString()
			_, err := r.Create(ctx, f.owner, s.ID, copy)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	ok, conflicts := 0, 0
	for err := range results {
		if err == nil {
			ok++
		} else if errors.Is(err, billing.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if ok != 1 || conflicts != 1 {
		t.Fatal(ok, conflicts)
	}
	refund := billingInput("refund", "0.75", "2024-02-01")
	refund.RefundOfID = &p.ID
	var ids []string
	var mu sync.Mutex
	results = make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			copy := refund
			copy.RequestID = uuid.NewString()
			if i == 1 {
				copy.OccurredOn = "2024-02-02"
			}
			entry, err := r.Create(ctx, f.owner, s.ID, copy)
			if err == nil {
				mu.Lock()
				ids = append(ids, entry.ID)
				mu.Unlock()
			}
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)
	ok, conflicts = 0, 0
	for err := range results {
		if err == nil {
			ok++
		} else if errors.Is(err, billing.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if ok != 1 || conflicts != 1 {
		t.Fatal("overrefund", ok, conflicts)
	}
	for _, change := range []func(*billing.Values){func(v *billing.Values) { v.State = "voided" }, func(v *billing.Values) { v.Amount = "0.5" }, func(v *billing.Values) { v.Currency = "USD" }, func(v *billing.Values) { v.Kind = "invoice" }} {
		v := p.Values
		change(&v)
		if _, e = r.Update(ctx, f.owner, s.ID, p.ID, billing.UpdateInput{Values: v, ExpectedVersion: 1, Confirmed: true}); !errors.Is(e, billing.ErrConflict) {
			t.Fatal("parent guard", v, e)
		}
	}
	child, e := r.Get(ctx, f.owner, s.ID, ids[0])
	if e != nil {
		t.Fatal(e)
	}
	child.State = "voided"
	if _, e = r.Update(ctx, f.owner, s.ID, child.ID, billing.UpdateInput{Values: child.Values, ExpectedVersion: 1, Confirmed: true}); e != nil {
		t.Fatal(e)
	}
	p.State = "voided"
	if _, e = r.Update(ctx, f.owner, s.ID, p.ID, billing.UpdateInput{Values: p.Values, ExpectedVersion: 1, Confirmed: true}); e != nil {
		t.Fatal("void after refunds", e)
	}
	child.State = "active"
	if _, e = r.Update(ctx, f.owner, s.ID, child.ID, billing.UpdateInput{Values: child.Values, ExpectedVersion: 2, Confirmed: true}); !errors.Is(e, billing.ErrConflict) {
		t.Fatal("restore refund to void payment", e)
	}
}
func TestBillingMigrationNineDownUpPreservesContracts(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	result := subscriptionResult(t, f, 1)
	zero := 0
	in := subscriptionInput()
	in.SourceResultID = &result
	in.SourceEventIndex = &zero
	s, e := NewSubscriptionRepository(f.runtime.pool).Create(ctx, f.owner, in)
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
	down, e := p.DownTo(ctx, 8)
	if e != nil || len(down) != 7 || down[0].Source.Version != 15 || down[1].Source.Version != 14 || down[2].Source.Version != 13 || down[3].Source.Version != 12 || down[4].Source.Version != 11 || down[5].Source.Version != 10 || down[6].Source.Version != 9 {
		t.Fatal(down, e)
	}
	var preservedV8 string
	if e = f.runtime.pool.QueryRow(ctx, `SELECT service FROM subscriptions WHERE owner_id=$1 AND id=$2`, f.owner, s.ID).Scan(&preservedV8); e != nil || preservedV8 != s.Service {
		t.Fatal("v8 data", e)
	}
	if _, e = p.Up(ctx); e != nil {
		t.Fatal(e)
	}
	preserved, e := NewSubscriptionRepository(f.runtime.pool).Create(ctx, f.owner, in)
	if e != nil || preserved.ID != s.ID || preserved.Version != s.Version || len(preserved.Sources) != 1 || preserved.Sources[0].OriginalEvent.Service != "Original service" {
		t.Fatal("v8 request/source preserved", preserved, e)
	}
	var version, audits int
	if e = f.runtime.pool.QueryRow(ctx, `SELECT (SELECT max(version_id) FROM goose_db_version WHERE is_applied),(SELECT count(*) FROM subscription_audit)`).Scan(&version, &audits); e != nil || version != 15 || audits != 1 {
		t.Fatal(version, audits, e)
	}
	if _, e = NewBillingRepository(f.runtime.pool).Create(ctx, f.owner, s.ID, billingInput("invoice", "0", "2024-01-01")); e != nil {
		t.Fatal(e)
	}
}
func TestBillingSourceConcurrencyAndAuditRollback(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	contracts := NewSubscriptionRepository(f.runtime.pool)
	result := subscriptionResult(t, f, 1)
	zero := 0
	sin := subscriptionInput()
	sin.SourceResultID = &result
	sin.SourceEventIndex = &zero
	s, e := contracts.Create(ctx, f.owner, sin)
	if e != nil {
		t.Fatal(e)
	}
	r := NewBillingRepository(f.runtime.pool)
	a, e := r.Create(ctx, f.owner, s.ID, billingInput("payment", "1", "2024-01-01"))
	if e != nil {
		t.Fatal(e)
	}
	b, e := r.Create(ctx, f.owner, s.ID, billingInput("payment", "2", "2024-01-01"))
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []string{a.ID, b.ID} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, err := r.AttachSource(ctx, f.owner, s.ID, id, billing.SourceInput{ExpectedVersion: 1, SourceResultID: result, SourceEventIndex: 0, Confirmed: true})
			results <- err
		}(id)
	}
	wg.Wait()
	close(results)
	ok, conflicts := 0, 0
	for err := range results {
		if err == nil {
			ok++
		} else if errors.Is(err, billing.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if ok != 1 || conflicts != 1 {
		t.Fatal(ok, conflicts)
	}
	// Failure at the final audit insert must roll back facts, sources and revision.
	if _, e = f.runtime.pool.Exec(ctx, `CREATE FUNCTION fail_billing_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced audit failure'; END $$; CREATE TRIGGER fail_billing_audit BEFORE INSERT ON billing_event_audit FOR EACH ROW EXECUTE FUNCTION fail_billing_audit()`); e != nil {
		t.Fatal(e)
	}
	current, e := r.Get(ctx, f.owner, s.ID, a.ID)
	if e != nil {
		t.Fatal(e)
	}
	updated := current.Values
	updated.Amount = "9"
	if _, e = r.Update(ctx, f.owner, s.ID, a.ID, billing.UpdateInput{Values: updated, ExpectedVersion: current.Version, Confirmed: true}); !errors.Is(e, billing.ErrUnavailable) {
		t.Fatal("audit failure", e)
	}
	after, e := r.Get(ctx, f.owner, s.ID, a.ID)
	if e != nil || after.Version != current.Version || after.Amount != current.Amount {
		t.Fatal(after, e)
	}
	if _, e = r.Create(ctx, f.owner, s.ID, billingInput("invoice", "10", "2024-01-01")); !errors.Is(e, billing.ErrUnavailable) {
		t.Fatal(e)
	}
	var entries, revisions, audits int
	if e = f.runtime.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM billing_events),(SELECT count(*) FROM billing_event_revisions),(SELECT count(*) FROM billing_event_audit)`).Scan(&entries, &revisions, &audits); e != nil || entries != 2 || revisions != 3 || audits != 3 {
		t.Fatal(entries, revisions, audits, e)
	}
}
func TestBillingRefundOwnershipBoundsAndKinds(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	contracts := NewSubscriptionRepository(f.runtime.pool)
	s, e := contracts.Create(ctx, f.owner, subscriptionInput())
	if e != nil {
		t.Fatal(e)
	}
	other, e := contracts.Create(ctx, f.owner, subscriptionInput())
	if e != nil {
		t.Fatal(e)
	}
	foreign, e := contracts.Create(ctx, f.other, subscriptionInput())
	if e != nil {
		t.Fatal(e)
	}
	r := NewBillingRepository(f.runtime.pool)
	p, e := r.Create(ctx, f.owner, s.ID, billingInput("payment", "1.00", "2024-01-01"))
	if e != nil {
		t.Fatal(e)
	}
	// A same-date invoice is a separate financial fact, never a payment duplicate.
	if _, e = r.Create(ctx, f.owner, s.ID, billingInput("invoice", "1", "2024-01-01")); e != nil {
		t.Fatal(e)
	}
	for _, o := range []struct{ owner, contract string }{{f.owner, other.ID}, {f.other, foreign.ID}} {
		refund := billingInput("refund", "0", "2024-01-01")
		refund.RefundOfID = &p.ID
		if _, e = r.Create(ctx, o.owner, o.contract, refund); !errors.Is(e, billing.ErrNotFound) {
			t.Fatal("foreign parent", e)
		}
	}
	invalid := billingInput("refund", "0.01", "2024-01-01")
	invalid.RefundOfID = &p.ID
	invalid.Currency = "USD"
	if _, e = r.Create(ctx, f.owner, s.ID, invalid); !errors.Is(e, billing.ErrConflict) {
		t.Fatal("refund currency", e)
	}
	refund := billingInput("refund", "0.750000000000", "2024-01-01")
	refund.RefundOfID = &p.ID
	child, e := r.Create(ctx, f.owner, s.ID, refund)
	if e != nil {
		t.Fatal(e)
	}
	child.Amount = "1"
	edited, e := r.Update(ctx, f.owner, s.ID, child.ID, billing.UpdateInput{Values: child.Values, ExpectedVersion: 1, Confirmed: true})
	if e != nil {
		t.Fatal("exclude self sum", e)
	}
	overflow := refund
	overflow.RequestID = uuid.NewString()
	overflow.OccurredOn = "2024-01-02"
	overflow.Amount = "0.000000000001"
	if _, e = r.Create(ctx, f.owner, s.ID, overflow); !errors.Is(e, billing.ErrConflict) {
		t.Fatal("exact upper bound", e)
	}
	edited.Amount = "0"
	if _, e = r.Update(ctx, f.owner, s.ID, child.ID, billing.UpdateInput{Values: edited.Values, ExpectedVersion: 2, Confirmed: true}); e != nil {
		t.Fatal(e)
	}
	p.State = "voided"
	if _, e = r.Update(ctx, f.owner, s.ID, p.ID, billing.UpdateInput{Values: p.Values, ExpectedVersion: 1, Confirmed: true}); !errors.Is(e, billing.ErrConflict) {
		t.Fatal("zero refund blocks void", e)
	}
}
func TestBillingUUIDCaseDoesNotIncludeSelfInDuplicateOrRefundTotal(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	s, e := NewSubscriptionRepository(f.runtime.pool).Create(ctx, f.owner, subscriptionInput())
	if e != nil {
		t.Fatal(e)
	}
	r := NewBillingRepository(f.runtime.pool)
	p, e := r.Create(ctx, f.owner, s.ID, billingInput("payment", "1", "2024-01-01"))
	if e != nil {
		t.Fatal(e)
	}
	preview, e := r.Duplicates(ctx, f.owner, strings.ToUpper(s.ID), billing.DuplicateFilter{Kind: p.Kind, OccurredOn: p.OccurredOn, Amount: p.Amount, Currency: p.Currency, ExcludeID: strings.ToUpper(p.ID), Limit: 20})
	if e != nil || preview.Total != 0 {
		t.Fatal("case insensitive exclusion", preview, e)
	}
	refund := billingInput("refund", "1", "2024-01-02")
	refund.RefundOfID = &p.ID
	child, e := r.Create(ctx, f.owner, s.ID, refund)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.Update(ctx, f.owner, strings.ToUpper(s.ID), strings.ToUpper(child.ID), billing.UpdateInput{Values: child.Values, ExpectedVersion: 1, Confirmed: true}); e != nil {
		t.Fatal("refund self total", e)
	}
}
func TestBillingVoidedRefundRequiresPaymentAndCurrency(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	s, e := NewSubscriptionRepository(f.runtime.pool).Create(ctx, f.owner, subscriptionInput())
	if e != nil {
		t.Fatal(e)
	}
	r := NewBillingRepository(f.runtime.pool)
	for _, kind := range []string{"invoice", "payment"} {
		in := billingInput(kind, "10", "2024-01-01")
		if kind == "payment" {
			in.State = "voided"
		}
		p, e := r.Create(ctx, f.owner, s.ID, in)
		if e != nil {
			t.Fatal(e)
		}
		refund := billingInput("refund", "1", "2024-02-01")
		refund.State = "voided"
		refund.RefundOfID = &p.ID
		if _, e = r.Create(ctx, f.owner, s.ID, refund); !errors.Is(e, billing.ErrConflict) {
			t.Fatal("voided refund still needs active payment", kind, e)
		}
	}
	p, e := r.Create(ctx, f.owner, s.ID, billingInput("payment", "10", "2024-01-02"))
	if e != nil {
		t.Fatal(e)
	}
	refund := billingInput("refund", "1", "2024-02-01")
	refund.State = "voided"
	refund.RefundOfID = &p.ID
	refund.Currency = "USD"
	if _, e = r.Create(ctx, f.owner, s.ID, refund); !errors.Is(e, billing.ErrConflict) {
		t.Fatal("voided currency", e)
	}
	refund.Currency = "JPY"
	child, e := r.Create(ctx, f.owner, s.ID, refund)
	if e != nil {
		t.Fatal(e)
	}
	p.State = "voided"
	if _, e = r.Update(ctx, f.owner, s.ID, p.ID, billing.UpdateInput{Values: p.Values, ExpectedVersion: 1, Confirmed: true}); e != nil {
		t.Fatal(e)
	}
	child.Amount = "2"
	if _, e = r.Update(ctx, f.owner, s.ID, child.ID, billing.UpdateInput{Values: child.Values, ExpectedVersion: 1, Confirmed: true}); e != nil {
		t.Fatal("voided history can remain on void parent", e)
	}
}
func TestBillingDuplicateTokenContainsAllVersionsAndPreviewNeverWrites(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	s, e := NewSubscriptionRepository(f.runtime.pool).Create(ctx, f.owner, subscriptionInput())
	if e != nil {
		t.Fatal(e)
	}
	r := NewBillingRepository(f.runtime.pool)
	in := billingInput("payment", "1", "2024-01-01")
	first, e := r.Create(ctx, f.owner, s.ID, in)
	if e != nil {
		t.Fatal(e)
	}
	filter := billing.DuplicateFilter{Kind: in.Kind, OccurredOn: in.OccurredOn, Amount: "1.00", Currency: in.Currency, Limit: 1}
	preview, e := r.Duplicates(ctx, f.owner, s.ID, filter)
	if e != nil {
		t.Fatal(e)
	}
	in.RequestID = uuid.NewString()
	in.DuplicateConfirmed = true
	in.DuplicateToken = &preview.Token
	second, e := r.Create(ctx, f.owner, s.ID, in)
	if e != nil {
		t.Fatal(e)
	}
	preview, e = r.Duplicates(ctx, f.owner, s.ID, filter)
	if e != nil || preview.Total != 2 || len(preview.Events) != 1 {
		t.Fatal(preview, e)
	}
	filter.Offset = 99
	all, e := r.Duplicates(ctx, f.owner, s.ID, filter)
	if e != nil || all.Token != preview.Token {
		t.Fatal("pagination independent token", all, e)
	}
	edited := first
	if preview.Events[0].ID == first.ID {
		edited = second
	}
	editFilter := filter
	editFilter.Offset = 0
	editFilter.ExcludeID = edited.ID
	editPreview, e := r.Duplicates(ctx, f.owner, s.ID, editFilter)
	if e != nil {
		t.Fatal(e)
	}
	edited.Amount = "01.000"
	if _, e = r.Update(ctx, f.owner, s.ID, edited.ID, billing.UpdateInput{Values: edited.Values, ExpectedVersion: 1, Confirmed: true, DuplicateConfirmed: true, DuplicateToken: &editPreview.Token}); e != nil {
		t.Fatal(e)
	}
	in.RequestID = uuid.NewString()
	in.DuplicateToken = &preview.Token
	if _, e = r.Create(ctx, f.owner, s.ID, in); !errors.Is(e, billing.ErrConflict) {
		t.Fatal("candidate version outside visible page invalidates token", e)
	}
	var events, revisions, audits int
	if e = f.runtime.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM billing_events),(SELECT count(*) FROM billing_event_revisions),(SELECT count(*) FROM billing_event_audit)`).Scan(&events, &revisions, &audits); e != nil || events != 2 || revisions != 3 || audits != 3 {
		t.Fatal("preview and rejection never write", events, revisions, audits, e)
	}
}
