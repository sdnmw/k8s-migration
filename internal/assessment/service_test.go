package assessment

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	domainapplication "github.com/smartx/sks-migration-center/internal/domain/application"
	domainassessment "github.com/smartx/sks-migration-center/internal/domain/assessment"
	domainenvironment "github.com/smartx/sks-migration-center/internal/domain/environment"
	"github.com/smartx/sks-migration-center/internal/repository"
)

type serviceApplicationStore struct {
	value domainapplication.SourceApplication
}

func (s *serviceApplicationStore) Upsert(context.Context, domainapplication.SourceApplication) (domainapplication.SourceApplication, error) {
	return domainapplication.SourceApplication{}, nil
}
func (s *serviceApplicationStore) Get(context.Context, uuid.UUID) (domainapplication.SourceApplication, error) {
	if s.value.ID == uuid.Nil {
		return domainapplication.SourceApplication{}, repository.ErrNotFound
	}
	return s.value, nil
}
func (s *serviceApplicationStore) ListByEnvironment(context.Context, uuid.UUID) ([]domainapplication.SourceApplication, error) {
	return nil, nil
}

type serviceEnvironmentStore struct{ value domainenvironment.Environment }

func (s *serviceEnvironmentStore) Create(context.Context, domainenvironment.Environment) error {
	return nil
}
func (s *serviceEnvironmentStore) Get(context.Context, uuid.UUID) (domainenvironment.Environment, error) {
	if s.value.ID == uuid.Nil {
		return domainenvironment.Environment{}, repository.ErrNotFound
	}
	return s.value, nil
}
func (s *serviceEnvironmentStore) List(context.Context) ([]domainenvironment.Environment, error) {
	return nil, nil
}
func (s *serviceEnvironmentStore) Update(context.Context, domainenvironment.Environment) error {
	return nil
}
func (s *serviceEnvironmentStore) Delete(context.Context, uuid.UUID) error { return nil }

type serviceAssessmentStore struct{ value domainassessment.Assessment }

func (s *serviceAssessmentStore) Create(_ context.Context, value domainassessment.Assessment) error {
	s.value = value
	return nil
}
func (s *serviceAssessmentStore) Get(context.Context, uuid.UUID) (domainassessment.Assessment, error) {
	if s.value.ID == uuid.Nil {
		return domainassessment.Assessment{}, repository.ErrNotFound
	}
	return s.value, nil
}

func TestServiceCreatesAndPersistsAssessmentAgainstConnectedTarget(t *testing.T) {
	applicationID := uuid.New()
	now := time.Now().UTC()
	applications := &serviceApplicationStore{value: domainapplication.SourceApplication{ID: applicationID, Inventory: domainapplication.Inventory{
		Workloads: []domainapplication.ResourceSummary{{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "shop", Name: "api"}},
	}}}
	environments := &serviceEnvironmentStore{value: domainenvironment.Environment{ID: uuid.New(), Role: domainenvironment.RoleTarget, Kind: domainenvironment.KindKubernetes, Status: domainenvironment.StatusConnected, CapabilitiesUpdatedAt: &now}}
	assessments := &serviceAssessmentStore{}
	service, err := NewService(applications, environments, assessments, DefaultEngine())
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	value, err := service.Create(context.Background(), applicationID, environments.value.ID)
	if err != nil {
		t.Fatalf("create assessment: %v", err)
	}
	if value.ID == uuid.Nil || assessments.value.ID != value.ID || value.WarningCount != 1 || value.InfoCount != 1 || value.Score != 88 {
		t.Fatalf("unexpected persisted assessment: %+v", value)
	}
}

func TestServiceRejectsTargetWithoutFreshCapabilities(t *testing.T) {
	applicationID := uuid.New()
	targetID := uuid.New()
	service, _ := NewService(
		&serviceApplicationStore{value: domainapplication.SourceApplication{ID: applicationID}},
		&serviceEnvironmentStore{value: domainenvironment.Environment{ID: targetID, Role: domainenvironment.RoleTarget, Kind: domainenvironment.KindKubernetes, Status: domainenvironment.StatusConnected}},
		&serviceAssessmentStore{}, DefaultEngine(),
	)
	_, err := service.Create(context.Background(), applicationID, targetID)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid input, got %v", err)
	}
}
