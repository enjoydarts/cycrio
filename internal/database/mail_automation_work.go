package database

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/enjoydarts/cycrio/internal/mailautomation"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"time"
)

// Tick only performs bounded database work. Mail and provider I/O run through
// existing leased jobs outside transactions, with one account per transaction.
func (r *MailAutomationRepository) Tick(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 100 {
		return 0, mailautomation.ErrInvalid
	}
	rows, e := r.pool.Query(ctx, `SELECT a.owner_id,a.account_id FROM mail_automations a JOIN users u ON u.id=a.owner_id AND u.state='active' WHERE a.state='enabled' ORDER BY a.updated_at,a.id LIMIT $1`, limit)
	if e != nil {
		return 0, processingError(e)
	}
	type target struct{ owner, account string }
	targets := []target{}
	for rows.Next() {
		var v target
		if e = rows.Scan(&v.owner, &v.account); e != nil {
			rows.Close()
			return 0, processingError(e)
		}
		targets = append(targets, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return 0, processingError(e)
	}
	count := 0
	for _, v := range targets {
		if ctx.Err() != nil {
			return count, ctx.Err()
		}
		if e = r.tickAccount(ctx, v.owner, v.account); e != nil {
			if errors.Is(e, mailautomation.ErrNotFound) {
				continue
			}
			return count, e
		}
		count++
	}
	return count, nil
}
func (r *MailAutomationRepository) tickAccount(ctx context.Context, owner, account string) error {
	tx, e := r.begin(ctx, owner, account)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var id, state, lastDiscovery, lastProcessing string
	var generation, version int64
	var due bool
	var backoff bool
	var retryLane string
	var retryWindow int
	e = tx.QueryRow(ctx, `SELECT id,state,generation,version,last_discovery_lane,last_processing_lane,next_check_at<=clock_timestamp(),coalesce(retry_after>clock_timestamp(),false),retry_lane,retry_window_index FROM mail_automations WHERE owner_id=$1 AND account_id=$2`, owner, account).Scan(&id, &state, &generation, &version, &lastDiscovery, &lastProcessing, &due, &backoff, &retryLane, &retryWindow)
	if e != nil {
		return processingError(e)
	}
	if state != "enabled" {
		return nil
	}
	var saved mailautomation.Basis
	var scope mailautomation.Scope
	if e = tx.QueryRow(ctx, `SELECT account_version,runtime_version,profile_id,policy_version,since::text,before::text,interval_seconds FROM mail_automation_scopes WHERE automation_id=$1 AND generation=$2`, id, generation).Scan(&saved.AccountVersion, &saved.RuntimeVersion, &saved.ProfileID, &saved.PolicyVersion, &scope.Since, &scope.Before, &scope.IntervalSeconds); e != nil {
		return processingError(e)
	}
	basis, reasons, e := automationReadiness(ctx, tx, owner, account, true)
	if e != nil {
		return e
	}
	reason := ""
	if len(reasons) > 0 {
		reason = reasons[0]
	}
	if basis.AccountVersion != saved.AccountVersion {
		reason = "account_changed"
	}
	if basis.ProfileID != saved.ProfileID || basis.PolicyVersion != saved.PolicyVersion {
		reason = "configuration_changed"
	}
	if reason != "" {
		if e = pauseAutomation(ctx, tx, id, reason); e != nil {
			return e
		}
		return processingError(tx.Commit(ctx))
	}
	var bootPending bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM mail_automation_progress WHERE automation_id=$1 AND generation=$2 AND lane='bootstrap' AND NOT done)`, id, generation).Scan(&bootPending); e != nil {
		return processingError(e)
	}
	if !bootPending {
		if e = ensureAutomaticWindows(ctx, tx, owner, account, id, generation, scope); e != nil {
			return e
		}
	}
	var active bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM mail_sync_runs WHERE account_id=$1 AND state IN ('queued','leased','running'))`, account).Scan(&active); e != nil {
		return processingError(e)
	}
	if !active && !backoff {
		lane := ""
		window := 0
		if retryLane != "" {
			lane = retryLane
			window = retryWindow
		} else if bootPending {
			lane = "bootstrap"
		} else {
			var history, incremental bool
			if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM mail_automation_progress WHERE automation_id=$1 AND generation=$2 AND lane='historical' AND NOT done),EXISTS(SELECT 1 FROM mail_automation_progress WHERE automation_id=$1 AND generation=$2 AND lane='incremental' AND NOT done)`, id, generation).Scan(&history, &incremental); e != nil {
				return processingError(e)
			}
			if due && !incremental {
				if _, e = tx.Exec(ctx, `UPDATE mail_automation_progress SET initialized=false,done=false,upper_uid=after_uid WHERE automation_id=$1 AND generation=$2 AND lane='incremental'`, id, generation); e != nil {
					return processingError(e)
				}
				incremental = true
			}
			if incremental && (!history || lastDiscovery != "incremental") {
				lane = "incremental"
			} else if history {
				lane = "historical"
			} else if incremental {
				lane = "incremental"
			}
			if lane == "historical" {
				if e = tx.QueryRow(ctx, `SELECT min(window_index) FROM mail_automation_progress WHERE automation_id=$1 AND generation=$2 AND lane='historical' AND NOT done`, id, generation).Scan(&window); e != nil {
					return processingError(e)
				}
			}
		}
		if lane != "" {
			if e = createAutomaticSync(ctx, tx, owner, account, id, generation, version, basis.AccountVersion, lane, window); e != nil {
				return e
			}
		}
	}
	if !bootPending {
		if e = createAutomaticProcessing(ctx, tx, owner, account, id, generation, version, basis, lastProcessing); e != nil {
			return e
		}
	}
	if _, e = tx.Exec(ctx, `UPDATE mail_automations SET updated_at=clock_timestamp() WHERE id=$1`, id); e != nil {
		return processingError(e)
	}
	return processingError(tx.Commit(ctx))
}

func ensureAutomaticWindows(ctx context.Context, tx pgx.Tx, owner, account, id string, generation int64, scope mailautomation.Scope) error {
	var count int
	if e := tx.QueryRow(ctx, `SELECT count(*) FROM mail_automation_progress WHERE automation_id=$1 AND generation=$2 AND lane='incremental'`, id, generation).Scan(&count); e != nil {
		return processingError(e)
	}
	if count == 0 {
		if _, e := tx.Exec(ctx, `INSERT INTO mail_automation_progress(owner_id,account_id,automation_id,generation,lane,window_index,folder_id,uid_validity,after_uid,upper_uid,initialized,done)SELECT owner_id,account_id,automation_id,generation,'incremental',0,folder_id,uid_validity,upper_uid,upper_uid,true,true FROM mail_automation_progress WHERE automation_id=$1 AND generation=$2 AND lane='bootstrap'`, id, generation); e != nil {
			return processingError(e)
		}
	}
	var pending bool
	if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM mail_automation_progress WHERE automation_id=$1 AND generation=$2 AND lane='historical' AND NOT done)`, id, generation).Scan(&pending); e != nil {
		return processingError(e)
	}
	if pending {
		return nil
	}
	var since *string
	var index int
	if e := tx.QueryRow(ctx, `SELECT max(before)::text,coalesce(max(window_index)+1,0) FROM mail_automation_progress WHERE automation_id=$1 AND generation=$2 AND lane='historical'`, id, generation).Scan(&since, &index); e != nil {
		return processingError(e)
	}
	start := scope.Since
	if since != nil {
		start = *since
	}
	if start >= scope.Before {
		return nil
	}
	a, end, e := mailautomation.Bounds(mailautomation.Scope{Since: start, Before: scope.Before})
	if e != nil {
		return e
	}
	windowEnd := a.AddDate(0, 0, 366)
	if windowEnd.After(end) {
		windowEnd = end
	}
	before := windowEnd.Format(time.DateOnly)
	_, e = tx.Exec(ctx, `INSERT INTO mail_automation_progress(owner_id,account_id,automation_id,generation,lane,window_index,folder_id,since,before,uid_validity,after_uid,upper_uid,initialized,done)SELECT owner_id,account_id,automation_id,generation,'historical',$3,folder_id,$4::date,$5::date,uid_validity,0,upper_uid,true,false FROM mail_automation_progress WHERE automation_id=$1 AND generation=$2 AND lane='bootstrap'`, id, generation, index, start, before)
	return processingError(e)
}

func createAutomaticSync(ctx context.Context, tx pgx.Tx, owner, account, id string, generation, version, accountVersion int64, lane string, window int) error {
	var credential string
	if e := tx.QueryRow(ctx, `SELECT c.id FROM mail_accounts a JOIN mail_account_credentials c ON c.account_id=a.id AND c.owner_id=a.owner_id AND c.version=a.credential_version AND c.state='active' WHERE a.id=$1`, account).Scan(&credential); e != nil {
		return processingError(e)
	}
	run := uuid.NewString()
	_, e := tx.Exec(ctx, `INSERT INTO mail_sync_runs(id,owner_id,account_id,request_id,config_version,credential_id,since,before,max_messages,state,folder_count,completed_folders,automation_id,automation_generation,automation_version,mode,window_index)SELECT $1,$2,$3,$4,$5,$6,min(since),min(before),1000,'queued',count(*),count(*) FILTER(WHERE done),$7,$8,$9,$10,$11 FROM mail_automation_progress WHERE automation_id=$7 AND generation=$8 AND lane=$10 AND window_index=$11`, run, owner, account, uuid.NewString(), accountVersion, credential, id, generation, version, lane, window)
	if e != nil {
		return processingError(e)
	}
	if _, e = tx.Exec(ctx, `INSERT INTO mail_sync_run_folders(owner_id,account_id,run_id,folder_id,uid_validity,after_uid,upper_uid,initialized,done)SELECT owner_id,account_id,$1,folder_id,uid_validity,after_uid,upper_uid,initialized,done FROM mail_automation_progress WHERE automation_id=$2 AND generation=$3 AND lane=$4 AND window_index=$5`, run, id, generation, lane, window); e != nil {
		return processingError(e)
	}
	if lane != "bootstrap" {
		if _, e = tx.Exec(ctx, `UPDATE mail_automations SET last_discovery_lane=$2 WHERE id=$1`, id, lane); e != nil {
			return processingError(e)
		}
	}
	if _, e = tx.Exec(ctx, `UPDATE mail_automations SET retry_after=NULL,reason='' WHERE id=$1`, id); e != nil {
		return processingError(e)
	}
	return auditSync(ctx, tx, owner, account, run, "sync_requested")
}

// A legacy size failure may be recovered only if every historical attempt was
// free of external calls. The newest result must still be the bounded attempt.
const automaticSizeRecovery = `EXISTS(SELECT 1 FROM mail_processing_items latest JOIN mail_processing_runs old ON old.id=latest.run_id WHERE latest.id=(SELECT i.id FROM mail_processing_items i WHERE i.message_id=m.id AND i.state<>'cancelled' ORDER BY i.created_at DESC,i.id DESC LIMIT 1) AND latest.state='failed' AND latest.error_code='input_too_large' AND NOT coalesce((old.request_doc->'input'->>'full_email')::boolean,false) AND NOT EXISTS(SELECT 1 FROM mail_processing_items any_attempt JOIN ai_calls c ON c.operation_id=any_attempt.id WHERE any_attempt.message_id=m.id))`

func createAutomaticProcessing(ctx context.Context, tx pgx.Tx, owner, account, id string, generation, version int64, basis mailautomation.Basis, lastLane string) error {
	var active bool
	if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM mail_processing_runs WHERE account_id=$1 AND state IN ('queued','leased','running'))`, account).Scan(&active); e != nil {
		return processingError(e)
	}
	if active {
		return nil
	}
	preferred := "incremental"
	if lastLane == preferred {
		preferred = "historical"
	}
	rows, e := tx.Query(ctx, `SELECT m.id,member.lane,(SELECT i.id::text FROM mail_processing_items i WHERE i.message_id=m.id AND i.state<>'cancelled' ORDER BY i.created_at DESC,i.id DESC LIMIT 1) FROM mail_automation_messages member JOIN mail_discovered_messages m ON m.owner_id=member.owner_id AND m.account_id=member.account_id AND m.id=member.message_id JOIN mail_account_folders f ON f.id=m.folder_id AND f.account_id=m.account_id WHERE member.automation_id=$1 AND member.generation=$2 AND f.selected AND f.available AND f.selectable AND (`+processingPending+` OR `+automaticSizeRecovery+`) ORDER BY (member.lane=$3) DESC,member.discovered_at,m.id LIMIT 100`, id, generation, preferred)
	if e != nil {
		return processingError(e)
	}
	ids := []string{}
	retries := map[string]string{}
	lane := ""
	for rows.Next() {
		var message, itemLane string
		var latest *string
		if e = rows.Scan(&message, &itemLane, &latest); e != nil {
			rows.Close()
			return processingError(e)
		}
		if lane == "" {
			lane = itemLane
		}
		if itemLane != lane {
			continue
		}
		ids = append(ids, message)
		if latest != nil {
			retries[message] = *latest
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return processingError(e)
	}
	if len(ids) == 0 {
		return nil
	}
	var credential string
	if e = tx.QueryRow(ctx, `SELECT c.id FROM mail_accounts a JOIN mail_account_credentials c ON c.account_id=a.id AND c.owner_id=a.owner_id AND c.version=a.credential_version AND c.state='active' WHERE a.id=$1`, account).Scan(&credential); e != nil {
		return processingError(e)
	}
	input := mailprocessing.Input{ExpectedVersion: basis.AccountVersion, RuntimeVersion: basis.RuntimeVersion, ProfileID: basis.ProfileID, PolicyVersion: basis.PolicyVersion, RequestID: uuid.NewString(), MaxMessages: 100, FullEmail: true}
	// Confirmation derives from the immutable automation scope, never a fabricated
	// interactive Confirmed=true flag. Retry CAS identities are frozen with it.
	raw, _ := json.Marshal(struct {
		Input          mailprocessing.Input `json:"input"`
		AutomaticRetry map[string]string    `json:"automatic_retry,omitempty"`
	}{input, retries})
	run := uuid.NewString()
	if _, e = tx.Exec(ctx, `INSERT INTO mail_processing_runs(id,owner_id,account_id,request_id,request_doc,config_version,credential_id,runtime_version,profile_id,policy_version,max_messages,total_count,state,automation_id,automation_generation,automation_version)VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,100,$11,'queued',$12,$13,$14)`, run, owner, account, input.RequestID, raw, basis.AccountVersion, credential, basis.RuntimeVersion, basis.ProfileID, basis.PolicyVersion, len(ids), id, generation, version); e != nil {
		return processingError(e)
	}
	for _, message := range ids {
		if _, e = tx.Exec(ctx, `INSERT INTO mail_processing_items(id,owner_id,account_id,run_id,message_id,state)VALUES($1,$2,$3,$4,$5,'queued')`, uuid.NewString(), owner, account, run, message); e != nil {
			return processingError(e)
		}
	}
	if _, e = tx.Exec(ctx, `UPDATE mail_automations SET last_processing_lane=$2 WHERE id=$1`, id, lane); e != nil {
		return processingError(e)
	}
	return auditProcessing(ctx, tx, owner, account, run, "processing_requested")
}
