//go:build integration

package database

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/mailautomation"
	"github.com/enjoydarts/cycrio/internal/mailsync"
	"github.com/google/uuid"
)

func activityJSON(t *testing.T, value any) map[string]json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]json.RawMessage
	if err = json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMailAutomationLastStartedBeyondRecentHistory(t *testing.T) {
	f, repo, in := automaticFixture(t)
	ctx := context.Background()
	v := automaticBootstrap(t, f, repo, in, 1)
	var started time.Time
	if err := f.runtime.pool.QueryRow(ctx, `SELECT max(started_at) FROM mail_sync_runs WHERE automation_id=$1`, v.ID).Scan(&started); err != nil {
		t.Fatal(err)
	}
	// Only one active run per account is allowed. Cancelled, never-started
	// requests can still displace all started attempts from the recent list.
	for range 20 {
		if _, err := f.runtime.pool.Exec(ctx, `INSERT INTO mail_sync_runs(id,owner_id,account_id,request_id,config_version,credential_id,max_messages,state,folder_count,automation_id,automation_generation,automation_version,mode) SELECT $1,owner_id,account_id,$2,config_version,credential_id,max_messages,'cancelled',folder_count,automation_id,automation_generation,automation_version,mode FROM mail_sync_runs WHERE automation_id=$3 LIMIT 1`, uuid.NewString(), uuid.NewString(), v.ID); err != nil {
			t.Fatal(err)
		}
	}
	// A different generation's newer start must not become this scope's start.
	if _, err := f.runtime.pool.Exec(ctx, `INSERT INTO mail_automation_scopes SELECT owner_id,account_id,automation_id,generation+1,account_version,runtime_version,profile_id,policy_version,since,before,full_email,interval_seconds,auto_register,confirmed_at FROM mail_automation_scopes WHERE automation_id=$1`, v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runtime.pool.Exec(ctx, `INSERT INTO mail_sync_runs(id,owner_id,account_id,request_id,config_version,credential_id,max_messages,state,folder_count,automation_id,automation_generation,automation_version,mode,started_at) SELECT $1,owner_id,account_id,$2,config_version,credential_id,max_messages,'completed',folder_count,automation_id,automation_generation+1,automation_version,mode,clock_timestamp() FROM mail_sync_runs WHERE automation_id=$3 LIMIT 1`, uuid.NewString(), uuid.NewString(), v.ID); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Read(ctx, f.owner, f.account)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.RecentSyncRuns) != 20 {
		t.Fatal("recent history contract changed")
	}
	for _, run := range got.RecentSyncRuns {
		if run.StartedAt != nil {
			t.Fatal("fixture did not hide all started attempts")
		}
	}
	var actual *time.Time
	if err = json.Unmarshal(activityJSON(t, got)["last_started_at"], &actual); err != nil || actual == nil || !actual.Equal(started) {
		t.Fatalf("last started lost beyond recent history: %v, %v", actual, err)
	}
	if _, err = repo.Read(ctx, f.other, f.account); !errors.Is(err, mailautomation.ErrNotFound) {
		t.Fatal("foreign owner could read activity", err)
	}
}

func TestMailAutomationActivityNeverInventsStart(t *testing.T) {
	f, repo, in := automaticFixture(t)
	ctx := context.Background()
	for _, configured := range []bool{false, true} {
		if configured {
			if _, err := repo.Save(ctx, f.owner, f.account, in); err != nil {
				t.Fatal(err)
			}
		}
		got, err := repo.Read(ctx, f.owner, f.account)
		if err != nil {
			t.Fatal(err)
		}
		if string(activityJSON(t, got)["last_started_at"]) != "null" {
			t.Fatal("unstarted automation must explicitly report null start")
		}
	}
}

func TestOverviewAutomationActivityAndStopReason(t *testing.T) {
	f, repo, in := automaticFixture(t)
	ctx := context.Background()
	v := automaticBootstrap(t, f, repo, in, 1)
	if _, err := repo.Tick(ctx, 10); err != nil {
		t.Fatal(err)
	}
	sync := NewMailSyncRepository(f.runtime.pool)
	read := func() map[string]json.RawMessage {
		t.Helper()
		report, err := NewOverviewRepository(f.runtime.pool).Read(ctx, f.owner)
		if err != nil || len(report.Connections) != 1 {
			t.Fatal("overview unavailable", err)
		}
		return activityJSON(t, report.Connections[0])
	}
	if string(read()["automation_running"]) != "false" {
		t.Fatal("queued request was reported as running")
	}
	leases, err := sync.Claim(ctx)
	if err != nil || len(leases) != 1 {
		t.Fatal("claim failed", err)
	}
	lease := leases[0]
	if string(read()["automation_running"]) != "false" {
		t.Fatal("leased request was reported as running before start")
	}
	if _, err = sync.Start(ctx, lease); err != nil {
		t.Fatal(err)
	}
	active := read()
	if string(active["automation_running"]) != "true" || string(active["last_started_at"]) == "null" || len(active["last_started_at"]) == 0 {
		t.Fatal("live running attempt missing from overview")
	}
	if _, err = f.runtime.pool.Exec(ctx, `UPDATE mail_sync_runs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, lease.RunID); err != nil {
		t.Fatal(err)
	}
	if string(read()["automation_running"]) != "false" {
		t.Fatal("expired worker lease was reported as live running")
	}
	if _, err = f.runtime.pool.Exec(ctx, `UPDATE mail_sync_runs SET lease_until=clock_timestamp()+interval '1 minute' WHERE id=$1`, lease.RunID); err != nil {
		t.Fatal(err)
	}
	if err := sync.Finish(ctx, lease, mailsync.Page{}, nil, "connection_failed"); err != nil {
		t.Fatal(err)
	}
	waiting := read()
	if string(waiting["automation_running"]) != "false" || string(waiting["automation_reason"]) != `"connection_failed"` || string(waiting["retry_at"]) == "null" || len(waiting["retry_at"]) == 0 {
		t.Fatal("retry activity missing from overview")
	}
	if _, err := f.runtime.pool.Exec(ctx, `UPDATE mail_automations SET state='stopped',reason='tls_failed',retry_after=NULL WHERE id=$1`, v.ID); err != nil {
		t.Fatal(err)
	}
	stopped := read()
	if string(stopped["automation_reason"]) != `"tls_failed"` || string(stopped["automation_running"]) != "false" || string(stopped["retry_at"]) != "null" {
		t.Fatal("automatic stop reason lost")
	}
}
