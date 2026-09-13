package velero

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	kubernetesadapter "github.com/smartx/sks-migration-center/internal/adapter/kubernetes"
	veleroadapter "github.com/smartx/sks-migration-center/internal/adapter/velero"
	"github.com/smartx/sks-migration-center/internal/addon"
	domainenvironment "github.com/smartx/sks-migration-center/internal/domain/environment"
	"github.com/smartx/sks-migration-center/internal/domain/platform"
	"github.com/smartx/sks-migration-center/internal/objectstorage"
	"github.com/smartx/sks-migration-center/internal/repository"
)

const (
	Namespace             = "velero"
	ReleaseName           = "sks-migration-velero"
	BackupLocationName    = "migration-minio"
	CredentialSecretName  = "sks-migration-cloud-credentials"
	CredentialSecretKey   = "cloud"
	VeleroVersion         = "1.18.1"
	AWSPluginVersion      = "1.14.0"
	OfficialChartVersion  = "12.1.0"
	OfficialChartArtifact = "velero-12.1.0.tgz"
)

var ErrInvalidInput = errors.New("invalid Velero add-on input")
var ErrUnmanagedInstallation = errors.New("existing Velero installation is not managed by SKS Migration Center")
var digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

type Images struct {
	Velero string
	AWS    string
}

type InstallInput struct {
	EnvironmentID        uuid.UUID `json:"environmentId"`
	ObjectStorageProfile uuid.UUID `json:"objectStorageProfileId"`
	Prefix               string    `json:"prefix,omitempty"`
	KubeletRoot          string    `json:"kubeletRoot,omitempty"`
}

type ReuseInput struct {
	EnvironmentID        uuid.UUID `json:"environmentId"`
	ObjectStorageProfile uuid.UUID `json:"objectStorageProfileId"`
	Prefix               string    `json:"prefix,omitempty"`
}

type InstallResult struct {
	Installation platform.AddonInstallation                `json:"installation"`
	Location     veleroadapter.BackupStorageLocationStatus `json:"backupStorageLocation"`
	Checks       []Check                                   `json:"checks"`
}

type Check struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type Vault interface {
	Resolve(context.Context, uuid.UUID) ([]byte, error)
}

type AddonManager interface {
	InstallOrUpgrade(context.Context, []byte, addon.InstallRequest) (addon.ReleaseState, error)
	Uninstall([]byte, string, string, time.Duration) error
}

func (s *Service) Uninstall(ctx context.Context, environmentID uuid.UUID) (platform.AddonInstallation, error) {
	if environmentID == uuid.Nil {
		return platform.AddonInstallation{}, fmt.Errorf("%w: environmentId is required", ErrInvalidInput)
	}
	installation, err := s.platform.GetAddonInstallation(ctx, environmentID, platform.AddonVelero)
	if err != nil {
		return platform.AddonInstallation{}, err
	}
	if installation.Status == platform.InstallationRemoved {
		return installation, nil
	}
	if installation.Values["managed"] != true {
		return platform.AddonInstallation{}, fmt.Errorf("%w: reused Velero must be removed by its owner", ErrUnmanagedInstallation)
	}
	environment, err := s.environments.Get(ctx, environmentID)
	if err != nil {
		return platform.AddonInstallation{}, err
	}
	if environment.CredentialID == nil {
		return platform.AddonInstallation{}, fmt.Errorf("%w: environment kubeconfig is unavailable", ErrInvalidInput)
	}
	kubeconfig, err := s.vault.Resolve(ctx, *environment.CredentialID)
	if err != nil {
		return platform.AddonInstallation{}, err
	}
	defer clearBytes(kubeconfig)
	if err := s.manager.Uninstall(append([]byte(nil), kubeconfig...), Namespace, ReleaseName, 30*time.Minute); err != nil {
		return platform.AddonInstallation{}, err
	}
	installation.Status, installation.Message, installation.UpdatedAt = platform.InstallationRemoved, "Velero Helm release 已卸载；MinIO 数据与凭证 Secret 保留", s.clock()
	if err := s.platform.UpsertAddonInstallation(ctx, installation); err != nil {
		return platform.AddonInstallation{}, err
	}
	return installation, nil
}

// Reuse connects an existing Velero server and node-agent to the migration
// repository without changing their Deployment or DaemonSet. Only a uniquely
// named credential Secret and BackupStorageLocation are created.
func (s *Service) Reuse(ctx context.Context, input ReuseInput) (InstallResult, error) {
	input.Prefix = strings.Trim(strings.TrimSpace(input.Prefix), "/")
	if input.EnvironmentID == uuid.Nil || input.ObjectStorageProfile == uuid.Nil || strings.Contains(input.Prefix, "..") || strings.ContainsAny(input.Prefix, "\\\n\r") {
		return InstallResult{}, fmt.Errorf("%w: environmentId, objectStorageProfileId and a valid prefix are required", ErrInvalidInput)
	}
	environment, err := s.environments.Get(ctx, input.EnvironmentID)
	if err != nil {
		return InstallResult{}, err
	}
	if err := validateEnvironment(environment); err != nil {
		return InstallResult{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if environment.CredentialID == nil {
		return InstallResult{}, fmt.Errorf("%w: environment kubeconfig is unavailable", ErrInvalidInput)
	}
	profile, err := s.platform.GetObjectStorageProfile(ctx, input.ObjectStorageProfile)
	if err != nil {
		return InstallResult{}, err
	}
	kubeconfig, err := s.vault.Resolve(ctx, *environment.CredentialID)
	if err != nil {
		return InstallResult{}, err
	}
	defer clearBytes(kubeconfig)
	existing, err := s.cluster.InspectVeleroInstallation(ctx, kubeconfig, Namespace)
	if err != nil {
		return InstallResult{}, err
	}
	if !existing.Exists || existing.ServerImage == "" || existing.NodeAgentImage == "" {
		return InstallResult{}, fmt.Errorf("%w: reusable Velero server and node-agent were not found", ErrInvalidInput)
	}
	encoded, err := s.vault.Resolve(ctx, profile.CredentialID)
	if err != nil {
		return InstallResult{}, err
	}
	defer clearBytes(encoded)
	var credential objectstorage.Credential
	if err := json.Unmarshal(encoded, &credential); err != nil || credential.AccessKey == "" || credential.SecretKey == "" {
		return InstallResult{}, fmt.Errorf("%w: stored object storage credential is invalid", ErrInvalidInput)
	}
	cloud := cloudCredential(credential)
	defer clearBytes(cloud)
	if err := s.cluster.PutOpaqueSecret(ctx, kubeconfig, Namespace, CredentialSecretName, map[string][]byte{CredentialSecretKey: cloud}); err != nil {
		return InstallResult{}, err
	}
	location, err := s.cr.EnsureBackupStorageLocation(ctx, kubeconfig, veleroadapter.BackupStorageLocationSpec{
		Namespace: Namespace, Name: BackupLocationName, Provider: "aws", Bucket: profile.Bucket, Prefix: input.Prefix,
		Region: profile.Region, Endpoint: profile.Endpoint, CredentialSecret: CredentialSecretName, CredentialKey: CredentialSecretKey,
		CABundle: []byte(credential.CABundle), AccessMode: backupLocationAccessMode(environment.Role),
	})
	if err != nil {
		return InstallResult{}, err
	}
	location, err = s.waitForLocation(ctx, kubeconfig, location)
	if err != nil {
		return InstallResult{}, err
	}
	now := s.clock()
	installation := platform.AddonInstallation{
		ID: uuid.New(), EnvironmentID: environment.ID, Type: platform.AddonVelero, Version: existingVeleroVersion(existing.ServerImage),
		Status: platform.InstallationReady, CreatedAt: now, UpdatedAt: now,
		Values: map[string]any{
			"managed": false, "mode": "REUSED", "serverImage": existing.ServerImage, "nodeAgentImage": existing.NodeAgentImage,
			"objectStorageProfileId": profile.ID.String(), "backupStorageLocation": BackupLocationName,
		},
		Message: "复用集群已有 Velero 和 node-agent；迁移专用 MinIO BSL 已就绪",
	}
	if err := s.platform.UpsertAddonInstallation(ctx, installation); err != nil {
		return InstallResult{}, err
	}
	return InstallResult{Installation: installation, Location: location, Checks: []Check{
		{Name: "Velero Server", Status: "PASSED", Message: "已连接集群现有 Velero"},
		{Name: "Node Agent", Status: "PASSED", Message: "已发现集群现有 node-agent"},
		{Name: "Backup Storage Location", Status: "PASSED", Message: "迁移专用 MinIO BSL 状态为 Available"},
	}}, nil
}

type ClusterOperator interface {
	InspectVeleroInstallation(context.Context, []byte, string) (kubernetesadapter.VeleroInstallation, error)
	EnsureAddonNamespace(context.Context, []byte, string) error
	PutOpaqueSecret(context.Context, []byte, string, string, map[string][]byte) error
}

type CRClient interface {
	EnsureBackupStorageLocation(context.Context, []byte, veleroadapter.BackupStorageLocationSpec) (veleroadapter.BackupStorageLocationStatus, error)
	BackupStorageLocationStatus(context.Context, []byte, string, string) (veleroadapter.BackupStorageLocationStatus, error)
}

type Service struct {
	platform     repository.PlatformRepository
	environments repository.EnvironmentRepository
	vault        Vault
	manager      AddonManager
	cluster      ClusterOperator
	cr           CRClient
	images       Images
	clock        func() time.Time
}

func NewService(platformRepository repository.PlatformRepository, environments repository.EnvironmentRepository, vault Vault, manager AddonManager, cluster ClusterOperator, cr CRClient, images Images) (*Service, error) {
	if platformRepository == nil || environments == nil || vault == nil || manager == nil || cluster == nil || cr == nil {
		return nil, errors.New("Velero repositories, vault, add-on manager, cluster operator and CR client are required")
	}
	if err := validateImages(images); err != nil {
		return nil, err
	}
	return &Service{
		platform: platformRepository, environments: environments, vault: vault, manager: manager, cluster: cluster, cr: cr, images: images,
		clock: func() time.Time { return time.Now().UTC() },
	}, nil
}

func (s *Service) Install(ctx context.Context, input InstallInput) (InstallResult, error) {
	input.Prefix, input.KubeletRoot = strings.Trim(strings.TrimSpace(input.Prefix), "/"), strings.TrimSpace(input.KubeletRoot)
	if input.KubeletRoot == "" {
		input.KubeletRoot = "/var/lib/kubelet"
	}
	if err := validateInput(input); err != nil {
		return InstallResult{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	environment, err := s.environments.Get(ctx, input.EnvironmentID)
	if err != nil {
		return InstallResult{}, err
	}
	if err := validateEnvironment(environment); err != nil {
		return InstallResult{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	profile, err := s.platform.GetObjectStorageProfile(ctx, input.ObjectStorageProfile)
	if err != nil {
		return InstallResult{}, err
	}
	if environment.CredentialID == nil {
		return InstallResult{}, fmt.Errorf("%w: environment kubeconfig is unavailable", ErrInvalidInput)
	}

	now := s.clock()
	installation := platform.AddonInstallation{
		ID: uuid.New(), EnvironmentID: environment.ID, Type: platform.AddonVelero, Version: VeleroVersion,
		Status: platform.InstallationInstalling, CreatedAt: now, UpdatedAt: now,
		Values: map[string]any{
			"managed": true, "chartVersion": OfficialChartVersion, "awsPluginVersion": AWSPluginVersion, "objectStorageProfileId": profile.ID.String(),
			"backupStorageLocation": BackupLocationName, "nodeAgent": true, "kubeletRoot": input.KubeletRoot,
		},
	}
	if err := s.platform.UpsertAddonInstallation(ctx, installation); err != nil {
		return InstallResult{}, err
	}
	fail := func(stage string, cause error) (InstallResult, error) {
		installation.Status, installation.Message, installation.UpdatedAt = platform.InstallationFailed, "Velero 安装失败："+stage, s.clock()
		_ = s.platform.UpsertAddonInstallation(context.WithoutCancel(ctx), installation)
		return InstallResult{}, fmt.Errorf("Velero install %s: %w", stage, cause)
	}

	kubeconfig, err := s.vault.Resolve(ctx, *environment.CredentialID)
	if err != nil {
		return fail("读取集群凭证", err)
	}
	defer clearBytes(kubeconfig)
	existing, err := s.cluster.InspectVeleroInstallation(ctx, kubeconfig, Namespace)
	if err != nil {
		return fail("检查已有 Velero", err)
	}
	if existing.Exists && (existing.ManagedBy != "Helm" || existing.ReleaseName != ReleaseName) {
		return fail("检查已有 Velero", fmt.Errorf("%w: image=%s managedBy=%s release=%s", ErrUnmanagedInstallation, existing.ServerImage, existing.ManagedBy, existing.ReleaseName))
	}
	encoded, err := s.vault.Resolve(ctx, profile.CredentialID)
	if err != nil {
		return fail("读取对象存储凭证", err)
	}
	defer clearBytes(encoded)
	var credential objectstorage.Credential
	if err := json.Unmarshal(encoded, &credential); err != nil || credential.AccessKey == "" || credential.SecretKey == "" {
		return fail("解析对象存储凭证", errors.New("stored object storage credential is invalid"))
	}
	cloud := cloudCredential(credential)
	defer clearBytes(cloud)
	if err := s.cluster.EnsureAddonNamespace(ctx, kubeconfig, Namespace); err != nil {
		return fail("准备命名空间", err)
	}
	if err := s.cluster.PutOpaqueSecret(ctx, kubeconfig, Namespace, CredentialSecretName, map[string][]byte{CredentialSecretKey: cloud}); err != nil {
		return fail("写入对象存储 Secret", err)
	}

	veleroRepo, veleroDigest := splitImage(s.images.Velero)
	pluginRepo, pluginDigest := splitImage(s.images.AWS)
	values := map[string]any{
		"image":       map[string]any{"repository": veleroRepo, "digest": veleroDigest},
		"credentials": map[string]any{"useSecret": true, "existingSecret": CredentialSecretName},
		"initContainers": []any{map[string]any{
			"name": "velero-plugin-for-aws", "image": pluginRepo + "@" + pluginDigest, "imagePullPolicy": "IfNotPresent",
			"volumeMounts": []any{map[string]any{"mountPath": "/target", "name": "plugins"}},
		}},
		"configuration": map[string]any{
			"backupStorageLocation": []any{}, "defaultVolumesToFsBackup": true, "uploaderType": "kopia", "features": "EnableCSI",
		},
		"snapshotsEnabled": false, "deployNodeAgent": true,
		"nodeAgent": map[string]any{
			"podVolumePath": input.KubeletRoot + "/pods", "pluginVolumePath": input.KubeletRoot + "/plugins",
			"containerSecurityContext": map[string]any{"privileged": true},
		},
		"namespace": map[string]any{"labels": map[string]any{
			"pod-security.kubernetes.io/enforce": "privileged", "pod-security.kubernetes.io/enforce-version": "latest",
		}},
	}
	if _, err := s.manager.InstallOrUpgrade(ctx, append([]byte(nil), kubeconfig...), addon.InstallRequest{
		ReleaseName: ReleaseName, Namespace: Namespace, ChartPath: OfficialChartArtifact, Version: OfficialChartVersion,
		Values: values, CreateNamespace: false, Timeout: 30 * time.Minute,
	}); err != nil {
		return fail("部署 Velero 和 node-agent", err)
	}
	location, err := s.cr.EnsureBackupStorageLocation(ctx, kubeconfig, veleroadapter.BackupStorageLocationSpec{
		Namespace: Namespace, Name: BackupLocationName, Provider: "aws", Bucket: profile.Bucket, Prefix: input.Prefix,
		Region: profile.Region, Endpoint: profile.Endpoint, CredentialSecret: CredentialSecretName, CredentialKey: CredentialSecretKey,
		CABundle: []byte(credential.CABundle), AccessMode: backupLocationAccessMode(environment.Role),
	})
	if err != nil {
		return fail("创建 BackupStorageLocation", err)
	}
	location, err = s.waitForLocation(ctx, kubeconfig, location)
	if err != nil {
		return fail("等待 BackupStorageLocation 可用", err)
	}
	installation.Status, installation.Message, installation.UpdatedAt = platform.InstallationReady, "Velero、node-agent 和 MinIO BSL 已就绪", s.clock()
	if err := s.platform.UpsertAddonInstallation(ctx, installation); err != nil {
		return InstallResult{}, err
	}
	return InstallResult{Installation: installation, Location: location, Checks: []Check{
		{Name: "Velero Server", Status: "PASSED", Message: "Velero 1.18.1 Deployment 已就绪"},
		{Name: "Node Agent", Status: "PASSED", Message: "Kopia node-agent 已按节点部署"},
		{Name: "Backup Storage Location", Status: "PASSED", Message: "MinIO BSL 状态为 Available"},
	}}, nil
}

func (s *Service) Status(ctx context.Context, environmentID uuid.UUID) (InstallResult, error) {
	installation, err := s.platform.GetAddonInstallation(ctx, environmentID, platform.AddonVelero)
	if err != nil {
		return InstallResult{}, err
	}
	if installation.Status == platform.InstallationRemoved {
		return InstallResult{Installation: installation, Location: veleroadapter.BackupStorageLocationStatus{Name: BackupLocationName, Phase: "Unknown", Message: "Velero is uninstalled"}}, nil
	}
	environment, err := s.environments.Get(ctx, environmentID)
	if err != nil {
		return InstallResult{}, err
	}
	if environment.CredentialID == nil {
		return InstallResult{}, fmt.Errorf("%w: environment kubeconfig is unavailable", ErrInvalidInput)
	}
	kubeconfig, err := s.vault.Resolve(ctx, *environment.CredentialID)
	if err != nil {
		return InstallResult{}, err
	}
	defer clearBytes(kubeconfig)
	location, err := s.cr.BackupStorageLocationStatus(ctx, kubeconfig, Namespace, BackupLocationName)
	if err != nil {
		return InstallResult{}, err
	}
	return InstallResult{Installation: installation, Location: location}, nil
}

func (s *Service) waitForLocation(ctx context.Context, kubeconfig []byte, current veleroadapter.BackupStorageLocationStatus) (veleroadapter.BackupStorageLocationStatus, error) {
	if current.Phase == "Available" {
		return current, nil
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return current, fmt.Errorf("wait for BackupStorageLocation: %w", ctx.Err())
		case <-ticker.C:
			value, err := s.cr.BackupStorageLocationStatus(ctx, kubeconfig, Namespace, BackupLocationName)
			if err != nil {
				return current, err
			}
			current = value
			if value.Phase == "Available" {
				return value, nil
			}
			if value.Phase == "Unavailable" {
				return value, errors.New("BackupStorageLocation is unavailable")
			}
		}
	}
}

func validateImages(images Images) error {
	for _, value := range []string{images.Velero, images.AWS} {
		parts := strings.Split(value, "@")
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || !digestPattern.MatchString(parts[1]) {
			return errors.New("Velero and AWS plugin images must be pinned by sha256 digest")
		}
	}
	return nil
}

func validateInput(input InstallInput) error {
	if input.EnvironmentID == uuid.Nil || input.ObjectStorageProfile == uuid.Nil {
		return errors.New("environmentId and objectStorageProfileId are required")
	}
	if strings.Contains(input.Prefix, "..") || strings.ContainsAny(input.Prefix, "\\\n\r") {
		return errors.New("object storage prefix is invalid")
	}
	if !strings.HasPrefix(input.KubeletRoot, "/") || strings.Contains(input.KubeletRoot, "..") || strings.ContainsAny(input.KubeletRoot, "\n\r") {
		return errors.New("kubeletRoot must be an absolute path without parent traversal")
	}
	return nil
}

func validateEnvironment(value domainenvironment.Environment) error {
	if value.Kind != domainenvironment.KindKubernetes || value.Status != domainenvironment.StatusConnected {
		return errors.New("Velero requires a connected Kubernetes environment")
	}
	return nil
}

func cloudCredential(value objectstorage.Credential) []byte {
	result := make([]byte, 0, len(value.AccessKey)+len(value.SecretKey)+64)
	result = append(result, "[default]\naws_access_key_id="...)
	result = append(result, value.AccessKey...)
	result = append(result, "\naws_secret_access_key="...)
	result = append(result, value.SecretKey...)
	result = append(result, '\n')
	return result
}

func splitImage(value string) (string, string) {
	parts := strings.SplitN(value, "@", 2)
	return parts[0], parts[1]
}

func existingVeleroVersion(image string) string {
	match := regexp.MustCompile(`(?i)(?:^|[:/])v?(\d+\.\d+\.\d+)(?:@|$)`).FindStringSubmatch(image)
	if len(match) == 2 {
		return match[1]
	}
	return "existing"
}

func backupLocationAccessMode(role domainenvironment.Role) string {
	if role == domainenvironment.RoleTarget {
		return "ReadOnly"
	}
	return "ReadWrite"
}

func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
