package database

import (
	"context"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (r *MailAccountRepository) SaveFolders(ctx context.Context, owner, id string, in mailaccounts.FolderInput) (mailaccounts.Account, error) {
	if in.FolderIDs == nil {
		in.FolderIDs = []string{}
	}
	tx, err := r.beginOwned(ctx, owner)
	if err != nil {
		return mailaccounts.Account{}, err
	}
	defer tx.Rollback(ctx)
	a, err := lockMailAccount(ctx, tx, owner, id, in.ExpectedVersion)
	if err != nil {
		return mailaccounts.Account{}, err
	}
	if a.State != "ready" {
		return mailaccounts.Account{}, mailaccounts.ErrConflict
	}
	// Validate the complete selection before writing any row.
	var count int
	if tx.QueryRow(ctx, `SELECT count(*) FROM mail_account_folders WHERE account_id=$1 AND id=ANY($2::uuid[]) AND available AND selectable`, id, in.FolderIDs).Scan(&count) != nil {
		return mailaccounts.Account{}, mailaccounts.ErrUnavailable
	}
	if count != len(in.FolderIDs) {
		return mailaccounts.Account{}, mailaccounts.ErrInvalid
	}
	if _, err := tx.Exec(ctx, `UPDATE mail_account_folders SET selected=(id=ANY($2::uuid[])) WHERE account_id=$1`, id, in.FolderIDs); err != nil {
		return mailaccounts.Account{}, mailaccounts.ErrUnavailable
	}
	if _, err := tx.Exec(ctx, `UPDATE mail_accounts SET version=version+1,updated_at=now() WHERE id=$1`, id); err != nil {
		return mailaccounts.Account{}, mailaccounts.ErrUnavailable
	}
	return completeMailAccount(ctx, tx, owner, id, "folders_selected", in.ExpectedVersion+1)
}
func replaceMailFolders(ctx context.Context, tx pgx.Tx, id string, items []mailaccounts.RemoteFolder) error {
	// Preserve selection and identity while marking every disappeared folder unavailable.
	names := make([]string, 0, len(items))
	for _, f := range items {
		names = append(names, f.Name)
	}
	if _, err := tx.Exec(ctx, `UPDATE mail_account_folders SET available=false,selected=false WHERE account_id=$1 AND NOT (name=ANY($2::text[]))`, id, names); err != nil {
		return mailaccounts.ErrUnavailable
	}
	for _, f := range items {
		if _, err := tx.Exec(ctx, `INSERT INTO mail_account_folders(id,account_id,name,delimiter,selectable,available) VALUES($1,$2,$3,$4,$5,true) ON CONFLICT(account_id,name) DO UPDATE SET delimiter=excluded.delimiter,selectable=excluded.selectable,available=true,selected=mail_account_folders.selected AND excluded.selectable`, uuid.NewString(), id, f.Name, f.Delimiter, f.Selectable); err != nil {
			return mailaccounts.ErrUnavailable
		}
	}
	return nil
}
