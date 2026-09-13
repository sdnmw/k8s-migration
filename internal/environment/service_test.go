package environment

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	kubernetesadapter "github.com/smartx/sks-migration-center/internal/adapter/kubernetes"
	sshadapter "github.com/smartx/sks-migration-center/internal/adapter/ssh"
	domaincredential "github.com/smartx/sks-migration-center/internal/domain/credential"
	domainenvironment "github.com/smartx/sks-migration-center/internal/domain/environment"
	"github.com/smartx/sks-migration-center/internal/repository"
)

type memoryEnvironmentRepository struct {
	values    map[uuid.UUID]domainenvironment.Environment
	createErr error
}

func (r *memoryEnvironmentRepository) Create(_ context.Context, value domainenvironment.Environment) error {
	if r.createErr != nil {
		return r.createErr
	}
	for _, existing := range r.values {
		if existing.Name == value.Name {
			return repository.ErrConflict
		}
	}
	r.values[value.ID] = value
	return nil
}
func (r *memoryEnvironmentRepository) Get(_ context.Context, id uuid.UUID) (domainenvironment.Environment, error) {
	value, ok := r.values[id]
	if !ok {
		return domainenvironment.Environment{}, repository.ErrNotFound
	}
	return value, nil
}
func (r *memoryEnvironmentRepository) List(context.Context) ([]domainenvironment.Environment, error) {
	values := make([]domainenvironment.Environment, 0, len(r.values))
	for _, value := range r.values {
		values = append(values, value)
	}
	return values, nil
}
func (r *memoryEnvironmentRepository) Update(_ context.Context, value domainenvironment.Environment) error {
	if _, ok := r.values[value.ID]; !ok {
		return repository.ErrNotFound
	}
	r.values[value.ID] = value
	return nil
}
func (r *memoryEnvironmentRepository) Delete(_ context.Context, id uuid.UUID) error {
	if _, ok := r.values[id]; !ok {
		return repository.ErrNotFound
	}
	delete(r.values, id)
	return nil
}

type memoryVault struct {
	values  map[uuid.UUID][]byte
	types   map[uuid.UUID]domaincredential.Type
	deleted []uuid.UUID
}

func (v *memoryVault) Store(_ context.Context, name string, credentialType domaincredential.Type, payload []byte) (domaincredential.Metadata, error) {
	id := uuid.New()
	v.values[id] = append([]byte(nil), payload...)
	if v.types != nil {
		v.types[id] = credentialType
	}
	return domaincredential.Metadata{ID: id, Name: name, Type: credentialType}, nil
}
func (v *memoryVault) Resolve(_ context.Context, id uuid.UUID) ([]byte, error) {
	value, ok := v.values[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return append([]byte(nil), value...), nil
}
func (v *memoryVault) Delete(_ context.Context, id uuid.UUID) error {
	delete(v.values, id)
	v.deleted = append(v.deleted, id)
	return nil
}

type fakeKubernetesClient struct {
	probeResult kubernetesadapter.ProbeResult
	probeErr    error
}

type fakeSSHClient struct {
	probeResult sshadapter.ProbeResult
	probeErr    error
}

func (f *fakeSSHClient) Prepare(endpoint string, _ sshadapter.Credential) (sshadapter.PreparedConfig, error) {
	return sshadapter.PreparedConfig{Endpoint: endpoint}, nil
}

func (f *fakeSSHClient) Probe(context.Context, string, sshadapter.Credential) (sshadapter.ProbeResult, error) {
	return f.probeResult, f.probeErr
}

func (f *fakeSSHClient) Discover(context.Context, string, sshadapter.Credential) (sshadapter.ProbeResult, error) {
	return f.probeResult, f.probeErr
}

func (f *fakeKubernetesClient) Prepare([]byte) (kubernetesadapter.PreparedConfig, error) {
	return kubernetesadapter.PreparedConfig{Endpoint: "https://sks.example.test:6443"}, nil
}
func (f *fakeKubernetesClient) Probe(context.Context, []byte) (kubernetesadapter.ProbeResult, error) {
	return f.probeResult, f.probeErr
}
func (f *fakeKubernetesClient) Discover(context.Context, []byte) (kubernetesadapter.ProbeResult, error) {
	return f.probeResult, f.probeErr
}
func (f *fakeKubernetesClient) ListNamespaces(context.Context, []byte) ([]string, error) {
	return []string{"business", "default"}, f.probeErr
}

func TestCreateEncryptsViaVaultAndNeverSerializesCredentialMetadata(t *testing.T) {
	store := &memoryEnvironmentRepository{values: map[uuid.UUID]domainenvironment.Environment{}}
	vault := &memoryVault{values: map[uuid.UUID][]byte{}}
	service, _ := NewService(store, vault, &fakeKubernetesClient{})
	service.clock = func() time.Time { return time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC) }

	value, err := service.Create(context.Background(), CreateInput{
		Name: " SKS Production ", Role: domainenvironment.RoleTarget, Kind: domainenvironment.KindKubernetes,
		Credential: []byte("sensitive-kubeconfig"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if value.Name != "SKS Production" || value.Endpoint != "https://sks.example.test:6443" || value.CredentialID == nil {
		t.Fatalf("unexpected environment: %+v", value)
	}
	encoded, _ := json.Marshal(value)
	if string(encoded) == "" || containsAny(string(encoded), "credentialId", "sensitive-kubeconfig") {
		t.Fatalf("credential metadata leaked in API representation: %s", encoded)
	}
}

func TestCreateRemovesCredentialWhenEnvironmentWriteFails(t *testing.T) {
	store := &memoryEnvironmentRepository{values: map[uuid.UUID]domainenvironment.Environment{}, createErr: repository.ErrConflict}
	vault := &memoryVault{values: map[uuid.UUID][]byte{}}
	service, _ := NewService(store, vault, &fakeKubernetesClient{})
	_, err := service.Create(context.Background(), CreateInput{Name: "duplicate", Role: domainenvironment.RoleSource, Kind: domainenvironment.KindKubernetes, Credential: []byte("value")})
	if !errors.Is(err, repository.ErrConflict) || len(vault.values) != 0 || len(vault.deleted) != 1 {
		t.Fatalf("expected rollback of credential, err=%v values=%d deleted=%d", err, len(vault.values), len(vault.deleted))
	}
}

func TestConnectionTestPersistsCapabilitySnapshot(t *testing.T) {
	store := &memoryEnvironmentRepository{values: map[uuid.UUID]domainenvironment.Environment{}}
	vault := &memoryVault{values: map[uuid.UUID][]byte{}}
	client := &fakeKubernetesClient{probeResult: kubernetesadapter.ProbeResult{
		Endpoint:     "https://sks.example.test:6443",
		Capabilities: domainenvironment.Capabilities{KubernetesVersion: "v1.36.2", NodeCount: 3, NamespaceCount: 8},
		Checks:       []domainenvironment.ConnectionCheck{{Name: "API Server", Status: domainenvironment.CheckPassed, Message: "已连接"}},
	}}
	service, _ := NewService(store, vault, client)
	value, err := service.Create(context.Background(), CreateInput{Name: "source", Role: domainenvironment.RoleSource, Kind: domainenvironment.KindKubernetes, Credential: []byte("value")})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	result, err := service.TestConnection(context.Background(), value.ID)
	if err != nil || !result.Success {
		t.Fatalf("test connection: result=%+v err=%v", result, err)
	}
	updated, _ := store.Get(context.Background(), value.ID)
	if updated.Status != domainenvironment.StatusConnected || updated.Capabilities.NodeCount != 3 || updated.CapabilitiesUpdatedAt == nil {
		t.Fatalf("capability snapshot not persisted: %+v", updated)
	}
}

func TestComposeEnvironmentStoresSSHSecretAndPersistsRuntimeProbe(t *testing.T) {
	store := &memoryEnvironmentRepository{values: map[uuid.UUID]domainenvironment.Environment{}}
	vault := &memoryVault{values: map[uuid.UUID][]byte{}, types: map[uuid.UUID]domaincredential.Type{}}
	sshClient := &fakeSSHClient{probeResult: sshadapter.ProbeResult{
		Endpoint: "ssh://compose.example.test:22", Runtime: map[string]string{"dockerVersion": "28.3.3", "composeVersion": "v2.39.2"},
		Checks: []domainenvironment.ConnectionCheck{{Name: "SSH Host Key", Status: domainenvironment.CheckPassed, Message: "主机指纹匹配"}},
	}}
	service, _ := NewService(store, vault, &fakeKubernetesClient{}, sshClient)
	value, err := service.Create(context.Background(), CreateInput{
		Name: "compose-source", Role: domainenvironment.RoleSource, Kind: domainenvironment.KindDockerCompose,
		Endpoint: "ssh://compose.example.test:22", SSH: &sshadapter.Credential{Username: "migration", PrivateKey: "private-key", HostKeyFingerprint: "SHA256:test"},
	})
	if err != nil {
		t.Fatalf("create Compose environment: %v", err)
	}
	if value.CredentialID == nil || vault.types[*value.CredentialID] != domaincredential.TypeSSH {
		t.Fatalf("SSH credential was not stored with SSH type: %+v", value)
	}
	encoded, _ := json.Marshal(value)
	if containsAny(string(encoded), "private-key", "migration", "credential") {
		t.Fatalf("SSH credential leaked in environment response: %s", encoded)
	}
	result, err := service.TestConnection(context.Background(), value.ID)
	if err != nil || !result.Success {
		t.Fatalf("test Compose environment: result=%+v err=%v", result, err)
	}
	updated, _ := store.Get(context.Background(), value.ID)
	if updated.Status != domainenvironment.StatusConnected || updated.Capabilities.Runtime["composeVersion"] != "v2.39.2" {
		t.Fatalf("runtime capabilities not persisted: %+v", updated)
	}
}

func TestRefreshCapabilitiesPersistsFullDiscoveryAndListsNamespaces(t *testing.T) {
	store := &memoryEnvironmentRepository{values: map[uuid.UUID]domainenvironment.Environment{}}
	vault := &memoryVault{values: map[uuid.UUID][]byte{}}
	client := &fakeKubernetesClient{probeResult: kubernetesadapter.ProbeResult{
		Endpoint: "https://sks.example.test:6443",
		Capabilities: domainenvironment.Capabilities{
			KubernetesVersion: "v1.36.2", Architectures: []string{"amd64"}, NodeCount: 3,
			StorageClasses: []domainenvironment.StorageClass{{Name: "smtx-block", Provisioner: "smtx-elf-csi-driver"}},
			CSIDrivers:     []string{"smtx-elf-csi-driver"}, Allocatable: map[string]string{"cpu": "48"},
		},
	}}
	service, _ := NewService(store, vault, client)
	value, _ := service.Create(context.Background(), CreateInput{Name: "target", Role: domainenvironment.RoleTarget, Kind: domainenvironment.KindKubernetes, Credential: []byte("value")})
	capabilities, err := service.RefreshCapabilities(context.Background(), value.ID)
	if err != nil || len(capabilities.StorageClasses) != 1 || capabilities.Allocatable["cpu"] != "48" {
		t.Fatalf("refresh capabilities=%+v err=%v", capabilities, err)
	}
	namespaces, err := service.Namespaces(context.Background(), value.ID)
	if err != nil || len(namespaces) != 2 || namespaces[0] != "business" {
		t.Fatalf("namespaces=%v err=%v", namespaces, err)
	}
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}
