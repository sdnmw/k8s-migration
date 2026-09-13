package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	assessmentservice "github.com/smartx/sks-migration-center/internal/assessment"
	domainassessment "github.com/smartx/sks-migration-center/internal/domain/assessment"
	"github.com/smartx/sks-migration-center/internal/repository"
)

type stubAssessmentService struct {
	value       domainassessment.Assessment
	application uuid.UUID
	target      uuid.UUID
	err         error
}

func (s *stubAssessmentService) Create(_ context.Context, applicationID, targetID uuid.UUID) (domainassessment.Assessment, error) {
	s.application, s.target = applicationID, targetID
	return s.value, s.err
}
func (s *stubAssessmentService) Get(context.Context, uuid.UUID) (domainassessment.Assessment, error) {
	return s.value, s.err
}

func TestCreateAssessmentReturnsCompletedResult(t *testing.T) {
	applicationID, targetID := uuid.New(), uuid.New()
	service := &stubAssessmentService{value: domainassessment.Assessment{ID: uuid.New(), ApplicationID: applicationID, Score: 70, BlockerCount: 1, Status: domainassessment.StatusCompleted, Issues: []domainassessment.Issue{}}}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/assessments", strings.NewReader(`{"applicationId":"`+applicationID.String()+`","targetEnvironmentId":"`+targetID.String()+`"}`))
	recorder := httptest.NewRecorder()
	createAssessmentHandler(Dependencies{Assessments: service}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated || !strings.Contains(recorder.Body.String(), `"score":70`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if service.application != applicationID || service.target != targetID {
		t.Fatalf("request not forwarded: %+v", service)
	}
}

func TestAssessmentHandlersMapErrors(t *testing.T) {
	for name, testCase := range map[string]struct {
		err    error
		status int
		code   string
	}{
		"invalid":   {err: assessmentservice.ErrInvalidInput, status: http.StatusBadRequest, code: "ASSESSMENT_INVALID"},
		"not found": {err: repository.ErrNotFound, status: http.StatusNotFound, code: "ASSESSMENT_NOT_FOUND"},
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/assessments", strings.NewReader(`{"applicationId":"`+uuid.NewString()+`","targetEnvironmentId":"`+uuid.NewString()+`"}`))
			recorder := httptest.NewRecorder()
			createAssessmentHandler(Dependencies{Assessments: &stubAssessmentService{err: testCase.err}}).ServeHTTP(recorder, request)
			if recorder.Code != testCase.status || !strings.Contains(recorder.Body.String(), testCase.code) {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}
