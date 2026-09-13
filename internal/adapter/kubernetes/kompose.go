package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes"
)

const (
	defaultKomposeTimeout = 5 * time.Minute
	maxKomposeOutputBytes = 10 * 1024 * 1024
)

type KomposeJobSpec struct {
	SystemNamespace string
	TargetNamespace string
	RunID           string
	ComposeYAML     []byte
	EnvironmentFile []byte
	KomposeImage    string
	HelperImage     string
	Timeout         time.Duration
}

func (c *Client) RunKomposeJob(ctx context.Context, kubeconfig []byte, spec KomposeJobSpec) ([]byte, error) {
	if err := validateKomposeSpec(spec); err != nil {
		return nil, err
	}
	if err := c.EnsureNamespace(ctx, kubeconfig, spec.SystemNamespace); err != nil {
		return nil, err
	}
	prepared, err := c.Prepare(kubeconfig)
	if err != nil {
		return nil, err
	}
	clientset, err := kubernetes.NewForConfig(prepared.Config)
	if err != nil {
		return nil, errors.New("could not initialize Kubernetes client for Kompose")
	}
	name := komposeJobName(spec.RunID)
	jobs := clientset.BatchV1().Jobs(spec.SystemNamespace)
	secrets := clientset.CoreV1().Secrets(spec.SystemNamespace)
	_ = jobs.Delete(ctx, name, metav1.DeleteOptions{PropagationPolicy: propagationPolicy(metav1.DeletePropagationBackground)})
	_ = secrets.Delete(ctx, name, metav1.DeleteOptions{})
	secret, job := komposeResources(spec, name)
	if _, err := secrets.Create(ctx, secret, metav1.CreateOptions{}); err != nil {
		return nil, fmt.Errorf("create Kompose input: %w", err)
	}
	defer func() {
		_ = jobs.Delete(context.WithoutCancel(ctx), name, metav1.DeleteOptions{PropagationPolicy: propagationPolicy(metav1.DeletePropagationBackground)})
		_ = secrets.Delete(context.WithoutCancel(ctx), name, metav1.DeleteOptions{})
	}()
	if _, err := jobs.Create(ctx, job, metav1.CreateOptions{}); err != nil {
		return nil, fmt.Errorf("create Kompose job: %w", err)
	}
	timeout := spec.Timeout
	if timeout <= 0 {
		timeout = defaultKomposeTimeout
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	podName, succeeded, err := waitForKomposeJob(waitCtx, clientset, spec.SystemNamespace, name)
	if err != nil {
		return nil, err
	}
	container := "manifests"
	if !succeeded {
		container = "converter"
	}
	limit := int64(maxKomposeOutputBytes)
	logs, logErr := clientset.CoreV1().Pods(spec.SystemNamespace).GetLogs(podName, &corev1.PodLogOptions{Container: container, LimitBytes: &limit}).DoRaw(ctx)
	if logErr != nil {
		return nil, fmt.Errorf("read Kompose %s output: %w", container, logErr)
	}
	if !succeeded {
		return nil, fmt.Errorf("Kompose conversion failed: %s", strings.TrimSpace(string(logs)))
	}
	if len(logs) == 0 || len(logs) >= maxKomposeOutputBytes {
		return nil, errors.New("Kompose produced no manifests or exceeded the 10 MiB output limit")
	}
	return logs, nil
}

func validateKomposeSpec(spec KomposeJobSpec) error {
	if len(validation.IsDNS1123Label(spec.SystemNamespace)) > 0 || len(validation.IsDNS1123Label(spec.TargetNamespace)) > 0 {
		return errors.New("Kompose system and target namespaces must be DNS labels")
	}
	if strings.TrimSpace(spec.RunID) == "" || len(spec.ComposeYAML) == 0 {
		return errors.New("Kompose run ID and compose.yaml are required")
	}
	for name, image := range map[string]string{"Kompose": spec.KomposeImage, "helper": spec.HelperImage} {
		if !strings.Contains(image, "@sha256:") {
			return fmt.Errorf("%s image must be pinned by sha256 digest", name)
		}
	}
	return nil
}

func komposeResources(spec KomposeJobSpec, name string) (*corev1.Secret, *batchv1.Job) {
	labels := map[string]string{
		"app.kubernetes.io/name":       "sks-migration-kompose",
		"app.kubernetes.io/managed-by": "sks-migration-center",
		"migration.smartx.com/run-id":  spec.RunID,
	}
	input := map[string][]byte{"compose.yaml": spec.ComposeYAML}
	if len(spec.EnvironmentFile) > 0 {
		input[".env"] = spec.EnvironmentFile
	}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: spec.SystemNamespace, Labels: labels}, Type: corev1.SecretTypeOpaque, Data: input}
	backoff, deadline, automount := int32(0), int64(300), false
	nonRoot, readOnly, privilegeEscalation := true, true, false
	uid, gid, mode := int64(65532), int64(65532), int32(0440)
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: spec.SystemNamespace, Labels: labels},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoff, ActiveDeadlineSeconds: &deadline,
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: labels}, Spec: corev1.PodSpec{
				AutomountServiceAccountToken: &automount, RestartPolicy: corev1.RestartPolicyNever,
				SecurityContext: &corev1.PodSecurityContext{RunAsNonRoot: &nonRoot, RunAsUser: &uid, RunAsGroup: &gid, FSGroup: &gid, SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}},
				InitContainers: []corev1.Container{{
					Name: "converter", Image: spec.KomposeImage, ImagePullPolicy: corev1.PullIfNotPresent, WorkingDir: "/input",
					Resources: dataMoverResources(),
					Command:   []string{"/bin/sh", "-ec"}, Args: []string{"kompose convert --file /input/compose.yaml --stdout > /output/manifests.yaml"},
					SecurityContext: &corev1.SecurityContext{AllowPrivilegeEscalation: &privilegeEscalation, ReadOnlyRootFilesystem: &readOnly, Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}},
					VolumeMounts:    []corev1.VolumeMount{{Name: "input", MountPath: "/input", ReadOnly: true}, {Name: "output", MountPath: "/output"}, {Name: "tmp", MountPath: "/tmp"}},
				}},
				Containers: []corev1.Container{{
					Name: "manifests", Image: spec.HelperImage, ImagePullPolicy: corev1.PullIfNotPresent,
					Resources: helperResources(),
					Command:   []string{"/bin/sh", "-ec"}, Args: []string{"cat /output/manifests.yaml"},
					SecurityContext: &corev1.SecurityContext{AllowPrivilegeEscalation: &privilegeEscalation, ReadOnlyRootFilesystem: &readOnly, Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}},
					VolumeMounts:    []corev1.VolumeMount{{Name: "output", MountPath: "/output", ReadOnly: true}},
				}},
				Volumes: []corev1.Volume{
					{Name: "input", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: name, DefaultMode: &mode}}},
					{Name: "output", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
					{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
				},
			}},
		},
	}
	return secret, job
}

func waitForKomposeJob(ctx context.Context, clientset kubernetes.Interface, namespace, name string) (string, bool, error) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		job, err := clientset.BatchV1().Jobs(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", false, fmt.Errorf("read Kompose job: %w", err)
		}
		done, succeeded := false, false
		for _, condition := range job.Status.Conditions {
			if condition.Status == corev1.ConditionTrue && (condition.Type == batchv1.JobComplete || condition.Type == batchv1.JobFailed) {
				done, succeeded = true, condition.Type == batchv1.JobComplete
				break
			}
		}
		if done {
			pods, listErr := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: "job-name=" + name})
			if listErr != nil || len(pods.Items) == 0 {
				if listErr == nil {
					listErr = errors.New("job pod was not found")
				}
				return "", false, fmt.Errorf("find Kompose pod: %w", listErr)
			}
			return pods.Items[0].Name, succeeded, nil
		}
		select {
		case <-ctx.Done():
			return "", false, fmt.Errorf("wait for Kompose job: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func komposeJobName(runID string) string {
	cleaned := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(runID), "-", ""))
	if len(cleaned) > 20 {
		cleaned = cleaned[:20]
	}
	return "smc-kompose-" + cleaned
}

func propagationPolicy(value metav1.DeletionPropagation) *metav1.DeletionPropagation { return &value }
