package objectstorage

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/adapter/s3"
	"github.com/smartx/sks-migration-center/internal/addon"
	domaincredential "github.com/smartx/sks-migration-center/internal/domain/credential"
	domainenvironment "github.com/smartx/sks-migration-center/internal/domain/environment"
	"github.com/smartx/sks-migration-center/internal/domain/platform"
	"github.com/smartx/sks-migration-center/internal/repository"
)

type platformStoreStub struct {
	addon       *platform.AddonInstallation
	profile     *platform.ObjectStorageProfile
	upsertCount int
}

func (s *platformStoreStub) CreateObjectStorageProfile(_ context.Context, value platform.ObjectStorageProfile) error {
	s.profile = &value
	return nil
}
func (s *platformStoreStub) GetObjectStorageProfile(_ context.Context, id uuid.UUID) (platform.ObjectStorageProfile, error) {
	if s.profile == nil || s.profile.ID != id {
		return platform.ObjectStorageProfile{}, repository.ErrNotFound
	}
	return *s.profile, nil
}
func (s *platformStoreStub) ListObjectStorageProfiles(context.Context) ([]platform.ObjectStorageProfile, error) {
	return nil, nil
}
func (s *platformStoreStub) UpsertAddonInstallation(_ context.Context, value platform.AddonInstallation) error {
	copy := value
	s.addon = &copy
	s.upsertCount++
	return nil
}
func (s *platformStoreStub) GetAddonInstallation(_ context.Context, environmentID uuid.UUID, addonType platform.AddonType) (platform.AddonInstallation, error) {
	if s.addon == nil || s.addon.EnvironmentID != environmentID || s.addon.Type != addonType {
		return platform.AddonInstallation{}, repository.ErrNotFound
	}
	return *s.addon, nil
}
func (s *platformStoreStub) ListAddonInstallations(_ context.Context, environmentID uuid.UUID) ([]platform.AddonInstallation, error) {
	if s.addon != nil && s.addon.EnvironmentID == environmentID {
		return []platform.AddonInstallation{*s.addon}, nil
	}
	return []platform.AddonInstallation{}, nil
}

type environmentStoreStub struct{ value domainenvironment.Environment }

func (s *environmentStoreStub) Create(context.Context, domainenvironment.Environment) error {
	return nil
}
func (s *environmentStoreStub) Get(_ context.Context, id uuid.UUID) (domainenvironment.Environment, error) {
	if s.value.ID != id {
		return domainenvironment.Environment{}, repository.ErrNotFound
	}
	return s.value, nil
}
func (s *environmentStoreStub) List(context.Context) ([]domainenvironment.Environment, error) {
	return nil, nil
}
func (s *environmentStoreStub) Update(context.Context, domainenvironment.Environment) error {
	return nil
}
func (s *environmentStoreStub) Delete(context.Context, uuid.UUID) error { return nil }

type vaultStub struct {
	kubeconfigID uuid.UUID
	stored       []byte
	metadataID   uuid.UUID
}

func (v *vaultStub) Store(_ context.Context, _ string, kind domaincredential.Type, value []byte) (domaincredential.Metadata, error) {
	if kind != domaincredential.TypeS3 || strings.Contains(string(value), "kubeconfig") {
		return domaincredential.Metadata{}, errors.New("unexpected credential")
	}
	v.stored = append([]byte(nil), value...)
	return domaincredential.Metadata{ID: v.metadataID, Type: kind}, nil
}
func (v *vaultStub) Resolve(_ context.Context, id uuid.UUID) ([]byte, error) {
	if id != v.kubeconfigID {
		return nil, repository.ErrNotFound
	}
	return []byte("kubeconfig"), nil
}
func (v *vaultStub) Delete(context.Context, uuid.UUID) error { return nil }

type managerStub struct {
	called bool
	values map[string]any
}

func (m *managerStub) InstallOrUpgrade(_ context.Context, kubeconfig []byte, request addon.InstallRequest) (addon.ReleaseState, error) {
	m.called, m.values = true, request.Values
	if string(kubeconfig) != "kubeconfig" || request.ChartPath != "minio-snsd" || request.Version != minioChartVersion {
		return addon.ReleaseState{}, errors.New("unexpected Helm request")
	}
	for index := range kubeconfig {
		kubeconfig[index] = 0
	}
	return addon.ReleaseState{Status: "deployed"}, nil
}

type clusterStub struct {
	namespaceEnsured bool
	secretWritten    bool
	secretRead       bool
	restarted        bool
}

func (c *clusterStub) EnsureNamespace(_ context.Context, kubeconfig []byte, namespace string) error {
	if string(kubeconfig) != "kubeconfig" || namespace != managedNamespace {
		return errors.New("unexpected namespace request")
	}
	c.namespaceEnsured = true
	return nil
}

func (c *clusterStub) PutOpaqueSecret(_ context.Context, kubeconfig []byte, namespace, name string, data map[string][]byte) error {
	if string(kubeconfig) != "kubeconfig" || namespace != managedNamespace || name != managedCredentialSecret || len(data["secret-key"]) < 32 {
		return errors.New("unexpected secret request")
	}
	c.secretWritten = true
	return nil
}
func (c *clusterStub) GetOpaqueSecret(_ context.Context, kubeconfig []byte, namespace, name string) (map[string][]byte, error) {
	if string(kubeconfig) != "kubeconfig" || namespace != managedNamespace {
		return nil, errors.New("unexpected secret read")
	}
	c.secretRead = true
	if name == managedCredentialSecret {
		return map[string][]byte{"access-key": []byte("existing-access-key"), "secret-key": []byte(strings.Repeat("s", 48))}, nil
	}
	if name == "minio-tls" {
		return map[string][]byte{"tls.crt": []byte("test-ca")}, nil
	}
	return nil, repository.ErrNotFound
}
func (c *clusterStub) RestartStatefulSet(_ context.Context, kubeconfig []byte, namespace, name string) error {
	if string(kubeconfig) != "kubeconfig" || namespace != managedNamespace || name != managedStatefulSet {
		return errors.New("unexpected restart request")
	}
	c.restarted = true
	return nil
}

type s3Stub struct {
	written  bool
	verified int
	fail     bool
}

func (s *s3Stub) WriteProbe(_ context.Context, config s3.Config, bucket string) (s3.Probe, error) {
	if s.fail {
		return s3.Probe{}, errors.New("S3 unavailable")
	}
	if config.Endpoint != "https://minio.example.test" || !config.TLSVerify || bucket != "velero" || len(config.SecretKey) < 32 {
		return s3.Probe{}, errors.New("unexpected S3 probe")
	}
	s.written = true
	return s3.Probe{Bucket: bucket, Key: "probe", SHA256: strings.Repeat("a", 64), Size: 4096}, nil
}
func (s *s3Stub) VerifyProbe(_ context.Context, _ s3.Config, _ s3.Probe, _ bool) error {
	s.verified++
	return nil
}

func TestBootstrapIsBlockedBeforeAnyClusterOrCredentialMutation(t *testing.T) {
	store, environments, vault, manager, cluster, s3Client, input := bootstrapFixture()
	policy := allowedPolicy()
	policy.ReleaseAllowed = false
	policy.SecurityGate.Status = "BLOCKED"
	policy.SecurityGate.Blockers = []SecurityBlocker{{ID: "GHSA-test", Severity: "HIGH", Reason: "unfixed"}}
	service, err := NewService(store, environments, vault, manager, cluster, s3Client, policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Bootstrap(context.Background(), input); !errors.Is(err, ErrReleaseBlocked) {
		t.Fatalf("expected release blocker, got %v", err)
	}
	if store.upsertCount != 0 || vault.stored != nil || manager.called || cluster.secretWritten || s3Client.written {
		t.Fatal("release blocker allowed an external mutation")
	}
}

func TestBootstrapRejectsImageOutsideAcceptedOfficialDigest(t *testing.T) {
	store, environments, vault, manager, cluster, s3Client, input := bootstrapFixture()
	input.ImageDigest = "sha256:" + strings.Repeat("b", 64)
	service, err := NewService(store, environments, vault, manager, cluster, s3Client, allowedPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Bootstrap(context.Background(), input); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected official image digest rejection, got %v", err)
	}
	if store.upsertCount != 0 || vault.stored != nil || manager.called || cluster.secretWritten || s3Client.written {
		t.Fatal("image digest mismatch allowed an external mutation")
	}
}

func TestRuntimeImageFromOfflineInstallerDrivesPolicyAndBootstrap(t *testing.T) {
	store, environments, vault, manager, cluster, s3Client, input := bootstrapFixture()
	service, err := NewService(store, environments, vault, manager, cluster, s3Client, allowedPolicy())
	if err != nil {
		t.Fatal(err)
	}
	runtimeDigest := "sha256:" + strings.Repeat("b", 64)
	if err := service.ConfigureRuntimeImage("harbor.local/migration/minio@" + runtimeDigest); err != nil {
		t.Fatal(err)
	}
	policy := service.Policy()
	if policy.OfficialImage.Repository != "harbor.local/migration/minio" || policy.OfficialImage.Digest != runtimeDigest {
		t.Fatalf("unexpected runtime image policy: %+v", policy.OfficialImage)
	}
	input.ImageRepo, input.ImageDigest = policy.OfficialImage.Repository, policy.OfficialImage.Digest
	if _, err := service.Bootstrap(context.Background(), input); err != nil {
		t.Fatalf("bootstrap with offline runtime image: %v", err)
	}
}

func TestRuntimeImageRejectsTagsAndURLs(t *testing.T) {
	store, environments, vault, manager, cluster, s3Client, _ := bootstrapFixture()
	service, err := NewService(store, environments, vault, manager, cluster, s3Client, allowedPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"harbor/minio:latest", "https://harbor/minio@sha256:" + strings.Repeat("a", 64)} {
		if err := service.ConfigureRuntimeImage(value); err == nil {
			t.Fatalf("expected invalid runtime image %q to fail", value)
		}
	}
}

func TestBootstrapInstallsProbesRestartsAndPersistsWithoutLeakingSecret(t *testing.T) {
	store, environments, vault, manager, cluster, s3Client, input := bootstrapFixture()
	service, err := NewService(store, environments, vault, manager, cluster, s3Client, allowedPolicy())
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Bootstrap(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Installation.Status != platform.InstallationReady || result.Profile.CredentialID != vault.metadataID || len(result.Checks) != 2 {
		t.Fatalf("unexpected bootstrap result: %#v", result)
	}
	if !manager.called || !cluster.namespaceEnsured || !cluster.secretWritten || !cluster.restarted || !s3Client.written || s3Client.verified != 2 || store.upsertCount != 2 {
		t.Fatalf("bootstrap steps missing: manager=%v cluster=%+v s3=%+v upserts=%d", manager.called, cluster, s3Client, store.upsertCount)
	}
	encoded, _ := json.Marshal(result)
	var credential Credential
	if err := json.Unmarshal(vault.stored, &credential); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), credential.SecretKey) || strings.Contains(string(encoded), credential.AccessKey) {
		t.Fatal("bootstrap response leaked S3 credentials")
	}
	if strings.Contains(mustJSON(t, result.Installation.Values), credential.SecretKey) {
		t.Fatal("add-on values persisted a secret")
	}
}

func TestBootstrapFailurePersistsSanitizedFailedState(t *testing.T) {
	store, environments, vault, manager, cluster, s3Client, input := bootstrapFixture()
	s3Client.fail = true
	service, err := NewService(store, environments, vault, manager, cluster, s3Client, allowedPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Bootstrap(context.Background(), input); err == nil {
		t.Fatal("expected S3 failure")
	}
	if store.addon == nil || store.addon.Status != platform.InstallationFailed || strings.Contains(store.addon.Message, "unavailable") {
		t.Fatalf("failure state was not sanitized: %#v", store.addon)
	}
}

func TestBootstrapRetriesFailedInstallationAndPreservesIdentity(t *testing.T) {
	store, environments, vault, manager, cluster, s3Client, input := bootstrapFixture()
	existingID, createdAt := uuid.New(), time.Unix(10, 0).UTC()
	store.addon = &platform.AddonInstallation{
		ID: existingID, EnvironmentID: input.EnvironmentID, Type: platform.AddonMinIO, Version: "old",
		Status: platform.InstallationFailed, CreatedAt: createdAt, UpdatedAt: createdAt,
	}
	service, err := NewService(store, environments, vault, manager, cluster, s3Client, allowedPolicy())
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Bootstrap(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Installation.ID != existingID || !result.Installation.CreatedAt.Equal(createdAt) || result.Installation.Status != platform.InstallationReady {
		t.Fatalf("failed installation was not retried in place: %+v", result.Installation)
	}
}

func TestBootstrapStillRejectsReadyInstallation(t *testing.T) {
	store, environments, vault, manager, cluster, s3Client, input := bootstrapFixture()
	store.addon = &platform.AddonInstallation{
		ID: uuid.New(), EnvironmentID: input.EnvironmentID, Type: platform.AddonMinIO, Version: "ready",
		Status: platform.InstallationReady, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	service, err := NewService(store, environments, vault, manager, cluster, s3Client, allowedPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Bootstrap(context.Background(), input); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ready installation conflict, got %v", err)
	}
	if manager.called || cluster.secretWritten || vault.stored != nil {
		t.Fatal("ready installation conflict allowed an external mutation")
	}
}

func TestAdoptExistingMinIORepairsFailedRecordWithoutHelmOrPVCMutation(t *testing.T) {
	store, environments, vault, manager, cluster, s3Client, input := bootstrapFixture()
	profileID, installationID := uuid.New(), uuid.New()
	store.addon = &platform.AddonInstallation{
		ID: installationID, EnvironmentID: input.EnvironmentID, Type: platform.AddonMinIO, Version: "old",
		Status: platform.InstallationFailed, Values: map[string]any{"profileId": profileID.String(), "storageClass": "smtx-block", "storageSize": "20Gi"},
		CreatedAt: time.Unix(10, 0).UTC(), UpdatedAt: time.Unix(20, 0).UTC(),
	}
	service, err := NewService(store, environments, vault, manager, cluster, s3Client, allowedPolicy())
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Adopt(context.Background(), AdoptInput{
		EnvironmentID: input.EnvironmentID, Endpoint: input.Endpoint, TLSSecretName: input.TLSSecretName,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Installation.ID != installationID || result.Installation.Status != platform.InstallationReady || result.Profile.ID != profileID {
		t.Fatalf("unexpected adoption result: %+v", result)
	}
	if result.Installation.Values["mode"] != "ADOPTED" || result.Installation.Values["profileId"] != profileID.String() {
		t.Fatalf("adoption metadata missing: %+v", result.Installation.Values)
	}
	if manager.called || cluster.secretWritten || cluster.restarted || !cluster.secretRead {
		t.Fatalf("adoption mutated managed workload: manager=%v cluster=%+v", manager.called, cluster)
	}
	var credential Credential
	if err := json.Unmarshal(vault.stored, &credential); err != nil {
		t.Fatal(err)
	}
	if credential.AccessKey != "existing-access-key" || credential.CABundle != "test-ca" || !s3Client.written || s3Client.verified != 1 {
		t.Fatalf("existing credentials or TLS CA were not used: credential=%+v s3=%+v", credential, s3Client)
	}
}

func TestConnectExternalS3StoresCredentialAndReturnsNoSecret(t *testing.T) {
	store, environments, vault, manager, cluster, s3Client, input := bootstrapFixture()
	service, err := NewService(store, environments, vault, manager, cluster, s3Client, allowedPolicy())
	if err != nil {
		t.Fatal(err)
	}
	secretKey := strings.Repeat("e", 48)
	result, err := service.ConnectExternal(context.Background(), ExternalInput{
		Name: "external-s3", Endpoint: input.Endpoint, Bucket: input.Bucket, Region: "minio",
		AccessKey: "external-access", SecretKey: secretKey, CABundle: "external-ca",
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secretKey) || result.Profile.CredentialID != vault.metadataID || store.profile == nil {
		t.Fatalf("external S3 response leaked or profile was not persisted: %s", encoded)
	}
	if manager.called || cluster.secretWritten || cluster.restarted || s3Client.verified != 1 {
		t.Fatalf("external S3 connection mutated Kubernetes: manager=%v cluster=%+v s3=%+v", manager.called, cluster, s3Client)
	}
}

func bootstrapFixture() (*platformStoreStub, *environmentStoreStub, *vaultStub, *managerStub, *clusterStub, *s3Stub, BootstrapInput) {
	environmentID, kubeconfigID := uuid.New(), uuid.New()
	store := &platformStoreStub{}
	environments := &environmentStoreStub{value: domainenvironment.Environment{
		ID: environmentID, Name: "target", Role: domainenvironment.RoleTarget, Kind: domainenvironment.KindKubernetes,
		Status: domainenvironment.StatusConnected, CredentialID: &kubeconfigID,
		Capabilities: domainenvironment.Capabilities{StorageClasses: []domainenvironment.StorageClass{{Name: "smtx-block", Provisioner: "com.smartx.elf-csi-driver"}}},
	}}
	vault := &vaultStub{kubeconfigID: kubeconfigID, metadataID: uuid.New()}
	return store, environments, vault, &managerStub{}, &clusterStub{}, &s3Stub{}, BootstrapInput{
		EnvironmentID: environmentID, Endpoint: "https://minio.example.test", Bucket: "velero", StorageClass: "smtx-block",
		StorageSize: "100Gi", ImageRepo: "harbor.local/migration/minio", ImageDigest: "sha256:" + strings.Repeat("a", 64), TLSSecretName: "minio-tls",
	}
}

func allowedPolicy() SourcePolicy {
	return SourcePolicy{
		APIVersion: sourceLockAPIVersion, Kind: sourceLockKind, Repository: "https://github.com/minio/minio.git",
		Ref: "SAFE.TEST", Commit: strings.Repeat("a", 40), License: "AGPL-3.0-only", SourceOfferRequired: true, ReleaseAllowed: true,
		OfficialImage: OfficialImage{Repository: "docker.io/minio/minio", Tag: "SAFE.TEST", Digest: "sha256:" + strings.Repeat("a", 64)},
		SecurityGate:  SecurityGate{Status: "PASSED", CheckedAt: time.Unix(1, 0)},
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
