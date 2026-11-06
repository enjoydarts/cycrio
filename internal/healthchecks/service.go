package healthchecks

import (
	"context"
	"time"
)

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
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		return ErrUnavailable
	}
	enabled, err := s.repository.Due(ctx)
	if err != nil {
		return ErrUnavailable
	}
	if !enabled {
		return nil
	}
	if s.workerReady(ctx) != nil || ctx.Err() != nil {
		return ErrUnavailable
	}
	if s.apiReady(ctx) != nil || ctx.Err() != nil {
		return ErrUnavailable
	}
	id, err := s.repository.Claim(ctx)
	if err != nil {
		return ErrUnavailable
	}
	if id == "" {
		return nil
	}
	secret, err := s.resolve(ctx, id)
	if err != nil || ctx.Err() != nil {
		return ErrUnavailable
	}
	if s.sender.Ping(ctx, string(secret)) != nil {
		return ErrUnavailable
	}
	return nil
}
