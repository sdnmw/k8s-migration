package acceptance

import (
	"context"
	"os"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubernetesclient "k8s.io/client-go/kubernetes"

	"github.com/smartx/sks-migration-center/internal/adapter/kubernetes"
)

const acceptanceHelperImage = "m.daocloud.io/docker.io/library/busybox@sha256:73aaf090f3d85aa34ee199857f03fa3a95c8ede2ffd4cc2cdb5b94e566b11662"

// TestNFSOnSKS creates a dedicated Retain StorageClass from an existing external
// NFS profile and verifies dynamic provisioning, write, unmount, remount and read.
func TestNFSOnSKS(t *testing.T) {
	kubeconfigPath := os.Getenv("TEST_SKS_KUBECONFIG")
	if kubeconfigPath == "" {
		t.Skip("TEST_SKS_KUBECONFIG is not set")
	}
	sourceStorageClass := envOrDefault("TEST_SKS_NFS_SOURCE_SC", "nfs-csi-velero-lab")
	targetStorageClass := envOrDefault("TEST_SKS_NFS_TARGET_SC", "sks-migration-nfs")
	kubeconfig, err := os.ReadFile(kubeconfigPath)
	if err != nil {
		t.Fatalf("read acceptance kubeconfig: %v", err)
	}
	defer zero(kubeconfig)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	cluster := kubernetes.NewClient(30 * time.Second)
	prepared, err := cluster.Prepare(kubeconfig)
	if err != nil {
		t.Fatalf("prepare acceptance kubeconfig: %v", err)
	}
	clientset, err := kubernetesclient.NewForConfig(prepared.Config)
	if err != nil {
		t.Fatalf("create acceptance Kubernetes client: %v", err)
	}
	driverExists, err := cluster.HasCSIDriver(ctx, kubeconfig, "nfs.csi.k8s.io")
	if err != nil || !driverExists {
		t.Fatalf("NFS CSI driver is unavailable: exists=%v err=%v", driverExists, err)
	}
	source, err := clientset.StorageV1().StorageClasses().Get(ctx, sourceStorageClass, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("read source NFS StorageClass: %v", err)
	}
	if source.Provisioner != "nfs.csi.k8s.io" || source.Parameters["server"] == "" || source.Parameters["share"] == "" {
		t.Fatalf("source StorageClass is not an external NFS CSI profile: provisioner=%s", source.Provisioner)
	}
	if err := cluster.EnsureNamespace(ctx, kubeconfig, acceptanceNamespace); err != nil {
		t.Fatalf("ensure managed namespace: %v", err)
	}
	if err := cluster.EnsureNFSStorageClass(ctx, kubeconfig, kubernetes.NFSStorageClassSpec{
		Name: targetStorageClass, Server: source.Parameters["server"], Export: source.Parameters["share"],
		MountOptions: source.MountOptions, ReclaimPolicy: corev1.PersistentVolumeReclaimRetain,
	}); err != nil {
		t.Fatalf("ensure managed NFS StorageClass: %v", err)
	}
	result, err := cluster.ProbeStorageClass(ctx, kubeconfig, acceptanceNamespace, targetStorageClass, acceptanceHelperImage)
	if err != nil {
		t.Fatalf("probe managed NFS StorageClass: %v", err)
	}
	if !result.Remounted || result.Bytes == 0 {
		t.Fatalf("NFS probe did not verify remount: %+v", result)
	}
	value, err := clientset.StorageV1().StorageClasses().Get(ctx, targetStorageClass, metav1.GetOptions{})
	if err != nil || value.ReclaimPolicy == nil || *value.ReclaimPolicy != corev1.PersistentVolumeReclaimRetain {
		t.Fatalf("managed NFS StorageClass is not Retain: err=%v", err)
	}
	t.Logf("NFS acceptance passed: driver=%s storageClass=%s reclaimPolicy=Retain bytes=%d remounted=%v",
		value.Provisioner, value.Name, result.Bytes, result.Remounted)
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
