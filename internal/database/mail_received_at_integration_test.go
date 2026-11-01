//go:build integration

package database

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/pressly/goose/v3"
)

// A receipt instant is source evidence, independent of the processing clock.
func TestMailReceivedAtPersistsAcrossPublicReads(t *testing.T) {
	for _, tc := range []struct {
		name     string
		received *time.Time
	}{
		{name: "unknown"},
		{name: "UTC date boundary", received: receivedInstant("2026-11-01T00:01:00+09:00")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := processingSetup(t)
			ctx := context.Background()
			lease, works := processingBatch(t, f, 1)
			label := "subscription_related"
			completion := mailprocessing.Completion{State: "succeeded", Classification: &label, Events: []ai.Event{{Service: "Synthetic service", EventType: "renewal"}}, ReceivedAt: tc.received}
			if err := f.repo.Finish(ctx, batchLease(lease, works[0]), completion); err != nil {
				t.Fatal(err)
			}
			// A replay must not replace already completed source evidence.
			completion.ReceivedAt = receivedInstant("2030-01-01T00:00:00Z")
			if err := f.repo.Finish(ctx, batchLease(lease, works[0]), completion); err != nil {
				t.Fatal(err)
			}
			check := func(name string, got *time.Time) {
				t.Helper()
				if tc.received == nil {
					if got != nil {
						t.Fatalf("%s invented an unknown received instant", name)
					}
					return
				}
				if got == nil || got.UTC().Format(time.RFC3339) != "2026-10-31T15:01:00Z" {
					t.Fatalf("%s lost receipt instant: %v", name, got)
				}
			}
			var stored *time.Time
			if err := f.runtime.pool.QueryRow(ctx, `SELECT received_at FROM mail_processing_items WHERE id=$1`, works[0].AttemptID).Scan(&stored); err != nil {
				t.Fatal(err)
			}
			check("database", stored)
			results, err := f.repo.Results(ctx, f.owner, f.account)
			if err != nil || len(results) != 1 {
				t.Fatal("processing results", err)
			}
			check("processing result", results[0].ReceivedAt)
			if tc.received != nil && results[0].FinishedAt.Equal(*tc.received) {
				t.Fatal("receipt and processing clocks were conflated")
			}
			repo := NewSubscriptionRepository(f.runtime.pool)
			filter := subscriptions.AnalysisFilter{Limit: 10}
			candidates, err := repo.Candidates(ctx, f.owner, filter)
			if err != nil || len(candidates.Candidates) != 1 {
				t.Fatal("candidates", err)
			}
			check("candidate", candidates.Candidates[0].ReceivedAt)
			feed, err := repo.AnalysisResults(ctx, f.owner, filter)
			if err != nil || len(feed.Results) != 1 {
				t.Fatal("feed", err)
			}
			check("analysis feed", feed.Results[0].ReceivedAt)
			detail, err := repo.AnalysisMessage(ctx, f.owner, works[0].MessageID)
			if err != nil || detail.Result == nil {
				t.Fatal("detail", err)
			}
			check("analysis detail", detail.Result.ReceivedAt)
			in := subscriptionInput()
			index := 0
			in.SourceResultID, in.SourceEventIndex = &works[0].AttemptID, &index
			created, err := repo.Create(ctx, f.owner, in)
			if err != nil || len(created.Sources) != 1 {
				t.Fatal("source", err)
			}
			check("registered source", created.Sources[0].ReceivedAt)
			if _, err := repo.Get(ctx, f.other, created.ID); !errors.Is(err, subscriptions.ErrNotFound) {
				t.Fatal("foreign source accessible", err)
			}
			foreign, err := repo.AnalysisResults(ctx, f.other, filter)
			if err != nil || len(foreign.Results) != 0 {
				t.Fatal("foreign results accessible", err)
			}
			foreignCandidates, err := repo.Candidates(ctx, f.other, filter)
			if err != nil || len(foreignCandidates.Candidates) != 0 {
				t.Fatal("foreign candidates accessible", err)
			}
			if _, err := f.runtime.pool.Exec(ctx, `UPDATE mail_processing_items SET received_at='2030-01-01T00:00:00Z' WHERE id=$1`, works[0].AttemptID); err == nil {
				t.Fatal("registered source receipt was mutable")
			}
		})
	}
}

func receivedInstant(value string) *time.Time {
	instant, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return &instant
}

func TestMailReceivedAtMigrationPreservesLegacyAndRefusesLoss(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	id := subscriptionResult(t, f, 1)
	db, err := sql.Open("pgx", f.runtime.pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	files, err := fs.Sub(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	p, err := goose.NewProvider(goose.DialectPostgres, db, files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.DownTo(ctx, 24); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatal(err)
	}
	var received *time.Time
	var version int
	if err := f.runtime.pool.QueryRow(ctx, `SELECT received_at,(SELECT max(version_id) FROM goose_db_version WHERE is_applied) FROM mail_processing_items WHERE id=$1`, id).Scan(&received, &version); err != nil || received != nil || version != latestMigrationVersion {
		t.Fatal("upgrade fabricated historical receipt", received, version, err)
	}
	lease, works := processingBatch(t, f, 1)
	label := "not_related"
	if err := f.repo.Finish(ctx, batchLease(lease, works[0]), mailprocessing.Completion{State: "not_related", Classification: &label, ReceivedAt: receivedInstant("2026-10-31T15:01:00Z")}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.DownTo(ctx, 24); err == nil {
		t.Fatal("downgrade discarded known receipt evidence")
	}
}
