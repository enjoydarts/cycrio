package airoutes

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type serviceRepo struct {
	mu      sync.Mutex
	lease   Lease
	applied int
	valid   bool
	state   RefreshState
	routes  []Route
	joined  chan struct{}
}

func (r *serviceRepo) List(context.Context, Filter) (ListResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return ListResult{Routes: r.routes, Refresh: r.state}, nil
}
func (r *serviceRepo) Claim(context.Context, string) (Lease, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.lease.ID != "" {
		l := r.lease
		l.Owner = false
		if r.joined != nil {
			r.joined <- struct{}{}
		}
		return l, nil
	}
	r.lease = Lease{ID: uuid.NewString(), CredentialID: uuid.NewString(), CredentialVersion: 1, Owner: true}
	r.state.Status = "running"
	return r.lease, nil
}
func (r *serviceRepo) Validate(_ context.Context, _ string, lease Lease) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.valid {
		return ErrConflict
	}
	if lease.ID != "" && lease.ID != r.lease.ID {
		return ErrConflict
	}
	return nil
}

type completingRepository struct {
	*serviceRepo
	once sync.Once
}

func (r *completingRepository) List(ctx context.Context, f Filter) (ListResult, error) {
	out, e := r.serviceRepo.List(ctx, f)
	r.once.Do(func() { r.mu.Lock(); defer r.mu.Unlock(); r.state.Status = "succeeded"; r.lease = Lease{} })
	return out, e
}
func TestRefreshWaiterAcceptsCompletionBetweenStateReads(t *testing.T) {
	repo := &completingRepository{serviceRepo: &serviceRepo{valid: true, lease: Lease{ID: uuid.NewString(), CredentialID: uuid.NewString(), CredentialVersion: 1}, state: RefreshState{Status: "running"}}}
	service := NewService(repo, nil, nil)
	out, e := service.Refresh(context.Background(), uuid.NewString())
	if e != nil || out.Refresh.Status != "succeeded" {
		t.Fatal(out, e)
	}
}
func (r *serviceRepo) Apply(ctx context.Context, _ string, _ Lease, rows []Route) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.valid {
		return ErrConflict
	}
	if ctx.Err() != nil {
		return ErrUnavailable
	}
	r.applied++
	r.routes = rows
	r.state.Status = "succeeded"
	r.lease = Lease{}
	return nil
}
func (r *serviceRepo) Finish(_ context.Context, _ Lease, code string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state.Status = "failed"
	r.state.ErrorCode = &code
	r.lease = Lease{}
	return nil
}
func TestRefreshCoalescesAndNeverPublishesRevokedFetch(t *testing.T) {
	for _, revoked := range []bool{false, true} {
		t.Run(map[bool]string{false: "coalesced", true: "revoked"}[revoked], func(t *testing.T) {
			r := &serviceRepo{valid: true, joined: make(chan struct{}, 1)}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			started, release := make(chan struct{}), make(chan struct{})
			calls := 0
			key := []byte("synthetic-secret")
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				close(started)
				<-release
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":[` + endpointJSON(t, nil) + `]}`))}, nil
			})}
			s := NewService(r, NewFetcher(client), func(context.Context, string) ([]byte, error) { return append([]byte(nil), key...), nil })
			done := make(chan error, 2)
			go func() { _, e := s.Refresh(ctx, uuid.NewString()); done <- e }()
			<-started
			go func() { _, e := s.Refresh(ctx, uuid.NewString()); done <- e }()
			select {
			case <-r.joined:
			case <-ctx.Done():
				t.Fatal("second refresh never joined")
			}
			if revoked {
				r.mu.Lock()
				r.valid = false
				r.mu.Unlock()
			}
			close(release)
			for range 2 {
				e := <-done
				if revoked && !errors.Is(e, ErrConflict) && !errors.Is(e, ErrUnavailable) {
					t.Fatal(e)
				}
				if !revoked && e != nil {
					t.Fatal(e)
				}
			}
			if calls != 1 || r.applied != map[bool]int{false: 1, true: 0}[revoked] {
				t.Fatal(calls, r.applied)
			}
		})
	}
}
