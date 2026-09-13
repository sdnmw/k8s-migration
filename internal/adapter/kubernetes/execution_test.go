package kubernetes

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestPreparePVCStagingOnlyForUnmountedClaims(t *testing.T) {
	namespace, runID := "business", "run-123"
	mounted := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "db-0", Namespace: namespace}, Status: corev1.PodStatus{Phase: corev1.PodRunning},
		Spec: corev1.PodSpec{Volumes: []corev1.Volume{{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "mounted"}}}}},
	}
	clientset := fake.NewSimpleClientset(mounted)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The fake client has no kubelet controller, so create the expected Running
	// Pod up front to exercise idempotent staging and mounted-PVC filtering.
	expectedName := stagingPodName(runID, "orphan")
	existing := stagingPod(PVCStagingSpec{Namespace: namespace, RunID: runID, HelperImage: "busybox@sha256:test"}, expectedName, "orphan")
	existing.Status.Phase = corev1.PodRunning
	if _, err := clientset.CoreV1().Pods(namespace).Create(ctx, existing, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	values, err := preparePVCStaging(ctx, clientset, PVCStagingSpec{
		Namespace: namespace, RunID: runID, PVCNames: []string{"mounted", "orphan", "orphan"}, HelperImage: "busybox@sha256:test",
	})
	if err != nil || len(values) != 1 || values[0].PVC != "orphan" {
		t.Fatalf("staging = %#v, err=%v", values, err)
	}
	pod, _ := clientset.CoreV1().Pods(namespace).Get(ctx, expectedName, metav1.GetOptions{})
	if pod.Annotations["backup.velero.io/backup-volumes"] != "data" || pod.Spec.AutomountServiceAccountToken == nil || *pod.Spec.AutomountServiceAccountToken {
		t.Fatalf("unexpected staging Pod: %#v", pod)
	}
	if err := deletePVCStaging(ctx, clientset, namespace, runID); err != nil {
		t.Fatal(err)
	}
	if pods, _ := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: "migration.smartx.com/purpose=pvc-staging"}); len(pods.Items) != 0 {
		t.Fatalf("staging Pods were not deleted: %#v", pods.Items)
	}
}

func TestListScalableWorkloadsPreservesReplicaCounts(t *testing.T) {
	three, two := int32(3), int32(2)
	clientset := fake.NewSimpleClientset(
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "business"}, Spec: appsv1.DeploymentSpec{Replicas: &three}},
		&appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "business"}, Spec: appsv1.StatefulSetSpec{Replicas: &two}},
	)
	values, err := listScalableWorkloads(context.Background(), clientset, "business")
	if err != nil || len(values) != 2 || values[0].Kind != "Deployment" || values[0].Replicas != 3 || values[1].Kind != "StatefulSet" || values[1].Replicas != 2 {
		t.Fatalf("workloads = %#v, err=%v", values, err)
	}
}

func TestVeleroStorageMappingConfigAndNamespaceValidation(t *testing.T) {
	ready := int32(1)
	clientset := fake.NewSimpleClientset(
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "target", Generation: 1}, Spec: appsv1.DeploymentSpec{Replicas: &ready}, Status: appsv1.DeploymentStatus{ObservedGeneration: 1, UpdatedReplicas: 1, AvailableReplicas: 1}},
		&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "data", Namespace: "target"}, Status: corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimBound}},
	)
	ctx := context.Background()
	// Exercise the same ConfigMap shape used by the real client without a REST config.
	runID := "run-123"
	desired := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: storageMappingConfigName(runID), Namespace: "velero", Labels: map[string]string{
		"migration.smartx.com/run-id": runID, "velero.io/plugin-config": "", "velero.io/change-storage-class": "RestoreItemAction",
	}}, Data: map[string]string{"source-sc": "target-sc"}}
	if _, err := clientset.CoreV1().ConfigMaps("velero").Create(ctx, desired, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	stored, _ := clientset.CoreV1().ConfigMaps("velero").Get(ctx, desired.Name, metav1.GetOptions{})
	if stored.Data["source-sc"] != "target-sc" || stored.Labels["velero.io/change-storage-class"] != "RestoreItemAction" {
		t.Fatalf("unexpected mapping ConfigMap: %#v", stored)
	}
	validation, err := validateNamespaceWithClient(ctx, clientset, "target")
	if err != nil || validation.Deployments != 1 || validation.PVCs != 1 {
		t.Fatalf("validation = %#v, err=%v", validation, err)
	}
}

func TestMergeStorageMappingOwnersSupportsConcurrentRunsAndRejectsConflicts(t *testing.T) {
	values, err := mergeStorageMappingOwners(map[string]string{
		"migration.smartx.com/run-a": `{"source-a":"target-a"}`,
		"migration.smartx.com/run-b": `{"source-b":"target-b"}`,
		"unrelated":                  "ignored",
	})
	if err != nil || values["source-a"] != "target-a" || values["source-b"] != "target-b" {
		t.Fatalf("merged mappings = %#v err=%v", values, err)
	}
	if _, err := mergeStorageMappingOwners(map[string]string{
		"migration.smartx.com/run-a": `{"source":"target-a"}`,
		"migration.smartx.com/run-b": `{"source":"target-b"}`,
	}); err == nil {
		t.Fatal("expected conflicting StorageClass mappings to fail")
	}
}
