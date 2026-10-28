package mailaccounts

import (
	"github.com/google/uuid"
	"testing"
)

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
