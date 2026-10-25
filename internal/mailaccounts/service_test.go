package mailaccounts

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"math"
	"strings"
	"testing"
)

func TestSaveRejectsInvalidInputBeforeStorageOrEncryption(t *testing.T) {
	owner, id := uuid.NewString(), uuid.NewString()
	valid := SaveInput{Label: "dummy label", Login: "dummy@example.test", AppPassword: "dummy-app-password"}
	tests := []struct {
		name   string
		change func(*SaveInput)
	}{
		{"negative version", func(in *SaveInput) { in.ExpectedVersion = -1 }},
		{"overflow version", func(in *SaveInput) { in.ExpectedVersion = math.MaxInt64 }},
		{"empty label", func(in *SaveInput) { in.Label = "  " }},
		{"long label", func(in *SaveInput) { in.Label = strings.Repeat("あ", 81) }},
		{"invalid label utf8", func(in *SaveInput) { in.Label = string([]byte{0xff}) }},
		{"control label", func(in *SaveInput) { in.Label = "a\x00b" }},
		{"display name", func(in *SaveInput) { in.Login = "Dummy <dummy@example.test>" }},
		{"multiple emails", func(in *SaveInput) { in.Login = "a@example.test,b@example.test" }},
		{"email control", func(in *SaveInput) { in.Login = "dummy\n@example.test" }},
		{"email whitespace", func(in *SaveInput) { in.Login = " dummy@example.test" }},
		{"incomplete email", func(in *SaveInput) { in.Login = "dummy" }},
		{"email too long", func(in *SaveInput) { in.Login = strings.Repeat("a", 250) + "@example.test" }},
		{"short password", func(in *SaveInput) { in.AppPassword = "1234567" }},
		{"long password", func(in *SaveInput) { in.AppPassword = strings.Repeat("x", 1025) }},
		{"password space", func(in *SaveInput) { in.AppPassword = "dummy password" }},
		{"password newline", func(in *SaveInput) { in.AppPassword = "dummy\npassword" }},
		{"password unicode", func(in *SaveInput) { in.AppPassword = "dummy-passwordあ" }},
	}
	s := NewService(nil, nil, nil)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			in := valid
			test.change(&in)
			if _, err := s.Save(context.Background(), owner, id, in); !errors.Is(err, ErrInvalid) {
				t.Fatal(err)
			}
		})
	}
	for _, bad := range []string{"", uuid.Nil.String(), strings.ToUpper(owner), "invalid"} {
		if _, err := s.Save(context.Background(), bad, id, valid); !errors.Is(err, ErrInvalid) {
			t.Fatal("owner", err)
		}
		if _, err := s.Save(context.Background(), owner, bad, valid); !errors.Is(err, ErrInvalid) {
			t.Fatal("account", err)
		}
	}
}
func TestInvalidOwnedOperationsDoNotReachRepository(t *testing.T) {
	ctx := context.Background()
	s := NewService(nil, nil, nil)
	owner, id := uuid.NewString(), uuid.NewString()
	for _, call := range []func() error{
		func() error { _, e := s.List(ctx, "invalid"); return e },
		func() error { _, e := s.Disconnect(ctx, owner, id, -1); return e },
		func() error { _, e := s.Probe(ctx, owner, id, "invalid"); return e },
		func() error { _, e := s.RequestProbe(ctx, owner, id, ProbeInput{RequestID: "invalid"}); return e },
		func() error {
			_, e := s.SaveFolders(ctx, owner, id, FolderInput{FolderIDs: []string{"invalid"}})
			return e
		},
		func() error {
			f := uuid.NewString()
			_, e := s.SaveFolders(ctx, owner, id, FolderInput{FolderIDs: []string{f, f}})
			return e
		},
		func() error { return s.Run(ctx, Lease{ProbeID: id, LeaseID: "invalid"}) },
	} {
		if err := call(); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
}
func TestInternalCredentialAndWorkCannotBeSerialized(t *testing.T) {
	for _, v := range []any{CredentialVersion{ID: "private-id", OwnerID: "private-owner", AccountID: "private-account", Version: 123}, Work{Credential: CredentialVersion{ID: "private-id"}}} {
		data, err := json.Marshal(v)
		if err != nil || string(data) != "{}" {
			t.Fatal(string(data), err)
		}
	}
	for _, code := range []string{"authentication_failed", "tls_failed", "connection_failed", "timeout", "response_too_large", "folder_limit", "invalid_response", "credentials_unavailable", "cancelled", "lease_expired"} {
		if got := errorCode(AdapterError{Code: code}); got != code {
			t.Fatal(got)
		}
		if got := errorCode(&AdapterError{Code: code}); got != code {
			t.Fatal(got)
		}
	}
	if (AdapterError{Code: "dummy-secret"}).Error() != "mail connection check failed" {
		t.Fatal("adapter error exposed code")
	}
}
