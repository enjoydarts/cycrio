package healthchecks

import (
	"context"
	"time"
)

// TickTimeout includes readiness, credential lookup and the bounded HTTP ping.
const TickTimeout = 15 * time.Second

type Repository interface {
	Due(context.Context) (bool, error)
	Claim(context.Context) (string, error)
}
type Sender interface {
	Ping(context.Context, string) error
}

type Service struct {
	repository            Repository
	resolve               func(context.Context, string) ([]byte, error)
	sender                Sender
	workerReady, apiReady func(context.Context) error
}

func NewService(repository Repository, resolve func(context.Context, string) ([]byte, error), sender Sender, workerReady, apiReady func(context.Context) error) *Service {
	return &Service{repository, resolve, sender, workerReady, apiReady}
}

func (s *Service) Tick(parent context.Context) error {
	ctx, cancel := context.WithTimeout(parent, TickTimeout)
	defer cancel()
	if ctx.Err() != nil {
		return NewFailure(StageTick, ctx.Err())
	}
	enabled, err := s.repository.Due(ctx)
	if err != nil {
		return NewFailure(StageDatabaseDue, err)
	}
	if !enabled {
		return nil
	}
	if err := s.workerReady(ctx); err != nil || ctx.Err() != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return NewFailure(StageWorkerProbe, err)
	}
	if err := s.apiReady(ctx); err != nil || ctx.Err() != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return NewFailure(StageAPIProbe, err)
	}
	id, err := s.repository.Claim(ctx)
	if err != nil {
		return NewFailure(StageDatabaseClaim, err)
	}
	if id == "" {
		return nil
	}
	secret, err := s.resolve(ctx, id)
	if err != nil || ctx.Err() != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return NewFailure(StageCredentialResolve, err)
	}
	if err := s.sender.Ping(ctx, string(secret)); err != nil {
		return NewFailure(StageHealthchecksPost, err)
	}
	return nil
}
