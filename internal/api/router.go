package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/smartx/sks-migration-center/internal/auth"
	"github.com/smartx/sks-migration-center/internal/credential"
	"github.com/smartx/sks-migration-center/internal/observability"
	"github.com/smartx/sks-migration-center/internal/repository"
)

type Dependencies struct {
	Version         string
	Environment     string
	Logger          *slog.Logger
	Readiness       func(context.Context) error
	Auth            *auth.Service
	Credentials     *credential.Vault
	Environments    EnvironmentService
	Compose         ComposeAnalyzer
	Applications    ApplicationService
	Assessments     AssessmentService
	Mappings        MappingService
	Transforms      TransformService
	MigrationPlans  MigrationPlanService
	MigrationRuns   MigrationRunService
	Evidence        MigrationEvidenceService
	Artifacts       MigrationArtifactService
	ObjectStorage   ObjectStorageService
	StorageProfiles StorageProfileService
	Velero          VeleroAddonService
	Audit           repository.IdentityRepository
	Cookies         CookieConfig
	Metrics         *observability.Metrics
}

type systemInfo struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Environment string `json:"environment"`
}

func NewRouter(deps Dependencies) http.Handler {
	if deps.Metrics == nil {
		deps.Metrics = observability.New()
	}
	mux := http.NewServeMux()
	private := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if deps.Readiness != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			if err := deps.Readiness(ctx); err != nil {
				writeProblem(w, problem{
					Type:   "/problems/not-ready",
					Title:  "Service not ready",
					Status: http.StatusServiceUnavailable,
					Detail: "A required dependency is unavailable.",
					Code:   "SYSTEM_NOT_READY",
				})
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	mux.Handle("GET /metrics", deps.Metrics.Handler())
	mux.HandleFunc("GET /api/v1/system/info", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, systemInfo{
			Name:        "SKS Migration Center",
			Version:     deps.Version,
			Environment: deps.Environment,
		})
	})
	mux.HandleFunc("POST /api/v1/auth/login", loginHandler(deps.Auth, deps.Cookies))
	private.HandleFunc("GET /api/v1/auth/me", meHandler())
	private.HandleFunc("POST /api/v1/auth/logout", logoutHandler(deps.Auth, deps.Cookies))
	private.HandleFunc("POST /api/v1/auth/password", changePasswordHandler(deps.Auth, deps.Cookies))
	private.HandleFunc("GET /api/v1/credentials", listCredentialsHandler(deps))
	private.HandleFunc("POST /api/v1/credentials", createCredentialHandler(deps))
	private.HandleFunc("DELETE /api/v1/credentials/{credentialId}", deleteCredentialHandler(deps))
	private.HandleFunc("GET /api/v1/environments", listEnvironmentsHandler(deps))
	private.HandleFunc("POST /api/v1/environments", createEnvironmentHandler(deps))
	private.HandleFunc("GET /api/v1/environments/{environmentId}", getEnvironmentHandler(deps))
	private.HandleFunc("DELETE /api/v1/environments/{environmentId}", deleteEnvironmentHandler(deps))
	private.HandleFunc("POST /api/v1/environments/{environmentId}/test", testEnvironmentHandler(deps))
	private.HandleFunc("GET /api/v1/environments/{environmentId}/capabilities", getEnvironmentCapabilitiesHandler(deps))
	private.HandleFunc("POST /api/v1/environments/{environmentId}/capabilities", refreshEnvironmentCapabilitiesHandler(deps))
	private.HandleFunc("GET /api/v1/environments/{environmentId}/namespaces", listEnvironmentNamespacesHandler(deps))
	private.HandleFunc("GET /api/v1/storage-profiles", listStorageProfilesHandler(deps))
	private.HandleFunc("POST /api/v1/storage-profiles", createStorageProfileHandler(deps))
	private.HandleFunc("POST /api/v1/storage-profiles/{profileId}/install", installStorageProfileHandler(deps))
	private.HandleFunc("POST /api/v1/storage-profiles/{profileId}/test", testStorageProfileHandler(deps))
	private.HandleFunc("POST /api/v1/object-storage/bootstrap", bootstrapObjectStorageHandler(deps))
	private.HandleFunc("POST /api/v1/object-storage/minio/adopt", adoptObjectStorageHandler(deps))
	private.HandleFunc("GET /api/v1/object-storage/profiles", listObjectStorageProfilesHandler(deps))
	private.HandleFunc("POST /api/v1/object-storage/profiles", connectExternalObjectStorageHandler(deps))
	private.HandleFunc("POST /api/v1/object-storage/minio/test", testObjectStorageHandler(deps))
	private.HandleFunc("GET /api/v1/object-storage/minio/source-policy", getMinIOSourcePolicyHandler(deps))
	private.HandleFunc("GET /api/v1/addons/{environmentId}/status", listAddonStatusHandler(deps))
	private.HandleFunc("POST /api/v1/addons/{environmentId}/install", installAddonHandler(deps))
	private.HandleFunc("GET /api/v1/addons/{environmentId}/velero/status", veleroStatusHandler(deps))
	private.HandleFunc("POST /api/v1/addons/{environmentId}/velero/repair", repairVeleroHandler(deps))
	private.HandleFunc("POST /api/v1/addons/{environmentId}/velero/reuse", reuseVeleroHandler(deps))
	private.HandleFunc("DELETE /api/v1/addons/{environmentId}/velero", uninstallAddonHandler(deps))
	private.HandleFunc("POST /api/v1/compose/analyze", analyzeComposeHandler(deps))
	private.HandleFunc("POST /api/v1/applications/discover", discoverApplicationHandler(deps))
	private.HandleFunc("POST /api/v1/applications/preview", previewApplicationHandler(deps))
	private.HandleFunc("POST /api/v1/applications/discover-all", discoverAllApplicationsHandler(deps))
	private.HandleFunc("POST /api/v1/applications/discover-compose", discoverComposeApplicationsHandler(deps))
	private.HandleFunc("GET /api/v1/applications", listApplicationsHandler(deps))
	private.HandleFunc("GET /api/v1/applications/{applicationId}", getApplicationHandler(deps))
	private.HandleFunc("POST /api/v1/assessments", createAssessmentHandler(deps))
	private.HandleFunc("GET /api/v1/assessments/{assessmentId}", getAssessmentHandler(deps))
	private.HandleFunc("GET /api/v1/mapping-profiles", listMappingsHandler(deps))
	private.HandleFunc("POST /api/v1/mapping-profiles", createMappingHandler(deps))
	private.HandleFunc("GET /api/v1/mapping-profiles/{profileId}", getMappingHandler(deps))
	private.HandleFunc("PUT /api/v1/mapping-profiles/{profileId}", updateMappingHandler(deps))
	private.HandleFunc("DELETE /api/v1/mapping-profiles/{profileId}", deleteMappingHandler(deps))
	private.HandleFunc("POST /api/v1/transforms/preview", previewTransformHandler(deps))
	private.HandleFunc("GET /api/v1/migration-plans", listMigrationPlansHandler(deps))
	private.HandleFunc("POST /api/v1/migration-plans", createMigrationPlanHandler(deps))
	private.HandleFunc("GET /api/v1/migration-plans/{planId}", getMigrationPlanHandler(deps))
	private.HandleFunc("POST /api/v1/migration-plans/{planId}/preflight", preflightMigrationPlanHandler(deps))
	private.HandleFunc("POST /api/v1/migration-plans/{planId}/runs", startMigrationRunHandler(deps))
	private.HandleFunc("GET /api/v1/migration-runs", listMigrationRunsHandler(deps))
	private.HandleFunc("GET /api/v1/migration-runs/{runId}", getMigrationRunHandler(deps))
	private.HandleFunc("GET /api/v1/migration-runs/{runId}/volume-transfers", listMigrationVolumeTransfersHandler(deps))
	private.HandleFunc("GET /api/v1/migration-runs/{runId}/topology", migrationTopologyHandler(deps))
	private.HandleFunc("POST /api/v1/migration-runs/{runId}/topology/refresh", refreshMigrationTopologyHandler(deps))
	private.HandleFunc("GET /api/v1/migration-runs/{runId}/timeline", migrationTimelineHandler(deps))
	private.HandleFunc("POST /api/v1/migration-runs/{runId}/cancel", cancelMigrationRunHandler(deps))
	private.HandleFunc("POST /api/v1/migration-runs/{runId}/retry", retryMigrationRunHandler(deps))
	private.HandleFunc("DELETE /api/v1/migration-runs/{runId}", deleteMigrationRunHandler(deps))
	private.HandleFunc("POST /api/v1/migration-runs/{runId}/cutover", confirmMigrationCutoverHandler(deps))
	private.HandleFunc("POST /api/v1/migration-runs/{runId}/rollback", rollbackMigrationRunHandler(deps))
	private.HandleFunc("POST /api/v1/migration-runs/{runId}/restore-source", restoreMigrationSourceHandler(deps))
	private.HandleFunc("GET /api/v1/migration-runs/{runId}/report", migrationReportHandler(deps))
	private.HandleFunc("GET /api/v1/migration-runs/{runId}/report-data", migrationReportDataHandler(deps))
	private.HandleFunc("POST /api/v1/migration-runs/{runId}/cleanup", cleanupMigrationArtifactsHandler(deps))
	private.HandleFunc("GET /api/v1/migration-runs/{runId}/events", streamMigrationEventsHandler(deps))
	mux.Handle("/api/v1/", requireAuthentication(deps.Auth, private))

	return deps.Metrics.Middleware(requestLogger(deps.Logger, securityHeaders(mux)))
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = encodeJSON(w, value)
}

func encodeJSON(w http.ResponseWriter, value any) error {
	return json.NewEncoder(w).Encode(value)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'; base-uri 'self'")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

func requestLogger(logger *slog.Logger, next http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		logger.Info("http request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(started))
	})
}
