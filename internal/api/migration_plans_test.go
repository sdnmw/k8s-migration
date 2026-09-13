package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	domainmigration "github.com/smartx/sks-migration-center/internal/domain/migration"
	"github.com/smartx/sks-migration-center/internal/repository"
)

type migrationPlanServiceStub struct {
	input  domainmigration.Plan
	plan   domainmigration.Plan
	result domainmigration.PreflightResult
	err    error
}

func (s *migrationPlanServiceStub) Create(_ context.Context, value domainmigration.Plan) (domainmigration.Plan, error) {
	s.input = value
	return s.plan, s.err
}
func (s *migrationPlanServiceStub) Get(context.Context, uuid.UUID) (domainmigration.Plan, error) {
	return s.plan, s.err
}
func (s *migrationPlanServiceStub) List(context.Context) ([]domainmigration.Plan, error) {
	return []domainmigration.Plan{s.plan}, s.err
}
func (s *migrationPlanServiceStub) Preflight(context.Context, uuid.UUID) (domainmigration.PreflightResult, error) {
	return s.result, s.err
}

func TestCreateMigrationPlanDecodesStrategyAndValidation(t *testing.T) {
	ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	service := &migrationPlanServiceStub{plan: domainmigration.Plan{ID: uuid.New(), Name: "api", Status: domainmigration.PlanDraft}}
	body := `{"name":"api","sourceEnvironmentId":"` + ids[0].String() + `","targetEnvironmentId":"` + ids[1].String() + `","sourceApplicationId":"` + ids[2].String() + `","assessmentId":"` + ids[3].String() + `","mappingProfileId":"` + ids[4].String() + `","strategy":{"resourceMode":"TRANSFORM","volumeMode":"FS_BACKUP","preSyncEnabled":true,"overwriteExistingResources":false,"preserveNodePort":false},"validationPolicy":{"requireWorkloadsReady":true,"requirePVCsBound":true,"timeoutSeconds":300}}`
	recorder := httptest.NewRecorder()
	createMigrationPlanHandler(Dependencies{MigrationPlans: service}).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/migration-plans", strings.NewReader(body)))
	if recorder.Code != http.StatusCreated || service.input.Strategy.VolumeMode != domainmigration.VolumeFSBackup || service.input.ValidationPolicy.TimeoutSeconds != 300 {
		t.Fatalf("status=%d input=%+v body=%s", recorder.Code, service.input, recorder.Body.String())
	}
}

func TestMigrationPlanPreflightReturnsBlockersAndMapsErrors(t *testing.T) {
	service := &migrationPlanServiceStub{result: domainmigration.PreflightResult{PlanID: uuid.New(), Ready: false, BlockerCount: 1, Checks: []domainmigration.PreflightCheck{{ID: "target.smartx-csi", Status: domainmigration.PreflightBlocker}}}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/migration-plans/id/preflight", nil)
	request.SetPathValue("planId", uuid.NewString())
	preflightMigrationPlanHandler(Dependencies{MigrationPlans: service}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"blockerCount":1`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	service.err = repository.ErrConflict
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/v1/migration-plans/id/preflight", nil)
	request.SetPathValue("planId", uuid.NewString())
	preflightMigrationPlanHandler(Dependencies{MigrationPlans: service}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "MIGRATION_PLAN_CONFLICT") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
