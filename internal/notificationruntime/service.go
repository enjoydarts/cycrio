package notificationruntime

import (
	"context"
	"errors"
)

var (
	ErrInvalidTask = errors.New("invalid notification task")
	ErrUnavailable = errors.New("notification execution unavailable")
)

type ExecutionState string

const (
	TaskIgnored  ExecutionState = "ignored"
	TaskRecorded ExecutionState = "recorded"
)

type Result struct {
	Status ExecutionState `json:"status"`
}

// Repository owns lease validation, current facts and preference checks,
// verified recipient/key resolution, durable unknown and the final authorized
// send transaction. Terminal or obsolete leases return TaskIgnored, nil.
type Repository interface {
	DeliverNotification(context.Context, string, string, Sender) (Result, error)
}

type Service struct {
	repository Repository
	sender     Sender
}

func NewService(repository Repository, sender Sender) *Service {
	return &Service{repository: repository, sender: sender}
}

func (service *Service) Execute(ctx context.Context, task Task) (Result, error) {
	task, err := NormalizeTask(task)
	if err != nil {
		return Result{}, err
	}
	if service == nil || service.repository == nil || service.sender == nil {
		return Result{}, ErrUnavailable
	}
	result, err := service.repository.DeliverNotification(ctx, task.DeliveryID, task.LeaseID, service.sender)
	if err != nil {
		if errors.Is(err, ErrInvalidTask) {
			return Result{}, ErrInvalidTask
		}
		return Result{}, ErrUnavailable
	}
	if result.Status != TaskIgnored && result.Status != TaskRecorded {
		return Result{}, ErrUnavailable
	}
	return result, nil
}
