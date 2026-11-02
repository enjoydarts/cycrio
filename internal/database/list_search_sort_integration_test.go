//go:build integration

package database

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/google/uuid"
)

func TestCandidateSearchSortPagination(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewSubscriptionRepository(f.runtime.pool)
	literal := "日本語%_\\"
	result := func(date string, events ...ai.Event) string {
		for i := range events {
			events[i].EventType = "renewal"
		}
		f.add(t, 1)
		in := f.in
		in.RequestID = uuid.NewString()
		in.MaxMessages = 1
		if _, e := f.repo.Request(ctx, f.owner, f.account, in); e != nil {
			t.Fatal(e)
		}
		lease, work := f.start(t)
		var received *time.Time
		if date != "" {
			v, e := time.Parse(time.RFC3339, date)
			if e != nil {
				t.Fatal(e)
			}
			received = &v
		}
		label := "subscription_related"
		if e := f.repo.Finish(ctx, lease, mailprocessing.Completion{State: "succeeded", Classification: &label, Events: events, ReceivedAt: received}); e != nil {
			t.Fatal(e)
		}
		return work.AttemptID
	}
	a := result("2026-01-01T00:00:00Z", ai.Event{Service: "Alpha", Plan: &literal}, ai.Event{Service: "Alpha"})
	b := result("2026-02-01T00:00:00Z", ai.Event{Service: "Zulu"})
	c := result("", ai.Event{Service: "Middle"})
	key := func(id string, index int) string {
		if index == 0 {
			return id + "/0"
		}
		return id + "/1"
	}
	for _, tc := range []struct {
		sort string
		want []string
	}{
		{"", []string{key(b, 0), key(a, 0), key(a, 1), key(c, 0)}},
		{"received_desc", []string{key(b, 0), key(a, 0), key(a, 1), key(c, 0)}},
		{"received_asc", []string{key(a, 0), key(a, 1), key(b, 0), key(c, 0)}},
		{"service", []string{key(a, 0), key(a, 1), key(c, 0), key(b, 0)}},
		{"service_desc", []string{key(b, 0), key(c, 0), key(a, 0), key(a, 1)}},
	} {
		var got []string
		for offset := 0; offset < 4; offset++ {
			p, e := r.Candidates(ctx, f.owner, subscriptions.AnalysisFilter{Sort: tc.sort, Limit: 1, Offset: offset, AccountID: f.account})
			if e != nil || p.Total != 4 || len(p.Candidates) != 1 {
				t.Fatalf("%s page %d: %+v %v", tc.sort, offset, p, e)
			}
			got = append(got, key(p.Candidates[0].ResultID, p.Candidates[0].EventIndex))
		}
		if !slices.Equal(got, tc.want) {
			t.Fatalf("%s got %v want %v", tc.sort, got, tc.want)
		}
	}
	for _, query := range []string{literal, "%", "_", "\\", " 日本語%_\\ "} {
		p, e := r.Candidates(ctx, f.owner, subscriptions.AnalysisFilter{Query: query, Limit: 1})
		if e != nil || p.Total != 1 || len(p.Candidates) != 1 || p.Candidates[0].ResultID != a || p.Candidates[0].EventIndex != 0 {
			t.Fatalf("literal %q: %+v %v", query, p, e)
		}
	}
	p, e := r.Candidates(ctx, f.owner, subscriptions.AnalysisFilter{Query: "ALPHA", Limit: 1, Offset: 1})
	if e != nil || p.Total != 2 || len(p.Candidates) != 1 || p.Candidates[0].EventIndex != 1 {
		t.Fatal(p, e)
	}
	p, e = r.Candidates(ctx, f.other, subscriptions.AnalysisFilter{Limit: 20, Query: "Alpha"})
	if e != nil || p.Total != 0 {
		t.Fatal("owner leaked", p, e)
	}
	if _, e = r.Candidates(ctx, f.other, subscriptions.AnalysisFilter{Limit: 20, AccountID: f.account}); !errors.Is(e, subscriptions.ErrNotFound) {
		t.Fatal("foreign account", e)
	}
	// Approval and dismissal remain excluded even when they match the query.
	in := subscriptionInput()
	in.SourceResultID = &a
	in.SourceEventIndex = autoPtr(0)
	if _, e = r.Create(ctx, f.owner, in); e != nil {
		t.Fatal(e)
	}
	if e = r.DismissCandidate(ctx, f.owner, subscriptions.DismissInput{SourceResultID: a, SourceEventIndex: 1, Confirmed: true}); e != nil {
		t.Fatal(e)
	}
	p, e = r.Candidates(ctx, f.owner, subscriptions.AnalysisFilter{Query: "Alpha", Limit: 20})
	if e != nil || p.Total != 0 || len(p.Candidates) != 0 {
		t.Fatal("decided candidates reappeared", p, e)
	}
	if _, e = r.Candidates(ctx, f.owner, subscriptions.AnalysisFilter{Sort: "updated", Limit: 20}); !errors.Is(e, subscriptions.ErrInvalid) {
		t.Fatal(e)
	}
	emptyAccount := uuid.NewString()
	if _, e = f.runtime.pool.Exec(ctx, `INSERT INTO mail_accounts(id,owner_id,label,state,version,credential_version) VALUES($1,$2,'Empty','disconnected',1,1)`, emptyAccount, f.owner); e != nil {
		t.Fatal(e)
	}
	p, e = r.Candidates(ctx, f.owner, subscriptions.AnalysisFilter{AccountID: emptyAccount, Query: "Zulu", Sort: "service_desc", Limit: 20})
	if e != nil || p.Total != 0 || len(p.Candidates) != 0 {
		t.Fatal("account filter leaked candidates", p, e)
	}
	// Equal timestamps across independent results use result ID, then event index.
	tied := []string{result("2026-03-01T00:00:00Z", ai.Event{Service: "Tied", Plan: autoPtr("First")}), result("2026-03-01T00:00:00Z", ai.Event{Service: "Tied", Plan: autoPtr("Second")})}
	slices.Sort(tied)
	for _, sort := range []string{"received_desc", "received_asc", "service", "service_desc"} {
		for offset := 0; offset < 2; offset++ {
			p, e = r.Candidates(ctx, f.owner, subscriptions.AnalysisFilter{Query: "Tied", Sort: sort, Limit: 1, Offset: offset})
			if e != nil || p.Total != 2 || len(p.Candidates) != 1 || p.Candidates[0].ResultID != tied[offset] {
				t.Fatal("unstable equal-time pagination", sort, offset, p, e)
			}
		}
	}
}

func TestSubscriptionSearchSortPagination(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewSubscriptionRepository(f.runtime.pool)
	var ids []string
	literal := "日本語%_\\"
	for _, spec := range []struct {
		service, date string
		plan          *string
	}{{"Alpha", "2026-01-01", &literal}, {"Alpha", "2026-01-01", nil}, {"Zulu", "2026-02-01", nil}, {"Middle", "", nil}} {
		in := subscriptionInput()
		in.Service = spec.service
		in.Plan = spec.plan
		if spec.date != "" {
			in.NextBillingDate = &spec.date
		}
		s, e := r.Create(ctx, f.owner, in)
		if e != nil {
			t.Fatal(e)
		}
		ids = append(ids, s.ID)
	}
	// Tied dates and service names must have stable ID ordering across pages.
	slices.Sort(ids[:2])
	if _, e := f.runtime.pool.Exec(ctx, `UPDATE subscriptions SET updated_at='2026-01-01T00:00:00Z' WHERE owner_id=$1`, f.owner); e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		sort string
		want []string
	}{
		{"", []string{ids[0], ids[1], ids[2], ids[3]}},
		{"next_billing", []string{ids[0], ids[1], ids[2], ids[3]}},
		{"next_billing_desc", []string{ids[2], ids[0], ids[1], ids[3]}},
		{"service", []string{ids[0], ids[1], ids[3], ids[2]}},
		{"service_desc", []string{ids[2], ids[3], ids[0], ids[1]}},
		{"updated", func() []string { v := slices.Clone(ids); slices.Sort(v); slices.Reverse(v); return v }()},
		{"updated_asc", func() []string { v := slices.Clone(ids); slices.Sort(v); return v }()},
	} {
		var got []string
		for offset := 0; offset < 4; offset++ {
			p, e := r.List(ctx, f.owner, subscriptions.Filter{Sort: tc.sort, Limit: 1, Offset: offset})
			if e != nil || p.Total != 4 || len(p.Subscriptions) != 1 {
				t.Fatalf("%s page %d: %+v %v", tc.sort, offset, p, e)
			}
			got = append(got, p.Subscriptions[0].ID)
		}
		if !slices.Equal(got, tc.want) {
			t.Fatalf("%s got %v want %v", tc.sort, got, tc.want)
		}
	}
	for _, query := range []string{literal, "%", "_", "\\", " 日本語%_\\ "} {
		p, e := r.List(ctx, f.owner, subscriptions.Filter{Query: query, Sort: "service_desc", Limit: 1})
		if e != nil || p.Total != 1 || len(p.Subscriptions) != 1 || p.Subscriptions[0].Plan == nil || *p.Subscriptions[0].Plan != literal {
			t.Fatal(query, p, e)
		}
	}
	p, e := r.List(ctx, f.owner, subscriptions.Filter{Query: "alpha", Limit: 1, Offset: 1})
	if e != nil || p.Total != 2 || len(p.Subscriptions) != 1 {
		t.Fatal(p, e)
	}
	foreign := subscriptionInput()
	foreign.Service = "Alpha"
	foreign.RequestID = uuid.NewString()
	if _, e = r.Create(ctx, f.other, foreign); e != nil {
		t.Fatal(e)
	}
	p, e = r.List(ctx, f.owner, subscriptions.Filter{Query: "alpha", Limit: 20})
	if e != nil || p.Total != 2 {
		t.Fatal("foreign owner leaked", p, e)
	}
	p, e = r.List(ctx, f.owner, subscriptions.Filter{Query: "missing", Limit: 20})
	if e != nil || p.Total != 0 || len(p.Subscriptions) != 0 {
		t.Fatal(p, e)
	}
}
