package airoutes

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

type Service struct {
	repo    Repository
	fetcher *Fetcher
	resolve CredentialResolver
}

func NewService(repo Repository, fetcher *Fetcher, resolve CredentialResolver) *Service {
	if fetcher == nil {
		fetcher = NewFetcher(nil)
	}
	return &Service{repo, fetcher, resolve}
}
func (s *Service) List(ctx context.Context, f Filter) (ListResult, error) {
	f, e := ValidateFilter(f)
	if e != nil {
		return ListResult{}, e
	}
	return s.repo.List(ctx, f)
}
func (s *Service) Refresh(ctx context.Context, actor string) (ListResult, error) {
	if _, e := uuid.Parse(actor); e != nil {
		return ListResult{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 9*time.Second)
	defer cancel()
	lease, e := s.repo.Claim(ctx, actor)
	if e != nil {
		return ListResult{}, e
	}
	if !lease.Owner {
		// The durable lease coalesces requests across API instances as well as within
		// one process. Each caller still checks its own current administrator role.
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			if e = s.repo.Validate(ctx, actor, Lease{CredentialID: lease.CredentialID, CredentialVersion: lease.CredentialVersion}); e != nil {
				return ListResult{}, e
			}
			list, err := s.repo.List(ctx, Filter{IncludeUnavailable: true})
			if err != nil {
				return ListResult{}, err
			}
			if list.Refresh.Status != "running" {
				if list.Refresh.Status != "succeeded" {
					return ListResult{}, ErrUnavailable
				}
				return list, nil
			}
			select {
			case <-ctx.Done():
				return ListResult{}, ErrUnavailable
			case <-ticker.C:
			}
		}
	}
	if e = s.repo.Validate(ctx, actor, lease); e == nil {
		if s.resolve == nil {
			e = ErrCredentialsRequired
		} else {
			var key []byte
			key, e = s.resolve(ctx, lease.CredentialID)
			if e == nil {
				var rows []Route
				rows, e = s.fetcher.Fetch(ctx, key)
				clear(key)
				if e == nil {
					e = s.repo.Validate(ctx, actor, lease)
				}
				if e == nil {
					e = s.repo.Apply(ctx, actor, lease, rows)
				}
			} else {
				clear(key)
			}
		}
	}
	if e == nil {
		return s.repo.List(ctx, Filter{IncludeUnavailable: true})
	}
	code := "provider_unavailable"
	if errors.Is(e, ErrCredentialsRequired) {
		code = "credentials_required"
	} else if errors.Is(e, ErrConflict) {
		code = "conflict"
	} else if ctx.Err() != nil {
		code = "cancelled"
	}
	cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer stop()
	_ = s.repo.Finish(cleanup, lease, code)
	if code == "credentials_required" {
		return ListResult{}, ErrCredentialsRequired
	}
	if code == "conflict" {
		return ListResult{}, ErrConflict
	}
	return ListResult{}, ErrUnavailable
}
