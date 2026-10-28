package mailsync

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
)

func TestAPIPageValidation(t *testing.T) {
	req := PageRequest{Provider: "gmail", Mode: "incremental", Limit: 2, Cursor: "previous"}
	good := Page{RemoteIDs: []string{"remote-a", "remote-b"}, Cursor: "next", Initialized: true}
	if !ValidPage(req, good) {
		t.Fatal("valid API page rejected")
	}
	for _, mutate := range []func(*Page){
		func(p *Page) { p.Cursor = "" }, func(p *Page) { p.Cursor = "previous" },
		func(p *Page) { p.Cursor = strings.Repeat("x", (128<<10)+1) },
		func(p *Page) { p.RemoteIDs = []string{"same", "same"} },
		func(p *Page) { p.RemoteIDs = []string{""} }, func(p *Page) { p.RemoteIDs = []string{"bad\n"} },
		func(p *Page) { p.RemoteIDs = []string{"a", "b", "c"} },
		func(p *Page) { p.UIDValidity = 1 }, func(p *Page) { p.UIDs = []uint32{1} },
		func(p *Page) { p.Initialized = false },
	} {
		p := good
		mutate(&p)
		if ValidPage(req, p) {
			t.Fatal("invalid API page accepted", p)
		}
	}
	if !ValidPage(req, Page{Cursor: "previous", Initialized: true, Done: true}) {
		t.Fatal("unchanged terminal polling checkpoint rejected")
	}
	req.Mode = "bootstrap"
	if !ValidPage(req, Page{Cursor: "next", Initialized: true}) {
		t.Fatal("multi-page bootstrap rejected")
	}
	if ValidPage(req, good) {
		t.Fatal("bootstrap inserted messages")
	}
	req.Mode = "unknown"
	if ValidPage(req, Page{Cursor: "next", Initialized: true, Done: true}) {
		t.Fatal("unknown discovery mode accepted")
	}
}

func TestAPIExecuteEncryptsCursorAndRemoteIDAcrossFolders(t *testing.T) {
	r, keys, lease := fixture(t)
	r.work.Credential.Provider = "gmail"
	r.work.Page.Provider = "gmail"
	r.work.Page.CursorEnvelope, _ = keys.Seal([]byte("private-checkpoint"), CursorAAD(r.work))
	var calls int
	factory := func(_ context.Context, credential mailaccounts.CredentialVersion) (Scanner, error) {
		if credential.Provider != "gmail" || credential.AccountID != r.work.Run.AccountID {
			t.Fatal("factory scope")
		}
		return scannerFunc(func(_ context.Context, login string, password []byte, req PageRequest) (Page, error) {
			calls++
			if login != "" || len(password) != 0 || req.Cursor != "private-checkpoint" {
				t.Fatal("API credentials/cursor")
			}
			return Page{Cursor: "private-next", RemoteIDs: []string{"private-message"}, Initialized: true, Done: true}, nil
		}), nil
	}
	s := NewService(r, keys, nil, WithScannerFactory(factory))
	if err := s.Execute(context.Background(), lease); err != nil || r.code != "" || len(r.refs) != 1 {
		t.Fatal(err, r.code, r.refs)
	}
	first := r.refs[0]
	if r.page.Cursor != "" || r.page.CursorEnvelope.KeyID == "" || bytes.Contains(r.page.CursorEnvelope.Ciphertext, []byte("private-next")) {
		t.Fatal("cursor was not encrypted")
	}
	plain, err := keys.Open(r.page.CursorEnvelope, CursorAAD(r.work))
	if err != nil || string(plain) != "private-next" {
		t.Fatal(err)
	}
	plain, err = keys.Open(first.Envelope, MessageAAD(r.work, first.ID))
	if err != nil {
		t.Fatal(err)
	}
	var ref struct{ Provider, RemoteID string }
	if json.Unmarshal(plain, &ref) != nil || ref.Provider != "gmail" || ref.RemoteID != "private-message" {
		t.Fatal("reference", string(plain))
	}
	if first.RemoteFingerprint == "" {
		t.Fatal("no account-wide fingerprint")
	}
	r.work.FolderID = uuid.NewString()
	r.work.Page.CursorEnvelope, _ = keys.Seal([]byte("private-checkpoint"), CursorAAD(r.work))
	if err := s.Execute(context.Background(), lease); err != nil || r.code != "" || r.refs[0].RemoteFingerprint != first.RemoteFingerprint {
		t.Fatal("label duplicate", err, r.code)
	}
	r.work.Page.CursorEnvelope = secretstore.Envelope{KeyID: "missing", Ciphertext: []byte("bad")}
	if err := s.Execute(context.Background(), lease); err != nil || r.code != "credentials_unavailable" || calls != 2 {
		t.Fatal("tampered cursor reached adapter", err, r.code, calls)
	}
}

func TestCursorAADScopeAllowsRunAndWindowChanges(t *testing.T) {
	r, keys, _ := fixture(t)
	envelope, _ := keys.Seal([]byte("checkpoint"), CursorAAD(r.work))
	copy := r.work
	copy.Run.ID = uuid.NewString()
	copy.Page.Mode = "historical"
	copy.Page.Since = "2025-01-01"
	if _, err := keys.Open(envelope, CursorAAD(copy)); err != nil {
		t.Fatal("run/window bound cursor", err)
	}
	for _, mutate := range []func(*Work){func(w *Work) { w.OwnerID = uuid.NewString() }, func(w *Work) { w.Run.AccountID = uuid.NewString() }, func(w *Work) { w.FolderID = uuid.NewString() }} {
		other := r.work
		mutate(&other)
		if _, err := keys.Open(envelope, CursorAAD(other)); err == nil {
			t.Fatal("cursor scope substitution")
		}
	}
}

func TestAPIExecuteRejectsAdapterWithoutPlainCheckpoint(t *testing.T) {
	r, keys, lease := fixture(t)
	r.work.Credential.Provider = "gmail"
	s := NewService(r, keys, nil, WithScannerFactory(func(context.Context, mailaccounts.CredentialVersion) (Scanner, error) {
		return scannerFunc(func(context.Context, string, []byte, PageRequest) (Page, error) {
			return Page{Initialized: true, Done: true, CursorEnvelope: secretstore.Envelope{KeyID: "fake", Ciphertext: []byte("fake")}}, nil
		}), nil
	}))
	if err := s.Execute(context.Background(), lease); err != nil || r.code != "invalid_response" || len(r.refs) != 0 {
		t.Fatal("adapter checkpoint accepted without validation", err, r.code)
	}
}
