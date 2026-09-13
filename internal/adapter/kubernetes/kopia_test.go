package kubernetes

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestKopiaRestoreResourcesMountsOnePVCAndUsesSecretRefs(t *testing.T) {
	spec := KopiaRestoreSpec{Namespace: "shop", RunID: "12345678-abcd", PVC: "redis-data", SnapshotID: "snapshot-1", Image: "harbor.local/kopia@sha256:test", Endpoint: "https://minio.local:9000", Bucket: "velero", Prefix: "compose/app", AccessKey: "access", SecretKey: "secret", Password: "password", CABundle: "test-ca", TLSVerify: true}
	if err := validateKopiaRestoreSpec(spec); err != nil {
		t.Fatal(err)
	}
	secret, job := kopiaRestoreResources(spec, kopiaRestoreName(spec.RunID, spec.PVC))
	if secret.StringData["secretKey"] != "secret" || len(job.Spec.Template.Spec.Containers) != 1 {
		t.Fatalf("unexpected resources: %+v %+v", secret, job)
	}
	if secret.StringData["caBundle"] != base64.StdEncoding.EncodeToString([]byte("test-ca")) {
		t.Fatalf("CA bundle is not encoded for the ROOT_CA_PEM_BASE64 environment: %q", secret.StringData["caBundle"])
	}
	container := job.Spec.Template.Spec.Containers[0]
	if container.VolumeMounts[0].MountPath != "/data" || !strings.Contains(container.Args[0], "snapshot restore 'snapshot-1' /data --delete-extra") {
		t.Fatalf("unexpected restore container: %+v", container)
	}
	if !strings.Contains(container.Args[0], "SSL_CERT_FILE=/tmp/sks-migration-root-ca.pem") {
		t.Fatalf("custom CA is not installed for Kopia restore: %s", container.Args[0])
	}
	if container.Env[0].Value != "" || container.Env[0].ValueFrom == nil {
		t.Fatal("credentials must be read through Secret refs")
	}
}

func TestKopiaRestoreRejectsUnpinnedImage(t *testing.T) {
	err := validateKopiaRestoreSpec(KopiaRestoreSpec{Namespace: "shop", RunID: "run", PVC: "data", SnapshotID: "id", Image: "kopia:latest", Endpoint: "https://minio", Bucket: "bucket", AccessKey: "a", SecretKey: "s", Password: "p"})
	if err == nil {
		t.Fatal("expected unpinned image to fail")
	}
}
