package acceptance

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubernetesclient "k8s.io/client-go/kubernetes"

	"github.com/smartx/sks-migration-center/internal/adapter/kubernetes"
	veleroadapter "github.com/smartx/sks-migration-center/internal/adapter/velero"
)

// TestExistingVeleroCrossClusterOnSKS proves that two already-installed Velero
// controllers can exchange an actual Backup through their shared BSL and
// restore it under a namespace mapping. All test resources use a unique run ID.
func TestExistingVeleroCrossClusterOnSKS(t *testing.T) {
	sourcePath, targetPath := os.Getenv("TEST_SKS_SOURCE_KUBECONFIG"), os.Getenv("TEST_SKS_TARGET_KUBECONFIG")
	storageLocation := os.Getenv("TEST_SKS_EXISTING_VELERO_BSL")
	if sourcePath == "" || targetPath == "" || storageLocation == "" {
		t.Skip("TEST_SKS_SOURCE_KUBECONFIG, TEST_SKS_TARGET_KUBECONFIG and TEST_SKS_EXISTING_VELERO_BSL are not set")
	}
	sourceConfig, targetConfig := readSensitiveFile(t, sourcePath), readSensitiveFile(t, targetPath)
	t.Cleanup(func() { zero(sourceConfig); zero(targetConfig) })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	runID, planID := uuid.New(), uuid.New()
	suffix := runID.String()[:8]
	sourceNamespace, targetNamespace := "smc-velero-src-"+suffix, "smc-velero-dst-"+suffix
	backupName, restoreName := "smc-velero-"+suffix, "smc-restore-"+suffix
	cluster, velero := kubernetes.NewClient(30*time.Second), veleroadapter.NewClient(30*time.Second)
	sourceClient := acceptanceClientset(t, cluster, sourceConfig)
	targetClient := acceptanceClientset(t, cluster, targetConfig)

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cleanupCancel()
		_ = sourceClient.CoreV1().Namespaces().Delete(cleanupCtx, sourceNamespace, metav1.DeleteOptions{})
		_ = targetClient.CoreV1().Namespaces().Delete(cleanupCtx, targetNamespace, metav1.DeleteOptions{})
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
		t.Fatalf("create source acceptance namespace: %v", err)
	}
	if _, err := sourceClient.CoreV1().ConfigMaps(sourceNamespace).Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "migration-sentinel"}, Data: map[string]string{"value": "sida-to-mw"},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create source ConfigMap: %v", err)
	}
	if _, err := sourceClient.CoreV1().Secrets(sourceNamespace).Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "migration-secret"}, Type: corev1.SecretTypeOpaque, Data: map[string][]byte{"value": []byte("velero-cross-cluster")},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create source Secret: %v", err)
	}

	started := time.Now()
	backup, err := velero.CreateBackup(ctx, sourceConfig, veleroadapter.BackupSpec{
		Namespace: "velero", Name: backupName, StorageLocation: storageLocation, IncludedNamespaces: []string{sourceNamespace},
		PlanID: planID.String(), RunID: runID.String(), TTL: 2 * time.Hour,
	})
	if err != nil {
		t.Fatalf("create source Velero Backup: %v", err)
	}
	backup = waitForSKSBackup(t, ctx, velero, sourceConfig, backupName, backup)
	if backup.Errors != 0 {
		t.Fatalf("source Backup completed with %d errors", backup.Errors)
	}

	targetBackup := waitForSKSBackup(t, ctx, velero, targetConfig, backupName, veleroadapter.BackupStatus{})
	restore, err := velero.CreateRestore(ctx, targetConfig, veleroadapter.RestoreSpec{
		Namespace: "velero", Name: restoreName, BackupName: targetBackup.Name, IncludedNamespaces: []string{sourceNamespace},
		NamespaceMappings: map[string]string{sourceNamespace: targetNamespace}, PlanID: planID.String(), RunID: runID.String(), RestorePVs: true,
	})
	if err != nil {
		t.Fatalf("create target Velero Restore: %v", err)
	}
	restore = waitForSKSRestore(t, ctx, velero, targetConfig, restoreName, restore)
	if restore.Errors != 0 {
		t.Fatalf("target Restore completed with %d errors", restore.Errors)
	}

	configMap, err := targetClient.CoreV1().ConfigMaps(targetNamespace).Get(ctx, "migration-sentinel", metav1.GetOptions{})
	if err != nil || configMap.Data["value"] != "sida-to-mw" {
		t.Fatalf("restored ConfigMap mismatch: value=%q err=%v", configMap.Data["value"], err)
	}
	secret, err := targetClient.CoreV1().Secrets(targetNamespace).Get(ctx, "migration-secret", metav1.GetOptions{})
	if err != nil || string(secret.Data["value"]) != "velero-cross-cluster" {
		t.Fatalf("restored Secret mismatch: err=%v", err)
	}
	t.Logf("existing Velero cross-cluster E2E passed in %s: backup=%d/%d restore=%d/%d",
		time.Since(started).Round(time.Millisecond), backup.ItemsBackedUp, backup.TotalItems, restore.ItemsRestored, restore.TotalItems)
}

func waitForVeleroBackupGone(t *testing.T, ctx context.Context, client *veleroadapter.Client, kubeconfig []byte, name string) {
	t.Helper()
	for {
		_, err := client.BackupStatus(ctx, kubeconfig, "velero", name)
		if err != nil && apierrors.IsNotFound(errors.Unwrap(err)) {
			return
		}
		select {
		case <-ctx.Done():
			t.Errorf("wait for Velero Backup %s cleanup: %v", name, ctx.Err())
			return
		case <-time.After(time.Second):
		}
	}
}

func acceptanceClientset(t *testing.T, cluster *kubernetes.Client, kubeconfig []byte) kubernetesclient.Interface {
	t.Helper()
	prepared, err := cluster.Prepare(kubeconfig)
	if err != nil {
		t.Fatal(err)
	}
	client, err := kubernetesclient.NewForConfig(prepared.Config)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func waitForSKSBackup(t *testing.T, ctx context.Context, client *veleroadapter.Client, kubeconfig []byte, name string, current veleroadapter.BackupStatus) veleroadapter.BackupStatus {
	t.Helper()
	for {
		if current.Phase == "Completed" {
			return current
		}
		if current.Phase == "Failed" || current.Phase == "PartiallyFailed" || current.Phase == "FailedValidation" {
			t.Fatalf("Velero Backup %s ended in %s: %s", name, current.Phase, current.Message)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for Velero Backup %s: %v", name, ctx.Err())
		case <-time.After(3 * time.Second):
			value, err := client.BackupStatus(ctx, kubeconfig, "velero", name)
			if err == nil {
				current = value
			} else if !apierrors.IsNotFound(errors.Unwrap(err)) {
				t.Fatalf("read Velero Backup %s: %v", name, err)
			}
			// Synced backups are absent on the target until the BSL resyncs.
		}
	}
}

func waitForSKSRestore(t *testing.T, ctx context.Context, client *veleroadapter.Client, kubeconfig []byte, name string, current veleroadapter.RestoreStatus) veleroadapter.RestoreStatus {
	t.Helper()
	for {
		if current.Phase == "Completed" {
			return current
		}
		if current.Phase == "Failed" || current.Phase == "PartiallyFailed" || current.Phase == "FailedValidation" {
			t.Fatalf("Velero Restore %s ended in %s: %s", name, current.Phase, current.Message)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for Velero Restore %s: %v", name, ctx.Err())
		case <-time.After(3 * time.Second):
			value, err := client.RestoreStatus(ctx, kubeconfig, "velero", name)
			if err != nil {
				t.Fatalf("read Velero Restore %s: %v", name, err)
			}
			current = value
		}
	}
}
