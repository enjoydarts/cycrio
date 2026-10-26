package mailprocessing

import (
	"context"
	"errors"
	"github.com/enjoydarts/cycrio/internal/mailcontent"
	"github.com/enjoydarts/cycrio/internal/secretstore"
)

type Service struct {
	repo    Repository
	keys    *secretstore.Keyring
	reader  mailcontent.Reader
	client  InferenceClient
	resolve CredentialResolver
}

func NewService(r Repository, k *secretstore.Keyring, reader mailcontent.Reader, client InferenceClient, resolve CredentialResolver) *Service {
	return &Service{r, k, reader, client, resolve}
}
func safe(e error) error {
	if e == nil {
		return nil
	}
	for _, known := range []error{ErrInvalid, ErrConflict, ErrNotFound} {
		if errors.Is(e, known) {
			return known
		}
	}
	return ErrUnavailable
}
func (s *Service) AdminRead(ctx context.Context, actor string) (PolicyState, error) {
	if !ValidID(actor) {
		return PolicyState{}, ErrInvalid
	}
	v, e := s.repo.AdminRead(ctx, actor)
	return v, safe(e)
}
func (s *Service) SavePolicy(ctx context.Context, actor string, in PolicyInput) (PolicyState, error) {
	if !ValidID(actor) || !ValidPolicy(in) {
		return PolicyState{}, ErrInvalid
	}
	v, e := s.repo.SavePolicy(ctx, actor, in)
	return v, safe(e)
}
func (s *Service) Preview(ctx context.Context, owner, account string) (Preview, error) {
	if !ValidID(owner) || !ValidID(account) {
		return Preview{}, ErrInvalid
	}
	v, e := s.repo.Preview(ctx, owner, account)
	return v, safe(e)
}
func (s *Service) Request(ctx context.Context, owner, account string, in Input) (Run, error) {
	if !ValidID(owner) || !ValidID(account) || !ValidInput(in) {
		return Run{}, ErrInvalid
	}
	v, e := s.repo.Request(ctx, owner, account, in)
	return v, safe(e)
}
func (s *Service) Retry(ctx context.Context, owner, account, message string, in RetryInput) (Run, error) {
	if !ValidID(owner) || !ValidID(account) || !ValidID(message) || !ValidRetry(in) {
		return Run{}, ErrInvalid
	}
	v, e := s.repo.Retry(ctx, owner, account, message, in)
	return v, safe(e)
}
func (s *Service) ListRuns(ctx context.Context, owner, account string) ([]Run, error) {
	if !ValidID(owner) || !ValidID(account) {
		return nil, ErrInvalid
	}
	v, e := s.repo.ListRuns(ctx, owner, account)
	if v == nil && e == nil {
		v = []Run{}
	}
	return v, safe(e)
}
func (s *Service) Results(ctx context.Context, owner, account string) ([]Result, error) {
	if !ValidID(owner) || !ValidID(account) {
		return nil, ErrInvalid
	}
	v, e := s.repo.Results(ctx, owner, account)
	if v == nil && e == nil {
		v = []Result{}
	}
	return v, safe(e)
}
func (s *Service) Cancel(ctx context.Context, owner, account, run string) (Run, error) {
	if !ValidID(owner) || !ValidID(account) || !ValidID(run) {
		return Run{}, ErrInvalid
	}
	v, e := s.repo.Cancel(ctx, owner, account, run)
	return v, safe(e)
}
func (s *Service) Claim(ctx context.Context) ([]Lease, error) {
	v, e := s.repo.Claim(ctx)
	if v == nil && e == nil {
		v = []Lease{}
	}
	return v, safe(e)
}
