package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/bootstrap"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/httpapi"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run() error {
	bootstrapPath := flag.String("bootstrap-file", ".local/bootstrap.json", "public sign-in bootstrap configuration")
	flag.Parse()
	config, err := bootstrap.Load(*bootstrapPath)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if os.Getenv("DATABASE_URL") == "" {
		return errors.New("DATABASE_URL is required")
	}
	poolConfig, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil {
		return errors.New("database configuration failed")
	}
	poolConfig.MaxConns = 5
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return errors.New("database configuration failed")
	}
	defer pool.Close()
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	store := database.NewStore(pool)
	verifier := auth.NewVerifier(config.Issuer(), config.AllowedOrigins, nil)
	server := &http.Server{Addr: addr, Handler: httpapi.NewHandler(config, verifier, store.FindPrincipal, pool.Ping), ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 64 << 10}
	serverErr := make(chan error, 1)
	go func() { serverErr <- server.ListenAndServe() }()
	log.Print("API started")
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
