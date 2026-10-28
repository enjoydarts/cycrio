package database

import (
	"context"
	"errors"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const mailProbeColumns = `p.id,p.account_id,p.state,p.error_code,p.created_at,p.started_at,p.finished_at`

func scanMailProbe(row pgx.Row) (mailaccounts.Probe, error) {
	var p mailaccounts.Probe
	err := row.Scan(&p.ID, &p.AccountID, &p.State, &p.ErrorCode, &p.CreatedAt, &p.StartedAt, &p.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, mailaccounts.ErrNotFound
	}
	if err != nil {
		return p, mailaccounts.ErrUnavailable
	}
	return p, nil
}
func (r *MailAccountRepository) RequestProbe(ctx context.Context, owner, id string, in mailaccounts.ProbeInput) (mailaccounts.Probe, error) {
	tx, err := r.beginOwned(ctx, owner)
	if err != nil {
		return mailaccounts.Probe{}, err
	}
	defer tx.Rollback(ctx)
	// Account ownership is checked before the owner-scoped idempotency lookup.
	a, err := scanMailAccount(tx.QueryRow(ctx, `SELECT `+mailAccountColumns+` FROM mail_accounts WHERE id=$1 AND owner_id=$2 FOR UPDATE`, id, owner))
	if err != nil {
		return mailaccounts.Probe{}, err
	}
	existing, err := scanMailProbe(tx.QueryRow(ctx, `SELECT `+mailProbeColumns+` FROM mail_account_probes p WHERE p.owner_id=$1 AND p.request_id=$2`, owner, in.RequestID))
	if err == nil {
		if existing.AccountID != id {
			return mailaccounts.Probe{}, mailaccounts.ErrConflict
		}
		if tx.Commit(ctx) != nil {
			return mailaccounts.Probe{}, mailaccounts.ErrUnavailable
		}
		return existing, nil
	}
	if !errors.Is(err, mailaccounts.ErrNotFound) {
		return mailaccounts.Probe{}, err
	}
	if a.Version != in.ExpectedVersion || a.State == "disconnected" {
		return mailaccounts.Probe{}, mailaccounts.ErrConflict
	}
	var pending bool
	if tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM mail_account_probes WHERE account_id=$1 AND state IN ('queued','leased','running'))`, id).Scan(&pending) != nil {
		return mailaccounts.Probe{}, mailaccounts.ErrUnavailable
	}
	if pending {
		return mailaccounts.Probe{}, mailaccounts.ErrConflict
	}
	var credential string
	if err := tx.QueryRow(ctx, `SELECT id FROM mail_account_credentials WHERE account_id=$1 AND owner_id=$2 AND version=$3 AND state='active'`, id, owner, a.CredentialVersion).Scan(&credential); err != nil {
		return mailaccounts.Probe{}, mailaccounts.ErrConflict
	}
	p, err := scanMailProbe(tx.QueryRow(ctx, `INSERT INTO mail_account_probes AS p(id,owner_id,account_id,request_id,config_version,credential_id,state) VALUES($1,$2,$3,$4,$5,$6,'queued') RETURNING `+mailProbeColumns, uuid.NewString(), owner, id, in.RequestID, a.Version, credential))
	if err != nil {
		return mailaccounts.Probe{}, err
	}
	if auditMail(ctx, tx, owner, id, "probe_requested", a.Version) != nil || tx.Commit(ctx) != nil {
		return mailaccounts.Probe{}, mailaccounts.ErrUnavailable
	}
	return p, nil
}
func (r *MailAccountRepository) Probe(ctx context.Context, owner, id, probe string) (mailaccounts.Probe, error) {
	tx, err := r.beginOwned(ctx, owner)
	if err != nil {
		return mailaccounts.Probe{}, err
	}
	defer tx.Rollback(ctx)
	p, err := scanMailProbe(tx.QueryRow(ctx, `SELECT `+mailProbeColumns+` FROM mail_account_probes p JOIN mail_accounts a ON a.id=p.account_id AND a.owner_id=p.owner_id WHERE p.id=$1 AND p.account_id=$2 AND p.owner_id=$3`, probe, id, owner))
	if err != nil {
		return p, err
	}
	if tx.Commit(ctx) != nil {
		return mailaccounts.Probe{}, mailaccounts.ErrUnavailable
	}
	return p, nil
}
func (r *MailAccountRepository) Claim(ctx context.Context) ([]mailaccounts.Lease, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, mailaccounts.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	// A leased job has not read credentials; a running job is never automatically retried.
	if _, err := tx.Exec(ctx, `UPDATE mail_account_probes SET state='queued',lease_id=NULL,lease_until=NULL WHERE state='leased' AND lease_until<=clock_timestamp()`); err != nil {
		return nil, mailaccounts.ErrUnavailable
	}
	if _, err := tx.Exec(ctx, `UPDATE mail_account_probes SET state='failed',error_code='lease_expired',finished_at=clock_timestamp(),lease_id=NULL,lease_until=NULL WHERE state='running' AND lease_until<=clock_timestamp()`); err != nil {
		return nil, mailaccounts.ErrUnavailable
	}
	rows, err := tx.Query(ctx, `SELECT id FROM mail_account_probes WHERE state='queued' ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 10`)
	if err != nil {
		return nil, mailaccounts.ErrUnavailable
	}
	items := []mailaccounts.Lease{}
	for rows.Next() {
		var l mailaccounts.Lease
		if rows.Scan(&l.ProbeID) != nil {
			rows.Close()
			return nil, mailaccounts.ErrUnavailable
		}
		l.LeaseID = uuid.NewString()
		items = append(items, l)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, mailaccounts.ErrUnavailable
	}
	for _, l := range items {
		if _, err := tx.Exec(ctx, `UPDATE mail_account_probes SET state='leased',lease_id=$2,lease_until=clock_timestamp()+interval '2 minutes' WHERE id=$1`, l.ProbeID, l.LeaseID); err != nil {
			return nil, mailaccounts.ErrUnavailable
		}
	}
	if tx.Commit(ctx) != nil {
		return nil, mailaccounts.ErrUnavailable
	}
	return items, nil
}

// lockMailWork uses the same user → account → probe lock order as configuration changes.
// Owner state is returned instead of rejected so stopped owners' jobs can be terminated.
type lockedMailWork struct {
	owner, account, ownerState, accountState, probeState, credential, lease string
	configVersion, accountVersion, credentialVersion                        int64
	alive                                                                   bool
}

func (r *MailAccountRepository) lockMailWork(ctx context.Context, l mailaccounts.Lease) (pgx.Tx, lockedMailWork, error) {
	var w lockedMailWork
	if err := r.pool.QueryRow(ctx, `SELECT owner_id,account_id FROM mail_account_probes WHERE id=$1`, l.ProbeID).Scan(&w.owner, &w.account); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, w, mailaccounts.ErrNotFound
		}
		return nil, w, mailaccounts.ErrUnavailable
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, w, mailaccounts.ErrUnavailable
	}
	fail := func() (pgx.Tx, lockedMailWork, error) { tx.Rollback(ctx); return nil, w, mailaccounts.ErrUnavailable }
	if tx.QueryRow(ctx, `SELECT state FROM users WHERE id=$1 FOR UPDATE`, w.owner).Scan(&w.ownerState) != nil {
		return fail()
	}
	if tx.QueryRow(ctx, `SELECT state,version,credential_version FROM mail_accounts WHERE id=$1 AND owner_id=$2 FOR UPDATE`, w.account, w.owner).Scan(&w.accountState, &w.accountVersion, &w.credentialVersion) != nil {
		return fail()
	}
	if tx.QueryRow(ctx, `SELECT state,credential_id,config_version,coalesce(lease_id::text,'') FROM mail_account_probes WHERE id=$1 FOR UPDATE`, l.ProbeID).Scan(&w.probeState, &w.credential, &w.configVersion, &w.lease) != nil {
		return fail()
	}
	// Evaluate real time only after all row locks have been acquired. A SELECT's
	// projected expressions may be evaluated before its FOR UPDATE lock wait.
	if tx.QueryRow(ctx, `SELECT coalesce(lease_until>clock_timestamp(),false) FROM mail_account_probes WHERE id=$1`, l.ProbeID).Scan(&w.alive) != nil {
		return fail()
	}
	return tx, w, nil
}
func endMailProbe(ctx context.Context, tx pgx.Tx, l mailaccounts.Lease, state, code string) error {
	if _, err := tx.Exec(ctx, `UPDATE mail_account_probes SET state=$2,error_code=$3,finished_at=clock_timestamp(),lease_id=NULL,lease_until=NULL WHERE id=$1`, l.ProbeID, state, code); err != nil {
		return mailaccounts.ErrUnavailable
	}
	return nil
}
func (r *MailAccountRepository) Start(ctx context.Context, l mailaccounts.Lease) (mailaccounts.Work, error) {
	tx, w, err := r.lockMailWork(ctx, l)
	if err != nil {
		return mailaccounts.Work{}, err
	}
	defer tx.Rollback(ctx)
	if w.probeState != "leased" || w.lease != l.LeaseID || !w.alive {
		return mailaccounts.Work{}, mailaccounts.ErrNotFound
	}
	var v mailaccounts.CredentialVersion
	err = tx.QueryRow(ctx, `SELECT id,owner_id,account_id,version,key_id,ciphertext,(SELECT provider FROM mail_accounts a WHERE a.id=mail_account_credentials.account_id) FROM mail_account_credentials WHERE id=$1 AND owner_id=$2 AND account_id=$3 AND version=$4 AND state='active'`, w.credential, w.owner, w.account, w.credentialVersion).Scan(&v.ID, &v.OwnerID, &v.AccountID, &v.Version, &v.Envelope.KeyID, &v.Envelope.Ciphertext, &v.Provider)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return mailaccounts.Work{}, mailaccounts.ErrUnavailable
	}
	if w.ownerState != "active" || w.accountState == "disconnected" || w.configVersion != w.accountVersion || errors.Is(err, pgx.ErrNoRows) {
		if endMailProbe(ctx, tx, l, "cancelled", "cancelled") != nil || tx.Commit(ctx) != nil {
			return mailaccounts.Work{}, mailaccounts.ErrUnavailable
		}
		return mailaccounts.Work{}, mailaccounts.ErrNotFound
	}
	if _, err := tx.Exec(ctx, `UPDATE mail_account_probes SET state='running',started_at=clock_timestamp(),lease_until=clock_timestamp()+interval '90 seconds' WHERE id=$1`, l.ProbeID); err != nil {
		return mailaccounts.Work{}, mailaccounts.ErrUnavailable
	}
	if tx.Commit(ctx) != nil {
		return mailaccounts.Work{}, mailaccounts.ErrUnavailable
	}
	return mailaccounts.Work{Credential: v}, nil
}
func (r *MailAccountRepository) Finish(ctx context.Context, l mailaccounts.Lease, code string, folders []mailaccounts.RemoteFolder) error {
	tx, w, err := r.lockMailWork(ctx, l)
	if errors.Is(err, mailaccounts.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if w.probeState != "running" || w.lease != l.LeaseID {
		return nil
	}
	var current bool
	if tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM mail_account_credentials WHERE id=$1 AND account_id=$2 AND owner_id=$3 AND version=$4 AND state='active')`, w.credential, w.account, w.owner, w.credentialVersion).Scan(&current) != nil {
		return mailaccounts.ErrUnavailable
	}
	if w.ownerState != "active" || w.accountState == "disconnected" || w.configVersion != w.accountVersion || !current {
		if endMailProbe(ctx, tx, l, "cancelled", "cancelled") != nil || tx.Commit(ctx) != nil {
			return mailaccounts.ErrUnavailable
		}
		return nil
	}
	if !w.alive {
		code = "lease_expired"
		folders = nil
	}
	state := "failed"
	if code == "" {
		state = "succeeded"
		if err := replaceMailFolders(ctx, tx, w.account, folders); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE mail_accounts SET checked_at=now(),last_error=$2,state=CASE WHEN $2='' THEN 'ready' WHEN $2 IN ('authentication_failed','credentials_unavailable') THEN 'needs_reauth' ELSE state END,updated_at=now() WHERE id=$1`, w.account, code); err != nil {
		return mailaccounts.ErrUnavailable
	}
	if endMailProbe(ctx, tx, l, state, code) != nil || tx.Commit(ctx) != nil {
		return mailaccounts.ErrUnavailable
	}
	return nil
}
