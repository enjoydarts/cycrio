package mailconnectionruntime

import (
	"context"
	"fmt"
	"testing"

	"github.com/enjoydarts/cycrio/internal/mailaccounts"
)

func TestFactorySelectsFranklyMailWithoutOAuthOrIMAPPool(t *testing.T) {
	store := &connectionFixture{s: mailaccounts.ConnectionSecret{Provider: "franklymail", Login: []byte("synthetic@example.test"), Password: []byte("synthetic-app-password")}}
	pool := &readerPoolFixture{reader: &pooledReader{}}
	f := &Factory{repo: store, readers: pool, resolve: func(context.Context, string) ([]byte, error) {
		t.Fatal("JMAP used OAuth client credentials")
		return nil, nil
	}}
	for _, resolve := range []func(context.Context, mailaccounts.CredentialVersion) (any, error){
		func(ctx context.Context, v mailaccounts.CredentialVersion) (any, error) { return f.Checker(ctx, v) },
		func(ctx context.Context, v mailaccounts.CredentialVersion) (any, error) { return f.Scanner(ctx, v) },
		func(ctx context.Context, v mailaccounts.CredentialVersion) (any, error) { return f.Reader(ctx, v) },
	} {
		v, err := resolve(context.Background(), mailaccounts.CredentialVersion{Provider: "franklymail"})
		if err != nil || fmt.Sprintf("%T", v) != "*jmapmail.Client" {
			t.Fatalf("wrong adapter: %T %v", v, err)
		}
	}
	if len(pool.scopes) != 0 {
		t.Fatal("JMAP entered the IMAP pool")
	}
	store.stopped = true
	if _, err := f.Reader(context.Background(), mailaccounts.CredentialVersion{}); err == nil {
		t.Fatal("disabled JMAP connection remained usable")
	}
}
