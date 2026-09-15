package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/domain/identity"
	domainmigration "github.com/smartx/sks-migration-center/internal/domain/migration"
	migrationservice "github.com/smartx/sks-migration-center/internal/migration"
	"github.com/smartx/sks-migration-center/internal/repository"
)

type MigrationPlanService interface {
	Create(context.Context, domainmigration.Plan) (domainmigration.Plan, error)
	Get(context.Context, uuid.UUID) (domainmigration.Plan, error)
	List(context.Context) ([]domainmigration.Plan, error)
	Preflight(context.Context, uuid.UUID) (domainmigration.PreflightResult, error)
}

type migrationPlanInput struct {
	Name                string                           `json:"name"`
	SourceEnvironmentID uuid.UUID                        `json:"sourceEnvironmentId"`
	TargetEnvironmentID uuid.UUID                        `json:"targetEnvironmentId"`
	SourceApplicationID uuid.UUID                        `json:"sourceApplicationId"`
	AssessmentID        uuid.UUID                        `json:"assessmentId"`
	MappingProfileID    *uuid.UUID                       `json:"mappingProfileId,omitempty"`
	Strategy            domainmigration.Strategy         `json:"strategy"`
	ValidationPolicy    domainmigration.ValidationPolicy `json:"validationPolicy"`
}

func (input migrationPlanInput) plan() domainmigration.Plan {
	return domainmigration.Plan{Name: input.Name, SourceEnvironmentID: input.SourceEnvironmentID,
		TargetEnvironmentID: input.TargetEnvironmentID, SourceApplicationID: input.SourceApplicationID,
		AssessmentID: input.AssessmentID, MappingProfileID: input.MappingProfileID,
		Strategy: input.Strategy, ValidationPolicy: input.ValidationPolicy}
}

func listMigrationPlansHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.MigrationPlans == nil {
			writeInternalProblem(w)
			return
		}
		values, err := deps.MigrationPlans.List(r.Context())
		if err != nil {
			writeMigrationPlanError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, values)
	}
}

func createMigrationPlanHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.MigrationPlans == nil {
			writeInternalProblem(w)
			return
		}
		var input migrationPlanInput
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			writeMigrationPlanProblem(w, http.StatusBadRequest, "MIGRATION_PLAN_INVALID", "迁移计划格式无效。")
			return
		}
		value, err := deps.MigrationPlans.Create(r.Context(), input.plan())
		if err != nil {
			writeMigrationPlanError(w, err)
			auditMigrationPlan(deps, r, "migration_plan.create", nil, "FAILURE", map[string]any{"name": input.Name})
			return
		}
		auditMigrationPlan(deps, r, "migration_plan.create", &value.ID, "SUCCESS", map[string]any{"name": value.Name, "status": value.Status})
		writeJSON(w, http.StatusCreated, value)
	}
}

func getMigrationPlanHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.MigrationPlans == nil {
			writeInternalProblem(w)
			return
		}
		id, ok := migrationPlanID(w, r)
		if !ok {
			return
		}
		value, err := deps.MigrationPlans.Get(r.Context(), id)
		if err != nil {
			writeMigrationPlanError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	}
}

func preflightMigrationPlanHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.MigrationPlans == nil {
			writeInternalProblem(w)
			return
		}
		id, ok := migrationPlanID(w, r)
		if !ok {
			return
		}
		value, err := deps.MigrationPlans.Preflight(r.Context(), id)
		if err != nil {
			writeMigrationPlanError(w, err)
			auditMigrationPlan(deps, r, "migration_plan.preflight", &id, "FAILURE", nil)
			return
		}
		auditMigrationPlan(deps, r, "migration_plan.preflight", &id, "SUCCESS", map[string]any{"ready": value.Ready, "blockerCount": value.BlockerCount, "warningCount": value.WarningCount})
		writeJSON(w, http.StatusOK, value)
	}
}

func migrationPlanID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("planId"))
	if err != nil {
		writeMigrationPlanProblem(w, http.StatusBadRequest, "MIGRATION_PLAN_INVALID", "planId 必须是有效的 UUID。")
		return uuid.Nil, false
	}
	return id, true
}

func writeMigrationPlanError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, repository.ErrNotFound):
		writeMigrationPlanProblem(w, http.StatusNotFound, "MIGRATION_PLAN_NOT_FOUND", "迁移计划或其引用对象不存在。")
	case errors.Is(err, repository.ErrConflict):
		writeMigrationPlanProblem(w, http.StatusConflict, "MIGRATION_PLAN_CONFLICT", "迁移计划当前状态不允许重新检查。")
	case errors.Is(err, migrationservice.ErrInvalidInput):
		writeMigrationPlanProblem(w, http.StatusBadRequest, "MIGRATION_PLAN_INVALID", strings.TrimPrefix(err.Error(), migrationservice.ErrInvalidInput.Error()+": "))
	default:
		writeInternalProblem(w)
	}
}

func writeMigrationPlanProblem(w http.ResponseWriter, status int, code, detail string) {
	writeProblem(w, problem{Type: "/problems/migration-plan", Title: "Migration plan error", Status: status, Detail: detail, Code: code})
}

func auditMigrationPlan(deps Dependencies, r *http.Request, action string, objectID *uuid.UUID, result string, detail map[string]any) {
	if deps.Audit == nil {
		return
	}
	actor := "unknown"
	if principal, ok := principalFromContext(r.Context()); ok {
		actor = principal.Administrator.Username
	}
	if err := deps.Audit.RecordAudit(r.Context(), identity.AuditEvent{Actor: actor, Action: action, ObjectType: "migration_plan", ObjectID: objectID, Result: result, Detail: detail}); err != nil && deps.Logger != nil {
		deps.Logger.Error("migration plan audit write failed", "action", action, "error", err)
	}
}
