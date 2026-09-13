package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadAPIDefaults(t *testing.T) {
	t.Setenv("HTTP_ADDR", "")
	t.Setenv("APP_ENV", "")
	t.Setenv("APP_VERSION", "")
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("SHUTDOWN_TIMEOUT", "")

	cfg, err := LoadAPI()
	if err != nil {
		t.Fatalf("LoadAPI returned an error: %v", err)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Fatalf("expected :8080, got %q", cfg.HTTPAddr)
	}
	if cfg.ShutdownTimeout != 15*time.Second {
		t.Fatalf("expected 15s, got %s", cfg.ShutdownTimeout)
	}
}

func TestLoadWorkerRejectsInvalidHeartbeat(t *testing.T) {
	t.Setenv("WORKER_HEARTBEAT_INTERVAL", "not-a-duration")
	if _, err := LoadWorker(); err == nil {
		t.Fatal("expected invalid heartbeat to return an error")
	}
}

func TestLoadWorkerRejectsHeartbeatAtLeaseDuration(t *testing.T) {
	t.Setenv("WORKER_HEARTBEAT_INTERVAL", "30s")
	t.Setenv("WORKER_LEASE_DURATION", "30s")
	if _, err := LoadWorker(); err == nil {
		t.Fatal("expected heartbeat equal to lease duration to return an error")
	}
}

func TestLoadWorkerLoadsExecutionConfiguration(t *testing.T) {
	t.Setenv("CREDENTIAL_MASTER_KEY_FILE", "/tmp/test-master-key")
	t.Setenv("CREDENTIAL_KEY_VERSION", "3")
	t.Setenv("STAGING_HELPER_IMAGE", "docker.io/library/busybox@sha256:"+strings.Repeat("a", 64))

	cfg, err := LoadWorker()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CredentialMasterKeyFile != "/tmp/test-master-key" || cfg.CredentialKeyVersion != 3 {
		t.Fatalf("unexpected credential configuration: %+v", cfg)
	}
	if !strings.Contains(cfg.StagingHelperImage, "@sha256:") {
		t.Fatalf("expected a digest-pinned staging helper image, got %q", cfg.StagingHelperImage)
	}
	if cfg.MetricsAddr != ":9090" {
		t.Fatalf("unexpected worker metrics address %q", cfg.MetricsAddr)
	}
}

func TestLoadWorkerRejectsUnlockedStagingImage(t *testing.T) {
	t.Setenv("STAGING_HELPER_IMAGE", "docker.io/library/busybox:1.37")
	if _, err := LoadWorker(); err == nil {
		t.Fatal("expected an unlocked staging helper image to fail")
	}
}

func TestLoadAPIAllowsExplicitInsecureCookiesForIsolatedOneTimeDeployment(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("COOKIE_SECURE", "false")
	config, err := LoadAPI()
	if err != nil || config.Security.CookieSecure {
		t.Fatalf("expected explicit insecure cookie setting to be preserved: %+v, %v", config.Security, err)
	}
}
