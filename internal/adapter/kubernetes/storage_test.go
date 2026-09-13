package kubernetes

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestDesiredNFSStorageClassUsesExternalServerAndRetain(t *testing.T) {
	value := desiredNFSStorageClass(NFSStorageClassSpec{
		Name: "migration-nfs", Server: "10.0.0.10", Export: "/migration", MountOptions: []string{"nfsvers=4.1"}, ReclaimPolicy: corev1.PersistentVolumeReclaimRetain,
	})
	if value.Provisioner != nfsCSIProvisioner || value.Parameters["server"] != "10.0.0.10" || value.Parameters["share"] != "/migration" {
		t.Fatalf("unexpected NFS StorageClass: %+v", value)
	}
	if value.ReclaimPolicy == nil || *value.ReclaimPolicy != corev1.PersistentVolumeReclaimRetain || value.Parameters["mountPermissions"] != "0777" {
		t.Fatalf("unexpected reclaim policy or permissions: %+v", value)
	}
}

func TestProbePodMeetsRestrictedPolicyAndSeparatesWriteRead(t *testing.T) {
	writer := probePod("managed", "writer", "claim", "helper@sha256:test", "payload", true)
	reader := probePod("managed", "reader", "claim", "helper@sha256:test", "payload", false)
	if writer.Annotations["k8tz.io/inject"] != "false" || writer.Spec.SecurityContext == nil || writer.Spec.SecurityContext.RunAsNonRoot == nil || !*writer.Spec.SecurityContext.RunAsNonRoot {
		t.Fatalf("writer is not restricted compatible: %+v", writer.Spec.SecurityContext)
	}
	if writer.Spec.Containers[0].SecurityContext == nil || writer.Spec.Containers[0].SecurityContext.AllowPrivilegeEscalation == nil || *writer.Spec.Containers[0].SecurityContext.AllowPrivilegeEscalation {
		t.Fatal("writer permits privilege escalation")
	}
	if writer.Spec.Containers[0].Command[2] == reader.Spec.Containers[0].Command[2] {
		t.Fatal("writer and remount reader must execute different checks")
	}
}
