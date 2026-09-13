package platform

import (
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

type AddonType string

const (
	AddonMinIO  AddonType = "MINIO"
	AddonVelero AddonType = "VELERO"
	AddonNFSCSI AddonType = "NFS_CSI"
)

type InstallationStatus string

const (
	InstallationPending    InstallationStatus = "PENDING"
	InstallationInstalling InstallationStatus = "INSTALLING"
	InstallationReady      InstallationStatus = "READY"
	InstallationFailed     InstallationStatus = "FAILED"
	InstallationRemoved    InstallationStatus = "REMOVED"
)

type ObjectStorageProfile struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	Endpoint     string    `json:"endpoint"`
	Bucket       string    `json:"bucket"`
	Region       string    `json:"region"`
	CredentialID uuid.UUID `json:"credentialId"`
	TLSVerify    bool      `json:"tlsVerify"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type AddonInstallation struct {
	ID            uuid.UUID          `json:"id"`
	EnvironmentID uuid.UUID          `json:"environmentId"`
	Type          AddonType          `json:"type"`
	Version       string             `json:"version"`
	Status        InstallationStatus `json:"status"`
	Values        map[string]any     `json:"values,omitempty"`
	Message       string             `json:"message,omitempty"`
	CreatedAt     time.Time          `json:"createdAt"`
	UpdatedAt     time.Time          `json:"updatedAt"`
}

func (p ObjectStorageProfile) Validate() error {
	endpoint, err := url.Parse(p.Endpoint)
	if p.ID == uuid.Nil || strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.Bucket) == "" || p.CredentialID == uuid.Nil {
		return errors.New("object storage id, name, bucket and credential are required")
	}
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || (endpoint.Path != "" && endpoint.Path != "/") || endpoint.RawQuery != "" || endpoint.Fragment != "" || !p.TLSVerify {
		return errors.New("managed object storage endpoint must be a verified HTTPS origin")
	}
	return nil
}

func (a AddonInstallation) Validate() error {
	if a.ID == uuid.Nil || a.EnvironmentID == uuid.Nil || strings.TrimSpace(a.Version) == "" {
		return errors.New("add-on id, environment and version are required")
	}
	if a.Type != AddonMinIO && a.Type != AddonVelero && a.Type != AddonNFSCSI {
		return errors.New("unsupported add-on type")
	}
	switch a.Status {
	case InstallationPending, InstallationInstalling, InstallationReady, InstallationFailed, InstallationRemoved:
		return nil
	default:
		return errors.New("unsupported add-on installation status")
	}
}
