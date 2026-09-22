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

func TestDiagnosisTerminalFailureOverridesHistoricalScheduledRetry(t *testing.T) {
	now := time.Now()
	result := diagnoseRun(domainmigration.Run{Status: domainmigration.RunFailed, ErrorMessage: "Deployment guestbook/frontend is not ready"},
		[]domainmigration.Step{{Status: domainmigration.StepFailed, Type: domainmigration.StepValidation}}, nil,
		[]domainmigration.StepAttempt{
			{Status: domainmigration.AttemptRetryScheduled, ErrorMessage: "等待重试"},
			{Status: domainmigration.AttemptFailed, ErrorMessage: "Deployment guestbook/frontend is not ready"},
		}, now)
	if result.State != "FAILED" || result.Title != "迁移执行已失败" {
		t.Fatalf("terminal failure was overwritten by retry evidence: %+v", result)
	}
	if result.Reason != "Deployment guestbook/frontend is not ready" {
		t.Fatalf("unexpected terminal failure reason: %+v", result)
	}
}

func TestDiagnosisUsesValidationAsSuccessfulStepAfterReconciliation(t *testing.T) {
	result := diagnoseRun(domainmigration.Run{Status: domainmigration.RunCompleted, ErrorCode: "VALIDATION_RECONCILED"},
		[]domainmigration.Step{
			{Status: domainmigration.StepSucceeded, Type: domainmigration.StepValidation},
			{Status: domainmigration.StepSucceeded, Type: domainmigration.StepRollback},
		}, nil, nil, time.Now())
	if result.State != "SUCCEEDED" || result.Title != "迁移成功" || result.LastSuccessfulStep != string(domainmigration.StepValidation) {
		t.Fatalf("unexpected reconciled diagnosis: %+v", result)
	}
}

func TestValidationFailureCanBeReconciledWhenEveryRequiredResourceIsHealthy(t *testing.T) {
	run := domainmigration.Run{Status: domainmigration.RunFailed}
	steps := []domainmigration.Step{
		{Type: domainmigration.StepValidation, Status: domainmigration.StepFailed},
		{Type: domainmigration.StepRollback, Status: domainmigration.StepSucceeded},
	}
	graph := domainmigration.TopologyGraph{Nodes: []domainmigration.TopologyNode{
		{Kind: "Deployment", Name: "frontend", Required: true, Status: domainmigration.ResourceSucceeded},
		{Kind: "Service", Name: "frontend", Required: true, Status: domainmigration.ResourceSucceeded},
		{Kind: "ConfigMap", Name: "kube-root-ca.crt", Required: false, Status: domainmigration.ResourceSkipped},
	}}
	if !validationFailureCanBeReconciled(run, steps, graph) {
		t.Fatal("healthy current observation should reconcile a validation-only false negative")
	}
	graph.Nodes[0].Status = domainmigration.ResourceFailed
	if validationFailureCanBeReconciled(run, steps, graph) {
		t.Fatal("failed required resource must not reconcile the run")
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

func TestKubernetesTopologyMapsStorageClassDependencyToTargetClass(t *testing.T) {
	sourceClassID := topologyNodeID("SOURCE", "storage.k8s.io/v1", "StorageClass", "", "legacy-nfs")
	sourcePVCID := topologyNodeID("SOURCE", "v1", "PersistentVolumeClaim", "demo", "data")
	source := domainmigration.TopologyGraph{Name: "sida", Type: "KUBERNETES", Namespace: "demo", Nodes: []domainmigration.TopologyNode{
		{ID: sourceClassID, Side: "SOURCE", APIVersion: "storage.k8s.io/v1", Kind: "StorageClass", Name: "legacy-nfs", Required: true, Status: domainmigration.ResourceDiscovered},
		{ID: sourcePVCID, Side: "SOURCE", APIVersion: "v1", Kind: "PersistentVolumeClaim", Namespace: "demo", Name: "data", Required: true, Status: domainmigration.ResourceDiscovered, Attributes: map[string]any{"storageClass": "legacy-nfs"}},
	}, Edges: []domainmigration.TopologyEdge{{ID: "uses-sc", From: sourcePVCID, To: sourceClassID, Relation: "USES_STORAGE_CLASS", Required: true}}}

	target, mappings := buildKubernetesTarget(source, domainmapping.Profile{Storage: []domainmapping.KeyValue{{Source: "legacy-nfs", Target: "shared-nfs"}}}, "mw")
	if !hasTopologyResource(target.Nodes, "StorageClass", "shared-nfs") || hasTopologyResource(target.Nodes, "StorageClass", "legacy-nfs") {
		t.Fatalf("target StorageClass dependency was not mapped: %+v", target.Nodes)
	}
	targetClassID := topologyNodeID("TARGET", "storage.k8s.io/v1", "StorageClass", "", "shared-nfs")
	if len(target.Edges) != 1 || target.Edges[0].To != targetClassID {
		t.Fatalf("PVC dependency does not point at the mapped StorageClass: %+v", target.Edges)
	}
	found := false
	for _, mapping := range mappings {
		if mapping.SourceNodeID != sourceClassID || mapping.TargetNodeID != targetClassID {
			continue
		}
		found = len(mapping.Changes) == 1 && mapping.Changes[0].Type == "STORAGE_CLASS" && mapping.Changes[0].TargetValue == "shared-nfs"
	}
	if !found {
		t.Fatalf("StorageClass mapping evidence missing: %+v", mappings)
	}

	observed := domainmigration.TopologyGraph{Nodes: []domainmigration.TopologyNode{{ID: targetClassID, Side: "TARGET", APIVersion: "storage.k8s.io/v1", Kind: "StorageClass", Name: "shared-nfs"}}}
	merged, drifted := mergeObservedTopology(target, observed, domainmigration.RunCompleted)
	for _, node := range merged.Nodes {
		if node.Kind == "StorageClass" && (node.Status != domainmigration.ResourceSucceeded || node.MappingChanges[0].Applied == nil || !*node.MappingChanges[0].Applied) {
			t.Fatalf("mapped StorageClass must validate against its target name: %+v", node)
		}
	}
	if drifted != 1 { // The PVC is intentionally absent from this focused observation.
		t.Fatalf("unexpected drift count: %d", drifted)
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
