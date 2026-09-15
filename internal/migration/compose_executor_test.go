package migration

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	kubernetesadapter "github.com/smartx/sks-migration-center/internal/adapter/kubernetes"
	sshadapter "github.com/smartx/sks-migration-center/internal/adapter/ssh"
	domainapplication "github.com/smartx/sks-migration-center/internal/domain/application"
	domainenvironment "github.com/smartx/sks-migration-center/internal/domain/environment"
	domainmapping "github.com/smartx/sks-migration-center/internal/domain/mapping"
	domainmigration "github.com/smartx/sks-migration-center/internal/domain/migration"
	domainplatform "github.com/smartx/sks-migration-center/internal/domain/platform"
	"github.com/smartx/sks-migration-center/internal/repository"
	"github.com/smartx/sks-migration-center/internal/transform"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestComposeExecutorConvertsTransformsAppliesAndValidates(t *testing.T) {
	runID, planID, appID, sourceID, targetID, mappingID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	definitionID, targetCredentialID := uuid.New(), uuid.New()
	definition, _ := json.Marshal(domainapplication.ComposeDefinition{ComposeYAML: []byte("services:\n  api:\n    image: legacy.local/team/api:v1\n")})
	application := domainapplication.SourceApplication{
		ID: appID, EnvironmentID: sourceID, Name: "Shop_App", SourceType: domainapplication.SourceCompose, DefinitionCredentialID: &definitionID,
		Inventory: domainapplication.Inventory{Compose: &domainapplication.ComposeInventory{ProjectName: "Shop_App", Services: []domainapplication.ComposeService{{Name: "api", Image: "legacy.local/team/api:v1"}}}},
	}
	plan := domainmigration.Plan{
		ID: planID, SourceEnvironmentID: sourceID, TargetEnvironmentID: targetID, SourceApplicationID: appID, MappingProfileID: mappingID,
		Strategy: domainmigration.Strategy{OverwriteExistingResources: true},
	}
	progress := &progressRepositoryStub{}
	kubernetes := &composeKubernetesStub{manifests: []byte("apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: api\nspec:\n  template:\n    spec:\n      containers:\n      - name: api\n        image: legacy.local/team/api:v1\n")}
	executor, err := NewComposeExecutor(
		&runPlanRepositoryStub{plan: plan}, &runRepositoryStub{run: domainmigration.Run{ID: runID, PlanID: planID}}, progress,
		&environmentRepositoryStub{values: map[uuid.UUID]domainenvironment.Environment{
			sourceID: {ID: sourceID, Role: domainenvironment.RoleSource, Kind: domainenvironment.KindDockerCompose, Status: domainenvironment.StatusConnected},
			targetID: {ID: targetID, Role: domainenvironment.RoleTarget, Kind: domainenvironment.KindKubernetes, Status: domainenvironment.StatusConnected, CredentialID: &targetCredentialID},
		}},
		&applicationRepositoryStub{value: application},
		&mappingRepositoryStub{value: domainmapping.Profile{ID: mappingID, TargetEnvironmentID: targetID, Namespaces: []domainmapping.KeyValue{{Source: "Shop_App", Target: "shop"}}, Registries: []domainmapping.KeyValue{{Source: "legacy.local", Target: "harbor.local/migrated"}}}},
		&executionVaultStub{values: map[uuid.UUID][]byte{definitionID: definition, targetCredentialID: []byte("target-kubeconfig")}},
		kubernetes, transform.NewEngine(), "harbor.local/kompose@sha256:test", "harbor.local/busybox@sha256:test",
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []domainmigration.StepType{domainmigration.StepPreflight, domainmigration.StepPreSync, domainmigration.StepQuiesce, domainmigration.StepFinalBackup, domainmigration.StepTransform, domainmigration.StepRestore, domainmigration.StepValidation} {
		if err := executor.Handle(context.Background(), domainmigration.Lease{RunID: runID, StepType: step}); err != nil {
			t.Fatalf("%s: %v", step, err)
		}
	}
	if kubernetes.conversions != 3 || len(kubernetes.applies) != 1 || kubernetes.validations != 1 {
		t.Fatalf("unexpected calls: %+v", kubernetes)
	}
	applied := string(kubernetes.applies[0].Manifests)
	if kubernetes.applies[0].DefaultNamespace != "shop" || !kubernetes.applies[0].Overwrite || !stringsContains(applied, "harbor.local/migrated/team/api:v1") {
		t.Fatalf("mapping or apply spec missing: %+v\n%s", kubernetes.applies[0], applied)
	}
	if len(progress.events) != 9 || progress.events[5].Type != "COMPOSE_MANIFESTS_APPLIED" || progress.events[7].Type != "COMPOSE_RESOURCE_VALIDATION_COLLECTED" {
		t.Fatalf("expected step events plus the idempotent apply checkpoint, got %+v", progress.events)
	}
}

func TestComposeExecutorBlocksVolumesUntilKopiaPath(t *testing.T) {
	executor, runID := composeExecutorFixture(t, []domainapplication.VolumeSummary{{Name: "data"}})
	err := executor.Handle(context.Background(), domainmigration.Lease{RunID: runID, StepType: domainmigration.StepPreflight})
	if err == nil || !stringsContains(err.Error(), "Kopia") {
		t.Fatalf("expected Kopia blocker, got %v", err)
	}
}

func TestComposeExecutorPublishesBuildOnlyImagesWithoutChangingSourceDefinition(t *testing.T) {
	runID, planID, appID, sourceID, targetID, mappingID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	definitionID, targetCredentialID, sourceCredentialID := uuid.New(), uuid.New(), uuid.New()
	composeYAML := []byte("services:\n  backend:\n    build:\n      context: /srv/backend\n")
	definition, _ := json.Marshal(domainapplication.ComposeDefinition{ComposeYAML: composeYAML})
	sshCredential, _ := json.Marshal(map[string]string{"username": "migration", "privateKey": "key", "hostKeyFingerprint": "SHA256:test"})
	application := domainapplication.SourceApplication{
		ID: appID, EnvironmentID: sourceID, Name: "react-express", SourceType: domainapplication.SourceCompose, DefinitionCredentialID: &definitionID,
		Inventory: domainapplication.Inventory{Compose: &domainapplication.ComposeInventory{ProjectName: "react-express", Services: []domainapplication.ComposeService{{Name: "backend", Build: true}}}},
	}
	runs, progress := &runRepositoryStub{run: domainmigration.Run{ID: runID, PlanID: planID}}, &progressRepositoryStub{}
	kubernetes := &composeKubernetesStub{manifests: []byte("apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: backend}\nspec: {template: {spec: {containers: [{name: backend, image: ignored}]}}}\n")}
	publisher := &composeImagePublisherStub{images: map[string]string{"backend": "harbor.local/migrations/compose/react-express-backend:run-" + runID.String()}}
	executor, err := NewComposeExecutor(
		&runPlanRepositoryStub{plan: domainmigration.Plan{ID: planID, SourceEnvironmentID: sourceID, TargetEnvironmentID: targetID, SourceApplicationID: appID, MappingProfileID: mappingID}}, runs, progress,
		&environmentRepositoryStub{values: map[uuid.UUID]domainenvironment.Environment{
			sourceID: {ID: sourceID, Role: domainenvironment.RoleSource, Kind: domainenvironment.KindDockerCompose, Endpoint: "ssh://compose:22", Status: domainenvironment.StatusConnected, CredentialID: &sourceCredentialID},
			targetID: {ID: targetID, Role: domainenvironment.RoleTarget, Kind: domainenvironment.KindKubernetes, Status: domainenvironment.StatusConnected, CredentialID: &targetCredentialID},
		}}, &applicationRepositoryStub{value: application}, &mappingRepositoryStub{value: domainmapping.Profile{ID: mappingID, TargetEnvironmentID: targetID}},
		&executionVaultStub{values: map[uuid.UUID][]byte{definitionID: definition, targetCredentialID: []byte("target"), sourceCredentialID: sshCredential}},
		kubernetes, transform.NewEngine(), "harbor/kompose@sha256:test", "harbor/helper@sha256:test",
		WithComposeBuildImagePublishing(publisher, "harbor.local/migrations/compose", sshadapter.RegistryCredential{Username: "robot", Password: "secret"}, []byte(`{"auths":{"harbor.local":{}}}`), "migration-registry"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.Handle(context.Background(), domainmigration.Lease{RunID: runID, StepType: domainmigration.StepPreflight}); err != nil {
		t.Fatal(err)
	}
	if len(publisher.calls) != 1 || len(kubernetes.conversionSpecs) != 1 || len(progress.events) != 2 {
		t.Fatalf("unexpected publishing flow: publisher=%+v conversions=%d events=%+v", publisher.calls, len(kubernetes.conversionSpecs), progress.events)
	}
	targetDefinition := string(kubernetes.conversionSpecs[0].ComposeYAML)
	if !stringsContains(targetDefinition, publisher.images["backend"]) || stringsContains(targetDefinition, "build:") {
		t.Fatalf("target conversion did not use the published image: %s", targetDefinition)
	}
	if string(composeYAML) != "services:\n  backend:\n    build:\n      context: /srv/backend\n" {
		t.Fatal("source Compose definition was mutated")
	}
	runs.events = append([]domainmigration.Event(nil), progress.events...)
	if err := executor.Handle(context.Background(), domainmigration.Lease{RunID: runID, StepType: domainmigration.StepRestore}); err != nil {
		t.Fatal(err)
	}
	if kubernetes.namespaceEnsures != 1 || kubernetes.registrySecrets != 1 || len(kubernetes.applies) != 1 {
		t.Fatalf("target registry preparation was not applied: %+v", kubernetes)
	}
	applied := string(kubernetes.applies[0].Manifests)
	if !stringsContains(applied, "imagePullSecrets") || !stringsContains(applied, "migration-registry") {
		t.Fatalf("published image pull secret was not injected into the target workload: %s", applied)
	}
}

func TestPublishedBuildImageDropsOnlyDevelopmentMountsFromTarget(t *testing.T) {
	composeYAML := []byte(`services:
  api:
    build:
      context: /srv/api
    volumes:
      - type: bind
        source: /srv/api
        target: /workspace
      - type: volume
        target: /workspace/node_modules
      - type: volume
        source: business-data
        target: /var/lib/app
volumes:
  business-data: {}
`)
	images := map[string]string{"api": "harbor.local/migrations/api:run-test"}
	converted, err := composeYAMLWithPublishedImages(composeYAML, images)
	if err != nil {
		t.Fatal(err)
	}
	text := string(converted)
	if stringsContains(text, "build:") || stringsContains(text, "/workspace") || !stringsContains(text, "business-data") {
		t.Fatalf("development mounts were not pruned correctly:\n%s", text)
	}
	inventory := &domainapplication.ComposeInventory{Services: []domainapplication.ComposeService{{
		Name: "api", Build: true, Mounts: []domainapplication.ComposeMount{
			{Type: "bind", Source: "/srv/api", Target: "/workspace"},
			{Type: "volume", Target: "/workspace/node_modules"},
			{Type: "volume", Source: "business-data", Target: "/var/lib/app"},
		},
	}}}
	filtered := composeInventoryForPublishedImages(inventory, composeYAML, images)
	if len(filtered.Services[0].Mounts) != 1 || filtered.Services[0].Mounts[0].Source != "business-data" {
		t.Fatalf("filtered inventory = %+v", filtered.Services[0].Mounts)
	}
	if len(inventory.Services[0].Mounts) != 3 {
		t.Fatal("source inventory was mutated")
	}
}

func TestComposeBuildImagePublishingOptionOwnsDockerConfig(t *testing.T) {
	dockerConfig := []byte(`{"auths":{"harbor.local":{"auth":"dGVzdDp0ZXN0"}}}`)
	option := WithComposeBuildImagePublishing(&composeImagePublisherStub{}, "harbor.local/migrations", sshadapter.RegistryCredential{Username: "test", Password: "test"}, dockerConfig, "registry-secret")
	for index := range dockerConfig {
		dockerConfig[index] = 0
	}
	executor := &ComposeExecutor{}
	if err := option(executor); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(executor.registryDockerConfig) {
		t.Fatal("executor retained the caller-owned Docker config buffer")
	}
}

func TestComposeTargetNamespaceIsStable(t *testing.T) {
	if got := composeTargetNamespace("My_App.Prod", nil); got != "my-app-prod" {
		t.Fatalf("namespace = %q", got)
	}
	if got := composeTargetNamespace("source", []domainmapping.KeyValue{{Source: "source", Target: "target"}}); got != "target" {
		t.Fatalf("mapped namespace = %q", got)
	}
}

func TestEnsureComposeInternalServicesUsesExposeAndPublishedPorts(t *testing.T) {
	result := transform.Result{Documents: []transform.Document{{
		APIVersion: "apps/v1", Kind: "Deployment", Namespace: "harbor", Name: "core",
		Object: unstructured.Unstructured{Object: map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": map[string]any{"name": "core", "namespace": "harbor"}}},
	}}}
	inventory := &domainapplication.ComposeInventory{Services: []domainapplication.ComposeService{
		{Name: "core", Expose: []string{"8080/tcp"}},
		{Name: "redis", Ports: []domainapplication.ComposePort{{Target: 6379, Protocol: "tcp"}}},
	}}
	ensureComposeInternalServices(&result, inventory, "harbor")
	if len(result.Documents) != 3 {
		t.Fatalf("documents=%d, want deployment plus two services", len(result.Documents))
	}
	for _, name := range []string{"core", "redis"} {
		found := false
		for _, document := range result.Documents {
			if document.Kind == "Service" && document.Name == name {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing generated Service %s", name)
		}
	}
}

func TestRawExposeSupplementsInventoryWhenFullComposeAnalysisIsUnavailable(t *testing.T) {
	inventory := &domainapplication.ComposeInventory{Services: []domainapplication.ComposeService{{Name: "core"}}}
	result := composeInventoryWithRawExpose(inventory, []byte("services:\n  core:\n    expose: [8080, 8443/tcp]\n"))
	if len(result.Services[0].Expose) != 2 || result.Services[0].Expose[0] != "8080" || result.Services[0].Expose[1] != "8443/tcp" {
		t.Fatalf("raw expose values not preserved: %+v", result.Services[0].Expose)
	}
	if len(inventory.Services[0].Expose) != 0 {
		t.Fatal("stored inventory was mutated")
	}
}

func TestComposeExecutorMigratesKopiaVolumeAndRestartsSourceOnRollback(t *testing.T) {
	runID, planID, appID, sourceID, targetID, mappingID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	definitionID, targetCredentialID, sourceCredentialID, storageCredentialID, profileID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	definition, _ := json.Marshal(domainapplication.ComposeDefinition{ComposeYAML: []byte("services:\n  redis:\n    image: redis:7\n    volumes: [data:/data]\nvolumes:\n  data: {}\n")})
	sshCredential, _ := json.Marshal(map[string]string{"username": "migration", "privateKey": "key", "hostKeyFingerprint": "SHA256:test"})
	storageCredential, _ := json.Marshal(map[string]string{"accessKey": "access", "secretKey": "secret", "caBundle": "ca"})
	application := domainapplication.SourceApplication{ID: appID, EnvironmentID: sourceID, Name: "shop", SourceType: domainapplication.SourceCompose, DefinitionCredentialID: &definitionID, Inventory: domainapplication.Inventory{Compose: &domainapplication.ComposeInventory{
		ProjectName: "shop", Services: []domainapplication.ComposeService{{Name: "redis", Image: "redis:7", Mounts: []domainapplication.ComposeMount{{Type: "volume", Source: "data", Target: "/data"}}}}, Volumes: []domainapplication.ComposeResource{{Name: "data", RuntimeName: "shop_data"}},
	}}}
	plan := domainmigration.Plan{ID: planID, SourceEnvironmentID: sourceID, TargetEnvironmentID: targetID, SourceApplicationID: appID, MappingProfileID: mappingID, Strategy: domainmigration.Strategy{VolumeMode: domainmigration.VolumeComposeKopia, OverwriteExistingResources: true}}
	progress, runs, mover := &progressRepositoryStub{}, &runRepositoryStub{run: domainmigration.Run{ID: runID, PlanID: planID}}, &composeSourceMoverStub{}
	kubernetes := &composeKubernetesStub{manifests: []byte("apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: redis\n  labels:\n    io.kompose.service: redis\nspec:\n  replicas: 1\n  template:\n    spec:\n      containers:\n      - name: redis\n        image: redis:7\n        volumeMounts:\n        - name: data\n          mountPath: /data\n      volumes:\n      - name: data\n        persistentVolumeClaim:\n          claimName: data\n---\napiVersion: v1\nkind: PersistentVolumeClaim\nmetadata:\n  name: data\nspec:\n  accessModes: [ReadWriteOnce]\n  resources:\n    requests:\n      storage: 100Mi\n")}
	executor, err := NewComposeExecutor(
		&runPlanRepositoryStub{plan: plan}, runs, progress,
		&environmentRepositoryStub{values: map[uuid.UUID]domainenvironment.Environment{
			sourceID: {ID: sourceID, Role: domainenvironment.RoleSource, Kind: domainenvironment.KindDockerCompose, Endpoint: "ssh://compose:22", Status: domainenvironment.StatusConnected, CredentialID: &sourceCredentialID},
			targetID: {ID: targetID, Role: domainenvironment.RoleTarget, Kind: domainenvironment.KindKubernetes, Status: domainenvironment.StatusConnected, CredentialID: &targetCredentialID},
		}},
		&applicationRepositoryStub{value: application}, &mappingRepositoryStub{value: domainmapping.Profile{ID: mappingID, TargetEnvironmentID: targetID}},
		&executionVaultStub{values: map[uuid.UUID][]byte{definitionID: definition, targetCredentialID: []byte("target"), sourceCredentialID: sshCredential, storageCredentialID: storageCredential}},
		kubernetes, transform.NewEngine(), "harbor/kompose@sha256:test", "harbor/helper@sha256:test",
		WithComposeDataMovement(&composePlatformStub{profile: domainplatform.ObjectStorageProfile{ID: profileID, Endpoint: "https://minio:9000", Bucket: "velero", CredentialID: storageCredentialID, TLSVerify: true}, installation: domainplatform.AddonInstallation{EnvironmentID: targetID, Type: domainplatform.AddonMinIO, Status: domainplatform.InstallationReady, Values: map[string]any{"profileId": profileID.String()}}}, mover, "harbor/kopia@sha256:test"),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []domainmigration.StepType{domainmigration.StepPreflight, domainmigration.StepPreSync, domainmigration.StepQuiesce, domainmigration.StepFinalBackup, domainmigration.StepTransfer, domainmigration.StepRestore} {
		if err := executor.Handle(context.Background(), domainmigration.Lease{RunID: runID, StepType: step}); err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		runs.events = append([]domainmigration.Event(nil), progress.events...)
	}
	if len(mover.snapshots) != 2 || len(mover.actions) != 1 || mover.actions[0] != "STOP" {
		t.Fatalf("unexpected source calls: %+v", mover)
	}
	if len(kubernetes.restores) != 1 || kubernetes.restores[0].PVC != "data" || len(kubernetes.scales) != 1 || kubernetes.scales[0][0].Replicas != 1 {
		t.Fatalf("unexpected target calls: %+v", kubernetes)
	}
	if err := executor.Handle(context.Background(), domainmigration.Lease{RunID: runID, StepType: domainmigration.StepRollback}); err != nil {
		t.Fatal(err)
	}
	if len(mover.actions) != 2 || mover.actions[1] != "START" {
		t.Fatalf("source was not restarted: %+v", mover.actions)
	}
}

type composeKubernetesStub struct {
	manifests           []byte
	conversions         int
	applies             []kubernetesadapter.ApplyManifestSpec
	validations         int
	endpointValidations int
	restores            []kubernetesadapter.KopiaRestoreSpec
	scales              [][]kubernetesadapter.ScalableWorkload
	conversionSpecs     []kubernetesadapter.KomposeJobSpec
	namespaceEnsures    int
	registrySecrets     int
}

type composeImagePublisherStub struct {
	images map[string]string
	calls  []sshadapter.ComposeImagePublishSpec
}

func (s *composeImagePublisherStub) PublishComposeImages(_ context.Context, _ string, _ sshadapter.Credential, spec sshadapter.ComposeImagePublishSpec) (map[string]string, error) {
	s.calls = append(s.calls, spec)
	result := map[string]string{}
	for key, value := range s.images {
		result[key] = value
	}
	return result, nil
}

type composeSourceMoverStub struct {
	actions   []string
	snapshots []string
}

func (s *composeSourceMoverStub) RunComposeAction(_ context.Context, _ string, _ sshadapter.Credential, value sshadapter.ComposeActionSpec) error {
	s.actions = append(s.actions, string(value.Action))
	return nil
}
func (s *composeSourceMoverStub) CreateKopiaSnapshot(_ context.Context, _ string, _ sshadapter.Credential, _ sshadapter.KopiaSnapshotSpec) (sshadapter.KopiaSnapshot, error) {
	id := "snapshot-" + string(rune('1'+len(s.snapshots)))
	s.snapshots = append(s.snapshots, id)
	return sshadapter.KopiaSnapshot{ID: id, SizeBytes: 42, Files: 3}, nil
}

type composePlatformStub struct {
	profile      domainplatform.ObjectStorageProfile
	installation domainplatform.AddonInstallation
}

func (s *composePlatformStub) CreateObjectStorageProfile(context.Context, domainplatform.ObjectStorageProfile) error {
	return nil
}
func (s *composePlatformStub) GetObjectStorageProfile(context.Context, uuid.UUID) (domainplatform.ObjectStorageProfile, error) {
	return s.profile, nil
}
func (s *composePlatformStub) ListObjectStorageProfiles(context.Context) ([]domainplatform.ObjectStorageProfile, error) {
	return nil, nil
}
func (s *composePlatformStub) UpsertAddonInstallation(context.Context, domainplatform.AddonInstallation) error {
	return nil
}
func (s *composePlatformStub) GetAddonInstallation(context.Context, uuid.UUID, domainplatform.AddonType) (domainplatform.AddonInstallation, error) {
	return s.installation, nil
}
func (s *composePlatformStub) ListAddonInstallations(context.Context, uuid.UUID) ([]domainplatform.AddonInstallation, error) {
	return nil, repository.ErrNotFound
}

func (s *composeKubernetesStub) RunKopiaRestoreJob(_ context.Context, _ []byte, value kubernetesadapter.KopiaRestoreSpec) error {
	s.restores = append(s.restores, value)
	return nil
}
func (s *composeKubernetesStub) ScaleWorkloads(_ context.Context, _ []byte, values []kubernetesadapter.ScalableWorkload) error {
	s.scales = append(s.scales, values)
	return nil
}

func (s *composeKubernetesStub) RunKomposeJob(_ context.Context, _ []byte, spec kubernetesadapter.KomposeJobSpec) ([]byte, error) {
	s.conversions++
	spec.ComposeYAML = append([]byte(nil), spec.ComposeYAML...)
	s.conversionSpecs = append(s.conversionSpecs, spec)
	return append([]byte(nil), s.manifests...), nil
}
func (s *composeKubernetesStub) EnsureMigrationNamespace(context.Context, []byte, string) error {
	s.namespaceEnsures++
	return nil
}
func (s *composeKubernetesStub) PutDockerConfigSecret(context.Context, []byte, string, string, []byte) error {
	s.registrySecrets++
	return nil
}
func (s *composeKubernetesStub) ApplyManifests(_ context.Context, _ []byte, value kubernetesadapter.ApplyManifestSpec) ([]kubernetesadapter.AppliedResource, error) {
	value.Manifests = append([]byte(nil), value.Manifests...)
	s.applies = append(s.applies, value)
	return []kubernetesadapter.AppliedResource{{Kind: "Deployment", Namespace: value.DefaultNamespace, Name: "api"}}, nil
}
func (s *composeKubernetesStub) ValidateNamespace(context.Context, []byte, string) (kubernetesadapter.NamespaceValidation, error) {
	s.validations++
	return kubernetesadapter.NamespaceValidation{Deployments: 1}, nil
}
func (s *composeKubernetesStub) ValidateEndpoints(context.Context, []byte, kubernetesadapter.EndpointValidationSpec) error {
	s.endpointValidations++
	return nil
}

func composeExecutorFixture(t *testing.T, volumes []domainapplication.VolumeSummary) (*ComposeExecutor, uuid.UUID) {
	t.Helper()
	runID, planID, appID, sourceID, targetID, mappingID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	definitionID, targetCredentialID := uuid.New(), uuid.New()
	definition, _ := json.Marshal(domainapplication.ComposeDefinition{ComposeYAML: []byte("services: {}")})
	executor, err := NewComposeExecutor(
		&runPlanRepositoryStub{plan: domainmigration.Plan{ID: planID, SourceEnvironmentID: sourceID, TargetEnvironmentID: targetID, SourceApplicationID: appID, MappingProfileID: mappingID}},
		&runRepositoryStub{run: domainmigration.Run{ID: runID, PlanID: planID}}, &progressRepositoryStub{},
		&environmentRepositoryStub{values: map[uuid.UUID]domainenvironment.Environment{
			sourceID: {ID: sourceID, Role: domainenvironment.RoleSource, Kind: domainenvironment.KindDockerCompose, Status: domainenvironment.StatusConnected},
			targetID: {ID: targetID, Role: domainenvironment.RoleTarget, Kind: domainenvironment.KindKubernetes, Status: domainenvironment.StatusConnected, CredentialID: &targetCredentialID},
		}},
		&applicationRepositoryStub{value: domainapplication.SourceApplication{ID: appID, EnvironmentID: sourceID, Name: "migration", SourceType: domainapplication.SourceCompose, DefinitionCredentialID: &definitionID, Inventory: domainapplication.Inventory{PVCs: volumes, Compose: &domainapplication.ComposeInventory{ProjectName: "migration", Services: []domainapplication.ComposeService{{Name: "api", Image: "example/api:v1", Mounts: []domainapplication.ComposeMount{{Type: "volume", Source: "data", Target: "/data"}}}}, Volumes: []domainapplication.ComposeResource{{Name: "data", RuntimeName: "migration_data"}}}}}},
		&mappingRepositoryStub{value: domainmapping.Profile{ID: mappingID, TargetEnvironmentID: targetID}},
		&executionVaultStub{values: map[uuid.UUID][]byte{definitionID: definition, targetCredentialID: []byte("target")}},
		&composeKubernetesStub{manifests: []byte("apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: api\n  labels:\n    io.kompose.service: api\nspec:\n  template:\n    spec:\n      containers:\n      - name: api\n        volumeMounts:\n        - name: data\n          mountPath: /data\n      volumes:\n      - name: data\n        persistentVolumeClaim:\n          claimName: data\n---\napiVersion: v1\nkind: PersistentVolumeClaim\nmetadata:\n  name: data\nspec:\n  accessModes: [ReadWriteOnce]\n")}, transform.NewEngine(),
		"harbor.local/kompose@sha256:test", "harbor.local/busybox@sha256:test",
	)
	if err != nil {
		t.Fatal(err)
	}
	return executor, runID
}

type executionHandlerStub struct {
	calls int
	err   error
}

func (s *executionHandlerStub) Handle(context.Context, domainmigration.Lease) error {
	s.calls++
	return s.err
}

func TestSourceDispatcherRoutesByPersistedApplicationType(t *testing.T) {
	runID, planID, appID := uuid.New(), uuid.New(), uuid.New()
	kubernetesHandler := &executionHandlerStub{err: errors.New("wrong handler")}
	composeHandler := &executionHandlerStub{}
	dispatcher, err := NewSourceDispatcher(
		&runRepositoryStub{run: domainmigration.Run{ID: runID, PlanID: planID}},
		&runPlanRepositoryStub{plan: domainmigration.Plan{ID: planID, SourceApplicationID: appID}},
		&applicationRepositoryStub{value: domainapplication.SourceApplication{ID: appID, SourceType: domainapplication.SourceCompose}},
		kubernetesHandler, composeHandler,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Handle(context.Background(), domainmigration.Lease{RunID: runID, StepType: domainmigration.StepTransform}); err != nil {
		t.Fatal(err)
	}
	if composeHandler.calls != 1 || kubernetesHandler.calls != 0 {
		t.Fatalf("unexpected routing: kubernetes=%d compose=%d", kubernetesHandler.calls, composeHandler.calls)
	}
}
