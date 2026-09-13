package objectstorage

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/adapter/s3"
	"github.com/smartx/sks-migration-center/internal/addon"
	domaincredential "github.com/smartx/sks-migration-center/internal/domain/credential"
	domainenvironment "github.com/smartx/sks-migration-center/internal/domain/environment"
	"github.com/smartx/sks-migration-center/internal/domain/platform"
	"github.com/smartx/sks-migration-center/internal/repository"
	"k8s.io/apimachinery/pkg/api/resource"
)

const managedNamespace = "sks-migration-system"
const managedRelease = "sks-migration-minio"
const managedStatefulSet = "sks-migration-minio"
const managedCredentialSecret = "sks-migration-minio-root"
const minioChartVersion = "0.1.0"

var ErrInvalidInput = errors.New("invalid object storage input")
var ErrConflict = errors.New("managed object storage already exists")
var digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var bucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
var dnsNamePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

type BootstrapInput struct {
	EnvironmentID uuid.UUID `json:"environmentId"`
	Name          string    `json:"name"`
	Endpoint      string    `json:"endpoint"`
	Bucket        string    `json:"bucket"`
	Region        string    `json:"region"`
	StorageClass  string    `json:"storageClass"`
	StorageSize   string    `json:"storageSize"`
	ImageRepo     string    `json:"imageRepository"`
	ImageDigest   string    `json:"imageDigest"`
	TLSSecretName string    `json:"tlsSecretName"`
	CABundle      string    `json:"caBundle,omitempty"`
}

type BootstrapResult struct {
	Installation platform.AddonInstallation    `json:"installation"`
	Profile      platform.ObjectStorageProfile `json:"profile"`
	Checks       []Check                       `json:"checks"`
}

type AdoptInput struct {
	EnvironmentID uuid.UUID `json:"environmentId"`
	Name          string    `json:"name"`
	Endpoint      string    `json:"endpoint"`
	Bucket        string    `json:"bucket"`
	Region        string    `json:"region"`
	TLSSecretName string    `json:"tlsSecretName"`
	CABundle      string    `json:"caBundle,omitempty"`
}

type ExternalInput struct {
	Name      string `json:"name"`
	Endpoint  string `json:"endpoint"`
	Bucket    string `json:"bucket"`
	Region    string `json:"region"`
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"`
	CABundle  string `json:"caBundle,omitempty"`
}

type ProfileResult struct {
	Profile platform.ObjectStorageProfile `json:"profile"`
	Checks  []Check                       `json:"checks"`
}

type Check struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type Credential struct {
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"`
	CABundle  string `json:"caBundle,omitempty"`
}

type Vault interface {
	Store(context.Context, string, domaincredential.Type, []byte) (domaincredential.Metadata, error)
	Resolve(context.Context, uuid.UUID) ([]byte, error)
	Delete(context.Context, uuid.UUID) error
}

type AddonManager interface {
	InstallOrUpgrade(context.Context, []byte, addon.InstallRequest) (addon.ReleaseState, error)
}

type ClusterOperator interface {
	EnsureNamespace(context.Context, []byte, string) error
	PutOpaqueSecret(context.Context, []byte, string, string, map[string][]byte) error
	GetOpaqueSecret(context.Context, []byte, string, string) (map[string][]byte, error)
	RestartStatefulSet(context.Context, []byte, string, string) error
}

type S3Client interface {
	WriteProbe(context.Context, s3.Config, string) (s3.Probe, error)
	VerifyProbe(context.Context, s3.Config, s3.Probe, bool) error
}

type Service struct {
	platform     repository.PlatformRepository
	environments repository.EnvironmentRepository
	vault        Vault
	manager      AddonManager
	cluster      ClusterOperator
	s3           S3Client
	policy       SourcePolicy
	runtimeImage OfficialImage
	clock        func() time.Time
}

func NewService(platformRepository repository.PlatformRepository, environmentRepository repository.EnvironmentRepository, vault Vault, manager AddonManager, cluster ClusterOperator, s3Client S3Client, policy SourcePolicy) (*Service, error) {
	if platformRepository == nil || environmentRepository == nil || vault == nil || manager == nil || cluster == nil || s3Client == nil {
		return nil, errors.New("object storage repositories, vault, add-on manager, cluster operator and S3 client are required")
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	return &Service{platform: platformRepository, environments: environmentRepository, vault: vault, manager: manager, cluster: cluster, s3: s3Client, policy: policy, runtimeImage: policy.OfficialImage, clock: func() time.Time { return time.Now().UTC() }}, nil
}

// ConfigureRuntimeImage keeps the audited upstream source policy while
// exposing the AMD64 image digest and Harbor repository injected by the
// offline installer.
func (s *Service) ConfigureRuntimeImage(value string) error {
	at := strings.LastIndex(value, "@")
	if at <= 0 || !digestPattern.MatchString(value[at+1:]) || strings.Contains(value[:at], "://") {
		return errors.New("MinIO runtime image must be repository@sha256:digest")
	}
	s.runtimeImage = OfficialImage{Repository: value[:at], Tag: s.policy.OfficialImage.Tag, Digest: value[at+1:]}
	return nil
}

func (s *Service) Bootstrap(ctx context.Context, input BootstrapInput) (BootstrapResult, error) {
	// The release gate is deliberately checked before credentials are resolved or cluster state is changed.
	if err := s.policy.RequireAllowed(); err != nil {
		return BootstrapResult{}, err
	}
	input = defaults(input)
	if err := validateBootstrap(input); err != nil {
		return BootstrapResult{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if input.ImageDigest != s.runtimeImage.Digest {
		return BootstrapResult{}, fmt.Errorf("%w: MinIO image digest does not match the accepted official image", ErrInvalidInput)
	}
	environment, err := s.environments.Get(ctx, input.EnvironmentID)
	if err != nil {
		return BootstrapResult{}, err
	}
	if err := validateTarget(environment, input.StorageClass); err != nil {
		return BootstrapResult{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	existing, err := s.platform.GetAddonInstallation(ctx, input.EnvironmentID, platform.AddonMinIO)
	if err == nil && existing.Status != platform.InstallationFailed && existing.Status != platform.InstallationRemoved {
		return BootstrapResult{}, ErrConflict
	} else if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return BootstrapResult{}, err
	}

	now := s.clock()
	profileID := uuid.New()
	installation := platform.AddonInstallation{
		ID: uuid.New(), EnvironmentID: input.EnvironmentID, Type: platform.AddonMinIO, Version: s.policy.Ref,
		Status: platform.InstallationInstalling, CreatedAt: now, UpdatedAt: now,
		Values: map[string]any{
			"profileId": profileID.String(), "storageClass": input.StorageClass, "storageSize": input.StorageSize,
			"imageRepository": input.ImageRepo, "imageDigest": input.ImageDigest, "sourceCommit": s.policy.Commit,
		},
	}
	if err == nil {
		installation.ID = existing.ID
		installation.CreatedAt = existing.CreatedAt
	}
	if err := s.platform.UpsertAddonInstallation(ctx, installation); err != nil {
		return BootstrapResult{}, err
	}
	fail := func(stage string, cause error) (BootstrapResult, error) {
		installation.Status, installation.Message, installation.UpdatedAt = platform.InstallationFailed, "MinIO 引导失败："+stage, s.clock()
		_ = s.platform.UpsertAddonInstallation(context.WithoutCancel(ctx), installation)
		return BootstrapResult{}, fmt.Errorf("MinIO bootstrap %s: %w", stage, cause)
	}

	if environment.CredentialID == nil {
		return fail("目标集群凭证缺失", errors.New("target kubeconfig is unavailable"))
	}
	kubeconfig, err := s.vault.Resolve(ctx, *environment.CredentialID)
	if err != nil {
		return fail("读取目标集群凭证", err)
	}
	defer clearBytes(kubeconfig)
	if input.CABundle == "" {
		input.CABundle = s.caBundleFromSecret(ctx, kubeconfig, input.TLSSecretName)
	}
	credential, err := generateCredential(input.CABundle)
	if err != nil {
		return fail("生成对象存储凭证", err)
	}
	credentialJSON, err := json.Marshal(credential)
	if err != nil {
		return fail("编码对象存储凭证", err)
	}
	metadata, err := s.vault.Store(ctx, input.Name+" S3", domaincredential.TypeS3, credentialJSON)
	clearBytes(credentialJSON)
	if err != nil {
		return fail("保存对象存储凭证", err)
	}
	if err := s.cluster.EnsureNamespace(ctx, kubeconfig, managedNamespace); err != nil {
		return fail("创建托管命名空间", err)
	}
	if err := s.cluster.PutOpaqueSecret(ctx, kubeconfig, managedNamespace, managedCredentialSecret, map[string][]byte{
		"access-key": []byte(credential.AccessKey), "secret-key": []byte(credential.SecretKey),
	}); err != nil {
		return fail("创建 MinIO Secret", err)
	}
	values := map[string]any{
		"image":             map[string]any{"repository": input.ImageRepo, "digest": input.ImageDigest},
		"credentialsSecret": managedCredentialSecret,
		"tls":               map[string]any{"enabled": true, "existingSecret": input.TLSSecretName},
		"persistence":       map[string]any{"storageClass": input.StorageClass, "size": input.StorageSize},
	}
	helmKubeconfig := append([]byte(nil), kubeconfig...)
	if _, err := s.manager.InstallOrUpgrade(ctx, helmKubeconfig, addon.InstallRequest{
		ReleaseName: managedRelease, Namespace: managedNamespace, ChartPath: "minio-snsd", Version: minioChartVersion,
		Values: values, CreateNamespace: true, Timeout: 30 * time.Minute,
	}); err != nil {
		return fail("安装 StatefulSet", err)
	}
	s3Config := s3.Config{Endpoint: input.Endpoint, Region: input.Region, AccessKey: credential.AccessKey, SecretKey: credential.SecretKey, CABundle: []byte(credential.CABundle), TLSVerify: true}
	probe, err := s.s3.WriteProbe(ctx, s3Config, input.Bucket)
	if err != nil {
		return fail("S3 写入探测", err)
	}
	if err := s.s3.VerifyProbe(ctx, s3Config, probe, false); err != nil {
		return fail("S3 读取校验", err)
	}
	if err := s.cluster.RestartStatefulSet(ctx, kubeconfig, managedNamespace, managedStatefulSet); err != nil {
		return fail("重启持久化检查", err)
	}
	if err := s.s3.VerifyProbe(ctx, s3Config, probe, true); err != nil {
		return fail("重启后数据校验", err)
	}
	profile := platform.ObjectStorageProfile{
		ID: profileID, Name: input.Name, Endpoint: input.Endpoint, Bucket: input.Bucket, Region: input.Region,
		CredentialID: metadata.ID, TLSVerify: true, CreatedAt: now, UpdatedAt: s.clock(),
	}
	if err := s.platform.CreateObjectStorageProfile(ctx, profile); err != nil {
		return fail("保存对象存储配置", err)
	}
	installation.Status, installation.Message, installation.UpdatedAt = platform.InstallationReady, "S3 读写及 MinIO 重启持久化校验通过", s.clock()
	if err := s.platform.UpsertAddonInstallation(ctx, installation); err != nil {
		return BootstrapResult{}, err
	}
	return BootstrapResult{Installation: installation, Profile: profile, Checks: []Check{
		{Name: "S3 Read/Write", Status: "PASSED", Message: "Bucket 创建、对象写入、读取和 SHA-256 校验通过"},
		{Name: "Restart Persistence", Status: "PASSED", Message: "StatefulSet 重启后探测对象保持一致"},
	}}, nil
}

// Adopt registers the fixed managed MinIO release already present in the
// target cluster. It deliberately performs no Helm mutation and never replaces
// the existing credential or TLS Secrets, StatefulSet, Service, or PVC.
func (s *Service) Adopt(ctx context.Context, input AdoptInput) (BootstrapResult, error) {
	input = defaultsAdopt(input)
	if err := validateAdopt(input); err != nil {
		return BootstrapResult{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	environment, err := s.environments.Get(ctx, input.EnvironmentID)
	if err != nil {
		return BootstrapResult{}, err
	}
	if err := validateTargetEnvironment(environment); err != nil {
		return BootstrapResult{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	existing, err := s.platform.GetAddonInstallation(ctx, input.EnvironmentID, platform.AddonMinIO)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return BootstrapResult{}, err
	}
	if err == nil && (existing.Status == platform.InstallationPending || existing.Status == platform.InstallationInstalling) {
		return BootstrapResult{}, ErrConflict
	}
	if err == nil {
		profileID := profileIDFromInstallation(existing)
		if current, getErr := s.platform.GetObjectStorageProfile(ctx, profileID); getErr == nil {
			checks, testErr := s.Test(ctx, current.ID)
			if testErr != nil {
				return BootstrapResult{}, testErr
			}
			if existing.Status != platform.InstallationReady {
				existing.Status, existing.Message, existing.UpdatedAt = platform.InstallationReady, "已重新验证现有 MinIO 对象存储配置", s.clock()
				if upsertErr := s.platform.UpsertAddonInstallation(ctx, existing); upsertErr != nil {
					return BootstrapResult{}, upsertErr
				}
			}
			return BootstrapResult{Installation: existing, Profile: current, Checks: checks}, nil
		} else if !errors.Is(getErr, repository.ErrNotFound) {
			return BootstrapResult{}, getErr
		}
	}
	if environment.CredentialID == nil {
		return BootstrapResult{}, fmt.Errorf("%w: target kubeconfig is unavailable", ErrInvalidInput)
	}
	kubeconfig, err := s.vault.Resolve(ctx, *environment.CredentialID)
	if err != nil {
		return BootstrapResult{}, err
	}
	defer clearBytes(kubeconfig)
	secret, err := s.cluster.GetOpaqueSecret(ctx, kubeconfig, managedNamespace, managedCredentialSecret)
	if err != nil {
		return BootstrapResult{}, fmt.Errorf("adopt MinIO credential Secret: %w", err)
	}
	defer clearSecretData(secret)
	accessKey, secretKey := strings.TrimSpace(string(secret["access-key"])), strings.TrimSpace(string(secret["secret-key"]))
	if accessKey == "" || secretKey == "" {
		return BootstrapResult{}, fmt.Errorf("%w: managed MinIO credential Secret is invalid", ErrInvalidInput)
	}
	if input.CABundle == "" {
		input.CABundle = s.caBundleFromSecret(ctx, kubeconfig, input.TLSSecretName)
	}
	credential := Credential{AccessKey: accessKey, SecretKey: secretKey, CABundle: input.CABundle}
	credentialJSON, err := json.Marshal(credential)
	if err != nil {
		return BootstrapResult{}, err
	}
	metadata, err := s.vault.Store(ctx, input.Name+" S3", domaincredential.TypeS3, credentialJSON)
	clearBytes(credentialJSON)
	if err != nil {
		return BootstrapResult{}, err
	}
	cleanupCredential := true
	defer func() {
		if cleanupCredential {
			_ = s.vault.Delete(context.WithoutCancel(ctx), metadata.ID)
		}
	}()
	s3Config := s3.Config{Endpoint: input.Endpoint, Region: input.Region, AccessKey: accessKey, SecretKey: secretKey, CABundle: []byte(input.CABundle), TLSVerify: true}
	probe, err := s.s3.WriteProbe(ctx, s3Config, input.Bucket)
	restarted := false
	if err != nil {
		// A previous failed bootstrap may have updated the credential Secret
		// before an immutable StatefulSet upgrade was rejected. Restart once so
		// the existing Pod reloads that Secret; the PVC is retained throughout.
		if restartErr := s.cluster.RestartStatefulSet(ctx, kubeconfig, managedNamespace, managedStatefulSet); restartErr != nil {
			return BootstrapResult{}, fmt.Errorf("adopt MinIO S3 write probe: %w", err)
		}
		restarted = true
		probe, err = s.s3.WriteProbe(ctx, s3Config, input.Bucket)
		if err != nil {
			return BootstrapResult{}, fmt.Errorf("adopt MinIO S3 write probe after credential reload: %w", err)
		}
	}
	if err := s.s3.VerifyProbe(ctx, s3Config, probe, true); err != nil {
		return BootstrapResult{}, fmt.Errorf("adopt MinIO S3 read probe: %w", err)
	}
	now := s.clock()
	profileID := profileIDFromInstallation(existing)
	profile := platform.ObjectStorageProfile{
		ID: profileID, Name: input.Name, Endpoint: input.Endpoint, Bucket: input.Bucket, Region: input.Region,
		CredentialID: metadata.ID, TLSVerify: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.platform.CreateObjectStorageProfile(ctx, profile); err != nil {
		return BootstrapResult{}, err
	}
	installation := existing
	if installation.ID == uuid.Nil {
		installation.ID, installation.CreatedAt = uuid.New(), now
	}
	installation.EnvironmentID, installation.Type, installation.Version = input.EnvironmentID, platform.AddonMinIO, s.policy.Ref
	installation.Status, installation.Message, installation.UpdatedAt = platform.InstallationReady, "已接管现有 MinIO；S3 读写校验通过，未重建工作负载和 PVC", now
	installation.Values = cloneValues(existing.Values)
	installation.Values["profileId"] = profile.ID.String()
	installation.Values["mode"] = "ADOPTED"
	installation.Values["managed"] = true
	installation.Values["namespace"] = managedNamespace
	installation.Values["releaseName"] = managedRelease
	if err := s.platform.UpsertAddonInstallation(ctx, installation); err != nil {
		return BootstrapResult{}, err
	}
	cleanupCredential = false
	message := "现有实例对象写入、读取和 SHA-256 校验通过；未重建 StatefulSet 或 PVC"
	if restarted {
		message = "重新加载现有凭据后对象读写校验通过；PVC 和数据保持不变"
	}
	return BootstrapResult{Installation: installation, Profile: profile, Checks: []Check{{Name: "Existing MinIO S3 Read/Write", Status: "PASSED", Message: message}}}, nil
}

func (s *Service) ConnectExternal(ctx context.Context, input ExternalInput) (ProfileResult, error) {
	input.Name, input.Endpoint, input.Bucket, input.Region = strings.TrimSpace(input.Name), strings.TrimSpace(input.Endpoint), strings.TrimSpace(input.Bucket), strings.TrimSpace(input.Region)
	input.AccessKey, input.SecretKey, input.CABundle = strings.TrimSpace(input.AccessKey), strings.TrimSpace(input.SecretKey), strings.TrimSpace(input.CABundle)
	if input.Region == "" {
		input.Region = "us-east-1"
	}
	profile := platform.ObjectStorageProfile{
		ID: uuid.New(), Name: input.Name, Endpoint: input.Endpoint, Bucket: input.Bucket, Region: input.Region,
		CredentialID: uuid.New(), TLSVerify: true, CreatedAt: s.clock(), UpdatedAt: s.clock(),
	}
	if input.AccessKey == "" || input.SecretKey == "" {
		return ProfileResult{}, fmt.Errorf("%w: S3 access key and secret key are required", ErrInvalidInput)
	}
	if err := profile.Validate(); err != nil || !bucketPattern.MatchString(input.Bucket) || strings.Contains(input.Bucket, "..") {
		return ProfileResult{}, fmt.Errorf("%w: external S3 name, HTTPS endpoint and bucket are invalid", ErrInvalidInput)
	}
	credentialJSON, err := json.Marshal(Credential{AccessKey: input.AccessKey, SecretKey: input.SecretKey, CABundle: input.CABundle})
	if err != nil {
		return ProfileResult{}, err
	}
	metadata, err := s.vault.Store(ctx, input.Name+" S3", domaincredential.TypeS3, credentialJSON)
	clearBytes(credentialJSON)
	if err != nil {
		return ProfileResult{}, err
	}
	cleanupCredential := true
	defer func() {
		if cleanupCredential {
			_ = s.vault.Delete(context.WithoutCancel(ctx), metadata.ID)
		}
	}()
	profile.CredentialID = metadata.ID
	config := s3.Config{Endpoint: input.Endpoint, Region: input.Region, AccessKey: input.AccessKey, SecretKey: input.SecretKey, CABundle: []byte(input.CABundle), TLSVerify: true}
	probe, err := s.s3.WriteProbe(ctx, config, input.Bucket)
	if err != nil {
		return ProfileResult{}, fmt.Errorf("external S3 write probe: %w", err)
	}
	if err := s.s3.VerifyProbe(ctx, config, probe, true); err != nil {
		return ProfileResult{}, fmt.Errorf("external S3 read probe: %w", err)
	}
	if err := s.platform.CreateObjectStorageProfile(ctx, profile); err != nil {
		return ProfileResult{}, err
	}
	cleanupCredential = false
	return ProfileResult{Profile: profile, Checks: []Check{{Name: "External S3 Read/Write", Status: "PASSED", Message: "外部 S3 对象写入、读取和 SHA-256 校验通过"}}}, nil
}

func (s *Service) Test(ctx context.Context, profileID uuid.UUID) ([]Check, error) {
	profile, err := s.platform.GetObjectStorageProfile(ctx, profileID)
	if err != nil {
		return nil, err
	}
	encoded, err := s.vault.Resolve(ctx, profile.CredentialID)
	if err != nil {
		return nil, err
	}
	defer clearBytes(encoded)
	var credential Credential
	if err := json.Unmarshal(encoded, &credential); err != nil {
		return nil, errors.New("stored S3 credential is invalid")
	}
	config := s3.Config{Endpoint: profile.Endpoint, Region: profile.Region, AccessKey: credential.AccessKey, SecretKey: credential.SecretKey, CABundle: []byte(credential.CABundle), TLSVerify: profile.TLSVerify}
	probe, err := s.s3.WriteProbe(ctx, config, profile.Bucket)
	if err != nil {
		return nil, err
	}
	if err := s.s3.VerifyProbe(ctx, config, probe, true); err != nil {
		return nil, err
	}
	return []Check{{Name: "S3 Read/Write", Status: "PASSED", Message: "对象写入、读取和 SHA-256 校验通过"}}, nil
}

func (s *Service) Addons(ctx context.Context, environmentID uuid.UUID) ([]platform.AddonInstallation, error) {
	if _, err := s.environments.Get(ctx, environmentID); err != nil {
		return nil, err
	}
	return s.platform.ListAddonInstallations(ctx, environmentID)
}

func (s *Service) Profiles(ctx context.Context) ([]platform.ObjectStorageProfile, error) {
	return s.platform.ListObjectStorageProfiles(ctx)
}

func (s *Service) Policy() SourcePolicy {
	result := s.policy
	result.OfficialImage = s.runtimeImage
	return result
}

func defaults(input BootstrapInput) BootstrapInput {
	input.Name, input.Endpoint, input.Bucket, input.Region = strings.TrimSpace(input.Name), strings.TrimSpace(input.Endpoint), strings.TrimSpace(input.Bucket), strings.TrimSpace(input.Region)
	input.StorageClass, input.StorageSize = strings.TrimSpace(input.StorageClass), strings.TrimSpace(input.StorageSize)
	input.ImageRepo, input.ImageDigest, input.TLSSecretName = strings.TrimSpace(input.ImageRepo), strings.TrimSpace(input.ImageDigest), strings.TrimSpace(input.TLSSecretName)
	if input.Name == "" {
		input.Name = "managed-minio"
	}
	if input.Bucket == "" {
		input.Bucket = "velero"
	}
	if input.Region == "" {
		input.Region = "minio"
	}
	if input.StorageSize == "" {
		input.StorageSize = "100Gi"
	}
	return input
}

func defaultsAdopt(input AdoptInput) AdoptInput {
	input.Name, input.Endpoint, input.Bucket, input.Region = strings.TrimSpace(input.Name), strings.TrimSpace(input.Endpoint), strings.TrimSpace(input.Bucket), strings.TrimSpace(input.Region)
	input.TLSSecretName, input.CABundle = strings.TrimSpace(input.TLSSecretName), strings.TrimSpace(input.CABundle)
	if input.Name == "" {
		input.Name = "managed-minio"
	}
	if input.Bucket == "" {
		input.Bucket = "velero"
	}
	if input.Region == "" {
		input.Region = "minio"
	}
	if input.TLSSecretName == "" {
		input.TLSSecretName = "sks-migration-minio-tls"
	}
	return input
}

func validateAdopt(input AdoptInput) error {
	if input.EnvironmentID == uuid.Nil || input.Endpoint == "" || !bucketPattern.MatchString(input.Bucket) || strings.Contains(input.Bucket, "..") || !dnsNamePattern.MatchString(input.TLSSecretName) {
		return errors.New("environmentId, HTTPS endpoint, valid bucket and TLS Secret are required")
	}
	return (platform.ObjectStorageProfile{ID: uuid.New(), Name: input.Name, Endpoint: input.Endpoint, Bucket: input.Bucket, Region: input.Region, CredentialID: uuid.New(), TLSVerify: true}).Validate()
}

func validateBootstrap(input BootstrapInput) error {
	if input.EnvironmentID == uuid.Nil || input.Endpoint == "" || input.StorageClass == "" || input.ImageRepo == "" || input.TLSSecretName == "" {
		return errors.New("environmentId, endpoint, StorageClass, image repository and TLS Secret are required")
	}
	if !digestPattern.MatchString(input.ImageDigest) || strings.Contains(input.ImageRepo, "@") || strings.Contains(input.ImageRepo, "://") {
		return errors.New("MinIO image repository and sha256 digest are invalid")
	}
	if !bucketPattern.MatchString(input.Bucket) || strings.Contains(input.Bucket, "..") || !dnsNamePattern.MatchString(input.TLSSecretName) {
		return errors.New("bucket or TLS Secret name is invalid")
	}
	quantity, err := resource.ParseQuantity(input.StorageSize)
	if err != nil || quantity.Sign() <= 0 {
		return errors.New("storageSize must be a positive Kubernetes quantity")
	}
	profile := platform.ObjectStorageProfile{ID: uuid.New(), Name: input.Name, Endpoint: input.Endpoint, Bucket: input.Bucket, Region: input.Region, CredentialID: uuid.New(), TLSVerify: true}
	return profile.Validate()
}

func validateTarget(value domainenvironment.Environment, storageClass string) error {
	if err := validateTargetEnvironment(value); err != nil {
		return err
	}
	for _, item := range value.Capabilities.StorageClasses {
		if item.Name == storageClass {
			if item.Provisioner != "com.smartx.elf-csi-driver" && item.Provisioner != "smtx-elf-csi-driver" {
				return errors.New("selected StorageClass is not provided by the SmartX ELF CSI driver")
			}
			return nil
		}
	}
	return errors.New("selected StorageClass was not discovered on the target cluster")
}

func validateTargetEnvironment(value domainenvironment.Environment) error {
	if value.Role != domainenvironment.RoleTarget || value.Kind != domainenvironment.KindKubernetes || value.Status != domainenvironment.StatusConnected {
		return errors.New("MinIO requires a connected target Kubernetes workload cluster")
	}
	return nil
}

func (s *Service) caBundleFromSecret(ctx context.Context, kubeconfig []byte, name string) string {
	secret, err := s.cluster.GetOpaqueSecret(ctx, kubeconfig, managedNamespace, name)
	if err != nil {
		return ""
	}
	defer clearSecretData(secret)
	if value := strings.TrimSpace(string(secret["ca.crt"])); value != "" {
		return value
	}
	if value := strings.TrimSpace(string(secret["public.crt"])); value != "" {
		return value
	}
	return strings.TrimSpace(string(secret["tls.crt"]))
}

func profileIDFromInstallation(value platform.AddonInstallation) uuid.UUID {
	if raw, ok := value.Values["profileId"].(string); ok {
		if parsed, err := uuid.Parse(raw); err == nil {
			return parsed
		}
	}
	return uuid.New()
}

func cloneValues(value map[string]any) map[string]any {
	result := make(map[string]any, len(value)+5)
	for key, item := range value {
		result[key] = item
	}
	return result
}

func clearSecretData(value map[string][]byte) {
	for key := range value {
		clearBytes(value[key])
	}
}

func generateCredential(caBundle string) (Credential, error) {
	accessBytes, secretBytes := make([]byte, 15), make([]byte, 48)
	if _, err := rand.Read(accessBytes); err != nil {
		return Credential{}, err
	}
	if _, err := rand.Read(secretBytes); err != nil {
		clearBytes(accessBytes)
		return Credential{}, err
	}
	access := strings.TrimRight(base32.StdEncoding.EncodeToString(accessBytes), "=")
	secret := base64.RawURLEncoding.EncodeToString(secretBytes)
	clearBytes(accessBytes)
	clearBytes(secretBytes)
	return Credential{AccessKey: access, SecretKey: secret, CABundle: strings.TrimSpace(caBundle)}, nil
}

func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
