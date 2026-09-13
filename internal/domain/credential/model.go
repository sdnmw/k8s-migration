package credential

import (
	"time"

	"github.com/google/uuid"
)

type Type string

const (
	TypeKubeconfig Type = "KUBECONFIG"
	TypeSSH        Type = "SSH"
	TypeRegistry   Type = "REGISTRY"
	TypeS3         Type = "S3"
	TypeCompose    Type = "COMPOSE_DEFINITION"
)

type Record struct {
	ID               uuid.UUID
	Name             string
	Type             Type
	EncryptedPayload []byte
	KeyVersion       int
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type Metadata struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Type      Type      `json:"type"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (t Type) Valid() bool {
	switch t {
	case TypeKubeconfig, TypeSSH, TypeRegistry, TypeS3, TypeCompose:
		return true
	default:
		return false
	}
}
