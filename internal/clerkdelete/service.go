package clerkdelete

import "context"

type Service struct {
	repo    Repository
	deleter Deleter
}

func NewService(repo Repository, deleter Deleter) *Service {
	return &Service{repo: repo, deleter: deleter}
}

// Drain processes a bounded number of outbox leases. Network failures are
// durable retry results; repository failures expose only a fixed diagnostic.
func (s *Service) Drain(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	if limit > 100 {
		limit = 100
	}
	processed := 0
	for processed < limit {
		if ctx.Err() != nil {
			return processed, ctx.Err()
		}
		work, err := s.repo.Claim(ctx)
		if err != nil {
			return processed, ErrUnavailable
		}
		if work == nil {
			return processed, nil
		}
		result := s.deleter.Delete(ctx, work.Target, work.Secret)
		clear(work.Secret)
		work.Secret = nil
		if err = s.repo.Finish(ctx, *work, safeResult(result)); err != nil {
			return processed, ErrUnavailable
		}
		processed++
	}
	return processed, nil
}
