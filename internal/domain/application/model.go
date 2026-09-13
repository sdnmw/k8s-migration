package application

import (
	"time"

	"github.com/google/uuid"
)

type SourceType string

const (
	SourceKubernetes SourceType = "KUBERNETES"
	SourceCompose    SourceType = "COMPOSE"
)

type ResourceSummary struct {
	APIVersion        string            `json:"apiVersion,omitempty"`
	Kind              string            `json:"kind"`
	Namespace         string            `json:"namespace,omitempty"`
	Name              string            `json:"name"`
	Labels            map[string]string `json:"labels,omitempty"`
	Images            []string          `json:"images,omitempty"`
	DataKeys          []string          `json:"dataKeys,omitempty"`
	SecretKeys        []string          `json:"secretKeys,omitempty"`
	Replicas          *int64            `json:"replicas,omitempty"`
	ReadyReplicas     *int64            `json:"readyReplicas,omitempty"`
	AvailableReplicas *int64            `json:"availableReplicas,omitempty"`
	Requests          map[string]string `json:"requests,omitempty"`
	Limits            map[string]string `json:"limits,omitempty"`
	MissingRequests   []string          `json:"missingRequests,omitempty"`
	IngressClassName  string            `json:"ingressClassName,omitempty"`
	ServiceType       string            `json:"serviceType,omitempty"`
	NodeSelectors     map[string]string `json:"nodeSelectors,omitempty"`
	NFSSources        []string          `json:"nfsSources,omitempty"`
	SecurityRisks     []string          `json:"securityRisks,omitempty"`
}

type ResourceReference struct {
	APIVersion string `json:"apiVersion,omitempty"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name"`
}

type Dependency struct {
	From     ResourceReference `json:"from"`
	To       ResourceReference `json:"to"`
	Type     string            `json:"type"`
	Required bool              `json:"required"`
}

type InventoryWarning struct {
	Code     string `json:"code"`
	Resource string `json:"resource,omitempty"`
	Message  string `json:"message"`
}

type VolumeSummary struct {
	Name             string   `json:"name"`
	Namespace        string   `json:"namespace,omitempty"`
	CapacityBytes    int64    `json:"capacityBytes,omitempty"`
	StorageClassName string   `json:"storageClassName,omitempty"`
	AccessModes      []string `json:"accessModes,omitempty"`
	VolumeMode       string   `json:"volumeMode,omitempty"`
	Source           string   `json:"source,omitempty"`
	Phase            string   `json:"phase,omitempty"`
}

type ImageSummary struct {
	Reference     string   `json:"reference"`
	Digest        string   `json:"digest,omitempty"`
	Architectures []string `json:"architectures,omitempty"`
}

type Inventory struct {
	Resources       []ResourceSummary  `json:"resources"`
	Workloads       []ResourceSummary  `json:"workloads"`
	Services        []ResourceSummary  `json:"services"`
	Ingresses       []ResourceSummary  `json:"ingresses"`
	ConfigMaps      []ResourceSummary  `json:"configMaps"`
	Secrets         []ResourceSummary  `json:"secrets"`
	ServiceAccounts []ResourceSummary  `json:"serviceAccounts"`
	Roles           []ResourceSummary  `json:"roles"`
	RoleBindings    []ResourceSummary  `json:"roleBindings"`
	CRDs            []ResourceSummary  `json:"crds"`
	CustomResources []ResourceSummary  `json:"customResources"`
	PVCs            []VolumeSummary    `json:"pvcs"`
	Images          []ImageSummary     `json:"images"`
	Dependencies    []Dependency       `json:"dependencies"`
	Warnings        []InventoryWarning `json:"warnings"`
	Counts          map[string]int     `json:"counts"`
	Compose         *ComposeInventory  `json:"compose,omitempty"`
}

type SourceApplication struct {
	ID                     uuid.UUID  `json:"id"`
	EnvironmentID          uuid.UUID  `json:"environmentId"`
	Name                   string     `json:"name"`
	SourceType             SourceType `json:"sourceType"`
	Namespace              string     `json:"namespace,omitempty"`
	Inventory              Inventory  `json:"inventory"`
	DefinitionCredentialID *uuid.UUID `json:"-"`
	CreatedAt              time.Time  `json:"createdAt"`
	UpdatedAt              time.Time  `json:"updatedAt"`
}

type ComposePort struct {
	Name      string `json:"name,omitempty"`
	Target    uint32 `json:"target"`
	Published string `json:"published,omitempty"`
	Protocol  string `json:"protocol"`
	HostIP    string `json:"hostIp,omitempty"`
}

type ComposeMount struct {
	Type     string `json:"type"`
	Source   string `json:"source,omitempty"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"readOnly"`
}

type ComposeService struct {
	Name            string         `json:"name"`
	Image           string         `json:"image,omitempty"`
	Build           bool           `json:"build"`
	Profiles        []string       `json:"profiles"`
	DependsOn       []string       `json:"dependsOn"`
	Ports           []ComposePort  `json:"ports"`
	Expose          []string       `json:"expose,omitempty"`
	Mounts          []ComposeMount `json:"mounts"`
	Networks        []string       `json:"networks"`
	EnvironmentKeys []string       `json:"environmentKeys"`
	Privileged      bool           `json:"privileged"`
	NetworkMode     string         `json:"networkMode,omitempty"`
}

type ComposeResource struct {
	Name        string `json:"name"`
	RuntimeName string `json:"runtimeName,omitempty"`
	Driver      string `json:"driver,omitempty"`
	External    bool   `json:"external"`
}

type ComposeWarning struct {
	Code    string `json:"code"`
	Service string `json:"service,omitempty"`
	Message string `json:"message"`
}

type ComposeInventory struct {
	ProjectName      string            `json:"projectName"`
	Status           string            `json:"status,omitempty"`
	ConfigFiles      []string          `json:"configFiles,omitempty"`
	WorkingDir       string            `json:"workingDir,omitempty"`
	Services         []ComposeService  `json:"services"`
	Volumes          []ComposeResource `json:"volumes"`
	Networks         []ComposeResource `json:"networks"`
	Configs          []string          `json:"configs"`
	Secrets          []string          `json:"secrets"`
	DisabledServices []string          `json:"disabledServices"`
	Warnings         []ComposeWarning  `json:"warnings"`
}

type ComposeDefinition struct {
	ComposeYAML     []byte `json:"composeYaml"`
	EnvironmentFile []byte `json:"environmentFile,omitempty"`
}
