package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/enjoydarts/cycrio/internal/health"
	"github.com/enjoydarts/cycrio/internal/infrastructure"
	"github.com/google/uuid"
	hatchet "github.com/hatchet-dev/hatchet/sdks/go"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if os.Getenv("DATABASE_URL") == "" {
		return errors.New("DATABASE_URL is required")
	}
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return errors.New("database configuration failed")
	}
	defer pool.Close()
	client, err := hatchet.NewClient(hatchet.WithClientLogLevel("warn"))
	if err != nil {
		return errors.New("Hatchet client configuration failed")
	}
	defer func() { _ = client.Close(context.Background()) }()
	task := client.NewStandaloneTask(infrastructure.TaskName, func(ctx hatchet.Context, input infrastructure.Input) (infrastructure.Output, error) {
		return infrastructure.Check(ctx, input, pool.Ping)
	})
	name := "cycrio-worker-" + uuid.NewString()
	worker, err := client.NewWorker(name, hatchet.WithWorkflows(task), hatchet.WithSlots(2))
	if err != nil {
		return errors.New("Hatchet worker registration failed")
	}
	cleanup, err := worker.Start()
	if err != nil {
		return errors.New("Hatchet worker start failed")
	}
	defer func() { _ = cleanup() }()
	ready := func(ctx context.Context) error {
		if err := pool.Ping(ctx); err != nil {
			return errors.New("database unavailable")
		}
		workers, err := client.Workers().List(ctx)
		if err != nil || workers.Rows == nil || !infrastructure.WorkerReady(name, *workers.Rows, time.Now()) {
			return errors.New("worker unavailable")
		}
		return nil
	}
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	server := &http.Server{Addr: addr, Handler: health.NewHandler(ready), ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	serverErr := make(chan error, 1)
	go func() { serverErr <- server.ListenAndServe() }()
	log.Print("worker started")
	select {
	case <-ctx.Done():
	case err := <-serverErr:
		if !errors.Is(err, http.ErrServerClosed) {
			return errors.New("HTTP server failed")
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return errors.New("HTTP shutdown failed")
	}
	return nil
}
