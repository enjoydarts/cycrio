package database

import (
	"context"

	"github.com/jackc/pgx/v5"
)

const anonymousErasureUserID = "00000000-0000-4000-8000-000000000036"

// PurgeDue only consumes explicitly requested withdrawals. A busy external
// operation leaves its request pending for a later bounded worker pass.
func (r *ErasureRepository) PurgeDue(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	if limit > 100 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `SELECT id::text,owner_id::text FROM user_erasure_requests WHERE state='pending' ORDER BY requested_at,id LIMIT $1`, limit)
	if err != nil {
		return 0, err
	}
	type job struct{ id, owner string }
	jobs := []job{}
	for rows.Next() {
		var j job
		if err = rows.Scan(&j.id, &j.owner); err != nil {
			rows.Close()
			return 0, err
		}
		jobs = append(jobs, j)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	done := 0
	for _, j := range jobs {
		tx, e := r.pool.Begin(ctx)
		if e != nil {
			return done, e
		}
		erased, e := purgeErasure(ctx, tx, j.id, j.owner)
		if e == nil && erased {
			e = tx.Commit(ctx)
		} else {
			_ = tx.Rollback(ctx)
		}
		if e != nil {
			_ = tx.Rollback(ctx)
			return done, e
		}
		if erased {
			done++
		}
	}
	return done, nil
}

func purgeErasure(ctx context.Context, tx pgx.Tx, request, owner string) (bool, error) {
	var acquired bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))`, "user-erasure-fence/v1:"+owner).Scan(&acquired); err != nil {
		return false, err
	}
	if !acquired {
		return false, nil
	}
	rows, err := tx.Query(ctx, `SELECT issuer,subject FROM auth_identities WHERE user_id=$1 ORDER BY issuer,subject`, owner)
	if err != nil {
		return false, err
	}
	type identity struct{ issuer, subject string }
	identities := []identity{}
	for rows.Next() {
		var i identity
		if err = rows.Scan(&i.issuer, &i.subject); err != nil {
			rows.Close()
			return false, err
		}
		identities = append(identities, i)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	for _, i := range identities {
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, i.issuer+"|"+i.subject); err != nil {
			return false, err
		}
	}
	var state string
	err = tx.QueryRow(ctx, `SELECT state FROM users WHERE id=$1 FOR NO KEY UPDATE`, owner).Scan(&state)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if state != "deleting" {
		return false, nil
	}
	err = tx.QueryRow(ctx, `SELECT state FROM user_erasure_requests WHERE id=$1 AND owner_id=$2 FOR UPDATE`, request, owner).Scan(&state)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if state != "pending" {
		return false, nil
	}
	if _, err = tx.Exec(ctx, `INSERT INTO users(id,state) VALUES($1,'disabled') ON CONFLICT(id) DO NOTHING`, anonymousErasureUserID); err != nil {
		return false, err
	}
	if err = archiveErasureCalls(ctx, tx, owner); err != nil {
		return false, err
	}
	if err = archiveErasureAudits(ctx, tx, owner); err != nil {
		return false, err
	}
	// Keep only one-way receipts so delayed webhooks cannot restore provider IDs.
	if err = archiveErasureResendEmails(ctx, tx, owner); err != nil {
		return false, err
	}
	for _, table := range []string{
		"notification_deliveries", "notification_events", "notification_import_episodes", "notification_generation_state", "notification_preferences_audit", "notification_preferences",
		"billing_schedule_audit", "billing_schedule_requests", "billing_schedule_settings", "subscription_change_requests",
		"billing_automation_events", "billing_automation_work", "billing_source_detachments", "billing_event_audit", "billing_event_revisions", "billing_event_sources", "billing_events",
		"subscription_automation_events", "subscription_automation_results", "subscription_automation_preference_audit", "subscription_automation_preferences", "subscription_candidate_dismissals", "subscription_audit", "subscription_sources", "subscriptions",
		"mail_processing_retry_batches", "mail_processing_audit", "mail_processing_items", "mail_processing_runs",
		"mail_sync_audit", "mail_sync_run_folders", "mail_sync_runs", "mail_sync_cursors",
		"mail_automation_audit", "mail_automation_messages", "mail_automation_progress", "mail_automation_scopes", "mail_automation_requests", "mail_automations",
		"mail_message_folder_memberships", "mail_discovered_messages",
		"mail_account_probes", "mail_oauth_pending", "integration_email_tests", "ai_user_budget_audit", "ai_user_budgets",
	} {
		if _, err = tx.Exec(ctx, `DELETE FROM `+table+` WHERE owner_id=$1`, owner); err != nil {
			return false, err
		}
	}
	for _, table := range []string{"mail_account_audit", "mail_account_folders"} {
		if _, err = tx.Exec(ctx, `DELETE FROM `+table+` WHERE account_id IN (SELECT id FROM mail_accounts WHERE owner_id=$1)`, owner); err != nil {
			return false, err
		}
	}
	for _, table := range []string{"mail_account_credentials", "mail_accounts"} {
		if _, err = tx.Exec(ctx, `DELETE FROM `+table+` WHERE owner_id=$1`, owner); err != nil {
			return false, err
		}
	}
	// Shared administrator configuration remains available. Only its actor is
	// replaced; private owner records above have already been removed.
	for _, reference := range [][2]string{
		{"credential_versions", "created_by"}, {"credential_audit", "actor_id"}, {"catalog_prices", "created_by"}, {"catalog_settings_versions", "created_by"}, {"catalog_audit", "actor_id"},
		{"ai_profiles", "created_by"}, {"ai_runtime_audit", "actor_id"}, {"ai_calls", "initiated_by_user_id"}, {"mail_processing_policy_audit", "actor_id"}, {"ai_routes_audit", "actor_id"},
		{"integration_audit", "actor_id"}, {"price_research_runs", "requested_by"}, {"price_research_audit", "actor_id"}, {"ai_user_budget_audit", "actor_id"}, {"mail_account_audit", "actor_id"},
	} {
		if _, err = tx.Exec(ctx, `UPDATE `+reference[0]+` SET `+reference[1]+`=$2 WHERE `+reference[1]+`=$1`, owner, anonymousErasureUserID); err != nil {
			return false, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE mail_processing_standard_proofs SET actor_id=$2,doc=CASE WHEN doc->>'actor_id'=actor_id::text THEN jsonb_set(doc,'{actor_id}',to_jsonb($2::uuid::text),false) ELSE doc END WHERE actor_id=$1`, owner, anonymousErasureUserID); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE ai_test_runs SET requested_by=$2,request_id=gen_random_uuid() WHERE requested_by=$1`, owner, anonymousErasureUserID); err != nil {
		return false, err
	}
	for _, i := range identities {
		if _, err = tx.Exec(ctx, `DELETE FROM integration_profiles WHERE issuer=$1 AND subject=$2`, i.issuer, i.subject); err != nil {
			return false, err
		}
	}
	for _, table := range []string{"auth_identities", "user_roles"} {
		if _, err = tx.Exec(ctx, `DELETE FROM `+table+` WHERE user_id=$1`, owner); err != nil {
			return false, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE user_erasure_requests SET state='completed',owner_id=NULL,completed_at=clock_timestamp() WHERE id=$1`, request); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM users WHERE id=$1`, owner); err != nil {
		return false, err
	}
	return true, nil
}

func archiveErasureAudits(ctx context.Context, tx pgx.Tx, owner string) error {
	// The table names, actions and predicates are program constants. Never copy
	// a source snapshot, free text, source ID or original owner into the archive.
	for _, audit := range [][4]string{
		{"mail_account_audit", "mail_accounts", "action", "account_id IN (SELECT id FROM mail_accounts WHERE owner_id=$1)"},
		{"mail_sync_audit", "mail_sync", "action", "owner_id=$1"}, {"mail_processing_audit", "mail_processing", "action", "owner_id=$1"},
		{"subscription_audit", "subscriptions", "action", "owner_id=$1"}, {"billing_event_audit", "billing_events", "action", "owner_id=$1"}, {"billing_schedule_audit", "billing_schedule", "action", "owner_id=$1"},
		{"subscription_change_requests", "subscription_changes", "'change_applied'", "owner_id=$1"}, {"subscription_automation_preference_audit", "subscription_automation", "'preferences_saved'", "owner_id=$1"},
		{"mail_automation_audit", "mail_automation", "action", "owner_id=$1"}, {"notification_preferences_audit", "notifications", "'preferences_saved'", "owner_id=$1"}, {"ai_user_budget_audit", "ai_budgets", "'budget_saved'", "owner_id=$1"},
	} {
		_, err := tx.Exec(ctx, `INSERT INTO anonymous_audit_counts(month,area,action,entries) SELECT date_trunc('month',created_at AT TIME ZONE 'Asia/Tokyo')::date,'`+audit[1]+`',`+audit[2]+`,count(*) FROM `+audit[0]+` WHERE `+audit[3]+` GROUP BY 1,3`, owner)
		if err != nil {
			return err
		}
	}
	return nil
}
