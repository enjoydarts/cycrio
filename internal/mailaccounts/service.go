package mailaccounts

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
	"time"
)

type Service struct {
	repository Repository
	keyring    *secretstore.Keyring
	checker    Checker
}

func NewService(repo Repository, keyring *secretstore.Keyring, checker Checker) *Service {
	return &Service{repo, keyring, checker}
}
func safeError(err error) error {
	if err == nil {
		return nil
	}
	for _, known := range []error{ErrInvalid, ErrConflict, ErrNotFound} {
		if errors.Is(err, known) {
			return known
		}
	}
	return ErrUnavailable
}
func credentialAAD(v CredentialVersion) []byte {
	data, _ := json.Marshal(struct {
		Owner, Account, ID string
		Revision           int64
		Provider, Purpose  string
	}{v.OwnerID, v.AccountID, v.ID, v.Version, "fastmail", "fastmail-imap/v1"})
	return data
}

// CredentialAAD shares the version-bound credential context with mail workers.
func CredentialAAD(v CredentialVersion) []byte { return credentialAAD(v) }

type plaintext struct {
	Login    []byte `json:"login"`
	Password []byte `json:"password"`
}

func (s *Service) List(ctx context.Context, owner string) ([]Account, error) {
	if !validID(owner) {
		return nil, ErrInvalid
	}
	items, err := s.repository.List(ctx, owner)
	if items == nil && err == nil {
		items = []Account{}
	}
	return items, safeError(err)
}
func (s *Service) Save(ctx context.Context, owner, id string, in SaveInput) (Account, error) {
	label, ok := validSave(owner, id, in)
	if !ok {
		return Account{}, ErrInvalid
	}
	// Use the new configuration revision as the credential revision. Folder edits
	// and disconnects may leave gaps; a credential revision is never reused.
	v := CredentialVersion{ID: uuid.NewString(), OwnerID: owner, AccountID: id, Version: in.ExpectedVersion + 1}
	p := plaintext{[]byte(in.Login), []byte(in.AppPassword)}
	defer clear(p.Login)
	defer clear(p.Password)
	plain, err := json.Marshal(p)
	if err != nil {
		return Account{}, ErrUnavailable
	}
	defer clear(plain)
	v.Envelope, err = s.keyring.Seal(plain, credentialAAD(v))
	if err != nil {
		return Account{}, ErrUnavailable
	}
	a, err := s.repository.Save(ctx, owner, id, in.ExpectedVersion, label, v)
	return a, safeError(err)
}
func (s *Service) Disconnect(ctx context.Context, owner, id string, expected int64) (Account, error) {
	if !validOwned(owner, id, expected) {
		return Account{}, ErrInvalid
	}
	a, err := s.repository.Disconnect(ctx, owner, id, expected)
	return a, safeError(err)
}
func (s *Service) RequestProbe(ctx context.Context, owner, id string, in ProbeInput) (Probe, error) {
	if !validOwned(owner, id, in.ExpectedVersion) || !validID(in.RequestID) {
		return Probe{}, ErrInvalid
	}
	p, err := s.repository.RequestProbe(ctx, owner, id, in)
	return p, safeError(err)
}
func (s *Service) Probe(ctx context.Context, owner, id, probe string) (Probe, error) {
	if !validID(owner) || !validID(id) || !validID(probe) {
		return Probe{}, ErrInvalid
	}
	p, err := s.repository.Probe(ctx, owner, id, probe)
	return p, safeError(err)
}
func (s *Service) SaveFolders(ctx context.Context, owner, id string, in FolderInput) (Account, error) {
	if !validOwned(owner, id, in.ExpectedVersion) || len(in.FolderIDs) > 1000 {
		return Account{}, ErrInvalid
	}
	seen := map[string]bool{}
	for _, f := range in.FolderIDs {
		if !validID(f) || seen[f] {
			return Account{}, ErrInvalid
		}
		seen[f] = true
	}
	a, err := s.repository.SaveFolders(ctx, owner, id, in)
	return a, safeError(err)
}
func (s *Service) Claim(ctx context.Context) ([]Lease, error) {
	items, err := s.repository.Claim(ctx)
	if items == nil && err == nil {
		items = []Lease{}
	}
	return items, safeError(err)
}
func errorCode(err error) string {
	var a AdapterError
	if errors.As(err, &a) && validErrorCode(a.Code) {
		return a.Code
	}
	var p *AdapterError
	if errors.As(err, &p) && p != nil && validErrorCode(p.Code) {
		return p.Code
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	return "connection_failed"
}
func (s *Service) Run(ctx context.Context, lease Lease) error {
	if !validID(lease.ProbeID) || !validID(lease.LeaseID) {
		return ErrInvalid
	}
	work, err := s.repository.Start(ctx, lease)
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrConflict) {
		return nil
	}
	if err != nil {
		return safeError(err)
	}
	code := ""
	var folders []RemoteFolder
	plain, err := s.keyring.Open(work.Credential.Envelope, credentialAAD(work.Credential))
	defer clear(plain)
	var p plaintext
	decodeErr := json.Unmarshal(plain, &p)
	// Decoding may populate fields before returning an error; clear those too.
	defer clear(p.Login)
	defer clear(p.Password)
	if err != nil || decodeErr != nil || len(p.Login) == 0 || len(p.Password) == 0 {
		code = "credentials_unavailable"
	} else {
		runctx, cancel := context.WithTimeout(ctx, 45*time.Second)
		if s.checker == nil {
			code = "connection_failed"
		} else {
			folders, err = s.checker.Check(runctx, string(p.Login), p.Password)
			if err != nil {
				code = errorCode(err)
			} else if runctx.Err() != nil {
				code = errorCode(runctx.Err())
			} else if len(folders) > 1000 {
				code = "folder_limit"
			} else if !validFolders(folders) {
				code = "invalid_response"
			}
		}
		cancel()
	}
	if code != "" {
		folders = nil
	}
	finishctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return safeError(s.repository.Finish(finishctx, lease, code, folders))
}
