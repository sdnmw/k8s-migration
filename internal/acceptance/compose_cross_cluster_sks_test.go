package acceptance

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kubernetesclient "k8s.io/client-go/kubernetes"

	kubernetesadapter "github.com/smartx/sks-migration-center/internal/adapter/kubernetes"
	sshadapter "github.com/smartx/sks-migration-center/internal/adapter/ssh"
	domainmapping "github.com/smartx/sks-migration-center/internal/domain/mapping"
	"github.com/smartx/sks-migration-center/internal/transform"
)

const (
	composeAcceptanceKomposeImage     = "192.168.112.30/sks/sks-migration-kompose@sha256:4c9475f96461a86eb5c65dadfdbdb29c3ddcd864db8c6a68115a60276682e5d7"
	composeAcceptanceTargetKopiaImage = "192.168.112.30/sks/sks-migration-kopia@sha256:6f124e918e1128d19e68518bf48477073b9de12f58dbb212ef29c09d14021347"
	composeAcceptanceSourceKopiaImage = "192.168.112.28/sida/sks-migration-kopia@sha256:6f124e918e1128d19e68518bf48477073b9de12f58dbb212ef29c09d14021347"
)

// TestComposeKopiaCrossClusterOnSKS exercises the real Compose data path:
// controlled SSH lifecycle, isolated Kompose conversion, online and stopped
// Kopia snapshots, SmartX block-PVC restore, workload start and data validation.
// It is opt-in because it writes to an SSH Docker host, MinIO and a real SKS
// workload cluster. The test removes only its randomly named source project and
// target namespace.
func TestComposeKopiaCrossClusterOnSKS(t *testing.T) {
	targetPath := os.Getenv("TEST_SKS_TARGET_KUBECONFIG")
	sshEndpoint := os.Getenv("TEST_COMPOSE_SSH_ENDPOINT")
	sshPassword := os.Getenv("TEST_COMPOSE_SSH_PASSWORD")
	sshFingerprint := os.Getenv("TEST_COMPOSE_SSH_HOST_KEY_FINGERPRINT")
	if targetPath == "" || sshEndpoint == "" || sshPassword == "" || sshFingerprint == "" {
		t.Skip("TEST_SKS_TARGET_KUBECONFIG and Compose SSH acceptance variables are not set")
	}
	sshUsername := os.Getenv("TEST_COMPOSE_SSH_USERNAME")
	if sshUsername == "" {
		sshUsername = "root"
	}
	komposeImage := envOrDefault("TEST_COMPOSE_KOMPOSE_IMAGE", composeAcceptanceKomposeImage)
	targetKopiaImage := envOrDefault("TEST_COMPOSE_KOPIA_IMAGE", composeAcceptanceTargetKopiaImage)
	sourceKopiaImage := envOrDefault("TEST_COMPOSE_SOURCE_KOPIA_IMAGE", composeAcceptanceSourceKopiaImage)
	targetConfig := readSensitiveFile(t, targetPath)
	defer zero(targetConfig)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	cluster := kubernetesadapter.NewClient(30 * time.Second)
	prepared, err := cluster.Prepare(targetConfig)
	if err != nil {
		t.Fatalf("prepare target kubeconfig: %v", err)
	}
	clientset, err := kubernetesclient.NewForConfig(prepared.Config)
	if err != nil {
		t.Fatalf("create target Kubernetes client: %v", err)
	}
	repository := composeAcceptanceRepository(ctx, t, clientset, sourceKopiaImage)

	suffix := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	runID := "compose-" + suffix
	project := "smccompose" + suffix
	namespace := "smc-compose-" + suffix
	sentinel := "sks-compose-e2e-" + suffix
	composeYAML := composeAcceptanceDefinition(sourceKopiaImage, sentinel)
	sshCredential := sshadapter.Credential{
		Username: sshUsername, Password: sshPassword, HostKeyFingerprint: sshFingerprint,
	}
	sshClient := sshadapter.NewClient(30 * time.Second)
	started := false
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cleanupCancel()
		if started {
			if cleanupErr := sshClient.RunComposeAction(cleanupCtx, sshEndpoint, sshCredential, sshadapter.ComposeActionSpec{
				ProjectName: project, ComposeYAML: composeYAML, Action: sshadapter.ComposeRemove,
			}); cleanupErr != nil {
				t.Errorf("remove source Compose acceptance project: %v", cleanupErr)
			}
		}
	})
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cleanupCancel()
		_ = clientset.CoreV1().Namespaces().Delete(cleanupCtx, namespace, metav1.DeleteOptions{})
	})

	if err := sshClient.RunComposeAction(ctx, sshEndpoint, sshCredential, sshadapter.ComposeActionSpec{
		ProjectName: project, ComposeYAML: composeYAML, Action: sshadapter.ComposeStart,
	}); err != nil {
		t.Fatalf("start source Compose fixture: %v", err)
	}
	started = true

	time.Sleep(time.Second)
	preSnapshot, err := sshClient.CreateKopiaSnapshot(ctx, sshEndpoint, sshCredential, sshadapter.KopiaSnapshotSpec{
		RunID: runID, Repository: repository, Source: sshadapter.KopiaSource{Name: "payload", Type: "volume", Path: project + "_payload"},
	})
	if err != nil {
		t.Fatalf("create online Kopia pre-sync snapshot: %v", err)
	}

	converted, err := cluster.RunKomposeJob(ctx, targetConfig, kubernetesadapter.KomposeJobSpec{
		SystemNamespace: "sks-migration-system", TargetNamespace: namespace, RunID: runID,
		ComposeYAML: composeYAML, KomposeImage: komposeImage, HelperImage: targetKopiaImage,
	})
	if err != nil {
		t.Fatalf("run isolated Kompose conversion: %v", err)
	}
	time.Sleep(16 * time.Second)
	if err := sshClient.RunComposeAction(ctx, sshEndpoint, sshCredential, sshadapter.ComposeActionSpec{
		ProjectName: project, ComposeYAML: composeYAML, Action: sshadapter.ComposeStop,
	}); err != nil {
		t.Fatalf("stop source Compose fixture: %v", err)
	}
	finalSnapshot, err := sshClient.CreateKopiaSnapshot(ctx, sshEndpoint, sshCredential, sshadapter.KopiaSnapshotSpec{
		RunID: runID, Repository: repository, Source: sshadapter.KopiaSource{Name: "payload", Type: "volume", Path: project + "_payload"},
	})
	if err != nil {
		t.Fatalf("create stopped-source final Kopia snapshot: %v", err)
	}
	if preSnapshot.ID == finalSnapshot.ID || finalSnapshot.SizeBytes < preSnapshot.SizeBytes {
		t.Fatalf("final snapshot did not capture an incremental source change: pre=%+v final=%+v", preSnapshot, finalSnapshot)
	}

	engine := transform.NewEngine()
	transformed, err := engine.Transform(converted, domainmapping.Profile{Registries: []domainmapping.KeyValue{{
		Source: "192.168.112.28/sida", Target: "192.168.112.30/sks",
	}}})
	if err != nil {
		t.Fatalf("transform Kompose manifests: %v", err)
	}
	workloadName, pvcName := pauseComposeAcceptanceManifests(t, &transformed)
	manifests, err := engine.Render(transformed)
	if err != nil {
		t.Fatalf("render transformed Kompose manifests: %v", err)
	}
	if _, err := cluster.ApplyManifests(ctx, targetConfig, kubernetesadapter.ApplyManifestSpec{
		DefaultNamespace: namespace, Manifests: manifests,
	}); err != nil {
		t.Fatalf("apply paused Compose manifests: %v", err)
	}
	if err := cluster.RunKopiaRestoreJob(ctx, targetConfig, kubernetesadapter.KopiaRestoreSpec{
		Namespace: namespace, RunID: runID, PVC: pvcName, SnapshotID: finalSnapshot.ID,
		Image: targetKopiaImage, Endpoint: repository.Endpoint, Bucket: repository.Bucket,
		Region: repository.Region, Prefix: repository.Prefix, AccessKey: repository.AccessKey,
		SecretKey: repository.SecretKey, TLSVerify: repository.TLSVerify, Password: repository.Password,
		Timeout: 10 * time.Minute,
	}); err != nil {
		t.Fatalf("restore final Kopia snapshot to SmartX block PVC: %v", err)
	}
	if err := cluster.ScaleWorkloads(ctx, targetConfig, []kubernetesadapter.ScalableWorkload{{
		Namespace: namespace, Kind: "Deployment", Name: workloadName, Replicas: 1,
	}}); err != nil {
		t.Fatalf("start restored target workload: %v", err)
	}
	waitForComposeAcceptance(t, ctx, cluster, clientset, targetConfig, namespace, sentinel)

	if err := sshClient.RunComposeAction(ctx, sshEndpoint, sshCredential, sshadapter.ComposeActionSpec{
		ProjectName: project, ComposeYAML: composeYAML, Action: sshadapter.ComposeStart,
	}); err != nil {
		t.Fatalf("restart source Compose fixture for rollback proof: %v", err)
	}
	t.Logf("Compose to SKS E2E passed: project=%s namespace=%s preBytes=%d finalBytes=%d targetPVC=%s rollback=passed",
		project, namespace, preSnapshot.SizeBytes, finalSnapshot.SizeBytes, pvcName)
}

func composeAcceptanceRepository(ctx context.Context, t *testing.T, clientset kubernetesclient.Interface, image string) sshadapter.KopiaRepository {
	t.Helper()
	service, err := clientset.CoreV1().Services("sks-migration-system").Get(ctx, "sks-migration-minio", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("read managed MinIO service: %v", err)
	}
	var nodePort int32
	for _, port := range service.Spec.Ports {
		if port.Name == "s3" {
			nodePort = port.NodePort
		}
	}
	if nodePort == 0 {
		t.Fatal("managed MinIO does not expose the S3 NodePort")
	}
	nodeIPs := discoverNodeIPs(ctx, t, clientset)
	credentials, err := clientset.CoreV1().Secrets("sks-migration-system").Get(ctx, "sks-migration-minio-root", metav1.GetOptions{})
	if err != nil || len(credentials.Data["access-key"]) == 0 || len(credentials.Data["secret-key"]) == 0 {
		t.Fatalf("read managed MinIO credentials: %v", err)
	}
	accessKey := string(credentials.Data["access-key"])
	secretKey := string(credentials.Data["secret-key"])
	return sshadapter.KopiaRepository{
		Image: image, Endpoint: fmt.Sprintf("https://%s:%d", nodeIPs[0].String(), nodePort),
		Bucket: "velero", Region: "minio", Prefix: "compose-e2e/" + uuid.NewString(),
		AccessKey: accessKey, SecretKey: secretKey, TLSVerify: false, Password: secretKey,
	}
}

func composeAcceptanceDefinition(image, sentinel string) []byte {
	return []byte(fmt.Sprintf(`services:
  data:
    image: %s
    user: "0:0"
    entrypoint: ["/bin/sh", "-ec"]
    command:
      - |
        if [ ! -f /data/sentinel.txt ]; then
          printf '%s-pre\n' > /data/sentinel.txt
          sleep 15
          printf '%s-final\n' >> /data/sentinel.txt
        fi
        printf '%s-pre\n%s-final\n' > /tmp/expected.txt
        cmp -s /tmp/expected.txt /data/sentinel.txt
        printf 'SMC_DATA_OK_%s\n'
        while true; do sleep 30; done
    volumes:
      - payload:/data
volumes:
  payload: {}
`, image, sentinel, sentinel, sentinel, sentinel, suffixForMarker(sentinel)))
}

func suffixForMarker(value string) string {
	return strings.TrimPrefix(value, "sks-compose-e2e-")
}

func pauseComposeAcceptanceManifests(t *testing.T, result *transform.Result) (string, string) {
	t.Helper()
	var workloadName, pvcName string
	for index := range result.Documents {
		document := &result.Documents[index]
		switch document.Kind {
		case "Deployment":
			workloadName = document.Name
			if err := unstructured.SetNestedField(document.Object.Object, int64(0), "spec", "replicas"); err != nil {
				t.Fatalf("pause converted Deployment: %v", err)
			}
		case "PersistentVolumeClaim":
			pvcName = document.Name
		}
	}
	if workloadName == "" || pvcName == "" {
		t.Fatalf("Kompose output did not contain one migratable Deployment and PVC: %+v", result.Documents)
	}
	return workloadName, pvcName
}

func waitForComposeAcceptance(t *testing.T, ctx context.Context, cluster *kubernetesadapter.Client, clientset kubernetesclient.Interface, kubeconfig []byte, namespace, sentinel string) {
	t.Helper()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		result, err := cluster.ValidateNamespace(ctx, kubeconfig, namespace)
		if err == nil && result.Deployments == 1 && result.PVCs == 1 {
			pods, listErr := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: "io.kompose.service=data"})
			if listErr == nil && len(pods.Items) == 1 {
				limit := int64(16 * 1024)
				logs, logErr := clientset.CoreV1().Pods(namespace).GetLogs(pods.Items[0].Name, &corev1.PodLogOptions{LimitBytes: &limit}).DoRaw(ctx)
				if logErr == nil && strings.Contains(string(logs), "SMC_DATA_OK_"+suffixForMarker(sentinel)) {
					return
				}
			}
		}
		select {
		case <-ctx.Done():
			deployment, _ := clientset.AppsV1().Deployments(namespace).Get(context.Background(), "data", metav1.GetOptions{})
			if deployment == nil {
				deployment = &appsv1.Deployment{}
			}
			t.Fatalf("wait for restored Compose workload: %v available=%d", ctx.Err(), deployment.Status.AvailableReplicas)
		case <-ticker.C:
		}
	}
}
