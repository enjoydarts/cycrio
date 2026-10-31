// prompteval is opt-in, synthetic-only, sequential and never retries paid calls.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/promptquality"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("prompteval", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	execute := flags.Bool("execute", false, "opt in to at most 128 paid external calls")
	actor := flags.String("actor-id", "", "administrator UUID (required for execution)")
	keyringPath := flags.String("keyring-file", ".local/secrets/keyring.json", "existing encryption keyring; never generated")
	reportPath := flags.String("report", "", "new report under .local; never overwritten")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return errors.New("invalid_arguments")
	}
	if !*execute {
		fmt.Fprintln(out, "dry-run: synthetic corpus, 24 development + 8 holdout, at most 128 paid calls; requires -execute -actor-id UUID; no database or external access")
		return nil
	}
	if _, err := uuid.Parse(*actor); err != nil {
		return errors.New("execute_requires_actor_uuid")
	}
	base, err := filepath.Abs(".local")
	if err != nil {
		return errors.New("report_unavailable")
	}
	if err = os.MkdirAll(base, 0700); err != nil {
		return errors.New("report_unavailable")
	}
	if *reportPath == "" {
		*reportPath = filepath.Join(base, "prompt-quality-"+time.Now().UTC().Format("20060102T150405Z")+"-"+uuid.NewString()+".json")
	}
	file, err := openReport(base, *reportPath)
	if err != nil {
		return errors.New("report_unavailable")
	}
	defer file.Close()
	report := promptquality.NewReport()
	report.Partial = true
	report.StopReason = "initialization_failed"
	// Reserve the private exclusive file before any paid call and retain partial
	// results on every normal failure, including cancellation.
	save := func() error {
		if err := json.NewEncoder(file).Encode(report); err != nil {
			return errors.New("report_write_failed")
		}
		if err := file.Sync(); err != nil {
			return errors.New("report_write_failed")
		}
		return nil
	}
	keyring, err := secretstore.Load(*keyringPath)
	if err != nil {
		_ = save()
		return errors.New("keyring_unavailable")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if os.Getenv("DATABASE_URL") == "" {
		_ = save()
		return errors.New("database_configuration_unavailable")
	}
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		_ = save()
		return errors.New("database_configuration_unavailable")
	}
	defer pool.Close()
	secrets := credentials.NewService(database.NewCredentialRepository(pool), keyring)
	report, err = promptquality.Run(ctx, database.NewAIRuntimeRepository(pool), ai.NewClient(nil), secrets.Resolve, *actor)
	if saveErr := save(); saveErr != nil {
		return saveErr
	}
	fmt.Fprintf(out, "synthetic evaluation: %d/128 calls, %d technical errors; report %s\n", report.CompletedCalls, report.TechnicalErrors, *reportPath)
	return err
}
func openReport(base, name string) (*os.File, error) {
	root, err := filepath.EvalSymlinks(base)
	if err != nil {
		return nil, err
	}
	path, err := filepath.Abs(name)
	if err != nil {
		return nil, err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	relative, err := filepath.Rel(root, parent)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, errors.New("report_outside_local")
	}
	return os.OpenFile(filepath.Join(parent, filepath.Base(path)), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
}
