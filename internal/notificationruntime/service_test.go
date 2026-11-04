package notificationruntime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type serviceRepository struct {
	calls           int
	delivery, lease string
	sender          Sender
	result          Result
	err             error
}

func (r *serviceRepository) DeliverNotification(_ context.Context, delivery, lease string, sender Sender) (Result, error) {
	r.calls++
	r.delivery, r.lease, r.sender = delivery, lease, sender
	return r.result, r.err
}

type serviceSender struct{ calls int }

func (s *serviceSender) Send(context.Context, []byte, Delivery) SendResult {
	s.calls++
	return SendResult{State: Accepted, ProviderID: providerID}
}

func TestServiceDelegatesOnlyValidatedTaskAndInjectedSender(t *testing.T) {
	repo := &serviceRepository{result: Result{Status: TaskRecorded}}
	sender := &serviceSender{}
	s := NewService(repo, sender)
	result, e := s.Execute(context.Background(), Task{DeliveryID: deliveryID, LeaseID: leaseID})
	if e != nil || result.Status != TaskRecorded || repo.calls != 1 || repo.delivery != deliveryID || repo.lease != leaseID || repo.sender != sender || sender.calls != 0 {
		t.Fatal("runtime bypassed repository authorization", result, e, repo.calls, sender.calls)
	}
	if _, e = s.Execute(context.Background(), Task{DeliveryID: deliveryID}); e != ErrInvalidTask || repo.calls != 1 {
		t.Fatal("invalid task reached repository", e, repo.calls)
	}
}

func TestServiceTerminalAndObsoleteLeaseAreIgnoredWithoutSend(t *testing.T) {
	repo := &serviceRepository{result: Result{Status: TaskIgnored}}
	sender := &serviceSender{}
	s := NewService(repo, sender)
	for range 2 {
		result, e := s.Execute(context.Background(), Task{DeliveryID: deliveryID, LeaseID: leaseID})
		if e != nil || result.Status != TaskIgnored || sender.calls != 0 {
			t.Fatal("ignored delivery sent", result, e, sender.calls)
		}
	}
}

func TestServiceUsesFixedErrorsAndRejectsUnknownRepositoryStatuses(t *testing.T) {
	for _, err := range []error{errors.New("synthetic-key verified@example.com private upstream"), fmt.Errorf("synthetic-key: %w", ErrUnavailable), fmt.Errorf("private-recipient: %w", ErrInvalidTask)} {
		repo := &serviceRepository{result: Result{Status: ExecutionState("private-status")}, err: err}
		result, e := NewService(repo, &serviceSender{}).Execute(context.Background(), Task{DeliveryID: deliveryID, LeaseID: leaseID})
		want := ErrUnavailable
		if errors.Is(err, ErrInvalidTask) {
			want = ErrInvalidTask
		}
		if e != want || result != (Result{}) || strings.Contains(e.Error(), "synthetic-key") || strings.Contains(e.Error(), "verified@example.com") {
			t.Fatal("private repository details leaked", result, e)
		}
	}
	repo := &serviceRepository{result: Result{Status: ExecutionState("private-status")}}
	if result, e := NewService(repo, &serviceSender{}).Execute(context.Background(), Task{DeliveryID: deliveryID, LeaseID: leaseID}); e != ErrUnavailable || result != (Result{}) {
		t.Fatal(result, e)
	}
	for _, s := range []*Service{nil, NewService(nil, &serviceSender{}), NewService(repo, nil)} {
		if _, e := s.Execute(context.Background(), Task{DeliveryID: deliveryID, LeaseID: leaseID}); e != ErrUnavailable {
			t.Fatal("missing dependency accepted", e)
		}
	}
}
