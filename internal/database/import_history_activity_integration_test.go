//go:build integration

package database

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/google/uuid"
)

func TestImportHistoryActivityUnstartedIsExplicitNull(t *testing.T) {
	f := processingSetup(t)
	page, err := f.repo.ImportHistory(context.Background(), f.owner, f.account, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	fields := activityJSON(t, page)
	for _, key := range []string{"last_sync_started_at", "last_analysis_started_at", "last_sync_activity", "last_analysis_activity", "automation_activity"} {
		if string(fields[key]) != "null" {
			t.Fatalf("%s must explicitly be null for an unstarted account", key)
		}
	}
}

func TestImportHistoryActivityIncludesAllRunsBeyondPage(t *testing.T) {
	f, automation, input := automaticFixture(t)
	automaticBootstrap(t, f, automation, input, 1)
	ctx := context.Background()
	f.in.PolicyVersion = input.PolicyVersion
	f.add(t, 1)
	if _, err := f.repo.Request(ctx, f.owner, f.account, f.in); err != nil {
		t.Fatal(err)
	}
	lease, _ := f.start(t)
	if err := f.repo.Finish(ctx, lease, mailprocessing.Completion{State: "failed", ErrorCode: "timeout"}); err != nil {
		t.Fatal(err)
	}
	syncAt := time.Date(2026, 11, 3, 14, 59, 58, 0, time.UTC)
	analysisAt := syncAt.Add(3 * time.Second)
	if _, err := f.runtime.pool.Exec(ctx, `UPDATE mail_sync_runs SET started_at=$1,examined_count=1,added_count=1 WHERE account_id=$2;`, syncAt, f.account); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runtime.pool.Exec(ctx, `UPDATE mail_processing_runs SET started_at=$1 WHERE account_id=$2`, analysisAt, f.account); err != nil {
		t.Fatal(err)
	}
	// A newer analysis on another connection owned by the same user must not
	// overwrite the selected connection's activity.
	otherAccount := uuid.NewString()
	cv := mailaccounts.CredentialVersion{ID: uuid.NewString(), OwnerID: f.owner, AccountID: otherAccount, Version: 1}
	var err error
	cv.Envelope, err = f.keys.Seal([]byte(`{"login":"c3ludGhldGlj","password":"c2VjcmV0"}`), mailaccounts.CredentialAAD(cv))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = NewMailAccountRepository(f.runtime.pool).Save(ctx, f.owner, otherAccount, 0, "Other synthetic", cv); err != nil {
		t.Fatal(err)
	}
	if _, err = f.runtime.pool.Exec(ctx, `INSERT INTO mail_processing_runs(id,owner_id,account_id,request_id,request_doc,config_version,credential_id,runtime_version,profile_id,policy_version,max_messages,state,started_at) SELECT $1,owner_id,$2,$3,'{}',config_version,$4,runtime_version,profile_id,policy_version,max_messages,'completed',$5 FROM mail_processing_runs WHERE account_id=$6 LIMIT 1`, uuid.NewString(), otherAccount, uuid.NewString(), cv.ID, analysisAt.Add(time.Hour), f.account); err != nil {
		t.Fatal(err)
	}
	// More recent requests never started. Neither the current page nor created_at
	// is evidence of when processing actually ran.
	for range 3 {
		if _, err := f.runtime.pool.Exec(ctx, `INSERT INTO mail_processing_runs(id,owner_id,account_id,request_id,request_doc,config_version,credential_id,runtime_version,profile_id,policy_version,max_messages,state) SELECT $1,owner_id,account_id,$2,'{}',config_version,credential_id,runtime_version,profile_id,policy_version,max_messages,'cancelled' FROM mail_processing_runs WHERE account_id=$3 LIMIT 1`, uuid.NewString(), uuid.NewString(), f.account); err != nil {
			t.Fatal(err)
		}
	}
	for _, offset := range []int{0, 2, 100} {
		page, err := f.repo.ImportHistory(ctx, f.owner, f.account, 1, offset)
		if err != nil {
			t.Fatal(err)
		}
		fields := activityJSON(t, page)
		var syncActivity struct {
			State      string `json:"state"`
			AddedCount int    `json:"added_count"`
		}
		if err := json.Unmarshal(fields["last_sync_activity"], &syncActivity); err != nil || syncActivity.State != "completed" || syncActivity.AddedCount != 1 {
			t.Fatalf("latest actual sync metadata missing at offset %d: %+v %v", offset, syncActivity, err)
		}
		var analysisActivity struct {
			State string `json:"state"`
		}
		if err := json.Unmarshal(fields["last_analysis_activity"], &analysisActivity); err != nil || analysisActivity.State != "completed_with_failures" {
			t.Fatalf("latest actual analysis metadata missing: %+v %v", analysisActivity, err)
		}
		for key, want := range map[string]time.Time{"last_sync_started_at": syncAt, "last_analysis_started_at": analysisAt} {
			var got *time.Time
			if err := json.Unmarshal(fields[key], &got); err != nil || got == nil || !got.Equal(want) {
				t.Fatalf("offset %d: %s missing or incorrect: %v %v", offset, key, got, err)
			}
		}
	}
	if _, err := f.repo.ImportHistory(ctx, f.other, f.account, 20, 0); !errors.Is(err, mailprocessing.ErrNotFound) {
		t.Fatal("foreign owner read activity", err)
	}
}

func TestImportHistoryActivityZeroResultsAndAutomationSchedule(t *testing.T) {
	f, automation, input := automaticFixture(t)
	v := automaticBootstrap(t, f, automation, input, 1)
	ctx := context.Background()
	checkAt := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	for _, state := range []string{"completed", "failed", "running", "cancelled"} {
		if _, err := f.runtime.pool.Exec(ctx, `UPDATE mail_sync_runs SET state=$1,added_count=0,lease_until=clock_timestamp()+interval '1 minute' WHERE account_id=$2`, state, f.account); err != nil {
			t.Fatal(err)
		}
		if _, err := f.runtime.pool.Exec(ctx, `UPDATE mail_automations SET state='enabled',next_check_at=$1,retry_after=$1 WHERE id=$2`, checkAt, v.ID); err != nil {
			t.Fatal(err)
		}
		page, err := f.repo.ImportHistory(ctx, f.owner, f.account, 1, 100)
		if err != nil {
			t.Fatal(err)
		}
		fields := activityJSON(t, page)
		var sync struct {
			State      string `json:"state"`
			AddedCount *int   `json:"added_count"`
		}
		if err = json.Unmarshal(fields["last_sync_activity"], &sync); err != nil || sync.State != state || sync.AddedCount == nil || *sync.AddedCount != 0 {
			t.Fatalf("sync metadata: %+v %v", sync, err)
		}
		var schedule struct {
			State       string     `json:"state"`
			NextCheckAt *time.Time `json:"next_check_at"`
			RetryAt     *time.Time `json:"retry_at"`
			Busy        bool       `json:"busy"`
		}
		if err = json.Unmarshal(fields["automation_activity"], &schedule); err != nil || schedule.State != "enabled" || schedule.NextCheckAt == nil || !schedule.NextCheckAt.Equal(checkAt) || schedule.RetryAt == nil || !schedule.RetryAt.Equal(checkAt) || schedule.Busy != (state == "running") {
			t.Fatalf("schedule: %+v %v", schedule, err)
		}
	}
	if _, err := f.runtime.pool.Exec(ctx, `UPDATE mail_automations SET state='stopped' WHERE id=$1`, v.ID); err != nil {
		t.Fatal(err)
	}
	page, err := f.repo.ImportHistory(ctx, f.owner, f.account, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	var schedule map[string]any
	if err = json.Unmarshal(activityJSON(t, page)["automation_activity"], &schedule); err != nil || schedule["next_check_at"] != nil || schedule["retry_at"] != nil {
		t.Fatalf("stopped schedule: %+v %v", schedule, err)
	}
	// An expired running lease is not evidence that a worker is still checking.
	if _, err := f.runtime.pool.Exec(ctx, `UPDATE mail_sync_runs SET state='running',lease_until=clock_timestamp()-interval '1 second' WHERE account_id=$1`, f.account); err != nil {
		t.Fatal(err)
	}
	page, err = f.repo.ImportHistory(ctx, f.owner, f.account, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	var sync map[string]any
	if err = json.Unmarshal(activityJSON(t, page)["last_sync_activity"], &sync); err != nil || sync["state"] != "unknown" {
		t.Fatalf("expired activity: %+v %v", sync, err)
	}
}
