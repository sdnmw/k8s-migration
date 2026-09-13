package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	sshadapter "github.com/smartx/sks-migration-center/internal/adapter/ssh"
	domainapplication "github.com/smartx/sks-migration-center/internal/domain/application"
	domaincredential "github.com/smartx/sks-migration-center/internal/domain/credential"
	domainenvironment "github.com/smartx/sks-migration-center/internal/domain/environment"
	"github.com/smartx/sks-migration-center/internal/repository"
)

var ErrInvalidInput = errors.New("invalid application discovery input")
var ErrDiscovery = errors.New("application discovery failed")

type Vault interface {
	Resolve(context.Context, uuid.UUID) ([]byte, error)
}

type KubernetesInventoryClient interface {
	DiscoverNamespace(context.Context, []byte, string) (domainapplication.Inventory, error)
	ListNamespaces(context.Context, []byte) ([]string, error)
}

type KubernetesCoreInventoryClient interface {
	DiscoverNamespaceCore(context.Context, []byte, string) (domainapplication.Inventory, error)
}

type ComposeHostClient interface {
	DiscoverComposeProjects(context.Context, string, sshadapter.Credential) ([]sshadapter.ComposeProject, error)
}

type ComposeAnalyzer interface {
	Analyze(context.Context, string, []byte, []byte) (domainapplication.ComposeInventory, error)
}

type ComposeVault interface {
	Store(context.Context, string, domaincredential.Type, []byte) (domaincredential.Metadata, error)
	Delete(context.Context, uuid.UUID) error
}

type Service struct {
	environments repository.EnvironmentRepository
	applications repository.ApplicationRepository
	vault        Vault
	kubernetes   KubernetesInventoryClient
	compose      ComposeAnalyzer
	composeVault ComposeVault
	composeHost  ComposeHostClient
	clock        func() time.Time
}

func (s *Service) WithComposeHost(client ComposeHostClient) *Service {
	s.composeHost = client
	return s
}

func NewService(environments repository.EnvironmentRepository, applications repository.ApplicationRepository, vault Vault, kubernetes KubernetesInventoryClient, composeAnalyzers ...ComposeAnalyzer) (*Service, error) {
	if environments == nil || applications == nil || vault == nil || kubernetes == nil {
		return nil, errors.New("environment and application repositories, credential vault and Kubernetes client are required")
	}
	service := &Service{environments: environments, applications: applications, vault: vault, kubernetes: kubernetes, clock: func() time.Time { return time.Now().UTC() }}
	if len(composeAnalyzers) > 0 {
		service.compose = composeAnalyzers[0]
		service.composeVault, _ = vault.(ComposeVault)
	}
	return service, nil
}

func (s *Service) Discover(ctx context.Context, environmentID uuid.UUID, namespace string) (domainapplication.SourceApplication, error) {
	return s.DiscoverSelected(ctx, environmentID, namespace, "", nil)
}

func (s *Service) Preview(ctx context.Context, environmentID uuid.UUID, namespace string) (domainapplication.Inventory, error) {
	namespace = strings.TrimSpace(namespace)
	if environmentID == uuid.Nil || namespace == "" {
		return domainapplication.Inventory{}, fmt.Errorf("%w: environmentId and namespace are required", ErrInvalidInput)
	}
	environment, err := s.environments.Get(ctx, environmentID)
	if err != nil {
		return domainapplication.Inventory{}, err
	}
	if environment.Role != domainenvironment.RoleSource || environment.Kind != domainenvironment.KindKubernetes || environment.CredentialID == nil {
		return domainapplication.Inventory{}, fmt.Errorf("%w: environment must be a Kubernetes source", ErrInvalidInput)
	}
	credential, err := s.vault.Resolve(ctx, *environment.CredentialID)
	if err != nil {
		return domainapplication.Inventory{}, fmt.Errorf("resolve source credential: %w", err)
	}
	inventory, err := s.kubernetes.DiscoverNamespace(ctx, credential, namespace)
	clearBytes(credential)
	if err != nil {
		return domainapplication.Inventory{}, fmt.Errorf("%w: namespace inventory unavailable", ErrDiscovery)
	}
	return inventory, nil
}

func (s *Service) DiscoverSelected(ctx context.Context, environmentID uuid.UUID, namespace, name string, selected []domainapplication.ResourceReference) (domainapplication.SourceApplication, error) {
	inventory, err := s.Preview(ctx, environmentID, namespace)
	if err != nil {
		return domainapplication.SourceApplication{}, err
	}
	namespace = strings.TrimSpace(namespace)
	name = strings.TrimSpace(name)
	if len(selected) > 0 {
		inventory, err = filterInventory(inventory, selected)
		if err != nil {
			return domainapplication.SourceApplication{}, err
		}
		if name == "" {
			name = namespace + "-selection"
		}
	} else {
		name = namespace
	}
	if len(name) > 128 {
		return domainapplication.SourceApplication{}, fmt.Errorf("%w: application name must not exceed 128 characters", ErrInvalidInput)
	}
	now := s.clock()
	value := domainapplication.SourceApplication{ID: uuid.New(), EnvironmentID: environmentID, Name: name, SourceType: domainapplication.SourceKubernetes, Namespace: namespace, Inventory: inventory, CreatedAt: now, UpdatedAt: now}
	stored, err := s.applications.Upsert(ctx, value)
	if err != nil {
		return domainapplication.SourceApplication{}, err
	}
	return stored, nil
}

func (s *Service) DiscoverAll(ctx context.Context, environmentID uuid.UUID) ([]domainapplication.SourceApplication, error) {
	environment, err := s.environments.Get(ctx, environmentID)
	if err != nil {
		return nil, err
	}
	if environment.Role != domainenvironment.RoleSource || environment.Kind != domainenvironment.KindKubernetes || environment.CredentialID == nil {
		return nil, fmt.Errorf("%w: environment must be a Kubernetes source", ErrInvalidInput)
	}
	credential, err := s.vault.Resolve(ctx, *environment.CredentialID)
	if err != nil {
		return nil, err
	}
	namespaces, err := s.kubernetes.ListNamespaces(ctx, credential)
	if err != nil {
		clearBytes(credential)
		return nil, fmt.Errorf("%w: namespace list unavailable", ErrDiscovery)
	}
	// Namespace discovery is I/O bound. Scanning it serially made medium sized
	// clusters hit the web gateway's 60 second timeout. Resolve the kubeconfig
	// once and use bounded concurrency; database writes remain serialized below.
	type discoveryResult struct {
		namespace string
		inventory domainapplication.Inventory
		err       error
	}
	work := make([]string, 0, len(namespaces))
	for _, namespace := range namespaces {
		if namespace == "kube-system" || namespace == "kube-public" || namespace == "kube-node-lease" {
			continue
		}
		work = append(work, namespace)
	}
	discovered := make([]discoveryResult, len(work))
	// Four in-flight core inventories keep discovery comfortably below the API
	// memory limit while still completing medium clusters within the gateway
	// timeout. Full CRD discovery is intentionally deferred until a Namespace
	// is selected.
	semaphore := make(chan struct{}, 4)
	coreClient, hasCoreDiscovery := s.kubernetes.(KubernetesCoreInventoryClient)
	var wait sync.WaitGroup
	for index, namespace := range work {
		wait.Add(1)
		go func(index int, namespace string) {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				discovered[index] = discoveryResult{namespace: namespace, err: ctx.Err()}
				return
			}
			var inventory domainapplication.Inventory
			var discoverErr error
			if hasCoreDiscovery {
				inventory, discoverErr = coreClient.DiscoverNamespaceCore(ctx, credential, namespace)
			} else {
				inventory, discoverErr = s.kubernetes.DiscoverNamespace(ctx, credential, namespace)
			}
			discovered[index] = discoveryResult{namespace: namespace, inventory: inventory, err: discoverErr}
		}(index, namespace)
	}
	wait.Wait()
	clearBytes(credential)

	result := make([]domainapplication.SourceApplication, 0, len(discovered))
	failed := make([]string, 0)
	for _, item := range discovered {
		if item.err != nil {
			failed = append(failed, item.namespace)
			continue
		}
		inventory := item.inventory
		if len(inventory.Workloads) == 0 && len(inventory.Services) == 0 && len(inventory.Ingresses) == 0 && len(inventory.PVCs) == 0 {
			continue
		}
		now := s.clock()
		value, discoverErr := s.applications.Upsert(ctx, domainapplication.SourceApplication{ID: uuid.New(), EnvironmentID: environmentID, Name: item.namespace, SourceType: domainapplication.SourceKubernetes, Namespace: item.namespace, Inventory: inventory, CreatedAt: now, UpdatedAt: now})
		if discoverErr != nil {
			failed = append(failed, item.namespace)
			continue
		}
		result = append(result, value)
	}
	if len(result) == 0 && len(failed) > 0 {
		return nil, fmt.Errorf("%w: %d namespaces could not be inventoried (for example %s)", ErrDiscovery, len(failed), strings.Join(failed[:min(3, len(failed))], ", "))
	}
	return result, nil
}

func (s *Service) DiscoverCompose(ctx context.Context, environmentID uuid.UUID) ([]domainapplication.SourceApplication, error) {
	if s.composeHost == nil || s.compose == nil {
		return nil, fmt.Errorf("%w: Compose host discovery is unavailable", ErrInvalidInput)
	}
	environment, err := s.environments.Get(ctx, environmentID)
	if err != nil {
		return nil, err
	}
	if environment.Role != domainenvironment.RoleSource || environment.Kind != domainenvironment.KindDockerCompose || environment.CredentialID == nil {
		return nil, fmt.Errorf("%w: environment must be a Docker Compose source", ErrInvalidInput)
	}
	payload, err := s.vault.Resolve(ctx, *environment.CredentialID)
	if err != nil {
		return nil, err
	}
	var sshCredential sshadapter.Credential
	if err := json.Unmarshal(payload, &sshCredential); err != nil {
		clearBytes(payload)
		return nil, fmt.Errorf("%w: stored SSH credential is invalid", ErrDiscovery)
	}
	clearBytes(payload)
	projects, err := s.composeHost.DiscoverComposeProjects(ctx, environment.Endpoint, sshCredential)
	if err != nil {
		if strings.Contains(err.Error(), "permission denied") {
			return nil, fmt.Errorf("%w: COMPOSE_CONFIG_PERMISSION: Compose 项目已发现，但 SSH 用户无法读取项目配置或引用文件。请使用有读取权限的 SSH 账号后重试。", ErrDiscovery)
		}
		return nil, fmt.Errorf("%w: Compose project discovery failed", ErrDiscovery)
	}
	result := make([]domainapplication.SourceApplication, 0, len(projects))
	for _, project := range projects {
		value, registerErr := s.RegisterCompose(ctx, environmentID, project.Name, project.ComposeYAML, nil)
		if registerErr != nil {
			return result, fmt.Errorf("%w: 项目 %s 解析失败：%v", ErrDiscovery, project.Name, registerErr)
		}
		if value.Inventory.Compose != nil {
			value.Inventory.Compose.Status = project.Status
			value.Inventory.Compose.ConfigFiles = append([]string(nil), project.ConfigFiles...)
			value.Inventory.Compose.WorkingDir = project.WorkingDir
			value.UpdatedAt = s.clock()
			value, registerErr = s.applications.Upsert(ctx, value)
		}
		if registerErr == nil {
			result = append(result, value)
		}
	}
	return result, nil
}

func filterInventory(value domainapplication.Inventory, selected []domainapplication.ResourceReference) (domainapplication.Inventory, error) {
	keys := make(map[string]bool, len(selected))
	for _, item := range selected {
		if strings.TrimSpace(item.Kind) == "" || strings.TrimSpace(item.Name) == "" {
			return domainapplication.Inventory{}, fmt.Errorf("%w: selected resources require kind and name", ErrInvalidInput)
		}
		keys[resourceKey(item.Kind, item.Namespace, item.Name)] = true
	}
	filterResources := func(items []domainapplication.ResourceSummary) []domainapplication.ResourceSummary {
		result := make([]domainapplication.ResourceSummary, 0)
		for _, item := range items {
			if keys[resourceKey(item.Kind, item.Namespace, item.Name)] {
				result = append(result, item)
			}
		}
		return result
	}
	value.Resources = filterResources(value.Resources)
	if len(value.Resources) == 0 {
		return domainapplication.Inventory{}, fmt.Errorf("%w: no selected resource exists in the namespace inventory", ErrInvalidInput)
	}
	value.Workloads = filterResources(value.Workloads)
	value.Services = filterResources(value.Services)
	value.Ingresses = filterResources(value.Ingresses)
	value.ConfigMaps = filterResources(value.ConfigMaps)
	value.Secrets = filterResources(value.Secrets)
	value.ServiceAccounts = filterResources(value.ServiceAccounts)
	value.Roles = filterResources(value.Roles)
	value.RoleBindings = filterResources(value.RoleBindings)
	value.CRDs = filterResources(value.CRDs)
	value.CustomResources = filterResources(value.CustomResources)
	pvcs := make([]domainapplication.VolumeSummary, 0)
	for _, pvc := range value.PVCs {
		if keys[resourceKey("PersistentVolumeClaim", pvc.Namespace, pvc.Name)] {
			pvcs = append(pvcs, pvc)
		}
	}
	value.PVCs = pvcs
	resourceKeys := make(map[string]bool, len(value.Resources))
	images := make(map[string]bool)
	value.Counts = map[string]int{}
	for _, item := range value.Resources {
		resourceKeys[resourceKey(item.Kind, item.Namespace, item.Name)] = true
		value.Counts[item.Kind]++
		for _, image := range item.Images {
			images[image] = true
		}
	}
	dependencies := make([]domainapplication.Dependency, 0)
	for _, dependency := range value.Dependencies {
		if resourceKeys[resourceKey(dependency.From.Kind, dependency.From.Namespace, dependency.From.Name)] && resourceKeys[resourceKey(dependency.To.Kind, dependency.To.Namespace, dependency.To.Name)] {
			dependencies = append(dependencies, dependency)
		}
	}
	value.Dependencies = dependencies
	imageList := make([]domainapplication.ImageSummary, 0)
	for _, image := range value.Images {
		if images[image.Reference] {
			imageList = append(imageList, image)
		}
	}
	value.Images = imageList
	value.Warnings = append(value.Warnings, domainapplication.InventoryWarning{Code: "MANUAL_RESOURCE_SELECTION", Message: "This application contains an explicit administrator-selected resource set."})
	return value, nil
}

func resourceKey(kind, namespace, name string) string {
	return strings.ToLower(strings.TrimSpace(kind)) + "\x00" + strings.TrimSpace(namespace) + "\x00" + strings.TrimSpace(name)
}

func (s *Service) RegisterCompose(ctx context.Context, environmentID uuid.UUID, projectName string, composeYAML, environmentFile []byte) (domainapplication.SourceApplication, error) {
	if environmentID == uuid.Nil || s.compose == nil || s.composeVault == nil {
		return domainapplication.SourceApplication{}, fmt.Errorf("%w: Compose registration is unavailable or environmentId is missing", ErrInvalidInput)
	}
	environment, err := s.environments.Get(ctx, environmentID)
	if err != nil {
		return domainapplication.SourceApplication{}, err
	}
	if environment.Role != domainenvironment.RoleSource || environment.Kind != domainenvironment.KindDockerCompose || environment.CredentialID == nil || environment.Status != domainenvironment.StatusConnected {
		return domainapplication.SourceApplication{}, fmt.Errorf("%w: environment must be a connected Docker Compose source", ErrInvalidInput)
	}
	composeInventory, err := s.compose.Analyze(ctx, projectName, composeYAML, environmentFile)
	if err != nil {
		return domainapplication.SourceApplication{}, fmt.Errorf("%w: Compose inventory unavailable: %v", ErrDiscovery, err)
	}
	definition, err := json.Marshal(domainapplication.ComposeDefinition{ComposeYAML: composeYAML, EnvironmentFile: environmentFile})
	if err != nil {
		return domainapplication.SourceApplication{}, fmt.Errorf("encode Compose definition: %w", err)
	}
	metadata, err := s.composeVault.Store(ctx, composeInventory.ProjectName+" Compose definition", domaincredential.TypeCompose, definition)
	clearBytes(definition)
	if err != nil {
		return domainapplication.SourceApplication{}, fmt.Errorf("store Compose definition: %w", err)
	}
	var previousCredential *uuid.UUID
	existing, listErr := s.applications.ListByEnvironment(ctx, environmentID)
	if listErr == nil {
		for index := range existing {
			if existing[index].Name == composeInventory.ProjectName && existing[index].Namespace == "" {
				previousCredential = existing[index].DefinitionCredentialID
				break
			}
		}
	}
	now := s.clock()
	value := domainapplication.SourceApplication{
		ID: uuid.New(), EnvironmentID: environmentID, Name: composeInventory.ProjectName, SourceType: domainapplication.SourceCompose,
		Inventory: composeApplicationInventory(composeInventory), DefinitionCredentialID: &metadata.ID, CreatedAt: now, UpdatedAt: now,
	}
	stored, err := s.applications.Upsert(ctx, value)
	if err != nil {
		_ = s.composeVault.Delete(context.WithoutCancel(ctx), metadata.ID)
		return domainapplication.SourceApplication{}, err
	}
	if previousCredential != nil && *previousCredential != metadata.ID {
		_ = s.composeVault.Delete(context.WithoutCancel(ctx), *previousCredential)
	}
	return stored, nil
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (domainapplication.SourceApplication, error) {
	return s.applications.Get(ctx, id)
}

func (s *Service) List(ctx context.Context, environmentID uuid.UUID) ([]domainapplication.SourceApplication, error) {
	if environmentID == uuid.Nil {
		return nil, fmt.Errorf("%w: environmentId is required", ErrInvalidInput)
	}
	return s.applications.ListByEnvironment(ctx, environmentID)
}

func composeApplicationInventory(value domainapplication.ComposeInventory) domainapplication.Inventory {
	result := domainapplication.Inventory{
		Resources: []domainapplication.ResourceSummary{}, Workloads: []domainapplication.ResourceSummary{}, Services: []domainapplication.ResourceSummary{},
		Ingresses: []domainapplication.ResourceSummary{}, ConfigMaps: []domainapplication.ResourceSummary{}, Secrets: []domainapplication.ResourceSummary{},
		ServiceAccounts: []domainapplication.ResourceSummary{}, Roles: []domainapplication.ResourceSummary{}, RoleBindings: []domainapplication.ResourceSummary{},
		CRDs: []domainapplication.ResourceSummary{}, CustomResources: []domainapplication.ResourceSummary{}, PVCs: []domainapplication.VolumeSummary{},
		Images: []domainapplication.ImageSummary{}, Dependencies: []domainapplication.Dependency{}, Warnings: []domainapplication.InventoryWarning{},
		Counts: map[string]int{}, Compose: &value,
	}
	images, volumes := map[string]bool{}, map[string]bool{}
	for _, service := range value.Services {
		risks := []string{}
		if service.Privileged {
			risks = append(risks, "PRIVILEGED")
		}
		workload := domainapplication.ResourceSummary{APIVersion: "apps/v1", Kind: "Deployment", Name: service.Name, Images: []string{}, SecurityRisks: risks}
		if service.Image != "" {
			workload.Images = append(workload.Images, service.Image)
			images[service.Image] = true
		}
		result.Workloads = append(result.Workloads, workload)
		result.Resources = append(result.Resources, workload)
		if len(service.Ports) > 0 {
			serviceResource := domainapplication.ResourceSummary{APIVersion: "v1", Kind: "Service", Name: service.Name, ServiceType: "ClusterIP"}
			result.Services = append(result.Services, serviceResource)
			result.Resources = append(result.Resources, serviceResource)
		}
		for index, mount := range service.Mounts {
			if mount.Source == "" || mount.Type == "tmpfs" || volumes[mount.Type+"\x00"+mount.Source] {
				continue
			}
			volumes[mount.Type+"\x00"+mount.Source] = true
			name := mount.Source
			if mount.Type == "bind" {
				name = fmt.Sprintf("%s-bind-%d", service.Name, index+1)
			}
			result.PVCs = append(result.PVCs, domainapplication.VolumeSummary{Name: name, VolumeMode: "Filesystem", AccessModes: []string{"ReadWriteOnce"}, Source: mount.Type + ":" + mount.Source})
		}
	}
	for image := range images {
		result.Images = append(result.Images, domainapplication.ImageSummary{Reference: image})
	}
	for _, warning := range value.Warnings {
		result.Warnings = append(result.Warnings, domainapplication.InventoryWarning{Code: warning.Code, Resource: warning.Service, Message: warning.Message})
	}
	sort.Slice(result.Images, func(i, j int) bool { return result.Images[i].Reference < result.Images[j].Reference })
	sort.Slice(result.PVCs, func(i, j int) bool { return result.PVCs[i].Name < result.PVCs[j].Name })
	result.Counts["Deployment"] = len(result.Workloads)
	result.Counts["Service"] = len(result.Services)
	result.Counts["PersistentVolumeClaim"] = len(result.PVCs)
	return result
}

func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
