//go:build integration

package database

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/enjoydarts/cycrio/internal/airuntime"
)

func TestMailAutomationCandidatesSkipRecoveryLookupsForCompletedHistory(t *testing.T) {
	f, r, in := automaticFixture(t)
	ctx := context.Background()
	f.in.PolicyVersion = in.PolicyVersion
	f.in.FullEmail = true
	l, _ := processingBatch(t, f, 1)
	var item string
	if err := f.runtime.pool.QueryRow(ctx, `SELECT id FROM mail_processing_items WHERE run_id=$1`, l.RunID).Scan(&item); err != nil {
		t.Fatal(err)
	}
	call, err := f.runtime.BeginCall(ctx, airuntime.BeginCallInput{OperationID: item, ProfileID: f.in.ProfileID, Scope: "user", ConsumingUserID: &f.owner, Stage: "classification", AttemptNo: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.runtime.pool.Exec(ctx, `UPDATE mail_processing_items SET state='succeeded',finished_at=clock_timestamp() WHERE id=$1`, item); err != nil {
		t.Fatal(err)
	}
	if _, err = f.runtime.pool.Exec(ctx, `UPDATE mail_processing_runs SET state='completed',lease_id=NULL,lease_until=NULL WHERE id=$1`, l.RunID); err != nil {
		t.Fatal(err)
	}
	v, err := r.Save(ctx, f.owner, f.account, in)
	if err != nil {
		t.Fatal(err)
	}
	// The payload is copied from the synthetic encrypted fixture. No processing
	// worker reads it: only the candidate query and its actual execution plan run.
	const completedMessages = 5000
	_, err = f.runtime.pool.Exec(ctx, `WITH messages AS (
	 INSERT INTO mail_discovered_messages(id,owner_id,account_id,folder_id,fingerprint,key_id,ciphertext)
	 SELECT gen_random_uuid(),m.owner_id,m.account_id,m.folder_id,'synthetic-history-'||n,m.key_id,m.ciphertext
	 FROM mail_discovered_messages m CROSS JOIN generate_series(1,$2::integer) n
	 WHERE m.id=(SELECT message_id FROM mail_processing_items WHERE id=$1)
	 RETURNING *
	), members AS (
	 INSERT INTO mail_automation_messages(owner_id,account_id,automation_id,generation,message_id,lane)
	 SELECT owner_id,account_id,$3,$4,id,'historical' FROM messages
	), items AS (
	 INSERT INTO mail_processing_items(id,owner_id,account_id,run_id,message_id,state,finished_at)
	 SELECT gen_random_uuid(),owner_id,account_id,$5,id,'succeeded',clock_timestamp() FROM messages
	 RETURNING *
	)
	INSERT INTO ai_calls(id,operation_id,profile_id,scope,consuming_user_id,provider,model_id,model_name,requested_model_id,credential_id,stage,attempt_no,status,mail_account_id)
	SELECT gen_random_uuid(),items.id,c.profile_id,c.scope,c.consuming_user_id,c.provider,c.model_id,c.model_name,c.requested_model_id,c.credential_id,c.stage,1,'succeeded',c.mail_account_id
	FROM items CROSS JOIN ai_calls c WHERE c.id=$6`, item, completedMessages, v.ID, v.Generation, l.RunID, call.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"mail_automation_messages", "mail_discovered_messages", "mail_processing_items", "mail_processing_runs", "ai_calls"} {
		if _, err = f.runtime.pool.Exec(ctx, `ANALYZE `+table); err != nil {
			t.Fatal(err)
		}
	}
	var raw []byte
	if err = f.runtime.pool.QueryRow(ctx, `EXPLAIN (ANALYZE, FORMAT JSON) `+automaticProcessingCandidatesSQL, v.ID, v.Generation, "incremental").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var plans []struct {
		Plan automaticCandidatePlan `json:"Plan"`
	}
	if err = json.Unmarshal(raw, &plans); err != nil || len(plans) != 1 {
		t.Fatal("invalid candidate execution plan", err)
	}
	if plans[0].Plan.ActualRows != 0 {
		t.Fatal("completed messages selected again", string(raw))
	}
	// Counting actual loops catches the correlated recovery lookup regression
	// without depending on the speed of the database or host running the test.
	var check func(automaticCandidatePlan)
	check = func(node automaticCandidatePlan) {
		if node.RelationName == "ai_calls" && node.ActualLoops > 1 {
			t.Errorf("recovery call lookup repeated %.0f times for %d completed messages", node.ActualLoops, completedMessages)
		}
		for _, child := range node.Plans {
			check(child)
		}
	}
	check(plans[0].Plan)
}

type automaticCandidatePlan struct {
	RelationName string                   `json:"Relation Name"`
	ActualRows   float64                  `json:"Actual Rows"`
	ActualLoops  float64                  `json:"Actual Loops"`
	Plans        []automaticCandidatePlan `json:"Plans"`
}
