package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/domain/identity"
	domainmigration "github.com/smartx/sks-migration-center/internal/domain/migration"
	migrationservice "github.com/smartx/sks-migration-center/internal/migration"
	"github.com/smartx/sks-migration-center/internal/repository"
)

type MigrationRunService interface {
	Start(context.Context, uuid.UUID) (domainmigration.RunSnapshot, error)
	List(context.Context) ([]domainmigration.RunSummary, error)
	Retry(context.Context, uuid.UUID) (domainmigration.RunSnapshot, error)
	Get(context.Context, uuid.UUID) (domainmigration.RunSnapshot, error)
	Cancel(context.Context, uuid.UUID) (domainmigration.RunSnapshot, error)
	Rollback(context.Context, uuid.UUID) (domainmigration.RunSnapshot, error)
	RestoreSource(context.Context, uuid.UUID) (domainmigration.RunSnapshot, error)
	ConfirmCutover(context.Context, uuid.UUID, uuid.UUID) error
	Report(context.Context, uuid.UUID) (domainmigration.Report, error)
	Events(context.Context, uuid.UUID, int64, int) ([]domainmigration.Event, error)
	VolumeTransfers(context.Context, uuid.UUID) ([]domainmigration.VolumeTransfer, error)
}

type MigrationArtifactService interface {
	Cleanup(context.Context, uuid.UUID) (domainmigration.ArtifactCleanupResult, error)
}

func deleteMigrationRunHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(r.PathValue("runId"))
		if err != nil {
			writeMigrationRunProblem(w, 400, "MIGRATION_RUN_INVALID", "任务编号无效")
			return
		}
		service, ok := deps.MigrationRuns.(interface {
			Delete(context.Context, uuid.UUID) error
		})
		if !ok {
			writeInternalProblem(w)
			return
		}
		if err := service.Delete(r.Context(), id); err != nil {
			auditMigrationRun(deps, r, "migration_run.delete", &id, "FAILURE", nil)
			writeMigrationRunError(w, err)
			return
		}
		auditMigrationRun(deps, r, "migration_run.delete", &id, "SUCCESS", nil)
		w.WriteHeader(http.StatusNoContent)
	}
}

type MigrationEvidenceService interface {
	Topology(context.Context, uuid.UUID) (domainmigration.TopologyEvidence, error)
	RefreshTopology(context.Context, uuid.UUID) (domainmigration.TopologyEvidence, error)
	Timeline(context.Context, uuid.UUID) (domainmigration.StepTimeline, error)
}

func listMigrationRunsHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.MigrationRuns == nil {
			writeInternalProblem(w)
			return
		}
		values, err := deps.MigrationRuns.List(r.Context())
		if err != nil {
			writeMigrationRunError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, values)
	}
}

func cleanupMigrationArtifactsHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Artifacts == nil {
			writeInternalProblem(w)
			return
		}
		runID, ok := migrationRunID(w, r)
		if !ok {
			return
		}
		value, err := deps.Artifacts.Cleanup(r.Context(), runID)
		if err != nil {
			writeMigrationRunError(w, err)
			auditMigrationRun(deps, r, "migration_run.cleanup", &runID, "FAILURE", nil)
			return
		}
		auditMigrationRun(deps, r, "migration_run.cleanup", &runID, "SUCCESS", map[string]any{"clusters": len(value.Clusters), "retained": value.Retained})
		writeJSON(w, http.StatusOK, value)
	}
}

func confirmMigrationCutoverHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.MigrationRuns == nil {
			writeInternalProblem(w)
			return
		}
		runID, ok := migrationRunID(w, r)
		if !ok {
			return
		}
		principal, ok := principalFromContext(r.Context())
		if !ok {
			writeUnauthorizedProblem(w)
			return
		}
		if err := deps.MigrationRuns.ConfirmCutover(r.Context(), runID, principal.Administrator.ID); err != nil {
			writeMigrationRunError(w, err)
			auditMigrationRun(deps, r, "migration_run.cutover", &runID, "FAILURE", nil)
			return
		}
		auditMigrationRun(deps, r, "migration_run.cutover", &runID, "SUCCESS", nil)
		w.WriteHeader(http.StatusNoContent)
	}
}

func rollbackMigrationRunHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.MigrationRuns == nil {
			writeInternalProblem(w)
			return
		}
		runID, ok := migrationRunID(w, r)
		if !ok {
			return
		}
		value, err := deps.MigrationRuns.Rollback(r.Context(), runID)
		if err != nil {
			writeMigrationRunError(w, err)
			auditMigrationRun(deps, r, "migration_run.rollback", &runID, "FAILURE", nil)
			return
		}
		auditMigrationRun(deps, r, "migration_run.rollback", &runID, "SUCCESS", nil)
		writeJSON(w, http.StatusAccepted, value)
	}
}

func restoreMigrationSourceHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.MigrationRuns == nil {
			writeInternalProblem(w)
			return
		}
		runID, ok := migrationRunID(w, r)
		if !ok {
			return
		}
		value, err := deps.MigrationRuns.RestoreSource(r.Context(), runID)
		if err != nil {
			writeMigrationRunError(w, err)
			auditMigrationRun(deps, r, "migration_run.restore_source", &runID, "FAILURE", nil)
			return
		}
		auditMigrationRun(deps, r, "migration_run.restore_source", &runID, "SUCCESS", nil)
		writeJSON(w, http.StatusAccepted, value)
	}
}

func migrationReportHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.MigrationRuns == nil || deps.Evidence == nil {
			writeInternalProblem(w)
			return
		}
		runID, ok := migrationRunID(w, r)
		if !ok {
			return
		}
		value, err := deps.MigrationRuns.Report(r.Context(), runID)
		if err != nil {
			writeMigrationRunError(w, err)
			return
		}
		topology, err := deps.Evidence.Topology(r.Context(), runID)
		if err != nil {
			writeMigrationRunError(w, err)
			return
		}
		timeline, err := deps.Evidence.Timeline(r.Context(), runID)
		if err != nil {
			writeMigrationRunError(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"migration-report-%s.html\"", runID))
		w.WriteHeader(http.StatusOK)
		if err := renderMigrationHTMLReport(w, migrationHTMLReport{Report: value, Topology: topology, Timeline: timeline}); err != nil {
			return
		}
	}
}

func migrationReportDataHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.MigrationRuns == nil || deps.Evidence == nil {
			writeInternalProblem(w)
			return
		}
		runID, ok := migrationRunID(w, r)
		if !ok {
			return
		}
		report, err := deps.MigrationRuns.Report(r.Context(), runID)
		if err != nil {
			writeMigrationRunError(w, err)
			return
		}
		topology, err := deps.Evidence.Topology(r.Context(), runID)
		if err != nil {
			writeMigrationRunError(w, err)
			return
		}
		timeline, err := deps.Evidence.Timeline(r.Context(), runID)
		if err != nil {
			writeMigrationRunError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, migrationHTMLReport{Report: report, Topology: topology, Timeline: timeline})
	}
}

func migrationTopologyHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Evidence == nil {
			writeInternalProblem(w)
			return
		}
		runID, ok := migrationRunID(w, r)
		if !ok {
			return
		}
		value, err := deps.Evidence.Topology(r.Context(), runID)
		if err != nil {
			writeMigrationRunError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	}
}

func refreshMigrationTopologyHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Evidence == nil {
			writeInternalProblem(w)
			return
		}
		runID, ok := migrationRunID(w, r)
		if !ok {
			return
		}
		value, err := deps.Evidence.RefreshTopology(r.Context(), runID)
		if err != nil {
			writeMigrationRunError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	}
}

func migrationTimelineHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Evidence == nil {
			writeInternalProblem(w)
			return
		}
		runID, ok := migrationRunID(w, r)
		if !ok {
			return
		}
		value, err := deps.Evidence.Timeline(r.Context(), runID)
		if err != nil {
			writeMigrationRunError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	}
}

func listMigrationVolumeTransfersHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.MigrationRuns == nil {
			writeInternalProblem(w)
			return
		}
		runID, ok := migrationRunID(w, r)
		if !ok {
			return
		}
		values, err := deps.MigrationRuns.VolumeTransfers(r.Context(), runID)
		if err != nil {
			writeMigrationRunError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, values)
	}
}

func startMigrationRunHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.MigrationRuns == nil {
			writeInternalProblem(w)
			return
		}
		planID, ok := migrationPlanID(w, r)
		if !ok {
			return
		}
		value, err := deps.MigrationRuns.Start(r.Context(), planID)
		if err != nil {
			writeMigrationRunError(w, err)
			auditMigrationRun(deps, r, "migration_run.start", nil, "FAILURE", map[string]any{"migrationPlanId": planID})
			return
		}
		auditMigrationRun(deps, r, "migration_run.start", &value.Run.ID, "SUCCESS", map[string]any{"migrationPlanId": planID, "runNumber": value.Run.RunNumber})
		writeJSON(w, http.StatusAccepted, value)
	}
}

func getMigrationRunHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.MigrationRuns == nil {
			writeInternalProblem(w)
			return
		}
		runID, ok := migrationRunID(w, r)
		if !ok {
			return
		}
		value, err := deps.MigrationRuns.Get(r.Context(), runID)
		if err != nil {
			writeMigrationRunError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	}
}

func cancelMigrationRunHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.MigrationRuns == nil {
			writeInternalProblem(w)
			return
		}
		runID, ok := migrationRunID(w, r)
		if !ok {
			return
		}
		value, err := deps.MigrationRuns.Cancel(r.Context(), runID)
		if err != nil {
			writeMigrationRunError(w, err)
			auditMigrationRun(deps, r, "migration_run.cancel", &runID, "FAILURE", nil)
			return
		}
		auditMigrationRun(deps, r, "migration_run.cancel", &runID, "SUCCESS", map[string]any{"status": value.Run.Status})
		writeJSON(w, http.StatusAccepted, value)
	}
}

func retryMigrationRunHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.MigrationRuns == nil {
			writeInternalProblem(w)
			return
		}
		runID, ok := migrationRunID(w, r)
		if !ok {
			return
		}
		value, err := deps.MigrationRuns.Retry(r.Context(), runID)
		if err != nil {
			writeMigrationRunError(w, err)
			auditMigrationRun(deps, r, "migration_run.retry", &runID, "FAILURE", nil)
			return
		}
		auditMigrationRun(deps, r, "migration_run.retry", &value.Run.ID, "SUCCESS", map[string]any{"previousRunId": runID, "runNumber": value.Run.RunNumber})
		writeJSON(w, http.StatusAccepted, value)
	}
}

func streamMigrationEventsHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.MigrationRuns == nil {
			writeInternalProblem(w)
			return
		}
		runID, ok := migrationRunID(w, r)
		if !ok {
			return
		}
		afterID, err := parseLastEventID(r.Header.Get("Last-Event-ID"))
		if err != nil {
			writeMigrationRunProblem(w, http.StatusBadRequest, "MIGRATION_EVENT_CURSOR_INVALID", "Last-Event-ID 必须是非负整数。")
			return
		}
		snapshot, err := deps.MigrationRuns.Get(r.Context(), runID)
		if err != nil {
			writeMigrationRunError(w, err)
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeInternalProblem(w)
			return
		}
		_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache, no-transform")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()
		poll := time.NewTicker(time.Second)
		heartbeat := time.NewTicker(15 * time.Second)
		defer poll.Stop()
		defer heartbeat.Stop()
		for {
			events, listErr := deps.MigrationRuns.Events(r.Context(), runID, afterID, 200)
			if listErr != nil {
				return
			}
			for _, event := range events {
				payload, marshalErr := json.Marshal(event)
				if marshalErr != nil {
					return
				}
				if _, writeErr := fmt.Fprintf(w, "id: %d\nevent: migration\ndata: %s\n\n", event.ID, payload); writeErr != nil {
					return
				}
				afterID = event.ID
			}
			if len(events) > 0 {
				flusher.Flush()
			}
			if domainmigration.IsTerminal(snapshot.Run.Status) {
				return
			}
			select {
			case <-r.Context().Done():
				return
			case <-heartbeat.C:
				if _, writeErr := fmt.Fprint(w, ": heartbeat\n\n"); writeErr != nil {
					return
				}
				flusher.Flush()
			case <-poll.C:
				snapshot, err = deps.MigrationRuns.Get(r.Context(), runID)
				if err != nil {
					return
				}
			}
		}
	}
}

func parseLastEventID(value string) (int64, error) {
	if strings.TrimSpace(value) == "" {
		return 0, nil
	}
	result, err := strconv.ParseInt(value, 10, 64)
	if err != nil || result < 0 {
		return 0, errors.New("invalid event cursor")
	}
	return result, nil
}

func migrationRunID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("runId"))
	if err != nil {
		writeMigrationRunProblem(w, http.StatusBadRequest, "MIGRATION_RUN_INVALID", "runId 必须是有效的 UUID。")
		return uuid.Nil, false
	}
	return id, true
}

func writeMigrationRunError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, repository.ErrNotFound):
		writeMigrationRunProblem(w, http.StatusNotFound, "MIGRATION_RUN_NOT_FOUND", "迁移任务或迁移计划不存在。")
	case errors.Is(err, repository.ErrConflict), errors.Is(err, migrationservice.ErrInvalidRunState):
		writeMigrationRunProblem(w, http.StatusConflict, "MIGRATION_RUN_CONFLICT", "迁移任务当前状态不允许执行该操作。")
	case errors.Is(err, migrationservice.ErrInvalidInput):
		writeMigrationRunProblem(w, http.StatusBadRequest, "MIGRATION_RUN_INVALID", strings.TrimPrefix(err.Error(), migrationservice.ErrInvalidInput.Error()+": "))
	default:
		writeInternalProblem(w)
	}
}

func writeMigrationRunProblem(w http.ResponseWriter, status int, code, detail string) {
	writeProblem(w, problem{Type: "/problems/migration-run", Title: "Migration run error", Status: status, Detail: detail, Code: code})
}

func auditMigrationRun(deps Dependencies, r *http.Request, action string, objectID *uuid.UUID, result string, detail map[string]any) {
	if deps.Audit == nil {
		return
	}
	actor := "unknown"
	if principal, ok := principalFromContext(r.Context()); ok {
		actor = principal.Administrator.Username
	}
	if err := deps.Audit.RecordAudit(r.Context(), identity.AuditEvent{Actor: actor, Action: action, ObjectType: "migration_run", ObjectID: objectID, Result: result, Detail: detail}); err != nil && deps.Logger != nil {
		deps.Logger.Error("migration run audit write failed", "action", action, "error", err)
	}
}
