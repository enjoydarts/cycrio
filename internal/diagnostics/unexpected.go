package diagnostics

import (
	"context"
	"errors"
	"runtime"
)

type unexpectedCause struct{ cause error }

func (e *unexpectedCause) Error() string { return e.cause.Error() }
func (e *unexpectedCause) Unwrap() error { return e.cause }

// IsUnexpected distinguishes captured infrastructure/provider faults from
// ordinary product rejections even when persisted outcome lookup is unavailable.
func IsUnexpected(err error) (unexpected bool) {
	defer func() {
		if recover() != nil {
			unexpected = false
		}
	}()
	pending := []error{err}
	for visited := 0; len(pending) > 0 && visited < 16; visited++ {
		current := pending[0]
		pending = pending[1:]
		if current == nil {
			continue
		}
		if _, ok := current.(*unexpectedCause); ok {
			return true
		}
		switch value := current.(type) {
		case interface{ Unwrap() []error }:
			for _, cause := range value.Unwrap() {
				if len(pending) >= 16 {
					break
				}
				pending = append(pending, cause)
			}
		case interface{ Unwrap() error }:
			pending = append(pending, value.Unwrap())
		}
	}
	return false
}

// RecordUnexpected promotes a genuine fault over an earlier expected rejection.
// Between genuine faults the first original cause and origin remain authoritative.
func RecordUnexpected(ctx context.Context, err error) {
	if ctx == nil || err == nil {
		return
	}
	scope, _ := ctx.Value(scopeKey{}).(*Scope)
	if scope == nil {
		return
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if IsUnexpected(scope.failure) {
		return
	}
	marked := &unexpectedCause{cause: err}
	w := Wrap(errors.New("operation failed"), marked).(*wrapped)
	if len(Stack(err)) == 0 {
		w.pcs = make([]uintptr, 64)
		w.pcs = w.pcs[:runtime.Callers(2, w.pcs)]
	}
	scope.failure = w
}
