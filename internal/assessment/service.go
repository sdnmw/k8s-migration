package assessment

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	domainapplication "github.com/smartx/sks-migration-center/internal/domain/application"
	domainassessment "github.com/smartx/sks-migration-center/internal/domain/assessment"
	domainenvironment "github.com/smartx/sks-migration-center/internal/domain/environment"
	"github.com/smartx/sks-migration-center/internal/repository"
)

var ErrInvalidInput = errors.New("invalid assessment input")

type Service struct {
	applications repository.ApplicationRepository
	environments repository.EnvironmentRepository
	assessments  repository.AssessmentRepository
	engine       *Engine
}

func NewService(applications repository.ApplicationRepository, environments repository.EnvironmentRepository, assessments repository.AssessmentRepository, engine *Engine) (*Service, error) {
	if applications == nil || environments == nil || assessments == nil || engine == nil {
		return nil, errors.New("application, environment and assessment repositories and engine are required")
	}
	return &Service{applications: applications, environments: environments, assessments: assessments, engine: engine}, nil
}

func (s *Service) Create(ctx context.Context, applicationID, targetEnvironmentID uuid.UUID) (domainassessment.Assessment, error) {
	if applicationID == uuid.Nil || targetEnvironmentID == uuid.Nil {
		return domainassessment.Assessment{}, fmt.Errorf("%w: applicationId and targetEnvironmentId are required", ErrInvalidInput)
	}
	application, err := s.applications.Get(ctx, applicationID)
	if err != nil {
		return domainassessment.Assessment{}, err
	}
	target, err := s.environments.Get(ctx, targetEnvironmentID)
	if err != nil {
		return domainassessment.Assessment{}, err
	}
	if target.Role != domainenvironment.RoleTarget || target.Kind != domainenvironment.KindKubernetes {
		return domainassessment.Assessment{}, fmt.Errorf("%w: target environment must be a Kubernetes target", ErrInvalidInput)
	}
	if target.Status != domainenvironment.StatusConnected || target.CapabilitiesUpdatedAt == nil {
		return domainassessment.Assessment{}, fmt.Errorf("%w: target environment capabilities must be connected and refreshed", ErrInvalidInput)
	}
	sourceCapabilities := domainenvironment.Capabilities{}
	if application.SourceType == domainapplication.SourceKubernetes && application.EnvironmentID != uuid.Nil {
		source, err := s.environments.Get(ctx, application.EnvironmentID)
		if err != nil {
			return domainassessment.Assessment{}, err
		}
		sourceCapabilities = source.Capabilities
	}
	result := s.engine.Assess(Context{Application: application, Source: sourceCapabilities, Target: target.Capabilities})
	if err := s.assessments.Create(ctx, result); err != nil {
		return domainassessment.Assessment{}, err
	}
	return result, nil
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (domainassessment.Assessment, error) {
	if id == uuid.Nil {
		return domainassessment.Assessment{}, fmt.Errorf("%w: assessmentId is required", ErrInvalidInput)
	}
	return s.assessments.Get(ctx, id)
}
