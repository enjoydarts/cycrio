//go:build integration

package database

import (
	"context"
	"database/sql"
	"io/fs"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/autoregistration"
	"github.com/pressly/goose/v3"
)

func TestSubscriptionAutomationMigrationFourteenPreservesThirteen(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	repo := NewSubscriptionRepository(f.runtime.pool)
	result := subscriptionResult(t, f, 1)
	index := 0
	in := subscriptionInput()
	in.SourceResultID = &result
	in.SourceEventIndex = &index
	s, err := repo.Create(ctx, f.owner, in)
	if err != nil {
		t.Fatal(err)
	}
	const snapshot = `SELECT jsonb_build_array((SELECT jsonb_agg(to_jsonb(x)-'provenance'-'auto_last_event_date'-'auto_last_event_date_basis' ORDER BY id) FROM subscriptions x),(SELECT jsonb_agg(to_jsonb(x)-'provenance'-'auto_last_event_date'-'auto_last_event_date_basis' ORDER BY result_id,event_index) FROM subscription_sources x),(SELECT jsonb_agg(x ORDER BY id) FROM subscription_audit x),(SELECT jsonb_agg(x ORDER BY id) FROM mail_processing_items x),(SELECT jsonb_agg(x ORDER BY id) FROM ai_calls x))`
	var before, after string
	if err = f.runtime.pool.QueryRow(ctx, snapshot).Scan(&before); err != nil {
		t.Fatal(err)
	}
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
	provider, err := goose.NewProvider(goose.DialectPostgres, db, files)
	if err != nil {
		t.Fatal(err)
	}
	down, err := provider.DownTo(ctx, 13)
	if err != nil || len(down) != 3 || down[0].Source.Version != 16 || down[1].Source.Version != 15 || down[2].Source.Version != 14 {
		t.Fatal(down, err)
	}
	if err = f.runtime.pool.QueryRow(ctx, snapshot).Scan(&after); err != nil || before != after {
		t.Fatal("v13 original data changed on down14", err)
	}
	if _, err = f.runtime.pool.Exec(ctx, `UPDATE subscriptions SET status='unknown' WHERE id=$1`, s.ID); err == nil {
		t.Fatal("down14 must restore exact v13 status check")
	}
	if _, err = f.runtime.pool.Exec(ctx, `UPDATE subscription_audit SET action='auto_created' WHERE subscription_id=$1`, s.ID); err == nil {
		t.Fatal("down14 must restore exact v13 audit check")
	}
	up, err := provider.Up(ctx)
	if err != nil || len(up) != 3 || up[0].Source.Version != 14 || up[1].Source.Version != 15 || up[2].Source.Version != 16 {
		t.Fatal(up, err)
	}
	if err = f.runtime.pool.QueryRow(ctx, snapshot).Scan(&after); err != nil || before != after {
		t.Fatal("v13 original data changed on up14", err)
	}
	preserved, err := repo.Create(ctx, f.owner, in)
	if err != nil || preserved.ID != s.ID || preserved.Provenance != "owner_confirmed" {
		t.Fatal(preserved, err)
	}
	var version int
	if err = f.runtime.pool.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil || version != 16 {
		t.Fatal(version, err)
	}
}

func TestSubscriptionAutomationMigrationDownRefusesToDiscardNewData(t *testing.T) {
	for _, mode := range []string{"preference", "automatic"} {
		t.Run(mode, func(t *testing.T) {
			f := processingSetup(t)
			ctx := context.Background()
			r := NewSubscriptionRepository(f.runtime.pool)
			if _, err := r.SaveAutomation(ctx, f.owner, autoregistration.PreferenceInput{Enabled: mode == "automatic"}); err != nil {
				t.Fatal(err)
			}
			if mode == "automatic" {
				subscriptionResult(t, f, 1)
				if _, err := r.RegisterDue(ctx, 20); err != nil {
					t.Fatal(err)
				}
			}
			const snapshot = `SELECT jsonb_build_array((SELECT jsonb_agg(x ORDER BY id) FROM subscriptions x),(SELECT jsonb_agg(x ORDER BY result_id,event_index) FROM subscription_sources x),(SELECT jsonb_agg(x ORDER BY id) FROM subscription_audit x),(SELECT jsonb_agg(x ORDER BY owner_id) FROM subscription_automation_preferences x),(SELECT jsonb_agg(x ORDER BY id) FROM subscription_automation_preference_audit x),(SELECT jsonb_agg(x ORDER BY result_id) FROM subscription_automation_results x),(SELECT jsonb_agg(x ORDER BY result_id,event_index) FROM subscription_automation_events x))`
			var before, after string
			if err := f.runtime.pool.QueryRow(ctx, snapshot).Scan(&before); err != nil {
				t.Fatal(err)
			}
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
			provider, err := goose.NewProvider(goose.DialectPostgres, db, files)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = provider.DownTo(ctx, 13); err == nil || !strings.Contains(err.Error(), "subscription automation data prevents downgrade") {
				t.Fatal("must refuse destructive downgrade", err)
			}
			if err = f.runtime.pool.QueryRow(ctx, snapshot).Scan(&after); err != nil || before != after {
				t.Fatal("failed down must preserve all v14 data", err)
			}
			var version int
			if err = f.runtime.pool.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil || version != 14 {
				t.Fatal("failed down must retain v14", version, err)
			}
		})
	}
}
