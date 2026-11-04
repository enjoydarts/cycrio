//go:build integration

package database

import (
	"context"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/google/uuid"
)

func TestErasureCleanupRemovesPrivateDataAndRetainsOtherOwners(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	result := subscriptionResult(t, f, 1)
	in := subscriptionInput()
	in.Service = "ERASURE_PRIVATE_CONTRACT"
	index := 0
	in.SourceResultID = &result
	in.SourceEventIndex = &index
	contract, err := NewSubscriptionRepository(f.runtime.pool).Create(ctx, f.owner, in)
	if err != nil {
		t.Fatal(err)
	}
	otherIn := subscriptionInput()
	otherIn.Service = "OTHER_OWNER_CONTRACT"
	if _, err = NewSubscriptionRepository(f.runtime.pool).Create(ctx, f.other, otherIn); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO notification_preferences(owner_id,version,settings) VALUES($1,2,'{"private":"ERASURE_PRIVATE_PREFERENCES"}')`,
		`INSERT INTO notification_preferences_audit(id,owner_id,actor_id,version,snapshot) VALUES(gen_random_uuid(),$1,$1,2,'{"private":"ERASURE_PRIVATE_AUDIT"}')`,
		`INSERT INTO ai_user_budgets(owner_id,version,monthly_limit_usd) VALUES($1,1,123)`,
		`INSERT INTO ai_user_budget_audit(owner_id,actor_id,version,monthly_limit_usd) VALUES($1,$1,1,123)`,
		`INSERT INTO auth_identities(issuer,subject,user_id) VALUES('synthetic-erasure','private-subject',$1)`,
		`INSERT INTO integration_profiles(issuer,subject,event_at,object_updated_at,key_id,ciphertext) SELECT issuer,subject,1,1,'synthetic',decode('cafe','hex') FROM auth_identities WHERE user_id=$1`,
		`INSERT INTO mail_oauth_pending(state_hash,browser_hash,owner_id,provider,key_id,ciphertext,expires_at) VALUES('private-state','private-browser',$1,'gmail','synthetic',decode('cafe','hex'),now()+interval '1 hour')`,
		`UPDATE users SET state='deleting' WHERE id=$1`,
		`INSERT INTO user_erasure_requests(client_request_id,owner_id,state,source) VALUES(gen_random_uuid(),$1,'pending','self')`,
	} {
		if _, err = f.runtime.pool.Exec(ctx, statement, f.owner); err != nil {
			t.Fatal(err)
		}
	}
	// A private snapshot in immutable billing history must be erased as well.
	entry := uuid.NewString()
	if _, err = f.runtime.pool.Exec(ctx, `INSERT INTO billing_events(id,owner_id,subscription_id,request_id,request_doc,kind,occurred_on,amount,currency,state) VALUES($1,$2,$3,gen_random_uuid(),'{}','invoice','2026-11-01','10','USD','active')`, entry, f.owner, contract.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.runtime.pool.Exec(ctx, `INSERT INTO billing_event_revisions(owner_id,subscription_id,entry_id,version,snapshot) VALUES($1,$2,$3,1,'{"private":"ERASURE_PRIVATE_SNAPSHOT"}')`, f.owner, contract.ID, entry); err != nil {
		t.Fatal(err)
	}
	if _, err = f.runtime.pool.Exec(ctx, `INSERT INTO ai_user_budget_audit(owner_id,actor_id,version,monthly_limit_usd) VALUES($1,$2,1,321)`, f.other, f.owner); err != nil {
		t.Fatal(err)
	}
	repo := NewErasureRepository(f.runtime.pool, f.keys)
	n, err := repo.PurgeDue(ctx, 10)
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	n, err = repo.PurgeDue(ctx, 10)
	if err != nil || n != 0 {
		t.Fatal("idempotence", n, err)
	}
	for _, table := range []string{"subscriptions", "subscription_audit", "subscription_sources", "billing_events", "billing_event_revisions", "notification_preferences", "notification_preferences_audit", "ai_user_budgets", "ai_user_budget_audit", "mail_account_credentials", "mail_accounts", "mail_processing_items", "mail_processing_runs", "mail_discovered_messages", "mail_oauth_pending"} {
		var count int
		if err = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE owner_id=$1`, f.owner).Scan(&count); err != nil || count != 0 {
			t.Fatal("private rows survived", table, count, err)
		}
	}
	var remaining, other, global int
	if err = f.runtime.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM users WHERE id=$1),(SELECT count(*) FROM subscriptions WHERE owner_id=$2),(SELECT count(*) FROM credential_versions)`, f.owner, f.other).Scan(&remaining, &other, &global); err != nil || remaining != 0 || other != 1 || global == 0 {
		t.Fatal(remaining, other, global, err)
	}
	var actor string
	if err = f.runtime.pool.QueryRow(ctx, `SELECT actor_id::text FROM ai_user_budget_audit WHERE owner_id=$1`, f.other).Scan(&actor); err != nil || actor != "00000000-0000-4000-8000-000000000036" {
		t.Fatal("other owner audit actor", actor, err)
	}
	if err = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM integration_profiles WHERE subject='private-subject'`).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatal("private profile", remaining, err)
	}
	var archive string
	if err = f.runtime.pool.QueryRow(ctx, `SELECT coalesce(jsonb_agg(a)::text,'[]') FROM anonymous_audit_counts a`).Scan(&archive); err != nil || strings.Contains(archive, "ERASURE_PRIVATE") || strings.Contains(archive, f.owner) || archive == "[]" {
		t.Fatal("audit allowlist", archive, err)
	}
}

func TestErasureCleanupRetainsSharedAdminConfigurationAndAvoidsTestNonceCollision(t *testing.T) {
	f := processingSetupOwned(t, true)
	ctx := context.Background()
	var testNonce string
	if err := f.runtime.pool.QueryRow(ctx, `SELECT request_id::text FROM ai_test_runs WHERE requested_by=$1 LIMIT 1`, f.admin).Scan(&testNonce); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runtime.pool.Exec(ctx, `INSERT INTO ai_test_runs(id,profile_id,requested_by,request_id,state) VALUES(gen_random_uuid(),$1,$2,$3,'succeeded')`, f.in.ProfileID, f.other, testNonce); err != nil {
		t.Fatal(err)
	}
	snapshot := uuid.NewString()
	if _, err := f.runtime.pool.Exec(ctx, `INSERT INTO ai_route_snapshots(id,captured_at,source_url,credential_id,credential_version,row_count) SELECT $1,now(),'https://openrouter.ai/api/v1/endpoints/zdr',id,version,1 FROM credential_versions LIMIT 1`, snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runtime.pool.Exec(ctx, `INSERT INTO ai_routes_audit(id,actor_id,snapshot_id) VALUES(gen_random_uuid(),$1,$2)`, f.admin, snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runtime.pool.Exec(ctx, `INSERT INTO mail_processing_standard_proofs(profile_id,policy_version,actor_id,confirmed_at,doc) VALUES($1,999,$2,now(),jsonb_build_object('shared',true,'actor_id',$2::uuid::text))`, f.in.ProfileID, f.admin); err != nil {
		t.Fatal(err)
	}
	call, err := f.runtime.BeginCall(ctx, airuntime.BeginCallInput{OperationID: uuid.NewString(), ProfileID: f.in.ProfileID, Scope: "admin_test", InitiatedByUserID: &f.admin, Stage: "classification", AttemptNo: 1})
	if err != nil {
		t.Fatal(err)
	}
	var credentials, profiles int
	if err = f.runtime.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM credential_versions),(SELECT count(*) FROM ai_profiles)`).Scan(&credentials, &profiles); err != nil {
		t.Fatal(err)
	}
	for _, owner := range []string{f.admin, f.other} {
		if _, err = f.runtime.pool.Exec(ctx, `UPDATE users SET state='deleting' WHERE id=$1`, owner); err != nil {
			t.Fatal(err)
		}
		if _, err = f.runtime.pool.Exec(ctx, `INSERT INTO user_erasure_requests(client_request_id,owner_id,state,source) VALUES(gen_random_uuid(),$1,'pending','self')`, owner); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := NewErasureRepository(f.runtime.pool, f.keys).PurgeDue(ctx, 10); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	var actualCredentials, actualProfiles, tests int
	if err = f.runtime.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM credential_versions),(SELECT count(*) FROM ai_profiles),(SELECT count(*) FROM ai_test_runs WHERE requested_by=$1)`, anonymousErasureUserID).Scan(&actualCredentials, &actualProfiles, &tests); err != nil || actualCredentials != credentials || actualProfiles != profiles || tests != 2 {
		t.Fatal("global data changed", actualCredentials, credentials, actualProfiles, profiles, tests, err)
	}
	var actor string
	if err = f.runtime.pool.QueryRow(ctx, `SELECT initiated_by_user_id::text FROM ai_calls WHERE id=$1`, call.ID).Scan(&actor); err != nil || actor != anonymousErasureUserID {
		t.Fatal("global call actor", actor, err)
	}
	for _, table := range []string{"ai_routes_audit", "mail_processing_standard_proofs"} {
		if err = f.runtime.pool.QueryRow(ctx, `SELECT actor_id::text FROM `+table+` LIMIT 1`).Scan(&actor); err != nil || actor != anonymousErasureUserID {
			t.Fatal("global immutable actor", table, actor, err)
		}
	}
	var privateActor bool
	if err = f.runtime.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM mail_processing_standard_proofs WHERE doc->>'actor_id'=$1)`, f.admin).Scan(&privateActor); err != nil || privateActor {
		t.Fatal("private actor in consent snapshot", privateActor, err)
	}
}

func TestErasureCleanupSkipsFenceAndRollsBackFailure(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	if _, err := f.runtime.pool.Exec(ctx, `UPDATE users SET state='deleting' WHERE id=$1`, f.owner); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runtime.pool.Exec(ctx, `INSERT INTO user_erasure_requests(client_request_id,owner_id,state,source) VALUES(gen_random_uuid(),$1,'pending','self')`, f.owner); err != nil {
		t.Fatal(err)
	}
	repo := NewErasureRepository(f.runtime.pool, f.keys)
	tx, err := f.runtime.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(hashtextextended($1,0))`, "user-erasure-fence/v1:"+f.owner); err != nil {
		t.Fatal(err)
	}
	if n, err := repo.PurgeDue(ctx, 1); err != nil || n != 0 {
		t.Fatal("in-flight external operation", n, err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = f.runtime.pool.Exec(ctx, `CREATE FUNCTION synthetic_reject_erasure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic failure'; END $$; CREATE TRIGGER synthetic_reject_erasure BEFORE DELETE ON users FOR EACH ROW EXECUTE FUNCTION synthetic_reject_erasure()`); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.PurgeDue(ctx, 1); err == nil {
		t.Fatal("failure not propagated")
	}
	var accounts, requests, archives int
	if err = f.runtime.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM mail_accounts WHERE owner_id=$1),(SELECT count(*) FROM user_erasure_requests WHERE owner_id=$1 AND state='pending'),(SELECT count(*) FROM anonymous_audit_counts)`, f.owner).Scan(&accounts, &requests, &archives); err != nil || accounts != 1 || requests != 1 || archives != 0 {
		t.Fatal("partial erasure committed", accounts, requests, archives, err)
	}
	if _, err = f.runtime.pool.Exec(ctx, `DROP TRIGGER synthetic_reject_erasure ON users; DROP FUNCTION synthetic_reject_erasure()`); err != nil {
		t.Fatal(err)
	}
	if n, err := repo.PurgeDue(ctx, 1); err != nil || n != 1 {
		t.Fatal("retry", n, err)
	}
}
