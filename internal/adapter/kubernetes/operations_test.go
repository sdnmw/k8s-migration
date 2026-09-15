package kubernetes

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPrepareMigrationNamespaceRemovesOnlyPlatformManagedPodSecurity(t *testing.T) {
	managed := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{
		"app.kubernetes.io/managed-by":               "sks-migration-center",
		"pod-security.kubernetes.io/enforce":         "restricted",
		"pod-security.kubernetes.io/enforce-version": "latest",
	}}}
	prepareMigrationNamespace(managed, true)
	if _, found := managed.Labels["pod-security.kubernetes.io/enforce"]; found {
		t.Fatal("platform-managed Pod Security policy was not removed")
	}
	if managed.Annotations["k8tz.io/inject"] != "false" {
		t.Fatal("migration namespace must disable k8tz injection")
	}

	userManaged := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{
		"pod-security.kubernetes.io/enforce": "restricted",
	}}}
	prepareMigrationNamespace(userManaged, false)
	if userManaged.Labels["pod-security.kubernetes.io/enforce"] != "restricted" {
		t.Fatal("an existing user-managed Pod Security policy must be preserved")
	}
}

func TestStatefulSetReadyRequiresObservedCompleteRevision(t *testing.T) {
	replicas := int32(1)
	value := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Generation: 3},
		Spec:       appsv1.StatefulSetSpec{Replicas: &replicas},
		Status: appsv1.StatefulSetStatus{
			ObservedGeneration: 3, ReadyReplicas: 1, CurrentReplicas: 1, UpdatedReplicas: 1,
			CurrentRevision: "rev-2", UpdateRevision: "rev-2",
		},
	}
	if !statefulSetReady(value, 3) {
		t.Fatal("expected completed rollout to be ready")
	}
	value.Status.UpdateRevision = "rev-3"
	if statefulSetReady(value, 3) {
		t.Fatal("revision mismatch must not be ready")
	}
	value.Status.UpdateRevision = "rev-2"
	value.Status.ObservedGeneration = 2
	if statefulSetReady(value, 3) {
		t.Fatal("unobserved generation must not be ready")
	}
}

func TestCloneSecretDataDoesNotAliasInput(t *testing.T) {
	input := map[string][]byte{"password": []byte("secret")}
	cloned := cloneSecretData(input)
	cloned["password"][0] = 'X'
	if string(input["password"]) != "secret" {
		t.Fatal("secret clone aliases caller data")
	}
}
