package storageprofile

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"

	kubernetesadapter "github.com/smartx/sks-migration-center/internal/adapter/kubernetes"
	"github.com/smartx/sks-migration-center/internal/addon"
	domainenvironment "github.com/smartx/sks-migration-center/internal/domain/environment"
	"github.com/smartx/sks-migration-center/internal/domain/platform"
	"github.com/smartx/sks-migration-center/internal/domain/storage"
	"github.com/smartx/sks-migration-center/internal/repository"
)

const (
	managedNamespace = "sks-migration-system"
	nfsCSIVersion    = "4.13.4"
	nfsCSIName       = "nfs.csi.k8s.io"
)

var ErrInvalidInput = errors.New("invalid storage profile input")
var dnsPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[-A-Za-z0-9.]*[A-Za-z0-9])?$`)
var mountOptionPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+(?:=[A-Za-z0-9._-]+)?$`)

type Input struct {
	EnvironmentID    uuid.UUID             `json:"environmentId"`
	Name             string                `json:"name"`
	Type             storage.ProfileType   `json:"type"`
	StorageClassName string                `json:"storageClassName"`
	NFSServer        string                `json:"nfsServer,omitempty"`
	NFSExport        string                `json:"nfsExport,omitempty"`
	MountOptions     []string              `json:"mountOptions,omitempty"`
	ReclaimPolicy    storage.ReclaimPolicy `json:"reclaimPolicy"`
}

type Check struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type TestResult struct {
	Profile storage.Profile                      `json:"profile"`
	Probe   kubernetesadapter.StorageProbeResult `json:"probe"`
	Checks  []Check                              `json:"checks"`
}

type Vault interface {
	Resolve(context.Context, uuid.UUID) ([]byte, error)
}

type ClusterOperator interface {
	EnsureNamespace(context.Context, []byte, string) error
	HasCSIDriver(context.Context, []byte, string) (bool, error)
	EnsureNFSStorageClass(context.Context, []byte, kubernetesadapter.NFSStorageClassSpec) error
	ProbeStorageClass(context.Context, []byte, string, string, string) (kubernetesadapter.StorageProbeResult, error)
}

type AddonManager interface {
	InstallOrUpgrade(context.Context, []byte, addon.InstallRequest) (addon.ReleaseState, error)
}

type PlatformStore interface {
	UpsertAddonInstallation(context.Context, platform.AddonInstallation) error
}

type Service struct {
	profiles     repository.StorageRepository
	environments repository.EnvironmentRepository
	platform     PlatformStore
	vault        Vault
	cluster      ClusterOperator
	manager      AddonManager
	images       Images
	clock        func() time.Time
}

type Images struct {
	Plugin      string
	Provisioner string
	Resizer     string
	Liveness    string
	Registrar   string
	Probe       string
}

func NewService(profiles repository.StorageRepository, environments repository.EnvironmentRepository, platformStore PlatformStore, vault Vault, cluster ClusterOperator, manager AddonManager, images Images) (*Service, error) {
	if profiles == nil || environments == nil || platformStore == nil || vault == nil || cluster == nil || manager == nil {
		return nil, errors.New("storage profile repositories, vault, cluster operator and add-on manager are required")
	}
	for _, image := range []string{images.Plugin, images.Provisioner, images.Resizer, images.Liveness, images.Registrar, images.Probe} {
		parts := strings.Split(image, "@")
		if len(parts) != 2 || parts[0] == "" || !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(parts[1]) {
			return nil, errors.New("all NFS CSI and probe images must be pinned by sha256 digest")
		}
	}
	return &Service{profiles: profiles, environments: environments, platform: platformStore, vault: vault, cluster: cluster, manager: manager, images: images, clock: func() time.Time { return time.Now().UTC() }}, nil
}

func (s *Service) Create(ctx context.Context, input Input) (storage.Profile, error) {
	input = normalize(input)
	if err := validateInput(input); err != nil {
		return storage.Profile{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	environment, err := s.environments.Get(ctx, input.EnvironmentID)
	if err != nil {
		return storage.Profile{}, err
	}
	if err := validateEnvironment(environment); err != nil {
		return storage.Profile{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	provisioner := ""
	status := storage.StatusPending
	switch input.Type {
	case storage.ProfileExternalNFS:
		provisioner = nfsCSIName
	case storage.ProfileExistingNFS:
		if !hasStorageClass(environment, input.StorageClassName, nfsCSIName) {
			return storage.Profile{}, fmt.Errorf("%w: selected NFS StorageClass was not discovered", ErrInvalidInput)
		}
		provisioner = nfsCSIName
	case storage.ProfileSMTXBlock:
		if !hasSmartXStorageClass(environment, input.StorageClassName) {
			return storage.Profile{}, fmt.Errorf("%w: selected SmartX block StorageClass was not discovered", ErrInvalidInput)
		}
		provisioner, status = "com.smartx.elf-csi-driver", storage.StatusReady
	}
	now := s.clock()
	value := storage.Profile{
		ID: uuid.New(), EnvironmentID: input.EnvironmentID, Name: input.Name, Type: input.Type,
		StorageClassName: input.StorageClassName, Provisioner: provisioner, NFSServer: input.NFSServer,
		NFSExport: input.NFSExport, MountOptions: input.MountOptions, ReclaimPolicy: input.ReclaimPolicy,
		Status: status, CreatedAt: now, UpdatedAt: now,
	}
	if err := value.Validate(); err != nil {
		return storage.Profile{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if err := s.profiles.CreateStorageProfile(ctx, value); err != nil {
		return storage.Profile{}, err
	}
	return value, nil
}

func (s *Service) List(ctx context.Context, environmentID *uuid.UUID) ([]storage.Profile, error) {
	return s.profiles.ListStorageProfiles(ctx, environmentID)
}

func (s *Service) Install(ctx context.Context, id uuid.UUID) (TestResult, error) {
	profile, environment, kubeconfig, err := s.resolve(ctx, id)
	if err != nil {
		return TestResult{}, err
	}
	defer clearBytes(kubeconfig)
	if profile.Type != storage.ProfileExternalNFS && profile.Type != storage.ProfileExistingNFS {
		return TestResult{}, fmt.Errorf("%w: only NFS profiles have an install workflow", ErrInvalidInput)
	}
	profile.Status, profile.UpdatedAt = storage.StatusInstalling, s.clock()
	if err := s.profiles.UpdateStorageProfile(ctx, profile); err != nil {
		return TestResult{}, err
	}
	fail := func(stage string, cause error) (TestResult, error) {
		profile.Status, profile.UpdatedAt = storage.StatusFailed, s.clock()
		_ = s.profiles.UpdateStorageProfile(context.WithoutCancel(ctx), profile)
		return TestResult{}, fmt.Errorf("NFS profile %s: %w", stage, cause)
	}

	if err := s.cluster.EnsureNamespace(ctx, kubeconfig, managedNamespace); err != nil {
		return fail("prepare probe namespace", err)
	}
	driverExists, err := s.cluster.HasCSIDriver(ctx, kubeconfig, nfsCSIName)
	if err != nil {
		return fail("discover CSI driver", err)
	}
	installMode := "REUSED_EXISTING"
	if !driverExists {
		installMode = "INSTALLED_OFFLINE"
		if _, err := s.manager.InstallOrUpgrade(ctx, append([]byte(nil), kubeconfig...), addon.InstallRequest{
			ReleaseName: "sks-migration-nfs-csi", Namespace: "kube-system", ChartPath: "csi-driver-nfs", Version: nfsCSIVersion,
			Timeout: 20 * time.Minute, Values: map[string]any{"images": map[string]any{
				"nfs": s.images.Plugin, "provisioner": s.images.Provisioner, "resizer": s.images.Resizer,
				"liveness": s.images.Liveness, "registrar": s.images.Registrar,
			}},
		}); err != nil {
			return fail("install offline CSI driver", err)
		}
	}
	installation := platform.AddonInstallation{
		ID: uuid.New(), EnvironmentID: environment.ID, Type: platform.AddonNFSCSI, Version: nfsCSIVersion,
		Status: platform.InstallationReady, Values: map[string]any{"mode": installMode, "driver": nfsCSIName},
		Message: "NFS CSI Driver available", CreatedAt: s.clock(), UpdatedAt: s.clock(),
	}
	if err := s.platform.UpsertAddonInstallation(ctx, installation); err != nil {
		return fail("save CSI installation", err)
	}
	if profile.Type == storage.ProfileExternalNFS {
		if err := s.cluster.EnsureNFSStorageClass(ctx, kubeconfig, kubernetesadapter.NFSStorageClassSpec{
			Name: profile.StorageClassName, Server: profile.NFSServer, Export: profile.NFSExport,
			MountOptions: profile.MountOptions, ReclaimPolicy: corev1.PersistentVolumeReclaimPolicy(profile.ReclaimPolicy),
		}); err != nil {
			return fail("create StorageClass", err)
		}
	}
	probe, err := s.cluster.ProbeStorageClass(ctx, kubeconfig, managedNamespace, profile.StorageClassName, s.images.Probe)
	if err != nil {
		return fail("read/write probe", err)
	}
	profile.Status, profile.UpdatedAt = storage.StatusReady, s.clock()
	if err := s.profiles.UpdateStorageProfile(ctx, profile); err != nil {
		return TestResult{}, err
	}
	return TestResult{Profile: profile, Probe: probe, Checks: []Check{
		{Name: "NFS CSI", Status: "PASSED", Message: "nfs.csi.k8s.io 可用（" + installMode + "）"},
		{Name: "PVC Read/Write", Status: "PASSED", Message: "动态供给、写入、卸载后重新挂载读取和清理通过"},
	}}, nil
}

func (s *Service) Test(ctx context.Context, id uuid.UUID) (TestResult, error) {
	profile, _, kubeconfig, err := s.resolve(ctx, id)
	if err != nil {
		return TestResult{}, err
	}
	defer clearBytes(kubeconfig)
	if profile.Type != storage.ProfileExternalNFS && profile.Type != storage.ProfileExistingNFS {
		return TestResult{}, fmt.Errorf("%w: this probe is only valid for NFS profiles", ErrInvalidInput)
	}
	probe, err := s.cluster.ProbeStorageClass(ctx, kubeconfig, managedNamespace, profile.StorageClassName, s.images.Probe)
	if err != nil {
		return TestResult{}, err
	}
	return TestResult{Profile: profile, Probe: probe, Checks: []Check{{Name: "PVC Read/Write", Status: "PASSED", Message: "写入及重新挂载读取通过"}}}, nil
}

func (s *Service) resolve(ctx context.Context, id uuid.UUID) (storage.Profile, domainenvironment.Environment, []byte, error) {
	profile, err := s.profiles.GetStorageProfile(ctx, id)
	if err != nil {
		return storage.Profile{}, domainenvironment.Environment{}, nil, err
	}
	environment, err := s.environments.Get(ctx, profile.EnvironmentID)
	if err != nil {
		return storage.Profile{}, domainenvironment.Environment{}, nil, err
	}
	if err := validateEnvironment(environment); err != nil {
		return storage.Profile{}, domainenvironment.Environment{}, nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if environment.CredentialID == nil {
		return storage.Profile{}, domainenvironment.Environment{}, nil, fmt.Errorf("%w: environment kubeconfig is unavailable", ErrInvalidInput)
	}
	kubeconfig, err := s.vault.Resolve(ctx, *environment.CredentialID)
	if err != nil {
		return storage.Profile{}, domainenvironment.Environment{}, nil, err
	}
	return profile, environment, kubeconfig, nil
}

func normalize(input Input) Input {
	input.Name, input.StorageClassName = strings.TrimSpace(input.Name), strings.TrimSpace(input.StorageClassName)
	input.NFSServer, input.NFSExport = strings.TrimSpace(input.NFSServer), strings.TrimSpace(input.NFSExport)
	if input.ReclaimPolicy == "" {
		input.ReclaimPolicy = storage.ReclaimRetain
	}
	options := make([]string, 0, len(input.MountOptions))
	for _, option := range input.MountOptions {
		if value := strings.TrimSpace(option); value != "" {
			options = append(options, value)
		}
	}
	input.MountOptions = options
	return input
}

func validateInput(input Input) error {
	if input.EnvironmentID == uuid.Nil || input.Name == "" || input.StorageClassName == "" {
		return errors.New("environmentId, name and storageClassName are required")
	}
	if input.ReclaimPolicy != storage.ReclaimRetain && input.ReclaimPolicy != storage.ReclaimDelete {
		return errors.New("reclaimPolicy must be Retain or Delete")
	}
	if input.Type != storage.ProfileSMTXBlock && input.Type != storage.ProfileExistingNFS && input.Type != storage.ProfileExternalNFS {
		return errors.New("unsupported storage profile type")
	}
	if input.Type == storage.ProfileExternalNFS {
		if !validNFSServer(input.NFSServer) || !strings.HasPrefix(input.NFSExport, "/") || strings.Contains(input.NFSExport, "..") {
			return errors.New("external NFS requires a valid server and absolute export without parent traversal")
		}
	}
	for _, option := range input.MountOptions {
		if !mountOptionPattern.MatchString(option) {
			return errors.New("mountOptions contain an unsupported value")
		}
	}
	return nil
}

func validateEnvironment(value domainenvironment.Environment) error {
	if value.Kind != domainenvironment.KindKubernetes || value.Status != domainenvironment.StatusConnected {
		return errors.New("storage profiles require a connected Kubernetes environment")
	}
	return nil
}

func hasStorageClass(environment domainenvironment.Environment, name, provisioner string) bool {
	for _, value := range environment.Capabilities.StorageClasses {
		if value.Name == name && value.Provisioner == provisioner {
			return true
		}
	}
	return false
}

func hasSmartXStorageClass(environment domainenvironment.Environment, name string) bool {
	return hasStorageClass(environment, name, "com.smartx.elf-csi-driver") || hasStorageClass(environment, name, "smtx-elf-csi-driver")
}

func validNFSServer(value string) bool {
	return net.ParseIP(value) != nil || (len(value) <= 253 && dnsPattern.MatchString(value))
}

func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
