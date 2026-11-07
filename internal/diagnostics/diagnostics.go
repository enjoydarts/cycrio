// Package diagnostics carries private technical causes across public error
// boundaries. Only the telemetry sender may serialize their sanitized details.
package diagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime"
	"sync"
)

type wrapped struct {
	public, cause error
	pcs           []uintptr
}

func (e *wrapped) Error() string        { return e.public.Error() }
func (e *wrapped) Unwrap() error        { return e.cause }
func (e *wrapped) Is(target error) bool { return errors.Is(e.public, target) }
func (e *wrapped) As(target any) bool   { return errors.As(e.public, target) }
func (e *wrapped) Format(state fmt.State, verb rune) {
	if verb == 'q' {
		fmt.Fprintf(state, "%q", e.Error())
		return
	}
	_, _ = io.WriteString(state, e.Error())
}
func (e *wrapped) MarshalJSON() ([]byte, error) { return json.Marshal(e.public) }
func (e *wrapped) DiagnosticStack() []uintptr   { return append([]uintptr(nil), e.pcs...) }

// Wrap keeps Error, formatting and JSON public while preserving cause identity.
// Existing origin stacks survive subsequent service-layer wrappers.
func Wrap(public, cause error) error {
	if cause == nil {
		return public
	}
	if public == nil {
		public = errors.New("operation failed")
	}
	pcs := Stack(cause)
	if len(pcs) == 0 {
		pcs = make([]uintptr, 64)
		pcs = pcs[:runtime.Callers(2, pcs)]
	}
	return &wrapped{public: public, cause: cause, pcs: pcs}
}

// Stack returns a bounded copy of the recorded origin, never machine paths.
func Stack(err error) (pcs []uintptr) {
	defer func() {
		if recover() != nil {
			pcs = nil
		}
	}()
	for depth := 0; err != nil && depth < 16; depth++ {
		if traced, ok := err.(interface{ DiagnosticStack() []uintptr }); ok {
			frames := traced.DiagnosticStack()
			if len(frames) > 64 {
				frames = frames[:64]
			}
			return append([]uintptr(nil), frames...)
		}
		err = errors.Unwrap(err)
	}
	return nil
}

type scopeKey struct{}
type Scope struct {
	mu      sync.Mutex
	failure error
}

func WithScope(ctx context.Context) (context.Context, *Scope) {
	scope := &Scope{}
	return context.WithValue(ctx, scopeKey{}, scope), scope
}
func (s *Scope) Error() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failure
}

// Record preserves one original failure for a request/job, including pipelines
// that persist a fixed result and intentionally return nil. It never sends data.
func Record(ctx context.Context, err error) {
	if ctx == nil || err == nil {
		return
	}
	scope, _ := ctx.Value(scopeKey{}).(*Scope)
	if scope == nil {
		return
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if scope.failure != nil {
		return
	}
	scope.failure = Wrap(errors.New("operation failed"), err)
	// Wrap is called from this helper; point the origin at its caller instead.
	if len(Stack(err)) == 0 {
		w := scope.failure.(*wrapped)
		w.pcs = make([]uintptr, 64)
		w.pcs = w.pcs[:runtime.Callers(2, w.pcs)]
	}
}
