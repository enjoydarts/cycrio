package database

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type MailProcessingRepository struct{ pool *pgxpool.Pool }

var _ mailprocessing.Repository = (*MailProcessingRepository)(nil)

func NewMailProcessingRepository(p *pgxpool.Pool) *MailProcessingRepository {
	return &MailProcessingRepository{p}
}
func processingError(e error) error {
	if e == nil {
		return nil
	}
	if errors.Is(e, pgx.ErrNoRows) || errors.Is(e, airuntime.ErrNotFound) || errors.Is(e, mailprocessing.ErrNotFound) {
		return mailprocessing.ErrNotFound
	}
	var pg *pgconn.PgError
	if errors.Is(e, airuntime.ErrConflict) || errors.Is(e, airuntime.ErrCredentialsRequired) || errors.Is(e, mailprocessing.ErrConflict) || errors.As(e, &pg) && pg.Code == "23505" {
		return mailprocessing.ErrConflict
	}
	if errors.Is(e, airuntime.ErrInvalid) || errors.Is(e, mailprocessing.ErrInvalid) {
		return mailprocessing.ErrInvalid
	}
	return mailprocessing.ErrUnavailable
}

const processingRunColumns = `id,account_id,state,max_messages,total_count,completed_count,related_count,unrelated_count,failed_count,unknown_count,created_at,started_at,finished_at`

func scanProcessingRun(row pgx.Row) (mailprocessing.Run, error) {
	var r mailprocessing.Run
	e := row.Scan(&r.ID, &r.AccountID, &r.State, &r.MaxMessages, &r.TotalCount, &r.CompletedCount, &r.RelatedCount, &r.UnrelatedCount, &r.FailedCount, &r.UnknownCount, &r.CreatedAt, &r.StartedAt, &r.FinishedAt)
	return r, processingError(e)
}
func (r *MailProcessingRepository) beginAccount(ctx context.Context, owner, account string) (pgx.Tx, error) {
	if !mailprocessing.ValidID(owner) || !mailprocessing.ValidID(account) {
		return nil, mailprocessing.ErrInvalid
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return nil, mailprocessing.ErrUnavailable
	}
	var state string
	// State updates must serialize with processing, while admin audit FK
	// checks may take KEY SHARE on this same actor after provider/model locks.
	e = tx.QueryRow(ctx, `SELECT state FROM users WHERE id=$1 FOR NO KEY UPDATE`, owner).Scan(&state)
	if e != nil || state != "active" {
		tx.Rollback(ctx)
		if e == nil || errors.Is(e, pgx.ErrNoRows) {
			return nil, mailprocessing.ErrNotFound
		}
		return nil, mailprocessing.ErrUnavailable
	}
	var found string
	if e = tx.QueryRow(ctx, `SELECT id FROM mail_accounts WHERE id=$1 AND owner_id=$2 FOR UPDATE`, account, owner).Scan(&found); e != nil {
		tx.Rollback(ctx)
		return nil, processingError(e)
	}
	return tx, nil
}

// A pending message has no submitted/finished attempt. Cancelled, unsent
// snapshots alone do not consume it; failed and unknown attempts always do.
const processingPending = `NOT EXISTS(SELECT 1 FROM mail_processing_items i WHERE i.message_id=m.id AND i.state<>'cancelled')`

// API identities can belong to multiple labels. Eligibility follows a folder
// in which this specific message was discovered, while retaining its original
// immutable source folder for decryption. An unrelated selected folder never
// grants access to a remote reference.
const processingSourceSelected = `((m.remote_fingerprint IS NULL AND coalesce(f.selected AND f.available AND f.selectable,false)) OR (m.remote_fingerprint IS NOT NULL AND EXISTS(SELECT 1 FROM mail_message_folder_memberships source_membership JOIN mail_account_folders selected_folder ON selected_folder.id=source_membership.folder_id AND selected_folder.account_id=source_membership.account_id WHERE source_membership.owner_id=m.owner_id AND source_membership.account_id=m.account_id AND source_membership.message_id=m.id AND selected_folder.selected AND selected_folder.available AND selected_folder.selectable)))`

func processingPreview(ctx context.Context, tx pgx.Tx, owner, account string, lock bool) (mailprocessing.Preview, error) {
	out := mailprocessing.Preview{AccountID: account}
	var state string
	if e := tx.QueryRow(ctx, `SELECT version,state FROM mail_accounts WHERE id=$1 AND owner_id=$2`, account, owner).Scan(&out.ExpectedVersion, &state); e != nil {
		return out, processingError(e)
	}
	settings, p, e := processingState(ctx, tx, lock)
	if e != nil {
		return out, e
	}
	out.RuntimeVersion = settings.RuntimeVersion
	if settings.Profile != nil {
		public := settings.Profile
		out.ProfileID = &public.ID
		out.Primary = &public.Primary
		out.Secondary = &public.Secondary
		out.PolicyVersion = settings.Policy.Version
		out.FullEmailAllowed = settings.Policy.FullEmailAllowed
		out.MaxInputBytes = public.MaxInputBytes
		out.MaxOutputTokens = public.MaxOutputTokens
		out.TimeoutSeconds = public.TimeoutSeconds
	}
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM mail_discovered_messages m LEFT JOIN mail_account_folders f ON f.id=m.folder_id AND f.account_id=m.account_id WHERE m.owner_id=$1 AND m.account_id=$2 AND `+processingSourceSelected+` AND `+processingPending, owner, account).Scan(&out.PendingCount); e != nil {
		return out, mailprocessing.ErrUnavailable
	}
	if e = tx.QueryRow(ctx, `SELECT count(*) FILTER(WHERE state='failed'),count(*) FILTER(WHERE state='unknown') FROM (SELECT DISTINCT ON(message_id) message_id,state FROM mail_processing_items WHERE owner_id=$1 AND account_id=$2 AND state IN ('succeeded','not_related','failed','unknown') ORDER BY message_id,created_at DESC,id DESC) latest`, owner, account).Scan(&out.FailedCount, &out.UnknownCount); e != nil {
		return out, mailprocessing.ErrUnavailable
	}
	var active, folders, credential bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM mail_processing_runs WHERE account_id=$1 AND state IN ('queued','leased','running')),EXISTS(SELECT 1 FROM mail_account_folders WHERE account_id=$1 AND selected AND available AND selectable),EXISTS(SELECT 1 FROM mail_accounts a JOIN mail_account_credentials c ON c.account_id=a.id AND c.version=a.credential_version WHERE a.id=$1 AND c.state='active')`, account).Scan(&active, &folders, &credential); e != nil {
		return out, mailprocessing.ErrUnavailable
	}
	switch {
	case state != "ready":
		out.BlockedReason = "account_not_ready"
	case !credential:
		out.BlockedReason = "credentials_required"
	case !folders:
		out.BlockedReason = "folders_required"
	case settings.Profile == nil:
		out.BlockedReason = "runtime_inactive"
	case !settings.Policy.Enabled:
		out.BlockedReason = "policy_disabled"
	case !mailprocessing.StandardProofCurrent(*settings.Policy):
		out.BlockedReason = "policy_reconfirmation_required"
	case active:
		out.BlockedReason = "processing_active"
	default:
		if e = runtimeProfileReady(ctx, tx, p, true, lock); e != nil {
			out.BlockedReason = "configuration_unavailable"
		} else {
			out.Ready = true
		}
	}
	return out, nil
}
func (r *MailProcessingRepository) Preview(ctx context.Context, owner, account string) (mailprocessing.Preview, error) {
	tx, e := r.beginAccount(ctx, owner, account)
	if e != nil {
		return mailprocessing.Preview{}, e
	}
	defer tx.Rollback(ctx)
	return processingPreview(ctx, tx, owner, account, false)
}

type processingRequest struct {
	Input            mailprocessing.Input `json:"input"`
	MessageID        string               `json:"message_id"`
	ExpectedResultID string               `json:"expected_result_id"`
	ConfirmUnknown   bool                 `json:"confirm_unknown"`
}

func (r *MailProcessingRepository) Request(ctx context.Context, owner, account string, in mailprocessing.Input) (mailprocessing.Run, error) {
	return r.request(ctx, owner, account, processingRequest{Input: in})
}
func (r *MailProcessingRepository) Retry(ctx context.Context, owner, account, message string, in mailprocessing.RetryInput) (mailprocessing.Run, error) {
	if !mailprocessing.ValidID(message) || !mailprocessing.ValidRetry(in) {
		return mailprocessing.Run{}, mailprocessing.ErrInvalid
	}
	return r.request(ctx, owner, account, processingRequest{in.Input, message, in.ExpectedResultID, in.ConfirmUnknown})
}
func (r *MailProcessingRepository) request(ctx context.Context, owner, account string, request processingRequest) (mailprocessing.Run, error) {
	in := request.Input
	if !mailprocessing.ValidInput(in) {
		return mailprocessing.Run{}, mailprocessing.ErrInvalid
	}
	tx, e := r.beginAccount(ctx, owner, account)
	if e != nil {
		return mailprocessing.Run{}, e
	}
	defer tx.Rollback(ctx)
	raw, _ := json.Marshal(request)
	existing, e := scanProcessingRun(tx.QueryRow(ctx, `SELECT `+processingRunColumns+` FROM mail_processing_runs WHERE owner_id=$1 AND request_id=$2`, owner, in.RequestID))
	if e == nil {
		var same bool
		if e = tx.QueryRow(ctx, `SELECT account_id=$2 AND request_doc=$3::jsonb FROM mail_processing_runs WHERE id=$1`, existing.ID, account, raw).Scan(&same); e != nil {
			return mailprocessing.Run{}, mailprocessing.ErrUnavailable
		}
		if !same {
			return mailprocessing.Run{}, mailprocessing.ErrConflict
		}
		return existing, nil
	}
	if !errors.Is(e, mailprocessing.ErrNotFound) {
		return mailprocessing.Run{}, e
	}
	p, e := processingPreview(ctx, tx, owner, account, true)
	if e != nil {
		return mailprocessing.Run{}, e
	}
	if !p.Ready || in.FullEmail && !p.FullEmailAllowed || p.ExpectedVersion != in.ExpectedVersion || p.RuntimeVersion != in.RuntimeVersion || p.ProfileID == nil || *p.ProfileID != in.ProfileID || p.PolicyVersion != in.PolicyVersion {
		return mailprocessing.Run{}, mailprocessing.ErrConflict
	}
	var credential string
	if e = tx.QueryRow(ctx, `SELECT c.id FROM mail_accounts a JOIN mail_account_credentials c ON c.account_id=a.id AND c.version=a.credential_version WHERE a.id=$1 AND c.owner_id=$2 AND c.state='active' FOR SHARE OF c`, account, owner).Scan(&credential); e != nil {
		return mailprocessing.Run{}, processingError(e)
	}
	ids := []string{}
	if request.MessageID != "" {
		var latest, state string
		e = tx.QueryRow(ctx, `SELECT id,state FROM mail_processing_items WHERE message_id=$1 AND owner_id=$2 AND account_id=$3 AND state IN ('succeeded','not_related','failed','unknown') ORDER BY created_at DESC,id DESC LIMIT 1`, request.MessageID, owner, account).Scan(&latest, &state)
		if errors.Is(e, pgx.ErrNoRows) {
			return mailprocessing.Run{}, mailprocessing.ErrConflict
		}
		if e != nil {
			return mailprocessing.Run{}, mailprocessing.ErrUnavailable
		}
		if latest != request.ExpectedResultID || (state != "failed" && state != "unknown") || state == "unknown" && !request.ConfirmUnknown {
			return mailprocessing.Run{}, mailprocessing.ErrConflict
		}
		var selected bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM mail_discovered_messages m LEFT JOIN mail_account_folders f ON f.id=m.folder_id AND f.account_id=m.account_id WHERE m.id=$1 AND m.owner_id=$2 AND m.account_id=$3 AND `+processingSourceSelected+`)`, request.MessageID, owner, account).Scan(&selected); e != nil {
			return mailprocessing.Run{}, mailprocessing.ErrUnavailable
		}
		if !selected {
			return mailprocessing.Run{}, mailprocessing.ErrConflict
		}
		ids = append(ids, request.MessageID)
	} else {
		rows, e := tx.Query(ctx, `SELECT m.id FROM mail_discovered_messages m LEFT JOIN mail_account_folders f ON f.id=m.folder_id AND f.account_id=m.account_id WHERE m.owner_id=$1 AND m.account_id=$2 AND `+processingSourceSelected+` AND `+processingPending+` ORDER BY m.created_at,m.id LIMIT $3`, owner, account, in.MaxMessages)
		if e != nil {
			return mailprocessing.Run{}, mailprocessing.ErrUnavailable
		}
		for rows.Next() {
			var id string
			if rows.Scan(&id) != nil {
				rows.Close()
				return mailprocessing.Run{}, mailprocessing.ErrUnavailable
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return mailprocessing.Run{}, mailprocessing.ErrUnavailable
		}
	}
	if len(ids) == 0 {
		return mailprocessing.Run{}, mailprocessing.ErrConflict
	}
	run, e := scanProcessingRun(tx.QueryRow(ctx, `INSERT INTO mail_processing_runs(id,owner_id,account_id,request_id,request_doc,config_version,credential_id,runtime_version,profile_id,policy_version,max_messages,total_count,state)VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'queued') RETURNING `+processingRunColumns, uuid.NewString(), owner, account, in.RequestID, raw, in.ExpectedVersion, credential, in.RuntimeVersion, in.ProfileID, in.PolicyVersion, in.MaxMessages, len(ids)))
	if e != nil {
		return mailprocessing.Run{}, e
	}
	for _, id := range ids {
		if _, e = tx.Exec(ctx, `INSERT INTO mail_processing_items(id,owner_id,account_id,run_id,message_id,state)VALUES($1,$2,$3,$4,$5,'queued')`, uuid.NewString(), owner, account, run.ID, id); e != nil {
			return mailprocessing.Run{}, mailprocessing.ErrUnavailable
		}
	}
	action := "processing_requested"
	if request.MessageID != "" {
		action = "processing_retried"
	}
	if e = auditProcessing(ctx, tx, owner, account, run.ID, action); e != nil {
		return mailprocessing.Run{}, e
	}
	if e = tx.Commit(ctx); e != nil {
		return mailprocessing.Run{}, processingError(e)
	}
	return run, nil
}
func auditProcessing(ctx context.Context, tx pgx.Tx, owner, account, run, action string) error {
	_, e := tx.Exec(ctx, `INSERT INTO mail_processing_audit(id,owner_id,account_id,run_id,action)VALUES($1,$2,$3,$4,$5)`, uuid.NewString(), owner, account, run, action)
	return processingError(e)
}
func (r *MailProcessingRepository) ListRuns(ctx context.Context, owner, account string) ([]mailprocessing.Run, error) {
	tx, e := r.beginAccount(ctx, owner, account)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	rows, e := tx.Query(ctx, `SELECT `+processingRunColumns+` FROM mail_processing_runs WHERE owner_id=$1 AND account_id=$2 ORDER BY created_at DESC,id DESC LIMIT 20`, owner, account)
	if e != nil {
		return nil, mailprocessing.ErrUnavailable
	}
	defer rows.Close()
	out := []mailprocessing.Run{}
	for rows.Next() {
		v, e := scanProcessingRun(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, processingError(rows.Err())
}
func (r *MailProcessingRepository) Results(ctx context.Context, owner, account string) ([]mailprocessing.Result, error) {
	tx, e := r.beginAccount(ctx, owner, account)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	rows, e := tx.Query(ctx, `SELECT id,message_id,state,classification,error_code,events,finished_at FROM (SELECT DISTINCT ON(message_id) id,message_id,state,classification,error_code,events,finished_at FROM mail_processing_items WHERE owner_id=$1 AND account_id=$2 AND state IN ('succeeded','not_related','failed','unknown') ORDER BY message_id,created_at DESC,id DESC) latest ORDER BY finished_at DESC,id DESC LIMIT 100`, owner, account)
	if e != nil {
		return nil, mailprocessing.ErrUnavailable
	}
	defer rows.Close()
	out := []mailprocessing.Result{}
	for rows.Next() {
		var v mailprocessing.Result
		var raw []byte
		if rows.Scan(&v.ID, &v.MessageID, &v.State, &v.Classification, &v.ErrorCode, &raw, &v.FinishedAt) != nil || json.Unmarshal(raw, &v.Events) != nil {
			return nil, mailprocessing.ErrUnavailable
		}
		if v.Events == nil {
			v.Events = []ai.Event{}
		}
		out = append(out, v)
	}
	return out, processingError(rows.Err())
}
func (r *MailProcessingRepository) Cancel(ctx context.Context, owner, account, run string) (mailprocessing.Run, error) {
	if !mailprocessing.ValidID(run) {
		return mailprocessing.Run{}, mailprocessing.ErrInvalid
	}
	tx, e := r.beginAccount(ctx, owner, account)
	if e != nil {
		return mailprocessing.Run{}, e
	}
	defer tx.Rollback(ctx)
	v, e := scanProcessingRun(tx.QueryRow(ctx, `SELECT `+processingRunColumns+` FROM mail_processing_runs WHERE id=$1 AND owner_id=$2 AND account_id=$3 FOR UPDATE`, run, owner, account))
	if e != nil {
		return v, e
	}
	if v.State != "queued" && v.State != "leased" && v.State != "running" {
		return v, mailprocessing.ErrConflict
	}
	if e = stopProcessing(ctx, tx, run, "cancelled", "cancelled"); e != nil {
		return mailprocessing.Run{}, e
	}
	if e = auditProcessing(ctx, tx, owner, account, run, "processing_cancelled"); e != nil {
		return mailprocessing.Run{}, e
	}
	v, e = scanProcessingRun(tx.QueryRow(ctx, `SELECT `+processingRunColumns+` FROM mail_processing_runs WHERE id=$1`, run))
	if e != nil {
		return v, e
	}
	if e = tx.Commit(ctx); e != nil {
		return mailprocessing.Run{}, processingError(e)
	}
	return v, nil
}
func stopProcessing(ctx context.Context, tx pgx.Tx, id, state, code string) error {
	_, e := tx.Exec(ctx, `SELECT stop_mail_processing($1,$2,$3)`, id, state, code)
	if e == nil && code == "lease_expired" {
		// A worker can disappear after BeginCall and never run FinishCall. Keep
		// its charge uncertain, close the calling marker atomically with the run,
		// and allow a later usage revision without reviving the expired work.
		_, e = tx.Exec(ctx, `UPDATE ai_calls c SET status='unknown',outcome='lease_expired',finished_at=clock_timestamp(),updated_at=clock_timestamp() FROM mail_processing_items i WHERE i.run_id=$1 AND c.operation_id=i.id AND c.scope='user' AND c.consuming_user_id=i.owner_id AND c.status='calling'`, id)
	}
	return processingError(e)
}
