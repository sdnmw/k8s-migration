package storage

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

type ProfileType string

const (
	ProfileSMTXBlock   ProfileType = "SMTX_BLOCK"
	ProfileExistingNFS ProfileType = "EXISTING_NFS_SC"
	ProfileExternalNFS ProfileType = "EXTERNAL_NFS_SC"
)

type ReclaimPolicy string

const (
	ReclaimRetain ReclaimPolicy = "Retain"
	ReclaimDelete ReclaimPolicy = "Delete"
)

const (
	StatusPending    = "PENDING"
	StatusInstalling = "INSTALLING"
	StatusReady      = "READY"
	StatusFailed     = "FAILED"
)

type Profile struct {
	ID               uuid.UUID     `json:"id"`
	EnvironmentID    uuid.UUID     `json:"environmentId"`
	Name             string        `json:"name"`
	Type             ProfileType   `json:"type"`
	StorageClassName string        `json:"storageClassName"`
	Provisioner      string        `json:"provisioner,omitempty"`
	NFSServer        string        `json:"nfsServer,omitempty"`
	NFSExport        string        `json:"nfsExport,omitempty"`
	MountOptions     []string      `json:"mountOptions,omitempty"`
	ReclaimPolicy    ReclaimPolicy `json:"reclaimPolicy"`
	Status           string        `json:"status"`
	CreatedAt        time.Time     `json:"createdAt"`
	UpdatedAt        time.Time     `json:"updatedAt"`
}

func (p Profile) Validate() error {
	if p.EnvironmentID == uuid.Nil || strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.StorageClassName) == "" {
		return errors.New("environmentId, name and storageClassName are required")
	}
	if p.Type == ProfileExternalNFS && (strings.TrimSpace(p.NFSServer) == "" || strings.TrimSpace(p.NFSExport) == "") {
		return errors.New("external NFS profile requires nfsServer and nfsExport")
	}
	if p.ReclaimPolicy != ReclaimRetain && p.ReclaimPolicy != ReclaimDelete {
		return errors.New("reclaimPolicy must be Retain or Delete")
	}
	if p.Type != ProfileSMTXBlock && p.Type != ProfileExistingNFS && p.Type != ProfileExternalNFS {
		return errors.New("unsupported storage profile type")
	}
	if p.Status != StatusPending && p.Status != StatusInstalling && p.Status != StatusReady && p.Status != StatusFailed {
		return errors.New("unsupported storage profile status")
	}
	return nil
}
