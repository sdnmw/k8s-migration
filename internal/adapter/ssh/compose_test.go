package ssh

import (
	"strings"
	"testing"
)

func TestComposeActionCommandContainsOnlyFixedLifecycleCommand(t *testing.T) {
	command, err := composeActionCommand(ComposeActionSpec{ProjectName: "shop", ComposeYAML: []byte("services: {}"), Action: ComposeStop})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(command, "docker compose --project-name 'shop'") || !strings.HasSuffix(command, " stop") {
		t.Fatalf("unexpected command: %s", command)
	}
	if _, err := composeActionCommand(ComposeActionSpec{ProjectName: "shop; reboot", ComposeYAML: []byte("x"), Action: ComposeStop}); err == nil {
		t.Fatal("expected invalid project name to be rejected")
	}
	cleanup, err := composeActionCommand(ComposeActionSpec{ProjectName: "shop", ComposeYAML: []byte("services: {}"), Action: ComposeRemove})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(cleanup, " down --volumes --remove-orphans") {
		t.Fatalf("unexpected cleanup command: %s", cleanup)
	}
}

func TestComposeImagePublishCommandUsesExistingContainerImage(t *testing.T) {
	command, images, err := composeImagePublishCommand(ComposeImagePublishSpec{
		RunID: "a2083e35", ProjectName: "react-express-mongodb", Services: []string{"frontend", "backend"},
		Repository: "harbor.example.local/migrations/compose", Registry: RegistryCredential{Username: "robot", Password: "secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if images["backend"] != "harbor.example.local/migrations/compose/react-express-mongodb-backend:run-a2083e35" {
		t.Fatalf("unexpected image mapping: %+v", images)
	}
	for _, want := range []string{"com.docker.compose.project", "docker inspect --format '{{.Image}}'", "docker tag", "docker --config \"$d\" push"} {
		if !strings.Contains(command, want) {
			t.Fatalf("publish command missing %q: %s", want, command)
		}
	}
	if strings.Contains(command, "secret") {
		t.Fatal("plain registry password was embedded in the command")
	}
}

func TestRegistryCredentialFromDockerConfig(t *testing.T) {
	credential, err := RegistryCredentialFromDockerConfig([]byte(`{"auths":{"harbor.local":{"auth":"cm9ib3Q6c2VjcmV0"}}}`), "harbor.local/migrations/compose")
	if err != nil || credential.Username != "robot" || credential.Password != "secret" {
		t.Fatalf("unexpected credential: %+v %v", credential, err)
	}
}

func TestKopiaSnapshotCommandRestrictsMountsAndPinsImage(t *testing.T) {
	repository := KopiaRepository{Image: "harbor.local/kopia@sha256:test", Endpoint: "https://minio.local:9000", Bucket: "velero", AccessKey: "access", SecretKey: "secret", Password: "password", CABundle: "test-ca", TLSVerify: true}
	command, err := kopiaSnapshotCommand(KopiaSnapshotSpec{RunID: "run-1", Repository: repository, Source: KopiaSource{Name: "data", Type: "volume", Path: "shop_data"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(command, "shop_data:/data:ro") || !strings.Contains(command, "snapshot create /data --json") {
		t.Fatalf("unexpected command: %s", command)
	}
	if !strings.Contains(command, "SSL_CERT_FILE=/tmp/sks-migration-root-ca.pem") || !strings.Contains(command, "ROOT_CA_PEM_BASE64=") {
		t.Fatalf("custom CA is not installed for the Kopia process: %s", command)
	}
	for _, path := range []string{"/", "/etc", "/etc/passwd", "relative/path", "/var/run/docker.sock"} {
		_, err := kopiaSnapshotCommand(KopiaSnapshotSpec{RunID: "run-1", Repository: repository, Source: KopiaSource{Name: "data", Type: "bind", Path: path}})
		if err == nil {
			t.Fatalf("expected bind %q to be rejected", path)
		}
	}
}

func TestParseKopiaSnapshot(t *testing.T) {
	value, err := parseKopiaSnapshot("status {not-json}\n{\n  \"id\": \"abc123\",\n  \"rootEntry\": {\"summ\": {\"size\": 42, \"files\": 3}}\n}\nlate status")
	if err != nil || value.ID != "abc123" || value.SizeBytes != 42 || value.Files != 3 {
		t.Fatalf("unexpected snapshot: %+v %v", value, err)
	}
}

func TestSplitComposeConfigFilesPreservesOverrideOrder(t *testing.T) {
	values := splitComposeConfigFiles(" /srv/shop/compose.yaml, /srv/shop/compose.prod.yaml ")
	if len(values) != 2 || values[0] != "/srv/shop/compose.yaml" || values[1] != "/srv/shop/compose.prod.yaml" {
		t.Fatalf("unexpected Compose config files: %v", values)
	}
}
