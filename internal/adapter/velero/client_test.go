package velero

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
)

const testKubeconfig = `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://127.0.0.1:6443
  name: test
contexts:
- context:
    cluster: test
    user: test
  name: test
current-context: test
users:
- name: test
  user:
    token: test
`

func TestEnsureLocationAndTrackBackupProgress(t *testing.T) {
	dynamicClient := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		backupStorageLocations: "BackupStorageLocationList", backups: "BackupList", restores: "RestoreList", podVolumeBackups: "PodVolumeBackupList",
		dataUploads: "DataUploadList", dataDownloads: "DataDownloadList",
	})
	client := newClient(time.Second, func(*rest.Config) (dynamic.Interface, error) { return dynamicClient, nil })
	ctx := context.Background()

	location, err := client.EnsureBackupStorageLocation(ctx, []byte(testKubeconfig), BackupStorageLocationSpec{
		Namespace: "velero", Name: "default", Provider: "aws", Bucket: "velero", Prefix: "sida",
		Region: "minio", Endpoint: "https://192.0.2.10:30164", CredentialSecret: "cloud-credentials", CredentialKey: "cloud", CABundle: []byte("test-ca"), AccessMode: "ReadOnly",
	})
	if err != nil || location.Name != "default" || location.Bucket != "velero" || location.Prefix != "sida" || location.Endpoint != "https://192.0.2.10:30164" {
		t.Fatalf("ensure location failed: %#v %v", location, err)
	}
	stored, err := dynamicClient.Resource(backupStorageLocations).Namespace("velero").Get(ctx, "default", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := nestedString(stored.Object, "spec", "config", "s3ForcePathStyle"); got != "true" {
		t.Fatalf("path style = %q", got)
	}
	if got := nestedString(stored.Object, "spec", "credential", "name"); got != "cloud-credentials" {
		t.Fatalf("credential secret = %q", got)
	}
	if got := nestedString(stored.Object, "spec", "accessMode"); got != "ReadOnly" {
		t.Fatalf("access mode = %q", got)
	}

	created, err := client.CreateBackup(ctx, []byte(testKubeconfig), BackupSpec{
		Namespace: "velero", Name: "migration-run-1", StorageLocation: "default", IncludedNamespaces: []string{"business"}, PlanID: "plan-1", RunID: "run-1",
	})
	if err != nil || created.Name != "migration-run-1" {
		t.Fatalf("create backup failed: %#v %v", created, err)
	}
	if _, err := client.CreateBackup(ctx, []byte(testKubeconfig), BackupSpec{
		Namespace: "velero", Name: "migration-run-1", StorageLocation: "default", IncludedNamespaces: []string{"business"}, PlanID: "plan-1", RunID: "run-1",
	}); err != nil {
		t.Fatalf("idempotent create failed: %v", err)
	}
	if _, err := client.CreateBackup(ctx, []byte(testKubeconfig), BackupSpec{
		Namespace: "velero", Name: "migration-run-1", StorageLocation: "default", IncludedNamespaces: []string{"business"}, PlanID: "plan-1", RunID: "other-run",
	}); err == nil {
		t.Fatal("expected conflicting migration identity to fail")
	}

	backup, err := dynamicClient.Resource(backups).Namespace("velero").Get(ctx, "migration-run-1", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	backup.Object["status"] = map[string]any{
		"phase": "InProgress", "warnings": int64(1), "errors": int64(0),
		"progress":       map[string]any{"itemsBackedUp": int64(7), "totalItems": int64(10)},
		"startTimestamp": "2026-09-04T10:00:00Z",
	}
	if _, err := dynamicClient.Resource(backups).Namespace("velero").UpdateStatus(ctx, backup, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	status, err := client.BackupStatus(ctx, []byte(testKubeconfig), "velero", "migration-run-1")
	if err != nil || status.Phase != "InProgress" || status.ItemsBackedUp != 7 || status.TotalItems != 10 {
		t.Fatalf("unexpected backup status: %#v %v", status, err)
	}

	pvb := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "velero.io/v1", "kind": "PodVolumeBackup",
		"metadata": map[string]any{"name": "pvb-1", "namespace": "velero", "labels": map[string]any{"velero.io/backup-name": "migration-run-1"}},
		"spec":     map[string]any{"pod": map[string]any{"name": "db-0"}, "volume": "data", "node": "worker-1"},
		"status":   map[string]any{"phase": "InProgress", "progress": map[string]any{"bytesDone": int64(1024), "totalBytes": int64(4096)}},
	}}
	if _, err := dynamicClient.Resource(podVolumeBackups).Namespace("velero").Create(ctx, pvb, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	transfers, err := client.VolumeTransfers(ctx, []byte(testKubeconfig), "velero", "migration-run-1")
	if err != nil || len(transfers) != 1 || transfers[0].BytesDone != 1024 || transfers[0].Volume != "data" {
		t.Fatalf("unexpected transfers: %#v %v", transfers, err)
	}

	restore, err := client.CreateRestore(ctx, []byte(testKubeconfig), RestoreSpec{
		Namespace: "velero", Name: "migration-run-1-restore", BackupName: "migration-run-1", IncludedNamespaces: []string{"business"},
		NamespaceMappings: map[string]string{"business": "business-restored"}, PlanID: "plan-1", RunID: "run-1", RestorePVs: true,
	})
	if err != nil || restore.Name != "migration-run-1-restore" {
		t.Fatalf("create restore failed: %#v %v", restore, err)
	}
	if _, err := client.CreateRestore(ctx, []byte(testKubeconfig), RestoreSpec{
		Namespace: "velero", Name: "migration-run-1-restore", BackupName: "migration-run-1", IncludedNamespaces: []string{"business"},
		PlanID: "plan-1", RunID: "run-1", RestorePVs: true,
	}); err != nil {
		t.Fatalf("idempotent restore failed: %v", err)
	}
	storedRestore, err := dynamicClient.Resource(restores).Namespace("velero").Get(ctx, "migration-run-1-restore", metav1.GetOptions{})
	if err != nil || nestedString(storedRestore.Object, "spec", "existingResourcePolicy") != "update" || nestedString(storedRestore.Object, "spec", "namespaceMapping", "business") != "business-restored" {
		t.Fatalf("unexpected stored restore: %#v %v", storedRestore, err)
	}
	storedRestore.Object["status"] = map[string]any{"phase": "InProgress", "progress": map[string]any{"itemsRestored": int64(5), "totalItems": int64(12)}}
	if _, err := dynamicClient.Resource(restores).Namespace("velero").UpdateStatus(ctx, storedRestore, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	restore, err = client.RestoreStatus(ctx, []byte(testKubeconfig), "velero", "migration-run-1-restore")
	if err != nil || restore.Phase != "InProgress" || restore.ItemsRestored != 5 || restore.TotalItems != 12 {
		t.Fatalf("unexpected restore status: %#v %v", restore, err)
	}
}

func TestCSIDataMoverBackupAndTransferProgress(t *testing.T) {
	dynamicClient := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		backups: "BackupList", dataUploads: "DataUploadList", dataDownloads: "DataDownloadList",
	})
	client := newClient(time.Second, func(*rest.Config) (dynamic.Interface, error) { return dynamicClient, nil })
	ctx := context.Background()
	_, err := client.CreateBackup(ctx, []byte(testKubeconfig), BackupSpec{
		Namespace: "velero", Name: "migration-data-mover", StorageLocation: "default", IncludedNamespaces: []string{"business"},
		PlanID: "plan-1", RunID: "run-1", SnapshotMoveData: true, DataMover: "velero",
	})
	if err != nil {
		t.Fatal(err)
	}
	backup, err := dynamicClient.Resource(backups).Namespace("velero").Get(ctx, "migration-data-mover", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !nestedBool(backup.Object, "spec", "snapshotMoveData") || !nestedBool(backup.Object, "spec", "snapshotVolumes") || nestedBool(backup.Object, "spec", "defaultVolumesToFsBackup") || nestedString(backup.Object, "spec", "datamover") != "velero" {
		t.Fatalf("unexpected data mover backup spec: %#v", backup.Object["spec"])
	}
	dataUpload := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "velero.io/v2alpha1", "kind": "DataUpload",
		"metadata": map[string]any{"name": "du-1", "namespace": "velero", "labels": map[string]any{"velero.io/backup-name": "migration-data-mover"}},
		"spec":     map[string]any{"sourceNamespace": "business", "sourcePVC": "database"},
		"status":   map[string]any{"phase": "InProgress", "acceptedByNode": "worker-1", "progress": map[string]any{"bytesDone": int64(2048), "totalBytes": int64(8192)}},
	}}
	if _, err := dynamicClient.Resource(dataUploads).Namespace("velero").Create(ctx, dataUpload, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	uploads, err := client.DataUploads(ctx, []byte(testKubeconfig), "velero", "migration-data-mover")
	if err != nil || len(uploads) != 1 || uploads[0].Pod != "business" || uploads[0].Volume != "database" || uploads[0].BytesDone != 2048 {
		t.Fatalf("unexpected DataUploads: %#v %v", uploads, err)
	}
	dataDownload := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "velero.io/v2alpha1", "kind": "DataDownload",
		"metadata": map[string]any{"name": "dd-1", "namespace": "velero", "labels": map[string]any{"velero.io/restore-name": "migration-restore"}},
		"spec":     map[string]any{"sourceNamespace": "business", "targetVolume": map[string]any{"namespace": "business-migrated", "pvc": "database"}},
		"status":   map[string]any{"phase": "Completed", "progress": map[string]any{"bytesDone": int64(8192), "totalBytes": int64(8192)}},
	}}
	if _, err := dynamicClient.Resource(dataDownloads).Namespace("velero").Create(ctx, dataDownload, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	downloads, err := client.DataDownloads(ctx, []byte(testKubeconfig), "velero", "migration-restore")
	if err != nil || len(downloads) != 1 || downloads[0].Volume != "database" || downloads[0].BytesDone != 8192 {
		t.Fatalf("unexpected DataDownloads: %#v %v", downloads, err)
	}
}

func TestDeleteRunArtifactsUsesExactRunLabel(t *testing.T) {
	runID := uuid.NewString()
	dynamicClient := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		backups: "BackupList", restores: "RestoreList", deleteBackupRequests: "DeleteBackupRequestList",
	})
	for _, value := range []*unstructured.Unstructured{
		{Object: map[string]any{"apiVersion": "velero.io/v1", "kind": "Backup", "metadata": map[string]any{"name": "delete-backup", "namespace": "velero", "labels": map[string]any{"migration.smartx.com/run-id": runID}}}},
		{Object: map[string]any{"apiVersion": "velero.io/v1", "kind": "Backup", "metadata": map[string]any{"name": "keep-backup", "namespace": "velero", "labels": map[string]any{"migration.smartx.com/run-id": uuid.NewString()}}}},
		{Object: map[string]any{"apiVersion": "velero.io/v1", "kind": "Restore", "metadata": map[string]any{"name": "delete-restore", "namespace": "velero", "labels": map[string]any{"migration.smartx.com/run-id": runID}}}},
	} {
		resource := backups
		if value.GetKind() == "Restore" {
			resource = restores
		}
		if _, err := dynamicClient.Resource(resource).Namespace("velero").Create(context.Background(), value, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	client := newClient(time.Second, func(*rest.Config) (dynamic.Interface, error) { return dynamicClient, nil })
	result, err := client.DeleteRunArtifacts(context.Background(), []byte(testKubeconfig), "velero", runID, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.BackupsDeleted != 1 || result.RestoresDeleted != 1 {
		t.Fatalf("unexpected cleanup result: %+v", result)
	}
	if _, err := dynamicClient.Resource(backups).Namespace("velero").Get(context.Background(), "keep-backup", metav1.GetOptions{}); err != nil {
		t.Fatal("cleanup removed another run's backup")
	}
	requests, err := dynamicClient.Resource(deleteBackupRequests).Namespace("velero").List(context.Background(), metav1.ListOptions{})
	if err != nil || len(requests.Items) != 1 || nestedString(requests.Items[0].Object, "spec", "backupName") != "delete-backup" {
		t.Fatalf("cleanup did not submit exact DeleteBackupRequest: items=%d err=%v", len(requests.Items), err)
	}
}

func TestWatchBackupForwardsStatus(t *testing.T) {
	dynamicClient := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{backups: "BackupList"})
	client := newClient(time.Second, func(*rest.Config) (dynamic.Interface, error) { return dynamicClient, nil })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates, failures, err := client.WatchBackup(ctx, []byte(testKubeconfig), "velero", "backup-1", "")
	if err != nil {
		t.Fatal(err)
	}
	value := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "velero.io/v1", "kind": "Backup", "metadata": map[string]any{"name": "backup-1", "namespace": "velero"},
		"status": map[string]any{"phase": "Completed", "errors": int64(0)},
	}}
	if _, err := dynamicClient.Resource(backups).Namespace("velero").Create(ctx, value, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	select {
	case failure := <-failures:
		if failure != nil {
			t.Fatal(failure)
		}
	case update := <-updates:
		if update.Phase != "Completed" {
			t.Fatalf("phase = %q", update.Phase)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for backup watch event")
	}
}

func nestedBool(object map[string]any, fields ...string) bool {
	value, _, _ := unstructured.NestedBool(object, fields...)
	return value
}
