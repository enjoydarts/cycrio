//go:build integration && evaluation && hatchet_evaluation

package database

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/evaluation"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
	hatchet "github.com/hatchet-dev/hatchet/sdks/go"
	"github.com/jackc/pgx/v5/pgxpool"
)

var hatchetCrashWorkerConfig = flag.String("hatchet-crash-worker-config", "", "private synthetic subprocess worker configuration")

type interruptionWorkerConfig struct {
	Database string `json:"database"`
	Keyring  string `json:"keyring"`
	Probes   string `json:"probes"`
	Task     string `json:"task"`
	Ready    string `json:"ready"`
	InFlight string `json:"in_flight"`
}

type crashInference struct {
	client pocInference
	marker string
	t      *testing.T
}

func (c *crashInference) Do(ctx context.Context, q ai.Request) (ai.Response, error) {
	// The synthetic provider has received the request, but no response reaches
	// FinishCall. SIGKILL must leave the real database's running call uncertain.
	_, err := c.client.Do(ctx, q)
	if err != nil {
		return ai.Response{}, err
	}
	writeInterruptionMetadata(c.t, c.marker, struct {
		Calls int `json:"calls"`
	}{1})
	<-ctx.Done()
	return ai.Response{}, ctx.Err()
}

// This entrypoint runs only in the dedicated child process. It never constructs
// application fixtures or a production worker and must target a temporary schema.
func TestHatchetCrashWorkerProcess(t *testing.T) {
	if *hatchetCrashWorkerConfig == "" {
		t.Skip("dedicated synthetic subprocess only")
	}
	file, err := os.Open(*hatchetCrashWorkerConfig)
	if err != nil {
		t.Fatal("open synthetic worker config")
	}
	decoder := json.NewDecoder(io.LimitReader(file, 65537))
	decoder.DisallowUnknownFields()
	var config interruptionWorkerConfig
	decodeErr := decoder.Decode(&config)
	trailing := decoder.Decode(new(any))
	closeErr := file.Close()
	const prefix = "cycrio-interruption-"
	if decodeErr != nil || trailing != io.EOF || closeErr != nil || !strings.HasPrefix(config.Task, prefix) || uuid.Validate(strings.TrimPrefix(config.Task, prefix)) != nil {
		t.Fatal("invalid synthetic worker config")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, config.Database)
	if err != nil {
		t.Fatal("synthetic child database")
	}
	defer pool.Close()
	var schema string
	if pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema) != nil || !strings.HasPrefix(schema, "test_identity_") {
		t.Fatal("synthetic child refuses non-test schema")
	}
	keys, err := secretstore.Load(config.Keyring)
	if err != nil {
		t.Fatal("synthetic child keyring")
	}
	probes, err := evaluation.ReadProbes(config.Probes)
	if err != nil || len(probes) != 2 || probes[0].ID != "body" || probes[1].ID != "credential" {
		t.Fatal("synthetic child probes")
	}
	defer clear(probes[0].Value)
	defer clear(probes[1].Value)
	f := processingFixture{repo: NewMailProcessingRepository(pool), runtime: NewAIRuntimeRepository(pool), keys: keys}
	inference := &crashInference{client: pocInference{body: probes[0].Value, secret: probes[1].Value}, marker: config.InFlight, t: t}
	service := interruptionService(f, inference, probes)
	newInterruptionTransport(t, config.Task, service.Execute)
	writeInterruptionMetadata(t, config.Ready, struct {
		Ready bool `json:"ready"`
	}{true})
	<-ctx.Done()
	t.Fatal("synthetic crash worker was not stopped")
}

func waitInterruptionFile(t *testing.T, ctx context.Context, path string) {
	t.Helper()
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if info, err := os.Stat(path); err == nil && info.Size() > 0 {
			return
		} else if err != nil && !os.IsNotExist(err) {
			t.Fatal("read synthetic worker marker")
		}
		select {
		case <-ctx.Done():
			t.Fatal("synthetic worker synchronization cancelled")
		case <-deadline.C:
			t.Fatal("synthetic worker synchronization timed out")
		case <-ticker.C:
		}
	}
}

func runHatchetWorkerCrash(t *testing.T, ctx context.Context, f processingFixture, lease mailprocessing.Lease, directory string, probes []evaluation.Probe) []string {
	t.Helper()
	private := filepath.Join(directory, "worker-crash")
	if os.Mkdir(private, 0700) != nil {
		t.Fatal("create synthetic child directory")
	}
	name := "cycrio-interruption-" + uuid.NewString()
	config := interruptionWorkerConfig{Database: f.runtime.pool.Config().ConnString(), Keyring: f.keyringPath, Probes: filepath.Join(directory, "probes.json"), Task: name, Ready: filepath.Join(private, "ready.json"), InFlight: filepath.Join(private, "in-flight.json")}
	configPath := filepath.Join(private, "worker.json")
	writeInterruptionMetadata(t, configPath, config)
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHatchetCrashWorkerProcess$", "-test.timeout=80s", "-hatchet-crash-worker-config="+configPath)
	// The outer wrapper captures and scans both processes' output together.
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	if child.Start() != nil {
		t.Fatal("start synthetic child worker")
	}
	waited := false
	defer func() {
		if !waited {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	}()
	waitInterruptionFile(t, ctx, config.Ready)
	// This parent worker deliberately registers only after the first process dies.
	client, err := hatchet.NewClient(hatchet.WithClientLogLevel("warn"))
	if err != nil {
		t.Fatal("crash dispatch client")
	}
	defer client.Close(context.Background())
	ref, err := client.RunNoWait(ctx, name, lease)
	if err != nil {
		t.Fatal("dispatch synthetic child work")
	}
	waitInterruptionFile(t, ctx, config.InFlight)
	var running int
	if f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM ai_calls WHERE consuming_user_id=$1 AND scope='user' AND status='calling'`, f.owner).Scan(&running) != nil || running != 1 {
		t.Fatal("crash did not occur during running AI call")
	}
	if child.Process.Kill() != nil {
		t.Fatal("kill synthetic child worker")
	}
	waitErr := child.Wait()
	waited = true
	status, ok := child.ProcessState.Sys().(syscall.WaitStatus)
	if waitErr == nil || !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		t.Fatal("synthetic worker did not die from SIGKILL")
	}
	if leases, err := f.repo.Claim(ctx); err != nil || len(leases) != 0 {
		t.Fatal("running crash work automatically retried before expiry")
	}
	// Advance this temporary schema's lease only. No six-minute wall-clock claim.
	if _, err = f.runtime.pool.Exec(ctx, `UPDATE mail_processing_runs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, lease.RunID); err != nil {
		t.Fatal("advance isolated crash lease")
	}
	if leases, err := f.repo.Claim(ctx); err != nil || len(leases) != 0 {
		t.Fatal("expired running call was automatically retried")
	}
	inference := &hatchetSyntheticInference{client: pocInference{body: probes[0].Value, secret: probes[1].Value}}
	service := interruptionService(f, inference, probes)
	replacement := newInterruptionTransport(t, name, service.Execute)
	duplicate, err := replacement.RunNoWait(ctx, lease)
	if err != nil {
		t.Fatal("redeliver crash lease to replacement worker")
	}
	awaitInterruption(t, ctx, duplicate, name)
	// Wait for the killed transport's bounded engine timeout too; dropping a result
	// subscription alone would leave an active Hatchet run behind the test.
	terminalCtx, stop := context.WithTimeout(ctx, 20*time.Second)
	defer stop()
	_, terminalErr := ref.ResultWithContext(terminalCtx)
	if terminalCtx.Err() != nil || terminalErr == nil {
		t.Fatal("killed task did not reach a failed terminal outcome")
	}
	if inference.Calls() != 0 {
		t.Fatal("replacement worker repeated uncertain AI call")
	}
	return []string{ref.RunId, duplicate.RunId}
}
