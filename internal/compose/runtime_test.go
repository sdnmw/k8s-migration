package compose

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const runtimeFixture = `services:
  n8n:
    image: n8nio/n8n:latest
    ports: ["127.0.0.1:5678:5678"]
    volumes: ["n8n_data:/home/node/.n8n", "/srv/n8n/local-files:/files"]
    labels:
      traefik.enable: "true"
      traefik.http.routers.n8n.rule: "Host(` + "`n8n.example.test`" + `)"
      traefik.http.routers.n8n.entrypoints: web,websecure
      traefik.http.routers.n8n.middlewares: n8n@docker
      traefik.http.routers.n8n.tls: "true"
      traefik.http.routers.n8n.tls.certresolver: mytlschallenge
      traefik.http.middlewares.n8n.headers.SSLRedirect: "true"
      traefik.http.middlewares.n8n.headers.STSSeconds: "315360000"
      traefik.http.middlewares.n8n.headers.STSIncludeSubdomains: "true"
  traefik:
    image: registry.test:5000/traefik:v3.3
    command:
      - --providers.docker=true
      - --providers.docker.exposedbydefault=false
      - --entrypoints.web.address=:80
      - --entrypoints.websecure.address=:443
      - --certificatesresolvers.mytlschallenge.acme.storage=/letsencrypt/acme.json
    volumes: ["/var/run/docker.sock:/var/run/docker.sock:ro", "traefik_data:/letsencrypt"]
volumes: {n8n_data: {}, traefik_data: {}}
`

func TestTargetRuntimePreservesDataAndRoutesWithoutChangingSource(t *testing.T) {
	source := []byte(runtimeFixture)
	before := bytes.Clone(source)
	target, err := PrepareTargetRuntime(context.Background(), "n8n-compose", source, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(source, before) || len(target.Proxies) != 1 {
		t.Fatal("source changed or adapter missing")
	}
	if strings.Contains(string(target.ComposeYAML), "docker.sock") || !strings.Contains(string(target.ComposeYAML), "providers.docker=false") || !strings.Contains(string(target.ComposeYAML), "providers.file.filename") {
		t.Fatalf("socket/provider not adapted: %s", target.ComposeYAML)
	}
	for _, s := range []string{"n8n_data", "traefik_data", "/letsencrypt", "/srv/n8n/local-files", "/home/node/.n8n", "/files", "acme.storage"} {
		if !strings.Contains(string(target.ComposeYAML), s) {
			t.Fatalf("source data/config lost: %s", s)
		}
	}
	var config map[string]any
	if err := yaml.Unmarshal(target.Proxies[0].Configuration, &config); err != nil {
		t.Fatal(err)
	}
	http := config["http"].(map[string]any)
	router := http["routers"].(map[string]any)["n8n"].(map[string]any)
	if router["service"] != "n8n" || router["tls"].(map[string]any)["certResolver"] != "mytlschallenge" {
		t.Fatal("TLS/backend lost")
	}
	if !strings.Contains(string(target.Proxies[0].Configuration), "http://n8n:5678") || strings.Contains(string(target.Proxies[0].Configuration), "@docker") || target.Proxies[0].BackendPorts["n8n"] != 5678 {
		t.Fatal("Docker routing not replaced by Service routing")
	}
	headers := http["middlewares"].(map[string]any)["n8n"].(map[string]any)["headers"].(map[string]any)
	if headers["sslRedirect"] != true || headers["stsSeconds"] != 315360000 {
		t.Fatalf("typed headers missing: %#v", headers)
	}
	inv, err := NewAnalyzer().Analyze(context.Background(), "n8n-compose", source, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range inv.Warnings {
		if w.Code == "COMPOSE_RUNTIME_ADAPTED" {
			found = true
		}
	}
	if !found {
		t.Fatal("assessment adapter warning missing")
	}
}

func TestTargetRuntimeRejectsUnsupportedDependenciesWithoutLeakingValues(t *testing.T) {
	for name, input := range map[string]string{
		"unknown socket consumer":     `services: {app: {image: app:v1, volumes: ["/var/run/docker.sock:/var/run/docker.sock"]}}`,
		"implicit routes":             strings.Replace(runtimeFixture, "--providers.docker.exposedbydefault=false", "--providers.docker.exposedbydefault=true", 1),
		"TCP route":                   strings.Replace(runtimeFixture, "traefik.http.routers.n8n.rule", "traefik.tcp.routers.n8n.rule", 1),
		"unsupported auth middleware": strings.Replace(runtimeFixture, "traefik.http.middlewares.n8n.headers.SSLRedirect: \"true\"", "traefik.http.middlewares.n8n.basicAuth.users: \"DO-NOT-LEAK\"", 1),
		"missing port":                strings.Replace(runtimeFixture, "ports: [\"127.0.0.1:5678:5678\"]", "ports: []", 1),
		"config collision":            strings.Replace(runtimeFixture, "--providers.docker=true", "--configFile=/etc/traefik.yaml", 1),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := PrepareTargetRuntime(context.Background(), "demo", []byte(input), nil)
			var permanent *RuntimeDependencyError
			if !errors.As(err, &permanent) || !permanent.Permanent() {
				t.Fatalf("expected permanent dependency error: %v", err)
			}
			if strings.Contains(err.Error(), "DO-NOT-LEAK") {
				t.Fatal("label value leaked")
			}
		})
	}
}

func TestTargetRuntimeLeavesOrdinaryComposeUnchanged(t *testing.T) {
	for _, source := range []string{`services: {db: {image: postgres:18, volumes: ["data:/data"]}}
volumes: {data: {}}`, `services: {app: {image: app, volumes: ["/srv/example.sock:/data/file.sock"]}}`} {
		result, err := PrepareTargetRuntime(context.Background(), "demo", []byte(source), nil)
		if err != nil || string(result.ComposeYAML) != source || len(result.Proxies) != 0 {
			t.Fatalf("ordinary Compose changed: %v", err)
		}
	}
	if !IsRuntimeSocket("/run/containerd/containerd.sock", "") || IsRuntimeSocket("/srv/database.sock", "") {
		t.Fatal("incorrect socket classification")
	}
}

func TestTargetRuntimePreservesExplicitBackendPortAndExpandsEnvironment(t *testing.T) {
	source := strings.Replace(runtimeFixture, "Host(`n8n.example.test`)", "Host(`${APP_HOST}`)", 1)
	source = strings.Replace(source, "traefik.http.routers.n8n.rule:", "traefik.http.services.custom.loadbalancer.server.port: \"5678\"\n      traefik.http.routers.n8n.service: custom\n      traefik.http.routers.n8n.rule:", 1)
	result, err := PrepareTargetRuntime(context.Background(), "demo", []byte(source), []byte("APP_HOST=resolved.example.test\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result.Proxies[0].Configuration), "resolved.example.test") || !strings.Contains(string(result.Proxies[0].Configuration), "custom:") {
		t.Fatal("environment/backend resolution missing")
	}
}
