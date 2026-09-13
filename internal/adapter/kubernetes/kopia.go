package kubernetes

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes"
)

const defaultKopiaRestoreTimeout = 30 * time.Minute

type KopiaRestoreSpec struct {
	SingleFile bool
	Namespace  string
	RunID      string
	PVC        string
	SnapshotID string
	Image      string
	Endpoint   string
	Bucket     string
	Region     string
	Prefix     string
	AccessKey  string
	SecretKey  string
	CABundle   string
	TLSVerify  bool
	Password   string
	Timeout    time.Duration
}

func (c *Client) RunKopiaRestoreJob(ctx context.Context, kubeconfig []byte, spec KopiaRestoreSpec) error {
	if err := validateKopiaRestoreSpec(spec); err != nil {
		return err
	}
	// Restoring ownership and permissions requires root. Compose migration
	// namespaces are therefore explicitly privileged during the restore phase.
	if err := c.EnsureAddonNamespace(ctx, kubeconfig, spec.Namespace); err != nil {
		return err
	}
	clientset, err := c.clientset(kubeconfig)
	if err != nil {
		return err
	}
	name := kopiaRestoreName(spec.RunID, spec.PVC)
	jobs := clientset.BatchV1().Jobs(spec.Namespace)
	secrets := clientset.CoreV1().Secrets(spec.Namespace)
	_ = jobs.Delete(ctx, name, metav1.DeleteOptions{PropagationPolicy: propagationPolicy(metav1.DeletePropagationBackground)})
	_ = secrets.Delete(ctx, name, metav1.DeleteOptions{})
	secret, job := kopiaRestoreResources(spec, name)
	if _, err := secrets.Create(ctx, secret, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("create Kopia restore credentials: %w", err)
	}
	defer func() {
		_ = jobs.Delete(context.WithoutCancel(ctx), name, metav1.DeleteOptions{PropagationPolicy: propagationPolicy(metav1.DeletePropagationBackground)})
		_ = secrets.Delete(context.WithoutCancel(ctx), name, metav1.DeleteOptions{})
	}()
	if _, err := jobs.Create(ctx, job, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("create Kopia restore job for PVC %s: %w", spec.PVC, err)
	}
	timeout := spec.Timeout
	if timeout <= 0 {
		timeout = defaultKopiaRestoreTimeout
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := waitForBatchJob(waitCtx, clientset, spec.Namespace, name); err != nil {
		return fmt.Errorf("restore PVC %s: %w", spec.PVC, err)
	}
	return nil
}

func validateKopiaRestoreSpec(spec KopiaRestoreSpec) error {
	parsed, err := url.Parse(strings.TrimSpace(spec.Endpoint))
	if len(validation.IsDNS1123Label(spec.Namespace)) > 0 || len(validation.IsDNS1123Subdomain(spec.PVC)) > 0 || spec.RunID == "" || spec.SnapshotID == "" {
		return errors.New("Kopia restore namespace, PVC, run and snapshot are required")
	}
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") {
		return errors.New("Kopia restore endpoint is invalid")
	}
	if !strings.Contains(spec.Image, "@sha256:") || spec.Bucket == "" || spec.AccessKey == "" || spec.SecretKey == "" || spec.Password == "" {
		return errors.New("pinned Kopia image and repository credentials are required")
	}
	return nil
}

func kopiaRestoreResources(spec KopiaRestoreSpec, name string) (*corev1.Secret, *batchv1.Job) {
	labels := map[string]string{"app.kubernetes.io/name": "sks-migration-kopia", "app.kubernetes.io/managed-by": "sks-migration-center", "migration.smartx.com/run-id": spec.RunID}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: spec.Namespace, Labels: labels}, Type: corev1.SecretTypeOpaque, StringData: map[string]string{
		"accessKey": spec.AccessKey, "secretKey": spec.SecretKey, "password": spec.Password, "caBundle": base64.StdEncoding.EncodeToString([]byte(spec.CABundle)),
	}}
	parsed, _ := url.Parse(spec.Endpoint)
	args := []string{"--bucket=" + spec.Bucket, "--endpoint=" + parsed.Host, "--region=" + spec.Region, "--prefix=" + strings.Trim(spec.Prefix, "/") + "/"}
	if parsed.Scheme == "http" || !spec.TLSVerify {
		args = append(args, "--disable-tls-verification")
	}
	restorePath := "/data"
	if spec.SingleFile {
		restorePath = "/data/content"
	}
	command := "if [ -n \"${ROOT_CA_PEM_BASE64:-}\" ]; then " +
		"printf %s \"$ROOT_CA_PEM_BASE64\" | base64 -d >/tmp/sks-migration-root-ca.pem; " +
		"export SSL_CERT_FILE=/tmp/sks-migration-root-ca.pem; fi; " +
		"kopia repository connect s3 " + joinShellArgs(args) + " >/dev/null; kopia snapshot restore " + shellArg(spec.SnapshotID) + " " + restorePath + " --delete-extra"
	backoff, deadline, automount, root, noEscalation := int32(1), int64(1800), false, int64(0), false
	env := []corev1.EnvVar{
		{Name: "AWS_ACCESS_KEY_ID", ValueFrom: secretEnv(name, "accessKey")},
		{Name: "AWS_SECRET_ACCESS_KEY", ValueFrom: secretEnv(name, "secretKey")},
		{Name: "KOPIA_PASSWORD", ValueFrom: secretEnv(name, "password")},
		{Name: "ROOT_CA_PEM_BASE64", ValueFrom: secretEnv(name, "caBundle")},
	}
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: spec.Namespace, Labels: labels}, Spec: batchv1.JobSpec{
		BackoffLimit: &backoff, ActiveDeadlineSeconds: &deadline,
		Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: labels}, Spec: corev1.PodSpec{
			AutomountServiceAccountToken: &automount, RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{{Name: "restore", Image: spec.Image, ImagePullPolicy: corev1.PullIfNotPresent, Command: []string{"/bin/sh", "-ec"}, Args: []string{command}, Env: env,
				Resources:       dataMoverResources(),
				SecurityContext: &corev1.SecurityContext{RunAsUser: &root, RunAsGroup: &root, AllowPrivilegeEscalation: &noEscalation},
				VolumeMounts:    []corev1.VolumeMount{{Name: "data", MountPath: "/data"}}}},
			Volumes: []corev1.Volume{{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: spec.PVC}}}},
		}},
	}}
	return secret, job
}

func waitForBatchJob(ctx context.Context, clientset kubernetes.Interface, namespace, name string) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		job, err := clientset.BatchV1().Jobs(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("read Job: %w", err)
		}
		for _, condition := range job.Status.Conditions {
			if condition.Status != corev1.ConditionTrue {
				continue
			}
			if condition.Type == batchv1.JobComplete {
				return nil
			}
			if condition.Type == batchv1.JobFailed {
				pods, listErr := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: "job-name=" + name})
				if listErr == nil && len(pods.Items) > 0 {
					limit := int64(16 * 1024)
					logs, logErr := clientset.CoreV1().Pods(namespace).GetLogs(pods.Items[0].Name, &corev1.PodLogOptions{LimitBytes: &limit}).DoRaw(ctx)
					if logErr == nil {
						return fmt.Errorf("Job failed: %s", strings.TrimSpace(string(logs)))
					}
				}
				return errors.New("Job failed")
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func secretEnv(name, key string) *corev1.EnvVarSource {
	return &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: name}, Key: key}}
}

func kopiaRestoreName(runID, pvc string) string {
	value := strings.ToLower(strings.ReplaceAll(runID, "-", ""))
	if len(value) > 12 {
		value = value[:12]
	}
	name := "smc-kopia-" + value + "-" + strings.ToLower(pvc)
	if len(name) > 63 {
		name = strings.TrimRight(name[:63], "-")
	}
	return name
}

func joinShellArgs(values []string) string {
	result := make([]string, len(values))
	for index := range values {
		result[index] = shellArg(values[index])
	}
	return strings.Join(result, " ")
}

func shellArg(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
