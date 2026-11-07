package mailprocessing

import (
	"context"
	"github.com/enjoydarts/cycrio/internal/diagnostics"
)

type workFailure struct {
	cause    error
	panicked bool
}
type workPanicKey struct{}

// Each child owns its scope. Merge only after all children finish, so an
// earlier ordinary failure cannot hide a later panic from another attempt.
func (s *Service) executeWorkDiagnostic(ctx context.Context, l Lease, w Work) (error, workFailure) {
	child, scope := diagnostics.WithScope(ctx)
	panicFailure := &workFailure{}
	child = context.WithValue(child, workPanicKey{}, panicFailure)
	err := s.executeWork(child, l, w)
	if panicFailure.panicked {
		return err, *panicFailure
	}
	return err, workFailure{cause: scope.Error()}
}

func recordWorkPanic(ctx context.Context, value any, secrets ...string) {
	cause := diagnostics.Panic(value, secrets...)
	// Capture this panic's origin before any recording/cleanup operation.
	cause = diagnostics.Wrap(cause, cause)
	if failure, ok := ctx.Value(workPanicKey{}).(*workFailure); ok && !failure.panicked {
		failure.cause, failure.panicked = cause, true
	}
	diagnostics.RecordUnexpected(ctx, cause)
}

func recordProviderFailure(ctx context.Context, err error) {
	if err == nil {
		return
	}
	code, _ := classify(err)
	switch code {
	case "invalid_request", "request_cancelled", "refusal":
		diagnostics.Record(ctx, err)
	default:
		diagnostics.RecordUnexpected(ctx, err)
	}
}

func recordReaderFailure(ctx context.Context, err error) {
	if err == nil {
		return
	}
	switch SourceErrorCode(err) {
	case "authentication_failed", "credentials_unavailable", "source_missing", "input_too_large", "unsupported_content", "uid_validity_changed", "cancelled":
		diagnostics.Record(ctx, err)
	default:
		diagnostics.RecordUnexpected(ctx, err)
	}
}
