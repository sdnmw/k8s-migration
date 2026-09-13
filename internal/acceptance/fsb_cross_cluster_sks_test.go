package acceptance

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubernetesclient "k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/smartx/sks-migration-center/internal/adapter/kubernetes"
	veleroadapter "github.com/smartx/sks-migration-center/internal/adapter/velero"
)

// TestExistingVeleroFSBCrossClusterOnSKS performs an actual Kopia file-system
// backup of a mounted PVC, restores it to the target cluster and verifies file
// content. StorageClass names may differ and are mapped exactly as production.
func TestExistingVeleroFSBCrossClusterOnSKS(t *testing.T) {
	sourcePath, targetPath := os.Getenv("TEST_SKS_SOURCE_KUBECONFIG"), os.Getenv("TEST_SKS_TARGET_KUBECONFIG")
	storageLocation := os.Getenv("TEST_SKS_EXISTING_VELERO_BSL")
	sourceSC, targetSC := os.Getenv("TEST_SKS_FSB_SOURCE_SC"), os.Getenv("TEST_SKS_FSB_TARGET_SC")
	image := os.Getenv("TEST_SKS_FSB_IMAGE")
	if sourcePath == "" || targetPath == "" || storageLocation == "" || sourceSC == "" || targetSC == "" || image == "" {
		t.Skip("source/target kubeconfigs, existing Velero BSL, source/target FSB StorageClasses and FSB image are required")
	}
	sourceConfig, targetConfig := readSensitiveFile(t, sourcePath), readSensitiveFile(t, targetPath)
	t.Cleanup(func() { zero(sourceConfig); zero(targetConfig) })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	accessMode := corev1.ReadWriteOnce
	if os.Getenv("TEST_SKS_FSB_ACCESS_MODE") == string(corev1.ReadWriteMany) {
		accessMode = corev1.ReadWriteMany
	}

	runID, planID := uuid.New(), uuid.New()
	suffix := runID.String()[:8]
	sourceNamespace, targetNamespace := "smc-fsb-src-"+suffix, "smc-fsb-dst-"+suffix
	backupName, restoreName := "smc-fsb-"+suffix, "smc-fsb-restore-"+suffix
	sentinel := "fsb-" + uuid.NewString()
	cluster, velero := kubernetes.NewClient(30*time.Second), veleroadapter.NewClient(30*time.Second)
	sourceREST, sourceClient := acceptanceRESTClient(t, cluster, sourceConfig)
	targetREST, targetClient := acceptanceRESTClient(t, cluster, targetConfig)

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cleanupCancel()
		_ = sourceClient.CoreV1().Namespaces().Delete(cleanupCtx, sourceNamespace, metav1.DeleteOptions{})
		_ = targetClient.CoreV1().Namespaces().Delete(cleanupCtx, targetNamespace, metav1.DeleteOptions{})
		_ = cluster.DeleteVeleroStorageClassMappings(cleanupCtx, targetConfig, "velero", runID.String())
		if _, err := velero.DeleteRunArtifacts(cleanupCtx, sourceConfig, "velero", runID.String(), true); err != nil {
			t.Errorf("submit source Velero cleanup: %v", err)
		} else {
			waitForVeleroBackupGone(t, cleanupCtx, velero, sourceConfig, backupName)
		}
		if _, err := velero.DeleteRunArtifacts(cleanupCtx, targetConfig, "velero", runID.String(), false); err != nil {
			t.Errorf("clean target Velero artifacts: %v", err)
		}
	})

	if err := cluster.EnsureNamespace(ctx, sourceConfig, sourceNamespace); err != nil {
		t.Fatalf("create source namespace: %v", err)
	}
	if _, err := sourceClient.CoreV1().PersistentVolumeClaims(sourceNamespace).Create(ctx, fsbPVC(sourceSC, accessMode), metav1.CreateOptions{}); err != nil {
		t.Fatalf("create source PVC: %v", err)
	}
	if _, err := sourceClient.CoreV1().Pods(sourceNamespace).Create(ctx, fsbPod(image, sentinel), metav1.CreateOptions{}); err != nil {
		t.Fatalf("create source data Pod: %v", err)
	}
	waitForFSBPod(t, ctx, sourceClient, sourceNamespace)
	if _, err := execInFSBPod(ctx, sourceREST, sourceClient, sourceNamespace, []string{"/bin/sh", "-c", `printf %s "$SENTINEL" > /data/sentinel && dd if=/dev/zero of=/data/payload bs=1024 count=64`}); err != nil {
		t.Fatalf("write source PVC data: %v", err)
	}

	started := time.Now()
	backup, err := velero.CreateBackup(ctx, sourceConfig, veleroadapter.BackupSpec{
		Namespace: "velero", Name: backupName, StorageLocation: storageLocation, IncludedNamespaces: []string{sourceNamespace},
		PlanID: planID.String(), RunID: runID.String(), TTL: 2 * time.Hour,
	})
	if err != nil {
		t.Fatalf("create FSB Backup: %v", err)
	}
	backup = waitForSKSBackup(t, ctx, velero, sourceConfig, backupName, backup)
	transfers, err := velero.VolumeTransfers(ctx, sourceConfig, "velero", backupName)
	if err != nil || len(transfers) == 0 {
		t.Fatalf("FSB Backup created no PodVolumeBackup: transfers=%+v err=%v", transfers, err)
	}
	for _, transfer := range transfers {
		if transfer.Phase != "Completed" {
			t.Fatalf("PodVolumeBackup %s phase=%s", transfer.Name, transfer.Phase)
		}
	}

	if err := cluster.EnsureVeleroStorageClassMappings(ctx, targetConfig, "velero", runID.String(), map[string]string{sourceSC: targetSC}); err != nil {
		t.Fatalf("create target StorageClass mapping: %v", err)
	}
	if err := cluster.EnsureMigrationNamespace(ctx, targetConfig, targetNamespace); err != nil {
		t.Fatalf("create target migration namespace: %v", err)
	}
	targetBackup := waitForSKSBackup(t, ctx, velero, targetConfig, backupName, veleroadapter.BackupStatus{})
	restore, err := velero.CreateRestore(ctx, targetConfig, veleroadapter.RestoreSpec{
		Namespace: "velero", Name: restoreName, BackupName: targetBackup.Name, IncludedNamespaces: []string{sourceNamespace},
		NamespaceMappings: map[string]string{sourceNamespace: targetNamespace}, PlanID: planID.String(), RunID: runID.String(), RestorePVs: true,
	})
	if err != nil {
		t.Fatalf("create FSB Restore: %v", err)
	}
	restore = waitForSKSRestore(t, ctx, velero, targetConfig, restoreName, restore)
	waitForFSBPod(t, ctx, targetClient, targetNamespace)
	value, err := execInFSBPod(ctx, targetREST, targetClient, targetNamespace, []string{"/bin/sh", "-c", "cat /data/sentinel"})
	if err != nil || strings.TrimSpace(value) != sentinel {
		t.Fatalf("restored PVC data mismatch: value=%q err=%v", value, err)
	}
	t.Logf("FSB cross-cluster E2E passed in %s: storage=%s->%s mode=%s backup=%d/%d restore=%d/%d volumes=%d",
		time.Since(started).Round(time.Millisecond), sourceSC, targetSC, accessMode, backup.ItemsBackedUp, backup.TotalItems, restore.ItemsRestored, restore.TotalItems, len(transfers))
}

func acceptanceRESTClient(t *testing.T, cluster *kubernetes.Client, kubeconfig []byte) (*rest.Config, kubernetesclient.Interface) {
	t.Helper()
	prepared, err := cluster.Prepare(kubeconfig)
	if err != nil {
		t.Fatal(err)
	}
	client, err := kubernetesclient.NewForConfig(prepared.Config)
	if err != nil {
		t.Fatal(err)
	}
	return prepared.Config, client
}

func fsbPVC(storageClass string, accessMode corev1.PersistentVolumeAccessMode) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "data"},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{accessMode}, StorageClassName: &storageClass,
			Resources: corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")}},
		},
	}
}

func fsbPod(image, sentinel string) *corev1.Pod {
	nonRoot, user, group, noEscalation := true, int64(1001), int64(1001), false
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "data", Labels: map[string]string{"app": "smc-fsb"}},
		Spec: corev1.PodSpec{
			SecurityContext: &corev1.PodSecurityContext{RunAsNonRoot: &nonRoot, RunAsUser: &user, FSGroup: &group, SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}},
			Containers: []corev1.Container{{
				Name: "data", Image: image, ImagePullPolicy: corev1.PullIfNotPresent, Command: []string{"/bin/sh", "-c", "sleep 3600"},
				Env:             []corev1.EnvVar{{Name: "SENTINEL", Value: sentinel}},
				SecurityContext: &corev1.SecurityContext{AllowPrivilegeEscalation: &noEscalation, Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}},
				VolumeMounts:    []corev1.VolumeMount{{Name: "data", MountPath: "/data"}},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("10m"), corev1.ResourceMemory: resource.MustParse("16Mi")},
					Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("64Mi")},
				},
			}},
			Volumes: []corev1.Volume{{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "data"}}}},
		},
	}
}

func waitForFSBPod(t *testing.T, ctx context.Context, client kubernetesclient.Interface, namespace string) {
	t.Helper()
	for {
		pod, err := client.CoreV1().Pods(namespace).Get(ctx, "data", metav1.GetOptions{})
		if err == nil && pod.Status.Phase == corev1.PodRunning {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for FSB Pod %s/data: %v", namespace, ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
}

func execInFSBPod(ctx context.Context, config *rest.Config, client kubernetesclient.Interface, namespace string, command []string) (string, error) {
	request := client.CoreV1().RESTClient().Post().Resource("pods").Name("data").Namespace(namespace).SubResource("exec")
	request.VersionedParams(&corev1.PodExecOptions{Container: "data", Command: command, Stdout: true, Stderr: true}, clientgoscheme.ParameterCodec)
	executor, err := remotecommand.NewSPDYExecutor(config, "POST", request.URL())
	if err != nil {
		return "", err
	}
	var stdout, stderr bytes.Buffer
	if err := executor.StreamWithContext(ctx, remotecommand.StreamOptions{Stdout: &stdout, Stderr: &stderr}); err != nil {
		return "", fmt.Errorf("exec in FSB Pod: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
