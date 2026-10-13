package migration

import (
	"strings"
	"testing"

	composeanalyzer "github.com/smartx/sks-migration-center/internal/compose"
	"github.com/smartx/sks-migration-center/internal/domain/application"
	"github.com/smartx/sks-migration-center/internal/domain/mapping"
	"github.com/smartx/sks-migration-center/internal/transform"
)

func TestComposeRuntimeSocketNeverCreatesTransferOrSharedClaim(t *testing.T) {
	inv := &application.ComposeInventory{Services: []application.ComposeService{{Name: "traefik", Mounts: []application.ComposeMount{{Type: "bind", Source: "/var/run/docker.sock", Target: "/var/run/docker.sock", ReadOnly: true}}}}}
	result := transform.Result{}
	if err := normalizeSharedComposeClaims(&result, inv); err != nil {
		t.Fatal(err)
	}
	transfers, err := composeTransfers(inv, result)
	if err != nil || len(transfers) != 0 {
		t.Fatalf("socket treated as data: %+v %v", transfers, err)
	}
	if hasComposeData(application.SourceApplication{Inventory: application.Inventory{Compose: inv}}) {
		t.Fatal("socket-only project requires backup")
	}
}

func TestComposeRuntimeInjectsOfflineConfigAndBackendService(t *testing.T) {
	engine := transform.NewEngine()
	result, err := engine.Transform([]byte("apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: traefik}\nspec: {template: {spec: {containers: [{name: traefik, image: traefik}]}}}\n"), mapping.Profile{})
	if err != nil {
		t.Fatal(err)
	}
	runtime := composeanalyzer.TargetRuntime{Proxies: []composeanalyzer.TraefikRuntime{{Service: "traefik", ConfigName: "traefik-migration-routing", Configuration: []byte("http: {}"), BackendPorts: map[string]int{"n8n": 5678}}}}
	if err := applyComposeRuntime(&result, runtime, "demo"); err != nil {
		t.Fatal(err)
	}
	encoded, err := engine.Render(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"kind: ConfigMap", "dynamic.yaml", "/run/sks-migration-traefik", "readOnly: true", "kind: Service", "port: 5678", "traefik-file-provider"} {
		if !strings.Contains(string(encoded), expected) {
			t.Fatalf("missing %s in %s", expected, encoded)
		}
	}
	if strings.Contains(string(encoded), "hostPath:") {
		t.Fatal("socket exposed via hostPath")
	}
}
