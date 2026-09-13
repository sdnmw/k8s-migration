package transform

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/domain/mapping"
	"github.com/smartx/sks-migration-center/internal/repository"
)

type transformMappingRepository struct {
	profile mapping.Profile
	err     error
}

func (r *transformMappingRepository) Create(context.Context, mapping.Profile) error { return nil }
func (r *transformMappingRepository) Get(context.Context, uuid.UUID) (mapping.Profile, error) {
	return r.profile, r.err
}
func (r *transformMappingRepository) List(context.Context, *uuid.UUID) ([]mapping.Profile, error) {
	return nil, nil
}
func (r *transformMappingRepository) Update(context.Context, mapping.Profile) error { return nil }
func (r *transformMappingRepository) Delete(context.Context, uuid.UUID) error       { return nil }

func TestServiceLoadsProfileAndTransforms(t *testing.T) {
	repository := &transformMappingRepository{profile: mapping.Profile{Namespaces: []mapping.KeyValue{{Source: "old", Target: "new"}}}}
	service, err := NewService(repository, NewEngine())
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Preview(context.Background(), uuid.New(), []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: settings\n  namespace: old\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Documents) != 1 || result.Documents[0].Namespace != "new" {
		t.Fatalf("unexpected transform result: %+v", result)
	}
}

func TestServiceRejectsMissingProfileAndPropagatesNotFound(t *testing.T) {
	service, _ := NewService(&transformMappingRepository{err: repository.ErrNotFound}, NewEngine())
	if _, err := service.Preview(context.Background(), uuid.Nil, []byte("x")); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid input, got %v", err)
	}
	if _, err := service.Preview(context.Background(), uuid.New(), []byte("x")); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}
