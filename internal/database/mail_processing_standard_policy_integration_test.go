//go:build integration

package database

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/airoutes"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/google/uuid"
)

func standardPolicyFixture(t *testing.T) (*MailProcessingRepository, string, mailprocessing.StandardPolicyInput, airoutes.Route) {
	t.Helper()
	routes, runtime, admin, config, _ := routeFixture(t)
	ctx := context.Background()
	route := refreshRoutes(t, routes, admin)
	// A legacy exact-tag profile remains usable without fabricating a paid test.
	config.OpenRouterProvider = route.Tag
	state, e := runtime.SaveProfile(ctx, admin, config)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = runtime.pool.Exec(ctx, `UPDATE ai_runtime_settings SET active_id=$1 WHERE singleton`, state.Draft.ID); e != nil {
		t.Fatal(e)
	}
	in := mailprocessing.StandardPolicyInput{ExpectedVersion: 0, RuntimeVersion: state.Version, ProfileID: state.Draft.ID, Enabled: true, PrimaryMode: "standard", PrimaryTermsID: mailprocessing.CurrentPrimaryTerms().ID, PrimaryConfirmed: true, SecondaryRouteID: route.ID, SecondaryConfirmed: true}
	return NewMailProcessingRepository(runtime.pool), admin, in, route
}

func TestStandardPolicyPreviewRequiresCurrentTermsButNotFreshRouteAfterSave(t *testing.T) {
	r, a, in, route := standardPolicyFixture(t)
	ctx := context.Background()
	account := uuid.NewString()
	for _, stmt := range []string{`INSERT INTO mail_accounts(id,owner_id,label,state,version,credential_version)VALUES($1,$2,'Synthetic','ready',1,1)`, `INSERT INTO mail_account_credentials(id,account_id,owner_id,version,key_id,ciphertext,state)VALUES(gen_random_uuid(),$1,$2,1,'synthetic','synthetic','active')`, `INSERT INTO mail_account_folders(id,account_id,name,delimiter,selectable,available,selected)VALUES(gen_random_uuid(),$1,'INBOX','/',true,true,true)`} {
		var e error
		if strings.Contains(stmt, "folders") {
			_, e = r.pool.Exec(ctx, stmt, account)
		} else {
			_, e = r.pool.Exec(ctx, stmt, account, a)
		}
		if e != nil {
			t.Fatal(e)
		}
	}
	preview, e := r.Preview(ctx, a, account)
	if e != nil || preview.Ready || preview.BlockedReason != "policy_disabled" {
		t.Fatal(preview, e)
	}
	s, e := r.SaveStandardPolicy(ctx, a, in)
	if e != nil {
		t.Fatal(e)
	}
	preview, e = r.Preview(ctx, a, account)
	if e != nil || !preview.Ready {
		t.Fatal(preview, e)
	}
	if preview.Primary.RetentionSourceURL != mailprocessing.CurrentPrimaryTerms().SourceURL || preview.Secondary.RetentionSourceURL != airoutes.SourceURL {
		t.Fatal("processing preview omitted official frozen retention sources", preview)
	}
	if _, e = r.pool.Exec(ctx, `ALTER TABLE ai_routes DISABLE TRIGGER preserve_routes`); e != nil {
		t.Fatal(e)
	}
	if _, e = r.pool.Exec(ctx, `UPDATE ai_routes SET doc=jsonb_set(doc,'{captured_at}',to_jsonb(clock_timestamp()-interval '16 minutes')) WHERE id=$1`, route.ID); e != nil {
		t.Fatal(e)
	}
	state, e := r.AdminRead(ctx, a)
	if e != nil || state.StandardTerms.Ready || !state.Policy.Enabled {
		t.Fatal(state, e)
	}
	preview, e = r.Preview(ctx, a, account)
	if e != nil || !preview.Ready {
		t.Fatal("route age disabled existing permission", preview, e)
	}
	if _, e = r.pool.Exec(ctx, `ALTER TABLE mail_processing_standard_proofs DISABLE TRIGGER preserve_mail_standard_proofs`); e != nil {
		t.Fatal(e)
	}
	if _, e = r.pool.Exec(ctx, `UPDATE mail_processing_standard_proofs SET doc=jsonb_set(doc,'{primary_terms,id}','"old-terms"') WHERE profile_id=$1 AND policy_version=$2`, in.ProfileID, s.Policy.Version); e != nil {
		t.Fatal(e)
	}
	preview, e = r.Preview(ctx, a, account)
	if e != nil || preview.Ready || preview.BlockedReason != "policy_reconfirmation_required" {
		t.Fatal("obsolete standard permission remained usable", preview, e)
	}
}
func TestStandardPolicyExplicitConsentAndImmutableHistory(t *testing.T) {
	r, a, in, route := standardPolicyFixture(t)
	ctx := context.Background()
	before, e := r.AdminRead(ctx, a)
	if e != nil || before.Policy.Enabled || before.Policy.StandardProof != nil || before.StandardTerms == nil || !before.StandardTerms.Ready || before.StandardTerms.SecondaryRoute.ID != route.ID {
		t.Fatal(before, e)
	}
	s, e := r.SaveStandardPolicy(ctx, a, in)
	if e != nil || !s.Policy.Enabled || s.Policy.Version != 1 || s.Policy.StandardProof == nil || s.Policy.StandardProof.SecondaryRoute.ID != route.ID {
		t.Fatal(s, e)
	}
	if s.Profile.Primary.Price != nil {
		t.Fatal("fixture should preserve unknown primary price")
	}
	if _, e = r.pool.Exec(ctx, `UPDATE mail_processing_standard_proofs SET doc=doc`); e == nil {
		t.Fatal("proof mutable")
	}
	old := s.Policy.StandardProof
	s, e = r.SavePolicy(ctx, a, mailprocessing.PolicyInput{ExpectedVersion: 1, ProfileID: in.ProfileID, Enabled: true, PrimaryRetention: "Custom primary", SecondaryRetention: "Custom secondary", PrimaryConfirmed: true, SecondaryConfirmed: true})
	if e != nil || s.Policy.StandardProof != nil {
		t.Fatal(s, e)
	}
	if s.Profile.Primary.RetentionSourceURL != "" || s.Profile.Secondary.RetentionSourceURL != "" {
		t.Fatal("custom save reused a previous standard source", s.Profile)
	}
	var count int
	if e = r.pool.QueryRow(ctx, `SELECT count(*) FROM mail_processing_standard_proofs WHERE profile_id=$1 AND policy_version=$2`, old.ProfileID, old.PolicyVersion).Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
}
func TestStandardPolicyRejectsInvalidStateWithoutPartialWrite(t *testing.T) {
	for _, kind := range []string{"unchecked_primary", "unchecked_secondary", "old_terms", "policy_version", "runtime_version", "profile", "route", "tag", "model", "stale", "generation", "key", "revoke", "role", "ambiguous", "cross_model", "output_limit"} {
		t.Run(kind, func(t *testing.T) {
			r, a, in, route := standardPolicyFixture(t)
			ctx := context.Background()
			var e error
			switch kind {
			case "unchecked_primary":
				in.PrimaryConfirmed = false
			case "unchecked_secondary":
				in.SecondaryConfirmed = false
			case "old_terms":
				in.PrimaryTermsID = "liquid-standard-old"
			case "policy_version":
				in.ExpectedVersion++
			case "runtime_version":
				in.RuntimeVersion++
			case "profile":
				in.ProfileID = "11111111-1111-4111-8111-111111111111"
			case "route":
				in.SecondaryRouteID = "11111111-1111-4111-8111-111111111111"
			case "tag":
				_, e = r.pool.Exec(ctx, `UPDATE ai_profiles SET doc=jsonb_set(doc,'{openrouter_provider}','"deepinfra"') WHERE id=$1`, in.ProfileID)
			case "model":
				_, e = r.pool.Exec(ctx, `UPDATE model_catalog SET doc=jsonb_set(doc,'{enabled}','false') WHERE provider='openrouter'`)
			case "stale":
				_, e = r.pool.Exec(ctx, `ALTER TABLE ai_routes DISABLE TRIGGER preserve_routes`)
				if e == nil {
					_, e = r.pool.Exec(ctx, `UPDATE ai_routes SET doc=jsonb_set(doc,'{captured_at}',to_jsonb(clock_timestamp()-interval '16 minutes')) WHERE id=$1`, route.ID)
				}
			case "generation":
				refreshRoutes(t, NewAIRoutesRepository(r.pool), a)
			case "key":
				_, e = r.pool.Exec(ctx, `UPDATE credential_versions SET state='superseded' WHERE provider='openrouter'`)
			case "revoke":
				_, e = r.pool.Exec(ctx, `UPDATE credential_versions SET state='disabled' WHERE provider='openrouter'`)
			case "role":
				_, e = r.pool.Exec(ctx, `DELETE FROM user_roles WHERE user_id=$1`, a)
			case "ambiguous", "cross_model", "output_limit":
				_, e = r.pool.Exec(ctx, `ALTER TABLE ai_routes DISABLE TRIGGER preserve_routes`)
				if e == nil {
					switch kind {
					case "ambiguous":
						_, e = r.pool.Exec(ctx, `UPDATE ai_routes SET doc=jsonb_set(doc,'{ambiguous}','true') WHERE id=$1`, route.ID)
					case "cross_model":
						_, e = r.pool.Exec(ctx, `UPDATE ai_routes SET doc=jsonb_set(doc,'{upstream_model_id}','"other/model"') WHERE id=$1`, route.ID)
					case "output_limit":
						_, e = r.pool.Exec(ctx, `UPDATE ai_routes SET doc=jsonb_set(doc,'{max_completion_tokens}','64') WHERE id=$1`, route.ID)
					}
				}
			}
			if e != nil {
				t.Fatal(e)
			}
			_, e = r.SaveStandardPolicy(ctx, a, in)
			if !errors.Is(e, mailprocessing.ErrInvalid) && !errors.Is(e, mailprocessing.ErrConflict) {
				t.Fatalf("invalid state accepted %v", e)
			}
			var count int
			if e = r.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM mail_processing_policies)+(SELECT count(*) FROM mail_processing_standard_proofs)+(SELECT count(*) FROM mail_processing_policy_audit)`).Scan(&count); e != nil || count != 0 {
				t.Fatal("partial policy/proof/audit", count, e)
			}
		})
	}
}

func TestStandardPolicyCustomPrimaryAndConcurrentCAS(t *testing.T) {
	r, a, in, _ := standardPolicyFixture(t)
	ctx := context.Background()
	in.PrimaryMode = "custom"
	in.PrimaryTermsID = ""
	in.PrimaryRetention = "Individually agreed retention"
	results := make(chan error, 8)
	for range 8 {
		go func() { _, e := r.SaveStandardPolicy(ctx, a, in); results <- e }()
	}
	success := 0
	for range 8 {
		e := <-results
		if e == nil {
			success++
		} else if !errors.Is(e, mailprocessing.ErrConflict) {
			t.Fatal(e)
		}
	}
	if success != 1 {
		t.Fatal("CAS winners", success)
	}
	state, e := r.AdminRead(ctx, a)
	if e != nil || state.Policy.PrimaryRetention != in.PrimaryRetention || state.Policy.StandardProof.PrimaryTerms != nil || state.Policy.StandardProof.PrimaryMode != "custom" {
		t.Fatal(state, e)
	}
	var count int
	if e = r.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM mail_processing_policies)+(SELECT count(*) FROM mail_processing_standard_proofs)+(SELECT count(*) FROM mail_processing_policy_audit)`).Scan(&count); e != nil || count != 3 {
		t.Fatal("partial concurrent writes", count, e)
	}
}

func TestStandardPolicyWriteFailureRollsBackPolicyProofAndAudit(t *testing.T) {
	for _, table := range []string{"mail_processing_policy_audit", "mail_processing_standard_proofs"} {
		t.Run(table, func(t *testing.T) {
			r, actor, in, _ := standardPolicyFixture(t)
			ctx := context.Background()
			if _, e := r.pool.Exec(ctx, `CREATE FUNCTION reject_standard_write() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'synthetic rejection'; END$$; CREATE TRIGGER reject_standard BEFORE INSERT ON `+table+` FOR EACH ROW EXECUTE FUNCTION reject_standard_write()`); e != nil {
				t.Fatal(e)
			}
			if _, e := r.SaveStandardPolicy(ctx, actor, in); !errors.Is(e, mailprocessing.ErrUnavailable) {
				t.Fatal("write failure was not safely returned", e)
			}
			var count int
			if e := r.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM mail_processing_policies)+(SELECT count(*) FROM mail_processing_standard_proofs)+(SELECT count(*) FROM mail_processing_policy_audit)`).Scan(&count); e != nil || count != 0 {
				t.Fatal("partial policy/proof/audit", count, e)
			}
		})
	}
}
