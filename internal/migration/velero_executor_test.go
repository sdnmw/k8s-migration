package migration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	kubernetesadapter "github.com/smartx/sks-migration-center/internal/adapter/kubernetes"
	veleroadapter "github.com/smartx/sks-migration-center/internal/adapter/velero"
	domainapplication "github.com/smartx/sks-migration-center/internal/domain/application"
	domainenvironment "github.com/smartx/sks-migration-center/internal/domain/environment"
	domainmapping "github.com/smartx/sks-migration-center/internal/domain/mapping"
	domainmigration "github.com/smartx/sks-migration-center/internal/domain/migration"
)

func TestVeleroExecutorRunsPreSyncFinalTransferAndRestore(t *testing.T) {
	runID, planID, applicationID, sourceID, targetID, mappingID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	sourceCredential, targetCredential := uuid.New(), uuid.New()
	plan := domainmigration.Plan{
		ID: planID, SourceEnvironmentID: sourceID, TargetEnvironmentID: targetID, SourceApplicationID: applicationID, MappingProfileID: mappingID,
		Strategy: domainmigration.Strategy{VolumeMode: domainmigration.VolumeFSBackup, PreSyncEnabled: true},
	}
	runs := &runRepositoryStub{run: domainmigration.Run{ID: runID, PlanID: planID}}
	progress := &progressRepositoryStub{}
	velero := &veleroExecutionStub{
		backupCreate:  veleroadapter.BackupStatus{Phase: "InProgress"},
		backupRead:    veleroadapter.BackupStatus{Phase: "Completed", ItemsBackedUp: 12, TotalItems: 12},
		restoreCreate: veleroadapter.RestoreStatus{Phase: "InProgress"},
		restoreRead:   veleroadapter.RestoreStatus{Phase: "Completed", ItemsRestored: 12, TotalItems: 12},
		transfers:     []veleroadapter.VolumeTransfer{{Name: "pvb", Pod: "db-0", Volume: "data", Phase: "Completed", BytesDone: 4096, TotalBytes: 4096}},
	}
	kubernetes := &kubernetesExecutionStub{workloads: []kubernetesadapter.ScalableWorkload{
		{Namespace: "business", Kind: "Deployment", Name: "api", Replicas: 3},
		{Namespace: "business", Kind: "StatefulSet", Name: "db", Replicas: 1},
	}}
	executor, err := NewVeleroExecutor(
		&runPlanRepositoryStub{plan: plan}, runs, progress,
		&environmentRepositoryStub{values: map[uuid.UUID]domainenvironment.Environment{
			sourceID: {ID: sourceID, Role: domainenvironment.RoleSource, Kind: domainenvironment.KindKubernetes, CredentialID: &sourceCredential},
			targetID: {ID: targetID, Role: domainenvironment.RoleTarget, Kind: domainenvironment.KindKubernetes, CredentialID: &targetCredential},
		}},
		&applicationRepositoryStub{value: domainapplication.SourceApplication{
			ID: applicationID, EnvironmentID: sourceID, SourceType: domainapplication.SourceKubernetes, Namespace: "business",
			Inventory: domainapplication.Inventory{PVCs: []domainapplication.VolumeSummary{{Name: "data"}}},
		}},
		&mappingRepositoryStub{value: domainmapping.Profile{ID: mappingID, TargetEnvironmentID: targetID, Namespaces: []domainmapping.KeyValue{{Source: "business", Target: "business-migrated"}}}},
		&executionVaultStub{values: map[uuid.UUID][]byte{sourceCredential: []byte("source-kubeconfig"), targetCredential: []byte("target-kubeconfig")}}, velero,
		WithKubernetesExecution(kubernetes, "busybox@sha256:"+strings.Repeat("a", 64)),
	)
	if err != nil {
		t.Fatal(err)
	}
	executor.pollInterval = time.Millisecond
	for _, step := range []domainmigration.StepType{
		domainmigration.StepPreflight, domainmigration.StepPreSync, domainmigration.StepQuiesce, domainmigration.StepFinalBackup,
		domainmigration.StepTransfer, domainmigration.StepTransform, domainmigration.StepRestore, domainmigration.StepValidation, domainmigration.StepRollback,
	} {
		if err := executor.Handle(context.Background(), domainmigration.Lease{RunID: runID, StepType: step}); err != nil {
			t.Fatalf("handle %s: %v", step, err)
		}
	}
	if len(velero.backups) != 2 || velero.backups[0].Name != backupName(runID, "presync") || velero.backups[1].Name != backupName(runID, "final") {
		t.Fatalf("unexpected backups: %#v", velero.backups)
	}
	if len(velero.restores) != 1 || velero.restores[0].NamespaceMappings["business"] != "business-migrated" || !velero.restores[0].RestorePVs {
		t.Fatalf("unexpected restore: %#v", velero.restores)
	}
	if progress.transferred != 4096 || progress.total != 4096 || len(progress.transfers) != 1 || progress.transfers[0].Status != domainmigration.TransferCompleted {
		t.Fatalf("unexpected progress: %#v %d/%d", progress.transfers, progress.transferred, progress.total)
	}
	if len(progress.events) == 0 || progress.events[len(progress.events)-1].Type != "SOURCE_ROLLBACK_COMPLETED" {
		t.Fatalf("unexpected events: %#v", progress.events)
	}
	if len(kubernetes.scales) != 3 || kubernetes.scales[0][0].Replicas != 0 || kubernetes.scales[1][0].Namespace != "business-migrated" || kubernetes.scales[2][0].Namespace != "business" {
		t.Fatalf("unexpected workload scaling: %#v", kubernetes.scales)
	}
	if kubernetes.prepares != 2 || kubernetes.deletes != 4 {
		t.Fatalf("unexpected staging lifecycle: prepares=%d deletes=%d", kubernetes.prepares, kubernetes.deletes)
	}
	if kubernetes.mappingEnsures != 1 || kubernetes.mappingDeletes != 1 || kubernetes.validations != 1 {
		t.Fatalf("unexpected transform/validation lifecycle: %#v", kubernetes)
	}
}

func TestVeleroExecutorStopsOnFailedBackup(t *testing.T) {
	executor, runID := veleroExecutorFixture(t, &veleroExecutionStub{backupCreate: veleroadapter.BackupStatus{Phase: "Failed", Errors: 1, Message: "repository unavailable"}})
	err := executor.Handle(context.Background(), domainmigration.Lease{RunID: runID, StepType: domainmigration.StepFinalBackup})
	if err == nil || !stringsContains(err.Error(), "repository unavailable") {
		t.Fatalf("error = %v", err)
	}
}

func TestQuiesceWithoutPVCDoesNotRequireWorkloadScaling(t *testing.T) {
	executor, runID := veleroExecutorFixture(t, &veleroExecutionStub{})
	if err := executor.Handle(context.Background(), domainmigration.Lease{RunID: runID, StepType: domainmigration.StepQuiesce}); err != nil {
		t.Fatalf("no-volume application should keep source running: %v", err)
	}
	kubernetes := &kubernetesExecutionStub{}
	executor.kubernetes = kubernetes
	if err := executor.Handle(context.Background(), domainmigration.Lease{RunID: runID, StepType: domainmigration.StepRollback}); err != nil {
		t.Fatalf("no-volume rollback should not require a replica snapshot: %v", err)
	}
	if len(kubernetes.scales) != 0 {
		t.Fatal("no-volume rollback changed source replicas")
	}
}

func TestVeleroExecutorUsesCSIDataMoverWithoutFilesystemStaging(t *testing.T) {
	runID, planID, appID, sourceID, targetID, mappingID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	sourceCredential, targetCredential := uuid.New(), uuid.New()
	progress := &progressRepositoryStub{}
	velero := &veleroExecutionStub{
		backupCreate: veleroadapter.BackupStatus{Phase: "Completed"},
		transfers:    []veleroadapter.VolumeTransfer{{Pod: "business", Volume: "raw-data", Phase: "Completed", BytesDone: 8192, TotalBytes: 8192}},
	}
	kubernetes := &kubernetesExecutionStub{}
	executor, err := NewVeleroExecutor(
		&runPlanRepositoryStub{plan: domainmigration.Plan{ID: planID, SourceEnvironmentID: sourceID, TargetEnvironmentID: targetID, SourceApplicationID: appID, MappingProfileID: mappingID, Strategy: domainmigration.Strategy{VolumeMode: domainmigration.VolumeCSIDataMover}}},
		&runRepositoryStub{run: domainmigration.Run{ID: runID, PlanID: planID}}, progress,
		&environmentRepositoryStub{values: map[uuid.UUID]domainenvironment.Environment{
			sourceID: {ID: sourceID, CredentialID: &sourceCredential, Capabilities: domainenvironment.Capabilities{
				OperatingSystems:           []string{"linux"},
				StorageClasses:             []domainenvironment.StorageClass{{Name: "legacy-block", Provisioner: "csi.source.example"}},
				VolumeSnapshotClassDetails: []domainenvironment.VolumeSnapshotClass{{Name: "legacy-snapshot", Driver: "csi.source.example"}},
			}},
			targetID: {ID: targetID, CredentialID: &targetCredential},
		}},
		&applicationRepositoryStub{value: domainapplication.SourceApplication{ID: appID, SourceType: domainapplication.SourceKubernetes, Namespace: "business", Inventory: domainapplication.Inventory{PVCs: []domainapplication.VolumeSummary{{Name: "raw-data", StorageClassName: "legacy-block", VolumeMode: "Block"}}}}},
		&mappingRepositoryStub{value: domainmapping.Profile{ID: mappingID}},
		&executionVaultStub{values: map[uuid.UUID][]byte{sourceCredential: []byte("source"), targetCredential: []byte("target")}}, velero,
		WithKubernetesExecution(kubernetes, "busybox@sha256:"+strings.Repeat("a", 64)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.Handle(context.Background(), domainmigration.Lease{RunID: runID, StepType: domainmigration.StepPreflight}); err != nil {
		t.Fatal(err)
	}
	if err := executor.Handle(context.Background(), domainmigration.Lease{RunID: runID, StepType: domainmigration.StepPreSync}); err != nil {
		t.Fatal(err)
	}
	if len(velero.backups) != 1 || !velero.backups[0].SnapshotMoveData || velero.backups[0].DataMover != "velero" {
		t.Fatalf("unexpected CSI Data Mover backup: %+v", velero.backups)
	}
	if kubernetes.prepares != 0 || len(progress.transfers) != 1 || progress.transfers[0].Engine != domainmigration.TransferCSIDataMover {
		t.Fatalf("Data Mover must not use FSB staging: kubernetes=%+v transfers=%+v", kubernetes, progress.transfers)
	}
}

func veleroExecutorFixture(t *testing.T, velero *veleroExecutionStub) (*VeleroExecutor, uuid.UUID) {
	t.Helper()
	runID, planID, appID, sourceID, targetID, mappingID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	sourceCredential, targetCredential := uuid.New(), uuid.New()
	executor, err := NewVeleroExecutor(
		&runPlanRepositoryStub{plan: domainmigration.Plan{ID: planID, SourceEnvironmentID: sourceID, TargetEnvironmentID: targetID, SourceApplicationID: appID, MappingProfileID: mappingID, Strategy: domainmigration.Strategy{VolumeMode: domainmigration.VolumeFSBackup}}},
		&runRepositoryStub{run: domainmigration.Run{ID: runID, PlanID: planID}}, &progressRepositoryStub{},
		&environmentRepositoryStub{values: map[uuid.UUID]domainenvironment.Environment{
			sourceID: {ID: sourceID, CredentialID: &sourceCredential}, targetID: {ID: targetID, CredentialID: &targetCredential},
		}},
		&applicationRepositoryStub{value: domainapplication.SourceApplication{ID: appID, SourceType: domainapplication.SourceKubernetes, Namespace: "business"}},
		&mappingRepositoryStub{value: domainmapping.Profile{ID: mappingID}},
		&executionVaultStub{values: map[uuid.UUID][]byte{sourceCredential: []byte("source"), targetCredential: []byte("target")}}, velero,
	)
	if err != nil {
		t.Fatal(err)
	}
	executor.pollInterval = time.Millisecond
	return executor, runID
}

type executionVaultStub struct{ values map[uuid.UUID][]byte }

func (s *executionVaultStub) Resolve(_ context.Context, id uuid.UUID) ([]byte, error) {
	value, ok := s.values[id]
	if !ok {
		return nil, errors.New("credential missing")
	}
	return append([]byte(nil), value...), nil
}

type veleroExecutionStub struct {
	backupCreate  veleroadapter.BackupStatus
	backupRead    veleroadapter.BackupStatus
	restoreCreate veleroadapter.RestoreStatus
	restoreRead   veleroadapter.RestoreStatus
	transfers     []veleroadapter.VolumeTransfer
	backups       []veleroadapter.BackupSpec
	restores      []veleroadapter.RestoreSpec
}

func (s *veleroExecutionStub) BackupStorageLocationStatus(context.Context, []byte, string, string) (veleroadapter.BackupStorageLocationStatus, error) {
	return veleroadapter.BackupStorageLocationStatus{Phase: "Available"}, nil
}

func (s *veleroExecutionStub) CreateBackup(_ context.Context, _ []byte, spec veleroadapter.BackupSpec) (veleroadapter.BackupStatus, error) {
	s.backups = append(s.backups, spec)
	return s.backupCreate, nil
}
func (s *veleroExecutionStub) BackupStatus(context.Context, []byte, string, string) (veleroadapter.BackupStatus, error) {
	return s.backupRead, nil
}
func (s *veleroExecutionStub) VolumeTransfers(context.Context, []byte, string, string) ([]veleroadapter.VolumeTransfer, error) {
	return append([]veleroadapter.VolumeTransfer(nil), s.transfers...), nil
}
func (s *veleroExecutionStub) DataUploads(context.Context, []byte, string, string) ([]veleroadapter.VolumeTransfer, error) {
	return append([]veleroadapter.VolumeTransfer(nil), s.transfers...), nil
}
func (s *veleroExecutionStub) DataDownloads(context.Context, []byte, string, string) ([]veleroadapter.VolumeTransfer, error) {
	return append([]veleroadapter.VolumeTransfer(nil), s.transfers...), nil
}
func (s *veleroExecutionStub) CreateRestore(_ context.Context, _ []byte, spec veleroadapter.RestoreSpec) (veleroadapter.RestoreStatus, error) {
	s.restores = append(s.restores, spec)
	return s.restoreCreate, nil
}
func (s *veleroExecutionStub) RestoreStatus(context.Context, []byte, string, string) (veleroadapter.RestoreStatus, error) {
	return s.restoreRead, nil
}

type progressRepositoryStub struct {
	transfers   []domainmigration.VolumeTransfer
	events      []domainmigration.Event
	transferred int64
	total       int64
	snapshots   []domainmigration.WorkloadReplicaSnapshot
}

func (s *progressRepositoryStub) UpsertVolumeTransfers(_ context.Context, _ uuid.UUID, values []domainmigration.VolumeTransfer) error {
	s.transfers = append([]domainmigration.VolumeTransfer(nil), values...)
	return nil
}
func (s *progressRepositoryStub) ListVolumeTransfers(context.Context, uuid.UUID) ([]domainmigration.VolumeTransfer, error) {
	return s.transfers, nil
}
func (s *progressRepositoryStub) UpdateRunBytes(_ context.Context, _ uuid.UUID, transferred, total int64) error {
	s.transferred, s.total = transferred, total
	return nil
}
func (s *progressRepositoryStub) AppendEvent(_ context.Context, value domainmigration.Event) error {
	s.events = append(s.events, value)
	return nil
}
func (s *progressRepositoryStub) SaveWorkloadReplicaSnapshots(_ context.Context, _ uuid.UUID, values []domainmigration.WorkloadReplicaSnapshot) error {
	if len(s.snapshots) == 0 {
		s.snapshots = append([]domainmigration.WorkloadReplicaSnapshot(nil), values...)
	}
	return nil
}
func (s *progressRepositoryStub) ListWorkloadReplicaSnapshots(context.Context, uuid.UUID) ([]domainmigration.WorkloadReplicaSnapshot, error) {
	return append([]domainmigration.WorkloadReplicaSnapshot(nil), s.snapshots...), nil
}

type kubernetesExecutionStub struct {
	workloads        []kubernetesadapter.ScalableWorkload
	scales           [][]kubernetesadapter.ScalableWorkload
	prepares         int
	deletes          int
	mappingEnsures   int
	mappingDeletes   int
	namespaceEnsures int
	validations      int
}

func (s *kubernetesExecutionStub) PreparePVCStaging(context.Context, []byte, kubernetesadapter.PVCStagingSpec) ([]kubernetesadapter.PVCStagingPod, error) {
	s.prepares++
	return nil, nil
}
func (s *kubernetesExecutionStub) DeletePVCStaging(context.Context, []byte, string, string) error {
	s.deletes++
	return nil
}
func (s *kubernetesExecutionStub) ListScalableWorkloads(context.Context, []byte, string) ([]kubernetesadapter.ScalableWorkload, error) {
	return append([]kubernetesadapter.ScalableWorkload(nil), s.workloads...), nil
}
func (s *kubernetesExecutionStub) ScaleWorkloads(_ context.Context, _ []byte, values []kubernetesadapter.ScalableWorkload) error {
	s.scales = append(s.scales, append([]kubernetesadapter.ScalableWorkload(nil), values...))
	return nil
}
func (s *kubernetesExecutionStub) EnsureVeleroStorageClassMappings(context.Context, []byte, string, string, map[string]string) error {
	s.mappingEnsures++
	return nil
}
func (s *kubernetesExecutionStub) DeleteVeleroStorageClassMappings(context.Context, []byte, string, string) error {
	s.mappingDeletes++
	return nil
}
func (s *kubernetesExecutionStub) ApplyPostRestoreMappings(context.Context, []byte, string, domainmapping.Profile) (kubernetesadapter.PostRestoreMappingResult, error) {
	return kubernetesadapter.PostRestoreMappingResult{Examined: 2, Updated: 1, Verified: 1}, nil
}
func (s *kubernetesExecutionStub) EnsureMigrationNamespace(context.Context, []byte, string) error {
	s.namespaceEnsures++
	return nil
}
func (s *kubernetesExecutionStub) ValidateNamespace(context.Context, []byte, string) (kubernetesadapter.NamespaceValidation, error) {
	s.validations++
	return kubernetesadapter.NamespaceValidation{Deployments: 1, StatefulSets: 1, PVCs: 1}, nil
}
func (s *kubernetesExecutionStub) ValidateEndpoints(context.Context, []byte, kubernetesadapter.EndpointValidationSpec) error {
	return nil
}
func (s *kubernetesExecutionStub) CSIDataMoverCapabilities(context.Context, []byte, string) (domainenvironment.CSIDataMoverCapabilities, error) {
	return domainenvironment.CSIDataMoverCapabilities{SnapshotAPI: true, BackupReady: true, RestoreReady: true}, nil
}

func stringsContains(value, substring string) bool {
	for index := 0; index+len(substring) <= len(value); index++ {
		if value[index:index+len(substring)] == substring {
			return true
		}
	}
	return false
}
