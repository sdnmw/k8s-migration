package migration

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	veleroadapter "github.com/smartx/sks-migration-center/internal/adapter/velero"
	domainenvironment "github.com/smartx/sks-migration-center/internal/domain/environment"
	domainmigration "github.com/smartx/sks-migration-center/internal/domain/migration"
	"github.com/smartx/sks-migration-center/internal/repository"
)

type VeleroArtifactClient interface {
	DeleteRunArtifacts(context.Context, []byte, string, string, bool) (veleroadapter.ArtifactCleanup, error)
}

type ArtifactCleanupService struct {
	plans        repository.MigrationPlanRepository
	runs         repository.MigrationRunRepository
	progress     repository.MigrationProgressRepository
	environments repository.EnvironmentRepository
	vault        ExecutionVault
	velero       VeleroArtifactClient
	clock        func() time.Time
}

func NewArtifactCleanupService(plans repository.MigrationPlanRepository, runs repository.MigrationRunRepository, progress repository.MigrationProgressRepository, environments repository.EnvironmentRepository, vault ExecutionVault, velero VeleroArtifactClient) (*ArtifactCleanupService, error) {
	if plans == nil || runs == nil || progress == nil || environments == nil || vault == nil || velero == nil {
		return nil, errors.New("artifact cleanup repositories, vault and Velero client are required")
	}
	return &ArtifactCleanupService{plans: plans, runs: runs, progress: progress, environments: environments, vault: vault, velero: velero, clock: func() time.Time { return time.Now().UTC() }}, nil
}

func (s *ArtifactCleanupService) Cleanup(ctx context.Context, runID uuid.UUID) (domainmigration.ArtifactCleanupResult, error) {
	if runID == uuid.Nil {
		return domainmigration.ArtifactCleanupResult{}, fmt.Errorf("%w: runId is required", ErrInvalidInput)
	}
	run, err := s.runs.GetRun(ctx, runID)
	if err != nil {
		return domainmigration.ArtifactCleanupResult{}, err
	}
	if !domainmigration.IsTerminal(run.Status) {
		return domainmigration.ArtifactCleanupResult{}, ErrInvalidRunState
	}
	plan, err := s.plans.GetPlan(ctx, run.PlanID)
	if err != nil {
		return domainmigration.ArtifactCleanupResult{}, err
	}
	result := domainmigration.ArtifactCleanupResult{RunID: runID, Clusters: []domainmigration.ArtifactCleanupCluster{}, CleanedAt: s.clock()}
	if plan.Strategy.VolumeMode == domainmigration.VolumeComposeKopia {
		result.Retained = []string{"Compose Kopia snapshots are retained in MinIO; repository maintenance may remove them after the rollback window."}
	} else if plan.Strategy.VolumeMode == domainmigration.VolumeFSBackup || plan.Strategy.VolumeMode == domainmigration.VolumeCSIDataMover {
		for _, cluster := range []struct {
			id   uuid.UUID
			role string
		}{{plan.SourceEnvironmentID, "SOURCE"}, {plan.TargetEnvironmentID, "TARGET"}} {
			environment, getErr := s.environments.Get(ctx, cluster.id)
			if getErr != nil {
				return result, getErr
			}
			if environment.Kind != domainenvironment.KindKubernetes || environment.CredentialID == nil {
				continue
			}
			kubeconfig, resolveErr := s.vault.Resolve(ctx, *environment.CredentialID)
			if resolveErr != nil {
				return result, resolveErr
			}
			deleted, deleteErr := s.velero.DeleteRunArtifacts(ctx, kubeconfig, "velero", runID.String(), cluster.role == "SOURCE")
			wipeBytes(kubeconfig)
			if deleteErr != nil {
				return result, fmt.Errorf("clean %s Velero artifacts: %w", cluster.role, deleteErr)
			}
			result.Clusters = append(result.Clusters, domainmigration.ArtifactCleanupCluster{
				EnvironmentID: cluster.id, Role: cluster.role, BackupsDeleted: deleted.BackupsDeleted, RestoresDeleted: deleted.RestoresDeleted,
			})
		}
	}
	if err := s.progress.AppendEvent(ctx, domainmigration.Event{
		RunID: runID, Type: "MIGRATION_ARTIFACTS_CLEANED", Severity: domainmigration.EventInfo,
		Message: "Migration backup artifacts were cleaned according to the run strategy",
		Detail:  map[string]any{"clusters": len(result.Clusters), "retained": result.Retained},
	}); err != nil {
		return result, err
	}
	return result, nil
}
