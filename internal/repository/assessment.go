package repository

import (
	"context"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/domain/assessment"
)

type AssessmentRepository interface {
	Create(context.Context, assessment.Assessment) error
	Get(context.Context, uuid.UUID) (assessment.Assessment, error)
}
