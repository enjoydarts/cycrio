//go:build integration

package database

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/mailoauth"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
	"path/filepath"
	"testing"
	"time"
)

func TestMailOAuthStateIsEncryptedSingleUseAndOwnerBound(t *testing.T) {
	identity, pool := testStore(t)
	ctx := context.Background()
	owner, e := identity.AllowIdentity(ctx, "https://oauth.example.test", "owner", true)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "keys.json")
	if e = secretstore.Initialize(path); e != nil {
		t.Fatal(e)
	}
	keys, e := secretstore.Load(path)
	if e != nil {
		t.Fatal(e)
	}
	cs := credentials.NewService(NewCredentialRepository(pool), keys)
	config, _ := json.Marshal(mailoauth.Config{ClientID: "dummy-client", ClientSecret: "dummy-secret", RedirectURI: "http://localhost:8090/v1/mail/oauth/callback/gmail"})
	if _, e = cs.Put(ctx, owner.ID, "gmail_oauth", 0, string(config)); e != nil {
		t.Fatal(e)
	}
	r := NewMailOAuthRepository(pool, keys)
	client, e := r.ActiveClient(ctx, "gmail")
	if e != nil {
		t.Fatal(e)
	}
	p := mailoauth.Pending{OwnerID: owner.ID, AccountID: uuid.NewString(), Provider: "gmail", Label: "Gmail", ClientID: client, Verifier: "private-verifier", ExpiresAt: time.Now().Add(time.Minute)}
	if e = r.SavePending(ctx, "hash-state", "hash-browser", p); e != nil {
		t.Fatal(e)
	}
	var encrypted []byte
	if e = pool.QueryRow(ctx, `SELECT ciphertext FROM mail_oauth_pending WHERE state_hash='hash-state'`).Scan(&encrypted); e != nil {
		t.Fatal(e)
	}
	if json.Valid(encrypted) {
		t.Fatal("plaintext pending stored")
	}
	if _, e = r.ConsumePending(ctx, "gmail", "hash-state", "wrong-browser"); !errors.Is(e, mailaccounts.ErrNotFound) {
		t.Fatal("wrong browser accepted", e)
	}
	got, e := r.ConsumePending(ctx, "gmail", "hash-state", "hash-browser")
	if e != nil || got.Verifier != p.Verifier || got.OwnerID != owner.ID {
		t.Fatal("pending missing", e)
	}
	if _, e = r.ConsumePending(ctx, "gmail", "hash-state", "hash-browser"); !errors.Is(e, mailaccounts.ErrNotFound) {
		t.Fatal("state replay accepted", e)
	}
	token := mailoauth.Token{AccessToken: "dummy-access", RefreshToken: "dummy-refresh", Expiry: time.Now().Add(time.Hour)}
	if e = r.SaveConnection(ctx, p, token); e != nil {
		t.Fatal(e)
	}
	list, e := NewMailAccountRepository(pool).List(ctx, owner.ID)
	if e != nil || len(list) != 1 || list[0].Provider != "gmail" {
		t.Fatal("OAuth account not saved", e)
	}
	if e = r.SaveConnection(ctx, p, token); !errors.Is(e, mailaccounts.ErrConflict) {
		t.Fatal("callback raced expected version", e)
	}
	var v mailaccounts.CredentialVersion
	if e = pool.QueryRow(ctx, `SELECT id,owner_id,account_id,version,key_id,ciphertext FROM mail_account_credentials WHERE account_id=$1`, p.AccountID).Scan(&v.ID, &v.OwnerID, &v.AccountID, &v.Version, &v.Envelope.KeyID, &v.Envelope.Ciphertext); e != nil {
		t.Fatal(e)
	}
	secret, e := r.WithConnection(ctx, v, func(s mailaccounts.ConnectionSecret) (mailaccounts.ConnectionSecret, error) {
		if string(s.Password) != "dummy-refresh" {
			t.Fatal("refresh unavailable")
		}
		s.Password = []byte("rotated-refresh")
		return s, nil
	})
	if e != nil || string(secret.Password) != "rotated-refresh" {
		t.Fatal("refresh update", e)
	}
	if _, e = NewMailAccountRepository(pool).Disconnect(ctx, owner.ID, p.AccountID, list[0].Version); e != nil {
		t.Fatal(e)
	}
	called := false
	if _, e = r.WithConnection(ctx, v, func(s mailaccounts.ConnectionSecret) (mailaccounts.ConnectionSecret, error) {
		called = true
		return s, nil
	}); !errors.Is(e, mailaccounts.ErrNotFound) || called {
		t.Fatal("disconnected credentials revived", e)
	}
	p.AccountID = uuid.NewString()
	if e = r.SavePending(ctx, "stop-state", "stop-browser", p); e != nil {
		t.Fatal(e)
	}
	if e = identity.SetUserState(ctx, owner.ID, "deleting"); e != nil {
		t.Fatal(e)
	}
	if _, e = r.ConsumePending(ctx, "gmail", "stop-state", "stop-browser"); !errors.Is(e, mailaccounts.ErrNotFound) {
		t.Fatal("stopped owner consumed state", e)
	}
}
