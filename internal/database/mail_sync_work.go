package database

import (
	"context"
	"errors"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/mailsync"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type lockedSync struct {
	owner, account, userState, accountState, state, credential, lease string
	version, accountVersion, credentialVersion                        int64
	alive                                                             bool
}

// Every mutation follows the same user -> account -> run lock order. Deadline
// evaluation is a separate statement after the final lock has been acquired.
func (r *MailSyncRepository) lockWork(ctx context.Context, id string) (pgx.Tx, lockedSync, error) {
	var w lockedSync
	e := r.pool.QueryRow(ctx, `SELECT owner_id,account_id FROM mail_sync_runs WHERE id=$1`, id).Scan(&w.owner, &w.account)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, w, mailsync.ErrNotFound
	}
	if e != nil {
		return nil, w, mailsync.ErrUnavailable
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return nil, w, mailsync.ErrUnavailable
	}
	fail := func() (pgx.Tx, lockedSync, error) { tx.Rollback(ctx); return nil, w, mailsync.ErrUnavailable }
	if tx.QueryRow(ctx, `SELECT state FROM users WHERE id=$1 FOR UPDATE`, w.owner).Scan(&w.userState) != nil {
		return fail()
	}
	if tx.QueryRow(ctx, `SELECT state,version,credential_version FROM mail_accounts WHERE id=$1 AND owner_id=$2 FOR UPDATE`, w.account, w.owner).Scan(&w.accountState, &w.accountVersion, &w.credentialVersion) != nil {
		return fail()
	}
	if tx.QueryRow(ctx, `SELECT state,config_version,credential_id,coalesce(lease_id::text,'') FROM mail_sync_runs WHERE id=$1 FOR UPDATE`, id).Scan(&w.state, &w.version, &w.credential, &w.lease) != nil {
		return fail()
	}
	if tx.QueryRow(ctx, `SELECT coalesce(lease_until>clock_timestamp(),false) FROM mail_sync_runs WHERE id=$1`, id).Scan(&w.alive) != nil {
		return fail()
	}
	return tx, w, nil
}
func validSyncWork(w lockedSync) bool {
	return w.userState == "active" && w.accountState == "ready" && w.version == w.accountVersion
}
func (r *MailSyncRepository) Claim(ctx context.Context) ([]mailsync.Lease, error) {
	// Candidate filtering avoids healthy leases occupying every queue slot.
	// The deadline is always evaluated again after acquiring all work locks.
	rows, e := r.pool.Query(ctx, `SELECT id FROM mail_sync_runs WHERE state='queued' OR (state IN ('leased','running') AND lease_until<=clock_timestamp()) ORDER BY created_at,id LIMIT 100`)
	if e != nil {
		return nil, mailsync.ErrUnavailable
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) != nil {
			rows.Close()
			return nil, mailsync.ErrUnavailable
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, mailsync.ErrUnavailable
	}
	out := []mailsync.Lease{}
	// Earlier transactions are already committed and must remain dispatchable
	// even when a later candidate fails. The dispatcher ignores error results.
	partial := func(err error) ([]mailsync.Lease, error) {
		if len(out) > 0 {
			return out, nil
		}
		return nil, err
	}
	for _, id := range ids {
		tx, w, e := r.lockWork(ctx, id)
		if e != nil {
			return partial(e)
		}
		state := w.state
		if state == "queued" || state == "leased" || state == "running" {
			if !validSyncWork(w) {
				e = endSync(ctx, tx, id, "cancelled", "cancelled")
				state = "cancelled"
			} else if state == "running" && !w.alive {
				e = endSync(ctx, tx, id, "failed", "lease_expired")
				state = "failed"
			} else if state == "leased" && !w.alive {
				state = "queued"
			}
		}
		var candidate mailsync.Lease
		if e == nil && state == "queued" {
			candidate = mailsync.Lease{RunID: id, LeaseID: uuid.NewString()}
			_, e = tx.Exec(ctx, `UPDATE mail_sync_runs SET state='leased',lease_id=$2,lease_until=clock_timestamp()+interval '2 minutes' WHERE id=$1`, id, candidate.LeaseID)
		}
		if e != nil {
			tx.Rollback(ctx)
			return partial(mailsync.ErrUnavailable)
		}
		if tx.Commit(ctx) != nil {
			tx.Rollback(ctx)
			return partial(mailsync.ErrUnavailable)
		}
		// Never publish a lease until its transaction has committed.
		if candidate.RunID != "" {
			out = append(out, candidate)
			if len(out) == 10 {
				break
			}
		}
	}
	return out, nil
}
func loadSyncPage(ctx context.Context, tx pgx.Tx, w lockedSync, id string) (mailsync.Work, error) {
	out := mailsync.Work{OwnerID: w.owner}
	var e error
	out.Run, e = scanMailSync(tx.QueryRow(ctx, `SELECT `+mailSyncColumns+` FROM mail_sync_runs WHERE id=$1`, id))
	if e != nil {
		return out, e
	}
	// Choose the immutable first unfinished identity before checking its live
	// availability. Filtering first could redirect an in-flight result to a
	// different folder after a concurrent discovery marks the original absent.
	var available bool
	e = tx.QueryRow(ctx, `SELECT f.folder_id,coalesce(m.name,''),f.uid_validity,f.after_uid,f.upper_uid,coalesce(m.selected AND m.available AND m.selectable,false) FROM mail_sync_run_folders f LEFT JOIN mail_account_folders m ON m.id=f.folder_id AND m.account_id=f.account_id WHERE f.run_id=$1 AND NOT f.done ORDER BY f.folder_id LIMIT 1`, id).Scan(&out.FolderID, &out.Page.Folder, &out.Page.UIDValidity, &out.Page.AfterUID, &out.Page.UpperUID, &available)
	if errors.Is(e, pgx.ErrNoRows) {
		return out, mailsync.ErrNotFound
	}
	if e != nil {
		return out, mailsync.ErrUnavailable
	}
	if !available {
		return out, mailsync.ErrNotFound
	}
	out.Page.Since = out.Run.Since
	out.Page.Before = out.Run.Before
	out.Page.Limit = min(100, out.Run.MaxMessages-out.Run.ExaminedCount)
	if out.Page.UIDValidity == 0 {
		e = tx.QueryRow(ctx, `SELECT uid_validity,after_uid FROM mail_sync_cursors WHERE owner_id=$1 AND account_id=$2 AND folder_id=$3 AND since=$4::date AND before=$5::date`, w.owner, w.account, out.FolderID, out.Run.Since, out.Run.Before).Scan(&out.Page.UIDValidity, &out.Page.AfterUID)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return out, mailsync.ErrUnavailable
		}
	}
	return out, nil
}
func (r *MailSyncRepository) Start(ctx context.Context, l mailsync.Lease) (mailsync.Work, error) {
	tx, w, e := r.lockWork(ctx, l.RunID)
	if e != nil {
		return mailsync.Work{}, e
	}
	defer tx.Rollback(ctx)
	if w.state != "leased" || w.lease != l.LeaseID || !w.alive {
		return mailsync.Work{}, mailsync.ErrNotFound
	}
	var credential mailsync.Work
	e = tx.QueryRow(ctx, `SELECT id,owner_id,account_id,version,key_id,ciphertext FROM mail_account_credentials WHERE id=$1 AND owner_id=$2 AND account_id=$3 AND version=$4 AND state='active'`, w.credential, w.owner, w.account, w.credentialVersion).Scan(&credential.Credential.ID, &credential.Credential.OwnerID, &credential.Credential.AccountID, &credential.Credential.Version, &credential.Credential.Envelope.KeyID, &credential.Credential.Envelope.Ciphertext)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return mailsync.Work{}, mailsync.ErrUnavailable
	}
	if !validSyncWork(w) || errors.Is(e, pgx.ErrNoRows) {
		if endSync(ctx, tx, l.RunID, "cancelled", "cancelled") != nil || tx.Commit(ctx) != nil {
			return mailsync.Work{}, mailsync.ErrUnavailable
		}
		return mailsync.Work{}, mailsync.ErrNotFound
	}
	out, e := loadSyncPage(ctx, tx, w, l.RunID)
	if errors.Is(e, mailsync.ErrNotFound) {
		if endSync(ctx, tx, l.RunID, "cancelled", "cancelled") != nil || tx.Commit(ctx) != nil {
			return out, mailsync.ErrUnavailable
		}
		return out, mailsync.ErrNotFound
	}
	if e != nil {
		return out, e
	}
	out.Credential = credential.Credential
	if _, e = tx.Exec(ctx, `UPDATE mail_sync_runs SET state='running',started_at=coalesce(started_at,clock_timestamp()),lease_until=clock_timestamp()+interval '90 seconds' WHERE id=$1`, l.RunID); e != nil || tx.Commit(ctx) != nil {
		return mailsync.Work{}, mailsync.ErrUnavailable
	}
	return out, nil
}
func (r *MailSyncRepository) Finish(ctx context.Context, l mailsync.Lease, p mailsync.Page, refs []mailsync.MessageRef, code string) error {
	tx, w, e := r.lockWork(ctx, l.RunID)
	if errors.Is(e, mailsync.ErrNotFound) {
		return nil
	}
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if w.state != "running" || w.lease != l.LeaseID {
		return nil
	}
	var current bool
	if tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM mail_account_credentials WHERE id=$1 AND owner_id=$2 AND account_id=$3 AND version=$4 AND state='active')`, w.credential, w.owner, w.account, w.credentialVersion).Scan(&current) != nil {
		return mailsync.ErrUnavailable
	}
	if !validSyncWork(w) || !current {
		if endSync(ctx, tx, l.RunID, "cancelled", "cancelled") != nil || tx.Commit(ctx) != nil {
			return mailsync.ErrUnavailable
		}
		return nil
	}
	if !w.alive {
		code = "lease_expired"
	}
	if code != "" {
		if mailsync.ErrorCode(mailaccounts.AdapterError{Code: code}) != code {
			code = "connection_failed"
		}
		if endSync(ctx, tx, l.RunID, "failed", code) != nil {
			return mailsync.ErrUnavailable
		}
		// Mark the run terminal first so the permanent cancellation trigger cannot overwrite it.
		if _, e = tx.Exec(ctx, `UPDATE mail_accounts SET last_error=$2,state=CASE WHEN $2 IN ('authentication_failed','credentials_unavailable') THEN 'needs_reauth' ELSE state END,updated_at=clock_timestamp() WHERE id=$1`, w.account, code); e != nil || tx.Commit(ctx) != nil {
			return mailsync.ErrUnavailable
		}
		return nil
	}
	work, e := loadSyncPage(ctx, tx, w, l.RunID)
	if errors.Is(e, mailsync.ErrNotFound) {
		if endSync(ctx, tx, l.RunID, "cancelled", "cancelled") != nil || tx.Commit(ctx) != nil {
			return mailsync.ErrUnavailable
		}
		return nil
	}
	if e != nil {
		return e
	}
	if !mailsync.ValidPage(work.Page, p) || len(refs) != len(p.UIDs) {
		return mailsync.ErrInvalid
	}
	added := 0
	for _, ref := range refs {
		if !mailsync.ValidID(ref.ID) || ref.Fingerprint == "" || ref.Envelope.KeyID == "" || len(ref.Envelope.Ciphertext) == 0 {
			return mailsync.ErrInvalid
		}
		tag, e := tx.Exec(ctx, `INSERT INTO mail_discovered_messages(id,owner_id,account_id,folder_id,fingerprint,key_id,ciphertext) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(owner_id,account_id,folder_id,fingerprint) DO NOTHING`, ref.ID, w.owner, w.account, work.FolderID, ref.Fingerprint, ref.Envelope.KeyID, ref.Envelope.Ciphertext)
		if e != nil {
			return mailsync.ErrUnavailable
		}
		added += int(tag.RowsAffected())
	}
	if _, e = tx.Exec(ctx, `INSERT INTO mail_sync_cursors(owner_id,account_id,folder_id,since,before,uid_validity,after_uid) VALUES($1,$2,$3,$4::date,$5::date,$6,$7) ON CONFLICT(folder_id,since,before) DO UPDATE SET uid_validity=excluded.uid_validity,after_uid=excluded.after_uid`, w.owner, w.account, work.FolderID, work.Run.Since, work.Run.Before, int64(p.UIDValidity), int64(p.AfterUID)); e != nil {
		return mailsync.ErrUnavailable
	}
	if _, e = tx.Exec(ctx, `UPDATE mail_sync_run_folders SET uid_validity=$3,after_uid=$4,upper_uid=$5,done=$6 WHERE run_id=$1 AND folder_id=$2`, l.RunID, work.FolderID, int64(p.UIDValidity), int64(p.AfterUID), int64(p.UpperUID), p.Done); e != nil {
		return mailsync.ErrUnavailable
	}
	examined := work.Run.ExaminedCount + len(p.UIDs)
	completed := work.Run.CompletedFolders
	if p.Done {
		completed++
	}
	state := "queued"
	if completed == work.Run.FolderCount {
		state = "completed"
	} else if examined >= work.Run.MaxMessages {
		state = "limited"
	}
	if _, e = tx.Exec(ctx, `UPDATE mail_sync_runs SET state=$2,examined_count=$3,added_count=added_count+$4,completed_folders=$5,lease_id=NULL,lease_until=NULL,finished_at=CASE WHEN $2 IN ('completed','limited') THEN clock_timestamp() ELSE NULL END WHERE id=$1`, l.RunID, state, examined, added, completed); e != nil || tx.Commit(ctx) != nil {
		return mailsync.ErrUnavailable
	}
	return nil
}
