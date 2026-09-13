package migration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	veleroadapter "github.com/smartx/sks-migration-center/internal/adapter/velero"
	domainenvironment "github.com/smartx/sks-migration-center/internal/domain/environment"
	domainmigration "github.com/smartx/sks-migration-center/internal/domain/migration"
)

type cleanupEnvironmentStore struct {
	values map[uuid.UUID]domainenvironment.Environment
}

func (s *cleanupEnvironmentStore) Create(context.Context, domainenvironment.Environment) error {
	return nil
}
func (s *cleanupEnvironmentStore) Get(_ context.Context, id uuid.UUID) (domainenvironment.Environment, error) {
	return s.values[id], nil
}
func (s *cleanupEnvironmentStore) List(context.Context) ([]domainenvironment.Environment, error) {
	return nil, nil
}
func (s *cleanupEnvironmentStore) Update(context.Context, domainenvironment.Environment) error {
	return nil
}
func (s *cleanupEnvironmentStore) Delete(context.Context, uuid.UUID) error { return nil }

type cleanupVault struct{ values map[uuid.UUID][]byte }

func (s *cleanupVault) Resolve(_ context.Context, id uuid.UUID) ([]byte, error) {
	return append([]byte(nil), s.values[id]...), nil
}

type cleanupVelero struct {
	calls      int
	deleteData []bool
}

func (s *cleanupVelero) DeleteRunArtifacts(_ context.Context, _ []byte, _, _ string, deleteData bool) (veleroadapter.ArtifactCleanup, error) {
	s.calls++
	s.deleteData = append(s.deleteData, deleteData)
	return veleroadapter.ArtifactCleanup{BackupsDeleted: 1, RestoresDeleted: 1}, nil
}

func TestArtifactCleanupDeletesOnlyTerminalVeleroRunArtifacts(t *testing.T) {
	sourceID, targetID, sourceCredential, targetCredential := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	plan := domainmigration.Plan{ID: uuid.New(), SourceEnvironmentID: sourceID, TargetEnvironmentID: targetID, Strategy: domainmigration.Strategy{VolumeMode: domainmigration.VolumeFSBackup}}
	runs := &runRepositoryStub{run: domainmigration.Run{ID: uuid.New(), PlanID: plan.ID, Status: domainmigration.RunCompleted}}
	environments := &cleanupEnvironmentStore{values: map[uuid.UUID]domainenvironment.Environment{
		sourceID: {ID: sourceID, Kind: domainenvironment.KindKubernetes, CredentialID: &sourceCredential},
		targetID: {ID: targetID, Kind: domainenvironment.KindKubernetes, CredentialID: &targetCredential},
	}}
	progress := &progressRepositoryStub{}
	velero := &cleanupVelero{}
	service, err := NewArtifactCleanupService(&runPlanRepositoryStub{plan: plan}, runs, progress, environments, &cleanupVault{values: map[uuid.UUID][]byte{sourceCredential: []byte("source"), targetCredential: []byte("target")}}, velero)
	if err != nil {
		t.Fatal(err)
	}
	service.clock = func() time.Time { return time.Date(2026, 9, 5, 2, 0, 0, 0, time.UTC) }
	result, err := service.Cleanup(context.Background(), runs.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if velero.calls != 2 || len(velero.deleteData) != 2 || !velero.deleteData[0] || velero.deleteData[1] || len(result.Clusters) != 2 || len(progress.events) != 1 || result.CleanedAt.IsZero() {
		t.Fatalf("unexpected cleanup: result=%+v calls=%d events=%+v", result, velero.calls, progress.events)
	}
	runs.run.Status = domainmigration.RunValidation
	if _, err := service.Cleanup(context.Background(), runs.run.ID); err != ErrInvalidRunState {
		t.Fatalf("active run cleanup error = %v", err)
	}
}
