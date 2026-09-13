package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
)

type EndpointValidationSpec struct {
	Namespace   string
	RunID       string
	HTTPChecks  []string
	TCPChecks   []string
	HelperImage string
	Timeout     time.Duration
}

func (c *Client) ValidateEndpoints(ctx context.Context, kubeconfig []byte, spec EndpointValidationSpec) error {
	if err := validateEndpointValidationSpec(spec); err != nil {
		return err
	}
	if len(spec.HTTPChecks) == 0 && len(spec.TCPChecks) == 0 {
		return nil
	}
	if err := c.EnsureNamespace(ctx, kubeconfig, spec.Namespace); err != nil {
		return err
	}
	clientset, err := c.clientset(kubeconfig)
	if err != nil {
		return err
	}
	name := endpointValidationJobName(spec.RunID)
	jobs := clientset.BatchV1().Jobs(spec.Namespace)
	_ = jobs.Delete(ctx, name, metav1.DeleteOptions{PropagationPolicy: propagationPolicy(metav1.DeletePropagationBackground)})
	job := endpointValidationJob(spec, name)
	defer func() {
		_ = jobs.Delete(context.WithoutCancel(ctx), name, metav1.DeleteOptions{PropagationPolicy: propagationPolicy(metav1.DeletePropagationBackground)})
	}()
	if _, err := jobs.Create(ctx, job, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("create endpoint validation Job: %w", err)
	}
	timeout := spec.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return waitForBatchJob(waitCtx, clientset, spec.Namespace, name)
}

func validateEndpointValidationSpec(spec EndpointValidationSpec) error {
	if len(validation.IsDNS1123Label(spec.Namespace)) > 0 || strings.TrimSpace(spec.RunID) == "" || !strings.Contains(spec.HelperImage, "@sha256:") {
		return errors.New("validation namespace, run ID and digest-pinned helper image are required")
	}
	if len(spec.HTTPChecks)+len(spec.TCPChecks) > 64 {
		return errors.New("endpoint validation is limited to 64 checks")
	}
	for _, value := range spec.HTTPChecks {
		parsed, err := url.Parse(strings.TrimSpace(value))
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
			return fmt.Errorf("invalid HTTP validation URL %q", value)
		}
	}
	for _, value := range spec.TCPChecks {
		host, port, err := net.SplitHostPort(strings.TrimSpace(value))
		parsedPort, parseErr := strconv.Atoi(port)
		if err != nil || host == "" || parseErr != nil || parsedPort < 1 || parsedPort > 65535 {
			return fmt.Errorf("invalid TCP validation endpoint %q; expected host:port", value)
		}
	}
	return nil
}

func endpointValidationJob(spec EndpointValidationSpec, name string) *batchv1.Job {
	commands := []string{"set -eu"}
	for _, value := range spec.HTTPChecks {
		commands = append(commands, "wget -q --spider --timeout=15 --no-check-certificate "+shellArg(value))
	}
	for _, value := range spec.TCPChecks {
		host, port, _ := net.SplitHostPort(value)
		commands = append(commands, "nc -z -w 15 "+shellArg(host)+" "+shellArg(port))
	}
	labels := map[string]string{"app.kubernetes.io/name": "sks-migration-validation", "app.kubernetes.io/managed-by": "sks-migration-center", "migration.smartx.com/run-id": spec.RunID}
	backoff, deadline, automount := int32(1), int64(300), false
	nonRoot, noEscalation, readOnly := true, false, true
	uid := int64(65532)
	return &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: spec.Namespace, Labels: labels}, Spec: batchv1.JobSpec{
		BackoffLimit: &backoff, ActiveDeadlineSeconds: &deadline,
		Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: labels}, Spec: corev1.PodSpec{
			AutomountServiceAccountToken: &automount, RestartPolicy: corev1.RestartPolicyNever,
			SecurityContext: &corev1.PodSecurityContext{RunAsNonRoot: &nonRoot, RunAsUser: &uid, SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}},
			Containers: []corev1.Container{{Name: "checks", Image: spec.HelperImage, ImagePullPolicy: corev1.PullIfNotPresent, Command: []string{"/bin/sh", "-ec"}, Args: []string{strings.Join(commands, "; ")},
				Resources:       helperResources(),
				SecurityContext: &corev1.SecurityContext{AllowPrivilegeEscalation: &noEscalation, ReadOnlyRootFilesystem: &readOnly, Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}}}},
		}},
	}}
}

func endpointValidationJobName(runID string) string {
	value := strings.ToLower(strings.ReplaceAll(runID, "-", ""))
	if len(value) > 20 {
		value = value[:20]
	}
	return "smc-validate-" + value
}
