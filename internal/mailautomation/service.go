package mailautomation

import (
	"context"
	"errors"
	"github.com/enjoydarts/cycrio/internal/mailsync"
)

type Service struct{ repository Repository }

func NewService(r Repository) *Service { return &Service{r} }
func safe(e error) error {
	if e == nil {
		return nil
	}
	for _, v := range []error{ErrInvalid, ErrConflict, ErrNotFound} {
		if errors.Is(e, v) {
			return v
		}
	}
	return ErrUnavailable
}
func valid(owner, account string) bool { return mailsync.ValidID(owner) && mailsync.ValidID(account) }
func (s *Service) Read(ctx context.Context, owner, account string) (State, error) {
	if !valid(owner, account) {
		return State{}, ErrInvalid
	}
	v, e := s.repository.Read(ctx, owner, account)
	return v, safe(e)
}
func (s *Service) Save(ctx context.Context, owner, account string, in Input) (State, error) {
	if !valid(owner, account) || !ValidInput(in) {
		return State{}, ErrInvalid
	}
	v, e := s.repository.Save(ctx, owner, account, in)
	return v, safe(e)
}
func (s *Service) Stop(ctx context.Context, owner, account string, in Control) (State, error) {
	if !valid(owner, account) || !ValidControl(in) {
		return State{}, ErrInvalid
	}
	v, e := s.repository.Stop(ctx, owner, account, in)
	return v, safe(e)
}
func (s *Service) Resume(ctx context.Context, owner, account string, in Control) (State, error) {
	if !valid(owner, account) || !ValidControl(in) {
		return State{}, ErrInvalid
	}
	v, e := s.repository.Resume(ctx, owner, account, in)
	return v, safe(e)
}
