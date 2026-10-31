//go:build integration

package database

import (
	"testing"

	"github.com/pressly/goose/v3"
)

const latestMigrationVersion = 23

// assertMigrationRange checks every applied version, including direction and
// count, so adding newer migrations does not weaken an older round-trip test.
func assertMigrationRange(t *testing.T, results []*goose.MigrationResult, err error, first, last int64) {
	t.Helper()
	if err != nil {
		t.Fatal(results, err)
	}
	step := int64(1)
	count := last - first + 1
	if last < first {
		step = -1
		count = first - last + 1
	}
	if int64(len(results)) != count {
		t.Fatalf("migration range %d..%d: got %d results: %v", first, last, len(results), results)
	}
	direction := "up"
	if step < 0 {
		direction = "down"
	}
	for i, result := range results {
		want := first + int64(i)*step
		if result == nil || result.Source == nil || result.Source.Version != want || result.Direction != direction || result.Error != nil {
			t.Fatalf("migration range %d..%d: index %d must be %s version %d: %v", first, last, i, direction, want, result)
		}
	}
}

// These columns only exist from v19/v20 onward. Compare the complete legacy
// record while allowing those independent migrations to be removed/reapplied.
func mailConnectionMigrationColumns(table string) []string {
	switch table {
	case "subscriptions":
		return []string{"deleted_at", "deleted_expected_version", "contract_kind", "effective_date", "access_end_date"}
	case "mail_accounts":
		return []string{"host", "port"}
	case "mail_account_folders":
		return []string{"display_name"}
	case "mail_sync_run_folders", "mail_sync_cursors", "mail_automation_progress":
		return []string{"cursor_key_id", "cursor_ciphertext"}
	case "mail_discovered_messages":
		return []string{"remote_fingerprint"}
	default:
		return []string{}
	}
}
