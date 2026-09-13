package storageprofile

import (
	"context"
	"errors"
	"testing"
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

const testProbeImage = "registry.test/probe@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type profileStoreStub struct{ values map[uuid.UUID]storage.Profile }

func (s *profileStoreStub) CreateStorageProfile(_ context.Context, value storage.Profile) error {
	if s.values == nil {
		s.values = map[uuid.UUID]storage.Profile{}
	}
	s.values[value.ID] = value
	return nil
}
func (s *profileStoreStub) GetStorageProfile(_ context.Context, id uuid.UUID) (storage.Profile, error) {
	value, ok := s.values[id]
	if !ok {
		return storage.Profile{}, repository.ErrNotFound
	}
	return value, nil
}
func (s *profileStoreStub) ListStorageProfiles(context.Context, *uuid.UUID) ([]storage.Profile, error) {
	result := make([]storage.Profile, 0, len(s.values))
	for _, value := range s.values {
		result = append(result, value)
	}
	return result, nil
}
func (s *profileStoreStub) UpdateStorageProfile(_ context.Context, value storage.Profile) error {
	s.values[value.ID] = value
	return nil
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

type platformStoreStub struct{ value platform.AddonInstallation }

func (s *platformStoreStub) UpsertAddonInstallation(_ context.Context, value platform.AddonInstallation) error {
	s.value = value
	return nil
}

type vaultStub struct{ credentialID uuid.UUID }

func (s *vaultStub) Resolve(_ context.Context, id uuid.UUID) ([]byte, error) {
	if id != s.credentialID {
		return nil, repository.ErrNotFound
	}
	return []byte("kubeconfig"), nil
}

type clusterStub struct {
	driverExists bool
	ensured      *kubernetesadapter.NFSStorageClassSpec
	probed       bool
}

func (s *clusterStub) EnsureNamespace(_ context.Context, kubeconfig []byte, namespace string) error {
	if string(kubeconfig) != "kubeconfig" || namespace != managedNamespace {
		return errors.New("unexpected namespace request")
	}
	return nil
}
func (s *clusterStub) HasCSIDriver(_ context.Context, _ []byte, name string) (bool, error) {
	if name != nfsCSIName {
		return false, errors.New("unexpected driver")
	}
	return s.driverExists, nil
}
func (s *clusterStub) EnsureNFSStorageClass(_ context.Context, _ []byte, value kubernetesadapter.NFSStorageClassSpec) error {
	s.ensured = &value
	return nil
}
func (s *clusterStub) ProbeStorageClass(_ context.Context, _ []byte, namespace, storageClass, image string) (kubernetesadapter.StorageProbeResult, error) {
	if namespace != managedNamespace || storageClass == "" || image != testProbeImage {
		return kubernetesadapter.StorageProbeResult{}, errors.New("unexpected probe")
	}
	s.probed = true
	return kubernetesadapter.StorageProbeResult{StorageClass: storageClass, PVCName: "probe", Bytes: 128, Remounted: true}, nil
}

type managerStub struct{ called bool }

func (s *managerStub) InstallOrUpgrade(_ context.Context, kubeconfig []byte, request addon.InstallRequest) (addon.ReleaseState, error) {
	s.called = true
	if string(kubeconfig) != "kubeconfig" || request.ChartPath != "csi-driver-nfs" || request.Version != nfsCSIVersion {
		return addon.ReleaseState{}, errors.New("unexpected NFS CSI install")
	}
	return addon.ReleaseState{Status: "deployed"}, nil
}

func TestCreateAndInstallExternalNFSReusesDriverAndProbesRemount(t *testing.T) {
	service, profiles, platformStore, cluster, manager, environmentID := fixture(t, true)
	value, err := service.Create(context.Background(), Input{
		EnvironmentID: environmentID, Name: "migration-nfs", Type: storage.ProfileExternalNFS,
		StorageClassName: "migration-nfs", NFSServer: "10.0.0.20", NFSExport: "/exports/migration",
	})
	if err != nil || value.ReclaimPolicy != storage.ReclaimRetain || value.Status != storage.StatusPending {
		t.Fatalf("create external NFS profile = %+v, %v", value, err)
	}
	result, err := service.Install(context.Background(), value.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Profile.Status != storage.StatusReady || !result.Probe.Remounted || !cluster.probed || manager.called {
		t.Fatalf("unexpected install result=%+v manager=%v", result, manager.called)
	}
	if cluster.ensured == nil || cluster.ensured.ReclaimPolicy != corev1.PersistentVolumeReclaimRetain {
		t.Fatalf("Retain StorageClass was not ensured: %+v", cluster.ensured)
	}
	if platformStore.value.Values["mode"] != "REUSED_EXISTING" || profiles.values[value.ID].Status != storage.StatusReady {
		t.Fatalf("installation was not persisted: %+v", platformStore.value)
	}
}

func TestInstallExternalNFSUsesBundledDriverWhenMissing(t *testing.T) {
	service, _, platformStore, _, manager, environmentID := fixture(t, false)
	value, err := service.Create(context.Background(), Input{
		EnvironmentID: environmentID, Name: "migration-nfs", Type: storage.ProfileExternalNFS,
		StorageClassName: "migration-nfs", NFSServer: "nfs.example.test", NFSExport: "/migration", ReclaimPolicy: storage.ReclaimDelete,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Install(context.Background(), value.ID); err != nil {
		t.Fatal(err)
	}
	if !manager.called || platformStore.value.Values["mode"] != "INSTALLED_OFFLINE" {
		t.Fatalf("missing driver did not use offline chart: manager=%v addon=%+v", manager.called, platformStore.value)
	}
}

func TestCreateRejectsTraversalAndUnknownExistingStorageClass(t *testing.T) {
	service, _, _, _, _, environmentID := fixture(t, true)
	_, err := service.Create(context.Background(), Input{
		EnvironmentID: environmentID, Name: "bad", Type: storage.ProfileExternalNFS,
		StorageClassName: "bad", NFSServer: "10.0.0.20", NFSExport: "/exports/../secret",
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected export traversal rejection, got %v", err)
	}
	_, err = service.Create(context.Background(), Input{
		EnvironmentID: environmentID, Name: "missing", Type: storage.ProfileExistingNFS, StorageClassName: "not-found",
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected unknown StorageClass rejection, got %v", err)
	}
}

func fixture(t *testing.T, driverExists bool) (*Service, *profileStoreStub, *platformStoreStub, *clusterStub, *managerStub, uuid.UUID) {
	t.Helper()
	environmentID, credentialID := uuid.New(), uuid.New()
	profiles := &profileStoreStub{values: map[uuid.UUID]storage.Profile{}}
	environments := &environmentStoreStub{value: domainenvironment.Environment{
		ID: environmentID, Name: "sks", Role: domainenvironment.RoleTarget, Kind: domainenvironment.KindKubernetes,
		CredentialID: &credentialID, Status: domainenvironment.StatusConnected,
		Capabilities: domainenvironment.Capabilities{StorageClasses: []domainenvironment.StorageClass{
			{Name: "existing-nfs", Provisioner: nfsCSIName},
		}}, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}}
	platformStore, cluster, manager := &platformStoreStub{}, &clusterStub{driverExists: driverExists}, &managerStub{}
	service, err := NewService(profiles, environments, platformStore, &vaultStub{credentialID: credentialID}, cluster, manager, Images{
		Plugin:      "registry.test/nfs@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Provisioner: "registry.test/provisioner@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Resizer:     "registry.test/resizer@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		Liveness:    "registry.test/liveness@sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		Registrar:   "registry.test/registrar@sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		Probe:       testProbeImage,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, profiles, platformStore, cluster, manager, environmentID
}
