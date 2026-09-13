package compose

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestAnalyzeCompleteComposeInventoryWithoutLeakingEnvValues(t *testing.T) {
	composeYAML := []byte(`name: factory
services:
  api:
    image: registry.example/api:${TAG}
    depends_on:
      redis:
        condition: service_healthy
    ports:
      - "8080:8080"
    volumes:
      - uploads:/srv/uploads
      - ./config:/srv/config:ro
    networks: [frontend, backend]
    environment:
      DATABASE_PASSWORD: ${DATABASE_PASSWORD}
  redis:
    image: redis:7
    profiles: [cache]
    expose: ["6379/tcp"]
    volumes:
      - redis-data:/data
volumes:
  uploads: {}
  redis-data: {}
networks:
  frontend: {}
  backend:
    internal: true
`)
	result, err := NewAnalyzer().Analyze(context.Background(), "", composeYAML, []byte("TAG=v1.2.3\nDATABASE_PASSWORD=do-not-leak\n"))
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if result.ProjectName != "migration" || len(result.Services) != 2 || len(result.Volumes) != 2 || len(result.Networks) != 3 {
		t.Fatalf("unexpected inventory: %+v", result)
	}
	if result.Services[0].Image != "registry.example/api:v1.2.3" || len(result.Services[0].DependsOn) != 1 || len(result.Services[0].Mounts) != 2 {
		t.Fatalf("unexpected api service: %+v", result.Services[0])
	}
	if len(result.Services[1].Expose) != 1 || result.Services[1].Expose[0] != "6379/tcp" {
		t.Fatalf("internal exposed ports were not inventoried: %+v", result.Services[1])
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "do-not-leak") {
		t.Fatal(".env value leaked in inventory")
	}
	if len(result.Warnings) != 1 || result.Warnings[0].Code != "COMPOSE_BIND_MOUNT" {
		t.Fatalf("expected bind mount warning: %+v", result.Warnings)
	}
}

func TestAnalyzeDiscoveryFixtureWhenProvided(t *testing.T) {
	path := os.Getenv("SKS_COMPOSE_DISCOVERY_FIXTURE")
	if path == "" {
		t.Skip("SKS_COMPOSE_DISCOVERY_FIXTURE is not set")
	}
	definition, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	result, err := NewAnalyzer().Analyze(context.Background(), "openclaw", definition, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.ProjectName != "openclaw" || len(result.Services) == 0 {
		t.Fatalf("unexpected discovered project: %+v", result)
	}
	foundWarning := false
	for _, warning := range result.Warnings {
		if warning.Code == "COMPOSE_UNRESOLVED_VARIABLES" {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Fatalf("expected unresolved variable warning: %+v", result.Warnings)
	}
}

func TestAnalyzeRejectsServerLocalFileReads(t *testing.T) {
	for name, contents := range map[string]string{
		"include":  "include: [other.yaml]\nservices: {}\n",
		"env file": "services:\n  app:\n    image: app\n    env_file: /etc/passwd\n",
		"secret":   "services: {app: {image: app}}\nsecrets:\n  key:\n    file: /etc/shadow\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NewAnalyzer().Analyze(context.Background(), "test", []byte(contents), nil)
			if !errors.Is(err, ErrInvalidCompose) {
				t.Fatalf("expected invalid compose, got %v", err)
			}
		})
	}
}

func TestAnalyzeKeepsVariableBasedDiscoveredComposeProject(t *testing.T) {
	definition := []byte(`
name: openclaw
services:
  openclaw-gateway:
    image: ${OPENCLAW_IMAGE}
    user: ${OPENCLAW_RUN_USER:-0:0}
    ports:
      - ${OPENCLAW_GATEWAY_PORT}:18789
    volumes:
      - type: volume
        source: ${OPENCLAW_DATA_DIR}
        target: /home/node/.openclaw
      - type: volume
        target: /home/node/.openclaw/extensions
networks:
  default:
    name: openclaw_default
`)
	result, err := NewAnalyzer().Analyze(context.Background(), "openclaw", definition, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.ProjectName != "openclaw" || len(result.Services) != 1 {
		t.Fatalf("unexpected inventory: %+v", result)
	}
	service := result.Services[0]
	if service.Image != "${OPENCLAW_IMAGE}" || len(service.Mounts) != 2 || service.Mounts[0].Source != "${OPENCLAW_DATA_DIR}" {
		t.Fatalf("variable references were not preserved: %+v", service)
	}
}
