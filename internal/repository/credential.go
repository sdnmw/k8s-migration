package repository

import (
	"context"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/domain/credential"
)

type CredentialRepository interface {
	CreateCredential(context.Context, credential.Record) error
	GetCredential(context.Context, uuid.UUID) (credential.Record, error)
	ListCredentials(context.Context) ([]credential.Record, error)
	DeleteCredential(context.Context, uuid.UUID) error
}
