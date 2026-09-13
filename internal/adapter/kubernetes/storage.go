package kubernetes

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubernetesclient "k8s.io/client-go/kubernetes"
)

const nfsCSIProvisioner = "nfs.csi.k8s.io"

type NFSStorageClassSpec struct {
	Name             string
	Server           string
	Export           string
	MountOptions     []string
	ReclaimPolicy    corev1.PersistentVolumeReclaimPolicy
	MountPermissions string
}

type StorageProbeResult struct {
	StorageClass string `json:"storageClass"`
	PVCName      string `json:"pvcName"`
	Bytes        int    `json:"bytes"`
	Remounted    bool   `json:"remounted"`
}

func (c *Client) HasCSIDriver(ctx context.Context, kubeconfig []byte, name string) (bool, error) {
	clientset, err := c.clientset(kubeconfig)
	if err != nil {
		return false, err
	}
	_, err = clientset.StorageV1().CSIDrivers().Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read CSI driver: %w", err)
	}
	return true, nil
}

func (c *Client) EnsureNFSStorageClass(ctx context.Context, kubeconfig []byte, spec NFSStorageClassSpec) error {
	if strings.TrimSpace(spec.Name) == "" || strings.TrimSpace(spec.Server) == "" || !strings.HasPrefix(spec.Export, "/") {
		return errors.New("NFS StorageClass name, server and absolute export are required")
	}
	clientset, err := c.clientset(kubeconfig)
	if err != nil {
		return err
	}
	desired := desiredNFSStorageClass(spec)
	storageClasses := clientset.StorageV1().StorageClasses()
	existing, err := storageClasses.Get(ctx, spec.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := storageClasses.Create(ctx, desired, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("create NFS StorageClass: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read NFS StorageClass: %w", err)
	}
	if existing.Provisioner != desired.Provisioner || !reflect.DeepEqual(existing.Parameters, desired.Parameters) ||
		!reflect.DeepEqual(existing.MountOptions, desired.MountOptions) || !sameReclaimPolicy(existing.ReclaimPolicy, desired.ReclaimPolicy) {
		return errors.New("StorageClass name is already used by a different configuration")
	}
	return nil
}

func (c *Client) ProbeStorageClass(ctx context.Context, kubeconfig []byte, namespace, storageClass, helperImage string) (StorageProbeResult, error) {
	if namespace == "" || storageClass == "" || helperImage == "" {
		return StorageProbeResult{}, errors.New("probe namespace, StorageClass and helper image are required")
	}
	clientset, err := c.clientset(kubeconfig)
	if err != nil {
		return StorageProbeResult{}, err
	}
	suffix, payload, err := probeValues()
	if err != nil {
		return StorageProbeResult{}, err
	}
	pvcName := "nfs-probe-" + suffix
	claim, err := clientset.CoreV1().PersistentVolumeClaims(namespace).Create(ctx, probePVC(namespace, pvcName, storageClass), metav1.CreateOptions{})
	if err != nil {
		return StorageProbeResult{}, fmt.Errorf("create NFS probe PVC: %w", err)
	}
	volumeName := ""
	defer func() {
		cleanupStorageProbe(context.WithoutCancel(ctx), clientset, namespace, pvcName, volumeName, suffix)
	}()
	claim, err = waitForPVCBound(ctx, clientset, namespace, claim.Name)
	if err != nil {
		return StorageProbeResult{}, err
	}
	volumeName = claim.Spec.VolumeName

	writer := probePod(namespace, "nfs-writer-"+suffix, pvcName, helperImage, payload, true)
	if err := runProbePod(ctx, clientset, writer); err != nil {
		return StorageProbeResult{}, fmt.Errorf("NFS write probe: %w", err)
	}
	if err := deleteAndWaitForPod(ctx, clientset, namespace, writer.Name); err != nil {
		return StorageProbeResult{}, err
	}
	reader := probePod(namespace, "nfs-reader-"+suffix, pvcName, helperImage, payload, false)
	if err := runProbePod(ctx, clientset, reader); err != nil {
		return StorageProbeResult{}, fmt.Errorf("NFS remount read probe: %w", err)
	}
	return StorageProbeResult{StorageClass: storageClass, PVCName: pvcName, Bytes: len(payload), Remounted: true}, nil
}

func desiredNFSStorageClass(spec NFSStorageClassSpec) *storagev1.StorageClass {
	allowExpansion := true
	bindingMode := storagev1.VolumeBindingImmediate
	permissions := spec.MountPermissions
	if permissions == "" {
		permissions = "0777"
	}
	return &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{Name: spec.Name, Labels: map[string]string{
			"app.kubernetes.io/managed-by": "sks-migration-center",
		}},
		Provisioner: nfsCSIProvisioner,
		Parameters: map[string]string{
			"server": spec.Server, "share": spec.Export, "mountPermissions": permissions,
		},
		ReclaimPolicy:        &spec.ReclaimPolicy,
		AllowVolumeExpansion: &allowExpansion,
		VolumeBindingMode:    &bindingMode,
		MountOptions:         append([]string(nil), spec.MountOptions...),
	}
}

func probePVC(namespace, name, storageClass string) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: map[string]string{
			"app.kubernetes.io/managed-by": "sks-migration-center", "migration.smartx.com/probe": "nfs",
		}},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany},
			StorageClassName: &storageClass,
			Resources: corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{
				corev1.ResourceStorage: resource.MustParse("64Mi"),
			}},
		},
	}
}

func probePod(namespace, name, pvcName, image, payload string, write bool) *corev1.Pod {
	runAs := int64(65532)
	readOnlyRoot := true
	allowPrivilegeEscalation := false
	automount := false
	command := `set -eu; test "$(cat /probe/payload)" = "$PROBE_PAYLOAD"; rm -f /probe/payload`
	if write {
		command = `set -eu; printf '%s' "$PROBE_PAYLOAD" > /probe/payload; sync; test "$(cat /probe/payload)" = "$PROBE_PAYLOAD"`
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: map[string]string{
			"app.kubernetes.io/managed-by": "sks-migration-center", "migration.smartx.com/probe": "nfs",
		}, Annotations: map[string]string{"k8tz.io/inject": "false"}},
		Spec: corev1.PodSpec{
			RestartPolicy:                corev1.RestartPolicyNever,
			AutomountServiceAccountToken: &automount,
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot: boolPointer(true), RunAsUser: &runAs, RunAsGroup: &runAs, FSGroup: &runAs,
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
			Containers: []corev1.Container{{
				Name: "probe", Image: image, ImagePullPolicy: corev1.PullIfNotPresent,
				Command: []string{"/bin/sh", "-c", command},
				Env:     []corev1.EnvVar{{Name: "PROBE_PAYLOAD", Value: payload}},
				SecurityContext: &corev1.SecurityContext{
					AllowPrivilegeEscalation: &allowPrivilegeEscalation, ReadOnlyRootFilesystem: &readOnlyRoot,
					Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
				},
				VolumeMounts: []corev1.VolumeMount{{Name: "probe", MountPath: "/probe"}},
			}},
			Volumes: []corev1.Volume{{Name: "probe", VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: pvcName},
			}}},
		},
	}
}

func waitForPVCBound(ctx context.Context, clientset kubernetesclient.Interface, namespace, name string) (*corev1.PersistentVolumeClaim, error) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		value, err := clientset.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, name, metav1.GetOptions{})
		if err == nil && value.Status.Phase == corev1.ClaimBound && value.Spec.VolumeName != "" {
			return value, nil
		}
		if err != nil && !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("read NFS probe PVC: %w", err)
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("wait for NFS probe PVC: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func runProbePod(ctx context.Context, clientset kubernetesclient.Interface, pod *corev1.Pod) error {
	if _, err := clientset.CoreV1().Pods(pod.Namespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("create probe Pod: %w", err)
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		value, err := clientset.CoreV1().Pods(pod.Namespace).Get(ctx, pod.Name, metav1.GetOptions{})
		if err == nil {
			switch value.Status.Phase {
			case corev1.PodSucceeded:
				return nil
			case corev1.PodFailed:
				return fmt.Errorf("probe Pod failed: %s", podFailureMessage(value))
			}
		}
		if err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("read probe Pod: %w", err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for probe Pod: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func deleteAndWaitForPod(ctx context.Context, clientset kubernetesclient.Interface, namespace, name string) error {
	if err := clientset.CoreV1().Pods(namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete write probe Pod: %w", err)
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		_, err := clientset.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for write probe Pod deletion: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func cleanupStorageProbe(ctx context.Context, clientset kubernetesclient.Interface, namespace, pvcName, volumeName, suffix string) {
	background := metav1.DeletePropagationBackground
	for _, name := range []string{"nfs-writer-" + suffix, "nfs-reader-" + suffix} {
		_ = clientset.CoreV1().Pods(namespace).Delete(ctx, name, metav1.DeleteOptions{PropagationPolicy: &background})
	}
	_ = clientset.CoreV1().PersistentVolumeClaims(namespace).Delete(ctx, pvcName, metav1.DeleteOptions{})
	if volumeName != "" {
		_ = clientset.CoreV1().PersistentVolumes().Delete(ctx, volumeName, metav1.DeleteOptions{})
	}
}

func probeValues() (string, string, error) {
	suffixBytes, payloadBytes := make([]byte, 5), make([]byte, 64)
	if _, err := rand.Read(suffixBytes); err != nil {
		return "", "", errors.New("generate NFS probe name")
	}
	if _, err := rand.Read(payloadBytes); err != nil {
		return "", "", errors.New("generate NFS probe payload")
	}
	return hex.EncodeToString(suffixBytes), hex.EncodeToString(payloadBytes), nil
}

func sameReclaimPolicy(left, right *corev1.PersistentVolumeReclaimPolicy) bool {
	return left != nil && right != nil && *left == *right
}

func boolPointer(value bool) *bool { return &value }

func podFailureMessage(value *corev1.Pod) string {
	for _, status := range value.Status.ContainerStatuses {
		if status.State.Terminated != nil && status.State.Terminated.Message != "" {
			return status.State.Terminated.Message
		}
		if status.State.Terminated != nil {
			return status.State.Terminated.Reason
		}
	}
	if value.Status.Message != "" {
		return value.Status.Message
	}
	return string(value.Status.Phase)
}
