package healthchecks

import (
	"context"
	"errors"
	"fmt"
	"net"
)

// Stage identifies a fixed heartbeat operation, never a URL or credential ID.
type Stage string

const (
	StageTick              Stage = "tick"
	StageDatabaseDue       Stage = "database_due"
	StageWorkerProbe       Stage = "worker_probe"
	StageAPIProbe          Stage = "api_probe"
	StageDatabaseClaim     Stage = "database_claim"
	StageCredentialResolve Stage = "credential_resolve"
	StageHealthchecksPost  Stage = "healthchecks_post"
)

// Failure preserves the public error while exposing only a sanitized cause.
// The original error is deliberately neither retained nor unwrapped.
type Failure struct{ cause *FailureCause }

func (e *Failure) Error() string { return ErrUnavailable.Error() }
func (e *Failure) Unwrap() error { return e.cause }

// FailureCause is safe for exception serialization. Its fields are constructed
// from fixed categories, HTTP status codes and validated SQLSTATE codes only.
type FailureCause struct {
	stage    Stage
	category string
	status   int
	sqlstate string
}

func (e *FailureCause) Error() string {
	message := fmt.Sprintf("heartbeat stage=%s category=%s", e.stage, e.category)
	if e.status != 0 {
		message += fmt.Sprintf(" status=%d", e.status)
	}
	if e.sqlstate != "" {
		message += " sqlstate=" + e.sqlstate
	}
	return message
}
func (e *FailureCause) Unwrap() error { return ErrUnavailable }

func failure(stage Stage, category string) *Failure {
	switch stage {
	case StageTick, StageDatabaseDue, StageWorkerProbe, StageAPIProbe, StageDatabaseClaim, StageCredentialResolve, StageHealthchecksPost:
	default:
		stage = StageTick
	}
	return &Failure{cause: &FailureCause{stage: stage, category: category}}
}

// NewFailure classifies errors without retaining their messages, SQL or URLs.
func NewFailure(stage Stage, err error) error {
	var existing *Failure
	if errors.As(err, &existing) {
		return existing
	}
	e := failure(stage, "unavailable")
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		e.cause.category = "timeout"
	case errors.Is(err, context.Canceled):
		e.cause.category = "cancelled"
	default:
		var network net.Error
		var database interface{ SQLState() string }
		if errors.As(err, &network) {
			e.cause.category = "network"
			if network.Timeout() {
				e.cause.category = "timeout"
			}
		} else if errors.As(err, &database) {
			e.cause.category = "database_error"
			code := database.SQLState()
			valid := len(code) == 5
			for _, r := range code {
				if (r < '0' || r > '9') && (r < 'A' || r > 'Z') {
					valid = false
				}
			}
			if valid {
				e.cause.sqlstate = code
			}
		}
	}
	return e
}

func transportFailure(stage Stage, err error) error {
	e := NewFailure(stage, err).(*Failure)
	if e.cause.category == "unavailable" {
		e.cause.category = "network"
	}
	return e
}
