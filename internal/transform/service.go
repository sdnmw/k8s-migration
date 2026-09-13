package transform

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/repository"
)

var ErrInvalidInput = errors.New("invalid transform input")

type Service struct {
	profiles repository.MappingRepository
	engine   *Engine
}

func NewService(profiles repository.MappingRepository, engine *Engine) (*Service, error) {
	if profiles == nil || engine == nil {
		return nil, errors.New("mapping repository and transform engine are required")
	}
	return &Service{profiles: profiles, engine: engine}, nil
}

func (s *Service) Preview(ctx context.Context, profileID uuid.UUID, manifests []byte) (Result, error) {
	if profileID == uuid.Nil {
		return Result{}, fmt.Errorf("%w: profileId is required", ErrInvalidInput)
	}
	profile, err := s.profiles.Get(ctx, profileID)
	if err != nil {
		return Result{}, err
	}
	return s.engine.Transform(manifests, profile)
}
