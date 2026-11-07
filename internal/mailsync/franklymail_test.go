package mailsync

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/enjoydarts/cycrio/internal/mailaccounts"
)

func TestFranklyMailUsesEncryptedAPICursorsAndRemoteReferences(t *testing.T) {
	r, keys, lease := fixture(t)
	r.work.Credential.Provider = "franklymail"
	r.work.Page.Provider = "franklymail"
	credential, _ := json.Marshal(mailaccounts.ConnectionSecret{Login: []byte("synthetic@example.test"), Password: []byte("synthetic-app-password"), Provider: "franklymail"})
	r.work.Credential.Envelope, _ = keys.Seal(credential, mailaccounts.CredentialAAD(r.work.Credential))
	r.work.Page.CursorEnvelope, _ = keys.Seal([]byte("private-jmap-state"), CursorAAD(r.work))
	called := false
	s := NewService(r, keys, nil, WithScannerFactory(func(_ context.Context, v mailaccounts.CredentialVersion) (Scanner, error) {
		if v.Provider != "franklymail" {
			t.Fatal(v.Provider)
		}
		return scannerFunc(func(_ context.Context, login string, password []byte, req PageRequest) (Page, error) {
			called = true
			if login != "synthetic@example.test" || string(password) != "synthetic-app-password" || req.Cursor != "private-jmap-state" {
				t.Fatal("password auth or opaque cursor was mishandled")
			}
			return Page{Cursor: "private-next-state", RemoteIDs: []string{"jmap-email-id"}, Initialized: true, Done: true}, nil
		}), nil
	}))
	if err := s.Execute(context.Background(), lease); err != nil || !called || r.code != "" || len(r.refs) != 1 {
		t.Fatal(err, called, r.code, r.refs)
	}
	ref := r.refs[0]
	plain, err := keys.Open(ref.Envelope, MessageAAD(r.work, ref.ID))
	if err != nil {
		t.Fatal(err)
	}
	var source struct{ Provider, RemoteID string }
	if json.Unmarshal(plain, &source) != nil || source.Provider != "franklymail" || source.RemoteID != "jmap-email-id" || ref.RemoteFingerprint == "" {
		t.Fatal("missing scoped JMAP identity")
	}
	if r.page.Cursor != "" || r.page.CursorEnvelope.KeyID == "" {
		t.Fatal("cursor was not encrypted")
	}
}
