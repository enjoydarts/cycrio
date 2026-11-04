//go:build integration

package database

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestErasureImmutabilityExceptionsStayOwnerAndActorScoped(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	for _, owner := range []string{f.owner, f.other} {
		if _, err := f.runtime.pool.Exec(ctx, `INSERT INTO ai_user_budget_audit(owner_id,actor_id,version,monthly_limit_usd) VALUES($1,$1,1,123)`, owner); err != nil {
			t.Fatal(err)
		}
		if _, err := f.runtime.pool.Exec(ctx, `INSERT INTO notification_preferences_audit(id,owner_id,actor_id,version,snapshot) VALUES(gen_random_uuid(),$1,$1,2,'{"private":"immutable"}')`, owner); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.runtime.pool.Exec(ctx, `INSERT INTO users(id,state) VALUES($1,'disabled')`, anonymousErasureUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runtime.pool.Exec(ctx, `UPDATE users SET state='deleting' WHERE id=$1`, f.owner); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"ai_user_budget_audit", "notification_preferences_audit"} {
		if _, err := f.runtime.pool.Exec(ctx, `UPDATE `+table+` SET version=version+1 WHERE owner_id=$1`, f.owner); err == nil {
			t.Fatal("deleting owner history UPDATE accepted", table)
		}
		if _, err := f.runtime.pool.Exec(ctx, `DELETE FROM `+table+` WHERE owner_id=$1`, f.other); err == nil {
			t.Fatal("other owner history DELETE accepted", table)
		}
		if _, err := f.runtime.pool.Exec(ctx, `DELETE FROM `+table+` WHERE owner_id=$1`, f.owner); err != nil {
			t.Fatal("deleting owner history DELETE rejected", table, err)
		}
	}
	// Global consent history permits only actor substitution, never document edits.
	if _, err := f.runtime.pool.Exec(ctx, `INSERT INTO mail_processing_standard_proofs(profile_id,policy_version,actor_id,confirmed_at,doc) VALUES($1,999,$2,now(),jsonb_build_object('immutable',true,'actor_id',$2::uuid::text))`, f.in.ProfileID, f.owner); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runtime.pool.Exec(ctx, `UPDATE mail_processing_standard_proofs SET actor_id=$1,doc='{}' WHERE policy_version=999`, anonymousErasureUserID); err == nil {
		t.Fatal("actor exception allowed doc change")
	}
	if _, err := f.runtime.pool.Exec(ctx, `UPDATE mail_processing_standard_proofs SET actor_id=$1 WHERE policy_version=999`, f.other); err == nil {
		t.Fatal("actor exception allowed arbitrary actor")
	}
	if _, err := f.runtime.pool.Exec(ctx, `UPDATE mail_processing_standard_proofs SET actor_id=$1 WHERE policy_version=999`, anonymousErasureUserID); err == nil {
		t.Fatal("consent actor column changed while JSON retained original actor")
	}
	if _, err := f.runtime.pool.Exec(ctx, `UPDATE mail_processing_standard_proofs SET actor_id=$1,doc=jsonb_set(doc,'{actor_id}',to_jsonb($1::uuid::text)) || '{"immutable":false}' WHERE policy_version=999`, anonymousErasureUserID); err == nil {
		t.Fatal("actor exception changed another JSON field")
	}
	if _, err := f.runtime.pool.Exec(ctx, `UPDATE mail_processing_standard_proofs SET actor_id=$1,doc=jsonb_set(doc,'{actor_id}',to_jsonb($1::uuid::text)) WHERE policy_version=999`, anonymousErasureUserID); err != nil {
		t.Fatal("anonymous actor substitution", err)
	}
	if _, err := f.runtime.pool.Exec(ctx, `DELETE FROM mail_processing_standard_proofs WHERE policy_version=999`); err == nil {
		t.Fatal("global proof deletion accepted")
	}
	if _, err := f.runtime.pool.Exec(ctx, `INSERT INTO anonymous_audit_counts(id,month,area,action,entries) VALUES($1,'2026-11-01','subscriptions','created',1)`, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{`UPDATE anonymous_audit_counts SET entries=2`, `DELETE FROM anonymous_audit_counts`} {
		if _, err := f.runtime.pool.Exec(ctx, statement); err == nil {
			t.Fatal("anonymous audit mutation accepted", statement)
		}
	}
}
