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
	factory mailcontent.ReaderFactory
}

type Option func(*Service)

func WithReaderFactory(factory mailcontent.ReaderFactory) Option {
	return func(s *Service) { s.factory = factory }
}
func NewService(r Repository, k *secretstore.Keyring, reader mailcontent.Reader, client InferenceClient, resolve CredentialResolver, options ...Option) *Service {
	s := &Service{repo: r, keys: k, reader: reader, client: client, resolve: resolve}
	for _, option := range options {
		option(s)
	}
	return s
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
func (s *Service) RetryFailed(ctx context.Context, owner, account string, in Input) (RetryBatch, error) {
	if !ValidID(owner) || !ValidID(account) || !ValidInput(in) {
		return RetryBatch{}, ErrInvalid
	}
	repo, ok := s.repo.(interface {
		RetryFailed(context.Context, string, string, Input) (RetryBatch, error)
	})
	if !ok {
		return RetryBatch{}, ErrUnavailable
	}
	v, e := repo.RetryFailed(ctx, owner, account, in)
	return v, safe(e)
}
func (s *Service) Reanalyze(ctx context.Context, owner, account string, in ReanalysisInput) (RetryBatch, error) {
	if !ValidID(owner) || !ValidID(account) || !ValidReanalysis(in) {
		return RetryBatch{}, ErrInvalid
	}
	repo, ok := s.repo.(interface {
		Reanalyze(context.Context, string, string, ReanalysisInput) (RetryBatch, error)
	})
	if !ok {
		return RetryBatch{}, ErrUnavailable
	}
	v, e := repo.Reanalyze(ctx, owner, account, in)
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

func (s *Service) ImportHistory(ctx context.Context, owner, account string, limit, offset int) (ImportHistoryPage, error) {
	if !ValidID(owner) || !ValidID(account) || limit < 1 || limit > 100 || offset < 0 || offset > 1000000 {
		return ImportHistoryPage{}, ErrInvalid
	}
	repo, ok := s.repo.(interface {
		ImportHistory(context.Context, string, string, int, int) (ImportHistoryPage, error)
	})
	if !ok {
		return ImportHistoryPage{}, ErrUnavailable
	}
	v, e := repo.ImportHistory(ctx, owner, account, limit, offset)
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
