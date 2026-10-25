//go:build integration

package database

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestKeyringGuardDetectsMailOnlyCredentialsIncludingDisabledVersions(t *testing.T) {
	identity, pool := testStore(t)
	ctx := context.Background()
	owner, err := identity.AllowIdentity(ctx, "https://clerk.synthetic.test", "mail-keyring-owner", false)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewCredentialRepository(pool)
	if exists, err := repo.HasEncryptedCredentials(ctx); err != nil || exists {
		t.Fatal("empty store", exists, err)
	}
	account := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO mail_accounts(id,owner_id,label,state,version,credential_version) VALUES($1,$2,'synthetic','disconnected',1,1)`, account, owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO mail_account_credentials(id,account_id,owner_id,version,key_id,ciphertext,state) VALUES($1,$2,$3,1,'synthetic-key',decode('01','hex'),'disabled')`, uuid.NewString(), account, owner.ID); err != nil {
		t.Fatal(err)
	}
	if exists, err := repo.HasEncryptedCredentials(ctx); err != nil || !exists {
		t.Fatal("mail ciphertext must prevent replacing a lost master key", exists, err)
	}
}
