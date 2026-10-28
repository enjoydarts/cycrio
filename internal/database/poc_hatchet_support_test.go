//go:build integration && evaluation && hatchet_evaluation

package database

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/evaluation"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/google/uuid"
	hatchet "github.com/hatchet-dev/hatchet/sdks/go"
)

func hatchetEvaluationProbes(t *testing.T, directory string) []evaluation.Probe {
	t.Helper()
	path := filepath.Join(directory, "probes.json")
	_, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		probes := []evaluation.Probe{{ID: "body", Value: []byte("CYCRIO_HATCHET_BODY_" + uuid.NewString())}, {ID: "credential", Value: []byte("CYCRIO_HATCHET_KEY_" + uuid.NewString())}}
		if evaluation.WriteProbes(path, probes) != nil {
			t.Fatal("create synthetic probes")
		}
	} else if err != nil {
		t.Fatal("read synthetic probe metadata")
	}
	probes, err := evaluation.ReadProbes(path)
	if err != nil || len(probes) != 2 || probes[0].ID != "body" || probes[1].ID != "credential" {
		t.Fatal("invalid shared synthetic probes")
	}
	return probes
}

func newInterruptionTransport(t *testing.T, name string, execute func(context.Context, mailprocessing.Lease) error) *hatchet.StandaloneTask {
	t.Helper()
	client, err := hatchet.NewClient(hatchet.WithClientLogLevel("warn"))
	if err != nil {
		t.Fatal("interruption client unavailable")
	}
	t.Cleanup(func() {
		ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if client.Close(ctx) != nil {
			t.Error("close interruption client")
		}
	})
	task := client.NewStandaloneTask(name, func(ctx hatchet.Context, lease mailprocessing.Lease) (hatchetEvaluationOutput, error) {
		if ctx.RetryCount() != 0 {
			return hatchetEvaluationOutput{}, errors.New("interruption automatic retry rejected")
		}
		if execute(ctx, lease) != nil {
			return hatchetEvaluationOutput{}, errors.New("interruption processing unavailable")
		}
		return hatchetEvaluationOutput{Status: "handled"}, nil
	}, hatchet.WithRetries(0), hatchet.WithExecutionTimeout(10*time.Second), hatchet.WithScheduleTimeout(30*time.Second))
	worker, err := client.NewWorker(name+"-"+uuid.NewString(), hatchet.WithWorkflows(task), hatchet.WithSlots(1))
	if err != nil {
		t.Fatal("register interruption worker")
	}
	stop, err := worker.Start()
	if err != nil {
		t.Fatal("start interruption worker")
	}
	t.Cleanup(func() {
		if stop() != nil {
			t.Error("stop interruption worker")
		}
	})
	return task
}

func awaitInterruption(t *testing.T, ctx context.Context, ref *hatchet.WorkflowRunRef, name string) {
	t.Helper()
	result, err := ref.ResultWithContext(ctx)
	if err != nil {
		t.Fatal("interruption task failed or timed out")
	}
	var output hatchetEvaluationOutput
	if result.TaskOutput(name).Into(&output) != nil || output.Status != "handled" {
		t.Fatal("invalid interruption task output")
	}
}

func writeInterruptionMetadata(t *testing.T, path string, value any) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal("create interruption artifact")
	}
	writeErr := json.NewEncoder(file).Encode(value)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatal("write interruption artifact")
	}
}
