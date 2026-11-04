package clerkdelete

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type fakeRepository struct {
	works               []*Work
	finished            []Result
	finishWork          []Work
	claimErr, finishErr error
}

func (r *fakeRepository) Claim(context.Context) (*Work, error) {
	if r.claimErr != nil {
		return nil, r.claimErr
	}
	if len(r.works) == 0 {
		return nil, nil
	}
	w := r.works[0]
	r.works = r.works[1:]
	return w, nil
}
func (r *fakeRepository) Finish(_ context.Context, w Work, result Result) error {
	r.finished = append(r.finished, result)
	r.finishWork = append(r.finishWork, w)
	return r.finishErr
}

type fakeDeleter func(context.Context, Target, []byte) Result

func (fn fakeDeleter) Delete(ctx context.Context, t Target, secret []byte) Result {
	return fn(ctx, t, secret)
}

func TestServiceDrainsBoundedWorkAndClearsSecrets(t *testing.T) {
	secret := []byte("sk_test_synthetic")
	r := &fakeRepository{works: []*Work{{ID: "one", LeaseID: "lease", Target: target, Secret: secret}, {ID: "two", Target: target}}}
	s := NewService(r, fakeDeleter(func(_ context.Context, target Target, key []byte) Result {
		if string(key) != "sk_test_synthetic" {
			t.Fatal("missing secret")
		}
		return Result{Deleted: true}
	}))
	n, err := s.Drain(context.Background(), 1)
	if err != nil || n != 1 || len(r.works) != 1 || len(r.finished) != 1 || !r.finished[0].Deleted || string(secret) != strings.Repeat("\x00", len(secret)) {
		t.Fatal(n, err, r.finished, secret)
	}
	if r.finishWork[0].Secret != nil {
		t.Fatal("repository finish received secret")
	}
	encoded, err := json.Marshal(Work{ID: "private-id", LeaseID: "private-lease", Target: target, Secret: []byte("private-key")})
	if err != nil || string(encoded) != "{}" {
		t.Fatal("work leaked into serialized payload", string(encoded), err)
	}
}

func TestServicePersistsRetryAndPropagatesOnlySafeErrors(t *testing.T) {
	r := &fakeRepository{works: []*Work{{ID: "one", Target: target}}}
	s := NewService(r, fakeDeleter(func(context.Context, Target, []byte) Result { return Result{Code: "rate_limited"} }))
	if n, err := s.Drain(context.Background(), 20); err != nil || n != 1 || len(r.finished) != 1 || r.finished[0].Code != "rate_limited" {
		t.Fatal(n, err, r.finished)
	}
	for _, claimFailure := range []bool{true, false} {
		r = &fakeRepository{works: []*Work{{ID: "one", Secret: []byte("private")}}}
		if claimFailure {
			r.claimErr = errors.New("private DB data")
		} else {
			r.finishErr = errors.New("private DB data")
		}
		s = NewService(r, fakeDeleter(func(context.Context, Target, []byte) Result { return Result{Deleted: true} }))
		if _, err := s.Drain(context.Background(), 1); !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "private") {
			t.Fatal(err)
		}
	}
}

func TestServiceCancellationAndEmptyLimitDoNotStartWork(t *testing.T) {
	for _, limit := range []int{0, -1, 1} {
		r := &fakeRepository{works: []*Work{{ID: "one"}}}
		ctx, cancel := context.WithCancel(context.Background())
		if limit == 1 {
			cancel()
		}
		defer cancel()
		s := NewService(r, fakeDeleter(func(context.Context, Target, []byte) Result {
			t.Fatal("work started after cancellation or empty limit")
			return Result{}
		}))
		_, _ = s.Drain(ctx, limit)
		if len(r.works) != 1 {
			t.Fatal("claimed work after cancellation or empty limit")
		}
	}
}
