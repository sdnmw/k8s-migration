package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	kubernetesadapter "github.com/smartx/sks-migration-center/internal/adapter/kubernetes"
	s3adapter "github.com/smartx/sks-migration-center/internal/adapter/s3"
	sshadapter "github.com/smartx/sks-migration-center/internal/adapter/ssh"
	veleroadapter "github.com/smartx/sks-migration-center/internal/adapter/velero"
	"github.com/smartx/sks-migration-center/internal/addon"
	"github.com/smartx/sks-migration-center/internal/api"
	applicationservice "github.com/smartx/sks-migration-center/internal/application"
	assessmentservice "github.com/smartx/sks-migration-center/internal/assessment"
	"github.com/smartx/sks-migration-center/internal/auth"
	composeanalyzer "github.com/smartx/sks-migration-center/internal/compose"
	"github.com/smartx/sks-migration-center/internal/config"
	"github.com/smartx/sks-migration-center/internal/credential"
	"github.com/smartx/sks-migration-center/internal/database"
	environmentservice "github.com/smartx/sks-migration-center/internal/environment"
	mappingservice "github.com/smartx/sks-migration-center/internal/mapping"
	migrationservice "github.com/smartx/sks-migration-center/internal/migration"
	"github.com/smartx/sks-migration-center/internal/objectstorage"
	"github.com/smartx/sks-migration-center/internal/observability"
	postgresrepository "github.com/smartx/sks-migration-center/internal/repository/postgres"
	"github.com/smartx/sks-migration-center/internal/security"
	"github.com/smartx/sks-migration-center/internal/storageprofile"
	"github.com/smartx/sks-migration-center/internal/transform"
	veleroservice "github.com/smartx/sks-migration-center/internal/velero"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "healthcheck" {
		if err := healthcheck(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	cfg, err := config.LoadAPI()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	startupCtx, startupCancel := context.WithTimeout(context.Background(), cfg.Database.ConnectTimeout)
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
	identityRepository := postgresrepository.NewIdentityRepository(pool)
	authService, err := auth.NewService(identityRepository, cfg.Security.SessionTTL)
	if err != nil {
		logger.Error("authentication service initialization failed", "error", err)
		os.Exit(1)
	}
	bootstrapPassword, err := readSecret(cfg.Security.BootstrapPasswordFile)
	if err != nil {
		logger.Error("administrator bootstrap secret unavailable", "error", err)
		os.Exit(1)
	}
	if err := authService.EnsureAdministrator(startupCtx, cfg.Security.AdministratorUsername, bootstrapPassword); err != nil {
		logger.Error("administrator bootstrap failed", "error", err)
		os.Exit(1)
	}
	keyring, err := security.LoadKeyringFile(cfg.Security.CredentialMasterKeyFile, cfg.Security.CredentialKeyVersion)
	if err != nil {
		logger.Error("credential keyring initialization failed", "error", err)
		os.Exit(1)
	}
	credentialVault, err := credential.NewVault(postgresrepository.NewCredentialRepository(pool), keyring)
	if err != nil {
		logger.Error("credential vault initialization failed", "error", err)
		os.Exit(1)
	}
	kubernetesClient := kubernetesadapter.NewClient(12 * time.Second)
	environmentRepository := postgresrepository.NewEnvironmentRepository(pool)
	environmentService, err := environmentservice.NewService(
		environmentRepository, credentialVault, kubernetesClient, sshadapter.NewClient(12*time.Second),
	)
	if err != nil {
		logger.Error("environment service initialization failed", "error", err)
		os.Exit(1)
	}
	applicationService, err := applicationservice.NewService(
		postgresrepository.NewEnvironmentRepository(pool), postgresrepository.NewApplicationRepository(pool), credentialVault, kubernetesadapter.NewClient(30*time.Second), composeanalyzer.NewAnalyzer(),
	)
	if err != nil {
		logger.Error("application discovery service initialization failed", "error", err)
		os.Exit(1)
	}
	applicationService.WithComposeHost(sshadapter.NewClient(30 * time.Second))
	assessmentService, err := assessmentservice.NewService(
		postgresrepository.NewApplicationRepository(pool), postgresrepository.NewEnvironmentRepository(pool),
		postgresrepository.NewAssessmentRepository(pool), assessmentservice.DefaultEngine(),
	)
	if err != nil {
		logger.Error("assessment service initialization failed", "error", err)
		os.Exit(1)
	}
	mappingService, err := mappingservice.NewService(postgresrepository.NewMappingRepository(pool), postgresrepository.NewEnvironmentRepository(pool))
	if err != nil {
		logger.Error("mapping service initialization failed", "error", err)
		os.Exit(1)
	}
	transformService, err := transform.NewService(postgresrepository.NewMappingRepository(pool), transform.NewEngine())
	if err != nil {
		logger.Error("transform service initialization failed", "error", err)
		os.Exit(1)
	}
	migrationPlanService, err := migrationservice.NewService(
		postgresrepository.NewMigrationRepository(pool), postgresrepository.NewEnvironmentRepository(pool),
		postgresrepository.NewApplicationRepository(pool), postgresrepository.NewAssessmentRepository(pool), postgresrepository.NewMappingRepository(pool),
	)
	if err != nil {
		logger.Error("migration plan service initialization failed", "error", err)
		os.Exit(1)
	}
	migrationRepository := postgresrepository.NewMigrationRepository(pool)
	migrationRunService, err := migrationservice.NewRunService(migrationRepository, migrationRepository, migrationRepository)
	if err != nil {
		logger.Error("migration run service initialization failed", "error", err)
		os.Exit(1)
	}
	migrationEvidenceService, err := migrationservice.NewEvidenceService(
		migrationRepository, migrationRepository, migrationRepository, migrationRepository,
		postgresrepository.NewApplicationRepository(pool), postgresrepository.NewMappingRepository(pool),
		environmentRepository, credentialVault, kubernetesClient,
	)
	if err != nil {
		logger.Error("migration evidence service initialization failed", "error", err)
		os.Exit(1)
	}
	migrationRunService.ConfigureEvidence(migrationEvidenceService)
	artifactCleanupService, err := migrationservice.NewArtifactCleanupService(
		migrationRepository, migrationRepository, migrationRepository, environmentRepository, credentialVault, veleroadapter.NewClient(20*time.Second),
	)
	if err != nil {
		logger.Error("migration artifact cleanup service initialization failed", "error", err)
		os.Exit(1)
	}
	chartManager, err := addon.NewManager(cfg.AddonChartRoot)
	if err != nil {
		logger.Error("add-on manager initialization failed", "error", err)
		os.Exit(1)
	}
	policyFile, err := os.Open(cfg.MinIOSourceLock)
	if err != nil {
		logger.Error("MinIO source lock unavailable", "error", err)
		os.Exit(1)
	}
	minioPolicy, err := objectstorage.LoadSourcePolicy(policyFile)
	_ = policyFile.Close()
	if err != nil {
		logger.Error("MinIO source lock invalid", "error", err)
		os.Exit(1)
	}
	objectStorageService, err := objectstorage.NewService(
		postgresrepository.NewPlatformRepository(pool), environmentRepository, credentialVault,
		chartManager, kubernetesClient, s3adapter.NewClient(), minioPolicy,
	)
	if err != nil {
		logger.Error("object storage service initialization failed", "error", err)
		os.Exit(1)
	}
	if err := objectStorageService.ConfigureRuntimeImage(cfg.MinIOImage); err != nil {
		logger.Error("MinIO runtime image configuration invalid", "error", err)
		os.Exit(1)
	}
	storageProfileService, err := storageprofile.NewService(
		postgresrepository.NewStorageRepository(pool), environmentRepository, postgresrepository.NewPlatformRepository(pool),
		credentialVault, kubernetesClient, chartManager, storageprofile.Images{
			Plugin: cfg.NFSImages.Plugin, Provisioner: cfg.NFSImages.Provisioner, Resizer: cfg.NFSImages.Resizer,
			Liveness: cfg.NFSImages.Liveness, Registrar: cfg.NFSImages.Registrar, Probe: cfg.NFSImages.Probe,
		},
	)
	if err != nil {
		logger.Error("storage profile service initialization failed", "error", err)
		os.Exit(1)
	}
	veleroService, err := veleroservice.NewService(
		postgresrepository.NewPlatformRepository(pool), environmentRepository, credentialVault, chartManager, kubernetesClient,
		veleroadapter.NewClient(20*time.Second), veleroservice.Images{Velero: cfg.VeleroImages.Server, AWS: cfg.VeleroImages.AWSPlugin},
	)
	if err != nil {
		logger.Error("Velero add-on service initialization failed", "error", err)
		os.Exit(1)
	}
	metrics := observability.New()

	server := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: api.NewRouter(api.Dependencies{
			Version:         cfg.Version,
			Environment:     cfg.Environment,
			Logger:          logger,
			Readiness:       pool.Ping,
			Auth:            authService,
			Credentials:     credentialVault,
			Environments:    environmentService,
			Compose:         composeanalyzer.NewAnalyzer(),
			Applications:    applicationService,
			Assessments:     assessmentService,
			Mappings:        mappingService,
			Transforms:      transformService,
			MigrationPlans:  migrationPlanService,
			MigrationRuns:   migrationRunService,
			Evidence:        migrationEvidenceService,
			Artifacts:       artifactCleanupService,
			ObjectStorage:   objectStorageService,
			StorageProfiles: storageProfileService,
			Velero:          veleroService,
			Audit:           identityRepository,
			Cookies:         api.CookieConfig{Secure: cfg.Security.CookieSecure},
			Metrics:         metrics,
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("api server started", "address", cfg.HTTPAddr, "version", cfg.Version)
		if serveErr := server.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- serveErr
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	select {
	case <-ctx.Done():
		logger.Info("shutdown requested")
	case serveErr := <-errCh:
		logger.Error("api server failed", "error", serveErr)
		os.Exit(1)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("api server shutdown failed", "error", err)
		os.Exit(1)
	}
}

func readSecret(path string) (string, error) {
	value, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	secret := strings.TrimSpace(string(value))
	if secret == "" {
		return "", fmt.Errorf("secret file %s is empty", path)
	}
	return secret, nil
}

func healthcheck() error {
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get("http://127.0.0.1:8080/healthz")
	if err != nil {
		return fmt.Errorf("healthcheck request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck returned %s", response.Status)
	}
	return nil
}
