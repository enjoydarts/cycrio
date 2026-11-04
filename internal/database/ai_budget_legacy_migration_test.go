//go:build integration

package database

import (
	"context"
	"testing"
)

func TestAIBudgetTablesAreOutsideLegacyMigrationSnapshots(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	for name, snapshot := range map[string]map[string]string{
		"routes_v11":             allLegacyRouteRecords(t, f.runtime.pool),
		"standard_terms_v12":     allStandardLegacyRecords(t, f.runtime.pool),
		"mail_automation_v15":    allAutomationLegacyRecords(t, f.runtime.pool),
		"billing_automation_v16": billingLegacyRecords(t, f.runtime.pool),
	} {
		for _, table := range []string{"ai_user_budgets", "ai_user_budget_audit"} {
			var count int
			if err := f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil || count != 0 {
				t.Fatal("new budget tables must start empty", table, count, err)
			}
			if data, included := snapshot[table]; included {
				t.Errorf("%s includes migration35-only table %s=%s", name, table, data)
			}
		}
		for _, legacy := range []string{"users", "ai_calls", "model_catalog", "mail_processing_items"} {
			if _, included := snapshot[legacy]; !included {
				t.Errorf("%s lost legacy table %s", name, legacy)
			}
		}
	}
}
