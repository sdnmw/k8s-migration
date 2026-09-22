package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/domain/identity"
	domainmigration "github.com/smartx/sks-migration-center/internal/domain/migration"
	migrationservice "github.com/smartx/sks-migration-center/internal/migration"
	"github.com/smartx/sks-migration-center/internal/repository"
)

type migrationRunServiceStub struct {
	snapshot        domainmigration.RunSnapshot
	summaries       []domainmigration.RunSummary
	events          []domainmigration.Event
	err             error
	action          string
	afterID         int64
	administratorID uuid.UUID
}

func (s *migrationRunServiceStub) List(context.Context) ([]domainmigration.RunSummary, error) {
	return s.summaries, s.err
}

func (s *migrationRunServiceStub) Delete(context.Context, uuid.UUID) error {
	s.action = "delete"
	return s.err
}

func TestDeleteMigrationRunIsExplicitAndRejectsActiveRunConflict(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{{nil, 204}, {repository.ErrConflict, 409}} {
		service := &migrationRunServiceStub{err: tc.err}
		request := httptest.NewRequest(http.MethodDelete, "/", nil)
		request.SetPathValue("runId", uuid.NewString())
		recorder := httptest.NewRecorder()
		deleteMigrationRunHandler(Dependencies{MigrationRuns: service}).ServeHTTP(recorder, request)
		if recorder.Code != tc.status || service.action != "delete" {
			t.Fatalf("delete status=%d", recorder.Code)
		}
	}
}

type migrationArtifactServiceStub struct {
	result domainmigration.ArtifactCleanupResult
	err    error
}

type migrationEvidenceServiceStub struct {
	topology domainmigration.TopologyEvidence
	timeline domainmigration.StepTimeline
	err      error
}

func (s *migrationEvidenceServiceStub) Topology(context.Context, uuid.UUID) (domainmigration.TopologyEvidence, error) {
	return s.topology, s.err
}
func (s *migrationEvidenceServiceStub) RefreshTopology(context.Context, uuid.UUID) (domainmigration.TopologyEvidence, error) {
	return s.topology, s.err
}
func (s *migrationEvidenceServiceStub) Timeline(context.Context, uuid.UUID) (domainmigration.StepTimeline, error) {
	return s.timeline, s.err
}

func (s *migrationArtifactServiceStub) Cleanup(context.Context, uuid.UUID) (domainmigration.ArtifactCleanupResult, error) {
	return s.result, s.err
}

func (s *migrationRunServiceStub) VolumeTransfers(context.Context, uuid.UUID) ([]domainmigration.VolumeTransfer, error) {
	return []domainmigration.VolumeTransfer{{ID: uuid.New(), SourceVolume: "db-0/data", Status: domainmigration.TransferRunning}}, s.err
}

func (s *migrationRunServiceStub) Start(context.Context, uuid.UUID) (domainmigration.RunSnapshot, error) {
	s.action = "start"
	return s.snapshot, s.err
}
func (s *migrationRunServiceStub) Retry(context.Context, uuid.UUID) (domainmigration.RunSnapshot, error) {
	s.action = "retry"
	return s.snapshot, s.err
}
func (s *migrationRunServiceStub) Get(context.Context, uuid.UUID) (domainmigration.RunSnapshot, error) {
	s.action = "get"
	return s.snapshot, s.err
}
func (s *migrationRunServiceStub) Cancel(context.Context, uuid.UUID) (domainmigration.RunSnapshot, error) {
	s.action = "cancel"
	return s.snapshot, s.err
}
func (s *migrationRunServiceStub) Rollback(context.Context, uuid.UUID) (domainmigration.RunSnapshot, error) {
	s.action = "rollback"
	return s.snapshot, s.err
}
func (s *migrationRunServiceStub) RestoreSource(context.Context, uuid.UUID) (domainmigration.RunSnapshot, error) {
	s.action = "restore-source"
	return s.snapshot, s.err
}
func (s *migrationRunServiceStub) ConfirmCutover(_ context.Context, _ uuid.UUID, administratorID uuid.UUID) error {
	s.action, s.administratorID = "cutover", administratorID
	return s.err
}
func (s *migrationRunServiceStub) Report(context.Context, uuid.UUID) (domainmigration.Report, error) {
	s.action = "report"
	return domainmigration.Report{Run: s.snapshot.Run}, s.err
}
func (s *migrationRunServiceStub) Events(_ context.Context, _ uuid.UUID, afterID int64, _ int) ([]domainmigration.Event, error) {
	s.afterID = afterID
	return s.events, s.err
}

func TestStartCancelAndRetryMigrationRunHandlers(t *testing.T) {
	runID := uuid.New()
	service := &migrationRunServiceStub{snapshot: domainmigration.RunSnapshot{Run: domainmigration.Run{ID: runID, Status: domainmigration.RunPending}, Steps: []domainmigration.Step{{Type: domainmigration.StepPreflight}}}}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/migration-plans/id/runs", nil)
	request.SetPathValue("planId", uuid.NewString())
	recorder := httptest.NewRecorder()
	startMigrationRunHandler(Dependencies{MigrationRuns: service}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted || service.action != "start" || !strings.Contains(recorder.Body.String(), runID.String()) {
		t.Fatalf("start status=%d action=%s body=%s", recorder.Code, service.action, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/migration-runs/id/cancel", nil)
	request.SetPathValue("runId", runID.String())
	recorder = httptest.NewRecorder()
	cancelMigrationRunHandler(Dependencies{MigrationRuns: service}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted || service.action != "cancel" {
		t.Fatalf("cancel status=%d action=%s", recorder.Code, service.action)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/migration-runs/id/retry", nil)
	request.SetPathValue("runId", runID.String())
	recorder = httptest.NewRecorder()
	retryMigrationRunHandler(Dependencies{MigrationRuns: service}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted || service.action != "retry" {
		t.Fatalf("retry status=%d action=%s", recorder.Code, service.action)
	}
}

func TestRetryMigrationRunExplainsComponentReadinessBlock(t *testing.T) {
	runID := uuid.New()
	service := &migrationRunServiceStub{err: fmt.Errorf("%w: source Velero needs repair", migrationservice.ErrRetryPrerequisite)}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/migration-runs/id/retry", nil)
	request.SetPathValue("runId", runID.String())
	recorder := httptest.NewRecorder()
	retryMigrationRunHandler(Dependencies{MigrationRuns: service}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "MIGRATION_RETRY_NOT_READY") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestListMigrationRunsHandler(t *testing.T) {
	service := &migrationRunServiceStub{summaries: []domainmigration.RunSummary{{Run: domainmigration.Run{ID: uuid.New()}, PlanName: "orders", SourceEnvironmentName: "sida", TargetEnvironmentName: "mw"}}}
	recorder := httptest.NewRecorder()
	listMigrationRunsHandler(Dependencies{MigrationRuns: service}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/migration-runs", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"planName":"orders"`) || !strings.Contains(recorder.Body.String(), `"sourceEnvironmentName":"sida"`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestMigrationEventStreamResumesAfterLastEventID(t *testing.T) {
	runID := uuid.New()
	service := &migrationRunServiceStub{
		snapshot: domainmigration.RunSnapshot{Run: domainmigration.Run{ID: runID, Status: domainmigration.RunCompleted}},
		events:   []domainmigration.Event{{ID: 42, RunID: runID, Type: "STEP_SUCCEEDED", Severity: domainmigration.EventInfo, Message: "done", CreatedAt: time.Now().UTC()}},
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/migration-runs/id/events", nil)
	request.SetPathValue("runId", runID.String())
	request.Header.Set("Last-Event-ID", "41")
	recorder := httptest.NewRecorder()
	streamMigrationEventsHandler(Dependencies{MigrationRuns: service}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || service.afterID != 41 || !strings.Contains(recorder.Body.String(), "id: 42\nevent: migration\ndata:") {
		t.Fatalf("status=%d after=%d body=%s", recorder.Code, service.afterID, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/migration-runs/id/events", nil)
	request.SetPathValue("runId", runID.String())
	request.Header.Set("Last-Event-ID", "bad")
	recorder = httptest.NewRecorder()
	streamMigrationEventsHandler(Dependencies{MigrationRuns: service}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "MIGRATION_EVENT_CURSOR_INVALID") {
		t.Fatalf("invalid cursor status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestMigrationRunConflictIsStableAPIError(t *testing.T) {
	service := &migrationRunServiceStub{err: repository.ErrConflict}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/migration-runs/id/cancel", nil)
	request.SetPathValue("runId", uuid.NewString())
	recorder := httptest.NewRecorder()
	cancelMigrationRunHandler(Dependencies{MigrationRuns: service}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "MIGRATION_RUN_CONFLICT") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestListMigrationVolumeTransfers(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.SetPathValue("runId", uuid.NewString())
	recorder := httptest.NewRecorder()
	listMigrationVolumeTransfersHandler(Dependencies{MigrationRuns: &migrationRunServiceStub{}}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "db-0/data") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestCutoverRollbackAndReportHandlers(t *testing.T) {
	runID, administratorID := uuid.New(), uuid.New()
	service := &migrationRunServiceStub{snapshot: domainmigration.RunSnapshot{Run: domainmigration.Run{ID: runID, Status: domainmigration.RunAwaitingCutover}}}
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.SetPathValue("runId", runID.String())
	request = request.WithContext(context.WithValue(request.Context(), principalContextKey{}, identity.Principal{Administrator: identity.Administrator{ID: administratorID, Username: "admin"}}))
	recorder := httptest.NewRecorder()
	confirmMigrationCutoverHandler(Dependencies{MigrationRuns: service}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent || service.action != "cutover" || service.administratorID != administratorID {
		t.Fatalf("cutover status=%d action=%s administrator=%s", recorder.Code, service.action, service.administratorID)
	}

	request = httptest.NewRequest(http.MethodPost, "/", nil)
	request.SetPathValue("runId", runID.String())
	recorder = httptest.NewRecorder()
	rollbackMigrationRunHandler(Dependencies{MigrationRuns: service}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted || service.action != "rollback" {
		t.Fatalf("rollback status=%d action=%s", recorder.Code, service.action)
	}

	request = httptest.NewRequest(http.MethodGet, "/", nil)
	request.SetPathValue("runId", runID.String())
	recorder = httptest.NewRecorder()
	applied := false
	evidence := &migrationEvidenceServiceStub{topology: domainmigration.TopologyEvidence{RunID: runID,
		Source:   domainmigration.TopologyGraph{Nodes: []domainmigration.TopologyNode{{ID: "source", Side: "SOURCE", Kind: "Deployment", Name: "<script>alert(1)</script>"}}},
		Mappings: []domainmigration.ResourceMapping{{ID: "mapping", SourceNodeID: "source", Changes: []domainmigration.MappingChange{{Type: "IMAGE", SourceValue: "old", TargetValue: "new", Changed: true, Applied: &applied}}}},
	}, timeline: domainmigration.StepTimeline{RunID: runID}}
	migrationReportHandler(Dependencies{MigrationRuns: service, Evidence: evidence}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || service.action != "report" || !strings.Contains(recorder.Header().Get("Content-Disposition"), runID.String()+".html") || !strings.Contains(recorder.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("report status=%d action=%s headers=%v", recorder.Code, service.action, recorder.Header())
	}
	if strings.Contains(recorder.Body.String(), "cdn") || !strings.Contains(recorder.Body.String(), "迁移诊断报告") || strings.Contains(recorder.Body.String(), "<script>alert(1)</script>") || !strings.Contains(recorder.Body.String(), "未生效") {
		t.Fatalf("report is not self-contained HTML: %s", recorder.Body.String())
	}
}

func TestRestoreMigrationSourceHandler(t *testing.T) {
	runID := uuid.New()
	service := &migrationRunServiceStub{snapshot: domainmigration.RunSnapshot{Run: domainmigration.Run{ID: runID, Status: domainmigration.RunRollingBack}}}
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.SetPathValue("runId", runID.String())
	recorder := httptest.NewRecorder()
	restoreMigrationSourceHandler(Dependencies{MigrationRuns: service}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted || service.action != "restore-source" {
		t.Fatalf("restore source status=%d action=%s", recorder.Code, service.action)
	}
}

func TestMigrationEvidenceHandlers(t *testing.T) {
	runID := uuid.New()
	evidence := &migrationEvidenceServiceStub{
		topology: domainmigration.TopologyEvidence{RunID: runID, SnapshotOrigin: "RECONSTRUCTED"},
		timeline: domainmigration.StepTimeline{RunID: runID, Diagnosis: domainmigration.RunDiagnosis{State: "RUNNING"}},
	}
	for _, test := range []struct {
		method  string
		handler http.HandlerFunc
		needle  string
	}{
		{http.MethodGet, migrationTopologyHandler(Dependencies{Evidence: evidence}), `"snapshotOrigin":"RECONSTRUCTED"`},
		{http.MethodPost, refreshMigrationTopologyHandler(Dependencies{Evidence: evidence}), `"migrationRunId":"` + runID.String()},
		{http.MethodGet, migrationTimelineHandler(Dependencies{Evidence: evidence}), `"state":"RUNNING"`},
	} {
		request := httptest.NewRequest(test.method, "/", nil)
		request.SetPathValue("runId", runID.String())
		recorder := httptest.NewRecorder()
		test.handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), test.needle) {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	}
}

func TestCleanupMigrationArtifactsHandler(t *testing.T) {
	runID := uuid.New()
	service := &migrationArtifactServiceStub{result: domainmigration.ArtifactCleanupResult{RunID: runID, Clusters: []domainmigration.ArtifactCleanupCluster{{Role: "SOURCE", BackupsDeleted: 2}}}}
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.SetPathValue("runId", runID.String())
	recorder := httptest.NewRecorder()
	cleanupMigrationArtifactsHandler(Dependencies{Artifacts: service}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"backupsDeleted":2`) {
		t.Fatalf("cleanup status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
