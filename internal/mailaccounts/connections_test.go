package mailaccounts

import (
	"bytes"
	"github.com/google/uuid"
	"testing"
)

func TestFranklyMailInputAndCredentialBinding(t *testing.T) {
	owner, id := uuid.NewString(), uuid.NewString()
	in := SaveInput{Provider: "franklymail", Label: "FranklyMail", Login: "person@example.test", AppPassword: "dummy-app-password"}
	if _, ok := validSave(owner, id, in); !ok {
		t.Fatal("FranklyMail password connection rejected")
	}
	for _, change := range []func(*SaveInput){
		func(v *SaveInput) { v.Host = "jmap.franklymail.com" },
		func(v *SaveInput) { v.Port = 443 },
		func(v *SaveInput) { v.Login = "mailbox" },
		func(v *SaveInput) { v.Login = "Person <person@example.test>" },
		func(v *SaveInput) { v.AppPassword = "secret\nvalue" },
	} {
		bad := in
		change(&bad)
		if _, ok := validSave(owner, id, bad); ok {
			t.Fatal("invalid FranklyMail input accepted")
		}
	}
	v := CredentialVersion{OwnerID: owner, AccountID: id, ID: uuid.NewString(), Version: 1}
	legacy := CredentialAAD(v)
	for _, provider := range []string{"fastmail", "imap", "gmail", "outlook"} {
		v.Provider = provider
		if !bytes.Equal(legacy, CredentialAAD(v)) {
			t.Fatal("legacy credential AAD changed", provider)
		}
	}
	v.Provider = "franklymail"
	if bytes.Equal(legacy, CredentialAAD(v)) {
		t.Fatal("FranklyMail AAD not provider-bound")
	}
}

func TestConnectionInputSeparatesIMAPFromOAuthAndLegacy(t *testing.T) {
	owner, id := uuid.NewString(), uuid.NewString()
	base := SaveInput{Provider: "imap", Label: "Personal", Login: "mailbox-user", AppPassword: "a password", Host: "mail.example.com", Port: 993}
	if _, ok := validSave(owner, id, base); !ok {
		t.Fatal("valid IMAP username and password rejected")
	}
	for _, change := range []func(*SaveInput){
		func(v *SaveInput) { v.Provider = "gmail" }, func(v *SaveInput) { v.Provider = "outlook" },
		func(v *SaveInput) { v.Port = 25 }, func(v *SaveInput) { v.Host = "https://mail.example.com" },
		func(v *SaveInput) { v.Host = "localhost" }, func(v *SaveInput) { v.Host = "127.0.0.1" },
		func(v *SaveInput) { v.Host = "169.254.169.254" }, func(v *SaveInput) { v.Login = "user\nsecret" },
	} {
		in := base
		change(&in)
		if _, ok := validSave(owner, id, in); ok {
			t.Fatal("unsafe or OAuth password input accepted")
		}
	}
	base.Port = 143
	if _, ok := validSave(owner, id, base); !ok {
		t.Fatal("STARTTLS port rejected")
	}
	legacy := SaveInput{Label: "Fastmail", Login: "person@example.com", AppPassword: "dummy-password"}
	if _, ok := validSave(owner, id, legacy); !ok {
		t.Fatal("legacy Fastmail input rejected")
	}
	legacy.Host = "mail.other.example"
	if _, ok := validSave(owner, id, legacy); ok {
		t.Fatal("Fastmail host override accepted")
	}
}
