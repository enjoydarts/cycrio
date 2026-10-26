package mailsync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
	"path/filepath"
	"testing"
)

func TestExecuteFingerprintIsStableOnlyWithinUIDGeneration(t *testing.T) {
	r, keys, lease := fixture(t)
	generation := uint32(7)
	scanner := scannerFunc(func(context.Context, string, []byte, PageRequest) (Page, error) {
		return Page{UIDValidity: generation, AfterUID: 1, UpperUID: 1, Done: true, UIDs: []uint32{1}}, nil
	})
	service := NewService(r, keys, scanner)
	execute := func() MessageRef {
		t.Helper()
		if err := service.Execute(context.Background(), lease); err != nil || r.code != "" || len(r.refs) != 1 {
			t.Fatal(err, r.code, r.refs)
		}
		return r.refs[0]
	}
	first, repeated := execute(), execute()
	if first.Fingerprint != repeated.Fingerprint {
		t.Fatal("same UID generation must deduplicate")
	}
	if first.ID == repeated.ID || bytes.Equal(first.Envelope.Ciphertext, repeated.Envelope.Ciphertext) {
		t.Fatal("repeated references must use independent encrypted envelopes")
	}
	generation = 8
	changed := execute()
	if first.Fingerprint == changed.Fingerprint {
		t.Fatal("new UID generation reused old message identity")
	}
}

type memoryRepo struct {
	work     Work
	code     string
	refs     []MessageRef
	requests int
	page     Page
	startErr error
}

func (r *memoryRepo) Request(context.Context, string, string, Input) (Run, error) {
	r.requests++
	return Run{}, nil
}
func (r *memoryRepo) List(context.Context, string, string) ([]Run, error)         { return nil, nil }
func (r *memoryRepo) Cancel(context.Context, string, string, string) (Run, error) { return Run{}, nil }
func (r *memoryRepo) Claim(context.Context) ([]Lease, error)                      { return nil, nil }
func (r *memoryRepo) Start(context.Context, Lease) (Work, error)                  { return r.work, r.startErr }
func (r *memoryRepo) Finish(ctx context.Context, l Lease, p Page, refs []MessageRef, code string) error {
	r.code = code
	r.refs = refs
	r.page = p
	return nil
}

type scannerFunc func(context.Context, string, []byte, PageRequest) (Page, error)

func (f scannerFunc) Discover(c context.Context, l string, p []byte, r PageRequest) (Page, error) {
	return f(c, l, p, r)
}
func fixture(t *testing.T) (*memoryRepo, *secretstore.Keyring, Lease) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "keyring.json")
	if e := secretstore.Initialize(path); e != nil {
		t.Fatal(e)
	}
	keys, e := secretstore.Load(path)
	if e != nil {
		t.Fatal(e)
	}
	r := &memoryRepo{work: Work{OwnerID: uuid.NewString(), FolderID: uuid.NewString(), Run: Run{ID: uuid.NewString(), AccountID: uuid.NewString(), MaxMessages: 2}, Page: PageRequest{Folder: "INBOX", Since: "2026-01-01", Before: "2026-01-02", Limit: 2}}}
	v := mailaccounts.CredentialVersion{ID: uuid.NewString(), OwnerID: r.work.OwnerID, AccountID: r.work.Run.AccountID, Version: 1}
	raw, _ := json.Marshal(struct{ Login, Password []byte }{[]byte("dummy@example.test"), []byte("dummy-password")})
	v.Envelope, e = keys.Seal(raw, mailaccounts.CredentialAAD(v))
	if e != nil {
		t.Fatal(e)
	}
	r.work.Credential = v
	return r, keys, Lease{RunID: r.work.Run.ID, LeaseID: uuid.NewString()}
}
func TestRequestRejectsInvalidBoundaries(t *testing.T) {
	owner, id := uuid.NewString(), uuid.NewString()
	good := Input{ExpectedVersion: 1, RequestID: uuid.NewString(), Since: "2026-01-01", Before: "2026-01-02", MaxMessages: 100}
	for _, mutate := range []func(*Input){func(i *Input) { i.MaxMessages = 0 }, func(i *Input) { i.MaxMessages = 1001 }, func(i *Input) { i.Before = i.Since }, func(i *Input) { i.Before = "2027-01-03" }, func(i *Input) { i.Since = "2026-1-01" }, func(i *Input) { i.RequestID = "secret" }, func(i *Input) { i.ExpectedVersion = 0 }} {
		in := good
		mutate(&in)
		r := &memoryRepo{}
		_, e := NewService(r, nil, nil).Request(context.Background(), owner, id, in)
		if !errors.Is(e, ErrInvalid) || r.requests != 0 {
			t.Fatalf("accepted %+v: %v", in, e)
		}
	}
	r := &memoryRepo{}
	if _, e := NewService(r, nil, nil).Request(context.Background(), owner, id, good); e != nil || r.requests != 1 {
		t.Fatal(e)
	}
}

func TestRequestDateYearsAreBoundedToIMAPCalendar(t *testing.T) {
	owner, id := uuid.NewString(), uuid.NewString()
	for _, tc := range []struct {
		since, before string
		valid         bool
	}{
		{"0001-01-01", "0001-01-02", false},
		{"1899-12-31", "1900-01-01", false},
		{"1900-01-01", "1900-01-02", true},
		{"9999-12-30", "9999-12-31", true},
		{"9999-12-31", "10000-01-01", false},
	} {
		t.Run(tc.since, func(t *testing.T) {
			r := &memoryRepo{}
			in := Input{ExpectedVersion: 1, RequestID: uuid.NewString(), Since: tc.since, Before: tc.before, MaxMessages: 1}
			_, err := NewService(r, nil, nil).Request(context.Background(), owner, id, in)
			if tc.valid {
				if err != nil || r.requests != 1 {
					t.Fatal(err, r.requests)
				}
			} else if !errors.Is(err, ErrInvalid) || r.requests != 0 {
				t.Fatal("unsafe IMAP year accepted", err, r.requests)
			}
		})
	}
}
func TestExecuteEncryptsScopedUIDTupleAndClearsPassword(t *testing.T) {
	r, keys, l := fixture(t)
	var password []byte
	scan := scannerFunc(func(ctx context.Context, login string, p []byte, req PageRequest) (Page, error) {
		password = p
		if login != "dummy@example.test" || string(p) != "dummy-password" {
			t.Fatal("credentials")
		}
		return Page{UIDValidity: 9, AfterUID: 7, UpperUID: 7, Done: true, UIDs: []uint32{2, 7}}, nil
	})
	if e := NewService(r, keys, scan).Execute(context.Background(), l); e != nil {
		t.Fatal(e)
	}
	if r.code != "" || len(r.refs) != 2 {
		t.Fatal(r.code, r.refs)
	}
	for _, b := range password {
		if b != 0 {
			t.Fatal("password retained")
		}
	}
	plain, e := keys.Open(r.refs[0].Envelope, MessageAAD(r.work, r.refs[0].ID))
	if e != nil {
		t.Fatal(e)
	}
	var tuple struct{ UIDValidity, UID uint32 }
	if json.Unmarshal(plain, &tuple) != nil || tuple.UID != 2 || tuple.UIDValidity != 9 {
		t.Fatalf("tuple %s", plain)
	}
	other := r.work
	other.OwnerID = uuid.NewString()
	if _, e = keys.Open(r.refs[0].Envelope, MessageAAD(other, r.refs[0].ID)); e == nil {
		t.Fatal("owner substitution accepted")
	}
}
func TestExecuteRejectsInvalidPagesAndRawErrors(t *testing.T) {
	pages := []Page{{UIDValidity: 1, AfterUID: 2, UpperUID: 2, Done: true, UIDs: []uint32{2, 2}}, {UIDValidity: 1, AfterUID: 2, UpperUID: 2, UIDs: []uint32{3}}, {UIDValidity: 0, Done: true}, {UIDValidity: 1, AfterUID: 0, UpperUID: 3}, {UIDValidity: 1, AfterUID: 3, UpperUID: 3, UIDs: []uint32{1, 2, 3}}}
	for _, p := range pages {
		r, k, l := fixture(t)
		scan := scannerFunc(func(context.Context, string, []byte, PageRequest) (Page, error) { return p, nil })
		if e := NewService(r, k, scan).Execute(context.Background(), l); e != nil || r.code != "invalid_response" || len(r.refs) != 0 {
			t.Fatal(p, r.code, e)
		}
	}
	r, k, l := fixture(t)
	scan := scannerFunc(func(context.Context, string, []byte, PageRequest) (Page, error) {
		return Page{}, errors.New("secret-upstream-password")
	})
	if e := NewService(r, k, scan).Execute(context.Background(), l); e != nil || r.code != "connection_failed" {
		t.Fatal(e, r.code)
	}
}
