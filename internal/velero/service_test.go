package velero

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
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

const veleroImage = "docker.io/velero/velero@sha256:11459094b1b21ec7c817b08f8067d9e89380835547915cac9c4132ff05b55b90"
const pluginImage = "docker.io/velero/velero-plugin-for-aws@sha256:7e82f717f44e89671212e0dfce7e061321c386ea84a33bca64a671670ca6c278"

func TestInstallUsesPinnedImagesAndWaitsForAvailableBSL(t *testing.T) {
	environmentID, kubeconfigID, profileID, s3ID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	environments := &fakeEnvironmentRepository{value: domainenvironment.Environment{
		ID: environmentID, Name: "sida", Role: domainenvironment.RoleSource, Kind: domainenvironment.KindKubernetes,
		CredentialID: &kubeconfigID, Status: domainenvironment.StatusConnected,
	}}
	platformStore := &fakePlatformRepository{profile: platform.ObjectStorageProfile{
		ID: profileID, Name: "minio", Endpoint: "https://192.0.2.10:30164", Bucket: "velero", Region: "minio",
		CredentialID: s3ID, TLSVerify: true,
	}}
	credentialJSON, _ := json.Marshal(objectstorage.Credential{AccessKey: "access", SecretKey: "secret", CABundle: "test-ca"})
	vault := &fakeVault{values: map[uuid.UUID][]byte{kubeconfigID: []byte("kubeconfig"), s3ID: credentialJSON}}
	manager, cluster, cr := &fakeManager{}, &fakeCluster{}, &fakeCR{phases: []string{"Unknown", "Available"}}
	service, err := NewService(platformStore, environments, vault, manager, cluster, cr, Images{Velero: veleroImage, AWS: pluginImage})
	if err != nil {
		t.Fatal(err)
	}
	service.clock = func() time.Time { return time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC) }
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := service.Install(ctx, InstallInput{EnvironmentID: environmentID, ObjectStorageProfile: profileID, Prefix: "/sida/"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Installation.Status != platform.InstallationReady || result.Location.Phase != "Available" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if manager.request.ChartPath != OfficialChartArtifact || manager.request.Version != OfficialChartVersion || manager.request.ReleaseName != ReleaseName {
		t.Fatalf("unexpected chart request: %#v", manager.request)
	}
	if got := nestedValue(manager.request.Values, "deployNodeAgent"); got != true {
		t.Fatalf("deployNodeAgent = %#v", got)
	}
	if got := nestedValue(manager.request.Values, "configuration", "defaultVolumesToFsBackup"); got != true {
		t.Fatalf("defaultVolumesToFsBackup = %#v", got)
	}
	if got := nestedValue(manager.request.Values, "nodeAgent", "podVolumePath"); got != "/var/lib/kubelet/pods" {
		t.Fatalf("podVolumePath = %#v", got)
	}
	if _, found := manager.request.Values["namespace"]; found {
		t.Fatal("namespace labels must be applied through client-go; chart labels create an offline kubectl hook")
	}
	if cluster.namespace != Namespace || !strings.Contains(string(cluster.secret[CredentialSecretKey]), "aws_access_key_id=access") || !strings.Contains(string(cluster.secret[CredentialSecretKey]), "aws_secret_access_key=secret") {
		t.Fatalf("cluster preparation was incomplete")
	}
	if cr.spec.Prefix != "sida" || cr.spec.Endpoint != platformStore.profile.Endpoint || string(cr.spec.CABundle) != "test-ca" || cr.spec.AccessMode != "ReadWrite" {
		t.Fatalf("unexpected BSL spec: %#v", cr.spec)
	}
	encodedValues, _ := json.Marshal(result.Installation.Values)
	if strings.Contains(string(encodedValues), "access") || strings.Contains(string(encodedValues), "secret") || strings.Contains(string(encodedValues), "test-ca") {
		t.Fatalf("persisted add-on values leaked credentials: %s", encodedValues)
	}
}

func TestInstallMarksFailureWithoutPersistingCredential(t *testing.T) {
	environmentID, credentialID, profileID := uuid.New(), uuid.New(), uuid.New()
	environments := &fakeEnvironmentRepository{value: domainenvironment.Environment{
		ID: environmentID, Name: "target", Role: domainenvironment.RoleTarget, Kind: domainenvironment.KindKubernetes,
		CredentialID: &credentialID, Status: domainenvironment.StatusConnected,
	}}
	platformStore := &fakePlatformRepository{profile: platform.ObjectStorageProfile{ID: profileID, CredentialID: uuid.New()}}
	vault := &fakeVault{values: map[uuid.UUID][]byte{credentialID: []byte("kubeconfig")}}
	service, err := NewService(platformStore, environments, vault, &fakeManager{}, &fakeCluster{}, &fakeCR{}, Images{Velero: veleroImage, AWS: pluginImage})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Install(context.Background(), InstallInput{EnvironmentID: environmentID, ObjectStorageProfile: profileID}); err == nil {
		t.Fatal("expected missing object storage credential to fail")
	}
	if platformStore.installation.Status != platform.InstallationFailed {
		t.Fatalf("installation status = %s", platformStore.installation.Status)
	}
	encoded, _ := json.Marshal(platformStore.installation)
	if strings.Contains(string(encoded), "kubeconfig") {
		t.Fatal("installation record leaked kubeconfig")
	}
}

func TestInstallRejectsUnmanagedExistingVeleroBeforeClusterWrites(t *testing.T) {
	environmentID, kubeconfigID, profileID, s3ID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	environments := &fakeEnvironmentRepository{value: domainenvironment.Environment{
		ID: environmentID, Name: "mw", Role: domainenvironment.RoleTarget, Kind: domainenvironment.KindKubernetes,
		CredentialID: &kubeconfigID, Status: domainenvironment.StatusConnected,
	}}
	platformStore := &fakePlatformRepository{profile: platform.ObjectStorageProfile{
		ID: profileID, Endpoint: "https://192.0.2.10:30164", Bucket: "velero", Region: "minio", CredentialID: s3ID, TLSVerify: true,
	}}
	credentialJSON, _ := json.Marshal(objectstorage.Credential{AccessKey: "access", SecretKey: "secret"})
	vault := &fakeVault{values: map[uuid.UUID][]byte{kubeconfigID: []byte("kubeconfig"), s3ID: credentialJSON}}
	manager := &fakeManager{}
	cluster := &fakeCluster{existing: kubernetesadapter.VeleroInstallation{Exists: true, ServerImage: "velero:v1.13.2"}}
	service, err := NewService(platformStore, environments, vault, manager, cluster, &fakeCR{}, Images{Velero: veleroImage, AWS: pluginImage})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Install(context.Background(), InstallInput{EnvironmentID: environmentID, ObjectStorageProfile: profileID})
	if !errors.Is(err, ErrUnmanagedInstallation) {
		t.Fatalf("error = %v, want ErrUnmanagedInstallation", err)
	}
	if cluster.namespace != "" || cluster.secret != nil || manager.request.ReleaseName != "" {
		t.Fatal("unmanaged Velero conflict changed cluster state")
	}
}

func TestReuseExistingVeleroCreatesMigrationBSLWithoutHelm(t *testing.T) {
	environmentID, kubeconfigID, profileID, s3ID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	environments := &fakeEnvironmentRepository{value: domainenvironment.Environment{
		ID: environmentID, Name: "sida", Role: domainenvironment.RoleSource, Kind: domainenvironment.KindKubernetes,
		CredentialID: &kubeconfigID, Status: domainenvironment.StatusConnected,
	}}
	store := &fakePlatformRepository{profile: platform.ObjectStorageProfile{
		ID: profileID, Endpoint: "https://minio.example.test", Bucket: "velero", Region: "minio", CredentialID: s3ID, TLSVerify: true,
	}}
	credentialJSON, _ := json.Marshal(objectstorage.Credential{AccessKey: "access", SecretKey: "secret", CABundle: "ca"})
	manager := &fakeManager{}
	cluster := &fakeCluster{existing: kubernetesadapter.VeleroInstallation{
		Exists: true, ServerImage: "velero/velero:v1.13.2", NodeAgentImage: "velero/velero:v1.13.2",
	}}
	cr := &fakeCR{}
	service, err := NewService(store, environments, &fakeVault{values: map[uuid.UUID][]byte{
		kubeconfigID: []byte("kubeconfig"), s3ID: credentialJSON,
	}}, manager, cluster, cr, Images{Velero: veleroImage, AWS: pluginImage})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Reuse(context.Background(), ReuseInput{EnvironmentID: environmentID, ObjectStorageProfile: profileID, Prefix: "/sida/"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Installation.Version != "1.13.2" || result.Installation.Values["managed"] != false || result.Location.Phase != "Available" {
		t.Fatalf("unexpected reuse result: %+v", result)
	}
	if manager.request.ReleaseName != "" || cluster.namespace != "" {
		t.Fatal("reuse must not install Helm or change the existing namespace")
	}
	if cr.spec.Name != BackupLocationName || cr.spec.CredentialSecret != CredentialSecretName || cr.spec.Prefix != "sida" || cr.spec.AccessMode != "ReadWrite" {
		t.Fatalf("unexpected migration BSL: %+v", cr.spec)
	}
	if !strings.Contains(string(cluster.secret[CredentialSecretKey]), "aws_access_key_id=access") {
		t.Fatal("migration credential Secret was not prepared")
	}
}

func TestReuseRequiresExistingNodeAgent(t *testing.T) {
	environmentID, kubeconfigID, profileID := uuid.New(), uuid.New(), uuid.New()
	service, err := NewService(
		&fakePlatformRepository{profile: platform.ObjectStorageProfile{ID: profileID}},
		&fakeEnvironmentRepository{value: domainenvironment.Environment{ID: environmentID, Kind: domainenvironment.KindKubernetes, Status: domainenvironment.StatusConnected, CredentialID: &kubeconfigID}},
		&fakeVault{values: map[uuid.UUID][]byte{kubeconfigID: []byte("kubeconfig")}},
		&fakeManager{}, &fakeCluster{existing: kubernetesadapter.VeleroInstallation{Exists: true, ServerImage: "velero:v1.13.2"}}, &fakeCR{},
		Images{Velero: veleroImage, AWS: pluginImage},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Reuse(context.Background(), ReuseInput{EnvironmentID: environmentID, ObjectStorageProfile: profileID})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
}

func TestStatusDoesNotTrustStaleReadyRecordWhenVeleroWasDeleted(t *testing.T) {
	environmentID, kubeconfigID := uuid.New(), uuid.New()
	store := &fakePlatformRepository{installation: platform.AddonInstallation{
		ID: uuid.New(), EnvironmentID: environmentID, Type: platform.AddonVelero, Version: VeleroVersion,
		Status: platform.InstallationReady, Values: map[string]any{"managed": true},
	}}
	service, err := NewService(store, &fakeEnvironmentRepository{value: domainenvironment.Environment{
		ID: environmentID, Kind: domainenvironment.KindKubernetes, Status: domainenvironment.StatusConnected, CredentialID: &kubeconfigID,
	}}, &fakeVault{values: map[uuid.UUID][]byte{kubeconfigID: []byte("kubeconfig")}}, &fakeManager{}, &fakeCluster{}, &fakeCR{}, Images{Velero: veleroImage, AWS: pluginImage})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Status(context.Background(), environmentID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Health != HealthNeedsRepair || !result.Repairable || result.Installation.Status != platform.InstallationReady {
		t.Fatalf("stale installation was not diagnosed: %+v", result)
	}
	if len(result.Checks) != 1 || !strings.Contains(result.Checks[0].Message, "Deployment") {
		t.Fatalf("missing resource evidence: %+v", result.Checks)
	}
}

func TestStatusDetectsRepositoryDrift(t *testing.T) {
	environmentID, kubeconfigID, profileID := uuid.New(), uuid.New(), uuid.New()
	store := &fakePlatformRepository{
		profile: platform.ObjectStorageProfile{ID: profileID, Endpoint: "https://minio.example.test", Bucket: "velero"},
		installation: platform.AddonInstallation{
			ID: uuid.New(), EnvironmentID: environmentID, Type: platform.AddonVelero, Version: VeleroVersion,
			Status: platform.InstallationReady, Values: map[string]any{"managed": true, "objectStorageProfileId": profileID.String(), "prefix": "migrations/shared"},
		},
	}
	service, err := NewService(store, &fakeEnvironmentRepository{value: domainenvironment.Environment{
		ID: environmentID, Kind: domainenvironment.KindKubernetes, Status: domainenvironment.StatusConnected, CredentialID: &kubeconfigID,
	}}, &fakeVault{values: map[uuid.UUID][]byte{kubeconfigID: []byte("kubeconfig")}}, &fakeManager{}, &fakeCluster{existing: kubernetesadapter.VeleroInstallation{
		Exists: true, ServerImage: "velero:v1", NodeAgentImage: "velero:v1",
	}}, &fakeCR{status: veleroadapter.BackupStorageLocationStatus{
		Name: BackupLocationName, Phase: "Available", Endpoint: "https://other.example.test", Bucket: "velero", Prefix: "migrations/other",
	}}, Images{Velero: veleroImage, AWS: pluginImage})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Status(context.Background(), environmentID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Health != HealthNeedsRepair || !result.Repairable || !strings.Contains(result.Checks[len(result.Checks)-1].Message, "不一致") {
		t.Fatalf("repository drift was not diagnosed: %+v", result)
	}
}

func TestStatusReturnsReadyOnlyAfterLiveChecks(t *testing.T) {
	environmentID, kubeconfigID, profileID := uuid.New(), uuid.New(), uuid.New()
	store := &fakePlatformRepository{
		profile: platform.ObjectStorageProfile{ID: profileID, Endpoint: "https://minio.example.test", Bucket: "velero"},
		installation: platform.AddonInstallation{
			ID: uuid.New(), EnvironmentID: environmentID, Type: platform.AddonVelero, Version: VeleroVersion,
			Status: platform.InstallationReady, Values: map[string]any{"managed": true, "objectStorageProfileId": profileID.String(), "prefix": "migrations"},
		},
	}
	service, err := NewService(store, &fakeEnvironmentRepository{value: domainenvironment.Environment{
		ID: environmentID, Kind: domainenvironment.KindKubernetes, Status: domainenvironment.StatusConnected, CredentialID: &kubeconfigID,
	}}, &fakeVault{values: map[uuid.UUID][]byte{kubeconfigID: []byte("kubeconfig")}}, &fakeManager{}, &fakeCluster{existing: kubernetesadapter.VeleroInstallation{
		Exists: true, ServerImage: "velero:v1", NodeAgentImage: "velero:v1",
	}}, &fakeCR{status: veleroadapter.BackupStorageLocationStatus{
		Name: BackupLocationName, Phase: "Available", Endpoint: "https://minio.example.test", Bucket: "velero", Prefix: "migrations",
	}}, Images{Velero: veleroImage, AWS: pluginImage})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Status(context.Background(), environmentID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Health != HealthReady || len(result.Checks) != 3 {
		t.Fatalf("live installation should be ready: %+v", result)
	}
}

type fakeVault struct{ values map[uuid.UUID][]byte }

func (f *fakeVault) Resolve(_ context.Context, id uuid.UUID) ([]byte, error) {
	value, ok := f.values[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return append([]byte(nil), value...), nil
}

type fakeManager struct {
	request     addon.InstallRequest
	uninstalled bool
}

func (f *fakeManager) InstallOrUpgrade(_ context.Context, _ []byte, request addon.InstallRequest) (addon.ReleaseState, error) {
	f.request = request
	return addon.ReleaseState{Name: request.ReleaseName, Namespace: request.Namespace, Status: "deployed"}, nil
}
func (f *fakeManager) Uninstall(_ []byte, namespace, releaseName string, _ time.Duration) error {
	f.uninstalled = namespace == Namespace && releaseName == ReleaseName
	return nil
}

func TestUninstallUsesHelmAndPreservesRepositoryData(t *testing.T) {
	environmentID, credentialID := uuid.New(), uuid.New()
	installation := platform.AddonInstallation{ID: uuid.New(), EnvironmentID: environmentID, Type: platform.AddonVelero, Version: VeleroVersion, Status: platform.InstallationReady, Values: map[string]any{"managed": true}, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	store := &fakePlatformRepository{installation: installation}
	manager := &fakeManager{}
	service, err := NewService(store, &fakeEnvironmentRepository{value: domainenvironment.Environment{ID: environmentID, Kind: domainenvironment.KindKubernetes, Status: domainenvironment.StatusConnected, CredentialID: &credentialID}}, &fakeVault{values: map[uuid.UUID][]byte{credentialID: []byte("kubeconfig")}}, manager, &fakeCluster{}, &fakeCR{}, Images{Velero: veleroImage, AWS: pluginImage})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Uninstall(context.Background(), environmentID)
	if err != nil {
		t.Fatal(err)
	}
	if !manager.uninstalled || result.Status != platform.InstallationRemoved || !strings.Contains(result.Message, "MinIO") {
		t.Fatalf("unexpected uninstall: manager=%v result=%+v", manager.uninstalled, result)
	}
}

func TestUninstallRefusesReusedVelero(t *testing.T) {
	environmentID := uuid.New()
	store := &fakePlatformRepository{installation: platform.AddonInstallation{
		ID: uuid.New(), EnvironmentID: environmentID, Type: platform.AddonVelero, Version: "1.13.2", Status: platform.InstallationReady,
		Values: map[string]any{"managed": false, "mode": "REUSED"},
	}}
	service, err := NewService(store, &fakeEnvironmentRepository{}, &fakeVault{}, &fakeManager{}, &fakeCluster{}, &fakeCR{}, Images{Velero: veleroImage, AWS: pluginImage})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Uninstall(context.Background(), environmentID); !errors.Is(err, ErrUnmanagedInstallation) {
		t.Fatalf("error = %v, want ErrUnmanagedInstallation", err)
	}
}

type fakeCluster struct {
	namespace string
	secret    map[string][]byte
	existing  kubernetesadapter.VeleroInstallation
}

func (f *fakeCluster) InspectVeleroInstallation(context.Context, []byte, string) (kubernetesadapter.VeleroInstallation, error) {
	return f.existing, nil
}

func (f *fakeCluster) EnsureAddonNamespace(_ context.Context, _ []byte, namespace string) error {
	f.namespace = namespace
	return nil
}

func (f *fakeCluster) PutOpaqueSecret(_ context.Context, _ []byte, _, _ string, data map[string][]byte) error {
	f.secret = make(map[string][]byte, len(data))
	for key, value := range data {
		f.secret[key] = append([]byte(nil), value...)
	}
	return nil
}

type fakeCR struct {
	spec   veleroadapter.BackupStorageLocationSpec
	phases []string
	status veleroadapter.BackupStorageLocationStatus
	err    error
}

func (f *fakeCR) EnsureBackupStorageLocation(_ context.Context, _ []byte, spec veleroadapter.BackupStorageLocationSpec) (veleroadapter.BackupStorageLocationStatus, error) {
	f.spec = spec
	phase := "Available"
	if len(f.phases) > 0 {
		phase, f.phases = f.phases[0], f.phases[1:]
	}
	return veleroadapter.BackupStorageLocationStatus{Name: spec.Name, Phase: phase}, nil
}

func (f *fakeCR) BackupStorageLocationStatus(_ context.Context, _ []byte, _, name string) (veleroadapter.BackupStorageLocationStatus, error) {
	if f.err != nil {
		return veleroadapter.BackupStorageLocationStatus{}, f.err
	}
	if f.status.Name != "" {
		return f.status, nil
	}
	phase := "Available"
	if len(f.phases) > 0 {
		phase, f.phases = f.phases[0], f.phases[1:]
	}
	return veleroadapter.BackupStorageLocationStatus{Name: name, Phase: phase}, nil
}

type fakePlatformRepository struct {
	profile      platform.ObjectStorageProfile
	installation platform.AddonInstallation
}

func (f *fakePlatformRepository) CreateObjectStorageProfile(context.Context, platform.ObjectStorageProfile) error {
	return nil
}
func (f *fakePlatformRepository) GetObjectStorageProfile(_ context.Context, id uuid.UUID) (platform.ObjectStorageProfile, error) {
	if id != f.profile.ID {
		return platform.ObjectStorageProfile{}, repository.ErrNotFound
	}
	return f.profile, nil
}
func (f *fakePlatformRepository) ListObjectStorageProfiles(context.Context) ([]platform.ObjectStorageProfile, error) {
	return []platform.ObjectStorageProfile{f.profile}, nil
}
func (f *fakePlatformRepository) UpsertAddonInstallation(_ context.Context, value platform.AddonInstallation) error {
	f.installation = value
	return nil
}
func (f *fakePlatformRepository) GetAddonInstallation(_ context.Context, environmentID uuid.UUID, addonType platform.AddonType) (platform.AddonInstallation, error) {
	if f.installation.EnvironmentID == environmentID && f.installation.Type == addonType {
		return f.installation, nil
	}
	return platform.AddonInstallation{}, repository.ErrNotFound
}
func (f *fakePlatformRepository) ListAddonInstallations(context.Context, uuid.UUID) ([]platform.AddonInstallation, error) {
	return []platform.AddonInstallation{f.installation}, nil
}

type fakeEnvironmentRepository struct{ value domainenvironment.Environment }

func (f *fakeEnvironmentRepository) Create(context.Context, domainenvironment.Environment) error {
	return nil
}
func (f *fakeEnvironmentRepository) Get(_ context.Context, id uuid.UUID) (domainenvironment.Environment, error) {
	if id != f.value.ID {
		return domainenvironment.Environment{}, repository.ErrNotFound
	}
	return f.value, nil
}
func (f *fakeEnvironmentRepository) List(context.Context) ([]domainenvironment.Environment, error) {
	return []domainenvironment.Environment{f.value}, nil
}
func (f *fakeEnvironmentRepository) Update(_ context.Context, value domainenvironment.Environment) error {
	f.value = value
	return nil
}
func (f *fakeEnvironmentRepository) Delete(context.Context, uuid.UUID) error { return nil }

func nestedValue(value map[string]any, fields ...string) any {
	var current any = value
	for _, field := range fields {
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = object[field]
	}
	return current
}

var _ repository.PlatformRepository = (*fakePlatformRepository)(nil)
var _ repository.EnvironmentRepository = (*fakeEnvironmentRepository)(nil)
