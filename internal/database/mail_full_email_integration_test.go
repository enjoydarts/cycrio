//go:build integration

package database

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/google/uuid"
)

func TestFullEmailRequiresExplicitAdminScopeBeforeRunOrAudit(t *testing.T) {
	f := processingSetup(t)
	f.add(t, 1)
	ctx := context.Background()
	in := f.in
	in.FullEmail = true
	if _, err := f.repo.Request(ctx, f.owner, f.account, in); !errors.Is(err, mailprocessing.ErrConflict) {
		t.Fatal("owner broadened administrator scope", err)
	}
	var runs, audits, calls int
	if err := f.runtime.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM mail_processing_runs),(SELECT count(*) FROM mail_processing_audit),(SELECT count(*) FROM ai_calls WHERE scope='user')`).Scan(&runs, &audits, &calls); err != nil || runs != 0 || audits != 0 || calls != 0 {
		t.Fatal(runs, audits, calls, err)
	}
}

func fullEmailPolicy(t *testing.T, f processingFixture, allowed bool, version int64) mailprocessing.PolicyState {
	t.Helper()
	s, err := f.repo.SavePolicy(context.Background(), f.admin, mailprocessing.PolicyInput{ExpectedVersion: version, ProfileID: f.in.ProfileID, Enabled: true, FullEmailAllowed: &allowed, PrimaryRetention: "Synthetic contract", SecondaryRetention: "Synthetic contract", PrimaryConfirmed: true, SecondaryConfirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestFullEmailModePersistsAndReplayCannotChangeIt(t *testing.T) {
	for _, full := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "whole"}[full], func(t *testing.T) {
			f := processingSetup(t)
			f.add(t, 1)
			ctx := context.Background()
			s := fullEmailPolicy(t, f, true, 1)
			f.in.PolicyVersion = s.Policy.Version
			f.in.FullEmail = full
			p, err := f.repo.Preview(ctx, f.owner, f.account)
			if err != nil || !p.FullEmailAllowed {
				t.Fatal("scope not projected", p, err)
			}
			run, err := f.repo.Request(ctx, f.owner, f.account, f.in)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.repo.Request(ctx, f.other, f.account, f.in); !errors.Is(err, mailprocessing.ErrNotFound) {
				t.Fatal("cross owner", err)
			}
			replay, err := f.repo.Request(ctx, f.owner, f.account, f.in)
			if err != nil || replay.ID != run.ID {
				t.Fatal("idempotency", err)
			}
			changed := f.in
			changed.FullEmail = !full
			if _, err = f.repo.Request(ctx, f.owner, f.account, changed); !errors.Is(err, mailprocessing.ErrConflict) {
				t.Fatal("mode nonce reused", err)
			}
			var raw []byte
			if err = f.runtime.pool.QueryRow(ctx, `SELECT request_doc FROM mail_processing_runs WHERE id=$1`, run.ID).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var saved processingRequest
			if json.Unmarshal(raw, &saved) != nil || saved.Input.FullEmail != full {
				t.Fatal("request mode lost")
			}
			_, w := f.start(t)
			if w.FullEmail != full {
				t.Fatal("saved mode not loaded", w.FullEmail)
			}
		})
	}
}

func TestFullEmailScopeCannotBeWrittenByOwnerAndLegacySaveCannotBroaden(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	full := true
	if _, err := f.repo.SavePolicy(ctx, f.owner, mailprocessing.PolicyInput{ExpectedVersion: 1, ProfileID: f.in.ProfileID, FullEmailAllowed: &full}); !errors.Is(err, mailprocessing.ErrConflict) {
		t.Fatal("nonadmin scope write", err)
	}
	s := fullEmailPolicy(t, f, true, 1)
	s, err := f.repo.SavePolicy(ctx, f.admin, mailprocessing.PolicyInput{ExpectedVersion: s.Policy.Version, ProfileID: f.in.ProfileID})
	if err != nil || s.Policy.Enabled || !s.Policy.FullEmailAllowed {
		t.Fatal("legacy stop lost scope", s, err)
	}
	s, err = f.repo.SavePolicy(ctx, f.admin, mailprocessing.PolicyInput{ExpectedVersion: s.Policy.Version, ProfileID: f.in.ProfileID, Enabled: true, PrimaryRetention: "Synthetic", SecondaryRetention: "Synthetic", PrimaryConfirmed: true, SecondaryConfirmed: true})
	if err != nil || s.Policy.FullEmailAllowed {
		t.Fatal("legacy enabled save must default bounded", s, err)
	}
}

func TestFullEmailRevocationStopsEachWorkerBoundaryAndKeepsLateCost(t *testing.T) {
	for _, boundary := range []string{"claim", "start", "secondary"} {
		t.Run(boundary, func(t *testing.T) {
			f := processingSetup(t)
			f.add(t, 1)
			ctx := context.Background()
			s := fullEmailPolicy(t, f, true, 1)
			f.in.FullEmail = true
			f.in.PolicyVersion = s.Policy.Version
			run, err := f.repo.Request(ctx, f.owner, f.account, f.in)
			if err != nil {
				t.Fatal(err)
			}
			var lease mailprocessing.Lease
			var call airuntime.Call
			if boundary != "claim" {
				leases, err := f.repo.Claim(ctx)
				if err != nil || len(leases) != 1 {
					t.Fatal(leases, err)
				}
				lease = leases[0]
			}
			if boundary == "secondary" {
				if _, err = f.repo.Start(ctx, lease); err != nil {
					t.Fatal(err)
				}
				call, err = f.repo.BeginCall(ctx, lease, "classification")
				if err != nil {
					t.Fatal(err)
				}
			}
			fullEmailPolicy(t, f, false, s.Policy.Version)
			switch boundary {
			case "claim":
				leases, err := f.repo.Claim(ctx)
				if err != nil || len(leases) != 0 {
					t.Fatal("revoked claim", leases, err)
				}
			case "start":
				if _, err = f.repo.Start(ctx, lease); !errors.Is(err, mailprocessing.ErrNotFound) {
					t.Fatal("revoked start", err)
				}
			case "secondary":
				usd, amount := "USD", "0.12"
				if _, err = f.repo.FinishCall(ctx, call.ID, airuntime.CallUpdate{Outcome: "success", Cost: ai.Cost{Status: "provider_reported", Currency: &usd, Amount: &amount}}); err != nil {
					t.Fatal("late actual cost lost", err)
				}
				if _, err = f.repo.BeginCall(ctx, lease, "extraction"); !errors.Is(err, mailprocessing.ErrNotFound) {
					t.Fatal("revoked secondary", err)
				}
				var recorded bool
				if err = f.runtime.pool.QueryRow(ctx, `SELECT adopted_amount=0.12 AND consuming_user_id=$2 FROM ai_calls WHERE id=$1`, call.ID, f.owner).Scan(&recorded); err != nil || !recorded {
					t.Fatal(recorded, err)
				}
			}
			var state string
			if err = f.runtime.pool.QueryRow(ctx, `SELECT state FROM mail_processing_runs WHERE id=$1`, run.ID).Scan(&state); err != nil || state != "cancelled" {
				t.Fatal("revoked run alive", state, err)
			}
			var calls int
			if err = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM ai_calls WHERE scope='user'`).Scan(&calls); err != nil || calls != map[bool]int{false: 0, true: 1}[boundary == "secondary"] {
				t.Fatal(calls, err)
			}
		})
	}
}

func TestFullEmailRetryScopeAndPersistedMode(t *testing.T) {
	f := processingSetup(t)
	ids := f.add(t, 1)
	ctx := context.Background()
	if _, err := f.repo.Request(ctx, f.owner, f.account, f.in); err != nil {
		t.Fatal(err)
	}
	l, _ := f.start(t)
	if err := f.repo.Finish(ctx, l, mailprocessing.Completion{State: "failed", ErrorCode: "input_too_large"}); err != nil {
		t.Fatal(err)
	}
	results, err := f.repo.Results(ctx, f.owner, f.account)
	if err != nil {
		t.Fatal(err)
	}
	in := mailprocessing.RetryInput{Input: f.in, ExpectedResultID: results[0].ID}
	in.RequestID = uuid.NewString()
	in.MaxMessages = 1
	in.FullEmail = true
	if _, err = f.repo.Retry(ctx, f.owner, f.account, ids[0], in); !errors.Is(err, mailprocessing.ErrConflict) {
		t.Fatal("retry broadened scope", err)
	}
	s := fullEmailPolicy(t, f, true, 1)
	in.PolicyVersion = s.Policy.Version
	run, err := f.repo.Retry(ctx, f.owner, f.account, ids[0], in)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := f.repo.Retry(ctx, f.owner, f.account, ids[0], in)
	if err != nil || replay.ID != run.ID {
		t.Fatal("retry replay", err)
	}
	changed := in
	changed.FullEmail = false
	if _, err = f.repo.Retry(ctx, f.owner, f.account, ids[0], changed); !errors.Is(err, mailprocessing.ErrConflict) {
		t.Fatal("retry mode reused", err)
	}
	_, w := f.start(t)
	if !w.FullEmail {
		t.Fatal("retry saved mode lost")
	}
}

func TestFullEmailWorkerChecksCurrentScopeEvenAtSamePolicyVersion(t *testing.T) {
	f := processingSetup(t)
	f.add(t, 1)
	ctx := context.Background()
	s := fullEmailPolicy(t, f, true, 1)
	f.in.PolicyVersion = s.Policy.Version
	f.in.FullEmail = true
	if _, err := f.repo.Request(ctx, f.owner, f.account, f.in); err != nil {
		t.Fatal(err)
	}
	l, _ := f.start(t)
	if _, err := f.runtime.pool.Exec(ctx, `UPDATE mail_processing_policies SET full_email_allowed=false WHERE profile_id=$1`, f.in.ProfileID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.BeginCall(ctx, l, "classification"); !errors.Is(err, mailprocessing.ErrNotFound) {
		t.Fatal("worker trusted nonce scope over active administrator scope", err)
	}
}

func TestFullEmailStandardPolicyFreezesScopeWithoutNewProfileOrPaidTest(t *testing.T) {
	for _, mode := range []string{"standard", "custom"} {
		t.Run(mode, func(t *testing.T) {
			r, admin, in, _ := standardPolicyFixture(t)
			ctx := context.Background()
			full := true
			in.FullEmailAllowed = &full
			if mode == "custom" {
				in.PrimaryMode = mode
				in.PrimaryTermsID = ""
				in.PrimaryRetention = "Synthetic contract"
			}
			before, err := r.AdminRead(ctx, admin)
			if err != nil {
				t.Fatal(err)
			}
			s, err := r.SaveStandardPolicy(ctx, admin, in)
			if err != nil || !s.Policy.FullEmailAllowed || s.Policy.StandardProof == nil || !s.Policy.StandardProof.FullEmailAllowed || !mailprocessing.StandardProofCurrent(*s.Policy) || before.RuntimeVersion != s.RuntimeVersion || before.Profile.ID != s.Profile.ID {
				t.Fatal("scope save changed runtime or lost proof", s, err)
			}
			if _, err = r.pool.Exec(ctx, `UPDATE mail_processing_policies SET full_email_allowed=false WHERE profile_id=$1`, in.ProfileID); err != nil {
				t.Fatal(err)
			}
			s, err = r.AdminRead(ctx, admin)
			if err != nil || mailprocessing.StandardProofCurrent(*s.Policy) {
				t.Fatal("scope mismatch accepted", s, err)
			}
		})
	}
}

func TestFullEmailStopDoesNotDependOnRouteMetadataReads(t *testing.T) {
	r, admin, in, _ := standardPolicyFixture(t)
	ctx := context.Background()
	full := true
	in.FullEmailAllowed = &full
	s, err := r.SaveStandardPolicy(ctx, admin, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.pool.Exec(ctx, `ALTER TABLE ai_routes_state RENAME TO unavailable_routes_state`); err != nil {
		t.Fatal(err)
	}
	s, err = r.SavePolicy(ctx, admin, mailprocessing.PolicyInput{ProfileID: in.ProfileID, ExpectedVersion: s.Policy.Version})
	if err != nil || s.Policy.Enabled || !s.Policy.FullEmailAllowed || s.StandardTerms != nil {
		t.Fatal("stop depends on route metadata or loses prior scope", s, err)
	}
}
