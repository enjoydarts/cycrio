package database

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/enjoydarts/cycrio/internal/autoregistration"
	"github.com/enjoydarts/cycrio/internal/mailautomation"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/enjoydarts/cycrio/internal/mailsync"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type MailAutomationRepository struct{ pool *pgxpool.Pool }

var _ mailautomation.Repository = (*MailAutomationRepository)(nil)

func NewMailAutomationRepository(pool *pgxpool.Pool) *MailAutomationRepository {
	return &MailAutomationRepository{pool}
}

// The caller already owns the user and account locks. Never acquire a second
// pool connection while holding them, including owner registration preferences.
func lockAutomation(ctx context.Context, tx pgx.Tx, owner, account string) error {
	var id string
	e := tx.QueryRow(ctx, `SELECT id FROM mail_automations WHERE owner_id=$1 AND account_id=$2 FOR UPDATE`, owner, account).Scan(&id)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	return processingError(e)
}
func (r *MailAutomationRepository) begin(ctx context.Context, owner, account string) (pgx.Tx, error) {
	tx, e := (&MailProcessingRepository{r.pool}).beginAccount(ctx, owner, account)
	if e != nil {
		return nil, e
	}
	if e = lockAutomation(ctx, tx, owner, account); e != nil {
		tx.Rollback(ctx)
		return nil, e
	}
	return tx, nil
}

func automationReadiness(ctx context.Context, tx pgx.Tx, owner, account string, lock bool) (mailautomation.Basis, []string, error) {
	var basis mailautomation.Basis
	reasons := []string{}
	var state string
	if e := tx.QueryRow(ctx, `SELECT version,state FROM mail_accounts WHERE id=$1 AND owner_id=$2`, account, owner).Scan(&basis.AccountVersion, &state); e != nil {
		return basis, reasons, processingError(e)
	}
	if state != "ready" {
		reasons = append(reasons, "account_not_ready")
	}
	var folders, credential bool
	if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM mail_account_folders WHERE account_id=$1 AND selected AND available AND selectable),EXISTS(SELECT 1 FROM mail_accounts a JOIN mail_account_credentials c ON c.account_id=a.id AND c.owner_id=a.owner_id AND c.version=a.credential_version AND c.state='active' WHERE a.id=$1)`, account).Scan(&folders, &credential); e != nil {
		return basis, reasons, mailautomation.ErrUnavailable
	}
	if !folders {
		reasons = append(reasons, "folders_required")
	}
	if !credential {
		reasons = append(reasons, "credentials_required")
	}
	settings, p, e := processingState(ctx, tx, lock)
	if e != nil {
		return basis, reasons, e
	}
	basis.RuntimeVersion = settings.RuntimeVersion
	if settings.Profile == nil {
		return basis, append(reasons, "runtime_inactive"), nil
	}
	basis.ProfileID = settings.Profile.ID
	basis.PolicyVersion = settings.Policy.Version
	if !settings.Policy.Enabled {
		reasons = append(reasons, "policy_disabled")
	}
	if !settings.Policy.FullEmailAllowed {
		reasons = append(reasons, "full_email_not_allowed")
	}
	if !mailprocessing.StandardProofCurrent(*settings.Policy) {
		reasons = append(reasons, "policy_reconfirmation_required")
	}
	if e = runtimeProfileReady(ctx, tx, p, true, lock); e != nil {
		if errors.Is(e, mailprocessing.ErrUnavailable) {
			return basis, reasons, e
		}
		reasons = append(reasons, "configuration_unavailable")
	}
	return basis, reasons, nil
}
func automationState(ctx context.Context, tx pgx.Tx, owner, account string) (mailautomation.State, error) {
	out := mailautomation.State{AccountID: account, State: "not_configured", BlockedReasons: []string{}, DefaultScope: mailautomation.DefaultScope(time.Now()), RecentSyncRuns: []mailsync.Run{}}
	var scope mailautomation.Scope
	var saved mailautomation.Basis
	e := tx.QueryRow(ctx, `SELECT a.id,a.version,a.generation,a.state,a.reason,a.last_success_at,a.next_check_at,s.account_version,s.runtime_version,s.profile_id,s.policy_version,s.since::text,s.before::text,s.full_email,s.interval_seconds,s.auto_register,a.discovery_failure_count,a.retry_after FROM mail_automations a JOIN mail_automation_scopes s ON s.automation_id=a.id AND s.generation=a.generation WHERE a.owner_id=$1 AND a.account_id=$2`, owner, account).Scan(&out.ID, &out.Version, &out.Generation, &out.State, &out.Reason, &out.LastSuccessAt, &out.NextCheckAt, &saved.AccountVersion, &saved.RuntimeVersion, &saved.ProfileID, &saved.PolicyVersion, &scope.Since, &scope.Before, &scope.FullEmail, &scope.IntervalSeconds, &scope.AutoRegister, &out.DiscoveryFailureCount, &out.RetryAt)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return out, processingError(e)
	}
	if e == nil {
		scope.Timezone = "Asia/Tokyo"
		out.Scope = &scope
	}
	out.Basis, out.BlockedReasons, e = automationReadiness(ctx, tx, owner, account, false)
	if e != nil {
		return out, e
	}
	// Preparation is the fresh administrator/account basis. Saved scope guards
	// below are intentionally separate so the owner can explicitly reconfirm.
	out.PreparationReady = len(out.BlockedReasons) == 0
	out.PreparationBlockedReasons = append([]string{}, out.BlockedReasons...)
	if out.ID != "" {
		if out.Basis.AccountVersion != saved.AccountVersion {
			out.BlockedReasons = append(out.BlockedReasons, "account_changed")
		}
		if out.Basis.ProfileID != saved.ProfileID || out.Basis.PolicyVersion != saved.PolicyVersion {
			out.BlockedReasons = append(out.BlockedReasons, "configuration_changed")
		}
		if out.State == "needs_confirmation" {
			out.BlockedReasons = append(out.BlockedReasons, out.Reason)
		}
		if len(out.BlockedReasons) > 0 && out.State == "enabled" {
			out.State = "needs_confirmation"
			out.Reason = out.BlockedReasons[0]
		}
		if e = tx.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM mail_automation_progress WHERE automation_id=$1 AND generation=$2 AND lane='bootstrap' AND NOT done),NOT EXISTS(SELECT 1 FROM mail_automation_progress p WHERE p.automation_id=$1 AND p.generation=$2 AND p.lane='historical' AND NOT done) AND EXISTS(SELECT 1 FROM mail_automation_progress p JOIN mail_automation_scopes s USING(automation_id,generation) WHERE p.automation_id=$1 AND p.generation=$2 AND p.lane='historical' AND p.before=s.before)`, out.ID, out.Generation).Scan(&out.Progress.BootstrapComplete, &out.Progress.HistoricalComplete); e != nil {
			return out, mailautomation.ErrUnavailable
		}
		if e = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE latest.state IS NULL OR latest.state IN ('cancelled','queued','running')),count(*) FILTER(WHERE latest.state IN ('succeeded','not_related')),count(*) FILTER(WHERE latest.state='failed'),count(*) FILTER(WHERE latest.state='unknown') FROM mail_automation_messages m LEFT JOIN LATERAL(SELECT state FROM mail_processing_items i WHERE i.message_id=m.message_id AND i.state<>'cancelled' ORDER BY i.created_at DESC,i.id DESC LIMIT 1) latest ON true WHERE m.automation_id=$1 AND m.generation=$2`, out.ID, out.Generation).Scan(&out.Progress.DiscoveredCount, &out.Progress.PendingCount, &out.Progress.CompletedCount, &out.Progress.FailedCount, &out.Progress.UnknownCount); e != nil {
			return out, mailautomation.ErrUnavailable
		}
		rows, e := tx.Query(ctx, `SELECT `+mailSyncColumns+` FROM mail_sync_runs WHERE owner_id=$1 AND account_id=$2 AND automation_id=$3 AND automation_generation=$4 ORDER BY created_at DESC,id DESC LIMIT 20`, owner, account, out.ID, out.Generation)
		if e != nil {
			return out, processingError(e)
		}
		for rows.Next() {
			run, e := scanMailSync(rows)
			if e != nil {
				rows.Close()
				return out, e
			}
			out.RecentSyncRuns = append(out.RecentSyncRuns, run)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, processingError(e)
		}
	}
	out.Ready = len(out.BlockedReasons) == 0
	return out, nil
}
func (r *MailAutomationRepository) Read(ctx context.Context, owner, account string) (mailautomation.State, error) {
	tx, e := r.begin(ctx, owner, account)
	if e != nil {
		return mailautomation.State{}, e
	}
	defer tx.Rollback(ctx)
	return automationState(ctx, tx, owner, account)
}

func automationReplay(ctx context.Context, tx pgx.Tx, owner, account, nonce string, doc []byte) (mailautomation.State, bool, error) {
	var raw []byte
	var same bool
	e := tx.QueryRow(ctx, `SELECT account_id=$2 AND request_doc=$3::jsonb,response_doc FROM mail_automation_requests WHERE owner_id=$1 AND request_id=$4`, owner, account, doc, nonce).Scan(&same, &raw)
	if errors.Is(e, pgx.ErrNoRows) {
		return mailautomation.State{}, false, nil
	}
	if e != nil {
		return mailautomation.State{}, false, mailautomation.ErrUnavailable
	}
	if !same {
		return mailautomation.State{}, false, mailautomation.ErrConflict
	}
	var v mailautomation.State
	if json.Unmarshal(raw, &v) != nil {
		return v, false, mailautomation.ErrUnavailable
	}
	return v, true, nil
}
func auditAutomation(ctx context.Context, tx pgx.Tx, owner, account, id string, generation, version int64, action, reason string) error {
	_, e := tx.Exec(ctx, `INSERT INTO mail_automation_audit(id,owner_id,account_id,automation_id,generation,version,action,reason)VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, uuid.NewString(), owner, account, id, generation, version, action, reason)
	return processingError(e)
}
func automationReply(ctx context.Context, tx pgx.Tx, owner, account, nonce string, doc []byte) (mailautomation.State, error) {
	v, e := automationState(ctx, tx, owner, account)
	if e != nil {
		return v, e
	}
	raw, _ := json.Marshal(v)
	if _, e = tx.Exec(ctx, `INSERT INTO mail_automation_requests(owner_id,account_id,request_id,request_doc,response_doc)VALUES($1,$2,$3,$4,$5)`, owner, account, nonce, doc, raw); e != nil {
		return v, processingError(e)
	}
	return v, processingError(tx.Commit(ctx))
}
func stopAutomaticRuns(ctx context.Context, tx pgx.Tx, id string) error {
	if _, e := tx.Exec(ctx, `UPDATE mail_sync_runs SET state='cancelled',error_code='cancelled',lease_id=NULL,lease_until=NULL,finished_at=clock_timestamp() WHERE automation_id=$1 AND state IN ('queued','leased','running')`, id); e != nil {
		return processingError(e)
	}
	rows, e := tx.Query(ctx, `SELECT id FROM mail_processing_runs WHERE automation_id=$1 AND state IN ('queued','leased','running') ORDER BY id FOR UPDATE`, id)
	if e != nil {
		return processingError(e)
	}
	ids := []string{}
	for rows.Next() {
		var run string
		if e = rows.Scan(&run); e != nil {
			rows.Close()
			return processingError(e)
		}
		ids = append(ids, run)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return processingError(e)
	}
	for _, run := range ids {
		// A started fetch without a calling marker has not sent data externally.
		if _, e = tx.Exec(ctx, `UPDATE mail_processing_items i SET state='queued' WHERE run_id=$1 AND state='running' AND NOT EXISTS(SELECT 1 FROM ai_calls c WHERE c.operation_id=i.id)`, run); e != nil {
			return processingError(e)
		}
		if e = stopProcessing(ctx, tx, run, "cancelled", "cancelled"); e != nil {
			return e
		}
	}
	return nil
}
func (r *MailAutomationRepository) Save(ctx context.Context, owner, account string, in mailautomation.Input) (mailautomation.State, error) {
	if !mailautomation.ValidInput(in) {
		return mailautomation.State{}, mailautomation.ErrInvalid
	}
	tx, e := r.begin(ctx, owner, account)
	if e != nil {
		return mailautomation.State{}, e
	}
	defer tx.Rollback(ctx)
	doc, _ := json.Marshal(struct {
		Action string               `json:"action"`
		Input  mailautomation.Input `json:"input"`
	}{"activate", in})
	if v, ok, e := automationReplay(ctx, tx, owner, account, in.RequestID, doc); ok || e != nil {
		return v, e
	}
	var id string
	var version, generation int64
	e = tx.QueryRow(ctx, `SELECT id,version,generation FROM mail_automations WHERE owner_id=$1 AND account_id=$2`, owner, account).Scan(&id, &version, &generation)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return mailautomation.State{}, processingError(e)
	}
	if version != in.ExpectedSettingsVersion {
		return mailautomation.State{}, mailautomation.ErrConflict
	}
	basis, reasons, e := automationReadiness(ctx, tx, owner, account, true)
	if e != nil {
		return mailautomation.State{}, e
	}
	if len(reasons) > 0 || basis.AccountVersion != in.ExpectedAccountVersion || basis.ProfileID != in.ProfileID || basis.PolicyVersion != in.PolicyVersion || in.RuntimeVersion > basis.RuntimeVersion {
		return mailautomation.State{}, mailautomation.ErrConflict
	}
	if id == "" {
		id = uuid.NewString()
	} else if e = stopAutomaticRuns(ctx, tx, id); e != nil {
		return mailautomation.State{}, e
	}
	version++
	generation++
	if _, e = tx.Exec(ctx, `INSERT INTO mail_automations(id,owner_id,account_id,version,generation,state)VALUES($1,$2,$3,$4,$5,'enabled') ON CONFLICT(owner_id,account_id) DO UPDATE SET version=$4,generation=$5,state='enabled',reason='',next_check_at=clock_timestamp(),last_success_at=NULL,last_discovery_lane='historical',last_processing_lane='historical',discovery_failure_count=0,retry_after=NULL,retry_lane='',retry_window_index=0,updated_at=clock_timestamp()`, id, owner, account, version, generation); e != nil {
		return mailautomation.State{}, processingError(e)
	}
	if _, e = tx.Exec(ctx, `INSERT INTO mail_automation_scopes(owner_id,account_id,automation_id,generation,account_version,runtime_version,profile_id,policy_version,since,before,full_email,interval_seconds,auto_register)VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9::date,$10::date,true,$11,$12)`, owner, account, id, generation, basis.AccountVersion, basis.RuntimeVersion, basis.ProfileID, basis.PolicyVersion, in.Since, in.Before, in.IntervalSeconds, in.AutoRegister); e != nil {
		return mailautomation.State{}, processingError(e)
	}
	if _, e = tx.Exec(ctx, `INSERT INTO mail_automation_progress(owner_id,account_id,automation_id,generation,lane,window_index,folder_id) SELECT $1,$2,$3,$4,'bootstrap',0,id FROM mail_account_folders WHERE account_id=$2 AND selected AND available AND selectable`, owner, account, id, generation); e != nil {
		return mailautomation.State{}, processingError(e)
	}
	if in.AutoRegister {
		pref, e := automationPreference(ctx, tx, owner)
		if e != nil {
			return mailautomation.State{}, e
		}
		if !pref.Enabled {
			if _, e = saveAutomationPreference(ctx, tx, owner, autoregistration.PreferenceInput{ExpectedVersion: pref.Version, Enabled: true}); e != nil {
				return mailautomation.State{}, e
			}
		}
	}
	if e = auditAutomation(ctx, tx, owner, account, id, generation, version, "activated", ""); e != nil {
		return mailautomation.State{}, e
	}
	return automationReply(ctx, tx, owner, account, in.RequestID, doc)
}
func (r *MailAutomationRepository) Stop(ctx context.Context, owner, account string, in mailautomation.Control) (mailautomation.State, error) {
	return r.control(ctx, owner, account, in, false)
}
func (r *MailAutomationRepository) Resume(ctx context.Context, owner, account string, in mailautomation.Control) (mailautomation.State, error) {
	return r.control(ctx, owner, account, in, true)
}
func (r *MailAutomationRepository) control(ctx context.Context, owner, account string, in mailautomation.Control, resume bool) (mailautomation.State, error) {
	if !mailautomation.ValidControl(in) {
		return mailautomation.State{}, mailautomation.ErrInvalid
	}
	tx, e := r.begin(ctx, owner, account)
	if e != nil {
		return mailautomation.State{}, e
	}
	defer tx.Rollback(ctx)
	action := "stopped"
	target := "stopped"
	if resume {
		action = "resumed"
		target = "enabled"
	}
	doc, _ := json.Marshal(struct {
		Action string                 `json:"action"`
		Input  mailautomation.Control `json:"input"`
	}{action, in})
	if v, ok, e := automationReplay(ctx, tx, owner, account, in.RequestID, doc); ok || e != nil {
		return v, e
	}
	v, e := automationState(ctx, tx, owner, account)
	if e != nil {
		return v, e
	}
	if v.ID == "" || v.Version != in.ExpectedSettingsVersion || resume && (v.State != "stopped" || !v.Ready) {
		return v, mailautomation.ErrConflict
	}
	if e = stopAutomaticRuns(ctx, tx, v.ID); e != nil {
		return mailautomation.State{}, e
	}
	// Required confirmation is never cleared by ordinary resume or stop, even
	// when an administrator subsequently restores an unavailable key/profile.
	reason := v.Reason
	if v.State == "needs_confirmation" {
		target = "needs_confirmation"
	} else {
		reason = ""
	}
	if _, e = tx.Exec(ctx, `UPDATE mail_automations SET version=version+1,state=$2,reason=$3,next_check_at=clock_timestamp(),retry_after=NULL,retry_lane='',retry_window_index=0,discovery_failure_count=CASE WHEN $2='enabled' THEN 0 ELSE discovery_failure_count END,updated_at=clock_timestamp() WHERE id=$1`, v.ID, target, reason); e != nil {
		return mailautomation.State{}, processingError(e)
	}
	if e = auditAutomation(ctx, tx, owner, account, v.ID, v.Generation, v.Version+1, action, reason); e != nil {
		return mailautomation.State{}, e
	}
	return automationReply(ctx, tx, owner, account, in.RequestID, doc)
}

// pauseAutomation is only called with owner/account/automation locks held.
func pauseAutomation(ctx context.Context, tx pgx.Tx, id, reason string) error {
	var owner, account, state string
	var generation, version int64
	if e := tx.QueryRow(ctx, `SELECT owner_id,account_id,generation,version,state FROM mail_automations WHERE id=$1`, id).Scan(&owner, &account, &generation, &version, &state); e != nil {
		return processingError(e)
	}
	if state != "enabled" {
		return nil
	}
	if e := stopAutomaticRuns(ctx, tx, id); e != nil {
		return e
	}
	if _, e := tx.Exec(ctx, `UPDATE mail_automations SET version=version+1,state='needs_confirmation',reason=$2,retry_after=NULL,retry_lane='',updated_at=clock_timestamp() WHERE id=$1`, id, reason); e != nil {
		return processingError(e)
	}
	return auditAutomation(ctx, tx, owner, account, id, generation, version+1, "paused", reason)
}

func suspendAutomation(ctx context.Context, tx pgx.Tx, id, reason string) error {
	var owner, account string
	var generation, version int64
	if e := tx.QueryRow(ctx, `SELECT owner_id,account_id,generation,version FROM mail_automations WHERE id=$1`, id).Scan(&owner, &account, &generation, &version); e != nil {
		return processingError(e)
	}
	if e := stopAutomaticRuns(ctx, tx, id); e != nil {
		return e
	}
	if _, e := tx.Exec(ctx, `UPDATE mail_automations SET version=version+1,state='stopped',reason=$2,retry_after=NULL,retry_lane='',updated_at=clock_timestamp() WHERE id=$1`, id, reason); e != nil {
		return processingError(e)
	}
	return auditAutomation(ctx, tx, owner, account, id, generation, version+1, "stopped", reason)
}

func retryAutomaticDiscovery(ctx context.Context, tx pgx.Tx, w lockedSync, code string) error {
	var failures int
	if e := tx.QueryRow(ctx, `UPDATE mail_automations SET discovery_failure_count=discovery_failure_count+1 WHERE id=$1 RETURNING discovery_failure_count`, w.automation).Scan(&failures); e != nil {
		return processingError(e)
	}
	delay, retry := mailautomation.DiscoveryBackoff(failures)
	if !retry {
		return suspendAutomation(ctx, tx, w.automation, code)
	}
	_, e := tx.Exec(ctx, `UPDATE mail_automations SET reason=$2,retry_after=clock_timestamp()+$3*interval '1 second',next_check_at=clock_timestamp()+$3*interval '1 second',retry_lane=$4,retry_window_index=$5,updated_at=clock_timestamp() WHERE id=$1`, w.automation, code, int(delay/time.Second), w.mode, w.window)
	return processingError(e)
}
