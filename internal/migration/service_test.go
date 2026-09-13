package migration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	domainapplication "github.com/smartx/sks-migration-center/internal/domain/application"
	domainassessment "github.com/smartx/sks-migration-center/internal/domain/assessment"
	domainenvironment "github.com/smartx/sks-migration-center/internal/domain/environment"
	domainmapping "github.com/smartx/sks-migration-center/internal/domain/mapping"
	domainmigration "github.com/smartx/sks-migration-center/internal/domain/migration"
	"github.com/smartx/sks-migration-center/internal/repository"
)

type planRepositoryStub struct {
	plan   domainmigration.Plan
	status domainmigration.PlanStatus
}

func (r *planRepositoryStub) CreatePlan(_ context.Context, value domainmigration.Plan) error {
	r.plan = value
	return nil
}
func (r *planRepositoryStub) GetPlan(context.Context, uuid.UUID) (domainmigration.Plan, error) {
	if r.plan.ID == uuid.Nil {
		return domainmigration.Plan{}, repository.ErrNotFound
	}
	return r.plan, nil
}
func (r *planRepositoryStub) ListPlans(context.Context) ([]domainmigration.Plan, error) {
	return []domainmigration.Plan{r.plan}, nil
}
func (r *planRepositoryStub) UpdatePlanStatus(_ context.Context, _ uuid.UUID, status domainmigration.PlanStatus) error {
	r.status = status
	r.plan.Status = status
	return nil
}

type environmentRepositoryStub struct {
	values map[uuid.UUID]domainenvironment.Environment
}

func (r *environmentRepositoryStub) Create(context.Context, domainenvironment.Environment) error {
	return nil
}
func (r *environmentRepositoryStub) Get(_ context.Context, id uuid.UUID) (domainenvironment.Environment, error) {
	value, ok := r.values[id]
	if !ok {
		return domainenvironment.Environment{}, repository.ErrNotFound
	}
	return value, nil
}
func (r *environmentRepositoryStub) List(context.Context) ([]domainenvironment.Environment, error) {
	return nil, nil
}
func (r *environmentRepositoryStub) Update(context.Context, domainenvironment.Environment) error {
	return nil
}
func (r *environmentRepositoryStub) Delete(context.Context, uuid.UUID) error { return nil }

type applicationRepositoryStub struct {
	value domainapplication.SourceApplication
}

func (r *applicationRepositoryStub) Upsert(context.Context, domainapplication.SourceApplication) (domainapplication.SourceApplication, error) {
	return r.value, nil
}
func (r *applicationRepositoryStub) Get(_ context.Context, id uuid.UUID) (domainapplication.SourceApplication, error) {
	if id != r.value.ID {
		return domainapplication.SourceApplication{}, repository.ErrNotFound
	}
	return r.value, nil
}
func (r *applicationRepositoryStub) ListByEnvironment(context.Context, uuid.UUID) ([]domainapplication.SourceApplication, error) {
	return []domainapplication.SourceApplication{r.value}, nil
}

type assessmentRepositoryStub struct{ value domainassessment.Assessment }

func (r *assessmentRepositoryStub) Create(context.Context, domainassessment.Assessment) error {
	return nil
}
func (r *assessmentRepositoryStub) Get(_ context.Context, id uuid.UUID) (domainassessment.Assessment, error) {
	if id != r.value.ID {
		return domainassessment.Assessment{}, repository.ErrNotFound
	}
	return r.value, nil
}

type mappingRepositoryStub struct{ value domainmapping.Profile }

func (r *mappingRepositoryStub) Create(context.Context, domainmapping.Profile) error { return nil }
func (r *mappingRepositoryStub) Get(_ context.Context, id uuid.UUID) (domainmapping.Profile, error) {
	if id != r.value.ID {
		return domainmapping.Profile{}, repository.ErrNotFound
	}
	return r.value, nil
}
func (r *mappingRepositoryStub) List(context.Context, *uuid.UUID) ([]domainmapping.Profile, error) {
	return []domainmapping.Profile{r.value}, nil
}
func (r *mappingRepositoryStub) Update(context.Context, domainmapping.Profile) error { return nil }
func (r *mappingRepositoryStub) Delete(context.Context, uuid.UUID) error             { return nil }

func TestCreateAndPreflightReadyPlan(t *testing.T) {
	service, plans, input := readyFixture(t)
	created, err := service.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == uuid.Nil || created.Status != domainmigration.PlanDraft {
		t.Fatalf("unexpected created plan: %+v", created)
	}
	result, err := service.Preflight(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Ready || result.BlockerCount != 0 || result.WarningCount != 1 || plans.status != domainmigration.PlanReady {
		t.Fatalf("unexpected preflight: %+v status=%s", result, plans.status)
	}
}

func TestPreflightBlocksAssessmentAndMissingSmartXCSI(t *testing.T) {
	service, plans, input := readyFixture(t)
	created, err := service.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	service.assessments.(*assessmentRepositoryStub).value.BlockerCount = 2
	target := service.environments.(*environmentRepositoryStub).values[input.TargetEnvironmentID]
	target.Capabilities.CSIDrivers = nil
	target.Capabilities.StorageClasses = []domainenvironment.StorageClass{{Name: "other", Provisioner: "other.csi", Default: true}}
	service.environments.(*environmentRepositoryStub).values[input.TargetEnvironmentID] = target
	result, err := service.Preflight(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Ready || result.BlockerCount < 2 || plans.status != domainmigration.PlanBlocked {
		t.Fatalf("blocked plan passed: %+v status=%s", result, plans.status)
	}
}

func TestPreflightAllowsVerifiedCSIDataMoverWithoutTargetSnapshotClass(t *testing.T) {
	service, _, input := readyFixture(t)
	source := service.environments.(*environmentRepositoryStub).values[input.SourceEnvironmentID]
	source.Capabilities.OperatingSystems = []string{"linux"}
	source.Capabilities.StorageClasses = []domainenvironment.StorageClass{{Name: "legacy", Provisioner: "csi.source.example", Default: true}}
	source.Capabilities.VolumeSnapshotClasses = []string{"legacy-snapshot"}
	source.Capabilities.VolumeSnapshotClassDetails = []domainenvironment.VolumeSnapshotClass{{Name: "legacy-snapshot", Driver: "csi.source.example"}}
	source.Capabilities.CSIDataMover = domainenvironment.CSIDataMoverCapabilities{BackupReady: true}
	service.environments.(*environmentRepositoryStub).values[input.SourceEnvironmentID] = source
	target := service.environments.(*environmentRepositoryStub).values[input.TargetEnvironmentID]
	target.Capabilities.CSIDataMover = domainenvironment.CSIDataMoverCapabilities{RestoreReady: true}
	target.Capabilities.VolumeSnapshotClasses = nil
	target.Capabilities.VolumeSnapshotClassDetails = nil
	target.Capabilities.StorageClasses[0].Provisioner = "com.smartx.elf-csi-driver"
	target.Capabilities.CSIDrivers = []string{"com.smartx.elf-csi-driver"}
	service.environments.(*environmentRepositoryStub).values[input.TargetEnvironmentID] = target
	application := service.applications.(*applicationRepositoryStub).value
	application.Inventory.PVCs[0].VolumeMode = "Block"
	service.applications.(*applicationRepositoryStub).value = application
	input.Strategy.VolumeMode = domainmigration.VolumeCSIDataMover

	created, err := service.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Preflight(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Ready || result.BlockerCount != 0 {
		t.Fatalf("verified Data Mover path was blocked: %+v", result)
	}
}

func TestPreflightBlocksRawBlockDataMoverOnUnconfirmedLinuxNodes(t *testing.T) {
	service, _, input := readyFixture(t)
	source := service.environments.(*environmentRepositoryStub).values[input.SourceEnvironmentID]
	source.Capabilities.OperatingSystems = []string{"windows"}
	source.Capabilities.StorageClasses = []domainenvironment.StorageClass{{Name: "legacy", Provisioner: "csi.source.example"}}
	source.Capabilities.VolumeSnapshotClassDetails = []domainenvironment.VolumeSnapshotClass{{Name: "legacy-snapshot", Driver: "csi.source.example"}}
	source.Capabilities.CSIDataMover = domainenvironment.CSIDataMoverCapabilities{BackupReady: true}
	service.environments.(*environmentRepositoryStub).values[input.SourceEnvironmentID] = source
	target := service.environments.(*environmentRepositoryStub).values[input.TargetEnvironmentID]
	target.Capabilities.CSIDataMover = domainenvironment.CSIDataMoverCapabilities{RestoreReady: true}
	service.environments.(*environmentRepositoryStub).values[input.TargetEnvironmentID] = target
	application := service.applications.(*applicationRepositoryStub).value
	application.Inventory.PVCs[0].VolumeMode = "Block"
	service.applications.(*applicationRepositoryStub).value = application
	input.Strategy.VolumeMode = domainmigration.VolumeCSIDataMover
	created, err := service.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Preflight(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Ready || result.BlockerCount == 0 {
		t.Fatalf("Windows/unconfirmed Raw Block path was not blocked: %+v", result)
	}
}

func TestPreflightBlocksDataMoverWhenSnapshotDriverDoesNotMatch(t *testing.T) {
	service, _, input := readyFixture(t)
	source := service.environments.(*environmentRepositoryStub).values[input.SourceEnvironmentID]
	source.Capabilities.StorageClasses = []domainenvironment.StorageClass{{Name: "legacy", Provisioner: "csi.source.example"}}
	source.Capabilities.VolumeSnapshotClassDetails = []domainenvironment.VolumeSnapshotClass{{Name: "wrong", Driver: "different.csi.example"}}
	source.Capabilities.CSIDataMover = domainenvironment.CSIDataMoverCapabilities{BackupReady: true}
	service.environments.(*environmentRepositoryStub).values[input.SourceEnvironmentID] = source
	target := service.environments.(*environmentRepositoryStub).values[input.TargetEnvironmentID]
	target.Capabilities.CSIDataMover = domainenvironment.CSIDataMoverCapabilities{RestoreReady: true}
	service.environments.(*environmentRepositoryStub).values[input.TargetEnvironmentID] = target
	input.Strategy.VolumeMode = domainmigration.VolumeCSIDataMover
	created, err := service.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Preflight(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Ready || result.BlockerCount == 0 {
		t.Fatalf("mismatched snapshot driver was not blocked: %+v", result)
	}
}

func TestCreateRejectsCrossEnvironmentReferences(t *testing.T) {
	service, _, input := readyFixture(t)
	application := service.applications.(*applicationRepositoryStub).value
	application.EnvironmentID = uuid.New()
	service.applications.(*applicationRepositoryStub).value = application
	if _, err := service.Create(context.Background(), input); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid cross reference, got %v", err)
	}
}

func readyFixture(t *testing.T) (*Service, *planRepositoryStub, domainmigration.Plan) {
	t.Helper()
	now := time.Now().UTC()
	sourceID, targetID, applicationID, assessmentID, mappingID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	plans := &planRepositoryStub{}
	environments := &environmentRepositoryStub{values: map[uuid.UUID]domainenvironment.Environment{
		sourceID: {ID: sourceID, Role: domainenvironment.RoleSource, Kind: domainenvironment.KindKubernetes, Status: domainenvironment.StatusConnected, CapabilitiesUpdatedAt: &now},
		targetID: {ID: targetID, Role: domainenvironment.RoleTarget, Kind: domainenvironment.KindKubernetes, Status: domainenvironment.StatusConnected, CapabilitiesUpdatedAt: &now, Capabilities: domainenvironment.Capabilities{CSIDrivers: []string{"smtx-elf-csi-driver"}, StorageClasses: []domainenvironment.StorageClass{{Name: "smtx-block", Provisioner: "smtx-elf-csi-driver", Default: true}}}},
	}}
	applications := &applicationRepositoryStub{value: domainapplication.SourceApplication{ID: applicationID, EnvironmentID: sourceID, SourceType: domainapplication.SourceKubernetes, Inventory: domainapplication.Inventory{PVCs: []domainapplication.VolumeSummary{{Name: "data", StorageClassName: "legacy", VolumeMode: "Filesystem"}}, Images: []domainapplication.ImageSummary{{Reference: "registry.local/api:v1"}}}}}
	assessments := &assessmentRepositoryStub{value: domainassessment.Assessment{ID: assessmentID, ApplicationID: applicationID, Status: domainassessment.StatusCompleted}}
	mappings := &mappingRepositoryStub{value: domainmapping.Profile{ID: mappingID, TargetEnvironmentID: targetID, Storage: []domainmapping.KeyValue{{Source: "legacy", Target: "smtx-block"}}, Registries: []domainmapping.KeyValue{{Source: "registry.local", Target: "harbor.local"}}}}
	service, err := NewService(plans, environments, applications, assessments, mappings)
	if err != nil {
		t.Fatal(err)
	}
	input := domainmigration.Plan{Name: "Production API", SourceEnvironmentID: sourceID, TargetEnvironmentID: targetID, SourceApplicationID: applicationID, AssessmentID: assessmentID, MappingProfileID: mappingID, Strategy: domainmigration.Strategy{ResourceMode: "TRANSFORM", VolumeMode: domainmigration.VolumeFSBackup, PreSyncEnabled: true}, ValidationPolicy: domainmigration.ValidationPolicy{RequireWorkloadsReady: true, RequirePVCsBound: true, TimeoutSeconds: 300}}
	return service, plans, input
}
