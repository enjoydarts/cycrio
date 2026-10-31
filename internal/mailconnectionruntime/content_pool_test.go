package mailconnectionruntime

import (
	"context"
	"errors"
	"testing"

	"github.com/enjoydarts/cycrio/internal/fastmail"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/mailcontent"
)

type pooledReader struct{}

func (*pooledReader) ReadText(context.Context, string, []byte, mailcontent.Source) ([]byte, error) {
	return []byte("synthetic pooled result"), nil
}

type readerPoolFixture struct {
	scopes []fastmail.ContentScope
	closed int
	reader *pooledReader
}

func (p *readerPoolFixture) Reader(s fastmail.ContentScope, _ *fastmail.Client) mailcontent.Reader {
	p.scopes = append(p.scopes, s)
	return p.reader
}
func (p *readerPoolFixture) Close() error { p.closed++; return nil }

func TestReaderUsesOwnerAndCredentialScopedPoolOnlyAfterCurrentValidation(t *testing.T) {
	store := &connectionFixture{s: mailaccounts.ConnectionSecret{Provider: "fastmail"}}
	pool := &readerPoolFixture{reader: &pooledReader{}}
	factory := &Factory{repo: store, readers: pool}
	v := mailaccounts.CredentialVersion{ID: "credential-a", OwnerID: "owner-a", AccountID: "account-a", Version: 3, Provider: "fastmail"}
	reader, e := factory.Reader(context.Background(), v)
	if e != nil {
		t.Fatal(e)
	}
	text, e := reader.ReadText(context.Background(), "synthetic", []byte("private"), mailcontent.Source{})
	if e != nil || string(text) != "synthetic pooled result" {
		t.Fatal("validated pooled reader not returned", e)
	}
	want := fastmail.ContentScope{OwnerID: v.OwnerID, AccountID: v.AccountID, CredentialID: v.ID, CredentialVersion: 3, Provider: v.Provider}
	if len(pool.scopes) != 1 || pool.scopes[0] != want {
		t.Fatal("pool ownership scope changed", pool.scopes)
	}
	store.stopped = true
	if _, e = factory.Reader(context.Background(), v); !errors.Is(e, mailaccounts.ErrNotFound) || len(pool.scopes) != 1 {
		t.Fatal("revoked credential reused cached reader", e)
	}
}

func TestGenericIMAPScopeIncludesEndpointAndFactoryClosesPool(t *testing.T) {
	store := &connectionFixture{s: mailaccounts.ConnectionSecret{Provider: "imap", Host: "mail.synthetic.example", Port: 993}}
	pool := &readerPoolFixture{reader: &pooledReader{}}
	factory := &Factory{repo: store, readers: pool}
	v := mailaccounts.CredentialVersion{ID: "credential", OwnerID: "owner", AccountID: "account", Version: 1, Provider: "imap", Host: "mail.synthetic.example", Port: 993}
	if _, e := factory.Reader(context.Background(), v); e != nil {
		t.Fatal(e)
	}
	if len(pool.scopes) != 1 || pool.scopes[0].Host != v.Host || pool.scopes[0].Port != 993 {
		t.Fatal("endpoint lost from scoped pooling", pool.scopes)
	}
	for range 2 {
		if e := factory.Close(); e != nil {
			t.Fatal(e)
		}
	}
	if pool.closed != 1 {
		t.Fatal("pool not closed exactly once", pool.closed)
	}
	if _, e := factory.Reader(context.Background(), v); !errors.Is(e, mailaccounts.ErrUnavailable) {
		t.Fatal("closed factory yielded reader", e)
	}
}
