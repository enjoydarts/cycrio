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

	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/airuntimeconfig"
	"github.com/enjoydarts/cycrio/internal/bootstrap"
	"github.com/enjoydarts/cycrio/internal/catalogruntime"
	"github.com/enjoydarts/cycrio/internal/clerkdelete"
	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/health"
	"github.com/enjoydarts/cycrio/internal/healthchecks"
	"github.com/enjoydarts/cycrio/internal/infrastructure"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/enjoydarts/cycrio/internal/mailprocessingruntime"
	"github.com/enjoydarts/cycrio/internal/mailruntime"
	"github.com/enjoydarts/cycrio/internal/mailsync"
	"github.com/enjoydarts/cycrio/internal/mailsyncruntime"
	"github.com/enjoydarts/cycrio/internal/notificationruntime"
	"github.com/enjoydarts/cycrio/internal/priceresearch"
	"github.com/enjoydarts/cycrio/internal/priceresearchruntime"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/enjoydarts/cycrio/internal/telemetry"
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

func run() (result error) {
	bootstrapPath := flag.String("bootstrap-file", ".local/bootstrap.json", "public sign-in bootstrap configuration")
	keyringPath := flag.String("keyring-file", ".local/secrets/keyring.json", "encryption keyring file")
	flag.Parse()
	config, err := bootstrap.Load(*bootstrapPath)
	if err != nil {
		return err
	}
	keyring, err := secretstore.Load(*keyringPath)
	if err != nil {
		return errors.New("encryption keyring unavailable")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ctx, cancelWorker := withWorkerShutdown(ctx)
	defer cancelWorker(nil)
	if os.Getenv("DATABASE_URL") == "" {
		return errors.New("DATABASE_URL is required")
	}
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return errors.New("database configuration failed")
	}
	defer pool.Close()
	reporter := telemetry.New(ctx, os.Getenv("CYCRIO_ENV") == "production", database.NewTelemetrySource(pool, keyring).Load, func(ctx context.Context, owner string, execute func(context.Context) error) error {
		return database.WithUserOperation(ctx, pool, owner, execute)
	}, "worker")
	defer reporter.Close()
	ctx = withWorkerTelemetry(ctx, reporter)
	var readOutcome jobOutcomeReader
	if os.Getenv("CYCRIO_ENV") == "production" {
		readOutcome = database.NewJobTelemetryRepository(pool).Outcome
		go queueAnalyticsLoop(ctx, database.NewJobTelemetryRepository(pool), reporter)
	}
	client, err := hatchet.NewClient(hatchet.WithClientLogLevel("warn"))
	if err != nil {
		return errors.New("Hatchet client configuration failed")
	}
	defer func() { _ = client.Close(context.Background()) }()
	task := client.NewStandaloneTask(infrastructure.TaskName, func(ctx hatchet.Context, input infrastructure.Input) (infrastructure.Output, error) {
		return captureWorkerTask(withWorkerTelemetry(ctx, reporter), nil, "worker_check", "", "", func(ctx context.Context) (infrastructure.Output, error) {
			return infrastructure.Check(ctx, input, pool.Ping)
		})
	})
	catalog := catalogruntime.New(pool, keyring)
	catalogTask := client.NewStandaloneTask(catalogTaskName, func(ctx hatchet.Context, input CatalogSyncInput) (CatalogSyncOutput, error) {
		return captureWorkerTask(withWorkerTelemetry(ctx, reporter), nil, "worker_catalog_sync", "", "", func(ctx context.Context) (CatalogSyncOutput, error) {
			return executeCatalogSync(ctx, input, catalog.SyncProvider)
		})
	}, hatchet.WithRetries(0))
	research := priceresearchruntime.New(pool, keyring)
	researchTask := client.NewStandaloneTask(priceResearchTaskName, func(ctx hatchet.Context, input priceresearch.Lease) (PriceResearchOutput, error) {
		return captureWorkerTask(withWorkerTelemetry(ctx, reporter), readOutcome, "worker_price_research", "research", input.RunID, func(ctx context.Context) (PriceResearchOutput, error) {
			if err := research.Execute(ctx, input); err != nil {
				return PriceResearchOutput{Status: "failed"}, hideWorkerCause("price research unavailable", err)
			}
			return PriceResearchOutput{Status: "recorded"}, nil
		})
	}, hatchet.WithRetries(0), hatchet.WithExecutionTimeout(9*time.Minute))
	execution := airuntimeconfig.New(pool, keyring)
	aiTask := client.NewStandaloneTask(aiTestTaskName, func(ctx hatchet.Context, input airuntime.Lease) (AITestOutput, error) {
		return captureWorkerTask(withWorkerTelemetry(ctx, reporter), readOutcome, "worker_ai_test", "ai_test", input.TestRunID, func(ctx context.Context) (AITestOutput, error) {
			if err := execution.Run(ctx, input); err != nil {
				if errors.Is(err, airuntime.ErrConflict) {
					return AITestOutput{Status: "ignored"}, nil
				}
				return AITestOutput{Status: "failed"}, hideWorkerCause("AI connection test unavailable", err)
			}
			return AITestOutput{Status: "recorded"}, nil
		})
	}, hatchet.WithRetries(0), hatchet.WithExecutionTimeout(5*time.Minute))
	mail := mailruntime.New(pool, keyring)
	mailTask := client.NewStandaloneTask(mailProbeTaskName, func(ctx hatchet.Context, input mailaccounts.Lease) (MailProbeOutput, error) {
		return captureWorkerTask(withWorkerTelemetry(ctx, reporter), readOutcome, "worker_mail_probe", "probe", input.ProbeID, func(ctx context.Context) (MailProbeOutput, error) {
			if err := database.WithMailOperation(ctx, pool, "probe", input.ProbeID, func(ctx context.Context) error { return mail.Run(ctx, input) }); err != nil {
				return MailProbeOutput{Status: "failed"}, hideWorkerCause("mail connection check unavailable", err)
			}
			return MailProbeOutput{Status: "handled"}, nil
		})
	}, hatchet.WithRetries(0), hatchet.WithExecutionTimeout(time.Minute))
	name := "cycrio-worker-" + uuid.NewString()
	discovery := mailsyncruntime.New(pool, keyring)
	discoveryWake := make(chan struct{}, 1)
	automationWake := make(chan struct{}, 1)
	discoverTask := client.NewStandaloneTask(mailDiscoverTaskName, func(ctx hatchet.Context, input mailsync.Lease) (MailDiscoverOutput, error) {
		return captureWorkerTask(withWorkerTelemetry(ctx, reporter), readOutcome, "worker_mail_discovery", "sync", input.RunID, func(ctx context.Context) (MailDiscoverOutput, error) {
			out := MailDiscoverOutput{Status: "ignored"}
			err := database.WithMailOperation(ctx, pool, "sync", input.RunID, func(ctx context.Context) (err error) {
				out, err = executeMailDiscovery(ctx, input, discovery.Execute, discoveryWake, automationWake)
				return err
			})
			return out, err
		})
	}, hatchet.WithRetries(0), hatchet.WithExecutionTimeout(time.Minute))
	processing := mailprocessingruntime.New(pool, keyring)
	defer func() { _ = processing.Close() }()
	processingWake := make(chan struct{}, 1)
	processingTask := client.NewStandaloneTask(mailProcessingTaskName, func(ctx hatchet.Context, input mailprocessing.Lease) (MailProcessingOutput, error) {
		return captureWorkerTask(withWorkerTelemetry(ctx, reporter), readOutcome, "worker_mail_processing", "processing", input.RunID, func(ctx context.Context) (MailProcessingOutput, error) {
			out := MailProcessingOutput{Status: "ignored"}
			err := database.WithMailOperation(ctx, pool, "processing", input.RunID, func(ctx context.Context) (err error) {
				out, err = executeMailProcessing(ctx, input, processing.Execute, processingWake, automationWake)
				return err
			})
			return out, err
		})
	}, hatchet.WithRetries(0), hatchet.WithExecutionTimeout(5*time.Minute))
	notificationRepository := database.NewNotificationDeliveryRepository(pool, keyring, config.Issuer())
	notificationService := notificationruntime.NewService(notificationRepository, notificationruntime.NewResendSender(nil))
	notificationTask := client.NewStandaloneTask(notificationTaskName, func(ctx hatchet.Context, input notificationruntime.Task) (notificationruntime.Result, error) {
		return captureWorkerTask(withWorkerTelemetry(ctx, reporter), readOutcome, "worker_notification_delivery", "notification", input.DeliveryID, func(ctx context.Context) (notificationruntime.Result, error) {
			return notificationService.Execute(ctx, input)
		})
	}, hatchet.WithRetries(0), hatchet.WithExecutionTimeout(30*time.Second))
	groups := workerGroups(name, workerTasks{check: task, catalog: catalogTask, priceResearch: researchTask, aiTest: aiTask, mailProbe: mailTask, mailDiscovery: discoverTask, mailProcessing: processingTask, notification: notificationTask})
	cleanup, err := startWorkerGroups(groups, func(group workerGroup) (workerStarter, error) {
		return client.NewWorker(group.name, hatchet.WithWorkflows(group.workflows...), hatchet.WithSlots(group.slots))
	})
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, cleanup()) }()
	workerNames := make([]string, 0, len(groups))
	for _, group := range groups {
		workerNames = append(workerNames, group.name)
	}
	go dispatchSubscriptionAutomation(withWorkerOperation(ctx, "worker_subscription_register"), database.NewSubscriptionRepository(pool).RegisterDue, time.Second)
	go dispatchSubscriptionAutomation(withWorkerOperation(ctx, "worker_subscription_changes"), database.NewSubscriptionRepository(pool).ApplyScheduledChangesDue, 2*time.Second)
	go dispatchPriceResearch(withWorkerOperation(ctx, "worker_price_research"), research.ClaimDue, func(ctx context.Context, l priceresearch.Lease) error {
		_, err := researchTask.RunNoWait(ctx, l)
		return err
	}, 10*time.Second)
	go dispatchSubscriptionAutomation(withWorkerOperation(ctx, "worker_billing_record"), database.NewBillingRepository(pool).RecordDue, time.Second)
	go dispatchSubscriptionAutomation(withWorkerOperation(ctx, "worker_notification_generate"), notificationRepository.GenerateDue, 10*time.Second)
	go dispatchSubscriptionAutomation(withWorkerOperation(ctx, "worker_erasure_purge"), database.NewErasureRepository(pool, keyring).PurgeDue, 10*time.Second)
	clerkErasure := clerkdelete.NewService(database.NewClerkErasureRepository(pool, keyring, config.Issuer()), clerkdelete.NewClient(nil))
	go dispatchClerkErasure(withWorkerOperation(ctx, "worker_clerk_erasure"), clerkErasure.Drain, 10*time.Second)
	go dispatchNotifications(withWorkerOperation(ctx, "worker_notification_delivery"), notificationRepository.ClaimNotifications, func(ctx context.Context, task notificationruntime.Task) error {
		_, err := notificationTask.RunNoWait(ctx, task)
		return err
	}, 10*time.Second)
	go dispatchMailAutomation(withWorkerOperation(ctx, "worker_mail_automation"), database.NewMailAutomationRepository(pool).Tick, 2*time.Second, automationWake, discoveryWake, processingWake)
	go dispatchCatalog(withWorkerOperation(ctx, "worker_catalog_sync"), catalog.ClaimDue, func(ctx context.Context, input CatalogSyncInput) error {
		_, err := catalogTask.RunNoWait(ctx, input)
		return err
	}, 10*time.Second)
	go dispatchAITests(withWorkerOperation(ctx, "worker_ai_test"), execution.Claim, func(ctx context.Context, input airuntime.Lease) error {
		_, err := aiTask.RunNoWait(ctx, input)
		return err
	}, 10*time.Second)
	go dispatchMailProbes(withWorkerOperation(ctx, "worker_mail_probe"), mail.Claim, func(ctx context.Context, input mailaccounts.Lease) error {
		_, err := mailTask.RunNoWait(ctx, input)
		return err
	}, 10*time.Second)
	go dispatchMailDiscovery(withWorkerOperation(ctx, "worker_mail_discovery"), discovery.Claim, func(ctx context.Context, input mailsync.Lease) error {
		_, err := discoverTask.RunNoWait(ctx, input)
		return err
	}, 10*time.Second, discoveryWake)
	go dispatchMailProcessing(withWorkerOperation(ctx, "worker_mail_processing"), processing.Claim, func(ctx context.Context, input mailprocessing.Lease) error {
		_, err := processingTask.RunNoWait(ctx, input)
		return err
	}, 10*time.Second, processingWake)
	ready := workerReadiness(ctx, func(ctx context.Context) error {
		if err := pool.Ping(ctx); err != nil {
			return errors.New("database unavailable")
		}
		workers, err := client.Workers().List(ctx)
		if err != nil || workers.Rows == nil || !infrastructure.WorkersReady(workerNames, *workers.Rows, time.Now()) {
			return errors.New("worker unavailable")
		}
		return nil
	})
	heartbeatCredentials := credentials.NewService(database.NewCredentialRepository(pool), keyring)
	heartbeat := healthchecks.NewService(database.NewHealthchecksRepository(pool), heartbeatCredentials.Resolve, healthchecks.NewClient(nil), ready, healthchecks.NewAPIProbe(nil))
	go dispatchHeartbeat(withWorkerOperation(ctx, "worker_heartbeat"), ready, heartbeat.Tick)
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
	return workerShutdownError(ctx)
}
