package infrastructure

import (
	"context"
	"errors"
	"strings"
	"time"
)

const TaskName = "cycrio-infrastructure-check"

type Input struct {
	CheckID string `json:"check_id"`
}

type Output struct {
	CheckID string `json:"check_id"`
	Status  string `json:"status"`
}

// Check validates infrastructure without placing application data into the job.
func Check(ctx context.Context, input Input, ping func(context.Context) error) (Output, error) {
	if strings.TrimSpace(input.CheckID) == "" {
		return Output{}, errors.New("check_id is required")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ping(ctx); err != nil {
		return Output{}, errors.New("database unavailable")
	}
	return Output{CheckID: input.CheckID, Status: "ok"}, nil
}
