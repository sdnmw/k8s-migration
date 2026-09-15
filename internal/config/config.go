package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultVersion         = "dev"
	defaultEnvironment     = "development"
	defaultHTTPAddr        = ":8080"
	defaultShutdownTimeout = 15 * time.Second
	defaultWorkerHeartbeat = 10 * time.Second
	defaultWorkerPoll      = 2 * time.Second
	defaultWorkerLease     = 30 * time.Second
	defaultWorkerRetry     = 10 * time.Second
	defaultDatabaseURL     = "postgres://migration:migration@127.0.0.1:5432/migration?sslmode=disable"
	defaultDatabaseTimeout = 10 * time.Second
	defaultSessionTTL      = 8 * time.Hour
)

type API struct {
	HTTPAddr        string
	Environment     string
	Version         string
	LogLevel        slog.Level
	ShutdownTimeout time.Duration
	Database        Database
	Security        Security
	AddonChartRoot  string
	MinIOSourceLock string
	MinIOImage      string
	NFSImages       NFSImages
	VeleroImages    VeleroImages
}

type NFSImages struct {
	Plugin      string
	Provisioner string
	Resizer     string
	Liveness    string
	Registrar   string
	Probe       string
}

type VeleroImages struct {
	Server    string
	AWSPlugin string
}

type Worker struct {
	Environment              string
	Version                  string
	LogLevel                 slog.Level
	HeartbeatInterval        time.Duration
	PollInterval             time.Duration
	LeaseDuration            time.Duration
	RetryDelay               time.Duration
	Database                 Database
	CredentialMasterKeyFile  string
	CredentialKeyVersion     int
	StagingHelperImage       string
	KomposeImage             string
	KopiaImage               string
	MetricsAddr              string
	ComposeImageRepository   string
	RegistryDockerConfigFile string
	RegistryPullSecretName   string
}

type Database struct {
	URL            string
	MaxConnections int32
	MinConnections int32
	ConnectTimeout time.Duration
}

type Security struct {
	AdministratorUsername   string
	BootstrapPasswordFile   string
	CredentialMasterKeyFile string
	CredentialKeyVersion    int
	SessionTTL              time.Duration
	CookieSecure            bool
}

func LoadAPI() (API, error) {
	shutdownTimeout, err := durationEnv("SHUTDOWN_TIMEOUT", defaultShutdownTimeout)
	if err != nil {
		return API{}, err
	}
	level, err := logLevelEnv()
	if err != nil {
		return API{}, err
	}

	environment := stringEnv("APP_ENV", defaultEnvironment)
	securityConfig, err := loadSecurityConfig(environment)
	if err != nil {
		return API{}, err
	}
	addonChartRoot, sourceLock := "/charts", "/release/minio/source.lock.yaml"
	if environment == "development" {
		addonChartRoot, sourceLock = "deploy/charts", "build/minio/source.lock.yaml"
	}
	return API{
		HTTPAddr:        stringEnv("HTTP_ADDR", defaultHTTPAddr),
		Environment:     environment,
		Version:         stringEnv("APP_VERSION", defaultVersion),
		LogLevel:        level,
		ShutdownTimeout: shutdownTimeout,
		Database:        databaseConfig(),
		Security:        securityConfig,
		AddonChartRoot:  stringEnv("ADDON_CHART_ROOT", addonChartRoot),
		MinIOSourceLock: stringEnv("MINIO_SOURCE_LOCK_FILE", sourceLock),
		MinIOImage:      stringEnv("MINIO_IMAGE", "docker.io/minio/minio@sha256:14cea493d9a34af32f524e538b8346cf79f3321eff8e708c1e2960462bd8936e"),
		NFSImages: NFSImages{
			Plugin:      stringEnv("NFS_PLUGIN_IMAGE", "m.daocloud.io/registry.k8s.io/sig-storage/nfsplugin@sha256:1eb5a85180a4ad0193a31d319b163f35c8c1857794ebaac71d8abcdd5a0516d3"),
			Provisioner: stringEnv("NFS_PROVISIONER_IMAGE", "m.daocloud.io/registry.k8s.io/sig-storage/csi-provisioner@sha256:a4b0b1a37605b7b04a293e136edf7006ec1786a8eb3f4e5a945f81d667dcc371"),
			Resizer:     stringEnv("NFS_RESIZER_IMAGE", "m.daocloud.io/registry.k8s.io/sig-storage/csi-resizer@sha256:a2d40c1c3ccb0c48b467125a6652c4dd5dcbf0d295641c9989581cfc690f6cf3"),
			Liveness:    stringEnv("NFS_LIVENESS_IMAGE", "m.daocloud.io/registry.k8s.io/sig-storage/livenessprobe@sha256:06da0d5b8908072f2e4522692aee8dc119fba7247a9658497e1153992cd777e9"),
			Registrar:   stringEnv("NFS_REGISTRAR_IMAGE", "m.daocloud.io/registry.k8s.io/sig-storage/csi-node-driver-registrar@sha256:f9de845b170155199f2a2a3f9531cf13d78e31235e9db6b6582a8b0db0a50dad"),
			Probe:       stringEnv("NFS_PROBE_IMAGE", "m.daocloud.io/docker.io/library/busybox@sha256:73aaf090f3d85aa34ee199857f03fa3a95c8ede2ffd4cc2cdb5b94e566b11662"),
		},
		VeleroImages: VeleroImages{
			Server:    stringEnv("VELERO_IMAGE", "docker.io/velero/velero@sha256:11459094b1b21ec7c817b08f8067d9e89380835547915cac9c4132ff05b55b90"),
			AWSPlugin: stringEnv("VELERO_AWS_PLUGIN_IMAGE", "docker.io/velero/velero-plugin-for-aws@sha256:7e82f717f44e89671212e0dfce7e061321c386ea84a33bca64a671670ca6c278"),
		},
	}, nil
}

func loadSecurityConfig(environment string) (Security, error) {
	sessionTTL, err := durationEnv("SESSION_TTL", defaultSessionTTL)
	if err != nil {
		return Security{}, err
	}
	cookieSecure, err := boolEnv("COOKIE_SECURE", environment != "development")
	if err != nil {
		return Security{}, err
	}
	keyVersion, err := positiveIntEnv("CREDENTIAL_KEY_VERSION", 1)
	if err != nil {
		return Security{}, err
	}
	return Security{
		AdministratorUsername:   stringEnv("ADMIN_USERNAME", "admin"),
		BootstrapPasswordFile:   stringEnv("ADMIN_BOOTSTRAP_PASSWORD_FILE", "/run/secrets/sks-migration/admin-password"),
		CredentialMasterKeyFile: stringEnv("CREDENTIAL_MASTER_KEY_FILE", "/run/secrets/sks-migration/master-key"),
		CredentialKeyVersion:    keyVersion,
		SessionTTL:              sessionTTL,
		CookieSecure:            cookieSecure,
	}, nil
}

func LoadWorker() (Worker, error) {
	heartbeat, err := durationEnv("WORKER_HEARTBEAT_INTERVAL", defaultWorkerHeartbeat)
	if err != nil {
		return Worker{}, err
	}
	level, err := logLevelEnv()
	if err != nil {
		return Worker{}, err
	}
	poll, err := durationEnv("WORKER_POLL_INTERVAL", defaultWorkerPoll)
	if err != nil {
		return Worker{}, err
	}
	lease, err := durationEnv("WORKER_LEASE_DURATION", defaultWorkerLease)
	if err != nil {
		return Worker{}, err
	}
	retry, err := durationEnv("WORKER_RETRY_DELAY", defaultWorkerRetry)
	if err != nil {
		return Worker{}, err
	}
	if heartbeat >= lease {
		return Worker{}, fmt.Errorf("WORKER_HEARTBEAT_INTERVAL must be shorter than WORKER_LEASE_DURATION")
	}
	keyVersion, err := positiveIntEnv("CREDENTIAL_KEY_VERSION", 1)
	if err != nil {
		return Worker{}, err
	}
	stagingHelperImage := stringEnv("STAGING_HELPER_IMAGE", "m.daocloud.io/docker.io/library/busybox@sha256:73aaf090f3d85aa34ee199857f03fa3a95c8ede2ffd4cc2cdb5b94e566b11662")
	if !strings.Contains(stagingHelperImage, "@sha256:") {
		return Worker{}, errors.New("STAGING_HELPER_IMAGE must be pinned by sha256 digest")
	}
	komposeImage := stringEnv("KOMPOSE_IMAGE", "registry.example.invalid/sks-migration-center/kompose@sha256:0000000000000000000000000000000000000000000000000000000000000000")
	if !strings.Contains(komposeImage, "@sha256:") {
		return Worker{}, errors.New("KOMPOSE_IMAGE must be pinned by sha256 digest")
	}
	kopiaImage := stringEnv("KOPIA_IMAGE", "registry.example.invalid/sks-migration-center/kopia@sha256:0000000000000000000000000000000000000000000000000000000000000000")
	if !strings.Contains(kopiaImage, "@sha256:") {
		return Worker{}, errors.New("KOPIA_IMAGE must be pinned by sha256 digest")
	}

	return Worker{
		Environment:              stringEnv("APP_ENV", defaultEnvironment),
		Version:                  stringEnv("APP_VERSION", defaultVersion),
		LogLevel:                 level,
		HeartbeatInterval:        heartbeat,
		PollInterval:             poll,
		LeaseDuration:            lease,
		RetryDelay:               retry,
		Database:                 databaseConfig(),
		CredentialMasterKeyFile:  stringEnv("CREDENTIAL_MASTER_KEY_FILE", "/run/secrets/sks-migration/master-key"),
		CredentialKeyVersion:     keyVersion,
		StagingHelperImage:       stagingHelperImage,
		KomposeImage:             komposeImage,
		KopiaImage:               kopiaImage,
		MetricsAddr:              stringEnv("WORKER_METRICS_ADDR", ":9090"),
		ComposeImageRepository:   stringEnv("COMPOSE_IMAGE_REPOSITORY", ""),
		RegistryDockerConfigFile: stringEnv("REGISTRY_DOCKER_CONFIG_FILE", ""),
		RegistryPullSecretName:   stringEnv("REGISTRY_PULL_SECRET_NAME", "sks-migration-registry"),
	}, nil
}

func databaseConfig() Database {
	return Database{
		URL:            stringEnv("DATABASE_URL", defaultDatabaseURL),
		MaxConnections: 20,
		MinConnections: 2,
		ConnectTimeout: defaultDatabaseTimeout,
	}
}

func durationEnv(name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", name)
	}
	return value, nil
}

func logLevelEnv() (slog.Level, error) {
	switch strings.ToLower(stringEnv("LOG_LEVEL", "info")) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("LOG_LEVEL must be debug, info, warn, or error")
	}
}

func stringEnv(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}

func boolEnv(name string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be true or false", name)
	}
	return value, nil
}

func positiveIntEnv(name string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return value, nil
}
