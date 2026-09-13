package mapping

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	domainenvironment "github.com/smartx/sks-migration-center/internal/domain/environment"
	domainmapping "github.com/smartx/sks-migration-center/internal/domain/mapping"
	"github.com/smartx/sks-migration-center/internal/repository"
)

var ErrInvalidInput = errors.New("invalid mapping profile input")

type Service struct {
	profiles     repository.MappingRepository
	environments repository.EnvironmentRepository
	clock        func() time.Time
}

func NewService(profiles repository.MappingRepository, environments repository.EnvironmentRepository) (*Service, error) {
	if profiles == nil || environments == nil {
		return nil, errors.New("mapping and environment repositories are required")
	}
	return &Service{profiles: profiles, environments: environments, clock: func() time.Time { return time.Now().UTC() }}, nil
}

func (s *Service) Create(ctx context.Context, value domainmapping.Profile) (domainmapping.Profile, error) {
	value.ID = uuid.New()
	if err := s.validate(ctx, &value); err != nil {
		return domainmapping.Profile{}, err
	}
	now := s.clock()
	value.CreatedAt, value.UpdatedAt = now, now
	if err := s.profiles.Create(ctx, value); err != nil {
		return domainmapping.Profile{}, err
	}
	return value, nil
}

func (s *Service) Update(ctx context.Context, id uuid.UUID, value domainmapping.Profile) (domainmapping.Profile, error) {
	if id == uuid.Nil {
		return domainmapping.Profile{}, fmt.Errorf("%w: profileId is required", ErrInvalidInput)
	}
	stored, err := s.profiles.Get(ctx, id)
	if err != nil {
		return domainmapping.Profile{}, err
	}
	value.ID, value.CreatedAt = id, stored.CreatedAt
	if err := s.validate(ctx, &value); err != nil {
		return domainmapping.Profile{}, err
	}
	value.UpdatedAt = s.clock()
	if err := s.profiles.Update(ctx, value); err != nil {
		return domainmapping.Profile{}, err
	}
	return value, nil
}

func (s *Service) validate(ctx context.Context, value *domainmapping.Profile) error {
	if value.TargetEnvironmentID == uuid.Nil {
		return fmt.Errorf("%w: targetEnvironmentId is required", ErrInvalidInput)
	}
	target, err := s.environments.Get(ctx, value.TargetEnvironmentID)
	if err != nil {
		return err
	}
	if target.Role != domainenvironment.RoleTarget || target.Kind != domainenvironment.KindKubernetes {
		return fmt.Errorf("%w: target environment must be a Kubernetes target", ErrInvalidInput)
	}
	if err := value.NormalizeAndValidate(target.Capabilities); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	normalizeEmptySlices(value)
	return nil
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (domainmapping.Profile, error) {
	if id == uuid.Nil {
		return domainmapping.Profile{}, fmt.Errorf("%w: profileId is required", ErrInvalidInput)
	}
	return s.profiles.Get(ctx, id)
}

func (s *Service) List(ctx context.Context, targetEnvironmentID *uuid.UUID) ([]domainmapping.Profile, error) {
	if targetEnvironmentID != nil && *targetEnvironmentID == uuid.Nil {
		return nil, fmt.Errorf("%w: targetEnvironmentId must be a valid UUID", ErrInvalidInput)
	}
	return s.profiles.List(ctx, targetEnvironmentID)
}

func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	if id == uuid.Nil {
		return fmt.Errorf("%w: profileId is required", ErrInvalidInput)
	}
	return s.profiles.Delete(ctx, id)
}

func normalizeEmptySlices(value *domainmapping.Profile) {
	if value.Storage == nil {
		value.Storage = []domainmapping.KeyValue{}
	}
	if value.Namespaces == nil {
		value.Namespaces = []domainmapping.KeyValue{}
	}
	if value.Ingress == nil {
		value.Ingress = []domainmapping.KeyValue{}
	}
	if value.Registries == nil {
		value.Registries = []domainmapping.KeyValue{}
	}
	if value.NFS == nil {
		value.NFS = []domainmapping.NFSMapping{}
	}
	if value.NodeLabels == nil {
		value.NodeLabels = []domainmapping.NodeLabelMapping{}
	}
}
