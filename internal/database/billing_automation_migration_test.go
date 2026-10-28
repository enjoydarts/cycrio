//go:build integration

package database

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func billingLegacyRecords(t *testing.T, pool *pgxpool.Pool) map[string]string {
	t.Helper()
	ctx := context.Background()
	rows, err := pool.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname=current_schema() AND tablename NOT LIKE 'integration_%' AND tablename NOT IN ('mail_oauth_pending','mail_message_folder_memberships') AND tablename NOT IN ('billing_automation_work','billing_automation_events','billing_source_detachments','goose_db_version') ORDER BY tablename`)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	rows.Close()
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	out := map[string]string{}
	for _, name := range names {
		drop := mailConnectionMigrationColumns(name)
		if name == "billing_events" || name == "billing_event_sources" {
			drop = append(drop, "provenance")
		}
		var raw string
		if err = pool.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(row ORDER BY row::text),'[]')::text FROM (SELECT to_jsonb(t)-$1::text[] AS row FROM `+pgx.Identifier{name}.Sanitize()+` t WHERE COALESCE(to_jsonb(t)->>'provider','') NOT IN ('clerk_webhook','resend_webhook','gmail_oauth','outlook_oauth'))x`, drop).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		out[name] = raw
	}
	return out
}
func TestBillingAutomationMigrationSeventeenPreservesEverySixteenRecord(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	contract, result := financialContract(t, f, financialEvent("payment", "1", "2024-01-01"))
	repo := NewBillingRepository(f.runtime.pool)
	in := billingInput("payment", "1", "2024-01-01")
	in.SourceResultID = &result
	in.SourceEventIndex = autoPtr(0)
	entry, err := repo.Create(ctx, f.owner, contract, in)
	if err != nil {
		t.Fatal(err)
	}
	before := billingLegacyRecords(t, f.runtime.pool)
	if len(before) < 55 {
		t.Fatal("all legacy tables must be covered", len(before))
	}
	provider := automationMigrationProvider(t, f.runtime.pool)
	down, err := provider.DownTo(ctx, 16)
	assertMigrationRange(t, down, err, latestMigrationVersion, 17)
	if !reflect.DeepEqual(before, billingLegacyRecords(t, f.runtime.pool)) {
		t.Fatal("down17 changed legacy records")
	}
	if _, err = f.runtime.pool.Exec(ctx, `UPDATE billing_event_audit SET action='automatic_created' WHERE entry_id=$1`, entry.ID); err == nil {
		t.Fatal("exact v16 audit restrictions must be restored")
	}
	up, err := provider.Up(ctx)
	assertMigrationRange(t, up, err, 17, latestMigrationVersion)
	if !reflect.DeepEqual(before, billingLegacyRecords(t, f.runtime.pool)) {
		t.Fatal("up17 changed legacy records")
	}
	after, err := repo.Get(ctx, f.owner, contract, entry.ID)
	if err != nil || after.Provenance != "owner_confirmed" || after.Sources[0].Provenance != "owner_confirmed" {
		t.Fatal(after, err)
	}
}
func TestBillingAutomationMigrationDownRefusesHistoryOrOrigins(t *testing.T) {
	for _, mode := range []string{"ledger", "entry_origin", "source_origin", "automatic_audit"} {
		t.Run(mode, func(t *testing.T) {
			f := processingSetup(t)
			ctx := context.Background()
			contract, result := financialContract(t, f, financialEvent("payment", "1", "2024-01-01"))
			repo := NewBillingRepository(f.runtime.pool)
			if mode == "ledger" {
				enableFinancial(t, f)
				if _, err := recorder(t, f).RecordDue(ctx, 20); err != nil {
					t.Fatal(err)
				}
			} else {
				in := billingInput("payment", "1", "2024-01-01")
				in.SourceResultID = &result
				in.SourceEventIndex = autoPtr(0)
				entry, err := repo.Create(ctx, f.owner, contract, in)
				if err != nil {
					t.Fatal(err)
				}
				switch mode {
				case "entry_origin":
					_, err = f.runtime.pool.Exec(ctx, `UPDATE billing_events SET provenance='automatic' WHERE id=$1`, entry.ID)
				case "source_origin":
					_, err = f.runtime.pool.Exec(ctx, `UPDATE billing_event_sources SET provenance='automatic' WHERE entry_id=$1`, entry.ID)
				case "automatic_audit":
					_, err = f.runtime.pool.Exec(ctx, `ALTER TABLE billing_event_audit DISABLE TRIGGER preserve_billing_audit; UPDATE billing_event_audit SET action='automatic_created'; ALTER TABLE billing_event_audit ENABLE TRIGGER preserve_billing_audit`)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			before := billingLegacyRecords(t, f.runtime.pool)
			if _, err := automationMigrationProvider(t, f.runtime.pool).DownTo(ctx, 16); err == nil || !strings.Contains(err.Error(), "billing automation data prevents downgrade") {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, billingLegacyRecords(t, f.runtime.pool)) {
				t.Fatal("refused downgrade changed data")
			}
			var version int
			if err := f.runtime.pool.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil || version != 17 {
				t.Fatal(version, err)
			}
		})
	}
}
