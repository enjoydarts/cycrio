//go:build integration

package database

import (
	"context"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/google/uuid"
	"testing"
)

func TestMailProcessingQueuedWorkBehindHealthyLeasesAndPartialClaimCommit(t *testing.T) {
	f := processingSetup(t)
	f.add(t, 1)
	ctx := context.Background()
	run, e := f.repo.Request(ctx, f.owner, f.account, f.in)
	if e != nil {
		t.Fatal(e)
	}
	// Healthy deliveries must not consume the bounded candidate window.
	tx, e := f.runtime.pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	var credential string
	if e = tx.QueryRow(ctx, `SELECT id FROM mail_account_credentials WHERE account_id=$1`, f.account).Scan(&credential); e != nil {
		t.Fatal(e)
	}
	for range 100 {
		a, c := uuid.NewString(), uuid.NewString()
		if _, e = tx.Exec(ctx, `INSERT INTO mail_accounts(id,owner_id,label,state,version,credential_version)VALUES($1,$2,'synthetic','ready',1,1)`, a, f.owner); e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(ctx, `INSERT INTO mail_account_credentials(id,owner_id,account_id,version,key_id,ciphertext,state)VALUES($1,$2,$3,1,'synthetic',decode('01','hex'),'active')`, c, f.owner, a); e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(ctx, `INSERT INTO mail_processing_runs(id,owner_id,account_id,request_id,request_doc,config_version,credential_id,runtime_version,profile_id,policy_version,max_messages,total_count,state,lease_id,lease_until,created_at) VALUES($1,$2,$3,$4,'{}',1,$5,$6,$7,1,1,0,'leased',$8,clock_timestamp()+interval '2 minutes',clock_timestamp()-interval '1 day')`, uuid.NewString(), f.owner, a, uuid.NewString(), c, f.in.RuntimeVersion, f.in.ProfileID, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	leases, e := f.repo.Claim(ctx)
	if e != nil || len(leases) != 1 || leases[0].RunID != run.ID {
		t.Fatal("healthy leases hid queued work", leases, e)
	}
	if _, e = f.repo.Start(ctx, leases[0]); e != nil {
		t.Fatal(e)
	}
	label := "not_related"
	if e = f.repo.Finish(ctx, leases[0], mailprocessing.Completion{State: "not_related", Classification: &label}); e != nil {
		t.Fatal(e)
	}
	// Two queued candidates: a DB failure on the later one must not hide the
	// already committed delivery from the caller.
	f.add(t, 1)
	in := f.in
	in.RequestID = uuid.NewString()
	first, e := f.repo.Request(ctx, f.owner, f.account, in)
	if e != nil {
		t.Fatal(e)
	}
	otherAccount := uuid.NewString()
	var otherCredential string
	if e = f.runtime.pool.QueryRow(ctx, `SELECT account_id,id FROM mail_account_credentials WHERE account_id<>$1 ORDER BY account_id LIMIT 1`, f.account).Scan(&otherAccount, &otherCredential); e != nil {
		t.Fatal(e)
	}
	folder, message, second := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, e = f.runtime.pool.Exec(ctx, `UPDATE mail_processing_runs SET state='cancelled',lease_id=NULL,lease_until=NULL WHERE account_id=$1`, otherAccount); e != nil {
		t.Fatal(e)
	}
	if _, e = f.runtime.pool.Exec(ctx, `INSERT INTO mail_account_folders(id,account_id,name,delimiter,selected,available,selectable)VALUES($1,$2,'synthetic','/',true,true,true)`, folder, otherAccount); e != nil {
		t.Fatal(e)
	}
	if _, e = f.runtime.pool.Exec(ctx, `INSERT INTO mail_discovered_messages(id,owner_id,account_id,folder_id,fingerprint,key_id,ciphertext)VALUES($1,$2,$3,$4,'synthetic','synthetic',decode('01','hex'))`, message, f.owner, otherAccount, folder); e != nil {
		t.Fatal(e)
	}
	if _, e = f.runtime.pool.Exec(ctx, `INSERT INTO mail_processing_runs(id,owner_id,account_id,request_id,request_doc,config_version,credential_id,runtime_version,profile_id,policy_version,max_messages,total_count,state)VALUES($1,$2,$3,$4,'{}',1,$5,$6,$7,1,1,1,'queued')`, second, f.owner, otherAccount, uuid.NewString(), otherCredential, f.in.RuntimeVersion, f.in.ProfileID); e != nil {
		t.Fatal(e)
	}
	if _, e = f.runtime.pool.Exec(ctx, `INSERT INTO mail_processing_items(id,owner_id,account_id,run_id,message_id,state)VALUES($1,$2,$3,$4,$5,'queued')`, uuid.NewString(), f.owner, otherAccount, second, message); e != nil {
		t.Fatal(e)
	}
	if _, e = f.runtime.pool.Exec(ctx, `ALTER TABLE mail_processing_runs ADD CONSTRAINT reject_second_lease CHECK(NOT (id='`+second+`'::uuid AND state='leased')) NOT VALID`); e != nil {
		t.Fatal(e)
	}
	leases, e = f.repo.Claim(ctx)
	if e != nil || len(leases) != 1 || leases[0].RunID != first.ID {
		t.Fatal("partial committed delivery lost", leases, e)
	}
	var persisted string
	if e = f.runtime.pool.QueryRow(ctx, `SELECT lease_id::text FROM mail_processing_runs WHERE id=$1`, first.ID).Scan(&persisted); e != nil || persisted != leases[0].LeaseID {
		t.Fatal("uncommitted delivery returned", persisted, e)
	}
}
