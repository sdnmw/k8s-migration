package acceptance

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubernetesclient "k8s.io/client-go/kubernetes"

	"github.com/smartx/sks-migration-center/internal/adapter/kubernetes"
)

// TestKindDualClusterControlPath exercises the same apply, readiness, quiesce
// and rollback adapters used by the worker against two real API servers. The
// Velero/Kopia data plane remains covered by its dedicated acceptance suites.
func TestKindDualClusterControlPath(t *testing.T) {
	sourcePath, targetPath := os.Getenv("TEST_KIND_SOURCE_KUBECONFIG"), os.Getenv("TEST_KIND_TARGET_KUBECONFIG")
	if sourcePath == "" || targetPath == "" {
		t.Skip("TEST_KIND_SOURCE_KUBECONFIG and TEST_KIND_TARGET_KUBECONFIG are not set")
	}
	image := os.Getenv("TEST_KIND_WORKLOAD_IMAGE")
	if image == "" {
		image = "docker.io/library/redis:8-alpine"
	}
	port := 6379
	if raw := os.Getenv("TEST_KIND_WORKLOAD_PORT"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 65535 {
			t.Fatalf("TEST_KIND_WORKLOAD_PORT must be between 1 and 65535")
		}
		port = parsed
	}
	sourceConfig, targetConfig := readSensitiveFile(t, sourcePath), readSensitiveFile(t, targetPath)
	defer zero(sourceConfig)
	defer zero(targetConfig)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	client := kubernetes.NewClient(30 * time.Second)
	namespace := "smc-kind-" + uuid.NewString()[:8]
	for _, item := range []struct {
		name       string
		kubeconfig []byte
	}{{"source", sourceConfig}, {"target", targetConfig}} {
		prepared, err := client.Prepare(item.kubeconfig)
		if err != nil {
			t.Fatalf("prepare %s cluster: %v", item.name, err)
		}
		clientset, err := kubernetesclient.NewForConfig(prepared.Config)
		if err != nil {
			t.Fatalf("create %s client: %v", item.name, err)
		}
		t.Cleanup(func() {
			_ = clientset.CoreV1().Namespaces().Delete(context.Background(), namespace, metav1.DeleteOptions{})
		})
	}

	manifest := dualKindManifest(namespace, image, port)
	started := time.Now()
	if _, err := client.ApplyManifests(ctx, sourceConfig, kubernetes.ApplyManifestSpec{DefaultNamespace: namespace, Manifests: manifest}); err != nil {
		t.Fatalf("apply source fixture: %v", err)
	}
	if _, err := client.ApplyManifests(ctx, targetConfig, kubernetes.ApplyManifestSpec{DefaultNamespace: namespace, Manifests: manifest}); err != nil {
		t.Fatalf("apply target fixture: %v", err)
	}
	waitForKindDeployment(t, ctx, client, sourceConfig, namespace)
	waitForKindDeployment(t, ctx, client, targetConfig, namespace)

	sourceWorkloads, err := client.ListScalableWorkloads(ctx, sourceConfig, namespace)
	if err != nil || len(sourceWorkloads) != 1 || sourceWorkloads[0].Replicas != 1 {
		t.Fatalf("source workload snapshot: %+v err=%v", sourceWorkloads, err)
	}
	quiesced := append([]kubernetes.ScalableWorkload(nil), sourceWorkloads...)
	quiesced[0].Replicas = 0
	if err := client.ScaleWorkloads(ctx, sourceConfig, quiesced); err != nil {
		t.Fatalf("quiesce source: %v", err)
	}
	if err := client.ScaleWorkloads(ctx, sourceConfig, sourceWorkloads); err != nil {
		t.Fatalf("rollback source: %v", err)
	}
	waitForKindDeployment(t, ctx, client, sourceConfig, namespace)

	prepared, _ := client.Prepare(targetConfig)
	targetClient, _ := kubernetesclient.NewForConfig(prepared.Config)
	configMap, err := targetClient.CoreV1().ConfigMaps(namespace).Get(ctx, "migration-sentinel", metav1.GetOptions{})
	if err != nil || configMap.Data["value"] != "kind-dual-cluster" {
		t.Fatalf("target ConfigMap fidelity failed: value=%q err=%v", configMap.Data["value"], err)
	}
	secret, err := targetClient.CoreV1().Secrets(namespace).Get(ctx, "migration-secret", metav1.GetOptions{})
	if err != nil || string(secret.Data["value"]) != "kind-secret" {
		t.Fatalf("target Secret fidelity failed: err=%v", err)
	}
	t.Logf("dual-cluster control-path E2E passed in %s", time.Since(started).Round(time.Millisecond))
}

func waitForKindDeployment(t *testing.T, ctx context.Context, client *kubernetes.Client, kubeconfig []byte, namespace string) {
	t.Helper()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		result, err := client.ValidateNamespace(ctx, kubeconfig, namespace)
		if err == nil && result.Deployments == 1 && result.Services == 1 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for %s readiness: %v last=%+v", namespace, ctx.Err(), result)
		case <-ticker.C:
		}
	}
}

func dualKindManifest(namespace, image string, port int) []byte {
	return []byte(fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata: {name: migration-sentinel, namespace: %s}
data: {value: kind-dual-cluster}
---
apiVersion: v1
kind: Secret
metadata: {name: migration-secret, namespace: %s}
type: Opaque
stringData: {value: kind-secret}
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: redis, namespace: %s}
spec:
  replicas: 1
  selector: {matchLabels: {app: redis}}
  template:
    metadata: {labels: {app: redis}}
    spec:
      securityContext:
        seccompProfile: {type: RuntimeDefault}
      containers:
        - name: redis
          image: %s
          imagePullPolicy: IfNotPresent
          securityContext:
            allowPrivilegeEscalation: false
            capabilities: {drop: [ALL]}
            runAsNonRoot: true
          ports: [{name: probe, containerPort: %d}]
          readinessProbe: {tcpSocket: {port: probe}, periodSeconds: 1}
---
apiVersion: v1
kind: Service
metadata: {name: redis, namespace: %s}
spec:
  selector: {app: redis}
  ports: [{name: probe, port: %d, targetPort: probe}]
`, namespace, namespace, namespace, image, port, namespace, port))
}
