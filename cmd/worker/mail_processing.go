package main

import (
	"context"
	"errors"
	"time"

	"github.com/enjoydarts/cycrio/internal/mailprocessing"
)

const mailProcessingTaskName = "cycrio-mail-process"

type MailProcessingOutput struct {
	Status string `json:"status"`
}

func executeMailProcessing(ctx context.Context, input mailprocessing.Lease, execute func(context.Context, mailprocessing.Lease) error) (MailProcessingOutput, error) {
	if err := execute(ctx, input); err != nil {
		return MailProcessingOutput{Status: "failed"}, errors.New("mail processing unavailable")
	}
	return MailProcessingOutput{Status: "handled"}, nil
}

func dispatchMailProcessing(ctx context.Context, claim func(context.Context) ([]mailprocessing.Lease, error), submit func(context.Context, mailprocessing.Lease) error, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		claimCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		leases, err := claim(claimCtx)
		cancel()
		if err == nil {
			for _, lease := range leases {
				if ctx.Err() != nil {
					return
				}
				sendCtx, stop := context.WithTimeout(ctx, 15*time.Second)
				_ = submit(sendCtx, lease)
				stop()
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
