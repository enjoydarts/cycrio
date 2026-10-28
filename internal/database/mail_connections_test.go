//go:build integration

package database

import (
	"context"
	"errors"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
	"path/filepath"
	"testing"
)

func TestMailConnectionProviderMetadataAndOwnerBoundary(t *testing.T) {
	identity, pool := testStore(t)
	ctx := context.Background()
	owner, e := identity.AllowIdentity(ctx, "https://connection.example.test", "owner", false)
	if e != nil {
		t.Fatal(e)
	}
	admin, e := identity.AllowIdentity(ctx, "https://connection.example.test", "admin", true)
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
	s := mailaccounts.NewService(NewMailAccountRepository(pool), keys, nil)
	in := mailaccounts.SaveInput{Provider: "imap", Label: "Public IMAP", Host: "mail.example.com", Port: 993, Login: "mailbox", AppPassword: "private fixture password"}
	id := uuid.NewString()
	a, e := s.Save(ctx, owner.ID, id, in)
	if e != nil || a.Provider != "imap" || a.Host != "mail.example.com" || a.Port != 993 {
		t.Fatal("IMAP metadata", e)
	}
	if _, e = s.Save(ctx, admin.ID, id, in); !errors.Is(e, mailaccounts.ErrNotFound) {
		t.Fatal("admin crossed owner")
	}
	list, e := s.List(ctx, owner.ID)
	if e != nil || len(list) != 1 || list[0].Provider != "imap" {
		t.Fatal("provider list", e)
	}
	in.Provider = "fastmail"
	in.Host = ""
	in.Port = 0
	in.Login = "user@example.com"
	in.AppPassword = "dummy-password"
	in.ExpectedVersion = 1
	if _, e = s.Save(ctx, owner.ID, id, in); !errors.Is(e, mailaccounts.ErrConflict) {
		t.Fatal("provider replacement accepted", e)
	}
}
