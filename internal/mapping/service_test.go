package mapping

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	domainenvironment "github.com/smartx/sks-migration-center/internal/domain/environment"
	domainmapping "github.com/smartx/sks-migration-center/internal/domain/mapping"
	"github.com/smartx/sks-migration-center/internal/repository"
)

type profileStore struct{ value domainmapping.Profile }

func (s *profileStore) Create(_ context.Context, value domainmapping.Profile) error {
	s.value = value
	return nil
}
func (s *profileStore) Get(context.Context, uuid.UUID) (domainmapping.Profile, error) {
	if s.value.ID == uuid.Nil {
		return domainmapping.Profile{}, repository.ErrNotFound
	}
	return s.value, nil
}
func (s *profileStore) List(context.Context, *uuid.UUID) ([]domainmapping.Profile, error) {
	return []domainmapping.Profile{s.value}, nil
}
func (s *profileStore) Update(_ context.Context, value domainmapping.Profile) error {
	s.value = value
	return nil
}
func (s *profileStore) Delete(context.Context, uuid.UUID) error { return nil }

type targetStore struct{ value domainenvironment.Environment }

func (s *targetStore) Create(context.Context, domainenvironment.Environment) error { return nil }
func (s *targetStore) Get(context.Context, uuid.UUID) (domainenvironment.Environment, error) {
	return s.value, nil
}
func (s *targetStore) List(context.Context) ([]domainenvironment.Environment, error) { return nil, nil }
func (s *targetStore) Update(context.Context, domainenvironment.Environment) error   { return nil }
func (s *targetStore) Delete(context.Context, uuid.UUID) error                       { return nil }

func TestServiceCreateValidatesTargetAndNormalizesEmptyLists(t *testing.T) {
	targetID := uuid.New()
	profiles := &profileStore{}
	service, _ := NewService(profiles, &targetStore{value: domainenvironment.Environment{ID: targetID, Role: domainenvironment.RoleTarget, Kind: domainenvironment.KindKubernetes}})
	now := time.Date(2026, 9, 3, 7, 0, 0, 0, time.UTC)
	service.clock = func() time.Time { return now }
	value, err := service.Create(context.Background(), domainmapping.Profile{Name: " profile ", TargetEnvironmentID: targetID})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if value.ID == uuid.Nil || value.Name != "profile" || value.Storage == nil || value.NFS == nil || !value.CreatedAt.Equal(now) {
		t.Fatalf("unexpected profile: %+v", value)
	}
}

func TestServiceRejectsSourceAsTarget(t *testing.T) {
	targetID := uuid.New()
	service, _ := NewService(&profileStore{}, &targetStore{value: domainenvironment.Environment{ID: targetID, Role: domainenvironment.RoleSource, Kind: domainenvironment.KindKubernetes}})
	if _, err := service.Create(context.Background(), domainmapping.Profile{Name: "profile", TargetEnvironmentID: targetID}); err == nil {
		t.Fatal("source environment must be rejected")
	}
}
