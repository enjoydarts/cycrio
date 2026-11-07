package mailprocessing

import (
	"context"
	"errors"
	"github.com/enjoydarts/cycrio/internal/diagnostics"
	"sync"
)

// Reserve capacity before touching queued work, so competing runs cannot hold
// running attempts while waiting for local execution slots.
func (s *Service) reserveBatch(ctx context.Context) (int, error) {
	if ctx.Err() != nil {
		return 0, ErrUnavailable
	}
	select {
	case s.batchSlots <- struct{}{}:
	case <-ctx.Done():
		return 0, ErrUnavailable
	}
	n := 1
	for n < cap(s.batchSlots) {
		select {
		case s.batchSlots <- struct{}{}:
			n++
		default:
			return n, nil
		}
	}
	return n, nil
}

func (s *Service) executeBatch(ctx context.Context, l Lease, repo BatchRepository) error {
	n, err := s.reserveBatch(ctx)
	if err != nil {
		return err
	}
	defer func() {
		for range n {
			<-s.batchSlots
		}
	}()
	if ctx.Err() != nil {
		return ErrUnavailable
	}
	work, err := repo.StartBatch(ctx, l, n)
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrConflict) {
		return nil
	}
	if err != nil {
		return safe(err)
	}
	if len(work) > n {
		return ErrUnavailable
	}
	seen := make(map[string]bool, len(work))
	owner := ""
	for _, w := range work {
		if !ValidID(w.AttemptID) || w.Run.ID != l.RunID || seen[w.AttemptID] || owner != "" && w.OwnerID != owner {
			return ErrUnavailable
		}
		owner = w.OwnerID
		seen[w.AttemptID] = true
	}
	var wg sync.WaitGroup
	errorsFound := make(chan error, len(work))
	diagnosticsFound := make(chan workFailure, len(work))
	for _, w := range work {
		wg.Go(func() {
			child := l
			child.AttemptID = w.AttemptID
			err, failure := s.executeWorkDiagnostic(ctx, child, w)
			if failure.cause != nil {
				diagnosticsFound <- failure
			}
			if err != nil {
				errorsFound <- err
			}
		})
	}
	// A Hatchet slot remains busy until every child's usage and result have been
	// recorded and all transient buffers have been cleared.
	wg.Wait()
	close(diagnosticsFound)
	var selected workFailure
	for failure := range diagnosticsFound {
		selected = selectBatchFailure(selected, failure)
	}
	if selected.panicked {
		diagnostics.RecordUnexpected(ctx, selected.cause)
	} else {
		diagnostics.Record(ctx, selected.cause)
	}
	close(errorsFound)
	for err := range errorsFound {
		return err
	}
	return nil
}

func selectBatchFailure(selected, failure workFailure) workFailure {
	if selected.cause == nil || failure.panicked && !selected.panicked || !selected.panicked && diagnostics.IsUnexpected(failure.cause) && !diagnostics.IsUnexpected(selected.cause) {
		return failure
	}
	return selected
}
