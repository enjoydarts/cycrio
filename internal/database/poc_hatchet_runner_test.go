//go:build integration && evaluation && hatchet_evaluation

package database

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/evaluation"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/google/uuid"
	hatchet "github.com/hatchet-dev/hatchet/sdks/go"
)

type hatchetSyntheticInference struct {
	mu     sync.Mutex
	client pocInference
}

func (c *hatchetSyntheticInference) Do(ctx context.Context, q ai.Request) (ai.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.client.Do(ctx, q)
}
func (c *hatchetSyntheticInference) Calls() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.client.calls
}

type hatchetEvaluationOutput struct {
	Status string `json:"status"`
}
type hatchetEvaluationReport struct {
	Version         int                `json:"version"`
	Mode            string             `json:"mode"`
	Scope           string             `json:"scope"`
	StartedAt       time.Time          `json:"started_at"`
	FinishedAt      time.Time          `json:"finished_at"`
	Messages        int                `json:"messages"`
	Calls           int64              `json:"synthetic_ai_calls"`
	ExternalAICalls int                `json:"external_ai_calls"`
	TransportRuns   int                `json:"hatchet_runs"`
	Outcomes        map[string]int     `json:"outcomes"`
	Checks          []evaluation.Check `json:"checks"`
	NotChecked      []string           `json:"not_checked"`
}

func runHatchetEvaluation(t *testing.T, directory, historyDSN string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	started := time.Now().UTC()
	f := processingSetup(t)
	f.add(t, 10)
	probes := hatchetEvaluationProbes(t, directory)
	body, secret := probes[0].Value, probes[1].Value
	defer clear(body)
	defer clear(secret)
	inference := &hatchetSyntheticInference{client: pocInference{body: body, secret: secret}}
	service := mailprocessing.NewService(f.repo, f.keys, pocReader{body: body, secret: secret}, inference, func(context.Context, string) ([]byte, error) { return bytes.Clone(secret), nil })
	client, err := hatchet.NewClient(hatchet.WithClientLogLevel("warn"))
	if err != nil {
		t.Fatal("Hatchet client unavailable")
	}
	defer func() {
		if client.Close(context.Background()) != nil {
			t.Error("Hatchet client close failed")
		}
	}()
	name := "cycrio-synthetic-evaluation-" + uuid.NewString()
	var invocations atomic.Int64
	task := client.NewStandaloneTask(name, func(ctx hatchet.Context, input mailprocessing.Lease) (hatchetEvaluationOutput, error) {
		invocations.Add(1)
		if ctx.RetryCount() != 0 {
			return hatchetEvaluationOutput{}, errors.New("synthetic task retry rejected")
		}
		if err := service.Execute(ctx, input); err != nil {
			return hatchetEvaluationOutput{}, errors.New("synthetic mail processing unavailable")
		}
		return hatchetEvaluationOutput{Status: "handled"}, nil
	}, hatchet.WithRetries(0), hatchet.WithExecutionTimeout(time.Minute), hatchet.WithScheduleTimeout(30*time.Second))
	worker, err := client.NewWorker(name, hatchet.WithWorkflows(task), hatchet.WithSlots(1))
	if err != nil {
		t.Fatal("register synthetic worker")
	}
	stop, err := worker.Start()
	if err != nil {
		t.Fatal("start synthetic worker")
	}
	stopped := false
	defer func() {
		if !stopped {
			if stop() != nil {
				t.Error("stop synthetic worker")
			}
		}
	}()
	runIDs := []string{}
	submit := func(lease mailprocessing.Lease) {
		t.Helper()
		result, err := task.Run(ctx, lease)
		if err != nil {
			t.Fatal("synthetic task failed or timed out")
		}
		var output hatchetEvaluationOutput
		if result.Into(&output) != nil || output.Status != "handled" || uuid.Validate(result.RunId) != nil {
			t.Fatal("invalid synthetic task output")
		}
		runIDs = append(runIDs, result.RunId)
	}
	completed := 0
	var last mailprocessing.Lease
	for completed < 10 {
		before := completed
		in := f.in
		in.RequestID = uuid.NewString()
		in.MaxMessages = 10 - completed
		run, err := service.Request(ctx, f.owner, f.account, in)
		if err != nil || run.TotalCount != in.MaxMessages {
			t.Fatal("request synthetic mail")
		}
		for range run.TotalCount {
			leases, err := service.Claim(ctx)
			if err != nil || len(leases) > 1 {
				t.Fatal("claim synthetic mail")
			}
			if len(leases) == 0 {
				break
			}
			last = leases[0]
			submit(last)
			completed++
		}
		if completed == before {
			t.Fatal("synthetic Hatchet evaluation stalled")
		}
	}
	submit(last)
	if inference.Calls() != 16 || invocations.Load() != 11 {
		t.Fatal("synthetic calls repeated or missing", inference.Calls(), invocations.Load())
	}
	results, err := service.Results(ctx, f.owner, f.account)
	if err != nil || len(results) != 10 {
		t.Fatal("synthetic results incomplete")
	}
	outcomes := map[string]int{}
	for _, result := range results {
		outcomes[result.State]++
	}
	if outcomes["succeeded"] != 6 || outcomes["not_related"] != 1 || outcomes["failed"] != 2 || outcomes["unknown"] != 1 {
		t.Fatal("unexpected synthetic Hatchet outcomes")
	}
	var calls, unknown int
	if f.runtime.pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE cost_kind='unknown') FROM ai_calls WHERE consuming_user_id=$1 AND scope='user'`, f.owner).Scan(&calls, &unknown) != nil || calls != 16 || unknown != 1 {
		t.Fatal("synthetic cost ledger mismatch")
	}
	raw, err := json.Marshal(results)
	if err != nil {
		t.Fatal("serialize synthetic results")
	}
	if hits, err := evaluation.Scan(bytes.NewReader(raw), probes); err != nil || len(hits) != 0 {
		t.Fatal("private probe in public result")
	}
	appCheck, hits, err := scanPoCDatabase(ctx, f.runtime.pool, probes)
	if err != nil || !appCheck.Passed || len(hits) != 0 {
		t.Fatal("private probe in application DB")
	}
	if stop() != nil {
		t.Fatal("stop synthetic worker")
	}
	stopped = true
	historyCheck := checkHatchetHistory(t, ctx, historyDSN, probes, runIDs)
	report := hatchetEvaluationReport{Version: 1, Mode: "synthetic", Scope: "local_hatchet_mail_processing", StartedAt: started, FinishedAt: time.Now().UTC(), Messages: 10, Calls: inference.Calls(), TransportRuns: len(runIDs), Outcomes: outcomes, Checks: []evaluation.Check{appCheck, historyCheck, {ID: "public_results", Scope: "results_api_json", Scanned: 10, Passed: true}}, NotChecked: []string{"docker_logs_until_collector_passes", "production_worker_dispatch_loop", "worker_process_crash", "disconnect_and_delete_via_hatchet", "hatchet_cloud_or_external_payload_storage", "real_google_login", "imap_network", "external_ai_accuracy", "external_ai_cost", "real_user_ui"}}
	file, err := os.OpenFile(filepath.Join(directory, "hatchet-report.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal("create Hatchet report")
	}
	writeErr := json.NewEncoder(file).Encode(report)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatal("write Hatchet report")
	}
	t.Log("synthetic Hatchet evaluation: 10 messages, 11 transport runs, 16 adapter calls; DB history and privacy checks passed")
}
