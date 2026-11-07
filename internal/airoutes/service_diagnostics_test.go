package airoutes

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestRefreshPreservesResolverFailureCause(t *testing.T) {
	cause := errors.New("credential decryption failure")
	repo := &serviceRepo{valid: true}
	s := NewService(repo, nil, func(context.Context, string) ([]byte, error) { return nil, cause })
	_, err := s.Refresh(context.Background(), uuid.NewString())
	if !errors.Is(err, cause) || !errors.Is(err, ErrUnavailable) || err.Error() != ErrUnavailable.Error() {
		t.Fatal("refresh failure cause lost or publicly exposed")
	}
}
