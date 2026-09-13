package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	assessmentservice "github.com/smartx/sks-migration-center/internal/assessment"
	domainassessment "github.com/smartx/sks-migration-center/internal/domain/assessment"
	"github.com/smartx/sks-migration-center/internal/domain/identity"
	"github.com/smartx/sks-migration-center/internal/repository"
)

type AssessmentService interface {
	Create(context.Context, uuid.UUID, uuid.UUID) (domainassessment.Assessment, error)
	Get(context.Context, uuid.UUID) (domainassessment.Assessment, error)
}

type createAssessmentRequest struct {
	ApplicationID       uuid.UUID `json:"applicationId"`
	TargetEnvironmentID uuid.UUID `json:"targetEnvironmentId"`
}

func createAssessmentHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Assessments == nil {
			writeInternalProblem(w)
			return
		}
		var request createAssessmentRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			writeAssessmentProblem(w, http.StatusBadRequest, "ASSESSMENT_INVALID", "请求必须包含有效的 applicationId 和 targetEnvironmentId。")
			return
		}
		value, err := deps.Assessments.Create(r.Context(), request.ApplicationID, request.TargetEnvironmentID)
		if err != nil {
			writeAssessmentError(w, err)
			auditAssessment(deps, r, "assessment.create", nil, "FAILURE", map[string]any{"applicationId": request.ApplicationID, "targetEnvironmentId": request.TargetEnvironmentID})
			return
		}
		auditAssessment(deps, r, "assessment.create", &value.ID, "SUCCESS", map[string]any{"applicationId": value.ApplicationID, "score": value.Score, "blockerCount": value.BlockerCount})
		writeJSON(w, http.StatusCreated, value)
	}
}

func getAssessmentHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Assessments == nil {
			writeInternalProblem(w)
			return
		}
		id, err := uuid.Parse(r.PathValue("assessmentId"))
		if err != nil {
			writeAssessmentProblem(w, http.StatusBadRequest, "ASSESSMENT_INVALID", "assessmentId 必须是有效的 UUID。")
			return
		}
		value, err := deps.Assessments.Get(r.Context(), id)
		if err != nil {
			writeAssessmentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	}
}

func writeAssessmentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, repository.ErrNotFound):
		writeAssessmentProblem(w, http.StatusNotFound, "ASSESSMENT_NOT_FOUND", "指定的应用、目标环境或评估不存在。")
	case errors.Is(err, assessmentservice.ErrInvalidInput):
		writeAssessmentProblem(w, http.StatusBadRequest, "ASSESSMENT_INVALID", strings.TrimPrefix(err.Error(), assessmentservice.ErrInvalidInput.Error()+": "))
	default:
		writeInternalProblem(w)
	}
}

func writeAssessmentProblem(w http.ResponseWriter, status int, code, detail string) {
	writeProblem(w, problem{Type: "/problems/assessment", Title: "Assessment error", Status: status, Detail: detail, Code: code})
}

func auditAssessment(deps Dependencies, r *http.Request, action string, objectID *uuid.UUID, result string, detail map[string]any) {
	if deps.Audit == nil {
		return
	}
	actor := "unknown"
	if principal, ok := principalFromContext(r.Context()); ok {
		actor = principal.Administrator.Username
	}
	if err := deps.Audit.RecordAudit(r.Context(), identity.AuditEvent{Actor: actor, Action: action, ObjectType: "assessment", ObjectID: objectID, Result: result, Detail: detail}); err != nil && deps.Logger != nil {
		deps.Logger.Error("assessment audit write failed", "action", action, "error", err)
	}
}
