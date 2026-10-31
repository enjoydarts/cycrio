package mailprocessing

import (
	"context"
	"errors"
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
	for _, w := range work {
		if !ValidID(w.AttemptID) || w.Run.ID != l.RunID || seen[w.AttemptID] {
			return ErrUnavailable
		}
		seen[w.AttemptID] = true
	}
	var wg sync.WaitGroup
	errorsFound := make(chan error, len(work))
	for _, w := range work {
		wg.Go(func() {
			child := l
			child.AttemptID = w.AttemptID
			if err := s.executeWork(ctx, child, w); err != nil {
				errorsFound <- err
			}
		})
	}
	// A Hatchet slot remains busy until every child's usage and result have been
	// recorded and all transient buffers have been cleared.
	wg.Wait()
	close(errorsFound)
	for err := range errorsFound {
		return err
	}
	return nil
}
