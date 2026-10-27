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
}

func NewService(r Repository, k *secretstore.Keyring, s Scanner) *Service { return &Service{r, k, s} }
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
	raw, e := s.keyring.Open(w.Credential.Envelope, mailaccounts.CredentialAAD(w.Credential))
	defer clear(raw)
	var p struct {
		Login    []byte `json:"login"`
		Password []byte `json:"password"`
	}
	decodeErr := json.Unmarshal(raw, &p)
	defer clear(p.Login)
	defer clear(p.Password)
	if e != nil || decodeErr != nil || len(p.Login) == 0 || len(p.Password) == 0 {
		code = "credentials_unavailable"
	} else {
		callctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		if s.scanner == nil {
			code = "connection_failed"
		} else {
			page, e = s.scanner.Discover(callctx, string(p.Login), p.Password, w.Page)
			if e != nil {
				code = ErrorCode(e)
			} else if callctx.Err() != nil {
				code = ErrorCode(callctx.Err())
			} else if w.Page.Mode != "" && w.Page.Mode != "manual" && w.Page.UIDValidity != 0 && page.UIDValidity != w.Page.UIDValidity {
				code = "uid_validity_changed"
			} else if !ValidPage(w.Page, page) {
				code = "invalid_response"
			}
		}
		cancel()
	}
	if code == "" {
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
