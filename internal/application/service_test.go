package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	sshadapter "github.com/smartx/sks-migration-center/internal/adapter/ssh"
	composeanalyzer "github.com/smartx/sks-migration-center/internal/compose"
	domainapplication "github.com/smartx/sks-migration-center/internal/domain/application"
	domaincredential "github.com/smartx/sks-migration-center/internal/domain/credential"
	domainenvironment "github.com/smartx/sks-migration-center/internal/domain/environment"
	"github.com/smartx/sks-migration-center/internal/repository"
)

type environmentStore struct{ value domainenvironment.Environment }

func (s *environmentStore) Create(context.Context, domainenvironment.Environment) error { return nil }
func (s *environmentStore) Get(context.Context, uuid.UUID) (domainenvironment.Environment, error) {
	if s.value.ID == uuid.Nil {
		return domainenvironment.Environment{}, repository.ErrNotFound
	}
	return s.value, nil
}
func (s *environmentStore) List(context.Context) ([]domainenvironment.Environment, error) {
	return []domainenvironment.Environment{s.value}, nil
}
func (s *environmentStore) Update(context.Context, domainenvironment.Environment) error { return nil }
func (s *environmentStore) Delete(context.Context, uuid.UUID) error                     { return nil }

type applicationStore struct {
	value domainapplication.SourceApplication
}

func (s *applicationStore) Upsert(_ context.Context, value domainapplication.SourceApplication) (domainapplication.SourceApplication, error) {
	if s.value.ID != uuid.Nil {
		value.ID, value.CreatedAt = s.value.ID, s.value.CreatedAt
	}
	s.value = value
	return value, nil
}
func (s *applicationStore) Get(context.Context, uuid.UUID) (domainapplication.SourceApplication, error) {
	return s.value, nil
}
func (s *applicationStore) ListByEnvironment(context.Context, uuid.UUID) ([]domainapplication.SourceApplication, error) {
	return []domainapplication.SourceApplication{s.value}, nil
}

type vault struct {
	value      []byte
	stored     []byte
	storedType domaincredential.Type
	storedID   uuid.UUID
}

func (v *vault) Resolve(context.Context, uuid.UUID) ([]byte, error) {
	return append([]byte(nil), v.value...), nil
}
func (v *vault) Store(_ context.Context, _ string, valueType domaincredential.Type, payload []byte) (domaincredential.Metadata, error) {
	v.stored, v.storedType, v.storedID = append([]byte(nil), payload...), valueType, uuid.New()
	return domaincredential.Metadata{ID: v.storedID, Type: valueType}, nil
}
func (v *vault) Delete(context.Context, uuid.UUID) error { return nil }

type inventoryClient struct {
	credential []byte
	namespace  string
	inventory  domainapplication.Inventory
	err        error
}

type composeHostStub struct {
	projects []sshadapter.ComposeProject
}

func (s *composeHostStub) DiscoverComposeProjects(context.Context, string, sshadapter.Credential) ([]sshadapter.ComposeProject, error) {
	return s.projects, nil
}

func (c *inventoryClient) DiscoverNamespace(_ context.Context, credential []byte, namespace string) (domainapplication.Inventory, error) {
	c.credential, c.namespace = credential, namespace
	return c.inventory, c.err
}

func (c *inventoryClient) ListNamespaces(context.Context, []byte) ([]string, error) {
	return []string{"business"}, c.err
}

func TestDiscoverPersistsKubernetesNamespaceInventoryAndClearsCredential(t *testing.T) {
	credentialID := uuid.New()
	environmentID := uuid.New()
	environments := &environmentStore{value: domainenvironment.Environment{ID: environmentID, Role: domainenvironment.RoleSource, Kind: domainenvironment.KindKubernetes, CredentialID: &credentialID}}
	applications := &applicationStore{}
	credentials := &vault{value: []byte("sensitive-kubeconfig")}
	client := &inventoryClient{inventory: domainapplication.Inventory{Counts: map[string]int{"Deployment": 1}}}
	service, _ := NewService(environments, applications, credentials, client)
	service.clock = func() time.Time { return time.Date(2026, 9, 3, 2, 0, 0, 0, time.UTC) }

	value, err := service.Discover(context.Background(), environmentID, " business ")
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if value.Namespace != "business" || value.SourceType != domainapplication.SourceKubernetes || value.Inventory.Counts["Deployment"] != 1 {
		t.Fatalf("unexpected application: %+v", value)
	}
	if client.namespace != "business" {
		t.Fatalf("namespace was not normalized: %q", client.namespace)
	}
	for _, value := range client.credential {
		if value != 0 {
			t.Fatal("resolved credential was not cleared after discovery")
		}
	}
}

func TestDiscoverRejectsNonKubernetesSource(t *testing.T) {
	environmentID := uuid.New()
	credentialID := uuid.New()
	environments := &environmentStore{value: domainenvironment.Environment{ID: environmentID, Role: domainenvironment.RoleSource, Kind: domainenvironment.KindDockerCompose, CredentialID: &credentialID}}
	service, _ := NewService(environments, &applicationStore{}, &vault{}, &inventoryClient{})
	_, err := service.Discover(context.Background(), environmentID, "business")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid source error, got %v", err)
	}
}

func TestRegisterComposePersistsInventoryAndKeepsDefinitionOutOfResponse(t *testing.T) {
	environmentID, sshCredentialID := uuid.New(), uuid.New()
	environments := &environmentStore{value: domainenvironment.Environment{
		ID: environmentID, Role: domainenvironment.RoleSource, Kind: domainenvironment.KindDockerCompose,
		Status: domainenvironment.StatusConnected, CredentialID: &sshCredentialID,
	}}
	applications, credentials := &applicationStore{}, &vault{}
	service, err := NewService(environments, applications, credentials, &inventoryClient{}, composeanalyzer.NewAnalyzer())
	if err != nil {
		t.Fatal(err)
	}
	composeYAML := []byte("services:\n  api:\n    image: registry.example/api:v1\n    ports: [\"8080:8080\"]\n    volumes: [\"data:/var/lib/api\"]\nvolumes:\n  data: {}\n")
	environmentFile := []byte("DATABASE_PASSWORD=must-not-leak\n")
	value, err := service.RegisterCompose(context.Background(), environmentID, "orders", composeYAML, environmentFile)
	if err != nil {
		t.Fatal(err)
	}
	if value.SourceType != domainapplication.SourceCompose || value.Inventory.Compose == nil || len(value.Inventory.Workloads) != 1 || len(value.Inventory.PVCs) != 1 {
		t.Fatalf("unexpected Compose application: %+v", value)
	}
	if credentials.storedType != domaincredential.TypeCompose || value.DefinitionCredentialID == nil || *value.DefinitionCredentialID != credentials.storedID {
		t.Fatalf("Compose definition was not stored through the credential vault: %+v", value)
	}
	response, _ := json.Marshal(value)
	if strings.Contains(string(response), "must-not-leak") || strings.Contains(string(response), "definitionCredential") {
		t.Fatalf("Compose definition leaked in application response: %s", response)
	}
	var storedDefinition domainapplication.ComposeDefinition
	if err := json.Unmarshal(credentials.stored, &storedDefinition); err != nil || !strings.Contains(string(storedDefinition.EnvironmentFile), "must-not-leak") {
		t.Fatal("stored Compose definition is incomplete")
	}
}

func TestDiscoverComposeKeepsHealthyProjectsWhenAnotherProjectIsInvalid(t *testing.T) {
	environmentID, sshCredentialID := uuid.New(), uuid.New()
	environments := &environmentStore{value: domainenvironment.Environment{
		ID: environmentID, Role: domainenvironment.RoleSource, Kind: domainenvironment.KindDockerCompose,
		Status: domainenvironment.StatusConnected, CredentialID: &sshCredentialID, Endpoint: "ssh://compose:22",
	}}
	sshCredential, _ := json.Marshal(sshadapter.Credential{Username: "migration", HostKeyFingerprint: "SHA256:test", PrivateKey: "key"})
	credentials := &vault{value: sshCredential}
	service, err := NewService(environments, &applicationStore{}, credentials, &inventoryClient{}, composeanalyzer.NewAnalyzer())
	if err != nil {
		t.Fatal(err)
	}
	service.WithComposeHost(&composeHostStub{projects: []sshadapter.ComposeProject{
		{Name: "broken", ComposeYAML: []byte("include: [missing.yaml]\nservices: {}\n")},
		{Name: "shop", Status: "running(1)", WorkingDir: "/srv/shop", ConfigFiles: []string{"/srv/shop/compose.yaml"}, ComposeYAML: []byte("services:\n  web:\n    image: nginx:1.27\n")},
	}})
	values, err := service.DiscoverCompose(context.Background(), environmentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0].Name != "shop" || values[0].Inventory.Compose.WorkingDir != "/srv/shop" {
		t.Fatalf("healthy Compose project was not retained: %+v", values)
	}
}

func TestFilterInventoryKeepsOnlyExactManualResourceSelection(t *testing.T) {
	value := domainapplication.Inventory{
		Resources: []domainapplication.ResourceSummary{
			{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "business", Name: "selected", Images: []string{"registry/selected:v1"}},
			{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "business", Name: "other", Images: []string{"registry/other:v1"}},
			{APIVersion: "v1", Kind: "PersistentVolumeClaim", Namespace: "business", Name: "data"},
		},
		Workloads: []domainapplication.ResourceSummary{
			{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "business", Name: "selected", Images: []string{"registry/selected:v1"}},
			{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "business", Name: "other", Images: []string{"registry/other:v1"}},
		},
		PVCs:   []domainapplication.VolumeSummary{{Namespace: "business", Name: "data"}},
		Images: []domainapplication.ImageSummary{{Reference: "registry/selected:v1"}, {Reference: "registry/other:v1"}},
		Dependencies: []domainapplication.Dependency{{
			From: domainapplication.ResourceReference{Kind: "Deployment", Namespace: "business", Name: "selected"},
			To:   domainapplication.ResourceReference{Kind: "PersistentVolumeClaim", Namespace: "business", Name: "data"}, Type: "MOUNTS", Required: true,
		}},
	}
	filtered, err := filterInventory(value, []domainapplication.ResourceReference{
		{Kind: "Deployment", Namespace: "business", Name: "selected"},
		{Kind: "PersistentVolumeClaim", Namespace: "business", Name: "data"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered.Resources) != 2 || len(filtered.Workloads) != 1 || filtered.Workloads[0].Name != "selected" || len(filtered.PVCs) != 1 || len(filtered.Images) != 1 || len(filtered.Dependencies) != 1 {
		t.Fatalf("manual filter selected unexpected resources: %+v", filtered)
	}
	foundMarker := false
	for _, warning := range filtered.Warnings {
		foundMarker = foundMarker || warning.Code == "MANUAL_RESOURCE_SELECTION"
	}
	if !foundMarker {
		t.Fatal("manual selection marker was not persisted")
	}
}
