//go:build integration

package database

import (
	"context"
	"errors"
	"testing"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/mailautomation"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"github.com/google/uuid"
)

func TestMailAutomationAdminModelActivationWaitsForPermissionThenUsesNewProfile(t *testing.T) {
	f, r, in := automaticFixture(t)
	ctx := context.Background()
	v := automaticBootstrap(t, f, r, in, 1)
	automaticDiscover(t, f, r, syncRefs(1), 1)
	l, _ := f.start(t)
	state, e := f.runtime.State(ctx)
	if e != nil {
		t.Fatal(e)
	}
	catalog := NewModelCatalogRepository(f.runtime.pool)
	model, e := catalog.AddModel(ctx, f.admin, modelcatalog.ManualModelInput{Provider: "openai", ModelID: "synthetic-replacement", DisplayName: "Replacement"})
	if e != nil {
		t.Fatal(e)
	}
	settings, e := catalog.Settings(ctx)
	if e != nil {
		t.Fatal(e)
	}
	settings, e = catalog.SaveSettings(ctx, f.admin, modelcatalog.SettingsInput{ExpectedVersion: settings.Version, PrimaryModelID: state.Active.Primary.ID, SecondaryModelID: model.ID})
	if e != nil {
		t.Fatal(e)
	}
	state, e = f.runtime.SaveProfile(ctx, f.admin, airuntime.ConfigInput{ExpectedVersion: state.Version, CatalogSettingsID: *settings.ID, MaxInputBytes: 4096, MaxOutputTokens: 512, TimeoutSeconds: 30})
	if e != nil {
		t.Fatal(e)
	}
	probe, e := f.runtime.RequestTest(ctx, f.admin, airuntime.TestInput{ExpectedVersion: state.Version, ProfileID: state.Draft.ID, RequestID: uuid.NewString()})
	if e != nil {
		t.Fatal(e)
	}
	leases, e := f.runtime.Claim(ctx)
	if e != nil || len(leases) != 1 {
		t.Fatal(leases, e)
	}
	if _, e = f.runtime.StartTest(ctx, leases[0]); e != nil {
		t.Fatal(e)
	}
	success, label, n := "success", "subscription_related", 1
	if e = f.runtime.FinishTest(ctx, leases[0], airuntime.TestCompletion{State: "succeeded", PrimaryOutcome: &success, SecondaryOutcome: &success, Classification: &label, ExtractedEvents: &n}); e != nil {
		t.Fatal(e)
	}
	state, e = f.runtime.Activate(ctx, f.admin, airuntime.ActivationInput{ExpectedVersion: state.Version, ProfileID: state.Draft.ID, TestID: probe.ID})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.repo.BeginCall(ctx, l, "classification"); !errors.Is(e, mailprocessing.ErrNotFound) {
		t.Fatal("old model run sent", e)
	}
	if _, e = r.Tick(ctx, 10); e != nil {
		t.Fatal(e)
	}
	got, e := r.Read(ctx, f.owner, f.account)
	if e != nil || got.State != "enabled" || got.Ready || got.Reason != "policy_disabled" || got.Generation != v.Generation {
		t.Fatal("new model permission wait revoked owner scope", got, e)
	}
	allowed := true
	policy, e := f.repo.SavePolicy(ctx, f.admin, mailprocessing.PolicyInput{ProfileID: state.Active.ID, Enabled: true, FullEmailAllowed: &allowed, PrimaryRetention: "Synthetic contract", SecondaryRetention: "Synthetic contract", PrimaryConfirmed: true, SecondaryConfirmed: true})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.Tick(ctx, 10); e != nil {
		t.Fatal(e)
	}
	got, e = r.Read(ctx, f.owner, f.account)
	if e != nil || got.State != "enabled" || !got.Ready || got.Generation != v.Generation || got.Progress.DiscoveredCount != 1 {
		t.Fatal(got, e)
	}
	var profile, initial string
	var frozen int64
	if e = f.runtime.pool.QueryRow(ctx, `SELECT r.profile_id,s.profile_id,r.policy_version FROM mail_processing_runs r JOIN mail_automation_scopes s ON s.automation_id=r.automation_id AND s.generation=r.automation_generation WHERE r.automation_id=$1 AND r.state='queued'`, v.ID).Scan(&profile, &initial, &frozen); e != nil || profile != state.Active.ID || initial != in.ProfileID || frozen != policy.Policy.Version {
		t.Fatal("run did not freeze new approved model", profile, initial, frozen, e)
	}
	_, work := f.start(t)
	if work.Profile.Secondary.ModelID != "synthetic-replacement" {
		t.Fatal("new run retained old model", work.Profile.Secondary)
	}
}

func TestMailAutomationAdminPolicyChangeUsesCurrentBasisWithoutNewScope(t *testing.T) {
	f, r, in := automaticFixture(t)
	ctx := context.Background()
	v := automaticBootstrap(t, f, r, in, 1)
	updated := fullEmailPolicy(t, f, true, in.PolicyVersion)
	automaticDiscover(t, f, r, syncRefs(1), 1)
	got, e := r.Read(ctx, f.owner, f.account)
	if e != nil || got.State != "enabled" || !got.Ready || got.Generation != v.Generation || got.Scope.Since != in.Since || got.Progress.DiscoveredCount != 1 {
		t.Fatal("administrator policy forced owner reconfirmation", got, e)
	}
	var frozen, initial int64
	if e = f.runtime.pool.QueryRow(ctx, `SELECT r.policy_version,s.policy_version FROM mail_processing_runs r JOIN mail_automation_scopes s ON s.automation_id=r.automation_id AND s.generation=r.automation_generation WHERE r.automation_id=$1`, v.ID).Scan(&frozen, &initial); e != nil || frozen != updated.Policy.Version || initial != in.PolicyVersion {
		t.Fatal("current run basis or immutable initial scope lost", frozen, initial, e)
	}
}

func TestMailAutomationAdminWaitingRecoversWithoutOwnerAndHonorsStop(t *testing.T) {
	for _, stop := range []bool{false, true} {
		t.Run(map[bool]string{false: "automatic recovery", true: "explicit stop"}[stop], func(t *testing.T) {
			f, r, in := automaticFixture(t)
			ctx := context.Background()
			v := automaticBootstrap(t, f, r, in, 1)
			fullEmailPolicy(t, f, false, in.PolicyVersion)
			for range 2 {
				if _, e := r.Tick(ctx, 10); e != nil {
					t.Fatal(e)
				}
			}
			got, e := r.Read(ctx, f.owner, f.account)
			if e != nil || got.State != "enabled" || got.Ready || got.Reason != "full_email_not_allowed" || got.Generation != v.Generation {
				t.Fatal("AI waiting became owner confirmation", got, e)
			}
			var pauses int
			if e = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM mail_automation_audit WHERE automation_id=$1 AND action='paused'`, v.ID).Scan(&pauses); e != nil || pauses != 1 {
				t.Fatal("waiting audit repeated", pauses, e)
			}
			if stop {
				got, e = r.Stop(ctx, f.owner, f.account, mailautomation.Control{ExpectedSettingsVersion: got.Version, RequestID: uuid.NewString()})
				if e != nil || got.State != "stopped" {
					t.Fatal(got, e)
				}
			}
			fullEmailPolicy(t, f, true, in.PolicyVersion+1)
			if _, e = r.Tick(ctx, 10); e != nil {
				t.Fatal(e)
			}
			got, e = r.Read(ctx, f.owner, f.account)
			want := "enabled"
			if stop {
				want = "stopped"
			}
			if e != nil || got.State != want || got.Reason != "" || !got.Ready || got.Generation != v.Generation {
				t.Fatal("recovery changed owner scope/stop", got, e)
			}
		})
	}
}

func TestMailAutomationLegacyAdminPauseRecoversOnlyWithUnchangedAccount(t *testing.T) {
	for _, reason := range []string{"runtime_inactive", "policy_disabled", "full_email_not_allowed", "policy_reconfirmation_required", "configuration_unavailable", "configuration_changed", "authentication_failed", "uid_validity_changed", "account_changed"} {
		for _, changed := range []bool{false, true} {
			t.Run(reason+map[bool]string{false: "", true: "/account changed"}[changed], func(t *testing.T) {
				f, r, in := automaticFixture(t)
				ctx := context.Background()
				v := automaticBootstrap(t, f, r, in, 1)
				if _, e := f.runtime.pool.Exec(ctx, `UPDATE mail_automations SET state='needs_confirmation',reason=$2 WHERE id=$1`, v.ID, reason); e != nil {
					t.Fatal(e)
				}
				if changed {
					if _, e := f.runtime.pool.Exec(ctx, `UPDATE mail_accounts SET version=version+1 WHERE id=$1`, f.account); e != nil {
						t.Fatal(e)
					}
				}
				if _, e := r.Tick(ctx, 10); e != nil {
					t.Fatal(e)
				}
				got, e := r.Read(ctx, f.owner, f.account)
				ownerReason := reason == "authentication_failed" || reason == "uid_validity_changed" || reason == "account_changed"
				want := "enabled"
				if changed || ownerReason {
					want = "needs_confirmation"
				}
				if e != nil || got.State != want || got.Generation != v.Generation {
					t.Fatal("unsafe legacy recovery", got, e)
				}
			})
		}
	}
}

func TestMailAutomationMidRunAdminChangeRequeuesOnlyUnsentAndKeepsLateCost(t *testing.T) {
	for _, sent := range []bool{false, true} {
		t.Run(map[bool]string{false: "unsent", true: "sent"}[sent], func(t *testing.T) {
			f, r, in := automaticFixture(t)
			ctx := context.Background()
			v := automaticBootstrap(t, f, r, in, 3)
			automaticDiscover(t, f, r, syncRefs(3), 3)
			completed, _ := f.start(t)
			label := "not_related"
			if e := f.repo.Finish(ctx, completed, mailprocessing.Completion{State: "not_related", Classification: &label}); e != nil {
				t.Fatal(e)
			}
			l, _ := f.start(t)
			var call airuntime.Call
			var e error
			if sent {
				call, e = f.repo.BeginCall(ctx, l, "classification")
				if e != nil {
					t.Fatal(e)
				}
			}
			policy := fullEmailPolicy(t, f, true, in.PolicyVersion)
			if _, e = f.repo.BeginCall(ctx, l, "classification"); !errors.Is(e, mailprocessing.ErrNotFound) {
				t.Fatal("stale run sent", e)
			}
			if _, e = r.Tick(ctx, 10); e != nil {
				t.Fatal(e)
			}
			got, e := r.Read(ctx, f.owner, f.account)
			if e != nil || got.State != "enabled" || !got.Ready || got.Generation != v.Generation || got.Progress.DiscoveredCount != 3 || got.Progress.CompletedCount != 1 || !got.Progress.HistoricalComplete || got.Progress.PendingCount != map[bool]int64{false: 2, true: 1}[sent] || got.Progress.UnknownCount != map[bool]int64{false: 0, true: 1}[sent] {
				t.Fatal("progress lost", got, e)
			}
			var count int
			if e = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM mail_processing_items i JOIN mail_processing_runs r ON r.id=i.run_id WHERE r.state='queued' AND r.policy_version=$1`, policy.Policy.Version).Scan(&count); e != nil || count != map[bool]int{false: 2, true: 1}[sent] {
				t.Fatal("unsafe resend or lost pending", count, e)
			}
			if sent {
				if _, e = f.repo.FinishCall(ctx, call.ID, airuntime.CallUpdate{Outcome: "success", Cost: ai.Cost{Status: "unknown"}}); e != nil {
					t.Fatal("late cost lost", e)
				}
			}
		})
	}
}

func TestMailAutomationWorkerOwnerFailureCannotBecomeRecoverableAIWait(t *testing.T) {
	for _, problem := range []string{"account", "folders", "credentials"} {
		t.Run(problem, func(t *testing.T) {
			f, r, in := automaticFixture(t)
			ctx := context.Background()
			automaticBootstrap(t, f, r, in, 1)
			automaticDiscover(t, f, r, syncRefs(1), 1)
			l, _ := f.start(t)
			queries := map[string]string{
				"account":     `UPDATE mail_accounts SET state='needs_reauth' WHERE id=$1`,
				"folders":     `UPDATE mail_account_folders SET selected=false,available=false WHERE account_id=$1`,
				"credentials": `UPDATE mail_account_credentials SET state='disabled' WHERE account_id=$1`,
			}
			if _, e := f.runtime.pool.Exec(ctx, queries[problem], f.account); e != nil {
				t.Fatal(e)
			}
			if _, e := f.repo.BeginCall(ctx, l, "classification"); !errors.Is(e, mailprocessing.ErrNotFound) {
				t.Fatal(e)
			}
			// The account-state trigger already cancels the run; Tick records its
			// owner confirmation. Folder/credential failures reach the job guard.
			if problem == "account" {
				if _, e := r.Tick(ctx, 10); e != nil {
					t.Fatal(e)
				}
			}
			var state, reason string
			if e := f.runtime.pool.QueryRow(ctx, `SELECT state,reason FROM mail_automations WHERE account_id=$1`, f.account).Scan(&state, &reason); e != nil || state != "needs_confirmation" || reason == "configuration_changed" {
				t.Fatal("owner problem recorded as AI wait", state, reason, e)
			}
			if _, e := r.Tick(ctx, 10); e != nil {
				t.Fatal(e)
			}
			restore := map[string]string{
				"account":     `UPDATE mail_accounts SET state='ready' WHERE id=$1`,
				"folders":     `UPDATE mail_account_folders SET available=true,selected=true WHERE account_id=$1`,
				"credentials": `UPDATE mail_account_credentials SET state='active' WHERE account_id=$1`,
			}
			if _, e := f.runtime.pool.Exec(ctx, restore[problem], f.account); e != nil {
				t.Fatal(e)
			}
			if _, e := r.Tick(ctx, 10); e != nil {
				t.Fatal(e)
			}
			if e := f.runtime.pool.QueryRow(ctx, `SELECT state FROM mail_automations WHERE account_id=$1`, f.account).Scan(&state); e != nil || state != "needs_confirmation" {
				t.Fatal("restored owner source automatically resumed", state, e)
			}
		})
	}
}

func TestMailAutomationWaitingTickDoesNotStarveLaterAccounts(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "same wait", true: "legacy with changed account"}[legacy], func(t *testing.T) {
			f, r, in := automaticFixture(t)
			ctx := context.Background()
			v, e := r.Save(ctx, f.owner, f.account, in)
			if e != nil {
				t.Fatal(e)
			}
			second := uuid.NewString()
			if _, e = f.runtime.pool.Exec(ctx, `INSERT INTO mail_accounts(id,owner_id,label,state,version,credential_version)VALUES($1,$2,'Second','ready',1,1)`, second, f.owner); e != nil {
				t.Fatal(e)
			}
			if _, e = f.runtime.pool.Exec(ctx, `INSERT INTO mail_account_credentials(id,account_id,owner_id,version,key_id,ciphertext,state) SELECT $1,$2,owner_id,1,key_id,ciphertext,'active' FROM mail_account_credentials WHERE account_id=$3`, uuid.NewString(), second, f.account); e != nil {
				t.Fatal(e)
			}
			if _, e = f.runtime.pool.Exec(ctx, `INSERT INTO mail_account_folders(id,account_id,name,delimiter,selectable,available,selected)VALUES($1,$2,'INBOX','/',true,true,true)`, uuid.NewString(), second); e != nil {
				t.Fatal(e)
			}
			in.RequestID = uuid.NewString()
			other, e := r.Save(ctx, f.owner, second, in)
			if e != nil {
				t.Fatal(e)
			}
			fullEmailPolicy(t, f, false, in.PolicyVersion)
			if _, e = f.runtime.pool.Exec(ctx, `UPDATE mail_automations SET reason='full_email_not_allowed',updated_at=clock_timestamp()-interval '2 hours' WHERE id=$1`, v.ID); e != nil {
				t.Fatal(e)
			}
			if legacy {
				if _, e = f.runtime.pool.Exec(ctx, `UPDATE mail_accounts SET version=version+1 WHERE id=$1`, f.account); e != nil {
					t.Fatal(e)
				}
				if _, e = f.runtime.pool.Exec(ctx, `UPDATE mail_automations SET state='needs_confirmation',reason='configuration_changed' WHERE id=$1`, v.ID); e != nil {
					t.Fatal(e)
				}
			}
			if _, e = f.runtime.pool.Exec(ctx, `UPDATE mail_automations SET updated_at=clock_timestamp()-interval '1 hour' WHERE id=$1`, other.ID); e != nil {
				t.Fatal(e)
			}
			for range 2 {
				if _, e = r.Tick(ctx, 1); e != nil {
					t.Fatal(e)
				}
			}
			var reason string
			if e = f.runtime.pool.QueryRow(ctx, `SELECT reason FROM mail_automations WHERE id=$1`, other.ID).Scan(&reason); e != nil || reason != "full_email_not_allowed" {
				t.Fatal("old waiting account starved a later account", reason, e)
			}
		})
	}
}

func TestMailAutomationLegacyAIStopCannotRecover(t *testing.T) {
	for _, auditStop := range []bool{false, true} {
		t.Run(map[bool]string{false: "new explicit stop", true: "legacy stop audit"}[auditStop], func(t *testing.T) {
			f, r, in := automaticFixture(t)
			ctx := context.Background()
			v, e := r.Save(ctx, f.owner, f.account, in)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = f.runtime.pool.Exec(ctx, `UPDATE mail_automations SET state='needs_confirmation',reason='configuration_changed' WHERE id=$1`, v.ID); e != nil {
				t.Fatal(e)
			}
			if auditStop {
				if _, e = f.runtime.pool.Exec(ctx, `INSERT INTO mail_automation_audit(id,owner_id,account_id,automation_id,generation,version,action,reason)VALUES($1,$2,$3,$4,1,2,'stopped','configuration_changed')`, uuid.NewString(), f.owner, f.account, v.ID); e != nil {
					t.Fatal(e)
				}
			} else {
				v, e = r.Stop(ctx, f.owner, f.account, mailautomation.Control{ExpectedSettingsVersion: v.Version, RequestID: uuid.NewString()})
				if e != nil || v.State != "stopped" {
					t.Fatal("legacy AI stop stayed recoverable", v, e)
				}
			}
			if _, e = r.Tick(ctx, 10); e != nil {
				t.Fatal(e)
			}
			var state string
			if e = f.runtime.pool.QueryRow(ctx, `SELECT state FROM mail_automations WHERE id=$1`, v.ID).Scan(&state); e != nil || state == "enabled" {
				t.Fatal("explicitly stopped automation recovered", state, e)
			}
		})
	}
}
