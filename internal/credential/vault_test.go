package credential

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/uuid"
	domaincredential "github.com/smartx/sks-migration-center/internal/domain/credential"
	"github.com/smartx/sks-migration-center/internal/repository"
	"github.com/smartx/sks-migration-center/internal/security"
)

type memoryCredentialRepository struct {
	record domaincredential.Record
}

func (r *memoryCredentialRepository) CreateCredential(_ context.Context, value domaincredential.Record) error {
	r.record = value
	return nil
}

func (r *memoryCredentialRepository) GetCredential(_ context.Context, id uuid.UUID) (domaincredential.Record, error) {
	if r.record.ID != id {
		return domaincredential.Record{}, repository.ErrNotFound
	}
	return r.record, nil
}

func (r *memoryCredentialRepository) ListCredentials(context.Context) ([]domaincredential.Record, error) {
	if r.record.ID == uuid.Nil {
		return []domaincredential.Record{}, nil
	}
	return []domaincredential.Record{r.record}, nil
}

func (r *memoryCredentialRepository) DeleteCredential(context.Context, uuid.UUID) error { return nil }

func TestVaultNeverReturnsPayloadFromStore(t *testing.T) {
	store := &memoryCredentialRepository{}
	keyring, _ := security.NewKeyring(3, map[int][]byte{3: bytes.Repeat([]byte{4}, 32)})
	vault, err := NewVault(store, keyring)
	if err != nil {
		t.Fatalf("NewVault: %v", err)
	}
	payload := []byte("apiVersion: v1\nusers:\n- token: secret")
	metadata, err := vault.Store(context.Background(), "source kubeconfig", domaincredential.TypeKubeconfig, payload)
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	if bytes.Contains(store.record.EncryptedPayload, []byte("secret")) || store.record.KeyVersion != 3 {
		t.Fatalf("credential was not envelope encrypted: %+v", store.record)
	}
	if metadata.Name != "source kubeconfig" || metadata.ID == uuid.Nil {
		t.Fatalf("unexpected metadata: %+v", metadata)
	}
	resolved, err := vault.Resolve(context.Background(), metadata.ID)
	if err != nil || !bytes.Equal(resolved, payload) {
		t.Fatalf("Resolve = %q, %v", resolved, err)
	}
}

func TestVaultListReturnsMetadataWithoutEncryptedPayload(t *testing.T) {
	store := &memoryCredentialRepository{}
	keyring, _ := security.NewKeyring(1, map[int][]byte{1: bytes.Repeat([]byte{7}, 32)})
	vault, _ := NewVault(store, keyring)
	created, err := vault.Store(context.Background(), "target registry", domaincredential.TypeRegistry, []byte(`{"username":"admin","password":"secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	values, err := vault.List(context.Background())
	if err != nil || len(values) != 1 || values[0].ID != created.ID || values[0].Name != "target registry" {
		t.Fatalf("unexpected credential metadata: %+v %v", values, err)
	}
}
