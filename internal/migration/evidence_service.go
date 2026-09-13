package migration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	domainapplication "github.com/smartx/sks-migration-center/internal/domain/application"
	domainenvironment "github.com/smartx/sks-migration-center/internal/domain/environment"
	domainmapping "github.com/smartx/sks-migration-center/internal/domain/mapping"
	domainmigration "github.com/smartx/sks-migration-center/internal/domain/migration"
	"github.com/smartx/sks-migration-center/internal/repository"
)

type InventoryDiscoverer interface {
	DiscoverNamespace(context.Context, []byte, string) (domainapplication.Inventory, error)
}

type EvidenceService struct {
	plans        repository.MigrationPlanRepository
	runs         repository.MigrationRunRepository
	progress     repository.MigrationProgressRepository
	evidence     repository.MigrationEvidenceRepository
	applications repository.ApplicationRepository
	mappings     repository.MappingRepository
	environments repository.EnvironmentRepository
	vault        ExecutionVault
	kubernetes   InventoryDiscoverer
	clock        func() time.Time
}

func NewEvidenceService(plans repository.MigrationPlanRepository, runs repository.MigrationRunRepository,
	progress repository.MigrationProgressRepository, evidence repository.MigrationEvidenceRepository,
	applications repository.ApplicationRepository, mappings repository.MappingRepository,
	environments repository.EnvironmentRepository, vault ExecutionVault, kubernetes InventoryDiscoverer) (*EvidenceService, error) {
	if plans == nil || runs == nil || progress == nil || evidence == nil || applications == nil || mappings == nil || environments == nil || vault == nil || kubernetes == nil {
		return nil, errors.New("migration evidence dependencies are required")
	}
	return &EvidenceService{plans: plans, runs: runs, progress: progress, evidence: evidence,
		applications: applications, mappings: mappings, environments: environments, vault: vault,
		kubernetes: kubernetes, clock: func() time.Time { return time.Now().UTC() }}, nil
}

func (s *EvidenceService) Topology(ctx context.Context, runID uuid.UUID) (domainmigration.TopologyEvidence, error) {
	if runID == uuid.Nil {
		return domainmigration.TopologyEvidence{}, fmt.Errorf("%w: runId is required", ErrInvalidInput)
	}
	stored, found, terminal, err := s.evidence.GetTopologyEvidence(ctx, runID)
	if err != nil {
		return domainmigration.TopologyEvidence{}, err
	}
	if found && terminal {
		return stored, nil
	}
	run, plan, application, profile, source, target, err := s.resolve(ctx, runID)
	if err != nil {
		return domainmigration.TopologyEvidence{}, err
	}
	value := stored
	if !found {
		value = buildTopologyEvidence(run, plan, application, profile, source, target, s.clock())
	}
	observation, observeErr := s.observeTarget(ctx, run, plan, application, profile, target, value.Target)
	if observeErr == nil {
		value.Target = observation.Graph
		if events, eventErr := allEvents(ctx, s.runs, runID); eventErr == nil {
			applyEventEvidence(&value.Target, events)
			observation.Graph = value.Target
		}
		value.TargetCapturedAt = &observation.CheckedAt
		value.CurrentObservation = &observation
	} else {
		message := "暂时无法检查目标资源，请检查目标集群连接；当前展示已保存的迁移证据。"
		seen := map[string]bool{}
		limitations := []string{}
		for _, item := range append(value.EvidenceLimitations, message) {
			if item == "目标集群当前不可达，目标状态依据持久化迁移事件推断" {
				continue
			}
			if !seen[item] {
				limitations = append(limitations, item)
				seen[item] = true
			}
		}
		value.EvidenceLimitations = limitations
	}
	terminal = domainmigration.IsTerminal(run.Status)
	if err := s.evidence.SaveTopologyEvidence(ctx, value, terminal); err != nil {
		return domainmigration.TopologyEvidence{}, err
	}
	return value, nil
}

func applyEventEvidence(graph *domainmigration.TopologyGraph, events []domainmigration.Event) {
	for _, event := range events {
		if event.Type != "TARGET_RESOURCE_VALIDATION_COLLECTED" && event.Type != "COMPOSE_RESOURCE_VALIDATION_COLLECTED" {
			continue
		}
		values, _ := event.Detail["results"].([]any)
		for _, raw := range values {
			item, _ := raw.(map[string]any)
			kind, _ := item["kind"].(string)
			name, _ := item["name"].(string)
			namespace, _ := item["namespace"].(string)
			status, _ := item["status"].(string)
			message, _ := item["message"].(string)
			for index := range graph.Nodes {
				node := &graph.Nodes[index]
				if node.Kind != kind || node.Name != name || (namespace != "" && node.Namespace != namespace) {
					continue
				}
				switch status {
				case "SUCCEEDED":
					node.Status, node.Health = domainmigration.ResourceSucceeded, "HEALTHY"
				case "WARNING":
					node.Status, node.Health = domainmigration.ResourceWarning, "DEGRADED"
				case "FAILED":
					node.Status, node.Health = domainmigration.ResourceFailed, "DEGRADED"
				}
				if message != "" {
					node.Message = message
				}
			}
		}
	}
	for index := range graph.Edges {
		graph.Edges[index].Status = edgeStatus(graph.Edges[index], graph.Nodes)
	}
}

func (s *EvidenceService) CaptureInitial(ctx context.Context, runID uuid.UUID) error {
	run, plan, application, profile, source, target, err := s.resolve(ctx, runID)
	if err != nil {
		return err
	}
	value := buildTopologyEvidence(run, plan, application, profile, source, target, s.clock())
	value.SnapshotOrigin = "NATIVE"
	value.EvidenceLimitations = nil
	return s.evidence.SaveTopologyEvidence(ctx, value, false)
}

func (s *EvidenceService) RefreshTopology(ctx context.Context, runID uuid.UUID) (domainmigration.TopologyEvidence, error) {
	value, err := s.Topology(ctx, runID)
	if err != nil {
		return domainmigration.TopologyEvidence{}, err
	}
	run, plan, application, profile, source, target, err := s.resolve(ctx, runID)
	if err != nil {
		return domainmigration.TopologyEvidence{}, err
	}
	// Rebuild the expectation from the immutable source inventory and mapping
	// profile. A previous observation can contain helper resources or legacy
	// pre-normalized Compose names and must not become the next expectation.
	rebuilt := buildTopologyEvidence(run, plan, application, profile, source, target, s.clock())
	expected := rebuilt.Target
	observation, observeErr := s.observeTarget(ctx, run, plan, application, profile, target, expected)
	if observeErr != nil {
		observation = domainmigration.CurrentTopologyObservation{CheckedAt: s.clock(), Error: observeErr.Error(), Graph: value.Target}
	} else {
		value.EvidenceLimitations = withoutTemporaryObservationLimit(value.EvidenceLimitations)
		// Legacy terminal snapshots may still point at pre-normalized Compose
		// resource names (for example db_data instead of db-data). Keep the
		// immutable snapshot intact, but return mappings rebuilt from the frozen
		// source inventory for this live observation.
		value.Mappings = rebuilt.Mappings
	}
	if err := s.evidence.SaveCurrentTopologyObservation(ctx, runID, observation); err != nil {
		return domainmigration.TopologyEvidence{}, err
	}
	value.CurrentObservation = &observation
	return value, nil
}

func (s *EvidenceService) Timeline(ctx context.Context, runID uuid.UUID) (domainmigration.StepTimeline, error) {
	run, err := s.runs.GetRun(ctx, runID)
	if err != nil {
		return domainmigration.StepTimeline{}, err
	}
	steps, err := s.runs.ListSteps(ctx, runID)
	if err != nil {
		return domainmigration.StepTimeline{}, err
	}
	events, err := allEvents(ctx, s.runs, runID)
	if err != nil {
		return domainmigration.StepTimeline{}, err
	}
	attempts, err := s.evidence.ListStepAttempts(ctx, runID)
	if err != nil {
		return domainmigration.StepTimeline{}, err
	}
	byStepAttempts := map[uuid.UUID][]domainmigration.StepAttempt{}
	for _, attempt := range attempts {
		byStepAttempts[attempt.StepID] = append(byStepAttempts[attempt.StepID], attempt)
	}
	byStepEvents := map[uuid.UUID][]domainmigration.Event{}
	for _, event := range events {
		if raw, ok := event.Detail["stepId"].(string); ok {
			if stepID, parseErr := uuid.Parse(raw); parseErr == nil {
				byStepEvents[stepID] = append(byStepEvents[stepID], event)
			}
		}
	}
	values := make([]domainmigration.TimelineStep, 0, len(steps))
	for _, step := range steps {
		stepAttempts := byStepAttempts[step.ID]
		if len(stepAttempts) == 0 && step.StartedAt != nil {
			status := domainmigration.AttemptRunning
			if step.Status == domainmigration.StepSucceeded || step.Status == domainmigration.StepSkipped {
				status = domainmigration.AttemptSucceeded
			} else if step.Status == domainmigration.StepFailed {
				status = domainmigration.AttemptFailed
			}
			stepAttempts = []domainmigration.StepAttempt{{StepID: step.ID, Attempt: step.Attempt, Status: status,
				StartedAt: *step.StartedAt, CompletedAt: step.CompletedAt, ErrorMessage: step.Summary,
				Diagnostic: "历史任务未保存逐次 Worker 心跳"}}
		}
		values = append(values, domainmigration.TimelineStep{Step: step, Attempts: stepAttempts, Events: byStepEvents[step.ID]})
	}
	return domainmigration.StepTimeline{RunID: runID, GeneratedAt: s.clock(), Steps: values, Events: events, Diagnosis: diagnoseRun(run, steps, events, attempts, s.clock())}, nil
}

func (s *EvidenceService) resolve(ctx context.Context, runID uuid.UUID) (domainmigration.Run, domainmigration.Plan,
	domainapplication.SourceApplication, domainmapping.Profile, domainenvironment.Environment, domainenvironment.Environment, error) {
	run, err := s.runs.GetRun(ctx, runID)
	if err != nil {
		return domainmigration.Run{}, domainmigration.Plan{}, domainapplication.SourceApplication{}, domainmapping.Profile{}, domainenvironment.Environment{}, domainenvironment.Environment{}, err
	}
	plan, err := s.plans.GetPlan(ctx, run.PlanID)
	if err != nil {
		return run, domainmigration.Plan{}, domainapplication.SourceApplication{}, domainmapping.Profile{}, domainenvironment.Environment{}, domainenvironment.Environment{}, err
	}
	application, err := s.applications.Get(ctx, plan.SourceApplicationID)
	if err != nil {
		return run, plan, domainapplication.SourceApplication{}, domainmapping.Profile{}, domainenvironment.Environment{}, domainenvironment.Environment{}, err
	}
	profile, err := s.mappings.Get(ctx, plan.MappingProfileID)
	if err != nil {
		return run, plan, application, domainmapping.Profile{}, domainenvironment.Environment{}, domainenvironment.Environment{}, err
	}
	source, err := s.environments.Get(ctx, plan.SourceEnvironmentID)
	if err != nil {
		return run, plan, application, profile, domainenvironment.Environment{}, domainenvironment.Environment{}, err
	}
	target, err := s.environments.Get(ctx, plan.TargetEnvironmentID)
	return run, plan, application, profile, source, target, err
}

func (s *EvidenceService) observeTarget(ctx context.Context, run domainmigration.Run, _ domainmigration.Plan,
	application domainapplication.SourceApplication, profile domainmapping.Profile, target domainenvironment.Environment,
	expected domainmigration.TopologyGraph) (domainmigration.CurrentTopologyObservation, error) {
	if target.CredentialID == nil {
		return domainmigration.CurrentTopologyObservation{}, errors.New("目标集群 kubeconfig 不可用")
	}
	credential, err := s.vault.Resolve(ctx, *target.CredentialID)
	if err != nil {
		return domainmigration.CurrentTopologyObservation{}, err
	}
	defer wipeBytes(credential)
	namespace := mappedValue(application.Namespace, profile.Namespaces)
	if expected.Namespace != "" {
		namespace = expected.Namespace
	}
	if application.SourceType == domainapplication.SourceCompose && application.Inventory.Compose != nil {
		namespace = mappedValue(application.Inventory.Compose.ProjectName, profile.Namespaces)
		if namespace == application.Inventory.Compose.ProjectName {
			namespace = strings.ToLower(strings.ReplaceAll(application.Inventory.Compose.ProjectName, "_", "-"))
		}
	}
	inventory, err := s.kubernetes.DiscoverNamespace(ctx, credential, namespace)
	if err != nil {
		return domainmigration.CurrentTopologyObservation{}, err
	}
	observed := graphFromKubernetesInventory("TARGET", target.Name, namespace, inventory, domainmigration.ResourceCreated)
	graph, drifted := mergeObservedTopology(expected, observed, run.Status)
	return domainmigration.CurrentTopologyObservation{Graph: graph, CheckedAt: s.clock(), Drifted: drifted}, nil
}

func buildTopologyEvidence(run domainmigration.Run, _ domainmigration.Plan, application domainapplication.SourceApplication,
	profile domainmapping.Profile, source, target domainenvironment.Environment, now time.Time) domainmigration.TopologyEvidence {
	var sourceGraph, targetGraph domainmigration.TopologyGraph
	var mappings []domainmigration.ResourceMapping
	if application.SourceType == domainapplication.SourceCompose && application.Inventory.Compose != nil {
		sourceGraph, targetGraph, mappings = buildComposeTopologies(application, profile, source.Name, target.Name)
	} else {
		sourceGraph = graphFromKubernetesInventory("SOURCE", source.Name, application.Namespace, application.Inventory, domainmigration.ResourceDiscovered)
		targetGraph, mappings = buildKubernetesTarget(sourceGraph, profile, target.Name)
	}
	return domainmigration.TopologyEvidence{RunID: run.ID, Source: sourceGraph, Target: targetGraph, Mappings: mappings,
		SnapshotOrigin: "RECONSTRUCTED", SourceCapturedAt: now,
		EvidenceLimitations: []string{"历史任务按已保存的应用清单、映射配置和目标当前状态补建；逐资源执行时刻不可恢复"}}
}

func graphFromKubernetesInventory(side, name, namespace string, inventory domainapplication.Inventory, status domainmigration.ResourceMigrationStatus) domainmigration.TopologyGraph {
	graph := domainmigration.TopologyGraph{Name: name, Type: "KUBERNETES", Namespace: namespace, Nodes: []domainmigration.TopologyNode{}, Edges: []domainmigration.TopologyEdge{}}
	for _, resource := range inventory.Resources {
		attributes := map[string]any{}
		if len(resource.Images) > 0 {
			attributes["images"] = resource.Images
		}
		if resource.Replicas != nil {
			attributes["replicas"] = *resource.Replicas
		}
		if resource.ReadyReplicas != nil {
			attributes["readyReplicas"] = *resource.ReadyReplicas
		}
		if resource.AvailableReplicas != nil {
			attributes["availableReplicas"] = *resource.AvailableReplicas
		}
		if resource.ServiceType != "" {
			attributes["serviceType"] = resource.ServiceType
		}
		if resource.IngressClassName != "" {
			attributes["ingressClass"] = resource.IngressClassName
		}
		if len(resource.DataKeys) > 0 {
			attributes["dataKeys"] = resource.DataKeys
		}
		if len(resource.SecretKeys) > 0 {
			attributes["secretKeys"] = resource.SecretKeys
		}
		if len(resource.NodeSelectors) > 0 {
			attributes["nodeSelectors"] = resource.NodeSelectors
		}
		if len(resource.NFSSources) > 0 {
			attributes["nfsSources"] = resource.NFSSources
		}
		if len(resource.Labels) > 0 {
			attributes["labels"] = resource.Labels
		}
		graph.Nodes = append(graph.Nodes, domainmigration.TopologyNode{ID: topologyNodeID(side, resource.APIVersion, resource.Kind, resource.Namespace, resource.Name), Side: side,
			APIVersion: resource.APIVersion, Kind: resource.Kind, Namespace: resource.Namespace, Name: resource.Name, Required: true, Status: status, Attributes: attributes})
	}
	for _, volume := range inventory.PVCs {
		id := topologyNodeID(side, "v1", "PersistentVolumeClaim", volume.Namespace, volume.Name)
		if hasNode(graph.Nodes, id) {
			for index := range graph.Nodes {
				if graph.Nodes[index].ID != id {
					continue
				}
				graph.Nodes[index].Attributes = mergeAttributes(graph.Nodes[index].Attributes, map[string]any{
					"storageClass":  volume.StorageClassName,
					"capacityBytes": volume.CapacityBytes,
					"accessModes":   volume.AccessModes,
					"volumeMode":    volume.VolumeMode,
					"phase":         volume.Phase,
				})
				break
			}
			continue
		}
		graph.Nodes = append(graph.Nodes, domainmigration.TopologyNode{ID: id, Side: side, APIVersion: "v1", Kind: "PersistentVolumeClaim", Namespace: volume.Namespace,
			Name: volume.Name, Required: true, Status: status, Attributes: map[string]any{"storageClass": volume.StorageClassName, "capacityBytes": volume.CapacityBytes, "accessModes": volume.AccessModes, "volumeMode": volume.VolumeMode, "phase": volume.Phase}})
	}
	for _, dependency := range inventory.Dependencies {
		from := topologyNodeID(side, dependency.From.APIVersion, dependency.From.Kind, dependency.From.Namespace, dependency.From.Name)
		to := topologyNodeID(side, dependency.To.APIVersion, dependency.To.Kind, dependency.To.Namespace, dependency.To.Name)
		if !hasNode(graph.Nodes, to) {
			graph.Nodes = append(graph.Nodes, domainmigration.TopologyNode{ID: to, Side: side, APIVersion: dependency.To.APIVersion, Kind: dependency.To.Kind,
				Namespace: dependency.To.Namespace, Name: dependency.To.Name, Required: dependency.Required, Status: status})
		}
		graph.Edges = append(graph.Edges, domainmigration.TopologyEdge{ID: from + ">" + dependency.Type + ">" + to, From: from, To: to,
			Relation: dependency.Type, Required: dependency.Required, Status: status})
	}
	sortGraph(&graph)
	return graph
}

func buildKubernetesTarget(source domainmigration.TopologyGraph, profile domainmapping.Profile, targetName string) (domainmigration.TopologyGraph, []domainmigration.ResourceMapping) {
	target := domainmigration.TopologyGraph{Name: targetName, Type: "KUBERNETES", Namespace: mappedValue(source.Namespace, profile.Namespaces), Nodes: []domainmigration.TopologyNode{}, Edges: []domainmigration.TopologyEdge{}}
	idMap := map[string]string{}
	mappings := make([]domainmigration.ResourceMapping, 0, len(source.Nodes))
	for _, node := range source.Nodes {
		targetNode := node
		targetNode.Side = "TARGET"
		targetNode.Namespace = mappedValue(node.Namespace, profile.Namespaces)
		targetNode.APIVersion = targetAPIVersion(node.APIVersion, node.Kind)
		targetNode.Status = domainmigration.ResourcePlanned
		targetNode.Health, targetNode.Message = "", "等待目标创建"
		targetNode.MappingChanges = mappingChangesForNode(node, &targetNode, profile)
		targetNode.ID = topologyNodeID("TARGET", targetNode.APIVersion, targetNode.Kind, targetNode.Namespace, targetNode.Name)
		idMap[node.ID] = targetNode.ID
		target.Nodes = append(target.Nodes, targetNode)
		mappings = append(mappings, domainmigration.ResourceMapping{ID: "map|" + node.ID, SourceNodeID: node.ID, TargetNodeID: targetNode.ID, Relation: "MIGRATES_TO", Changes: targetNode.MappingChanges})
	}
	for _, edge := range source.Edges {
		from, fromOK := idMap[edge.From]
		to, toOK := idMap[edge.To]
		if !fromOK || !toOK {
			continue
		}
		target.Edges = append(target.Edges, domainmigration.TopologyEdge{ID: "target|" + edge.ID, From: from, To: to, Relation: edge.Relation, Required: edge.Required, Status: domainmigration.ResourcePlanned})
	}
	sortGraph(&target)
	return target, mappings
}

func buildComposeTopologies(application domainapplication.SourceApplication, profile domainmapping.Profile, sourceName, targetName string) (domainmigration.TopologyGraph, domainmigration.TopologyGraph, []domainmigration.ResourceMapping) {
	compose := application.Inventory.Compose
	project := compose.ProjectName
	namespace := mappedValue(project, profile.Namespaces)
	if namespace == project {
		namespace = strings.ToLower(strings.ReplaceAll(project, "_", "-"))
	}
	source := domainmigration.TopologyGraph{Name: sourceName, Type: "COMPOSE", Namespace: project, Nodes: []domainmigration.TopologyNode{}, Edges: []domainmigration.TopologyEdge{}}
	target := domainmigration.TopologyGraph{Name: targetName, Type: "KUBERNETES", Namespace: namespace, Nodes: []domainmigration.TopologyNode{}, Edges: []domainmigration.TopologyEdge{}}
	mappings := []domainmigration.ResourceMapping{}
	serviceTargets := map[string]string{}
	volumeTargets := map[string]string{}
	for _, service := range compose.Services {
		sourceID := composeNodeID("ComposeService", project, service.Name)
		source.Nodes = append(source.Nodes, domainmigration.TopologyNode{ID: sourceID, Side: "SOURCE", Kind: "ComposeService", Namespace: project, Name: service.Name,
			Required: true, Status: domainmigration.ResourceDiscovered, Attributes: map[string]any{"image": service.Image, "ports": service.Ports, "mounts": service.Mounts, "networks": service.Networks, "profiles": service.Profiles}})
		targetName := kubernetesResourceName(service.Name)
		deploymentID := topologyNodeID("TARGET", "apps/v1", "Deployment", namespace, targetName)
		images := []string{}
		if service.Image != "" {
			images = []string{rewriteRegistry(service.Image, profile.Registries)}
		}
		changes := []domainmigration.MappingChange{{Type: "RESOURCE_GENERATION", SourceValue: "ComposeService", TargetValue: "Deployment", Changed: true, Applied: boolPointer(true)}}
		if targetName != service.Name {
			changes = append(changes, domainmigration.MappingChange{Type: "RESOURCE_NAME", Path: "metadata.name", SourceValue: service.Name, TargetValue: targetName, Changed: true})
		}
		if namespace != project {
			changes = append(changes, domainmigration.MappingChange{Type: "NAMESPACE", Path: "metadata.namespace", SourceValue: project, TargetValue: namespace, Changed: true})
		}
		if service.Image != "" && images[0] != service.Image {
			changes = append(changes, domainmigration.MappingChange{Type: "IMAGE", Path: "spec.template.spec.containers[].image", SourceValue: service.Image, TargetValue: images[0], Changed: true})
		}
		target.Nodes = append(target.Nodes, domainmigration.TopologyNode{ID: deploymentID, Side: "TARGET", APIVersion: "apps/v1", Kind: "Deployment", Namespace: namespace, Name: targetName,
			Required: true, Status: domainmigration.ResourcePlanned, Message: "等待目标创建", Attributes: map[string]any{"images": images}, MappingChanges: changes})
		serviceTargets[service.Name] = deploymentID
		mappings = append(mappings, domainmigration.ResourceMapping{ID: "map|" + sourceID + "|deployment", SourceNodeID: sourceID, TargetNodeID: deploymentID, Relation: "GENERATES", Changes: changes})
		serviceID := topologyNodeID("TARGET", "v1", "Service", namespace, targetName)
		target.Nodes = append(target.Nodes, domainmigration.TopologyNode{ID: serviceID, Side: "TARGET", APIVersion: "v1", Kind: "Service", Namespace: namespace, Name: targetName,
			Required: true, Status: domainmigration.ResourcePlanned, Message: "等待目标创建", Attributes: map[string]any{"ports": service.Ports},
			MappingChanges: []domainmigration.MappingChange{{Type: "RESOURCE_GENERATION", SourceValue: "Compose service", TargetValue: "Service", Changed: true, Applied: boolPointer(true)}}})
		target.Edges = append(target.Edges, domainmigration.TopologyEdge{ID: serviceID + ">SELECTS>" + deploymentID, From: serviceID, To: deploymentID, Relation: "SELECTS", Required: true, Status: domainmigration.ResourcePlanned})
		mappings = append(mappings, domainmigration.ResourceMapping{ID: "map|" + sourceID + "|service", SourceNodeID: sourceID, TargetNodeID: serviceID, Relation: "EXPOSES_AS"})
	}
	for _, volume := range compose.Volumes {
		sourceID := composeNodeID("ComposeVolume", project, volume.Name)
		source.Nodes = append(source.Nodes, domainmigration.TopologyNode{ID: sourceID, Side: "SOURCE", Kind: "ComposeVolume", Namespace: project, Name: volume.Name,
			Required: !volume.External, Status: domainmigration.ResourceDiscovered, Attributes: map[string]any{"runtimeName": volume.RuntimeName, "driver": volume.Driver, "external": volume.External}})
		targetName := kubernetesResourceName(volume.Name)
		targetID := topologyNodeID("TARGET", "v1", "PersistentVolumeClaim", namespace, targetName)
		changes := []domainmigration.MappingChange{{Type: "RESOURCE_GENERATION", SourceValue: "ComposeVolume", TargetValue: "PersistentVolumeClaim", Changed: true, Applied: boolPointer(true)}}
		if targetName != volume.Name {
			changes = append(changes, domainmigration.MappingChange{Type: "RESOURCE_NAME", Path: "metadata.name", SourceValue: volume.Name, TargetValue: targetName, Changed: true})
		}
		if len(profile.Storage) > 0 && profile.Storage[0].Source != profile.Storage[0].Target {
			changes = append(changes, domainmigration.MappingChange{Type: "STORAGE_CLASS", Path: "spec.storageClassName", SourceValue: profile.Storage[0].Source, TargetValue: profile.Storage[0].Target, Changed: true})
		}
		target.Nodes = append(target.Nodes, domainmigration.TopologyNode{ID: targetID, Side: "TARGET", APIVersion: "v1", Kind: "PersistentVolumeClaim", Namespace: namespace, Name: targetName,
			Required: !volume.External, Status: domainmigration.ResourcePlanned, Message: "等待目标创建", MappingChanges: changes})
		volumeTargets[volume.Name] = targetID
		mappings = append(mappings, domainmigration.ResourceMapping{ID: "map|" + sourceID, SourceNodeID: sourceID, TargetNodeID: targetID, Relation: "RESTORES_TO", Changes: changes})
	}
	for _, network := range compose.Networks {
		sourceID := composeNodeID("ComposeNetwork", project, network.Name)
		source.Nodes = append(source.Nodes, domainmigration.TopologyNode{ID: sourceID, Side: "SOURCE", Kind: "ComposeNetwork", Namespace: project, Name: network.Name,
			Required: false, Status: domainmigration.ResourceSkipped, Message: "Kubernetes 使用集群网络，不生成独立资源", Attributes: map[string]any{"driver": network.Driver, "external": network.External}})
		mappings = append(mappings, domainmigration.ResourceMapping{ID: "map|" + sourceID, SourceNodeID: sourceID, Relation: "USES_CLUSTER_NETWORK",
			Changes: []domainmigration.MappingChange{{Type: "RESOURCE_GENERATION", SourceValue: "ComposeNetwork", TargetValue: "Kubernetes cluster network", Changed: true, Applied: boolPointer(true)}}})
	}
	for _, service := range compose.Services {
		from := composeNodeID("ComposeService", project, service.Name)
		for _, dependency := range service.DependsOn {
			to := composeNodeID("ComposeService", project, dependency)
			source.Edges = append(source.Edges, domainmigration.TopologyEdge{ID: from + ">DEPENDS_ON>" + to, From: from, To: to, Relation: "DEPENDS_ON", Required: true, Status: domainmigration.ResourceDiscovered})
			if serviceTargets[service.Name] != "" && serviceTargets[dependency] != "" {
				target.Edges = append(target.Edges, domainmigration.TopologyEdge{ID: "target|" + from + ">DEPENDS_ON>" + to, From: serviceTargets[service.Name], To: serviceTargets[dependency], Relation: "DEPENDS_ON", Required: true, Status: domainmigration.ResourcePlanned})
			}
		}
		for _, network := range service.Networks {
			to := composeNodeID("ComposeNetwork", project, network)
			source.Edges = append(source.Edges, domainmigration.TopologyEdge{ID: from + ">ATTACHED_TO>" + to, From: from, To: to, Relation: "ATTACHED_TO", Required: false, Status: domainmigration.ResourceDiscovered})
		}
		for _, mount := range service.Mounts {
			if mount.Type != "volume" || mount.Source == "" {
				continue
			}
			to := composeNodeID("ComposeVolume", project, mount.Source)
			source.Edges = append(source.Edges, domainmigration.TopologyEdge{ID: from + ">MOUNTS>" + to, From: from, To: to, Relation: "MOUNTS", Required: true, Status: domainmigration.ResourceDiscovered})
			if serviceTargets[service.Name] != "" && volumeTargets[mount.Source] != "" {
				target.Edges = append(target.Edges, domainmigration.TopologyEdge{ID: "target|" + from + ">MOUNTS>" + to, From: serviceTargets[service.Name], To: volumeTargets[mount.Source], Relation: "MOUNTS", Required: true, Status: domainmigration.ResourcePlanned})
			}
		}
	}
	sortGraph(&source)
	sortGraph(&target)
	sort.Slice(mappings, func(i, j int) bool { return mappings[i].ID < mappings[j].ID })
	return source, target, mappings
}

func mappingChangesForNode(source domainmigration.TopologyNode, target *domainmigration.TopologyNode, profile domainmapping.Profile) []domainmigration.MappingChange {
	changes := []domainmigration.MappingChange{}
	if source.Namespace != target.Namespace {
		changes = append(changes, domainmigration.MappingChange{Type: "NAMESPACE", Path: "metadata.namespace", SourceValue: source.Namespace, TargetValue: target.Namespace, Changed: true})
	}
	if source.APIVersion != target.APIVersion {
		changes = append(changes, domainmigration.MappingChange{Type: "API_VERSION", Path: "apiVersion", SourceValue: source.APIVersion, TargetValue: target.APIVersion, Changed: true})
	}
	if source.Kind == "PersistentVolumeClaim" {
		from, _ := source.Attributes["storageClass"].(string)
		to := mappedValue(from, profile.Storage)
		explicit := false
		for _, item := range profile.Storage {
			if item.Source == from {
				explicit = true
				break
			}
		}
		if from != "" && (from != to || explicit) {
			changes = append(changes, domainmigration.MappingChange{Type: "STORAGE_CLASS", Path: "spec.storageClassName", SourceValue: from, TargetValue: to, Changed: true})
			target.Attributes = cloneAttributes(target.Attributes)
			target.Attributes["storageClass"] = to
		}
	}
	if source.Kind == "Ingress" {
		from, _ := source.Attributes["ingressClass"].(string)
		to := mappedValue(from, profile.Ingress)
		if from != to {
			changes = append(changes, domainmigration.MappingChange{Type: "INGRESS_CLASS", Path: "spec.ingressClassName", SourceValue: from, TargetValue: to, Changed: true})
			target.Attributes = cloneAttributes(target.Attributes)
			target.Attributes["ingressClass"] = to
		}
	}
	if raw, ok := source.Attributes["images"].([]string); ok {
		mapped := make([]string, 0, len(raw))
		for _, image := range raw {
			next := rewriteRegistry(image, profile.Registries)
			mapped = append(mapped, next)
			if next != image {
				changes = append(changes, domainmigration.MappingChange{Type: "IMAGE", Path: "spec.template.spec.containers[].image", SourceValue: image, TargetValue: next, Changed: true})
			}
		}
		target.Attributes = cloneAttributes(target.Attributes)
		target.Attributes["images"] = mapped
	}
	if selectors := stringMap(source.Attributes["nodeSelectors"]); len(selectors) > 0 && isWorkloadKind(source.Kind) {
		for _, item := range profile.NodeLabels {
			if _, used := selectors[item.Source]; used {
				targetValue := item.Target
				if item.Action == "DROP" {
					targetValue = "<dropped>"
				}
				changes = append(changes, domainmigration.MappingChange{Type: "NODE_LABEL", Path: "spec.template.spec.nodeSelector/affinity", SourceValue: item.Source, TargetValue: targetValue, Changed: true})
			}
		}
	}
	for _, sourceNFS := range stringSlice(source.Attributes["nfsSources"]) {
		for _, item := range profile.NFS {
			if sourceNFS == item.SourceServer+":"+item.SourceExport {
				changes = append(changes, domainmigration.MappingChange{Type: "NFS", Path: "spec.template.spec.volumes[].nfs", SourceValue: sourceNFS, TargetValue: item.TargetServer + ":" + item.TargetExport, Changed: true})
			}
		}
	}
	return changes
}

func mergeObservedTopology(expected, observed domainmigration.TopologyGraph, runStatus domainmigration.RunStatus) (domainmigration.TopologyGraph, int) {
	byKey := map[string]domainmigration.TopologyNode{}
	for _, node := range observed.Nodes {
		byKey[resourceIdentity(node.Kind, node.Namespace, node.Name)] = node
	}
	result := expected
	result.Edges = observed.Edges
	drifted := 0
	seen := map[string]bool{}
	for index := range result.Nodes {
		node := &result.Nodes[index]
		key := resourceIdentity(node.Kind, node.Namespace, node.Name)
		actual, ok := byKey[key]
		if !ok {
			if domainmigration.IsTerminal(runStatus) || runStatus == domainmigration.RunAwaitingCutover {
				node.Status, node.Health, node.Message = domainmigration.ResourceMissing, "MISSING", "目标资源不存在"
				drifted++
			}
			continue
		}
		seen[key] = true
		node.Attributes = mergeAttributes(node.Attributes, actual.Attributes)
		if runStatus == domainmigration.RunCompleted || runStatus == domainmigration.RunAwaitingCutover {
			node.Status, node.Health, node.Message = domainmigration.ResourceSucceeded, "HEALTHY", "目标资源已创建并通过任务验证"
		} else if domainmigration.IsTerminal(runStatus) {
			if healthy, message := currentlyHealthy(*node); healthy {
				node.Status, node.Health, node.Message = domainmigration.ResourceSucceeded, "HEALTHY", message
			} else {
				node.Status, node.Health, node.Message = domainmigration.ResourceWarning, "DEGRADED", message
			}
		} else {
			node.Status, node.Health, node.Message = domainmigration.ResourceCreated, "PROGRESSING", "目标资源已创建，等待完整验证"
		}
		for changeIndex := range node.MappingChanges {
			node.MappingChanges[changeIndex].Applied = boolPointer(mappingAppearsApplied(node.MappingChanges[changeIndex], actual))
			if !*node.MappingChanges[changeIndex].Applied {
				node.Status, node.Health, node.Message = domainmigration.ResourceFailed, "DEGRADED", "目标实际值与资源映射不一致"
				drifted++
			}
		}
	}
	for _, node := range observed.Nodes {
		key := resourceIdentity(node.Kind, node.Namespace, node.Name)
		if seen[key] {
			continue
		}
		if isMigrationHelperResource(node) {
			continue
		}
		if isGeneratedClusterResource(node) {
			node.Required = false
			node.Status, node.Health, node.Message = domainmigration.ResourceSkipped, "IGNORED", "集群或控制器自动生成，无需纳入迁移结论"
			result.Nodes = append(result.Nodes, node)
			continue
		}
		node.Status, node.Health, node.Message = domainmigration.ResourceWarning, "DRIFTED", "目标存在非计划资源"
		result.Nodes = append(result.Nodes, node)
		drifted++
	}
	nodeIDs := map[string]bool{}
	for _, node := range result.Nodes {
		nodeIDs[node.ID] = true
	}
	edges := result.Edges[:0]
	for _, edge := range result.Edges {
		if !nodeIDs[edge.From] || !nodeIDs[edge.To] {
			continue
		}
		edge.Status = edgeStatus(edge, result.Nodes)
		edges = append(edges, edge)
	}
	result.Edges = edges
	sortGraph(&result)
	return result, drifted
}

func currentlyHealthy(node domainmigration.TopologyNode) (bool, string) {
	switch node.Kind {
	case "Deployment":
		desired := integerAttribute(node.Attributes, "replicas")
		available := integerAttribute(node.Attributes, "availableReplicas")
		if available >= desired && desired > 0 {
			return true, fmt.Sprintf("目标当前就绪：desired=%d available=%d", desired, available)
		}
		return false, fmt.Sprintf("目标当前未就绪：desired=%d available=%d", desired, available)
	case "StatefulSet", "DaemonSet":
		desired := integerAttribute(node.Attributes, "replicas")
		ready := integerAttribute(node.Attributes, "readyReplicas")
		if ready >= desired && desired > 0 {
			return true, fmt.Sprintf("目标当前就绪：desired=%d ready=%d", desired, ready)
		}
		return false, fmt.Sprintf("目标当前未就绪：desired=%d ready=%d", desired, ready)
	case "PersistentVolumeClaim":
		phase, _ := node.Attributes["phase"].(string)
		if phase == "Bound" {
			return true, "目标 PVC 当前为 Bound"
		}
		return false, "目标 PVC 当前状态为 " + firstNonEmpty(phase, "Unknown")
	default:
		return true, "目标资源当前存在"
	}
}

func integerAttribute(values map[string]any, key string) int64 {
	switch value := values[key].(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case float64:
		return int64(value)
	case json.Number:
		parsed, _ := value.Int64()
		return parsed
	}
	return 0
}

func isGeneratedClusterResource(node domainmigration.TopologyNode) bool {
	if node.Kind == "StorageClass" {
		return true
	}
	if node.Kind == "ServiceAccount" && node.Name == "default" {
		return true
	}
	return node.Kind == "ConfigMap" && node.Name == "kube-root-ca.crt"
}

func isMigrationHelperResource(node domainmigration.TopologyNode) bool {
	labels := stringMap(node.Attributes["labels"])
	if labels["app.kubernetes.io/managed-by"] == "sks-migration-center" {
		return true
	}
	return strings.HasPrefix(node.Name, "smc-kopia-") ||
		strings.HasPrefix(node.Name, "smc-validation-") ||
		strings.HasPrefix(node.Name, "smc-pvc-")
}

func diagnoseRun(run domainmigration.Run, steps []domainmigration.Step, events []domainmigration.Event, attempts []domainmigration.StepAttempt, now time.Time) domainmigration.RunDiagnosis {
	result := domainmigration.RunDiagnosis{State: "HEALTHY", Title: "迁移执行正常"}
	for _, step := range steps {
		if step.Status == domainmigration.StepSucceeded {
			result.LastSuccessfulStep = string(step.Type)
		}
		if step.Status == domainmigration.StepFailed {
			id := step.ID
			result.FailedStepID = &id
			result.State, result.Title, result.Reason = "FAILED", "迁移步骤失败", firstNonEmpty(step.Summary, run.ErrorMessage)
		}
	}
	if len(events) > 0 {
		value := events[len(events)-1].CreatedAt
		result.LastEventAt = &value
	}
	for _, attempt := range attempts {
		if attempt.Status == domainmigration.AttemptRunning && attempt.LeaseExpiresAt != nil && attempt.LeaseExpiresAt.Before(now) {
			result.State, result.Title, result.Reason, result.Remediation = "STALLED", "Worker 心跳已过期", "当前步骤租约已经过期，任务将由可用 Worker 重新领取。", "检查 Worker Pod、数据库连接和目标组件状态。"
		}
		if attempt.Status == domainmigration.AttemptRunning && result.State == "HEALTHY" {
			result.State, result.Title, result.Reason = "WAITING", "步骤正在执行，等待结果", "Worker 正在运行；心跳仅表示 Worker 在线，不代表外部操作已经完成。"
			if result.LastEventAt != nil && now.Sub(*result.LastEventAt) > 3*time.Minute {
				result.Title, result.Reason, result.Remediation = "长时间未收到步骤进展", "最近三分钟没有新的关键进度事件，当前步骤可能仍在等待外部资源。", "展开当前步骤查看等待对象；检查源/目标工作负载、Velero 及连接状态。"
			}
		}
		if attempt.Status == domainmigration.AttemptRetryScheduled {
			result.State, result.Title, result.Reason = "RETRYING", "等待自动重试", attempt.ErrorMessage
		}
	}
	if run.Status == domainmigration.RunCompleted {
		result.State, result.Title, result.Reason = "SUCCEEDED", "迁移已完成", "全部步骤完成并已确认人工切流。"
	}
	if run.Status == domainmigration.RunAwaitingCutover {
		result.State, result.Title, result.Reason = "WAITING", "等待人工切流", "目标验证已通过，等待管理员确认外部流量切换。"
	}
	if run.Status == domainmigration.RunCancelled {
		result.State, result.Title, result.Reason = "CANCELLED", "迁移已取消", firstNonEmpty(run.ErrorMessage, "源业务未停止或已经恢复。")
	}
	return result
}

func allEvents(ctx context.Context, runs repository.MigrationRunRepository, runID uuid.UUID) ([]domainmigration.Event, error) {
	values := []domainmigration.Event{}
	var after int64
	for {
		batch, err := runs.ListEvents(ctx, runID, after, 500)
		if err != nil {
			return nil, err
		}
		values = append(values, batch...)
		if len(batch) < 500 {
			return values, nil
		}
		after = batch[len(batch)-1].ID
	}
}

func topologyNodeID(side, apiVersion, kind, namespace, name string) string {
	return strings.Join([]string{side, apiVersion, kind, namespace, name}, "|")
}
func composeNodeID(kind, project, name string) string {
	return strings.Join([]string{"SOURCE", "compose", kind, project, name}, "|")
}
func resourceIdentity(kind, namespace, name string) string {
	return strings.Join([]string{kind, namespace, name}, "|")
}

func kubernetesResourceName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var result strings.Builder
	lastDash := false
	for _, character := range value {
		allowed := character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '.' || character == '-'
		if allowed {
			result.WriteRune(character)
			lastDash = character == '-'
			continue
		}
		if !lastDash {
			result.WriteByte('-')
			lastDash = true
		}
	}
	name := strings.Trim(result.String(), ".-")
	if len(name) > 63 {
		name = strings.Trim(name[:63], ".-")
	}
	if name == "" {
		return "resource"
	}
	return name
}

func withoutTemporaryObservationLimit(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "暂时无法检查目标资源，请检查目标集群连接；当前展示已保存的迁移证据。" ||
			value == "目标集群当前不可达，目标状态依据持久化迁移事件推断" {
			continue
		}
		result = append(result, value)
	}
	return result
}
func hasNode(nodes []domainmigration.TopologyNode, id string) bool {
	for _, node := range nodes {
		if node.ID == id {
			return true
		}
	}
	return false
}
func mappedValue(value string, mappings []domainmapping.KeyValue) string {
	for _, item := range mappings {
		if item.Source == value {
			return item.Target
		}
	}
	return value
}
func rewriteRegistry(value string, mappings []domainmapping.KeyValue) string {
	for _, item := range mappings {
		source := strings.TrimSuffix(item.Source, "/")
		if value == source || strings.HasPrefix(value, source+"/") {
			return strings.TrimSuffix(item.Target, "/") + strings.TrimPrefix(value, source)
		}
	}
	return value
}
func targetAPIVersion(value, kind string) string {
	if kind == "Ingress" && (value == "extensions/v1beta1" || value == "networking.k8s.io/v1beta1") {
		return "networking.k8s.io/v1"
	}
	if (kind == "Deployment" || kind == "StatefulSet" || kind == "DaemonSet") && strings.HasPrefix(value, "apps/") && value != "apps/v1" {
		return "apps/v1"
	}
	return value
}
func boolPointer(value bool) *bool { return &value }
func cloneAttributes(value map[string]any) map[string]any {
	result := map[string]any{}
	for key, item := range value {
		result[key] = item
	}
	return result
}
func mergeAttributes(first, second map[string]any) map[string]any {
	result := cloneAttributes(first)
	for key, value := range second {
		result[key] = value
	}
	return result
}
func mappingAppearsApplied(change domainmigration.MappingChange, node domainmigration.TopologyNode) bool {
	switch change.Type {
	case "NAMESPACE":
		return node.Namespace == change.TargetValue
	case "STORAGE_CLASS":
		value, _ := node.Attributes["storageClass"].(string)
		return value == change.TargetValue
	case "INGRESS_CLASS":
		value, _ := node.Attributes["ingressClass"].(string)
		return value == change.TargetValue
	case "IMAGE":
		for _, value := range stringSlice(node.Attributes["images"]) {
			if value == change.TargetValue {
				return true
			}
		}
		return false
	case "NODE_LABEL":
		selectors := stringMap(node.Attributes["nodeSelectors"])
		if change.TargetValue == "<dropped>" {
			_, exists := selectors[change.SourceValue]
			return !exists
		}
		_, ok := selectors[change.TargetValue]
		return ok
	case "NFS":
		for _, value := range stringSlice(node.Attributes["nfsSources"]) {
			if value == change.TargetValue {
				return true
			}
		}
		return false
	default:
		return true
	}
}
func stringMap(value any) map[string]string {
	if values, ok := value.(map[string]string); ok {
		return values
	}
	result := map[string]string{}
	if values, ok := value.(map[string]any); ok {
		for key, raw := range values {
			if text, ok := raw.(string); ok {
				result[key] = text
			}
		}
	}
	return result
}
func stringSlice(value any) []string {
	if values, ok := value.([]string); ok {
		return values
	}
	if values, ok := value.([]any); ok {
		result := []string{}
		for _, value := range values {
			if text, ok := value.(string); ok {
				result = append(result, text)
			}
		}
		return result
	}
	return nil
}
func isWorkloadKind(value string) bool {
	switch value {
	case "Deployment", "StatefulSet", "DaemonSet", "Job", "CronJob":
		return true
	}
	return false
}
func edgeStatus(edge domainmigration.TopologyEdge, nodes []domainmigration.TopologyNode) domainmigration.ResourceMigrationStatus {
	values := map[string]domainmigration.ResourceMigrationStatus{}
	for _, node := range nodes {
		values[node.ID] = node.Status
	}
	for _, id := range []string{edge.From, edge.To} {
		if values[id] == domainmigration.ResourceFailed || values[id] == domainmigration.ResourceMissing {
			return domainmigration.ResourceFailed
		}
		if values[id] == domainmigration.ResourceWarning {
			return domainmigration.ResourceWarning
		}
	}
	return domainmigration.ResourceSucceeded
}
func sortGraph(graph *domainmigration.TopologyGraph) {
	sort.Slice(graph.Nodes, func(i, j int) bool { return graph.Nodes[i].ID < graph.Nodes[j].ID })
	sort.Slice(graph.Edges, func(i, j int) bool { return graph.Edges[i].ID < graph.Edges[j].ID })
}
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
