package database

import (
	"context"
	"errors"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type MailAccountRepository struct{ pool *pgxpool.Pool }

var _ mailaccounts.Repository = (*MailAccountRepository)(nil)

func NewMailAccountRepository(pool *pgxpool.Pool) *MailAccountRepository {
	return &MailAccountRepository{pool}
}
func (r *MailAccountRepository) beginOwned(ctx context.Context, owner string) (pgx.Tx, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, mailaccounts.ErrUnavailable
	}
	var state string
	err = tx.QueryRow(ctx, `SELECT state FROM users WHERE id=$1 FOR UPDATE`, owner).Scan(&state)
	if err != nil || state != "active" {
		tx.Rollback(ctx)
		if errors.Is(err, pgx.ErrNoRows) || err == nil {
			return nil, mailaccounts.ErrNotFound
		}
		return nil, mailaccounts.ErrUnavailable
	}
	return tx, nil
}

const mailAccountColumns = `id,provider,label,state,version,credential_version,checked_at,last_error,created_at,updated_at`

func scanMailAccount(row pgx.Row) (mailaccounts.Account, error) {
	var a mailaccounts.Account
	err := row.Scan(&a.ID, &a.Provider, &a.Label, &a.State, &a.Version, &a.CredentialVersion, &a.CheckedAt, &a.LastError, &a.CreatedAt, &a.UpdatedAt)
	a.Folders = []mailaccounts.Folder{}
	if errors.Is(err, pgx.ErrNoRows) {
		return a, mailaccounts.ErrNotFound
	}
	if err != nil {
		return a, mailaccounts.ErrUnavailable
	}
	return a, nil
}
func lockMailAccount(ctx context.Context, tx pgx.Tx, owner, id string, expected int64) (mailaccounts.Account, error) {
	a, err := scanMailAccount(tx.QueryRow(ctx, `SELECT `+mailAccountColumns+` FROM mail_accounts WHERE id=$1 AND owner_id=$2 FOR UPDATE`, id, owner))
	if err == nil && a.Version != expected {
		return a, mailaccounts.ErrConflict
	}
	return a, err
}
func fillMailAccount(ctx context.Context, tx pgx.Tx, a *mailaccounts.Account) error {
	rows, err := tx.Query(ctx, `SELECT id,name,delimiter,selectable,available,selected FROM mail_account_folders WHERE account_id=$1 ORDER BY name,id`, a.ID)
	if err != nil {
		return mailaccounts.ErrUnavailable
	}
	for rows.Next() {
		var f mailaccounts.Folder
		if rows.Scan(&f.ID, &f.Name, &f.Delimiter, &f.Selectable, &f.Available, &f.Selected) != nil {
			rows.Close()
			return mailaccounts.ErrUnavailable
		}
		a.Folders = append(a.Folders, f)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return mailaccounts.ErrUnavailable
	}
	p, err := scanMailProbe(tx.QueryRow(ctx, `SELECT `+mailProbeColumns+` FROM mail_account_probes p WHERE p.account_id=$1 AND p.credential_id IN (SELECT id FROM mail_account_credentials WHERE account_id=$1 AND version=$2 AND state='active') ORDER BY p.created_at DESC,p.id DESC LIMIT 1`, a.ID, a.CredentialVersion))
	if err == nil {
		a.LatestProbe = &p
	} else if !errors.Is(err, mailaccounts.ErrNotFound) {
		return err
	}
	return nil
}
func (r *MailAccountRepository) List(ctx context.Context, owner string) ([]mailaccounts.Account, error) {
	tx, err := r.beginOwned(ctx, owner)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT `+mailAccountColumns+` FROM mail_accounts WHERE owner_id=$1 ORDER BY created_at,id`, owner)
	if err != nil {
		return nil, mailaccounts.ErrUnavailable
	}
	items := []mailaccounts.Account{}
	for rows.Next() {
		a, err := scanMailAccount(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, mailaccounts.ErrUnavailable
	}
	for i := range items {
		if err := fillMailAccount(ctx, tx, &items[i]); err != nil {
			return nil, err
		}
	}
	if tx.Commit(ctx) != nil {
		return nil, mailaccounts.ErrUnavailable
	}
	return items, nil
}
func auditMail(ctx context.Context, tx pgx.Tx, owner, id, action string, version int64) error {
	_, err := tx.Exec(ctx, `INSERT INTO mail_account_audit(id,actor_id,account_id,action,version) VALUES($1,$2,$3,$4,$5)`, uuid.NewString(), owner, id, action, version)
	if err != nil {
		return mailaccounts.ErrUnavailable
	}
	return nil
}
func completeMailAccount(ctx context.Context, tx pgx.Tx, owner, id, action string, version int64) (mailaccounts.Account, error) {
	if err := auditMail(ctx, tx, owner, id, action, version); err != nil {
		return mailaccounts.Account{}, err
	}
	a, err := scanMailAccount(tx.QueryRow(ctx, `SELECT `+mailAccountColumns+` FROM mail_accounts WHERE id=$1 AND owner_id=$2`, id, owner))
	if err != nil {
		return a, err
	}
	if err := fillMailAccount(ctx, tx, &a); err != nil {
		return mailaccounts.Account{}, err
	}
	if tx.Commit(ctx) != nil {
		return mailaccounts.Account{}, mailaccounts.ErrUnavailable
	}
	return a, nil
}
func cancelMailProbes(ctx context.Context, tx pgx.Tx, id string) error {
	_, err := tx.Exec(ctx, `UPDATE mail_account_probes SET state='cancelled',error_code='cancelled',finished_at=now(),lease_id=NULL,lease_until=NULL WHERE account_id=$1 AND state IN ('queued','leased','running')`, id)
	return err
}
func (r *MailAccountRepository) Save(ctx context.Context, owner, id string, expected int64, label string, v mailaccounts.CredentialVersion) (mailaccounts.Account, error) {
	if expected < 0 || v.Version != expected+1 || v.OwnerID != owner || v.AccountID != id || v.Envelope.KeyID == "" || len(v.Envelope.Ciphertext) == 0 {
		return mailaccounts.Account{}, mailaccounts.ErrInvalid
	}
	tx, err := r.beginOwned(ctx, owner)
	if err != nil {
		return mailaccounts.Account{}, err
	}
	defer tx.Rollback(ctx)
	_, err = lockMailAccount(ctx, tx, owner, id, expected)
	if errors.Is(err, mailaccounts.ErrNotFound) {
		var exists bool
		if tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM mail_accounts WHERE id=$1)`, id).Scan(&exists) != nil {
			return mailaccounts.Account{}, mailaccounts.ErrUnavailable
		}
		if exists {
			return mailaccounts.Account{}, mailaccounts.ErrNotFound
		}
		if expected != 0 {
			return mailaccounts.Account{}, mailaccounts.ErrConflict
		}
		var count int
		if tx.QueryRow(ctx, `SELECT count(*) FROM mail_accounts WHERE owner_id=$1`, owner).Scan(&count) != nil {
			return mailaccounts.Account{}, mailaccounts.ErrUnavailable
		}
		if count >= 10 {
			return mailaccounts.Account{}, mailaccounts.ErrConflict
		}
		_, err = tx.Exec(ctx, `INSERT INTO mail_accounts(id,owner_id,label,state,version,credential_version) VALUES($1,$2,$3,'unverified',1,$4)`, id, owner, label, v.Version)
	} else if err == nil {
		if cancelMailProbes(ctx, tx, id) != nil {
			return mailaccounts.Account{}, mailaccounts.ErrUnavailable
		}
		if _, err := tx.Exec(ctx, `DELETE FROM mail_account_folders WHERE account_id=$1`, id); err != nil {
			return mailaccounts.Account{}, mailaccounts.ErrUnavailable
		}
		_, err = tx.Exec(ctx, `UPDATE mail_accounts SET label=$2,state='unverified',version=version+1,credential_version=$3,checked_at=NULL,last_error='',updated_at=now() WHERE id=$1`, id, label, v.Version)
	}
	if err != nil {
		if errors.Is(err, mailaccounts.ErrConflict) {
			return mailaccounts.Account{}, err
		}
		return mailaccounts.Account{}, mailaccounts.ErrUnavailable
	}
	if _, err := tx.Exec(ctx, `UPDATE mail_account_credentials SET state='superseded' WHERE account_id=$1 AND state='active'`, id); err != nil {
		return mailaccounts.Account{}, mailaccounts.ErrUnavailable
	}
	if _, err := tx.Exec(ctx, `INSERT INTO mail_account_credentials(id,account_id,owner_id,version,key_id,ciphertext,state) VALUES($1,$2,$3,$4,$5,$6,'active')`, v.ID, id, owner, v.Version, v.Envelope.KeyID, v.Envelope.Ciphertext); err != nil {
		return mailaccounts.Account{}, mailaccounts.ErrUnavailable
	}
	return completeMailAccount(ctx, tx, owner, id, "account_saved", expected+1)
}
func (r *MailAccountRepository) Disconnect(ctx context.Context, owner, id string, expected int64) (mailaccounts.Account, error) {
	tx, err := r.beginOwned(ctx, owner)
	if err != nil {
		return mailaccounts.Account{}, err
	}
	defer tx.Rollback(ctx)
	a, err := lockMailAccount(ctx, tx, owner, id, expected)
	if err != nil {
		return mailaccounts.Account{}, err
	}
	if a.State == "disconnected" {
		return mailaccounts.Account{}, mailaccounts.ErrConflict
	}
	if cancelMailProbes(ctx, tx, id) != nil {
		return mailaccounts.Account{}, mailaccounts.ErrUnavailable
	}
	for _, query := range []string{`UPDATE mail_account_credentials SET state='disabled' WHERE account_id=$1`, `DELETE FROM mail_account_folders WHERE account_id=$1`, `UPDATE mail_accounts SET state='disconnected',version=version+1,checked_at=NULL,last_error='',updated_at=now() WHERE id=$1`} {
		if _, err := tx.Exec(ctx, query, id); err != nil {
			return mailaccounts.Account{}, mailaccounts.ErrUnavailable
		}
	}
	return completeMailAccount(ctx, tx, owner, id, "account_disconnected", expected+1)
}
