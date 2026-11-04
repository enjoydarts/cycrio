// Package clerkdelete removes Clerk identities after local withdrawal without
// exposing their subjects or provider credentials in public/job payloads.
package clerkdelete

import (
	"context"
	"errors"
	"time"
)

var ErrUnavailable = errors.New("Clerk deletion unavailable")

type Target struct {
	Issuer     string
	Subject    string
	InstanceID string
}
type Work struct {
	ID      string `json:"-"`
	LeaseID string `json:"-"`
	Target  Target `json:"-"`
	Secret  []byte `json:"-"`
}
type Result struct {
	Deleted    bool
	Code       string
	RetryAfter time.Duration
}
type Repository interface {
	Claim(context.Context) (*Work, error)
	Finish(context.Context, Work, Result) error
}
type Deleter interface {
	Delete(context.Context, Target, []byte) Result
}
