package mailsync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
	"time"
)

type Service struct {
	repository Repository
	keyring    *secretstore.Keyring
	scanner    Scanner
	factory    ScannerFactory
}

type Option func(*Service)

func WithScannerFactory(factory ScannerFactory) Option {
	return func(s *Service) { s.factory = factory }
}
func NewService(r Repository, k *secretstore.Keyring, scanner Scanner, options ...Option) *Service {
	s := &Service{repository: r, keyring: k, scanner: scanner}
	for _, option := range options {
		option(s)
	}
	return s
}
func safe(err error) error {
	if err == nil {
		return nil
	}
	for _, e := range []error{ErrInvalid, ErrConflict, ErrNotFound} {
		if errors.Is(err, e) {
			return e
		}
	}
	return ErrUnavailable
}
func (s *Service) Request(ctx context.Context, owner, id string, in Input) (Run, error) {
	if !ValidID(owner) || !ValidID(id) || !ValidInput(in) {
		return Run{}, ErrInvalid
	}
	r, e := s.repository.Request(ctx, owner, id, in)
	return r, safe(e)
}
func (s *Service) List(ctx context.Context, owner, id string) ([]Run, error) {
	if !ValidID(owner) || !ValidID(id) {
		return nil, ErrInvalid
	}
	r, e := s.repository.List(ctx, owner, id)
	if r == nil && e == nil {
		r = []Run{}
	}
	return r, safe(e)
}
func (s *Service) Cancel(ctx context.Context, owner, id, run string) (Run, error) {
	if !ValidID(owner) || !ValidID(id) || !ValidID(run) {
		return Run{}, ErrInvalid
	}
	r, e := s.repository.Cancel(ctx, owner, id, run)
	return r, safe(e)
}
func (s *Service) Claim(ctx context.Context) ([]Lease, error) {
	r, e := s.repository.Claim(ctx)
	if r == nil && e == nil {
		r = []Lease{}
	}
	return r, safe(e)
}

// MessageAAD binds encrypted source references to their owner and immutable row identity.
func MessageAAD(w Work, id string) []byte {
	b, _ := json.Marshal(struct{ Owner, Account, Folder, ID, Purpose string }{w.OwnerID, w.Run.AccountID, w.FolderID, id, "mail-source/v1"})
	return b
}

// CursorAAD permits copying progress between runs and lanes of one folder,
// while rejecting substitution from another owner, account or folder.
func CursorAAD(w Work) []byte {
	b, _ := json.Marshal(struct{ Owner, Account, Folder, Purpose string }{w.OwnerID, w.Run.AccountID, w.FolderID, "mail-cursor/v1"})
	return b
}
func ErrorCode(err error) string {
	var a mailaccounts.AdapterError
	var p *mailaccounts.AdapterError
	code := ""
	if errors.As(err, &a) {
		code = a.Code
	} else if errors.As(err, &p) && p != nil {
		code = p.Code
	}
	switch code {
	case "authentication_failed", "tls_failed", "connection_failed", "timeout", "response_too_large", "folder_limit", "invalid_response", "credentials_unavailable", "cancelled", "lease_expired", "uid_validity_changed":
		return code
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	return "connection_failed"
}
func (s *Service) Execute(ctx context.Context, l Lease) error {
	if !ValidID(l.RunID) || !ValidID(l.LeaseID) {
		return ErrInvalid
	}
	w, e := s.repository.Start(ctx, l)
	if errors.Is(e, ErrNotFound) || errors.Is(e, ErrConflict) {
		return nil
	}
	if e != nil {
		return safe(e)
	}
	code := ""
	var page Page
	var refs []MessageRef
	api := APIProvider(w.Credential.Provider)
	w.Page.Provider = w.Credential.Provider
	var raw []byte
	if s.keyring == nil {
		e = secretstore.ErrUnavailable
	} else if !api {
		raw, e = s.keyring.Open(w.Credential.Envelope, mailaccounts.CredentialAAD(w.Credential))
	}
	defer clear(raw)
	var p struct {
		Login    []byte `json:"login"`
		Password []byte `json:"password"`
	}
	var decodeErr error
	if !api {
		decodeErr = json.Unmarshal(raw, &p)
	}
	defer clear(p.Login)
	defer clear(p.Password)
	if e == nil && api && (w.Page.CursorEnvelope.KeyID != "" || len(w.Page.CursorEnvelope.Ciphertext) != 0) {
		var cursor []byte
		cursor, e = s.keyring.Open(w.Page.CursorEnvelope, CursorAAD(w))
		if e == nil && (len(cursor) == 0 || len(cursor) > MaxCursorBytes) {
			e = ErrInvalid
		}
		w.Page.Cursor = string(cursor)
		clear(cursor)
	}
	if e != nil || decodeErr != nil || !api && (len(p.Login) == 0 || len(p.Password) == 0) {
		code = "credentials_unavailable"
	} else {
		callctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		scanner := s.scanner
		if s.factory != nil {
			scanner, e = s.factory(callctx, w.Credential)
		} else if api {
			scanner = nil
		}
		if e != nil {
			code = ErrorCode(e)
		} else if scanner == nil {
			code = "connection_failed"
		} else {
			page, e = scanner.Discover(callctx, string(p.Login), p.Password, w.Page)
			if e != nil {
				code = ErrorCode(e)
			} else if callctx.Err() != nil {
				code = ErrorCode(callctx.Err())
			} else if api && page.Cursor == "" {
				code = "invalid_response"
			} else if w.Page.Mode != "" && w.Page.Mode != "manual" && w.Page.UIDValidity != 0 && page.UIDValidity != w.Page.UIDValidity {
				code = "uid_validity_changed"
			} else if !ValidPage(w.Page, page) {
				code = "invalid_response"
			}
		}
		cancel()
	}
	if code == "" {
		if api {
			for _, remoteID := range page.RemoteIDs {
				tuple, _ := json.Marshal(struct{ Provider, RemoteID string }{w.Credential.Provider, remoteID})
				scoped, _ := json.Marshal(struct{ Owner, Account, Provider, RemoteID string }{w.OwnerID, w.Run.AccountID, w.Credential.Provider, remoteID})
				digest := sha256.Sum256(scoped)
				clear(scoped)
				fingerprint := hex.EncodeToString(digest[:])
				ref := MessageRef{ID: uuid.NewString(), Fingerprint: fingerprint, RemoteFingerprint: fingerprint}
				ref.Envelope, e = s.keyring.Seal(tuple, MessageAAD(w, ref.ID))
				clear(tuple)
				if e != nil {
					code = "credentials_unavailable"
					break
				}
				refs = append(refs, ref)
			}
			if code == "" {
				cursor := []byte(page.Cursor)
				page.CursorEnvelope, e = s.keyring.Seal(cursor, CursorAAD(w))
				clear(cursor)
				if e != nil {
					code = "credentials_unavailable"
				}
			}
			page.Cursor = ""
		}
		for _, uid := range page.UIDs {
			tuple, _ := json.Marshal(struct{ UIDValidity, UID uint32 }{page.UIDValidity, uid})
			scoped, _ := json.Marshal(struct {
				Owner, Account, Folder string
				UIDValidity, UID       uint32
			}{w.OwnerID, w.Run.AccountID, w.FolderID, page.UIDValidity, uid})
			digest := sha256.Sum256(scoped)
			clear(scoped)
			ref := MessageRef{ID: uuid.NewString(), Fingerprint: hex.EncodeToString(digest[:])}
			ref.Envelope, e = s.keyring.Seal(tuple, MessageAAD(w, ref.ID))
			clear(tuple)
			if e != nil {
				code = "credentials_unavailable"
				break
			}
			refs = append(refs, ref)
		}
	}
	if code != "" {
		page = Page{}
		refs = nil
	}
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return safe(s.repository.Finish(finish, l, page, refs, code))
}
