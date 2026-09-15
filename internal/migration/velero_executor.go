package migration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	kubernetesadapter "github.com/smartx/sks-migration-center/internal/adapter/kubernetes"
	veleroadapter "github.com/smartx/sks-migration-center/internal/adapter/velero"
	domainapplication "github.com/smartx/sks-migration-center/internal/domain/application"
	domainenvironment "github.com/smartx/sks-migration-center/internal/domain/environment"
	domainmapping "github.com/smartx/sks-migration-center/internal/domain/mapping"
	domainmigration "github.com/smartx/sks-migration-center/internal/domain/migration"
	"github.com/smartx/sks-migration-center/internal/repository"
	veleroservice "github.com/smartx/sks-migration-center/internal/velero"
)

var ErrUnsupportedExecution = errors.New("unsupported migration execution")

type ExecutionVault interface {
	Resolve(context.Context, uuid.UUID) ([]byte, error)
}

type VeleroExecutionClient interface {
	BackupStorageLocationStatus(context.Context, []byte, string, string) (veleroadapter.BackupStorageLocationStatus, error)
	CreateBackup(context.Context, []byte, veleroadapter.BackupSpec) (veleroadapter.BackupStatus, error)
	BackupStatus(context.Context, []byte, string, string) (veleroadapter.BackupStatus, error)
	VolumeTransfers(context.Context, []byte, string, string) ([]veleroadapter.VolumeTransfer, error)
	DataUploads(context.Context, []byte, string, string) ([]veleroadapter.VolumeTransfer, error)
	DataDownloads(context.Context, []byte, string, string) ([]veleroadapter.VolumeTransfer, error)
	CreateRestore(context.Context, []byte, veleroadapter.RestoreSpec) (veleroadapter.RestoreStatus, error)
	RestoreStatus(context.Context, []byte, string, string) (veleroadapter.RestoreStatus, error)
}

type KubernetesExecutionClient interface {
	PreparePVCStaging(context.Context, []byte, kubernetesadapter.PVCStagingSpec) ([]kubernetesadapter.PVCStagingPod, error)
	DeletePVCStaging(context.Context, []byte, string, string) error
	ListScalableWorkloads(context.Context, []byte, string) ([]kubernetesadapter.ScalableWorkload, error)
	ScaleWorkloads(context.Context, []byte, []kubernetesadapter.ScalableWorkload) error
	EnsureVeleroStorageClassMappings(context.Context, []byte, string, string, map[string]string) error
	DeleteVeleroStorageClassMappings(context.Context, []byte, string, string) error
	ApplyPostRestoreMappings(context.Context, []byte, string, domainmapping.Profile) (kubernetesadapter.PostRestoreMappingResult, error)
	EnsureMigrationNamespace(context.Context, []byte, string) error
	ValidateNamespace(context.Context, []byte, string) (kubernetesadapter.NamespaceValidation, error)
	ValidateEndpoints(context.Context, []byte, kubernetesadapter.EndpointValidationSpec) error
	CSIDataMoverCapabilities(context.Context, []byte, string) (domainenvironment.CSIDataMoverCapabilities, error)
}

type KubernetesResourceSelectionClient interface {
	LabelResources(context.Context, []byte, []domainapplication.ResourceReference, string, string) error
}

const resourceSelectionLabel = "migration.smartx.com/selection"

type VeleroExecutorOption func(*VeleroExecutor) error

func WithKubernetesExecution(client KubernetesExecutionClient, stagingImage string) VeleroExecutorOption {
	return func(executor *VeleroExecutor) error {
		if client == nil || strings.TrimSpace(stagingImage) == "" || !strings.Contains(stagingImage, "@sha256:") {
			return errors.New("Kubernetes execution client and digest-pinned staging image are required")
		}
		executor.kubernetes, executor.stagingImage = client, stagingImage
		return nil
	}
}

type VeleroExecutor struct {
	plans        repository.MigrationPlanRepository
	runs         repository.MigrationRunRepository
	progress     repository.MigrationProgressRepository
	environments repository.EnvironmentRepository
	applications repository.ApplicationRepository
	mappings     repository.MappingRepository
	vault        ExecutionVault
	velero       VeleroExecutionClient
	kubernetes   KubernetesExecutionClient
	stagingImage string
	pollInterval time.Duration
}

func NewVeleroExecutor(plans repository.MigrationPlanRepository, runs repository.MigrationRunRepository, progress repository.MigrationProgressRepository, environments repository.EnvironmentRepository, applications repository.ApplicationRepository, mappings repository.MappingRepository, vault ExecutionVault, velero VeleroExecutionClient, options ...VeleroExecutorOption) (*VeleroExecutor, error) {
	if plans == nil || runs == nil || progress == nil || environments == nil || applications == nil || mappings == nil || vault == nil || velero == nil {
		return nil, errors.New("Velero executor repositories, vault and client are required")
	}
	executor := &VeleroExecutor{
		plans: plans, runs: runs, progress: progress, environments: environments, applications: applications,
		mappings: mappings, vault: vault, velero: velero, pollInterval: 2 * time.Second,
	}
	for _, option := range options {
		if err := option(executor); err != nil {
			return nil, err
		}
	}
	return executor, nil
}

func (e *VeleroExecutor) Handle(ctx context.Context, lease domainmigration.Lease) error {
	switch lease.StepType {
	case domainmigration.StepPreflight:
		return e.preflight(ctx, lease.RunID)
	case domainmigration.StepPreSync:
		return e.backup(ctx, lease.RunID, "presync")
	case domainmigration.StepQuiesce:
		return e.quiesce(ctx, lease.RunID)
	case domainmigration.StepFinalBackup:
		return e.backup(ctx, lease.RunID, "final")
	case domainmigration.StepTransfer:
		return e.verifyTransfer(ctx, lease.RunID)
	case domainmigration.StepTransform:
		return e.prepareTransform(ctx, lease.RunID)
	case domainmigration.StepRestore:
		return e.restore(ctx, lease.RunID)
	case domainmigration.StepValidation:
		return e.validateTarget(ctx, lease.RunID)
	case domainmigration.StepRollback:
		return e.rollback(ctx, lease.RunID)
	default:
		return fmt.Errorf("%w: step %s is not a Velero step", ErrUnsupportedExecution, lease.StepType)
	}
}

func (e *VeleroExecutor) preflight(ctx context.Context, runID uuid.UUID) error {
	resolved, err := e.resolve(ctx, runID)
	if err != nil {
		return err
	}
	defer resolved.clear()
	for _, cluster := range []struct {
		name       string
		kubeconfig []byte
	}{{"source", resolved.sourceKube}, {"target", resolved.targetKube}} {
		status, err := e.velero.BackupStorageLocationStatus(ctx, cluster.kubeconfig, veleroservice.Namespace, veleroservice.BackupLocationName)
		if err != nil {
			return fmt.Errorf("read %s Velero BackupStorageLocation: %w", cluster.name, err)
		}
		if status.Phase != "Available" {
			return fmt.Errorf("%s Velero BackupStorageLocation is %s: %s", cluster.name, status.Phase, status.Message)
		}
	}
	if resolved.plan.Strategy.VolumeMode == domainmigration.VolumeCSIDataMover {
		if e.kubernetes == nil {
			return fmt.Errorf("%w: Kubernetes CSI Data Mover probe is not configured", ErrUnsupportedExecution)
		}
		sourceRuntime, err := e.kubernetes.CSIDataMoverCapabilities(ctx, resolved.sourceKube, veleroservice.Namespace)
		if err != nil {
			return fmt.Errorf("probe source CSI Data Mover: %w", err)
		}
		if !sourceRuntime.BackupReady {
			return errors.New("source CSI Data Mover runtime is not ready")
		}
		targetRuntime, err := e.kubernetes.CSIDataMoverCapabilities(ctx, resolved.targetKube, veleroservice.Namespace)
		if err != nil {
			return fmt.Errorf("probe target CSI Data Mover: %w", err)
		}
		if !targetRuntime.RestoreReady {
			return errors.New("target CSI Data Mover runtime is not ready")
		}
		for _, volume := range resolved.application.Inventory.PVCs {
			if _, found := resolved.source.Capabilities.SnapshotDriverForStorageClass(volume.StorageClassName); !found {
				return fmt.Errorf("PVC %s has no VolumeSnapshotClass matching its StorageClass provisioner", volume.Name)
			}
			if strings.EqualFold(volume.VolumeMode, "Block") && !resolved.source.Capabilities.SupportsRawBlockDataMover() {
				return fmt.Errorf("PVC %s is Raw Block but the source environment is not confirmed Linux-only", volume.Name)
			}
		}
	}
	return e.progress.AppendEvent(ctx, domainmigration.Event{
		RunID: runID, Type: "EXECUTION_PREFLIGHT_PASSED", Severity: domainmigration.EventInfo,
		Message: "Source and target Velero BackupStorageLocations are Available",
	})
}

type executionContext struct {
	run         domainmigration.Run
	plan        domainmigration.Plan
	application domainapplication.SourceApplication
	source      domainenvironment.Environment
	target      domainenvironment.Environment
	mapping     domainmapping.Profile
	sourceKube  []byte
	targetKube  []byte
}

func (e *VeleroExecutor) resolve(ctx context.Context, runID uuid.UUID) (executionContext, error) {
	run, err := e.runs.GetRun(ctx, runID)
	if err != nil {
		return executionContext{}, err
	}
	plan, err := e.plans.GetPlan(ctx, run.PlanID)
	if err != nil {
		return executionContext{}, err
	}
	application, err := e.applications.Get(ctx, plan.SourceApplicationID)
	if err != nil {
		return executionContext{}, err
	}
	if application.SourceType != domainapplication.SourceKubernetes || application.Namespace == "" {
		return executionContext{}, fmt.Errorf("%w: Velero requires a Kubernetes Namespace source", ErrUnsupportedExecution)
	}
	source, err := e.environments.Get(ctx, plan.SourceEnvironmentID)
	if err != nil {
		return executionContext{}, err
	}
	target, err := e.environments.Get(ctx, plan.TargetEnvironmentID)
	if err != nil {
		return executionContext{}, err
	}
	mapping, err := resolveMappingProfile(ctx, e.mappings, plan)
	if err != nil {
		return executionContext{}, err
	}
	if source.CredentialID == nil || target.CredentialID == nil {
		return executionContext{}, fmt.Errorf("%w: source and target kubeconfig are required", ErrUnsupportedExecution)
	}
	sourceKube, err := e.vault.Resolve(ctx, *source.CredentialID)
	if err != nil {
		return executionContext{}, err
	}
	targetKube, err := e.vault.Resolve(ctx, *target.CredentialID)
	if err != nil {
		wipeBytes(sourceKube)
		return executionContext{}, err
	}
	return executionContext{run: run, plan: plan, application: application, source: source, target: target, mapping: mapping, sourceKube: sourceKube, targetKube: targetKube}, nil
}

func (e *VeleroExecutor) backup(ctx context.Context, runID uuid.UUID, stage string) error {
	resolved, err := e.resolve(ctx, runID)
	if err != nil {
		return err
	}
	defer resolved.clear()
	cleanupStaging := false
	selectionValue := runID.String()
	stagingLabels := map[string]string(nil)
	if manualResourceSelection(resolved.application.Inventory) {
		stagingLabels = map[string]string{resourceSelectionLabel: selectionValue}
	}
	if resolved.plan.Strategy.VolumeMode == domainmigration.VolumeFSBackup && len(resolved.application.Inventory.PVCs) > 0 {
		if e.kubernetes == nil {
			return fmt.Errorf("%w: Kubernetes PVC staging is not configured", ErrUnsupportedExecution)
		}
		pvcNames := make([]string, 0, len(resolved.application.Inventory.PVCs))
		for _, volume := range resolved.application.Inventory.PVCs {
			pvcNames = append(pvcNames, volume.Name)
		}
		if _, err := e.kubernetes.PreparePVCStaging(ctx, resolved.sourceKube, kubernetesadapter.PVCStagingSpec{
			Namespace: resolved.application.Namespace, RunID: runID.String(), PVCNames: pvcNames, HelperImage: e.stagingImage, Labels: stagingLabels,
		}); err != nil {
			return err
		}
		cleanupStaging = true
		defer func() {
			if cleanupStaging {
				_ = e.kubernetes.DeletePVCStaging(context.WithoutCancel(ctx), resolved.sourceKube, resolved.application.Namespace, runID.String())
			}
		}()
	}
	name := backupName(runID, stage)
	labelSelector := map[string]string(nil)
	if manualResourceSelection(resolved.application.Inventory) {
		selectorClient, ok := e.kubernetes.(KubernetesResourceSelectionClient)
		if !ok {
			return fmt.Errorf("%w: exact Kubernetes resource selection is unavailable", ErrUnsupportedExecution)
		}
		if err := selectorClient.LabelResources(ctx, resolved.sourceKube, inventoryReferences(resolved.application.Inventory), resourceSelectionLabel, selectionValue); err != nil {
			return err
		}
		labelSelector = map[string]string{resourceSelectionLabel: selectionValue}
	}
	status, err := e.velero.CreateBackup(ctx, resolved.sourceKube, veleroadapter.BackupSpec{
		Namespace: veleroservice.Namespace, Name: name, StorageLocation: veleroservice.BackupLocationName,
		IncludedNamespaces: []string{resolved.application.Namespace}, PlanID: resolved.plan.ID.String(), RunID: runID.String(), TTL: 7 * 24 * time.Hour,
		SnapshotMoveData: resolved.plan.Strategy.VolumeMode == domainmigration.VolumeCSIDataMover, DataMover: "velero",
		LabelSelector: labelSelector,
	})
	if err != nil {
		return err
	}
	lastBytes := int64(-1)
	for {
		transferred, total, progressErr := e.recordTransfers(ctx, resolved, name)
		if progressErr != nil {
			return progressErr
		}
		if transferred != lastBytes {
			lastBytes = transferred
			_ = e.progress.AppendEvent(ctx, domainmigration.Event{
				RunID: runID, Type: "VOLUME_TRANSFER_PROGRESS", Severity: domainmigration.EventInfo,
				Message: fmt.Sprintf("Velero %s volume progress %d/%d bytes", stage, transferred, total),
				Detail:  map[string]any{"stage": stage, "backup": name, "bytesTransferred": transferred, "bytesTotal": total},
			})
		}
		switch status.Phase {
		case "Completed":
			if status.Errors > 0 {
				return fmt.Errorf("Velero Backup completed with %d errors", status.Errors)
			}
			if e.kubernetes != nil {
				if err := e.kubernetes.DeletePVCStaging(ctx, resolved.sourceKube, resolved.application.Namespace, runID.String()); err != nil {
					return err
				}
				cleanupStaging = false
			}
			return nil
		case "Failed", "FailedValidation", "PartiallyFailed":
			return fmt.Errorf("Velero Backup %s: errors=%d warnings=%d message=%s", status.Phase, status.Errors, status.Warnings, status.Message)
		}
		if err := wait(ctx, e.pollInterval); err != nil {
			return err
		}
		status, err = e.velero.BackupStatus(ctx, resolved.sourceKube, veleroservice.Namespace, name)
		if err != nil {
			return err
		}
	}
}

func (e *VeleroExecutor) quiesce(ctx context.Context, runID uuid.UUID) error {
	resolved, err := e.resolve(ctx, runID)
	if err != nil {
		return err
	}
	defer resolved.clear()
	if len(resolved.application.Inventory.PVCs) == 0 {
		return e.progress.AppendEvent(ctx, domainmigration.Event{RunID: runID, Type: "SOURCE_QUIESCE_NOT_REQUIRED", Severity: domainmigration.EventInfo, Message: "所选应用没有 PVC，无需停机同步卷数据，源业务保持运行，目标验证完成后由管理员切流。"})
	}
	if e.kubernetes == nil {
		return fmt.Errorf("%w: Kubernetes workload scaling is not configured", ErrUnsupportedExecution)
	}
	workloads, err := e.kubernetes.ListScalableWorkloads(ctx, resolved.sourceKube, resolved.application.Namespace)
	if err != nil {
		return err
	}
	if manualResourceSelection(resolved.application.Inventory) {
		selected := map[string]bool{}
		for _, resource := range resolved.application.Inventory.Workloads {
			selected[strings.ToLower(resource.Kind)+"\x00"+resource.Name] = true
		}
		filtered := make([]kubernetesadapter.ScalableWorkload, 0)
		for _, workload := range workloads {
			if selected[strings.ToLower(workload.Kind)+"\x00"+workload.Name] {
				filtered = append(filtered, workload)
			}
		}
		workloads = filtered
	}
	snapshots := make([]domainmigration.WorkloadReplicaSnapshot, 0, len(workloads))
	stopped := make([]kubernetesadapter.ScalableWorkload, 0, len(workloads))
	for _, workload := range workloads {
		snapshots = append(snapshots, domainmigration.WorkloadReplicaSnapshot{
			RunID: runID, Namespace: workload.Namespace, Kind: workload.Kind, Name: workload.Name, Replicas: workload.Replicas,
		})
		workload.Replicas = 0
		stopped = append(stopped, workload)
	}
	if err := e.progress.SaveWorkloadReplicaSnapshots(ctx, runID, snapshots); err != nil {
		return err
	}
	if err := e.kubernetes.ScaleWorkloads(ctx, resolved.sourceKube, stopped); err != nil {
		return err
	}
	return e.progress.AppendEvent(ctx, domainmigration.Event{
		RunID: runID, Type: "SOURCE_QUIESCED", Severity: domainmigration.EventInfo,
		Message: fmt.Sprintf("Source workloads scaled to zero in namespace %s", resolved.application.Namespace),
		Detail:  map[string]any{"namespace": resolved.application.Namespace, "workloads": len(stopped)},
	})
}

func manualResourceSelection(inventory domainapplication.Inventory) bool {
	for _, warning := range inventory.Warnings {
		if warning.Code == "MANUAL_RESOURCE_SELECTION" {
			return true
		}
	}
	return false
}

func inventoryReferences(inventory domainapplication.Inventory) []domainapplication.ResourceReference {
	result := make([]domainapplication.ResourceReference, 0, len(inventory.Resources))
	for _, resource := range inventory.Resources {
		result = append(result, domainapplication.ResourceReference{APIVersion: resource.APIVersion, Kind: resource.Kind, Namespace: resource.Namespace, Name: resource.Name})
	}
	return result
}

func (e *VeleroExecutor) rollback(ctx context.Context, runID uuid.UUID) error {
	resolved, err := e.resolve(ctx, runID)
	if err != nil {
		return err
	}
	defer resolved.clear()
	if e.kubernetes == nil {
		return fmt.Errorf("%w: Kubernetes workload scaling is not configured", ErrUnsupportedExecution)
	}
	snapshots, err := e.progress.ListWorkloadReplicaSnapshots(ctx, runID)
	if err != nil {
		return err
	}
	if len(snapshots) == 0 {
		if len(resolved.application.Inventory.PVCs) == 0 {
			return e.progress.AppendEvent(ctx, domainmigration.Event{RunID: runID, Type: "SOURCE_ROLLBACK_NOT_REQUIRED", Severity: domainmigration.EventInfo, Message: "无卷迁移没有停止源业务，无需恢复源端副本。"})
		}
		return errors.New("source workload replica snapshot is missing")
	}
	if err := e.kubernetes.ScaleWorkloads(ctx, resolved.sourceKube, scalableWorkloads(snapshots, "")); err != nil {
		return err
	}
	_ = e.kubernetes.DeletePVCStaging(context.WithoutCancel(ctx), resolved.sourceKube, resolved.application.Namespace, runID.String())
	return e.progress.AppendEvent(ctx, domainmigration.Event{
		RunID: runID, Type: "SOURCE_ROLLBACK_COMPLETED", Severity: domainmigration.EventInfo,
		Message: "Source workload replicas were restored", Detail: map[string]any{"workloads": len(snapshots)},
	})
}

func (e *VeleroExecutor) verifyTransfer(ctx context.Context, runID uuid.UUID) error {
	resolved, err := e.resolve(ctx, runID)
	if err != nil {
		return err
	}
	defer resolved.clear()
	name := backupName(runID, "final")
	status, err := e.velero.BackupStatus(ctx, resolved.sourceKube, veleroservice.Namespace, name)
	if err != nil {
		return err
	}
	if status.Phase != "Completed" || status.Errors > 0 {
		return fmt.Errorf("final Velero Backup is not transferable: phase=%s errors=%d", status.Phase, status.Errors)
	}
	_, _, err = e.recordTransfers(ctx, resolved, name)
	return err
}

func (e *VeleroExecutor) prepareTransform(ctx context.Context, runID uuid.UUID) error {
	resolved, err := e.resolve(ctx, runID)
	if err != nil {
		return err
	}
	defer resolved.clear()
	if e.kubernetes == nil {
		return fmt.Errorf("%w: Kubernetes restore transform is not configured", ErrUnsupportedExecution)
	}
	mappings := make(map[string]string, len(resolved.mapping.Storage))
	for _, value := range resolved.mapping.Storage {
		mappings[value.Source] = value.Target
	}
	if err := e.kubernetes.EnsureVeleroStorageClassMappings(ctx, resolved.targetKube, veleroservice.Namespace, runID.String(), mappings); err != nil {
		return err
	}
	return e.progress.AppendEvent(ctx, domainmigration.Event{
		RunID: runID, Type: "RESTORE_TRANSFORM_PREPARED", Severity: domainmigration.EventInfo,
		Message: fmt.Sprintf("Prepared %d StorageClass mappings for Velero restore", len(mappings)),
		Detail:  map[string]any{"storageClassMappings": len(mappings), "registryMappingsPendingPostRestore": len(resolved.mapping.Registries)},
	})
}

func (e *VeleroExecutor) restore(ctx context.Context, runID uuid.UUID) error {
	resolved, err := e.resolve(ctx, runID)
	if err != nil {
		return err
	}
	defer resolved.clear()
	backup := backupName(runID, "final")
	if err := e.waitForSyncedBackup(ctx, resolved.targetKube, backup); err != nil {
		return err
	}
	targetNamespace := resolved.application.Namespace
	mappings := map[string]string{}
	for _, value := range resolved.mapping.Namespaces {
		if value.Source == resolved.application.Namespace {
			targetNamespace = value.Target
			mappings[value.Source] = value.Target
		}
	}
	if e.kubernetes != nil {
		if err := e.kubernetes.EnsureMigrationNamespace(ctx, resolved.targetKube, targetNamespace); err != nil {
			return err
		}
	}
	var excludedResources []string
	if len(resolved.application.Inventory.PVCs) == 0 {
		// Recreate controller-owned Pods on the target; source admission-injected
		// runtime fields (e.g. hostPath timezone mounts) are not portable manifests.
		excludedResources = []string{"pods", "replicasets.apps", "events", "events.events.k8s.io"}
	}
	status, err := e.velero.CreateRestore(ctx, resolved.targetKube, veleroadapter.RestoreSpec{
		ExcludedResources: excludedResources,
		Namespace:         veleroservice.Namespace, Name: restoreName(runID), BackupName: backup,
		IncludedNamespaces: []string{resolved.application.Namespace}, NamespaceMappings: mappings,
		PlanID: resolved.plan.ID.String(), RunID: runID.String(), RestorePVs: resolved.plan.Strategy.VolumeMode != domainmigration.VolumeNone,
	})
	if err != nil {
		return err
	}
	for {
		if resolved.plan.Strategy.VolumeMode == domainmigration.VolumeCSIDataMover {
			if _, _, err := e.recordDataMoverDownloads(ctx, resolved, restoreName(runID)); err != nil {
				return err
			}
		}
		switch status.Phase {
		case "Completed":
			if status.Errors > 0 {
				return fmt.Errorf("Velero Restore completed with %d errors", status.Errors)
			}
			if e.kubernetes != nil {
				mappingResult, err := e.kubernetes.ApplyPostRestoreMappings(ctx, resolved.targetKube, targetNamespace, resolved.mapping)
				if err != nil {
					return err
				}
				if err := e.progress.AppendEvent(ctx, domainmigration.Event{
					RunID: runID, Type: "POST_RESTORE_MAPPINGS_APPLIED", Severity: domainmigration.EventInfo,
					Message: fmt.Sprintf("Applied and verified mutable mappings on %d target resources", mappingResult.Verified),
					Detail:  map[string]any{"examined": mappingResult.Examined, "updated": mappingResult.Updated, "verified": mappingResult.Verified},
				}); err != nil {
					return err
				}
				if err := e.kubernetes.DeleteVeleroStorageClassMappings(ctx, resolved.targetKube, veleroservice.Namespace, runID.String()); err != nil {
					return err
				}
				if err := e.kubernetes.DeletePVCStaging(ctx, resolved.targetKube, targetNamespace, runID.String()); err != nil {
					return err
				}
				snapshots, err := e.progress.ListWorkloadReplicaSnapshots(ctx, runID)
				if err != nil {
					return err
				}
				if len(snapshots) > 0 {
					if err := e.kubernetes.ScaleWorkloads(ctx, resolved.targetKube, scalableWorkloads(snapshots, targetNamespace)); err != nil {
						return err
					}
				}
			}
			return e.progress.AppendEvent(ctx, domainmigration.Event{
				RunID: runID, Type: "VELERO_RESTORE_COMPLETED", Severity: domainmigration.EventInfo,
				Message: fmt.Sprintf("Velero restore completed into namespace %s", targetNamespace),
				Detail:  map[string]any{"restore": status.Name, "namespace": targetNamespace, "itemsRestored": status.ItemsRestored, "totalItems": status.TotalItems},
			})
		case "Failed", "FailedValidation", "PartiallyFailed":
			return fmt.Errorf("Velero Restore %s: errors=%d warnings=%d message=%s", status.Phase, status.Errors, status.Warnings, status.Message)
		}
		if err := wait(ctx, e.pollInterval); err != nil {
			return err
		}
		status, err = e.velero.RestoreStatus(ctx, resolved.targetKube, veleroservice.Namespace, restoreName(runID))
		if err != nil {
			return err
		}
	}
}

func (e *VeleroExecutor) validateTarget(ctx context.Context, runID uuid.UUID) error {
	resolved, err := e.resolve(ctx, runID)
	if err != nil {
		return err
	}
	defer resolved.clear()
	if e.kubernetes == nil {
		return fmt.Errorf("%w: Kubernetes target validation is not configured", ErrUnsupportedExecution)
	}
	targetNamespace := resolved.application.Namespace
	for _, value := range resolved.mapping.Namespaces {
		if value.Source == resolved.application.Namespace {
			targetNamespace = value.Target
		}
	}
	validationTimeout := time.Duration(resolved.plan.ValidationPolicy.TimeoutSeconds) * time.Second
	result, err := waitForNamespaceValidation(ctx, validationTimeout, e.pollInterval, func(validationCtx context.Context) (kubernetesadapter.NamespaceValidation, error) {
		return e.kubernetes.ValidateNamespace(validationCtx, resolved.targetKube, targetNamespace)
	})
	severity := domainmigration.EventInfo
	message := fmt.Sprintf("Collected validation results for %d target resources", len(result.Results))
	if err != nil {
		severity, message = domainmigration.EventError, fmt.Sprintf("Target resource validation found failures: %v", err)
	}
	if appendErr := e.progress.AppendEvent(ctx, domainmigration.Event{RunID: runID, Type: "TARGET_RESOURCE_VALIDATION_COLLECTED", Severity: severity, Message: message, Detail: map[string]any{"namespace": targetNamespace, "results": result.Results}}); appendErr != nil {
		return appendErr
	}
	if err != nil {
		return err
	}
	if err := e.kubernetes.ValidateEndpoints(ctx, resolved.targetKube, kubernetesadapter.EndpointValidationSpec{Namespace: targetNamespace, RunID: runID.String(), HTTPChecks: resolved.plan.ValidationPolicy.HTTPChecks, TCPChecks: resolved.plan.ValidationPolicy.TCPChecks, HelperImage: e.stagingImage, Timeout: time.Duration(resolved.plan.ValidationPolicy.TimeoutSeconds) * time.Second}); err != nil {
		return err
	}
	return e.progress.AppendEvent(ctx, domainmigration.Event{
		RunID: runID, Type: "TARGET_BASIC_VALIDATION_PASSED", Severity: domainmigration.EventInfo,
		Message: fmt.Sprintf("Target namespace %s resources and configured endpoints are ready", targetNamespace),
		Detail:  map[string]any{"namespace": targetNamespace, "deployments": result.Deployments, "statefulSets": result.StatefulSets, "pvcs": result.PVCs, "services": result.Services, "ingresses": result.Ingresses, "httpChecks": len(resolved.plan.ValidationPolicy.HTTPChecks), "tcpChecks": len(resolved.plan.ValidationPolicy.TCPChecks)},
	})
}

func (e *VeleroExecutor) waitForSyncedBackup(ctx context.Context, targetKube []byte, name string) error {
	for {
		status, err := e.velero.BackupStatus(ctx, targetKube, veleroservice.Namespace, name)
		if err == nil && status.Phase == "Completed" {
			return nil
		}
		if err == nil && (status.Phase == "Failed" || status.Phase == "FailedValidation" || status.Phase == "PartiallyFailed") {
			return fmt.Errorf("target-synced Velero Backup is unusable: phase=%s", status.Phase)
		}
		if err := wait(ctx, e.pollInterval); err != nil {
			return fmt.Errorf("wait for target Velero Backup sync: %w", err)
		}
	}
}

func (e *VeleroExecutor) recordTransfers(ctx context.Context, resolved executionContext, backup string) (int64, int64, error) {
	values, err := e.velero.VolumeTransfers(ctx, resolved.sourceKube, veleroservice.Namespace, backup)
	engine := domainmigration.TransferVeleroFSB
	if resolved.plan.Strategy.VolumeMode == domainmigration.VolumeCSIDataMover {
		values, err = e.velero.DataUploads(ctx, resolved.sourceKube, veleroservice.Namespace, backup)
		engine = domainmigration.TransferCSIDataMover
	}
	if err != nil {
		return 0, 0, err
	}
	transfers := make([]domainmigration.VolumeTransfer, 0, len(values))
	var transferred, total int64
	for _, value := range values {
		done, size := value.BytesDone, value.TotalBytes
		if size > 0 && done > size {
			done = size
		}
		transferred, total = transferred+done, total+size
		namespace, sourceVolume := resolved.application.Namespace, value.Pod+"/"+value.Volume
		if engine == domainmigration.TransferCSIDataMover {
			namespace, sourceVolume = value.Pod, value.Volume
			if namespace == "" {
				namespace = resolved.application.Namespace
			}
		}
		transfers = append(transfers, domainmigration.VolumeTransfer{
			RunID: resolved.run.ID, Engine: engine, Namespace: namespace,
			SourceVolume: sourceVolume, TargetVolume: value.Volume, TotalBytes: size,
			TransferredBytes: done, ChecksumStatus: "PENDING", Status: transferStatus(value.Phase), ErrorMessage: value.Message,
		})
	}
	if err := e.progress.UpsertVolumeTransfers(ctx, resolved.run.ID, transfers); err != nil {
		return 0, 0, err
	}
	if err := e.progress.UpdateRunBytes(ctx, resolved.run.ID, transferred, total); err != nil {
		return 0, 0, err
	}
	return transferred, total, nil
}

func (e *VeleroExecutor) recordDataMoverDownloads(ctx context.Context, resolved executionContext, restore string) (int64, int64, error) {
	values, err := e.velero.DataDownloads(ctx, resolved.targetKube, veleroservice.Namespace, restore)
	if err != nil {
		return 0, 0, err
	}
	if len(values) == 0 {
		return 0, 0, nil
	}
	transfers := make([]domainmigration.VolumeTransfer, 0, len(values))
	var transferred, total int64
	for _, value := range values {
		done, size := value.BytesDone, value.TotalBytes
		if size > 0 && done > size {
			done = size
		}
		transferred, total = transferred+done, total+size
		namespace := value.Pod
		if namespace == "" {
			namespace = resolved.application.Namespace
		}
		transfers = append(transfers, domainmigration.VolumeTransfer{
			RunID: resolved.run.ID, Engine: domainmigration.TransferCSIDataMover, Namespace: namespace,
			SourceVolume: value.Volume, TargetVolume: value.Volume, TotalBytes: size, TransferredBytes: done,
			ChecksumStatus: "PENDING", Status: transferStatus(value.Phase), ErrorMessage: value.Message,
		})
	}
	if err := e.progress.UpsertVolumeTransfers(ctx, resolved.run.ID, transfers); err != nil {
		return 0, 0, err
	}
	if err := e.progress.UpdateRunBytes(ctx, resolved.run.ID, transferred, total); err != nil {
		return 0, 0, err
	}
	return transferred, total, nil
}

func transferStatus(phase string) domainmigration.TransferStatus {
	switch strings.ToLower(phase) {
	case "inprogress", "running", "prepared", "accepted":
		return domainmigration.TransferRunning
	case "completed":
		return domainmigration.TransferCompleted
	case "failed", "failedvalidation":
		return domainmigration.TransferFailed
	case "canceled", "cancelled":
		return domainmigration.TransferCancelled
	default:
		return domainmigration.TransferPending
	}
}

func backupName(runID uuid.UUID, stage string) string {
	return "migration-" + strings.ReplaceAll(runID.String(), "-", "")[:12] + "-" + stage
}

func restoreName(runID uuid.UUID) string { return backupName(runID, "restore") }

func scalableWorkloads(values []domainmigration.WorkloadReplicaSnapshot, targetNamespace string) []kubernetesadapter.ScalableWorkload {
	result := make([]kubernetesadapter.ScalableWorkload, 0, len(values))
	for _, value := range values {
		namespace := value.Namespace
		if targetNamespace != "" {
			namespace = targetNamespace
		}
		result = append(result, kubernetesadapter.ScalableWorkload{Namespace: namespace, Kind: value.Kind, Name: value.Name, Replicas: value.Replicas})
	}
	return result
}

func wait(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (value *executionContext) clear() {
	wipeBytes(value.sourceKube)
	wipeBytes(value.targetKube)
}

func wipeBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
