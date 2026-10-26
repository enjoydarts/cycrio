//go:build integration

package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"io/fs"
	"strings"
	"testing"
	"time"
)

func TestMailProcessingStopsRemainPermanentAndKeepLateBilling(t *testing.T) {
	for _, kind := range []string{"cancel", "user", "account_version", "disconnect", "rotate", "runtime", "policy", "folder", "key", "model"} {
		t.Run(kind, func(t *testing.T) {
			f := processingSetup(t)
			f.add(t, 2)
			ctx := context.Background()
			run, e := f.repo.Request(ctx, f.owner, f.account, f.in)
			if e != nil {
				t.Fatal(e)
			}
			l, w := f.start(t)
			call, e := f.repo.BeginCall(ctx, l, "classification")
			if e != nil {
				t.Fatal(e)
			}
			switch kind {
			case "cancel":
				_, e = f.repo.Cancel(ctx, f.owner, f.account, run.ID)
			case "user":
				_, e = f.runtime.pool.Exec(ctx, `UPDATE users SET state='disabled' WHERE id=$1`, f.owner)
				if e == nil {
					_, e = f.runtime.pool.Exec(ctx, `UPDATE users SET state='active' WHERE id=$1`, f.owner)
				}
			case "account_version":
				_, e = f.runtime.pool.Exec(ctx, `UPDATE mail_accounts SET version=version+1 WHERE id=$1`, f.account)
			case "disconnect":
				_, e = NewMailAccountRepository(f.runtime.pool).Disconnect(ctx, f.owner, f.account, 1)
			case "rotate":
				cv := mailaccounts.CredentialVersion{ID: uuid.NewString(), OwnerID: f.owner, AccountID: f.account, Version: 2}
				cv.Envelope, e = f.keys.Seal([]byte(`{"login":"c3ludGhldGlj","password":"bmV3"}`), mailaccounts.CredentialAAD(cv))
				if e == nil {
					_, e = NewMailAccountRepository(f.runtime.pool).Save(ctx, f.owner, f.account, 1, "Rotated", cv)
				}
			case "runtime":
				_, e = f.runtime.Deactivate(ctx, f.admin, f.in.RuntimeVersion)
				if e == nil {
					_, e = f.runtime.pool.Exec(ctx, `UPDATE ai_runtime_settings SET version=version+1,active_id=$1 WHERE singleton`, f.in.ProfileID)
				}
			case "policy":
				var s mailprocessing.PolicyState
				s, e = f.repo.SavePolicy(ctx, f.admin, mailprocessing.PolicyInput{ExpectedVersion: 1, ProfileID: f.in.ProfileID})
				if e == nil {
					_, e = f.repo.SavePolicy(ctx, f.admin, mailprocessing.PolicyInput{ExpectedVersion: s.Policy.Version, ProfileID: f.in.ProfileID, Enabled: true, PrimaryRetention: "Reconfirmed", SecondaryRetention: "Reconfirmed", PrimaryConfirmed: true, SecondaryConfirmed: true})
				}
			case "folder":
				_, e = f.runtime.pool.Exec(ctx, `DELETE FROM mail_account_folders WHERE id=$1`, f.folder)
			case "key":
				_, e = f.runtime.pool.Exec(ctx, `UPDATE credential_versions SET state='disabled' WHERE id=$1`, w.Profile.Primary.CredentialID)
			case "model":
				_, e = f.runtime.pool.Exec(ctx, `UPDATE model_catalog SET doc=jsonb_set(doc,'{enabled}','false') WHERE id=$1`, w.Profile.Primary.ID)
			}
			if e != nil {
				t.Fatal(e)
			}
			if _, e = f.repo.BeginCall(ctx, l, "extraction"); !errors.Is(e, mailprocessing.ErrNotFound) {
				t.Fatal("stopped run allowed call", e)
			}
			// Usage from a request already sent before the stop remains attributable.
			usd, amount := "USD", "0.12"
			if _, e = f.repo.FinishCall(ctx, call.ID, airuntime.CallUpdate{Outcome: "success", Cost: ai.Cost{Status: "provider_reported", Amount: &amount, Currency: &usd}}); e != nil {
				t.Fatal("late billing discarded", e)
			}
			label := "subscription_related"
			if e = f.repo.Finish(ctx, l, mailprocessing.Completion{State: "succeeded", Classification: &label, Events: []ai.Event{{Service: "Synthetic", EventType: "payment"}}}); e != nil {
				t.Fatal(e)
			}
			results, e := f.repo.Results(ctx, f.owner, f.account)
			if e != nil || len(results) != 1 || results[0].State != "unknown" || len(results[0].Events) != 0 {
				t.Fatal("late result adopted", results, e)
			}
			runs, e := f.repo.ListRuns(ctx, f.owner, f.account)
			if e != nil || runs[0].State != "cancelled" || runs[0].UnknownCount != 1 {
				t.Fatal(runs, e)
			}
			ls, e := f.repo.Claim(ctx)
			if e != nil || len(ls) != 0 {
				t.Fatal("stopped run redelivered", ls, e)
			}
			if _, e = f.repo.Start(ctx, l); !errors.Is(e, mailprocessing.ErrNotFound) {
				t.Fatal("old lease revived", e)
			}
		})
	}
}

func TestMailProcessingLeaseExpiryAndClockAfterLock(t *testing.T) {
	for _, stage := range []string{"claim", "start", "begin", "finish"} {
		t.Run(stage, func(t *testing.T) {
			f := processingSetup(t)
			f.add(t, 2)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			run, e := f.repo.Request(ctx, f.owner, f.account, f.in)
			if e != nil {
				t.Fatal(e)
			}
			ls, e := f.repo.Claim(ctx)
			if e != nil || len(ls) != 1 {
				t.Fatal(ls, e)
			}
			l := ls[0]
			if stage != "start" {
				if _, e = f.repo.Start(ctx, l); e != nil {
					t.Fatal(e)
				}
			}
			block, e := f.runtime.pool.Begin(ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer block.Rollback(ctx)
			if _, e = block.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, f.owner); e != nil {
				t.Fatal(e)
			}
			done := make(chan error, 1)
			go func() {
				var err error
				switch stage {
				case "claim":
					var leases []mailprocessing.Lease
					leases, err = f.repo.Claim(ctx)
					if err == nil && len(leases) != 0 {
						err = errors.New("running redelivery")
					}
				case "start":
					_, err = f.repo.Start(ctx, l)
				case "begin":
					_, err = f.repo.BeginCall(ctx, l, "classification")
				case "finish":
					label := "not_related"
					err = f.repo.Finish(ctx, l, mailprocessing.Completion{State: "not_related", Classification: &label})
				}
				done <- err
			}()
			if stage == "claim" { // Make it a candidate, then explicitly run the next claim after expiry.
				if _, e = f.runtime.pool.Exec(ctx, `UPDATE mail_processing_runs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, run.ID); e != nil {
					t.Fatal(e)
				}
				if e = block.Commit(ctx); e != nil {
					t.Fatal(e)
				}
				if e = <-done; e != nil {
					t.Fatal(e)
				}
				if ls, e = f.repo.Claim(ctx); e != nil || len(ls) != 0 {
					t.Fatal(ls, e)
				}
			} else {
				started := waitProcessingLock(t, f.runtime.pool, `SELECT state FROM users WHERE id=$1 FOR NO KEY UPDATE`)
				if _, e = f.runtime.pool.Exec(ctx, `UPDATE mail_processing_runs SET lease_until=$2::timestamptz+interval '1 microsecond' WHERE id=$1`, run.ID, started); e != nil {
					t.Fatal(e)
				}
				if e = block.Commit(ctx); e != nil {
					t.Fatal(e)
				}
				e = <-done
				if stage == "finish" {
					if e != nil {
						t.Fatal(e)
					}
				} else if !errors.Is(e, mailprocessing.ErrNotFound) {
					t.Fatal("expired lease accepted", stage, e)
				}
			}
			var state string
			if e = f.runtime.pool.QueryRow(ctx, `SELECT state FROM mail_processing_runs WHERE id=$1`, run.ID).Scan(&state); e != nil {
				t.Fatal(e)
			}
			if stage == "start" {
				if state != "leased" {
					t.Fatal(state)
				}
				ls, e = f.repo.Claim(ctx)
				if e != nil || len(ls) != 1 || ls[0].LeaseID == l.LeaseID {
					t.Fatal("unstarted lease redelivery", ls, e)
				}
			} else {
				if state != "failed" {
					t.Fatal(state)
				}
				results, e := f.repo.Results(ctx, f.owner, f.account)
				if e != nil || len(results) != 1 || results[0].State != "unknown" || results[0].ErrorCode != "lease_expired" {
					t.Fatal(results, e)
				}
			}
		})
	}
}

func TestMailProcessingSelectedSnapshotAndAuditRollback(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	ids := f.add(t, 2)
	hidden := uuid.NewString()
	if _, e := f.runtime.pool.Exec(ctx, `INSERT INTO mail_account_folders(id,account_id,name,delimiter,selectable,available,selected)VALUES($1,$2,'unselected','/',true,true,false)`, hidden, f.account); e != nil {
		t.Fatal(e)
	}
	if _, e := f.runtime.pool.Exec(ctx, `UPDATE mail_discovered_messages SET folder_id=$2 WHERE id=$1`, ids[1], hidden); e != nil {
		t.Fatal(e)
	}
	if _, e := f.runtime.pool.Exec(ctx, `ALTER TABLE mail_processing_audit ADD CONSTRAINT reject_processing CHECK(action<>'processing_requested') NOT VALID`); e != nil {
		t.Fatal(e)
	}
	if _, e := f.repo.Request(ctx, f.owner, f.account, f.in); !errors.Is(e, mailprocessing.ErrUnavailable) {
		t.Fatal("audit failure ignored", e)
	}
	var count int
	if e := f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM mail_processing_runs`).Scan(&count); e != nil || count != 0 {
		t.Fatal("run survived failed audit", count, e)
	}
	if _, e := f.runtime.pool.Exec(ctx, `ALTER TABLE mail_processing_audit DROP CONSTRAINT reject_processing`); e != nil {
		t.Fatal(e)
	}
	run, e := f.repo.Request(ctx, f.owner, f.account, f.in)
	if e != nil || run.TotalCount != 1 {
		t.Fatal("selected snapshot", run, e)
	}
	f.add(t, 1)
	l, w := f.start(t)
	if w.MessageID != ids[0] {
		t.Fatal("unselected source included")
	}
	label := "not_related"
	if e = f.repo.Finish(ctx, l, mailprocessing.Completion{State: "not_related", Classification: &label}); e != nil {
		t.Fatal(e)
	}
	ls, e := f.repo.Claim(ctx)
	if e != nil || len(ls) != 0 {
		t.Fatal("post-request source included", ls, e)
	}
	// Policy updates also roll back with their own audit entry.
	if _, e = f.runtime.pool.Exec(ctx, `ALTER TABLE mail_processing_policy_audit ADD CONSTRAINT reject_policy CHECK(false) NOT VALID`); e != nil {
		t.Fatal(e)
	}
	if _, e = f.repo.SavePolicy(ctx, f.admin, mailprocessing.PolicyInput{ExpectedVersion: 1, ProfileID: f.in.ProfileID}); !errors.Is(e, mailprocessing.ErrUnavailable) {
		t.Fatal(e)
	}
	state, e := f.repo.AdminRead(ctx, f.admin)
	if e != nil || state.Policy.Version != 1 || !state.Policy.Enabled {
		t.Fatal("policy changed without audit", state, e)
	}
}

func TestMailProcessingFirstPolicyWriteCAS(t *testing.T) {
	f := processingSetup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, e := f.runtime.pool.Exec(ctx, `DELETE FROM mail_processing_policy_audit; DELETE FROM mail_processing_policies`); e != nil {
		t.Fatal(e)
	}
	if _, e := f.runtime.pool.Exec(ctx, `CREATE FUNCTION block_policy_insert() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN PERFORM pg_advisory_xact_lock(71267126); RETURN NEW; END$$; CREATE TRIGGER block_policy BEFORE INSERT ON mail_processing_policies FOR EACH ROW EXECUTE FUNCTION block_policy_insert()`); e != nil {
		t.Fatal(e)
	}
	block, e := f.runtime.pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer block.Rollback(ctx)
	if _, e = block.Exec(ctx, `SELECT pg_advisory_xact_lock(71267126)`); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 2)
	for range 2 {
		go func() {
			_, e := f.repo.SavePolicy(ctx, f.admin, mailprocessing.PolicyInput{ProfileID: f.in.ProfileID})
			done <- e
		}()
	}
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting int
		if e = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE 'INSERT INTO mail_processing_policies(%'`).Scan(&waiting); e != nil {
			t.Fatal(e)
		}
		if waiting == 2 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("writers did not reach first-insert race")
		case <-ticker.C:
		}
	}
	if e = block.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	success, conflicts := 0, 0
	for range 2 {
		e = <-done
		if e == nil {
			success++
		} else if errors.Is(e, mailprocessing.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatal("first policy CAS winners", success, conflicts)
	}
}

func TestMailProcessingMigrationSevenDownUp(t *testing.T) {
	_, pool := testStore(t)
	ctx := context.Background()
	db, e := sql.Open("pgx", pool.Config().ConnString())
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	files, e := fs.Sub(migrations, "migrations")
	if e != nil {
		t.Fatal(e)
	}
	p, e := goose.NewProvider(goose.DialectPostgres, db, files)
	if e != nil {
		t.Fatal(e)
	}
	down, e := p.DownTo(ctx, 6)
	if e != nil || len(down) != 2 || down[0].Source.Version != 8 || down[1].Source.Version != 7 {
		t.Fatal(down, e)
	}
	var exists bool
	if e = pool.QueryRow(ctx, `SELECT to_regclass('mail_processing_runs') IS NULL AND to_regclass('mail_discovered_messages') IS NOT NULL AND to_regprocedure('stop_mail_processing(uuid,text,text)') IS NULL`).Scan(&exists); e != nil || !exists {
		t.Fatal("migration down", exists, e)
	}
	if _, e = p.Up(ctx); e != nil {
		t.Fatal(e)
	}
	var version int
	if e = pool.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); e != nil || version != 8 {
		t.Fatal(version, e)
	}
}

func TestMailProcessingDTOsNeverExposeSourceOrCredentialReferences(t *testing.T) {
	f := processingSetup(t)
	f.add(t, 1)
	ctx := context.Background()
	p, e := f.repo.Preview(ctx, f.owner, f.account)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := json.Marshal(p)
	if e != nil {
		t.Fatal(e)
	}
	for _, word := range []string{"credential", "ciphertext", "key_id", "folder_id", "uid_validity", "workspace", "prompt_version"} {
		if strings.Contains(string(raw), word) {
			t.Fatal("public preview contains", word)
		}
	}
}

func TestMailProcessingItemCannotReferenceAnotherOwnersSource(t *testing.T) {
	f := processingSetup(t)
	f.add(t, 1)
	ctx := context.Background()
	run, e := f.repo.Request(ctx, f.owner, f.account, f.in)
	if e != nil {
		t.Fatal(e)
	}
	account, message := uuid.NewString(), uuid.NewString()
	if _, e = f.runtime.pool.Exec(ctx, `INSERT INTO mail_accounts(id,owner_id,label,state,version,credential_version)VALUES($1,$2,'Other owner','ready',1,1)`, account, f.other); e != nil {
		t.Fatal(e)
	}
	if _, e = f.runtime.pool.Exec(ctx, `INSERT INTO mail_discovered_messages(id,owner_id,account_id,folder_id,fingerprint,key_id,ciphertext)VALUES($1,$2,$3,$4,'synthetic','synthetic',decode('01','hex'))`, message, f.other, account, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if _, e = f.runtime.pool.Exec(ctx, `INSERT INTO mail_processing_items(id,owner_id,account_id,run_id,message_id,state)VALUES($1,$2,$3,$4,$5,'queued')`, uuid.NewString(), f.owner, f.account, run.ID, message); e == nil {
		t.Fatal("cross-owner source reference accepted")
	}
}
