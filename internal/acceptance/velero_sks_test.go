package acceptance

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubernetesclient "k8s.io/client-go/kubernetes"

	"github.com/smartx/sks-migration-center/internal/adapter/kubernetes"
	veleroadapter "github.com/smartx/sks-migration-center/internal/adapter/velero"
	"github.com/smartx/sks-migration-center/internal/addon"
	veleroservice "github.com/smartx/sks-migration-center/internal/velero"
)

const (
	acceptanceVeleroImage = "docker.io/velero/velero@sha256:11459094b1b21ec7c817b08f8067d9e89380835547915cac9c4132ff05b55b90"
	acceptanceAWSPlugin   = "docker.io/velero/velero-plugin-for-aws@sha256:7e82f717f44e89671212e0dfce7e061321c386ea84a33bca64a671670ca6c278"
	acceptanceTestNS      = "sks-migration-velero-acceptance"
)

// TestVeleroOnSKS installs the pinned official Velero release on both the
// target and source workload clusters, connects both to the target MinIO and
// verifies a source Backup CR reaches Completed. It intentionally leaves the
// Velero releases in place for subsequent migration acceptance.
func TestVeleroOnSKS(t *testing.T) {
	sourcePath := os.Getenv("TEST_SKS_SOURCE_KUBECONFIG")
	targetPath := os.Getenv("TEST_SKS_TARGET_KUBECONFIG")
	if sourcePath == "" || targetPath == "" {
		t.Skip("TEST_SKS_SOURCE_KUBECONFIG and TEST_SKS_TARGET_KUBECONFIG are not set")
	}
	sourceConfig := readSensitiveFile(t, sourcePath)
	targetConfig := readSensitiveFile(t, targetPath)
	defer zero(sourceConfig)
	defer zero(targetConfig)

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()
	endpoint, caBundle, accessKey, secretKey := minIOConnection(t, ctx, targetConfig)
	defer zero(caBundle)
	defer zero(accessKey)
	defer zero(secretKey)
	cloud := append([]byte("[default]\naws_access_key_id="), accessKey...)
	cloud = append(cloud, "\naws_secret_access_key="...)
	cloud = append(cloud, secretKey...)
	cloud = append(cloud, '\n')
	defer zero(cloud)

	manager, err := addon.NewManager(filepath.Join("..", "..", "deploy", "charts"))
	if err != nil {
		t.Fatalf("initialize Helm manager: %v", err)
	}
	cluster := kubernetes.NewClient(30 * time.Second)
	cr := veleroadapter.NewClient(30 * time.Second)
	installAcceptanceVelero(t, ctx, manager, cluster, cr, targetConfig, cloud, caBundle, endpoint, "target")
	installAcceptanceVelero(t, ctx, manager, cluster, cr, sourceConfig, cloud, caBundle, endpoint, "source")

	prepared, err := cluster.Prepare(sourceConfig)
	if err != nil {
		t.Fatalf("prepare source kubeconfig: %v", err)
	}
	clientset, err := kubernetesclient.NewForConfig(prepared.Config)
	if err != nil {
		t.Fatalf("create source client: %v", err)
	}
	if err := cluster.EnsureNamespace(ctx, sourceConfig, acceptanceTestNS); err != nil {
		t.Fatalf("ensure source test namespace: %v", err)
	}
	sentinel := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "migration-sentinel"},
		Data:       map[string]string{"value": "velero-acceptance"},
	}
	_, err = clientset.CoreV1().ConfigMaps(acceptanceTestNS).Create(ctx, sentinel, metav1.CreateOptions{})
	if err != nil {
		existing, getErr := clientset.CoreV1().ConfigMaps(acceptanceTestNS).Get(ctx, sentinel.Name, metav1.GetOptions{})
		if getErr != nil {
			t.Fatalf("read existing source sentinel: %v", getErr)
		}
		existing.Data = sentinel.Data
		_, err = clientset.CoreV1().ConfigMaps(acceptanceTestNS).Update(ctx, existing, metav1.UpdateOptions{})
	}
	if err != nil {
		t.Fatalf("write source sentinel: %v", err)
	}
	backupName := fmt.Sprintf("migration-acceptance-%d", time.Now().UTC().Unix())
	status, err := cr.CreateBackup(ctx, sourceConfig, veleroadapter.BackupSpec{
		Namespace: veleroservice.Namespace, Name: backupName, StorageLocation: veleroservice.BackupLocationName,
		IncludedNamespaces: []string{acceptanceTestNS}, PlanID: "acceptance-plan", RunID: backupName, TTL: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("create source backup: %v", err)
	}
	deadline := time.NewTicker(3 * time.Second)
	defer deadline.Stop()
	for status.Phase != "Completed" {
		if status.Phase == "Failed" || status.Phase == "PartiallyFailed" {
			t.Fatalf("source backup ended in %s: errors=%d warnings=%d message=%s", status.Phase, status.Errors, status.Warnings, status.Message)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for source backup: %v", ctx.Err())
		case <-deadline.C:
			status, err = cr.BackupStatus(ctx, sourceConfig, veleroservice.Namespace, backupName)
			if err != nil {
				t.Fatalf("read source backup: %v", err)
			}
		}
	}
	t.Logf("Velero acceptance passed: target BSL=Available source BSL=Available backup=%s phase=%s items=%d/%d",
		status.Name, status.Phase, status.ItemsBackedUp, status.TotalItems)
}

func installAcceptanceVelero(t *testing.T, ctx context.Context, manager *addon.Manager, cluster *kubernetes.Client, cr *veleroadapter.Client, kubeconfig, cloud, caBundle []byte, endpoint, prefix string) {
	t.Helper()
	if err := cluster.EnsureAddonNamespace(ctx, kubeconfig, veleroservice.Namespace); err != nil {
		t.Fatalf("ensure %s Velero namespace: %v", prefix, err)
	}
	if err := cluster.PutOpaqueSecret(ctx, kubeconfig, veleroservice.Namespace, veleroservice.CredentialSecretName, map[string][]byte{veleroservice.CredentialSecretKey: cloud}); err != nil {
		t.Fatalf("write %s Velero credential Secret: %v", prefix, err)
	}
	veleroImage := envOrDefault("TEST_VELERO_IMAGE", acceptanceVeleroImage)
	pluginImage := envOrDefault("TEST_VELERO_AWS_PLUGIN_IMAGE", acceptanceAWSPlugin)
	veleroRepo, veleroDigest := splitPinnedImage(t, veleroImage)
	_, err := manager.InstallOrUpgrade(ctx, append([]byte(nil), kubeconfig...), addon.InstallRequest{
		ReleaseName: veleroservice.ReleaseName, Namespace: veleroservice.Namespace,
		ChartPath: veleroservice.OfficialChartArtifact, Version: veleroservice.OfficialChartVersion,
		CreateNamespace: false, Timeout: 25 * time.Minute,
		Values: map[string]any{
			"image":       map[string]any{"repository": veleroRepo, "digest": veleroDigest},
			"credentials": map[string]any{"useSecret": true, "existingSecret": veleroservice.CredentialSecretName},
			"initContainers": []any{map[string]any{
				"name": "velero-plugin-for-aws", "image": pluginImage, "imagePullPolicy": "IfNotPresent",
				"volumeMounts": []any{map[string]any{"mountPath": "/target", "name": "plugins"}},
			}},
			"configuration":    map[string]any{"backupStorageLocation": []any{}, "defaultVolumesToFsBackup": true, "uploaderType": "kopia", "features": "EnableCSI"},
			"snapshotsEnabled": false, "deployNodeAgent": true,
			"nodeAgent": map[string]any{
				"podVolumePath": "/var/lib/kubelet/pods", "pluginVolumePath": "/var/lib/kubelet/plugins",
				"containerSecurityContext": map[string]any{"privileged": true},
			},
		},
	})
	if err != nil {
		t.Fatalf("install %s Velero: %v", prefix, err)
	}
	status, err := cr.EnsureBackupStorageLocation(ctx, kubeconfig, veleroadapter.BackupStorageLocationSpec{
		Namespace: veleroservice.Namespace, Name: veleroservice.BackupLocationName, Provider: "aws",
		Bucket: "velero", Prefix: "acceptance/" + prefix, Region: "minio", Endpoint: endpoint,
		CredentialSecret: veleroservice.CredentialSecretName, CredentialKey: veleroservice.CredentialSecretKey, CABundle: caBundle,
	})
	if err != nil {
		t.Fatalf("create %s BSL: %v", prefix, err)
	}
	for status.Phase != "Available" {
		if status.Phase == "Unavailable" {
			t.Fatalf("%s BSL is unavailable: %s", prefix, status.Message)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for %s BSL: %v", prefix, ctx.Err())
		case <-time.After(2 * time.Second):
			status, err = cr.BackupStorageLocationStatus(ctx, kubeconfig, veleroservice.Namespace, veleroservice.BackupLocationName)
			if err != nil {
				t.Fatalf("read %s BSL: %v", prefix, err)
			}
		}
	}
}

func minIOConnection(t *testing.T, ctx context.Context, targetKubeconfig []byte) (string, []byte, []byte, []byte) {
	t.Helper()
	cluster := kubernetes.NewClient(30 * time.Second)
	prepared, err := cluster.Prepare(targetKubeconfig)
	if err != nil {
		t.Fatalf("prepare target kubeconfig: %v", err)
	}
	clientset, err := kubernetesclient.NewForConfig(prepared.Config)
	if err != nil {
		t.Fatalf("create target client: %v", err)
	}
	service, err := clientset.CoreV1().Services(acceptanceNamespace).Get(ctx, acceptanceWorkload, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("read target MinIO Service: %v", err)
	}
	var port int32
	for _, value := range service.Spec.Ports {
		if value.Name == "s3" {
			port = value.NodePort
		}
	}
	if port == 0 {
		t.Fatal("target MinIO does not expose the S3 NodePort")
	}
	credentials, err := clientset.CoreV1().Secrets(acceptanceNamespace).Get(ctx, "sks-migration-minio-root", metav1.GetOptions{})
	if err != nil || len(credentials.Data["access-key"]) == 0 || len(credentials.Data["secret-key"]) == 0 {
		t.Fatalf("read target MinIO credentials: %v", err)
	}
	tlsSecret, err := clientset.CoreV1().Secrets(acceptanceNamespace).Get(ctx, "sks-migration-minio-tls", metav1.GetOptions{})
	if err != nil || len(tlsSecret.Data["public.crt"]) == 0 {
		t.Fatalf("read target MinIO CA: %v", err)
	}
	nodeIPs := discoverNodeIPs(ctx, t, clientset)
	return fmt.Sprintf("https://%s:%d", nodeIPs[0].String(), port),
		append([]byte(nil), tlsSecret.Data["public.crt"]...),
		append([]byte(nil), credentials.Data["access-key"]...),
		append([]byte(nil), credentials.Data["secret-key"]...)
}

func readSensitiveFile(t *testing.T, path string) []byte {
	t.Helper()
	value, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read acceptance kubeconfig: %v", err)
	}
	return value
}

func splitPinnedImage(t *testing.T, value string) (string, string) {
	t.Helper()
	for index := len(value) - 1; index >= 0; index-- {
		if value[index] == '@' {
			return value[:index], value[index+1:]
		}
	}
	t.Fatalf("acceptance image is not digest pinned")
	return "", ""
}
