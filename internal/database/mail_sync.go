package database

import (
	"context"
	"errors"
	"github.com/enjoydarts/cycrio/internal/mailsync"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type MailSyncRepository struct{ pool *pgxpool.Pool }

var _ mailsync.Repository = (*MailSyncRepository)(nil)

func NewMailSyncRepository(p *pgxpool.Pool) *MailSyncRepository { return &MailSyncRepository{p} }

const mailSyncColumns = `id,account_id,state,since::text,before::text,error_code,max_messages,examined_count,added_count,folder_count,completed_folders,created_at,started_at,finished_at`

func scanMailSync(row pgx.Row) (mailsync.Run, error) {
	var r mailsync.Run
	e := row.Scan(&r.ID, &r.AccountID, &r.State, &r.Since, &r.Before, &r.ErrorCode, &r.MaxMessages, &r.ExaminedCount, &r.AddedCount, &r.FolderCount, &r.CompletedFolders, &r.CreatedAt, &r.StartedAt, &r.FinishedAt)
	if errors.Is(e, pgx.ErrNoRows) {
		return r, mailsync.ErrNotFound
	}
	if e != nil {
		return r, mailsync.ErrUnavailable
	}
	return r, nil
}
func (r *MailSyncRepository) beginAccount(ctx context.Context, owner, id string) (pgx.Tx, error) {
	tx, e := (&MailAccountRepository{r.pool}).beginOwned(ctx, owner)
	if e != nil {
		return nil, e
	}
	var found string
	if e = tx.QueryRow(ctx, `SELECT id FROM mail_accounts WHERE id=$1 AND owner_id=$2 FOR UPDATE`, id, owner).Scan(&found); e != nil {
		tx.Rollback(ctx)
		if errors.Is(e, pgx.ErrNoRows) {
			return nil, mailsync.ErrNotFound
		}
		return nil, mailsync.ErrUnavailable
	}
	return tx, nil
}
func (r *MailSyncRepository) Request(ctx context.Context, owner, id string, in mailsync.Input) (mailsync.Run, error) {
	if !mailsync.ValidID(owner) || !mailsync.ValidID(id) || !mailsync.ValidInput(in) {
		return mailsync.Run{}, mailsync.ErrInvalid
	}
	tx, e := r.beginAccount(ctx, owner, id)
	if e != nil {
		return mailsync.Run{}, e
	}
	defer tx.Rollback(ctx)
	existing, e := scanMailSync(tx.QueryRow(ctx, `SELECT `+mailSyncColumns+` FROM mail_sync_runs WHERE owner_id=$1 AND request_id=$2`, owner, in.RequestID))
	if e == nil {
		var version int64
		if tx.QueryRow(ctx, `SELECT config_version FROM mail_sync_runs WHERE id=$1`, existing.ID).Scan(&version) != nil {
			return mailsync.Run{}, mailsync.ErrUnavailable
		}
		if existing.AccountID != id || existing.Since != in.Since || existing.Before != in.Before || existing.MaxMessages != in.MaxMessages || version != in.ExpectedVersion {
			return mailsync.Run{}, mailsync.ErrConflict
		}
		return existing, nil
	}
	if !errors.Is(e, mailsync.ErrNotFound) {
		return mailsync.Run{}, e
	}
	var version int64
	var state, credential string
	var count int
	var active bool
	if tx.QueryRow(ctx, `SELECT version,state FROM mail_accounts WHERE id=$1`, id).Scan(&version, &state) != nil {
		return mailsync.Run{}, mailsync.ErrUnavailable
	}
	if version != in.ExpectedVersion || state != "ready" {
		return mailsync.Run{}, mailsync.ErrConflict
	}
	if tx.QueryRow(ctx, `SELECT count(*) FROM mail_account_folders WHERE account_id=$1 AND selected AND available AND selectable`, id).Scan(&count) != nil || tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM mail_sync_runs WHERE account_id=$1 AND state IN ('queued','leased','running'))`, id).Scan(&active) != nil {
		return mailsync.Run{}, mailsync.ErrUnavailable
	}
	if count == 0 || count > 1000 || active {
		return mailsync.Run{}, mailsync.ErrConflict
	}
	if tx.QueryRow(ctx, `SELECT c.id FROM mail_account_credentials c JOIN mail_accounts a ON a.id=c.account_id WHERE c.account_id=$1 AND c.owner_id=$2 AND c.state='active' AND c.version=a.credential_version`, id, owner).Scan(&credential) != nil {
		return mailsync.Run{}, mailsync.ErrConflict
	}
	run, e := scanMailSync(tx.QueryRow(ctx, `INSERT INTO mail_sync_runs(id,owner_id,account_id,request_id,config_version,credential_id,since,before,max_messages,state,folder_count) VALUES($1,$2,$3,$4,$5,$6,$7::date,$8::date,$9,'queued',$10) RETURNING `+mailSyncColumns, uuid.NewString(), owner, id, in.RequestID, version, credential, in.Since, in.Before, in.MaxMessages, count))
	if e != nil {
		return mailsync.Run{}, e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO mail_sync_run_folders(owner_id,account_id,run_id,folder_id) SELECT $1,$2,$3,id FROM mail_account_folders WHERE account_id=$2 AND selected AND available AND selectable`, owner, id, run.ID); e != nil {
		return mailsync.Run{}, mailsync.ErrUnavailable
	}
	if auditSync(ctx, tx, owner, id, run.ID, "sync_requested") != nil || tx.Commit(ctx) != nil {
		return mailsync.Run{}, mailsync.ErrUnavailable
	}
	return run, nil
}
func auditSync(ctx context.Context, tx pgx.Tx, owner, account, run, action string) error {
	_, e := tx.Exec(ctx, `INSERT INTO mail_sync_audit(id,owner_id,account_id,run_id,action) VALUES($1,$2,$3,$4,$5)`, uuid.NewString(), owner, account, run, action)
	return e
}
func (r *MailSyncRepository) List(ctx context.Context, owner, id string) ([]mailsync.Run, error) {
	tx, e := r.beginAccount(ctx, owner, id)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	rows, e := tx.Query(ctx, `SELECT `+mailSyncColumns+` FROM mail_sync_runs WHERE owner_id=$1 AND account_id=$2 ORDER BY created_at DESC,id DESC LIMIT 20`, owner, id)
	if e != nil {
		return nil, mailsync.ErrUnavailable
	}
	defer rows.Close()
	out := []mailsync.Run{}
	for rows.Next() {
		v, e := scanMailSync(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	if rows.Err() != nil {
		return nil, mailsync.ErrUnavailable
	}
	return out, nil
}
func (r *MailSyncRepository) Cancel(ctx context.Context, owner, id, run string) (mailsync.Run, error) {
	tx, e := r.beginAccount(ctx, owner, id)
	if e != nil {
		return mailsync.Run{}, e
	}
	defer tx.Rollback(ctx)
	v, e := scanMailSync(tx.QueryRow(ctx, `SELECT `+mailSyncColumns+` FROM mail_sync_runs WHERE id=$1 AND owner_id=$2 AND account_id=$3 FOR UPDATE`, run, owner, id))
	if e != nil {
		return v, e
	}
	if v.State != "queued" && v.State != "leased" && v.State != "running" {
		return v, mailsync.ErrConflict
	}
	if endSync(ctx, tx, run, "cancelled", "cancelled") != nil || auditSync(ctx, tx, owner, id, run, "sync_cancelled") != nil {
		return mailsync.Run{}, mailsync.ErrUnavailable
	}
	v, e = scanMailSync(tx.QueryRow(ctx, `SELECT `+mailSyncColumns+` FROM mail_sync_runs WHERE id=$1`, run))
	if e != nil {
		return v, e
	}
	if tx.Commit(ctx) != nil {
		return mailsync.Run{}, mailsync.ErrUnavailable
	}
	return v, nil
}
func endSync(ctx context.Context, tx pgx.Tx, id, state, code string) error {
	_, e := tx.Exec(ctx, `UPDATE mail_sync_runs SET state=$2,error_code=$3,finished_at=clock_timestamp(),lease_id=NULL,lease_until=NULL WHERE id=$1`, id, state, code)
	return e
}
