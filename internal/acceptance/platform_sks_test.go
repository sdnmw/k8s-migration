package acceptance

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	kubernetesclient "k8s.io/client-go/kubernetes"

	kubernetesadapter "github.com/smartx/sks-migration-center/internal/adapter/kubernetes"
	"github.com/smartx/sks-migration-center/internal/addon"
)

const (
	platformNamespace = "sks-migration-center"
	platformRelease   = "sks-migration-center"
	platformSecret    = "sks-migration-center-secrets"

	platformAPIDigest      = "sha256:8909bb2d1e2d431c30fb4bcfa2ea70efa8fd84a6d3b91eb25b157e682b79f744"
	platformWorkerDigest   = "sha256:fba7e8deb41722b5b9b4a2fd4f11c7e91a66fdf32caae1fea8c5d359aa472381"
	platformWebDigest      = "sha256:a4443cee1da8bd209cf02fa7b730d9885270b89fa40e7f8354c26c0219f39f5a"
	platformPostgresDigest = "sha256:8264ed299ea76e62f986e69773bfba69a16d901904fe1b302c4db88fed69f5e8"
	platformKomposeDigest  = "sha256:4c9475f96461a86eb5c65dadfdbdb29c3ddcd864db8c6a68115a60276682e5d7"
	platformKopiaDigest    = "sha256:6f124e918e1128d19e68518bf48477073b9de12f58dbb212ef29c09d14021347"
)

// TestPlatformOnSKS installs or upgrades the complete API, worker, web and
// PostgreSQL platform on a real SKS workload cluster. It verifies the SmartX
// block PVC, all workloads and the browser-facing login path. The release and
// PVC are intentionally left installed for use after the acceptance run.
func TestPlatformOnSKS(t *testing.T) {
	kubeconfigPath := strings.TrimSpace(os.Getenv("TEST_SKS_KUBECONFIG"))
	adminPasswordPath := strings.TrimSpace(os.Getenv("TEST_PLATFORM_ADMIN_PASSWORD_FILE"))
	masterKeyPath := strings.TrimSpace(os.Getenv("TEST_PLATFORM_MASTER_KEY_FILE"))
	if kubeconfigPath == "" || adminPasswordPath == "" || masterKeyPath == "" {
		t.Skip("TEST_SKS_KUBECONFIG, TEST_PLATFORM_ADMIN_PASSWORD_FILE and TEST_PLATFORM_MASTER_KEY_FILE are required")
	}

	kubeconfig := readSensitiveFile(t, kubeconfigPath)
	adminPassword := readTrimmedSecret(t, adminPasswordPath)
	masterKey := readTrimmedSecret(t, masterKeyPath)
	defer zero(kubeconfig)
	defer zero(adminPassword)
	defer zero(masterKey)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	cluster := kubernetesadapter.NewClient(30 * time.Second)
	prepared, err := cluster.Prepare(kubeconfig)
	if err != nil {
		t.Fatalf("prepare target kubeconfig: %v", err)
	}
	clientset, err := kubernetesclient.NewForConfig(prepared.Config)
	if err != nil {
		t.Fatalf("create target Kubernetes client: %v", err)
	}
	if err := cluster.EnsureNamespace(ctx, kubeconfig, platformNamespace); err != nil {
		t.Fatalf("ensure platform namespace: %v", err)
	}
	secretData := ensurePlatformSecret(t, ctx, cluster, clientset, kubeconfig, adminPassword, masterKey)
	defer clearSecretData(secretData)

	manager, err := addon.NewManager(filepath.Join("..", "..", "deploy", "charts"))
	if err != nil {
		t.Fatalf("initialize Helm SDK manager: %v", err)
	}
	state, err := manager.InstallOrUpgrade(ctx, append([]byte(nil), kubeconfig...), addon.InstallRequest{
		ReleaseName: platformRelease,
		Namespace:   platformNamespace,
		ChartPath:   "sks-migration-center",
		Version:     "0.1.0",
		Timeout:     20 * time.Minute,
		Values:      platformValues(),
	})
	if err != nil {
		dumpPlatformStatus(ctx, t, clientset)
		t.Fatalf("install platform with Helm SDK: %v", err)
	}
	if state.Status != "deployed" {
		t.Fatalf("platform release status = %q, want deployed", state.Status)
	}

	waitForPlatformReady(ctx, t, clientset)
	pvc, err := clientset.CoreV1().PersistentVolumeClaims(platformNamespace).Get(ctx, "data-sks-migration-center-postgres-0", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("read PostgreSQL PVC: %v", err)
	}
	if pvc.Status.Phase != corev1.ClaimBound || ptrValue(pvc.Spec.StorageClassName) != "smtx-elf-csi-driver" {
		t.Fatalf("PostgreSQL PVC phase=%s storageClass=%q, want Bound on smtx-elf-csi-driver", pvc.Status.Phase, ptrValue(pvc.Spec.StorageClassName))
	}

	service, err := clientset.CoreV1().Services(platformNamespace).Get(ctx, platformRelease, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("read platform Service: %v", err)
	}
	nodePort := serviceNodePort(service, "http")
	if nodePort == 0 {
		t.Fatal("platform Service does not expose an HTTP NodePort")
	}
	nodeIPs := discoverNodeIPs(ctx, t, clientset)
	baseURL := fmt.Sprintf("http://%s:%d", nodeIPs[0].String(), nodePort)
	verifyPlatformLogin(ctx, t, baseURL, adminPassword)

	t.Logf("platform acceptance passed: release=%s revision=%d pods=4/4 PVC=%s storageClass=%s login=passed URL=%s",
		state.Name, state.Revision, pvc.Name, ptrValue(pvc.Spec.StorageClassName), baseURL)
}

func ensurePlatformSecret(t *testing.T, ctx context.Context, cluster *kubernetesadapter.Client, clientset kubernetesclient.Interface, kubeconfig, adminPassword, masterKey []byte) map[string][]byte {
	t.Helper()
	existing, err := clientset.CoreV1().Secrets(platformNamespace).Get(ctx, platformSecret, metav1.GetOptions{})
	if err == nil {
		for _, key := range []string{"admin-password", "master-key", "postgres-password", "database-url"} {
			if len(existing.Data[key]) == 0 {
				t.Fatalf("existing platform Secret is missing %q; refusing a partial credential rotation", key)
			}
		}
		return cloneBytesMap(existing.Data)
	}
	if !apierrors.IsNotFound(err) {
		t.Fatalf("read existing platform Secret: %v", err)
	}
	postgresPassword := make([]byte, 32)
	if _, err := rand.Read(postgresPassword); err != nil {
		t.Fatalf("generate PostgreSQL password: %v", err)
	}
	encodedPostgresPassword := []byte(base64.RawURLEncoding.EncodeToString(postgresPassword))
	zero(postgresPassword)
	data := map[string][]byte{
		"admin-password":    append([]byte(nil), adminPassword...),
		"master-key":        append([]byte(nil), masterKey...),
		"postgres-password": encodedPostgresPassword,
		"database-url": []byte(fmt.Sprintf(
			"postgres://migration:%s@sks-migration-center-postgres:5432/migration?sslmode=disable", encodedPostgresPassword,
		)),
	}
	if err := cluster.PutOpaqueSecret(ctx, kubeconfig, platformNamespace, platformSecret, data); err != nil {
		clearSecretData(data)
		t.Fatalf("create platform Secret: %v", err)
	}
	return data
}

func platformValues() map[string]any {
	return map[string]any{
		"global": map[string]any{"storageClass": "smtx-elf-csi-driver"},
		"image": map[string]any{
			"api":        pinnedValue("192.168.112.30/sks/sks-migration-api", platformAPIDigest),
			"worker":     pinnedValue("192.168.112.30/sks/sks-migration-worker", platformWorkerDigest),
			"web":        pinnedValue("192.168.112.30/sks/sks-migration-web", platformWebDigest),
			"postgres":   pinnedValue("192.168.112.30/sks/sks-migration-postgres", platformPostgresDigest),
			"pullPolicy": "IfNotPresent",
		},
		"addonImages": map[string]any{
			"minio":           pinnedValue("192.168.112.30/sks/minio", "sha256:ba0d540656240f346fd9fee842516687782cb51ad1282064ef64dd7c725f08cc"),
			"velero":          pinnedValue("m.daocloud.io/docker.io/velero/velero", "sha256:11459094b1b21ec7c817b08f8067d9e89380835547915cac9c4132ff05b55b90"),
			"veleroAWSPlugin": pinnedValue("m.daocloud.io/docker.io/velero/velero-plugin-for-aws", "sha256:7e82f717f44e89671212e0dfce7e061321c386ea84a33bca64a671670ca6c278"),
			"nfsPlugin":       pinnedValue("m.daocloud.io/registry.k8s.io/sig-storage/nfsplugin", "sha256:1eb5a85180a4ad0193a31d319b163f35c8c1857794ebaac71d8abcdd5a0516d3"),
			"nfsProvisioner":  pinnedValue("m.daocloud.io/registry.k8s.io/sig-storage/csi-provisioner", "sha256:a4b0b1a37605b7b04a293e136edf7006ec1786a8eb3f4e5a945f81d667dcc371"),
			"nfsResizer":      pinnedValue("m.daocloud.io/registry.k8s.io/sig-storage/csi-resizer", "sha256:a2d40c1c3ccb0c48b467125a6652c4dd5dcbf0d295641c9989581cfc690f6cf3"),
			"nfsLiveness":     pinnedValue("m.daocloud.io/registry.k8s.io/sig-storage/livenessprobe", "sha256:06da0d5b8908072f2e4522692aee8dc119fba7247a9658497e1153992cd777e9"),
			"nfsRegistrar":    pinnedValue("m.daocloud.io/registry.k8s.io/sig-storage/csi-node-driver-registrar", "sha256:f9de845b170155199f2a2a3f9531cf13d78e31235e9db6b6582a8b0db0a50dad"),
			"nfsProbe":        pinnedValue("m.daocloud.io/docker.io/library/busybox", "sha256:73aaf090f3d85aa34ee199857f03fa3a95c8ede2ffd4cc2cdb5b94e566b11662"),
			"kompose":         pinnedValue("192.168.112.30/sks/sks-migration-kompose", platformKomposeDigest),
			"kopia":           pinnedValue("192.168.112.30/sks/sks-migration-kopia", platformKopiaDigest),
		},
		"service":       map[string]any{"type": "NodePort", "port": 80},
		"config":        map[string]any{"cookieSecure": false},
		"networkPolicy": map[string]any{"enabled": true},
	}
}

func pinnedValue(repository, digest string) map[string]any {
	return map[string]any{"repository": repository, "digest": digest}
}

func waitForPlatformReady(ctx context.Context, t *testing.T, clientset kubernetesclient.Interface) {
	t.Helper()
	err := wait.PollUntilContextTimeout(ctx, 3*time.Second, 20*time.Minute, true, func(ctx context.Context) (bool, error) {
		deployments, err := clientset.AppsV1().Deployments(platformNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: "app.kubernetes.io/instance=" + platformRelease,
		})
		if err != nil {
			return false, nil
		}
		readyDeployments := 0
		for index := range deployments.Items {
			deployment := &deployments.Items[index]
			if deploymentReady(deployment) {
				readyDeployments++
			}
		}
		statefulSet, err := clientset.AppsV1().StatefulSets(platformNamespace).Get(ctx, "sks-migration-center-postgres", metav1.GetOptions{})
		return err == nil && readyDeployments == 3 && statefulSet.Status.ReadyReplicas == 1, nil
	})
	if err != nil {
		dumpPlatformStatus(ctx, t, clientset)
		t.Fatalf("wait for platform workloads: %v", err)
	}
}

func deploymentReady(value *appsv1.Deployment) bool {
	desired := int32(1)
	if value.Spec.Replicas != nil {
		desired = *value.Spec.Replicas
	}
	return value.Status.ObservedGeneration >= value.Generation && value.Status.ReadyReplicas == desired && value.Status.UpdatedReplicas == desired
}

func dumpPlatformStatus(ctx context.Context, t *testing.T, clientset kubernetesclient.Interface) {
	t.Helper()
	pods, err := clientset.CoreV1().Pods(platformNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Logf("platform pod diagnostics unavailable: %v", err)
		return
	}
	for index := range pods.Items {
		pod := &pods.Items[index]
		t.Logf("platform pod status: name=%s phase=%s reason=%s message=%s", pod.Name, pod.Status.Phase, pod.Status.Reason, pod.Status.Message)
		for _, status := range pod.Status.ContainerStatuses {
			if status.State.Waiting != nil {
				t.Logf("container waiting: pod=%s container=%s reason=%s message=%s", pod.Name, status.Name, status.State.Waiting.Reason, status.State.Waiting.Message)
			}
		}
	}
}

func serviceNodePort(service *corev1.Service, name string) int32 {
	for _, port := range service.Spec.Ports {
		if port.Name == name {
			return port.NodePort
		}
	}
	return 0
}

func verifyPlatformLogin(ctx context.Context, t *testing.T, baseURL string, adminPassword []byte) {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create HTTP cookie jar: %v", err)
	}
	client := &http.Client{Timeout: 20 * time.Second, Jar: jar}
	body, err := json.Marshal(map[string]string{"username": "admin", "password": string(adminPassword)})
	if err != nil {
		t.Fatalf("encode login request: %v", err)
	}
	defer zero(body)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/v1/auth/login", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create login request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("call platform login: %v", err)
	}
	loginResponse := readLimitedResponse(t, response)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("platform login status=%d body=%s", response.StatusCode, loginResponse)
	}

	request, err = http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/v1/auth/me", nil)
	if err != nil {
		t.Fatalf("create current-user request: %v", err)
	}
	response, err = client.Do(request)
	if err != nil {
		t.Fatalf("call current-user API: %v", err)
	}
	meResponse := readLimitedResponse(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("current-user status=%d body=%s", response.StatusCode, meResponse)
	}
	var administrator struct {
		Username string `json:"username"`
	}
	if err := json.Unmarshal([]byte(meResponse), &administrator); err != nil || administrator.Username != "admin" {
		t.Fatalf("unexpected current-user response")
	}
}

func readLimitedResponse(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	value, err := io.ReadAll(io.LimitReader(response.Body, 4<<10))
	if err != nil {
		t.Fatalf("read HTTP response: %v", err)
	}
	return string(value)
}

func readTrimmedSecret(t *testing.T, path string) []byte {
	t.Helper()
	value, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read acceptance secret file: %v", err)
	}
	trimmed := []byte(strings.TrimSpace(string(value)))
	zero(value)
	if len(trimmed) == 0 {
		t.Fatal("acceptance secret file is empty")
	}
	return trimmed
}

func cloneBytesMap(source map[string][]byte) map[string][]byte {
	result := make(map[string][]byte, len(source))
	for key, value := range source {
		result[key] = append([]byte(nil), value...)
	}
	return result
}

func clearSecretData(data map[string][]byte) {
	for key := range data {
		zero(data[key])
	}
}
