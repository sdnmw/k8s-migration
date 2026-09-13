package environment

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Role string

const (
	RoleSource Role = "SOURCE"
	RoleTarget Role = "TARGET"
)

type Kind string

const (
	KindKubernetes    Kind = "KUBERNETES"
	KindDockerCompose Kind = "DOCKER_COMPOSE"
)

type Status string

const (
	StatusPending      Status = "PENDING"
	StatusConnected    Status = "CONNECTED"
	StatusDisconnected Status = "DISCONNECTED"
	StatusError        Status = "ERROR"
)

type Capabilities struct {
	KubernetesVersion          string                   `json:"kubernetesVersion,omitempty"`
	Architectures              []string                 `json:"architectures,omitempty"`
	OperatingSystems           []string                 `json:"operatingSystems,omitempty"`
	NodeCount                  int                      `json:"nodeCount,omitempty"`
	NamespaceCount             int                      `json:"namespaceCount,omitempty"`
	StorageClasses             []StorageClass           `json:"storageClasses,omitempty"`
	CSIDrivers                 []string                 `json:"csiDrivers,omitempty"`
	VolumeSnapshotClasses      []string                 `json:"volumeSnapshotClasses,omitempty"`
	VolumeSnapshotClassDetails []VolumeSnapshotClass    `json:"volumeSnapshotClassDetails,omitempty"`
	CSIDataMover               CSIDataMoverCapabilities `json:"csiDataMover"`
	IngressClasses             []string                 `json:"ingressClasses,omitempty"`
	APIGroups                  []string                 `json:"apiGroups,omitempty"`
	Allocatable                map[string]string        `json:"allocatable,omitempty"`
	Security                   map[string]any           `json:"security,omitempty"`
	Runtime                    map[string]string        `json:"runtime,omitempty"`
}

type StorageClass struct {
	Name              string `json:"name"`
	Provisioner       string `json:"provisioner"`
	Default           bool   `json:"default"`
	AllowExpansion    bool   `json:"allowExpansion"`
	VolumeBindingMode string `json:"volumeBindingMode,omitempty"`
}

type VolumeSnapshotClass struct {
	Name           string `json:"name"`
	Driver         string `json:"driver"`
	DeletionPolicy string `json:"deletionPolicy,omitempty"`
}

type CSIDataMoverCapabilities struct {
	SnapshotAPI         bool  `json:"snapshotApi"`
	DataUploadAPI       bool  `json:"dataUploadApi"`
	DataDownloadAPI     bool  `json:"dataDownloadApi"`
	BackupRepositoryAPI bool  `json:"backupRepositoryApi"`
	EnableCSI           bool  `json:"enableCsi"`
	NodeAgentDesired    int32 `json:"nodeAgentDesired"`
	NodeAgentReady      int32 `json:"nodeAgentReady"`
	BackupReady         bool  `json:"backupReady"`
	RestoreReady        bool  `json:"restoreReady"`
}

func (c Capabilities) SnapshotDriverForStorageClass(name string) (string, bool) {
	provisioner := ""
	for _, storageClass := range c.StorageClasses {
		if storageClass.Name == name || (name == "" && storageClass.Default) {
			provisioner = storageClass.Provisioner
			break
		}
	}
	if provisioner == "" {
		return "", false
	}
	for _, snapshotClass := range c.VolumeSnapshotClassDetails {
		if snapshotClass.Driver == provisioner {
			return snapshotClass.Driver, true
		}
	}
	return provisioner, false
}

func (c Capabilities) SupportsRawBlockDataMover() bool {
	if len(c.OperatingSystems) == 0 {
		return false
	}
	for _, operatingSystem := range c.OperatingSystems {
		if !strings.EqualFold(operatingSystem, "linux") {
			return false
		}
	}
	return true
}

type Environment struct {
	ID                    uuid.UUID    `json:"id"`
	Name                  string       `json:"name"`
	Role                  Role         `json:"role"`
	Kind                  Kind         `json:"kind"`
	Endpoint              string       `json:"endpoint,omitempty"`
	CredentialID          *uuid.UUID   `json:"-"`
	Status                Status       `json:"status"`
	StatusMessage         string       `json:"statusMessage,omitempty"`
	Capabilities          Capabilities `json:"capabilities"`
	CapabilitiesUpdatedAt *time.Time   `json:"capabilitiesUpdatedAt,omitempty"`
	CreatedAt             time.Time    `json:"createdAt"`
	UpdatedAt             time.Time    `json:"updatedAt"`
}

type CheckStatus string

const (
	CheckPassed  CheckStatus = "PASSED"
	CheckFailed  CheckStatus = "FAILED"
	CheckWarning CheckStatus = "WARNING"
)

type ConnectionCheck struct {
	Name    string      `json:"name"`
	Status  CheckStatus `json:"status"`
	Message string      `json:"message"`
}

type ConnectionTest struct {
	Success bool              `json:"success"`
	Checks  []ConnectionCheck `json:"checks"`
}

func (e Environment) Validate() error {
	if strings.TrimSpace(e.Name) == "" {
		return errors.New("environment name is required")
	}
	if e.Role != RoleSource && e.Role != RoleTarget {
		return errors.New("environment role must be SOURCE or TARGET")
	}
	if e.Kind != KindKubernetes && e.Kind != KindDockerCompose {
		return errors.New("environment kind must be KUBERNETES or DOCKER_COMPOSE")
	}
	if e.Role == RoleTarget && e.Kind != KindKubernetes {
		return errors.New("target environment must be KUBERNETES")
	}
	return nil
}
