// Package erasure defines the authenticated local account withdrawal contract.
package erasure

import (
	"errors"
	"github.com/google/uuid"
)

var (
	ErrInvalid     = errors.New("invalid erasure request")
	ErrForbidden   = errors.New("erasure forbidden")
	ErrConflict    = errors.New("erasure request conflict")
	ErrNotFound    = errors.New("erasure request not found")
	ErrUnavailable = errors.New("erasure unavailable")
)

type Input struct {
	RequestID    string `json:"request_id"`
	Confirmed    bool   `json:"confirmed"`
	Confirmation string `json:"confirmation"`
}
type Receipt struct {
	RequestID string `json:"request_id"`
	Status    string `json:"status"`
}

func ValidID(s string) bool {
	id, e := uuid.Parse(s)
	return e == nil && id != uuid.Nil && id.String() == s
}
func Normalize(in Input) (Input, error) {
	if !ValidID(in.RequestID) || !in.Confirmed || in.Confirmation != "退会する" {
		return Input{}, ErrInvalid
	}
	return in, nil
}
