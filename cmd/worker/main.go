package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	kubernetesadapter "github.com/smartx/sks-migration-center/internal/adapter/kubernetes"
	sshadapter "github.com/smartx/sks-migration-center/internal/adapter/ssh"
	veleroadapter "github.com/smartx/sks-migration-center/internal/adapter/velero"
	"github.com/smartx/sks-migration-center/internal/config"
	"github.com/smartx/sks-migration-center/internal/credential"
	"github.com/smartx/sks-migration-center/internal/database"
	domainmigration "github.com/smartx/sks-migration-center/internal/domain/migration"
	migrationservice "github.com/smartx/sks-migration-center/internal/migration"
	"github.com/smartx/sks-migration-center/internal/observability"
	postgresrepository "github.com/smartx/sks-migration-center/internal/repository/postgres"
	"github.com/smartx/sks-migration-center/internal/security"
	"github.com/smartx/sks-migration-center/internal/transform"
	"github.com/smartx/sks-migration-center/internal/worker"
)

func main() {
	cfg, err := config.LoadWorker()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	startupCtx, startupCancel := context.WithTimeout(ctx, cfg.Database.ConnectTimeout)
	defer startupCancel()
	pool, err := database.Open(startupCtx, database.Config{
		URL:            cfg.Database.URL,
		MaxConnections: cfg.Database.MaxConnections,
		MinConnections: cfg.Database.MinConnections,
		ConnectTimeout: cfg.Database.ConnectTimeout,
	})
	if err != nil {
		logger.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := database.Migrate(startupCtx, pool); err != nil {
		logger.Error("database migration failed", "error", err)
		os.Exit(1)
	}
	keyring, err := security.LoadKeyringFile(cfg.CredentialMasterKeyFile, cfg.CredentialKeyVersion)
	if err != nil {
		logger.Error("credential keyring initialization failed", "error", err)
		os.Exit(1)
	}
	credentialVault, err := credential.NewVault(postgresrepository.NewCredentialRepository(pool), keyring)
	if err != nil {
		logger.Error("credential vault initialization failed", "error", err)
		os.Exit(1)
	}
	migrationRepository := postgresrepository.NewMigrationRepository(pool)
	environmentRepository := postgresrepository.NewEnvironmentRepository(pool)
	applicationRepository := postgresrepository.NewApplicationRepository(pool)
	mappingRepository := postgresrepository.NewMappingRepository(pool)
	platformRepository := postgresrepository.NewPlatformRepository(pool)
	kubernetesClient := kubernetesadapter.NewClient(30 * time.Second)
	veleroExecutor, err := migrationservice.NewVeleroExecutor(
		migrationRepository, migrationRepository, migrationRepository,
		environmentRepository, applicationRepository, mappingRepository,
		credentialVault, veleroadapter.NewClient(30*time.Second),
		migrationservice.WithKubernetesExecution(kubernetesClient, cfg.StagingHelperImage),
	)
	if err != nil {
		logger.Error("migration executor initialization failed", "error", err)
		os.Exit(1)
	}
	composeExecutor, err := migrationservice.NewComposeExecutor(
		migrationRepository, migrationRepository, migrationRepository,
		environmentRepository, applicationRepository, mappingRepository,
		credentialVault, kubernetesClient, transform.NewEngine(), cfg.KomposeImage, cfg.StagingHelperImage,
		migrationservice.WithComposeDataMovement(platformRepository, sshadapter.NewClient(30*time.Second), cfg.KopiaImage),
	)
	if err != nil {
		logger.Error("Compose migration executor initialization failed", "error", err)
		os.Exit(1)
	}
	dispatcher, err := migrationservice.NewSourceDispatcher(migrationRepository, migrationRepository, applicationRepository, veleroExecutor, composeExecutor)
	if err != nil {
		logger.Error("migration source dispatcher initialization failed", "error", err)
		os.Exit(1)
	}
	handlers := map[domainmigration.StepType]worker.Handler{}
	for _, stepType := range []domainmigration.StepType{
		domainmigration.StepPreflight, domainmigration.StepPreSync, domainmigration.StepQuiesce,
		domainmigration.StepFinalBackup, domainmigration.StepTransfer, domainmigration.StepTransform,
		domainmigration.StepRestore, domainmigration.StepValidation, domainmigration.StepRollback,
	} {
		handlers[stepType] = dispatcher
	}

	hostname, err := os.Hostname()
	if err != nil {
		hostname = "worker"
	}
	metrics := observability.New()
	runner, err := worker.NewRunner(worker.Config{
		Version:           cfg.Version,
		OwnerID:           hostname + "-" + uuid.NewString(),
		PollInterval:      cfg.PollInterval,
		HeartbeatInterval: cfg.HeartbeatInterval,
		LeaseDuration:     cfg.LeaseDuration,
		RetryDelay:        cfg.RetryDelay,
		MaxAttempts:       5,
		Observer:          metrics,
	}, postgresrepository.NewJobRepository(pool), handlers, logger)
	if err != nil {
		logger.Error("worker configuration failed", "error", err)
		os.Exit(1)
	}
	metricsServer := &http.Server{Addr: cfg.MetricsAddr, Handler: metrics.Handler(), ReadHeaderTimeout: 5 * time.Second}
	errCh := make(chan error, 2)
	go func() {
		logger.Info("worker metrics server started", "address", cfg.MetricsAddr)
		if serveErr := metricsServer.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- fmt.Errorf("worker metrics server: %w", serveErr)
		}
	}()
	go func() { errCh <- runner.Run(ctx) }()
	select {
	case <-ctx.Done():
	case runErr := <-errCh:
		if runErr != nil {
			logger.Error("worker stopped with an error", "error", runErr)
			stop()
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := metricsServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("worker metrics shutdown failed", "error", err)
	}
}
