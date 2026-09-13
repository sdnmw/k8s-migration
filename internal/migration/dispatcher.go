package migration

import (
	"context"
	"errors"
	"fmt"

	"github.com/smartx/sks-migration-center/internal/domain/application"
	domainmigration "github.com/smartx/sks-migration-center/internal/domain/migration"
	"github.com/smartx/sks-migration-center/internal/repository"
)

type ExecutionHandler interface {
	Handle(context.Context, domainmigration.Lease) error
}

type SourceDispatcher struct {
	runs         repository.MigrationRunRepository
	plans        repository.MigrationPlanRepository
	applications repository.ApplicationRepository
	kubernetes   ExecutionHandler
	compose      ExecutionHandler
}

func NewSourceDispatcher(runs repository.MigrationRunRepository, plans repository.MigrationPlanRepository, applications repository.ApplicationRepository, kubernetes, compose ExecutionHandler) (*SourceDispatcher, error) {
	if runs == nil || plans == nil || applications == nil || kubernetes == nil || compose == nil {
		return nil, errors.New("dispatcher repositories and source handlers are required")
	}
	return &SourceDispatcher{runs: runs, plans: plans, applications: applications, kubernetes: kubernetes, compose: compose}, nil
}

func (d *SourceDispatcher) Handle(ctx context.Context, lease domainmigration.Lease) error {
	run, err := d.runs.GetRun(ctx, lease.RunID)
	if err != nil {
		return err
	}
	plan, err := d.plans.GetPlan(ctx, run.PlanID)
	if err != nil {
		return err
	}
	value, err := d.applications.Get(ctx, plan.SourceApplicationID)
	if err != nil {
		return err
	}
	switch value.SourceType {
	case application.SourceKubernetes:
		return d.kubernetes.Handle(ctx, lease)
	case application.SourceCompose:
		return d.compose.Handle(ctx, lease)
	default:
		return fmt.Errorf("%w: source type %s", ErrUnsupportedExecution, value.SourceType)
	}
}
