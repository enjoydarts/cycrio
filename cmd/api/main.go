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

	"github.com/enjoydarts/cycrio/internal/airuntimeconfig"
	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/bootstrap"
	"github.com/enjoydarts/cycrio/internal/catalogruntime"
	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/httpapi"
	"github.com/enjoydarts/cycrio/internal/mailprocessingruntime"
	"github.com/enjoydarts/cycrio/internal/mailruntime"
	"github.com/enjoydarts/cycrio/internal/mailsyncruntime"
	"github.com/enjoydarts/cycrio/internal/secretstore"
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
	keyringPath := flag.String("keyring-file", ".local/secrets/keyring.json", "encryption keyring file")
	flag.Parse()
	config, err := bootstrap.Load(*bootstrapPath)
	if err != nil {
		return err
	}
	keyring, err := secretstore.Load(*keyringPath)
	if err != nil {
		return errors.New("encryption keyring unavailable; initialize or restore the keyring before starting the API")
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
	credentialService := credentials.NewService(database.NewCredentialRepository(pool), keyring)
	subscriptionRepository := database.NewSubscriptionRepository(pool)
	billingRepository := database.NewBillingRepository(pool)
	server := &http.Server{Addr: addr, Handler: httpapi.NewHandler(config, verifier, store.FindPrincipal, pool.Ping, httpapi.WithCredentials(credentialService), httpapi.WithModelCatalog(catalogruntime.New(pool, keyring)), httpapi.WithAIRuntime(airuntimeconfig.New(pool, keyring)), httpapi.WithMailAccounts(mailruntime.New(pool, keyring)), httpapi.WithMailSync(mailsyncruntime.New(pool, keyring)), httpapi.WithMailProcessing(mailprocessingruntime.New(pool, keyring)), httpapi.WithSubscriptions(subscriptionRepository), httpapi.WithAnalysisFeed(subscriptionRepository), httpapi.WithBilling(billingRepository)), ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 64 << 10}
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
