package migration

import (
	"testing"
	"time"

	domainapplication "github.com/smartx/sks-migration-center/internal/domain/application"
	domainmapping "github.com/smartx/sks-migration-center/internal/domain/mapping"
	domainmigration "github.com/smartx/sks-migration-center/internal/domain/migration"
)

func TestDiagnosisDoesNotEquateFreshHeartbeatWithProgress(t *testing.T) {
	now := time.Now()
	lease := now.Add(time.Minute)
	result := diagnoseRun(domainmigration.Run{Status: domainmigration.RunQuiesce}, nil,
		[]domainmigration.Event{{CreatedAt: now.Add(-18 * time.Minute)}},
		[]domainmigration.StepAttempt{{Status: domainmigration.AttemptRunning, LeaseExpiresAt: &lease}}, now)
	if result.State != "WAITING" || result.Title != "长时间未收到步骤进展" {
		t.Fatalf("unexpected diagnosis: %+v", result)
	}
}

func TestComposeTopologyShowsServicesVolumesNetworksAndGeneratedResources(t *testing.T) {
	application := domainapplication.SourceApplication{SourceType: domainapplication.SourceCompose, Inventory: domainapplication.Inventory{Compose: &domainapplication.ComposeInventory{
		ProjectName: "nacos-demo",
		Services: []domainapplication.ComposeService{
			{Name: "web", Image: "docker.io/demo/web:v1", DependsOn: []string{"nacos", "postgres"}, Ports: []domainapplication.ComposePort{{Target: 8080}}, Mounts: []domainapplication.ComposeMount{{Type: "volume", Source: "data", Target: "/data"}}, Networks: []string{"backend"}},
			{Name: "nacos", Image: "nacos/nacos-server:v2"}, {Name: "postgres", Image: "postgres:16"},
		},
		Volumes:  []domainapplication.ComposeResource{{Name: "data"}, {Name: "pgdata"}},
		Networks: []domainapplication.ComposeResource{{Name: "backend"}},
	}}}
	profile := domainmapping.Profile{Namespaces: []domainmapping.KeyValue{{Source: "nacos-demo", Target: "nacos-demo-migrated"}}, Registries: []domainmapping.KeyValue{{Source: "docker.io", Target: "harbor.local/sks"}}, Storage: []domainmapping.KeyValue{{Source: "local", Target: "smtx-block"}}}
	source, target, mappings := buildComposeTopologies(application, profile, "compose-host", "mw")
	if len(source.Nodes) != 6 {
		t.Fatalf("source nodes=%d, want 3 services + 2 volumes + 1 network", len(source.Nodes))
	}
	if !containsTopologyKind(target.Nodes, "Deployment") || !containsTopologyKind(target.Nodes, "Service") || !containsTopologyKind(target.Nodes, "PersistentVolumeClaim") {
		t.Fatalf("target topology does not show generated resources: %+v", target.Nodes)
	}
	if countTopologyKind(target.Nodes, "Service") != 3 {
		t.Fatalf("target services=%d, want one generated Service per Compose service", countTopologyKind(target.Nodes, "Service"))
	}
	if len(mappings) < 5 || target.Namespace != "nacos-demo-migrated" {
		t.Fatalf("mappings=%d namespace=%s", len(mappings), target.Namespace)
	}
}

func TestComposeTopologyDoesNotClaimUnchangedStorageClassWasMapped(t *testing.T) {
	application := domainapplication.SourceApplication{SourceType: domainapplication.SourceCompose, Inventory: domainapplication.Inventory{Compose: &domainapplication.ComposeInventory{
		ProjectName: "demo", Volumes: []domainapplication.ComposeResource{{Name: "data"}},
	}}}
	profile := domainmapping.Profile{Storage: []domainmapping.KeyValue{{Source: "smtx-elf-csi-driver", Target: "smtx-elf-csi-driver"}}}
	_, target, _ := buildComposeTopologies(application, profile, "source", "target")
	for _, node := range target.Nodes {
		for _, change := range node.MappingChanges {
			if change.Type == "STORAGE_CLASS" {
				t.Fatalf("unchanged storage class must not be reported as a mapping: %+v", change)
			}
		}
	}
}

func TestComposeTopologyNormalizesGeneratedKubernetesNames(t *testing.T) {
	application := domainapplication.SourceApplication{SourceType: domainapplication.SourceCompose, Inventory: domainapplication.Inventory{Compose: &domainapplication.ComposeInventory{
		ProjectName: "nextcloud", Services: []domainapplication.ComposeService{{Name: "web_app"}},
		Volumes: []domainapplication.ComposeResource{{Name: "db_data"}},
	}}}
	_, target, mappings := buildComposeTopologies(application, domainmapping.Profile{}, "source", "target")
	if !hasTopologyResource(target.Nodes, "Deployment", "web-app") || !hasTopologyResource(target.Nodes, "PersistentVolumeClaim", "db-data") {
		t.Fatalf("generated names were not normalized: %+v", target.Nodes)
	}
	foundNameChange := false
	for _, mapping := range mappings {
		for _, change := range mapping.Changes {
			foundNameChange = foundNameChange || change.Type == "RESOURCE_NAME" && change.SourceValue == "db_data" && change.TargetValue == "db-data"
		}
	}
	if !foundNameChange {
		t.Fatal("resource-name normalization must be visible in migration evidence")
	}
}

func TestKubernetesTopologyShowsExplicitSameNameStorageClassMapping(t *testing.T) {
	source := domainmigration.TopologyNode{Kind: "PersistentVolumeClaim", Attributes: map[string]any{"storageClass": "smtx-elf-csi-driver"}}
	target := source
	changes := mappingChangesForNode(source, &target, domainmapping.Profile{Storage: []domainmapping.KeyValue{{Source: "smtx-elf-csi-driver", Target: "smtx-elf-csi-driver"}}})
	if len(changes) != 1 || changes[0].Type != "STORAGE_CLASS" || !changes[0].Changed {
		t.Fatalf("explicit same-name storage mapping must remain visible evidence: %+v", changes)
	}
}

func TestObservedTopologyMarksMissingAndUnappliedMappings(t *testing.T) {
	targetID := topologyNodeID("TARGET", "apps/v1", "Deployment", "shop-new", "api")
	expected := domainmigration.TopologyGraph{Name: "mw", Type: "KUBERNETES", Namespace: "shop-new", Nodes: []domainmigration.TopologyNode{{
		ID: targetID, Side: "TARGET", APIVersion: "apps/v1", Kind: "Deployment", Namespace: "shop-new", Name: "api", Required: true, Status: domainmigration.ResourcePlanned,
		MappingChanges: []domainmigration.MappingChange{{Type: "IMAGE", SourceValue: "docker.io/demo/api:v1", TargetValue: "harbor.local/sks/api:v1", Changed: true}},
	}}}
	observed := domainmigration.TopologyGraph{Name: "mw", Type: "KUBERNETES", Namespace: "shop-new", Nodes: []domainmigration.TopologyNode{{
		ID: targetID, Side: "TARGET", APIVersion: "apps/v1", Kind: "Deployment", Namespace: "shop-new", Name: "api", Status: domainmigration.ResourceCreated,
		Attributes: map[string]any{"images": []string{"docker.io/demo/api:v1"}},
	}}}
	merged, _ := mergeObservedTopology(expected, observed, domainmigration.RunCompleted)
	if merged.Nodes[0].MappingChanges[0].Applied == nil || *merged.Nodes[0].MappingChanges[0].Applied {
		t.Fatalf("registry mapping must be recorded as not applied: %+v", merged.Nodes[0].MappingChanges)
	}
	missing, _ := mergeObservedTopology(expected, domainmigration.TopologyGraph{}, domainmigration.RunCompleted)
	if missing.Nodes[0].Status != domainmigration.ResourceMissing {
		t.Fatalf("missing target status=%s", missing.Nodes[0].Status)
	}
}

func TestObservedTopologyOmitsMigrationHelperResources(t *testing.T) {
	expected := domainmigration.TopologyGraph{Name: "mw", Type: "KUBERNETES", Namespace: "nextcloud"}
	observed := domainmigration.TopologyGraph{Name: "mw", Type: "KUBERNETES", Namespace: "nextcloud", Nodes: []domainmigration.TopologyNode{{
		ID: "helper", Side: "TARGET", Kind: "Job", Namespace: "nextcloud", Name: "smc-kopia-run-volume",
		Attributes: map[string]any{"labels": map[string]string{"app.kubernetes.io/managed-by": "sks-migration-center"}},
	}}}
	merged, drifted := mergeObservedTopology(expected, observed, domainmigration.RunCompleted)
	if len(merged.Nodes) != 0 || drifted != 0 {
		t.Fatalf("migration helper leaked into application topology: nodes=%+v drifted=%d", merged.Nodes, drifted)
	}
}

func containsTopologyKind(nodes []domainmigration.TopologyNode, kind string) bool {
	for _, node := range nodes {
		if node.Kind == kind {
			return true
		}
	}
	return false
}

func countTopologyKind(nodes []domainmigration.TopologyNode, kind string) int {
	count := 0
	for _, node := range nodes {
		if node.Kind == kind {
			count++
		}
	}
	return count
}

func hasTopologyResource(nodes []domainmigration.TopologyNode, kind, name string) bool {
	for _, node := range nodes {
		if node.Kind == kind && node.Name == name {
			return true
		}
	}
	return false
}
