package kubernetes

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
)

func TestDiscoverCSIDataMoverRequiresAllRuntimeComponents(t *testing.T) {
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "velero", Namespace: "velero"}, Spec: appsv1.DeploymentSpec{
		Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "velero", Args: []string{"server", "--features=EnableCSI"}}}}},
	}}
	nodeAgent := &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: "node-agent", Namespace: "velero"}, Status: appsv1.DaemonSetStatus{DesiredNumberScheduled: 3, NumberReady: 3}}
	clientset := kubernetesfake.NewSimpleClientset(deployment, nodeAgent)
	dynamicClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		dataUploads: "DataUploadList", dataDownloads: "DataDownloadList", backupRepositories: "BackupRepositoryList",
	},
		&unstructured.Unstructured{Object: map[string]any{"apiVersion": "velero.io/v2alpha1", "kind": "DataUpload", "metadata": map[string]any{"name": "probe", "namespace": "velero"}}},
		&unstructured.Unstructured{Object: map[string]any{"apiVersion": "velero.io/v2alpha1", "kind": "DataDownload", "metadata": map[string]any{"name": "probe", "namespace": "velero"}}},
		&unstructured.Unstructured{Object: map[string]any{"apiVersion": "velero.io/v1", "kind": "BackupRepository", "metadata": map[string]any{"name": "probe", "namespace": "velero"}}},
	)

	value, err := discoverCSIDataMover(context.Background(), clientset, dynamicClient, "velero", true)
	if err != nil {
		t.Fatal(err)
	}
	if !value.BackupReady || !value.RestoreReady || !value.EnableCSI || value.NodeAgentReady != 3 {
		t.Fatalf("unexpected data mover capability: %+v", value)
	}
}

func TestDiscoverCSIDataMoverDoesNotClaimPartialInstallation(t *testing.T) {
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "velero", Namespace: "velero"}, Spec: appsv1.DeploymentSpec{
		Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "velero", Args: []string{"server"}}}}},
	}}
	nodeAgent := &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: "node-agent", Namespace: "velero"}, Status: appsv1.DaemonSetStatus{DesiredNumberScheduled: 3, NumberReady: 2}}
	dynamicClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		dataUploads: "DataUploadList", dataDownloads: "DataDownloadList", backupRepositories: "BackupRepositoryList",
	})
	value, err := discoverCSIDataMover(context.Background(), kubernetesfake.NewSimpleClientset(deployment, nodeAgent), dynamicClient, "velero", true)
	if err != nil {
		t.Fatal(err)
	}
	if value.BackupReady || value.RestoreReady {
		t.Fatalf("partial installation must remain unavailable: %+v", value)
	}
}
