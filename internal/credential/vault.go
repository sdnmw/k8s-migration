package credential

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	domaincredential "github.com/smartx/sks-migration-center/internal/domain/credential"
	"github.com/smartx/sks-migration-center/internal/repository"
	"github.com/smartx/sks-migration-center/internal/security"
)

type Vault struct {
	repository repository.CredentialRepository
	keyring    *security.Keyring
}

func NewVault(store repository.CredentialRepository, keyring *security.Keyring) (*Vault, error) {
	if store == nil || keyring == nil {
		return nil, errors.New("credential repository and keyring are required")
	}
	return &Vault{repository: store, keyring: keyring}, nil
}

func (v *Vault) Store(ctx context.Context, name string, credentialType domaincredential.Type, plaintext []byte) (domaincredential.Metadata, error) {
	if name == "" || len(plaintext) == 0 || !credentialType.Valid() {
		return domaincredential.Metadata{}, errors.New("credential name and payload are required")
	}
	id := uuid.New()
	aad := associatedData(id, credentialType)
	encrypted, err := v.keyring.Encrypt(plaintext, aad)
	if err != nil {
		return domaincredential.Metadata{}, err
	}
	now := time.Now().UTC()
	record := domaincredential.Record{
		ID: id, Name: name, Type: credentialType, EncryptedPayload: encrypted,
		KeyVersion: v.keyring.ActiveVersion(), CreatedAt: now, UpdatedAt: now,
	}
	if err := v.repository.CreateCredential(ctx, record); err != nil {
		return domaincredential.Metadata{}, err
	}
	return metadata(record), nil
}

func (v *Vault) Resolve(ctx context.Context, id uuid.UUID) ([]byte, error) {
	record, err := v.repository.GetCredential(ctx, id)
	if err != nil {
		return nil, err
	}
	plaintext, err := v.keyring.Decrypt(record.EncryptedPayload, associatedData(record.ID, record.Type))
	if err != nil {
		return nil, fmt.Errorf("decrypt credential %s: %w", id, err)
	}
	return plaintext, nil
}

func (v *Vault) Delete(ctx context.Context, id uuid.UUID) error {
	return v.repository.DeleteCredential(ctx, id)
}

func (v *Vault) List(ctx context.Context) ([]domaincredential.Metadata, error) {
	records, err := v.repository.ListCredentials(ctx)
	if err != nil {
		return nil, err
	}
	values := make([]domaincredential.Metadata, 0, len(records))
	for _, record := range records {
		values = append(values, metadata(record))
	}
	return values, nil
}

func associatedData(id uuid.UUID, credentialType domaincredential.Type) []byte {
	return []byte(id.String() + ":" + string(credentialType))
}

func metadata(record domaincredential.Record) domaincredential.Metadata {
	return domaincredential.Metadata{
		ID: record.ID, Name: record.Name, Type: record.Type, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
}
