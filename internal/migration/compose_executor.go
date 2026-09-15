package migration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	kubernetesadapter "github.com/smartx/sks-migration-center/internal/adapter/kubernetes"
	sshadapter "github.com/smartx/sks-migration-center/internal/adapter/ssh"
	composeanalyzer "github.com/smartx/sks-migration-center/internal/compose"
	domainapplication "github.com/smartx/sks-migration-center/internal/domain/application"
	domainenvironment "github.com/smartx/sks-migration-center/internal/domain/environment"
	domainmapping "github.com/smartx/sks-migration-center/internal/domain/mapping"
	domainmigration "github.com/smartx/sks-migration-center/internal/domain/migration"
	domainplatform "github.com/smartx/sks-migration-center/internal/domain/platform"
	"github.com/smartx/sks-migration-center/internal/repository"
	"github.com/smartx/sks-migration-center/internal/transform"
	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8svalidation "k8s.io/apimachinery/pkg/util/validation"
)

const composeSystemNamespace = "sks-migration-system"

type ComposeKubernetesClient interface {
	RunKomposeJob(context.Context, []byte, kubernetesadapter.KomposeJobSpec) ([]byte, error)
	ApplyManifests(context.Context, []byte, kubernetesadapter.ApplyManifestSpec) ([]kubernetesadapter.AppliedResource, error)
	RunKopiaRestoreJob(context.Context, []byte, kubernetesadapter.KopiaRestoreSpec) error
	ScaleWorkloads(context.Context, []byte, []kubernetesadapter.ScalableWorkload) error
	ValidateNamespace(context.Context, []byte, string) (kubernetesadapter.NamespaceValidation, error)
	ValidateEndpoints(context.Context, []byte, kubernetesadapter.EndpointValidationSpec) error
	EnsureMigrationNamespace(context.Context, []byte, string) error
	PutDockerConfigSecret(context.Context, []byte, string, string, []byte) error
}

type ComposeSourceMover interface {
	RunComposeAction(context.Context, string, sshadapter.Credential, sshadapter.ComposeActionSpec) error
	CreateKopiaSnapshot(context.Context, string, sshadapter.Credential, sshadapter.KopiaSnapshotSpec) (sshadapter.KopiaSnapshot, error)
}

type ComposeImagePublisher interface {
	PublishComposeImages(context.Context, string, sshadapter.Credential, sshadapter.ComposeImagePublishSpec) (map[string]string, error)
}

type ComposeRegistryProjectManager interface {
	EnsurePublicProject(context.Context, string) (string, error)
}

type ComposeTransformEngine interface {
	Transform([]byte, domainmapping.Profile) (transform.Result, error)
	Render(transform.Result) ([]byte, error)
}

type ComposeExecutor struct {
	plans                  repository.MigrationPlanRepository
	runs                   repository.MigrationRunRepository
	progress               repository.MigrationProgressRepository
	environments           repository.EnvironmentRepository
	applications           repository.ApplicationRepository
	mappings               repository.MappingRepository
	vault                  ExecutionVault
	kubernetes             ComposeKubernetesClient
	transform              ComposeTransformEngine
	komposeImage           string
	helperImage            string
	kopiaImage             string
	platform               repository.PlatformRepository
	sourceMover            ComposeSourceMover
	imagePublisher         ComposeImagePublisher
	registryProjectManager ComposeRegistryProjectManager
	mirrorAllImages        bool
	imageRepository        string
	registryCredential     sshadapter.RegistryCredential
	registryDockerConfig   []byte
	registryPullSecretName string
}

type ComposeExecutorOption func(*ComposeExecutor) error

func WithComposeDataMovement(platformRepository repository.PlatformRepository, sourceMover ComposeSourceMover, kopiaImage string) ComposeExecutorOption {
	return func(executor *ComposeExecutor) error {
		if platformRepository == nil || sourceMover == nil || !strings.Contains(kopiaImage, "@sha256:") {
			return errors.New("Compose Kopia dependencies and digest-pinned image are required")
		}
		executor.platform, executor.sourceMover, executor.kopiaImage = platformRepository, sourceMover, kopiaImage
		return nil
	}
}

func WithComposeBuildImagePublishing(publisher ComposeImagePublisher, repository string, credential sshadapter.RegistryCredential, dockerConfig []byte, pullSecretName string) ComposeExecutorOption {
	configCopy := append([]byte(nil), dockerConfig...)
	return func(executor *ComposeExecutor) error {
		if publisher == nil || strings.TrimSpace(repository) == "" || strings.TrimSpace(credential.Username) == "" || credential.Password == "" || len(configCopy) == 0 || strings.TrimSpace(pullSecretName) == "" {
			return errors.New("Compose build-image publishing configuration is incomplete")
		}
		executor.imagePublisher = publisher
		executor.imageRepository = strings.TrimSuffix(strings.TrimSpace(repository), "/")
		executor.registryCredential = credential
		executor.registryDockerConfig = append([]byte(nil), configCopy...)
		executor.registryPullSecretName = strings.TrimSpace(pullSecretName)
		return nil
	}
}

func WithComposeRegistryProjectManager(manager ComposeRegistryProjectManager) ComposeExecutorOption {
	return func(executor *ComposeExecutor) error {
		if manager == nil {
			return errors.New("Compose registry project manager is required")
		}
		executor.registryProjectManager = manager
		executor.mirrorAllImages = true
		return nil
	}
}

func NewComposeExecutor(plans repository.MigrationPlanRepository, runs repository.MigrationRunRepository, progress repository.MigrationProgressRepository, environments repository.EnvironmentRepository, applications repository.ApplicationRepository, mappings repository.MappingRepository, vault ExecutionVault, kubernetes ComposeKubernetesClient, engine ComposeTransformEngine, komposeImage, helperImage string, options ...ComposeExecutorOption) (*ComposeExecutor, error) {
	if plans == nil || runs == nil || progress == nil || environments == nil || applications == nil || mappings == nil || vault == nil || kubernetes == nil || engine == nil {
		return nil, errors.New("Compose executor dependencies are required")
	}
	if !strings.Contains(komposeImage, "@sha256:") || !strings.Contains(helperImage, "@sha256:") {
		return nil, errors.New("Compose executor images must be pinned by sha256 digest")
	}
	executor := &ComposeExecutor{
		plans: plans, runs: runs, progress: progress, environments: environments, applications: applications, mappings: mappings,
		vault: vault, kubernetes: kubernetes, transform: engine, komposeImage: komposeImage, helperImage: helperImage,
	}
	for _, option := range options {
		if err := option(executor); err != nil {
			return nil, err
		}
	}
	return executor, nil
}

func (e *ComposeExecutor) Handle(ctx context.Context, lease domainmigration.Lease) error {
	switch lease.StepType {
	case domainmigration.StepPreflight:
		return e.preflight(ctx, lease.RunID)
	case domainmigration.StepPreSync:
		return e.preSync(ctx, lease.RunID)
	case domainmigration.StepQuiesce:
		return e.quiesce(ctx, lease.RunID)
	case domainmigration.StepFinalBackup:
		return e.finalBackup(ctx, lease.RunID)
	case domainmigration.StepTransfer:
		return e.transfer(ctx, lease.RunID)
	case domainmigration.StepTransform:
		return e.transformCandidate(ctx, lease.RunID)
	case domainmigration.StepRestore:
		return e.restore(ctx, lease.RunID)
	case domainmigration.StepValidation:
		return e.validate(ctx, lease.RunID)
	case domainmigration.StepRollback:
		return e.rollback(ctx, lease.RunID)
	default:
		return fmt.Errorf("%w: step %s is not a Compose step", ErrUnsupportedExecution, lease.StepType)
	}
}

type composeExecutionContext struct {
	run             domainmigration.Run
	plan            domainmigration.Plan
	application     domainapplication.SourceApplication
	source          domainenvironment.Environment
	target          domainenvironment.Environment
	mapping         domainmapping.Profile
	definition      domainapplication.ComposeDefinition
	targetKube      []byte
	namespace       string
	publishedImages map[string]string
}

func (e *ComposeExecutor) resolve(ctx context.Context, runID uuid.UUID) (composeExecutionContext, error) {
	run, err := e.runs.GetRun(ctx, runID)
	if err != nil {
		return composeExecutionContext{}, err
	}
	plan, err := e.plans.GetPlan(ctx, run.PlanID)
	if err != nil {
		return composeExecutionContext{}, err
	}
	value, err := e.applications.Get(ctx, plan.SourceApplicationID)
	if err != nil {
		return composeExecutionContext{}, err
	}
	if value.SourceType != domainapplication.SourceCompose || value.DefinitionCredentialID == nil || value.Inventory.Compose == nil {
		return composeExecutionContext{}, fmt.Errorf("%w: registered Compose definition is required", ErrUnsupportedExecution)
	}
	if value.EnvironmentID != plan.SourceEnvironmentID {
		return composeExecutionContext{}, errors.New("Compose application belongs to another source environment")
	}
	source, err := e.environments.Get(ctx, plan.SourceEnvironmentID)
	if err != nil {
		return composeExecutionContext{}, err
	}
	target, err := e.environments.Get(ctx, plan.TargetEnvironmentID)
	if err != nil {
		return composeExecutionContext{}, err
	}
	if source.Kind != domainenvironment.KindDockerCompose || source.Role != domainenvironment.RoleSource || source.Status != domainenvironment.StatusConnected {
		return composeExecutionContext{}, fmt.Errorf("%w: connected Docker Compose source is required", ErrUnsupportedExecution)
	}
	if target.Kind != domainenvironment.KindKubernetes || target.Role != domainenvironment.RoleTarget || target.CredentialID == nil || target.Status != domainenvironment.StatusConnected {
		return composeExecutionContext{}, fmt.Errorf("%w: connected Kubernetes target is required", ErrUnsupportedExecution)
	}
	mapping, err := e.mappings.Get(ctx, plan.MappingProfileID)
	if err != nil {
		return composeExecutionContext{}, err
	}
	if mapping.TargetEnvironmentID != target.ID {
		return composeExecutionContext{}, errors.New("mapping profile belongs to another target environment")
	}
	definitionBytes, err := e.vault.Resolve(ctx, *value.DefinitionCredentialID)
	if err != nil {
		return composeExecutionContext{}, fmt.Errorf("resolve Compose definition: %w", err)
	}
	var definition domainapplication.ComposeDefinition
	if err := json.Unmarshal(definitionBytes, &definition); err != nil {
		wipeBytes(definitionBytes)
		return composeExecutionContext{}, errors.New("stored Compose definition is invalid")
	}
	wipeBytes(definitionBytes)
	targetKube, err := e.vault.Resolve(ctx, *target.CredentialID)
	if err != nil {
		wipeBytes(definition.ComposeYAML)
		wipeBytes(definition.EnvironmentFile)
		return composeExecutionContext{}, err
	}
	namespace := composeTargetNamespace(value.Name, mapping.Namespaces)
	publishedImages, err := e.publishedComposeImages(ctx, runID)
	if err != nil {
		wipeBytes(definition.ComposeYAML)
		wipeBytes(definition.EnvironmentFile)
		wipeBytes(targetKube)
		return composeExecutionContext{}, err
	}
	return composeExecutionContext{run: run, plan: plan, application: value, source: source, target: target, mapping: mapping, definition: definition, targetKube: targetKube, namespace: namespace, publishedImages: publishedImages}, nil
}

func (r *composeExecutionContext) clear() {
	wipeBytes(r.definition.ComposeYAML)
	wipeBytes(r.definition.EnvironmentFile)
	wipeBytes(r.targetKube)
}

func (e *ComposeExecutor) preflight(ctx context.Context, runID uuid.UUID) error {
	resolved, err := e.resolve(ctx, runID)
	if err != nil {
		return err
	}
	defer resolved.clear()
	servicesToPublish := make([]string, 0)
	sourceImages := make(map[string]string)
	for _, service := range resolved.application.Inventory.Compose.Services {
		sourceImages[service.Name] = strings.TrimSpace(service.Image)
		if strings.TrimSpace(resolved.publishedImages[service.Name]) != "" {
			continue
		}
		if e.mirrorAllImages || strings.TrimSpace(service.Image) == "" {
			servicesToPublish = append(servicesToPublish, service.Name)
		}
	}
	if len(servicesToPublish) > 0 {
		if e.imagePublisher == nil {
			return fmt.Errorf("Compose services %s require migration Harbor image publishing", strings.Join(servicesToPublish, ", "))
		}
		repository := e.imageRepository
		if e.registryProjectManager != nil {
			var projectErr error
			repository, projectErr = e.registryProjectManager.EnsurePublicProject(ctx, resolved.application.Inventory.Compose.ProjectName)
			if projectErr != nil {
				if strings.TrimSpace(repository) == "" {
					return fmt.Errorf("prepare application Harbor project before migration: %w", projectErr)
				}
				if err := e.progress.AppendEvent(ctx, domainmigration.Event{RunID: runID, Type: "COMPOSE_HARBOR_PROJECT_FALLBACK", Severity: domainmigration.EventWarning, Message: fmt.Sprintf("Could not create the application Harbor project; using %s", repository), Detail: map[string]any{"repository": repository, "reason": projectErr.Error()}}); err != nil {
					return err
				}
			}
		}
		credential, err := e.resolveSSHCredential(ctx, resolved.source)
		if err != nil {
			return err
		}
		images, err := e.imagePublisher.PublishComposeImages(ctx, resolved.source.Endpoint, credential, sshadapter.ComposeImagePublishSpec{
			RunID: runID.String(), ProjectName: resolved.application.Inventory.Compose.ProjectName,
			Services: servicesToPublish, SourceImages: sourceImages, Repository: repository, Registry: e.registryCredential,
		})
		if err != nil {
			return fmt.Errorf("mirror Compose images to migration Harbor: %w", err)
		}
		for name, image := range resolved.publishedImages {
			images[name] = image
		}
		eventType := "COMPOSE_BUILD_IMAGES_PUBLISHED"
		message := fmt.Sprintf("Published %d local Compose images to the migration Harbor", len(servicesToPublish))
		if e.mirrorAllImages {
			eventType = "COMPOSE_IMAGES_MIRRORED"
			message = fmt.Sprintf("Mirrored %d Compose service images to %s", len(servicesToPublish), repository)
		}
		if err := e.progress.AppendEvent(ctx, domainmigration.Event{RunID: runID, Type: eventType, Severity: domainmigration.EventInfo, Message: message, Detail: map[string]any{"images": images, "services": servicesToPublish, "repository": repository}}); err != nil {
			return err
		}
		resolved.publishedImages = images
	}
	result, err := e.convert(ctx, resolved)
	if err != nil {
		return err
	}
	transfers, err := composeTransfersForExecution(resolved, result)
	if err != nil {
		return err
	}
	if len(transfers) > 0 {
		if resolved.plan.Strategy.VolumeMode != domainmigration.VolumeComposeKopia {
			return errors.New("Compose volumes require the Kopia migration strategy (COMPOSE_KOPIA)")
		}
		if e.platform == nil || e.sourceMover == nil || e.kopiaImage == "" || resolved.source.CredentialID == nil {
			return errors.New("Compose volume migration requires the Kopia data path")
		}
		if _, _, err := e.resolveKopiaRepository(ctx, resolved); err != nil {
			return err
		}
	}
	return e.progress.AppendEvent(ctx, domainmigration.Event{RunID: runID, Type: "COMPOSE_PREFLIGHT_PASSED", Severity: domainmigration.EventInfo, Message: "Kompose conversion and manifest transformation passed", Detail: map[string]any{"namespace": resolved.namespace, "resources": len(result.Documents)}})
}

func (e *ComposeExecutor) preSync(ctx context.Context, runID uuid.UUID) error {
	resolved, err := e.resolve(ctx, runID)
	if err != nil {
		return err
	}
	defer resolved.clear()
	if !hasComposeData(resolved.application) {
		return e.statelessStep(ctx, runID, domainmigration.StepPreSync)
	}
	transfers, err := e.resolveTransfers(ctx, resolved)
	if err != nil {
		return err
	}
	if len(transfers) == 0 {
		return e.statelessStep(ctx, runID, domainmigration.StepPreSync)
	}
	return e.snapshotVolumes(ctx, resolved, transfers, false)
}

func (e *ComposeExecutor) quiesce(ctx context.Context, runID uuid.UUID) error {
	resolved, err := e.resolve(ctx, runID)
	if err != nil {
		return err
	}
	defer resolved.clear()
	if !hasComposeData(resolved.application) {
		return e.statelessStep(ctx, runID, domainmigration.StepQuiesce)
	}
	transfers, err := e.resolveTransfers(ctx, resolved)
	if err != nil {
		return err
	}
	if len(transfers) == 0 {
		return e.statelessStep(ctx, runID, domainmigration.StepQuiesce)
	}
	events, err := e.runs.ListEvents(ctx, runID, 0, 500)
	if err != nil {
		return err
	}
	if eventExists(events, "COMPOSE_SOURCE_STOPPED") {
		return nil
	}
	credential, err := e.resolveSSHCredential(ctx, resolved.source)
	if err != nil {
		return err
	}
	if err := e.sourceMover.RunComposeAction(ctx, resolved.source.Endpoint, credential, sshadapter.ComposeActionSpec{ProjectName: resolved.application.Inventory.Compose.ProjectName, ComposeYAML: resolved.definition.ComposeYAML, EnvironmentFile: resolved.definition.EnvironmentFile, Action: sshadapter.ComposeStop}); err != nil {
		return err
	}
	return e.progress.AppendEvent(ctx, domainmigration.Event{RunID: runID, Type: "COMPOSE_SOURCE_STOPPED", Severity: domainmigration.EventWarning, Message: "Compose services were stopped for the final incremental snapshot"})
}

func (e *ComposeExecutor) finalBackup(ctx context.Context, runID uuid.UUID) error {
	resolved, err := e.resolve(ctx, runID)
	if err != nil {
		return err
	}
	defer resolved.clear()
	if !hasComposeData(resolved.application) {
		return e.statelessStep(ctx, runID, domainmigration.StepFinalBackup)
	}
	transfers, err := e.resolveTransfers(ctx, resolved)
	if err != nil {
		return err
	}
	if len(transfers) == 0 {
		return e.statelessStep(ctx, runID, domainmigration.StepFinalBackup)
	}
	events, err := e.runs.ListEvents(ctx, runID, 0, 500)
	if err != nil {
		return err
	}
	if !eventExists(events, "COMPOSE_SOURCE_STOPPED") {
		return errors.New("Compose source must be stopped before the final Kopia snapshot")
	}
	return e.snapshotVolumes(ctx, resolved, transfers, true)
}

func (e *ComposeExecutor) transfer(ctx context.Context, runID uuid.UUID) error {
	resolved, err := e.resolve(ctx, runID)
	if err != nil {
		return err
	}
	defer resolved.clear()
	if !hasComposeData(resolved.application) {
		return e.statelessStep(ctx, runID, domainmigration.StepTransfer)
	}
	transfers, err := e.resolveTransfers(ctx, resolved)
	if err != nil {
		return err
	}
	if len(transfers) == 0 {
		return e.statelessStep(ctx, runID, domainmigration.StepTransfer)
	}
	snapshots, err := e.finalSnapshots(ctx, runID)
	if err != nil {
		return err
	}
	if len(snapshots) != len(transfers) {
		return errors.New("final Kopia snapshots are incomplete")
	}
	return e.progress.AppendEvent(ctx, domainmigration.Event{RunID: runID, Type: "COMPOSE_TRANSFER_READY", Severity: domainmigration.EventInfo, Message: fmt.Sprintf("%d final Kopia snapshots are ready in target MinIO", len(snapshots))})
}

func (e *ComposeExecutor) statelessStep(ctx context.Context, runID uuid.UUID, step domainmigration.StepType) error {
	return e.progress.AppendEvent(ctx, domainmigration.Event{RunID: runID, Type: "COMPOSE_STATELESS_STEP", Severity: domainmigration.EventInfo, Message: fmt.Sprintf("%s completed without a data transfer for this stateless Compose application", step)})
}

func (e *ComposeExecutor) transformCandidate(ctx context.Context, runID uuid.UUID) error {
	resolved, err := e.resolve(ctx, runID)
	if err != nil {
		return err
	}
	defer resolved.clear()
	result, err := e.convert(ctx, resolved)
	if err != nil {
		return err
	}
	return e.progress.AppendEvent(ctx, domainmigration.Event{RunID: runID, Type: "COMPOSE_TRANSFORMED", Severity: domainmigration.EventInfo, Message: "Kompose candidate manifests were normalized and mapped", Detail: map[string]any{"resources": len(result.Documents), "changed": result.Changed, "namespace": resolved.namespace}})
}

func (e *ComposeExecutor) restore(ctx context.Context, runID uuid.UUID) error {
	resolved, err := e.resolve(ctx, runID)
	if err != nil {
		return err
	}
	defer resolved.clear()
	result, err := e.convert(ctx, resolved)
	if err != nil {
		return err
	}
	if len(resolved.publishedImages) > 0 {
		if err := e.kubernetes.EnsureMigrationNamespace(ctx, resolved.targetKube, resolved.namespace); err != nil {
			return fmt.Errorf("prepare target namespace for private migration images: %w", err)
		}
		if err := e.kubernetes.PutDockerConfigSecret(ctx, resolved.targetKube, resolved.namespace, e.registryPullSecretName, e.registryDockerConfig); err != nil {
			return err
		}
		ensureImagePullSecret(&result, e.registryPullSecretName)
	}
	transfers, err := composeTransfersForExecution(resolved, result)
	if err != nil {
		return err
	}
	var desiredWorkloads []kubernetesadapter.ScalableWorkload
	if len(transfers) > 0 {
		snapshots, snapshotErr := e.finalSnapshots(ctx, runID)
		if snapshotErr != nil {
			return snapshotErr
		}
		for _, transfer := range transfers {
			snapshot, ok := snapshots[transfer.key()]
			if !ok {
				continue
			}
			for i := range result.Documents {
				object := result.Documents[i].Object
				if object.GetKind() != "PersistentVolumeClaim" || object.GetName() != transfer.targetPVC {
					continue
				}
				current, _, _ := unstructured.NestedString(object.Object, "spec", "resources", "requests", "storage")
				quantity, _ := resource.ParseQuantity(current)
				needed := (snapshot.SizeBytes + snapshot.SizeBytes/4 + (1 << 30) - 1) / (1 << 30)
				if needed < 2 {
					needed = 2
				}
				if quantity.Value() < needed*(1<<30) {
					_ = unstructured.SetNestedField(object.Object, fmt.Sprintf("%dGi", needed), "spec", "resources", "requests", "storage")
				}
			}
		}
		desiredWorkloads = pauseComposeWorkloads(&result, resolved.namespace)
	}
	manifests, err := e.transform.Render(result)
	if err != nil {
		return err
	}
	events, err := e.runs.ListEvents(ctx, runID, 0, 500)
	if err != nil {
		wipeBytes(manifests)
		return err
	}
	manifestsWereApplied := eventExists(events, "COMPOSE_MANIFESTS_APPLIED")
	applied, err := e.kubernetes.ApplyManifests(ctx, resolved.targetKube, kubernetesadapter.ApplyManifestSpec{DefaultNamespace: resolved.namespace, Manifests: manifests, Overwrite: resolved.plan.Strategy.OverwriteExistingResources || manifestsWereApplied})
	wipeBytes(manifests)
	if err != nil {
		if appendErr := e.progress.AppendEvent(ctx, domainmigration.Event{RunID: runID, Type: "COMPOSE_MANIFEST_APPLY_PARTIAL", Severity: domainmigration.EventError,
			Message: fmt.Sprintf("Compose manifest apply stopped after %d resources: %v", len(applied), err),
			Detail:  map[string]any{"applied": applied, "appliedCount": len(applied), "failure": err.Error()}}); appendErr != nil {
			return appendErr
		}
		return err
	}
	if !manifestsWereApplied {
		if err := e.progress.AppendEvent(ctx, domainmigration.Event{RunID: runID, Type: "COMPOSE_MANIFESTS_APPLIED", Severity: domainmigration.EventInfo, Message: fmt.Sprintf("Applied %d paused Compose resources before volume restore", len(applied)), Detail: map[string]any{"resources": len(applied), "namespace": resolved.namespace}}); err != nil {
			return err
		}
	}
	if len(transfers) > 0 {
		repositoryConfig, _, err := e.resolveKopiaRepository(ctx, resolved)
		if err != nil {
			return err
		}
		snapshots, err := e.finalSnapshots(ctx, runID)
		if err != nil {
			return err
		}
		for _, transfer := range transfers {
			snapshot, ok := snapshots[transfer.key()]
			if !ok {
				return fmt.Errorf("final Kopia snapshot for %s is missing", transfer.source.Name)
			}
			if err := e.kubernetes.RunKopiaRestoreJob(ctx, resolved.targetKube, kubernetesadapter.KopiaRestoreSpec{
				SingleFile: composePVCIsSingleFile(result, transfer.targetPVC),
				Namespace:  resolved.namespace, RunID: runID.String(), PVC: transfer.targetPVC, SnapshotID: snapshot.ID,
				Image: repositoryConfig.Image, Endpoint: repositoryConfig.Endpoint, Bucket: repositoryConfig.Bucket,
				Region: repositoryConfig.Region, Prefix: repositoryConfig.Prefix, AccessKey: repositoryConfig.AccessKey,
				SecretKey: repositoryConfig.SecretKey, CABundle: repositoryConfig.CABundle, TLSVerify: repositoryConfig.TLSVerify,
				Password: repositoryConfig.Password,
			}); err != nil {
				return err
			}
		}
		if err := e.kubernetes.ScaleWorkloads(ctx, resolved.targetKube, desiredWorkloads); err != nil {
			return fmt.Errorf("start restored Compose workloads: %w", err)
		}
	}
	return e.progress.AppendEvent(ctx, domainmigration.Event{RunID: runID, Type: "COMPOSE_RESTORED", Severity: domainmigration.EventInfo, Message: fmt.Sprintf("Applied %d Compose resources to namespace %s", len(applied), resolved.namespace), Detail: map[string]any{"resources": len(applied), "namespace": resolved.namespace}})
}

func (e *ComposeExecutor) validate(ctx context.Context, runID uuid.UUID) error {
	resolved, err := e.resolve(ctx, runID)
	if err != nil {
		return err
	}
	defer resolved.clear()
	validationTimeout := time.Duration(resolved.plan.ValidationPolicy.TimeoutSeconds) * time.Second
	result, err := waitForNamespaceValidation(ctx, validationTimeout, 2*time.Second, func(validationCtx context.Context) (kubernetesadapter.NamespaceValidation, error) {
		return e.kubernetes.ValidateNamespace(validationCtx, resolved.targetKube, resolved.namespace)
	})
	severity := domainmigration.EventInfo
	message := fmt.Sprintf("Collected validation results for %d Compose target resources", len(result.Results))
	if err != nil {
		severity, message = domainmigration.EventError, fmt.Sprintf("Compose target validation found failures: %v", err)
	}
	if appendErr := e.progress.AppendEvent(ctx, domainmigration.Event{RunID: runID, Type: "COMPOSE_RESOURCE_VALIDATION_COLLECTED", Severity: severity, Message: message, Detail: map[string]any{"namespace": resolved.namespace, "results": result.Results}}); appendErr != nil {
		return appendErr
	}
	if err != nil {
		return err
	}
	if err := e.kubernetes.ValidateEndpoints(ctx, resolved.targetKube, kubernetesadapter.EndpointValidationSpec{Namespace: resolved.namespace, RunID: runID.String(), HTTPChecks: resolved.plan.ValidationPolicy.HTTPChecks, TCPChecks: resolved.plan.ValidationPolicy.TCPChecks, HelperImage: e.helperImage, Timeout: time.Duration(resolved.plan.ValidationPolicy.TimeoutSeconds) * time.Second}); err != nil {
		return err
	}
	return e.progress.AppendEvent(ctx, domainmigration.Event{RunID: runID, Type: "COMPOSE_VALIDATED", Severity: domainmigration.EventInfo, Message: fmt.Sprintf("Compose workloads and configured endpoints are ready in namespace %s", resolved.namespace), Detail: map[string]any{"deployments": result.Deployments, "statefulSets": result.StatefulSets, "pvcs": result.PVCs, "services": result.Services, "ingresses": result.Ingresses, "httpChecks": len(resolved.plan.ValidationPolicy.HTTPChecks), "tcpChecks": len(resolved.plan.ValidationPolicy.TCPChecks)}})
}

func (e *ComposeExecutor) rollback(ctx context.Context, runID uuid.UUID) error {
	resolved, err := e.resolve(ctx, runID)
	if err != nil {
		return err
	}
	defer resolved.clear()
	events, err := e.runs.ListEvents(ctx, runID, 0, 500)
	if err != nil {
		return err
	}
	if !eventExists(events, "COMPOSE_SOURCE_STOPPED") {
		return e.progress.AppendEvent(ctx, domainmigration.Event{RunID: runID, Type: "COMPOSE_ROLLBACK", Severity: domainmigration.EventWarning, Message: "Compose source was not stopped; target resources are retained for diagnosis"})
	}
	if eventExists(events, "COMPOSE_SOURCE_RESTARTED") {
		return nil
	}
	credential, err := e.resolveSSHCredential(ctx, resolved.source)
	if err != nil {
		return err
	}
	if err := e.sourceMover.RunComposeAction(ctx, resolved.source.Endpoint, credential, sshadapter.ComposeActionSpec{ProjectName: resolved.application.Inventory.Compose.ProjectName, ComposeYAML: resolved.definition.ComposeYAML, EnvironmentFile: resolved.definition.EnvironmentFile, Action: sshadapter.ComposeStart}); err != nil {
		return err
	}
	return e.progress.AppendEvent(ctx, domainmigration.Event{RunID: runID, Type: "COMPOSE_SOURCE_RESTARTED", Severity: domainmigration.EventWarning, Message: "Compose services were restarted after migration rollback; target resources were retained for diagnosis"})
}

func (e *ComposeExecutor) convert(ctx context.Context, resolved composeExecutionContext) (transform.Result, error) {
	composeYAML := resolved.definition.ComposeYAML
	if len(resolved.publishedImages) > 0 {
		var err error
		composeYAML, err = composeYAMLWithPublishedImages(composeYAML, resolved.publishedImages)
		if err != nil {
			return transform.Result{}, err
		}
		defer wipeBytes(composeYAML)
	}
	manifests, err := e.kubernetes.RunKomposeJob(ctx, resolved.targetKube, kubernetesadapter.KomposeJobSpec{
		SystemNamespace: composeSystemNamespace, TargetNamespace: resolved.namespace, RunID: resolved.run.ID.String(),
		ComposeYAML: composeYAML, EnvironmentFile: resolved.definition.EnvironmentFile,
		KomposeImage: e.komposeImage, HelperImage: e.helperImage,
	})
	if err != nil {
		return transform.Result{}, err
	}
	defer wipeBytes(manifests)
	result, err := e.transform.Transform(manifests, resolved.mapping)
	if err != nil {
		return result, err
	}
	// Kompose only creates Services for published ports. Compose applications
	// such as Harbor primarily use `expose` for their internal service DNS, so
	// create the missing ClusterIP Services before applying the result.
	inventory := resolved.application.Inventory.Compose
	if analyzed, analyzeErr := composeanalyzer.NewAnalyzer().Analyze(ctx, resolved.application.Name, resolved.definition.ComposeYAML, resolved.definition.EnvironmentFile); analyzeErr == nil {
		inventory = &analyzed
	}
	inventory = composeInventoryWithRawExpose(inventory, resolved.definition.ComposeYAML)
	inventory = composeInventoryForPublishedImages(inventory, resolved.definition.ComposeYAML, resolved.publishedImages)
	ensureComposeInternalServices(&result, inventory, resolved.namespace)
	if err := normalizeSharedComposeClaims(&result, inventory); err != nil {
		return result, err
	}
	inspector, ok := e.sourceMover.(interface {
		InspectBindFiles(context.Context, string, sshadapter.Credential, []string) (map[string]bool, error)
	})
	if !ok || inventory == nil {
		return result, nil
	}
	paths := []string{}
	for _, svc := range inventory.Services {
		for _, mount := range svc.Mounts {
			if mount.Type == "bind" {
				paths = append(paths, mount.Source)
			}
		}
	}
	if len(paths) == 0 {
		return result, nil
	}
	credential, err := e.resolveSSHCredential(ctx, resolved.source)
	if err != nil {
		return result, err
	}
	files, err := inspector.InspectBindFiles(ctx, resolved.source.Endpoint, credential, paths)
	if err != nil {
		return result, err
	}
	for _, svc := range inventory.Services {
		for _, mount := range svc.Mounts {
			if !files[mount.Source] {
				continue
			}
			claim, err := targetClaimForMount(result, svc.Name, mount.Target)
			if err != nil {
				return result, err
			}
			for i := range result.Documents {
				object := result.Documents[i].Object
				if object.GetKind() == "PersistentVolumeClaim" && object.GetName() == claim {
					a := object.GetAnnotations()
					if a == nil {
						a = map[string]string{}
					}
					a["migration.smartx.com/single-file"] = "content"
					object.SetAnnotations(a)
				}
				if object.GetName() != svc.Name || (object.GetKind() != "Deployment" && object.GetKind() != "StatefulSet") {
					continue
				}
				containers, _, _ := unstructured.NestedSlice(object.Object, "spec", "template", "spec", "containers")
				for _, raw := range containers {
					container := raw.(map[string]any)
					mounts, _, _ := unstructured.NestedSlice(container, "volumeMounts")
					for _, rawMount := range mounts {
						m := rawMount.(map[string]any)
						if m["mountPath"] == mount.Target {
							m["subPath"] = "content"
						}
					}
					container["volumeMounts"] = mounts
				}
				_ = unstructured.SetNestedSlice(object.Object, containers, "spec", "template", "spec", "containers")
			}
		}
	}
	return result, nil
}

func (e *ComposeExecutor) publishedComposeImages(ctx context.Context, runID uuid.UUID) (map[string]string, error) {
	events, err := e.runs.ListEvents(ctx, runID, 0, 500)
	if err != nil {
		return nil, err
	}
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].Type != "COMPOSE_IMAGES_MIRRORED" && events[index].Type != "COMPOSE_BUILD_IMAGES_PUBLISHED" {
			continue
		}
		result := map[string]string{}
		switch values := events[index].Detail["images"].(type) {
		case map[string]string:
			for key, value := range values {
				result[key] = value
			}
		case map[string]any:
			for key, raw := range values {
				if value, ok := raw.(string); ok && value != "" {
					result[key] = value
				}
			}
		}
		return result, nil
	}
	return map[string]string{}, nil
}

func composeYAMLWithPublishedImages(contents []byte, images map[string]string) ([]byte, error) {
	var root map[string]any
	if err := yaml.Unmarshal(contents, &root); err != nil {
		return nil, errors.New("stored Compose definition cannot be updated with published images")
	}
	services, ok := root["services"].(map[string]any)
	if !ok {
		return nil, errors.New("stored Compose definition has no services")
	}
	for name, image := range images {
		raw, found := services[name]
		service, valid := raw.(map[string]any)
		if !found || !valid || strings.TrimSpace(image) == "" {
			return nil, fmt.Errorf("published image for Compose service %s cannot be applied", name)
		}
		if _, hasBuild := service["build"]; hasBuild {
			buildContext := composeBuildContext(service["build"])
			service["volumes"] = composeTargetVolumes(service["volumes"], buildContext)
			delete(service, "build")
		}
		service["image"] = image
	}
	result, err := yaml.Marshal(root)
	if err != nil {
		return nil, errors.New("render Compose definition with published images")
	}
	return result, nil
}

func composeBuildContext(value any) string {
	switch build := value.(type) {
	case string:
		return filepath.Clean(build)
	case map[string]any:
		context, _ := build["context"].(string)
		if strings.TrimSpace(context) != "" {
			return filepath.Clean(context)
		}
	}
	return ""
}

func composeTargetVolumes(value any, buildContext string) []any {
	volumes, _ := value.([]any)
	result := make([]any, 0, len(volumes))
	for _, raw := range volumes {
		source, _, mountType := composeMountFields(raw)
		if mountType == "volume" && source == "" {
			continue
		}
		if mountType == "bind" && sameComposePath(source, buildContext) {
			continue
		}
		result = append(result, raw)
	}
	return result
}

func composeMountFields(value any) (source, target, mountType string) {
	switch mount := value.(type) {
	case string:
		parts := strings.Split(mount, ":")
		if len(parts) == 1 {
			return "", strings.TrimSpace(parts[0]), "volume"
		}
		source, target = strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if strings.HasPrefix(source, ".") || filepath.IsAbs(source) {
			mountType = "bind"
		} else {
			mountType = "volume"
		}
		return source, target, mountType
	case map[string]any:
		source, _ = mount["source"].(string)
		target, _ = mount["target"].(string)
		mountType, _ = mount["type"].(string)
		return strings.TrimSpace(source), strings.TrimSpace(target), strings.TrimSpace(mountType)
	default:
		return "", "", ""
	}
}

func sameComposePath(source, buildContext string) bool {
	if strings.TrimSpace(source) == "" || strings.TrimSpace(buildContext) == "" {
		return false
	}
	return filepath.Clean(source) == filepath.Clean(buildContext)
}

func ignoredPublishedBuildMounts(contents []byte, images map[string]string) map[string]map[string]bool {
	result := map[string]map[string]bool{}
	if len(images) == 0 {
		return result
	}
	var root map[string]any
	if yaml.Unmarshal(contents, &root) != nil {
		return result
	}
	services, _ := root["services"].(map[string]any)
	for name := range images {
		service, _ := services[name].(map[string]any)
		buildContext := composeBuildContext(service["build"])
		volumes, _ := service["volumes"].([]any)
		for _, raw := range volumes {
			source, target, mountType := composeMountFields(raw)
			if target == "" {
				continue
			}
			if (mountType == "volume" && source == "") || (mountType == "bind" && sameComposePath(source, buildContext)) {
				if result[name] == nil {
					result[name] = map[string]bool{}
				}
				result[name][target] = true
			}
		}
	}
	return result
}

func composeInventoryForPublishedImages(inventory *domainapplication.ComposeInventory, contents []byte, images map[string]string) *domainapplication.ComposeInventory {
	if inventory == nil || len(images) == 0 {
		return inventory
	}
	ignored := ignoredPublishedBuildMounts(contents, images)
	copy := *inventory
	copy.Services = append([]domainapplication.ComposeService(nil), inventory.Services...)
	for index := range copy.Services {
		service := &copy.Services[index]
		service.Mounts = append([]domainapplication.ComposeMount(nil), service.Mounts...)
		filtered := service.Mounts[:0]
		for _, mount := range service.Mounts {
			if ignored[service.Name][mount.Target] {
				continue
			}
			filtered = append(filtered, mount)
		}
		service.Mounts = filtered
	}
	return &copy
}

func ensureImagePullSecret(result *transform.Result, name string) {
	if result == nil || strings.TrimSpace(name) == "" {
		return
	}
	for index := range result.Documents {
		object := &result.Documents[index].Object
		if object.GetKind() != "Deployment" && object.GetKind() != "StatefulSet" && object.GetKind() != "DaemonSet" && object.GetKind() != "Job" && object.GetKind() != "CronJob" {
			continue
		}
		path := []string{"spec", "template", "spec", "imagePullSecrets"}
		if object.GetKind() == "CronJob" {
			path = []string{"spec", "jobTemplate", "spec", "template", "spec", "imagePullSecrets"}
		}
		values, _, _ := unstructured.NestedSlice(object.Object, path...)
		found := false
		for _, raw := range values {
			item, _ := raw.(map[string]any)
			if item["name"] == name {
				found = true
				break
			}
		}
		if !found {
			values = append(values, map[string]any{"name": name})
			_ = unstructured.SetNestedSlice(object.Object, values, path...)
		}
	}
}

// composeInventoryWithRawExpose keeps internal-port discovery available even
// when the complete Compose analyzer rejects an unrelated external reference.
// It reads only service names and expose declarations and never persists or
// reports environment values from the definition.
func composeInventoryWithRawExpose(inventory *domainapplication.ComposeInventory, contents []byte) *domainapplication.ComposeInventory {
	if inventory == nil {
		return nil
	}
	copy := *inventory
	copy.Services = append([]domainapplication.ComposeService(nil), inventory.Services...)
	var raw struct {
		Services map[string]struct {
			Expose []any `yaml:"expose"`
		} `yaml:"services"`
	}
	if yaml.Unmarshal(contents, &raw) != nil {
		return &copy
	}
	for index := range copy.Services {
		service, ok := raw.Services[copy.Services[index].Name]
		if !ok || len(service.Expose) == 0 {
			continue
		}
		values := make([]string, 0, len(service.Expose))
		for _, value := range service.Expose {
			values = append(values, fmt.Sprint(value))
		}
		copy.Services[index].Expose = values
	}
	return &copy
}

func ensureComposeInternalServices(result *transform.Result, inventory *domainapplication.ComposeInventory, namespace string) {
	if result == nil || inventory == nil {
		return
	}
	existing := map[string]bool{}
	for _, document := range result.Documents {
		if document.Object.GetKind() == "Service" {
			existing[document.Object.GetName()] = true
		}
	}
	services := append([]domainapplication.ComposeService(nil), inventory.Services...)
	sort.Slice(services, func(i, j int) bool { return services[i].Name < services[j].Name })
	for _, service := range services {
		if existing[service.Name] {
			continue
		}
		ports := composeInternalServicePorts(service)
		if len(ports) == 0 {
			continue
		}
		object := unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "Service",
			"metadata": map[string]any{
				"name":      service.Name,
				"namespace": namespace,
				"labels": map[string]any{
					"app.kubernetes.io/managed-by": "sks-migration-center",
					"io.kompose.service":           service.Name,
				},
			},
			"spec": map[string]any{
				"type":     "ClusterIP",
				"selector": map[string]any{"io.kompose.service": service.Name},
				"ports":    ports,
			},
		}}
		result.Documents = append(result.Documents, transform.Document{
			APIVersion: "v1", Kind: "Service", Namespace: namespace, Name: service.Name, Changed: true, Object: object,
		})
		result.Changed++
	}
}

func composeInternalServicePorts(service domainapplication.ComposeService) []any {
	result := []any{}
	seen := map[string]bool{}
	appendPort := func(port uint32, protocol, preferredName string) {
		if port == 0 || port > 65535 {
			return
		}
		protocol = strings.ToUpper(strings.TrimSpace(protocol))
		if protocol != "UDP" && protocol != "SCTP" {
			protocol = "TCP"
		}
		key := fmt.Sprintf("%d/%s", port, protocol)
		if seen[key] {
			return
		}
		seen[key] = true
		name := strings.ToLower(strings.TrimSpace(preferredName))
		if len(k8svalidation.IsDNS1123Label(name)) > 0 {
			name = fmt.Sprintf("port-%d-%s", port, strings.ToLower(protocol))
		}
		result = append(result, map[string]any{"name": name, "port": int64(port), "targetPort": int64(port), "protocol": protocol})
	}
	for _, port := range service.Ports {
		appendPort(port.Target, port.Protocol, port.Name)
	}
	for _, exposed := range service.Expose {
		parts := strings.SplitN(strings.TrimSpace(exposed), "/", 2)
		parsed, err := strconv.ParseUint(parts[0], 10, 16)
		if err != nil {
			continue
		}
		protocol := "tcp"
		if len(parts) == 2 {
			protocol = parts[1]
		}
		appendPort(uint32(parsed), protocol, "")
	}
	return result
}

func composePVCIsSingleFile(result transform.Result, claim string) bool {
	for _, doc := range result.Documents {
		if doc.Object.GetKind() == "PersistentVolumeClaim" && doc.Object.GetName() == claim {
			return doc.Object.GetAnnotations()["migration.smartx.com/single-file"] == "content"
		}
	}
	return false
}

func normalizeSharedComposeClaims(result *transform.Result, inventory *domainapplication.ComposeInventory) error {
	if inventory == nil {
		return nil
	}
	canonical, duplicates := map[string]string{}, map[string]bool{}
	for _, service := range inventory.Services {
		for _, mount := range service.Mounts {
			if mount.Type != "bind" && mount.Type != "volume" {
				continue
			}
			claim, err := targetClaimForMount(*result, service.Name, mount.Target)
			if err != nil {
				return err
			}
			key := mount.Type + "\x00" + filepath.Clean(mount.Source)
			first := canonical[key]
			if first == "" {
				canonical[key] = claim
				continue
			}
			if first == claim {
				continue
			}
			for index := range result.Documents {
				object := result.Documents[index].Object
				if object.GetName() != service.Name || (object.GetKind() != "Deployment" && object.GetKind() != "StatefulSet") {
					continue
				}
				volumes, _, _ := unstructured.NestedSlice(object.Object, "spec", "template", "spec", "volumes")
				for _, raw := range volumes {
					volume, _ := raw.(map[string]any)
					nested, _ := volume["persistentVolumeClaim"].(map[string]any)
					if nested["claimName"] == claim {
						nested["claimName"] = first
					}
				}
				_ = unstructured.SetNestedSlice(object.Object, volumes, "spec", "template", "spec", "volumes")
			}
			duplicates[claim] = true
		}
	}
	if len(duplicates) > 0 {
		documents := result.Documents[:0]
		for _, document := range result.Documents {
			if document.Object.GetKind() == "PersistentVolumeClaim" && duplicates[document.Object.GetName()] {
				continue
			}
			documents = append(documents, document)
		}
		result.Documents = documents
	}
	return nil
}

type composeTransfer struct {
	source    sshadapter.KopiaSource
	targetPVC string
}

func (value composeTransfer) key() string { return value.source.Name + "\x00" + value.targetPVC }

func hasComposeData(value domainapplication.SourceApplication) bool {
	if value.Inventory.Compose == nil {
		return false
	}
	for _, service := range value.Inventory.Compose.Services {
		for _, mount := range service.Mounts {
			if mount.Type == "volume" || mount.Type == "bind" {
				return true
			}
		}
	}
	return len(value.Inventory.PVCs) > 0
}

func (e *ComposeExecutor) resolveTransfers(ctx context.Context, resolved composeExecutionContext) ([]composeTransfer, error) {
	result, err := e.convert(ctx, resolved)
	if err != nil {
		return nil, err
	}
	return composeTransfersForExecution(resolved, result)
}

func composeTransfersForExecution(resolved composeExecutionContext, result transform.Result) ([]composeTransfer, error) {
	inventory := composeInventoryForPublishedImages(resolved.application.Inventory.Compose, resolved.definition.ComposeYAML, resolved.publishedImages)
	return composeTransfers(inventory, result)
}

func composeTransfers(inventory *domainapplication.ComposeInventory, result transform.Result) ([]composeTransfer, error) {
	if inventory == nil {
		return nil, errors.New("Compose inventory is missing")
	}
	runtimeVolumes := map[string]string{}
	for _, volume := range inventory.Volumes {
		runtimeName := volume.RuntimeName
		if runtimeName == "" {
			if volume.External {
				runtimeName = volume.Name
			} else {
				runtimeName = inventory.ProjectName + "_" + volume.Name
			}
		}
		runtimeVolumes[volume.Name] = runtimeName
	}
	unique := map[string]bool{}
	values := make([]composeTransfer, 0)
	for _, service := range inventory.Services {
		for _, mount := range service.Mounts {
			if mount.Type != "volume" && mount.Type != "bind" {
				continue
			}
			claim, err := targetClaimForMount(result, service.Name, mount.Target)
			if err != nil {
				return nil, err
			}
			source := sshadapter.KopiaSource{Name: claim, Type: mount.Type, Path: mount.Source}
			if mount.Type == "volume" {
				if mount.Source == "" {
					return nil, fmt.Errorf("anonymous volume on service %s cannot be mapped to a stable Docker volume", service.Name)
				}
				source.Path = runtimeVolumes[mount.Source]
				if source.Path == "" {
					return nil, fmt.Errorf("Compose volume %s is not declared", mount.Source)
				}
			} else {
				clean := filepath.Clean(mount.Source)
				if !filepath.IsAbs(clean) {
					return nil, fmt.Errorf("relative bind mount %s on service %s is not supported", mount.Source, service.Name)
				}
				source.Path = clean
			}
			value := composeTransfer{source: source, targetPVC: claim}
			if !unique[value.key()] {
				unique[value.key()] = true
				values = append(values, value)
			}
		}
	}
	return values, nil
}

func targetClaimForMount(result transform.Result, service, targetPath string) (string, error) {
	for index := range result.Documents {
		object := result.Documents[index].Object
		if object.GetKind() != "Deployment" && object.GetKind() != "StatefulSet" {
			continue
		}
		if object.GetName() != service && object.GetLabels()["io.kompose.service"] != service {
			continue
		}
		containers, _, _ := unstructured.NestedSlice(object.Object, "spec", "template", "spec", "containers")
		volumeName := ""
		for _, rawContainer := range containers {
			container, _ := rawContainer.(map[string]any)
			mounts, _, _ := unstructured.NestedSlice(container, "volumeMounts")
			for _, rawMount := range mounts {
				mount, _ := rawMount.(map[string]any)
				if path, _ := mount["mountPath"].(string); path == targetPath {
					volumeName, _ = mount["name"].(string)
					break
				}
			}
		}
		volumes, _, _ := unstructured.NestedSlice(object.Object, "spec", "template", "spec", "volumes")
		for _, rawVolume := range volumes {
			volume, _ := rawVolume.(map[string]any)
			if name, _ := volume["name"].(string); name != volumeName {
				continue
			}
			claim, _, _ := unstructured.NestedString(volume, "persistentVolumeClaim", "claimName")
			if claim != "" {
				return claim, nil
			}
		}
	}
	return "", fmt.Errorf("Kompose did not create a PVC for service %s mount %s", service, targetPath)
}

func pauseComposeWorkloads(result *transform.Result, namespace string) []kubernetesadapter.ScalableWorkload {
	values := make([]kubernetesadapter.ScalableWorkload, 0)
	for index := range result.Documents {
		document := &result.Documents[index]
		if document.Kind != "Deployment" && document.Kind != "StatefulSet" {
			continue
		}
		replicas, found, _ := unstructured.NestedInt64(document.Object.Object, "spec", "replicas")
		if !found {
			replicas = 1
		}
		values = append(values, kubernetesadapter.ScalableWorkload{Namespace: namespace, Kind: document.Kind, Name: document.Name, Replicas: int32(replicas)})
		_ = unstructured.SetNestedField(document.Object.Object, int64(0), "spec", "replicas")
	}
	return values
}

func (e *ComposeExecutor) resolveSSHCredential(ctx context.Context, source domainenvironment.Environment) (sshadapter.Credential, error) {
	if source.CredentialID == nil {
		return sshadapter.Credential{}, errors.New("Compose source SSH credential is missing")
	}
	encoded, err := e.vault.Resolve(ctx, *source.CredentialID)
	if err != nil {
		return sshadapter.Credential{}, err
	}
	defer wipeBytes(encoded)
	var value sshadapter.Credential
	if err := json.Unmarshal(encoded, &value); err != nil {
		return sshadapter.Credential{}, errors.New("stored Compose SSH credential is invalid")
	}
	return value, nil
}

func (e *ComposeExecutor) resolveKopiaRepository(ctx context.Context, resolved composeExecutionContext) (sshadapter.KopiaRepository, sshadapter.Credential, error) {
	if e.platform == nil || e.sourceMover == nil {
		return sshadapter.KopiaRepository{}, sshadapter.Credential{}, errors.New("Compose Kopia data movement is not configured")
	}
	installation, err := e.platform.GetAddonInstallation(ctx, resolved.target.ID, domainplatform.AddonMinIO)
	if err != nil || installation.Status != domainplatform.InstallationReady {
		return sshadapter.KopiaRepository{}, sshadapter.Credential{}, errors.New("target MinIO installation is not ready")
	}
	profileValue, ok := installation.Values["profileId"].(string)
	profileID, parseErr := uuid.Parse(profileValue)
	if !ok || parseErr != nil {
		return sshadapter.KopiaRepository{}, sshadapter.Credential{}, errors.New("target MinIO profile reference is invalid")
	}
	profile, err := e.platform.GetObjectStorageProfile(ctx, profileID)
	if err != nil {
		return sshadapter.KopiaRepository{}, sshadapter.Credential{}, err
	}
	encoded, err := e.vault.Resolve(ctx, profile.CredentialID)
	if err != nil {
		return sshadapter.KopiaRepository{}, sshadapter.Credential{}, err
	}
	defer wipeBytes(encoded)
	var credential struct {
		AccessKey string `json:"accessKey"`
		SecretKey string `json:"secretKey"`
		CABundle  string `json:"caBundle"`
	}
	if err := json.Unmarshal(encoded, &credential); err != nil {
		return sshadapter.KopiaRepository{}, sshadapter.Credential{}, errors.New("stored object storage credential is invalid")
	}
	sshCredential, err := e.resolveSSHCredential(ctx, resolved.source)
	if err != nil {
		return sshadapter.KopiaRepository{}, sshadapter.Credential{}, err
	}
	return sshadapter.KopiaRepository{
		Image: e.kopiaImage, Endpoint: profile.Endpoint, Bucket: profile.Bucket, Region: profile.Region,
		Prefix: "compose/" + resolved.application.ID.String(), AccessKey: credential.AccessKey, SecretKey: credential.SecretKey,
		CABundle: credential.CABundle, TLSVerify: profile.TLSVerify, Password: credential.SecretKey,
	}, sshCredential, nil
}

func (e *ComposeExecutor) snapshotVolumes(ctx context.Context, resolved composeExecutionContext, transfers []composeTransfer, final bool) error {
	repositoryConfig, credential, err := e.resolveKopiaRepository(ctx, resolved)
	if err != nil {
		return err
	}
	existing, err := e.runs.ListEvents(ctx, resolved.run.ID, 0, 500)
	if err != nil {
		return err
	}
	completed := snapshotKeys(existing, final)
	var total int64
	for _, transfer := range transfers {
		if completed[transfer.key()] {
			continue
		}
		phase := "presync"
		if final {
			phase = "final"
		}
		snapshot, err := e.sourceMover.CreateKopiaSnapshot(ctx, resolved.source.Endpoint, credential, sshadapter.KopiaSnapshotSpec{RunID: resolved.run.ID.String(), Phase: phase, Repository: repositoryConfig, Source: transfer.source})
		if err != nil {
			return fmt.Errorf("snapshot Compose volume %s: %w", transfer.source.Name, err)
		}
		total += snapshot.SizeBytes
		if err := e.progress.UpsertVolumeTransfers(ctx, resolved.run.ID, []domainmigration.VolumeTransfer{{
			ID: uuid.NewSHA1(resolved.run.ID, []byte(transfer.key())), RunID: resolved.run.ID, Engine: domainmigration.TransferComposeKopia,
			Namespace: resolved.namespace, SourceVolume: transfer.source.Name, TargetVolume: transfer.targetPVC,
			TotalBytes: snapshot.SizeBytes, TransferredBytes: snapshot.SizeBytes, ChecksumStatus: "SNAPSHOT_COMPLETE", Status: domainmigration.TransferCompleted,
		}}); err != nil {
			return err
		}
		if err := e.progress.AppendEvent(ctx, domainmigration.Event{RunID: resolved.run.ID, Type: "COMPOSE_KOPIA_SNAPSHOT", Severity: domainmigration.EventInfo, Message: fmt.Sprintf("Kopia snapshot completed for %s", transfer.source.Name), Detail: map[string]any{
			"source": transfer.source.Name, "targetPvc": transfer.targetPVC, "snapshotId": snapshot.ID, "sizeBytes": snapshot.SizeBytes, "files": snapshot.Files, "final": final,
		}}); err != nil {
			return err
		}
	}
	if total > 0 {
		if err := e.progress.UpdateRunBytes(ctx, resolved.run.ID, total, total); err != nil {
			return err
		}
	}
	phase := "online pre-sync"
	if final {
		phase = "final stopped-source sync"
	}
	return e.progress.AppendEvent(ctx, domainmigration.Event{RunID: resolved.run.ID, Type: "COMPOSE_KOPIA_PHASE_COMPLETED", Severity: domainmigration.EventInfo, Message: fmt.Sprintf("Kopia %s completed for %d volumes", phase, len(transfers)), Detail: map[string]any{"final": final, "volumes": len(transfers)}})
}

func (e *ComposeExecutor) finalSnapshots(ctx context.Context, runID uuid.UUID) (map[string]sshadapter.KopiaSnapshot, error) {
	events, err := e.runs.ListEvents(ctx, runID, 0, 500)
	if err != nil {
		return nil, err
	}
	result := map[string]sshadapter.KopiaSnapshot{}
	for _, event := range events {
		if event.Type != "COMPOSE_KOPIA_SNAPSHOT" || event.Detail["final"] != true {
			continue
		}
		source, _ := event.Detail["source"].(string)
		target, _ := event.Detail["targetPvc"].(string)
		id, _ := event.Detail["snapshotId"].(string)
		size, _ := event.Detail["sizeBytes"].(float64)
		if source != "" && target != "" && id != "" {
			result[source+"\x00"+target] = sshadapter.KopiaSnapshot{ID: id, SizeBytes: int64(size)}
		}
	}
	return result, nil
}

func snapshotKeys(events []domainmigration.Event, final bool) map[string]bool {
	result := map[string]bool{}
	for _, event := range events {
		if event.Type == "COMPOSE_KOPIA_SNAPSHOT" && event.Detail["final"] == final {
			source, _ := event.Detail["source"].(string)
			target, _ := event.Detail["targetPvc"].(string)
			result[source+"\x00"+target] = source != "" && target != ""
		}
	}
	return result
}

func eventExists(events []domainmigration.Event, eventType string) bool {
	for _, event := range events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}

func composeTargetNamespace(project string, mappings []domainmapping.KeyValue) string {
	for _, value := range mappings {
		if value.Source == project && len(k8svalidation.IsDNS1123Label(value.Target)) == 0 {
			return value.Target
		}
	}
	value := strings.ToLower(strings.TrimSpace(project))
	var result strings.Builder
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-' {
			result.WriteRune(character)
		} else {
			result.WriteByte('-')
		}
	}
	value = strings.Trim(result.String(), "-")
	if len(value) > 63 {
		value = strings.TrimRight(value[:63], "-")
	}
	if value == "" {
		return "migration"
	}
	return value
}
