package acceptance

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/smartx/sks-migration-center/internal/adapter/kubernetes"
)

// TestCSIDataMoverCapabilitiesOnSKSReadOnly verifies the storage snapshot
// relationship and Velero runtime without creating or changing cluster objects.
func TestCSIDataMoverCapabilitiesOnSKSReadOnly(t *testing.T) {
	kubeconfigPath := os.Getenv("TEST_SKS_KUBECONFIG")
	if kubeconfigPath == "" {
		t.Skip("TEST_SKS_KUBECONFIG is not set")
	}
	kubeconfig := readSensitiveFile(t, kubeconfigPath)
	defer zero(kubeconfig)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := kubernetes.NewClient(30 * time.Second)
	result, err := client.Discover(ctx, kubeconfig)
	if err != nil {
		t.Fatalf("discover SKS capabilities: %v", err)
	}
	smartXClass := ""
	for _, storageClass := range result.Capabilities.StorageClasses {
		if storageClass.Provisioner == "com.smartx.elf-csi-driver" || storageClass.Provisioner == "smtx-elf-csi-driver" {
			smartXClass = storageClass.Name
			if storageClass.Default {
				break
			}
		}
	}
	if smartXClass == "" {
		t.Fatal("SKS has no SmartX ELF CSI StorageClass")
	}
	driver, matched := result.Capabilities.SnapshotDriverForStorageClass(smartXClass)
	if !matched {
		t.Fatalf("SmartX StorageClass %s has no same-driver VolumeSnapshotClass", smartXClass)
	}
	runtime, err := client.CSIDataMoverCapabilities(ctx, kubeconfig, "velero")
	if err != nil {
		t.Fatalf("probe Velero Data Mover runtime: %v", err)
	}
	t.Logf("SKS CSI read-only probe: storageClass=%s snapshotDriver=%s snapshotAPI=%t uploadAPI=%t downloadAPI=%t enableCSI=%t nodeAgent=%d/%d backupReady=%t restoreReady=%t",
		smartXClass, driver, runtime.SnapshotAPI, runtime.DataUploadAPI, runtime.DataDownloadAPI, runtime.EnableCSI,
		runtime.NodeAgentReady, runtime.NodeAgentDesired, runtime.BackupReady, runtime.RestoreReady)
}
