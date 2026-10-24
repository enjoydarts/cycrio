package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/enjoydarts/cycrio/internal/infrastructure"
	"github.com/google/uuid"
	hatchet "github.com/hatchet-dev/hatchet/sdks/go"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	client, err := hatchet.NewClient(hatchet.WithClientLogLevel("warn"))
	if err != nil {
		return errors.New("Hatchet client configuration failed")
	}
	defer func() { _ = client.Close(context.Background()) }()
	id := uuid.NewString()
	result, err := awaitResult(ctx, func() (*hatchet.WorkflowResult, error) {
		return client.Run(ctx, infrastructure.TaskName, infrastructure.Input{CheckID: id})
	})
	if err != nil {
		return errors.New("infrastructure task failed or timed out")
	}
	var output infrastructure.Output
	if err := result.TaskOutput(infrastructure.TaskName).Into(&output); err != nil {
		return errors.New("infrastructure task returned invalid output")
	}
	if output.CheckID != id || output.Status != "ok" {
		return errors.New("infrastructure task result mismatch")
	}
	fmt.Println("infrastructure check: ok")
	return nil
}
