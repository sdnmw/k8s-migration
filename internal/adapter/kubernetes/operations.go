package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubernetesclient "k8s.io/client-go/kubernetes"
)

type VeleroInstallation struct {
	Exists         bool
	ServerImage    string
	NodeAgentImage string
	ManagedBy      string
	ReleaseName    string
}

func (c *Client) InspectVeleroInstallation(ctx context.Context, kubeconfig []byte, namespace string) (VeleroInstallation, error) {
	clientset, err := c.clientset(kubeconfig)
	if err != nil {
		return VeleroInstallation{}, err
	}
	deployment, err := clientset.AppsV1().Deployments(namespace).Get(ctx, "velero", metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return VeleroInstallation{}, nil
	}
	if err != nil {
		return VeleroInstallation{}, fmt.Errorf("inspect existing Velero Deployment: %w", err)
	}
	result := VeleroInstallation{Exists: true, ManagedBy: deployment.Labels["app.kubernetes.io/managed-by"], ReleaseName: deployment.Labels["app.kubernetes.io/instance"]}
	if len(deployment.Spec.Template.Spec.Containers) > 0 {
		result.ServerImage = deployment.Spec.Template.Spec.Containers[0].Image
	}
	nodeAgent, err := clientset.AppsV1().DaemonSets(namespace).Get(ctx, "node-agent", metav1.GetOptions{})
	if err == nil && len(nodeAgent.Spec.Template.Spec.Containers) > 0 {
		result.NodeAgentImage = nodeAgent.Spec.Template.Spec.Containers[0].Image
	} else if err != nil && !apierrors.IsNotFound(err) {
		return VeleroInstallation{}, fmt.Errorf("inspect existing Velero node-agent: %w", err)
	}
	return result, nil
}

func (c *Client) EnsureNamespace(ctx context.Context, kubeconfig []byte, name string) error {
	return c.ensureNamespace(ctx, kubeconfig, name, "restricted")
}

func (c *Client) EnsureAddonNamespace(ctx context.Context, kubeconfig []byte, name string) error {
	return c.ensureNamespace(ctx, kubeconfig, name, "privileged")
}

// EnsureMigrationNamespace permits Velero's restore-wait helper while keeping
// host namespaces and privileged containers disallowed.
func (c *Client) EnsureMigrationNamespace(ctx context.Context, kubeconfig []byte, name string) error {
	return c.ensureNamespace(ctx, kubeconfig, name, "baseline")
}

func (c *Client) ensureNamespace(ctx context.Context, kubeconfig []byte, name, podSecurityLevel string) error {
	if name == "" {
		return errors.New("namespace name is required")
	}
	if podSecurityLevel != "restricted" && podSecurityLevel != "baseline" && podSecurityLevel != "privileged" {
		return errors.New("unsupported namespace Pod Security level")
	}
	clientset, err := c.clientset(kubeconfig)
	if err != nil {
		return err
	}
	namespaces := clientset.CoreV1().Namespaces()
	existing, err := namespaces.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = namespaces.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by":               "sks-migration-center",
				"pod-security.kubernetes.io/enforce":         podSecurityLevel,
				"pod-security.kubernetes.io/enforce-version": "latest",
			},
			Annotations: map[string]string{"k8tz.io/inject": "false"},
		}}, metav1.CreateOptions{})
	} else if err == nil {
		existing = existing.DeepCopy()
		if existing.Labels == nil {
			existing.Labels = map[string]string{}
		}
		if existing.Annotations == nil {
			existing.Annotations = map[string]string{}
		}
		existing.Labels["app.kubernetes.io/managed-by"] = "sks-migration-center"
		existing.Labels["pod-security.kubernetes.io/enforce"] = podSecurityLevel
		existing.Labels["pod-security.kubernetes.io/enforce-version"] = "latest"
		existing.Annotations["k8tz.io/inject"] = "false"
		_, err = namespaces.Update(ctx, existing, metav1.UpdateOptions{})
	}
	if err != nil {
		return fmt.Errorf("ensure managed Kubernetes namespace: %w", err)
	}
	return nil
}

func (c *Client) PutOpaqueSecret(ctx context.Context, kubeconfig []byte, namespace, name string, data map[string][]byte) error {
	if namespace == "" || name == "" || len(data) == 0 {
		return errors.New("secret namespace, name and data are required")
	}
	clientset, err := c.clientset(kubeconfig)
	if err != nil {
		return err
	}
	secrets := clientset.CoreV1().Secrets(namespace)
	existing, err := secrets.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = secrets.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: map[string]string{"app.kubernetes.io/managed-by": "sks-migration-center"}},
			Type:       corev1.SecretTypeOpaque, Data: cloneSecretData(data),
		}, metav1.CreateOptions{})
	} else if err == nil {
		existing = existing.DeepCopy()
		existing.Type = corev1.SecretTypeOpaque
		existing.Data = cloneSecretData(data)
		if existing.Labels == nil {
			existing.Labels = map[string]string{}
		}
		existing.Labels["app.kubernetes.io/managed-by"] = "sks-migration-center"
		_, err = secrets.Update(ctx, existing, metav1.UpdateOptions{})
	}
	if err != nil {
		return fmt.Errorf("write managed Kubernetes Secret: %w", err)
	}
	return nil
}

func (c *Client) GetOpaqueSecret(ctx context.Context, kubeconfig []byte, namespace, name string) (map[string][]byte, error) {
	if namespace == "" || name == "" {
		return nil, errors.New("secret namespace and name are required")
	}
	clientset, err := c.clientset(kubeconfig)
	if err != nil {
		return nil, err
	}
	value, err := clientset.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("read managed Kubernetes Secret: %w", err)
	}
	return cloneSecretData(value.Data), nil
}

func (c *Client) RestartStatefulSet(ctx context.Context, kubeconfig []byte, namespace, name string) error {
	clientset, err := c.clientset(kubeconfig)
	if err != nil {
		return err
	}
	statefulSets := clientset.AppsV1().StatefulSets(namespace)
	value, err := statefulSets.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("read managed StatefulSet: %w", err)
	}
	value = value.DeepCopy()
	if value.Spec.Template.Annotations == nil {
		value.Spec.Template.Annotations = map[string]string{}
	}
	value.Spec.Template.Annotations["migration.smartx.com/restarted-at"] = time.Now().UTC().Format(time.RFC3339Nano)
	updated, err := statefulSets.Update(ctx, value, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("restart managed StatefulSet: %w", err)
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		current, err := statefulSets.Get(ctx, name, metav1.GetOptions{})
		if err == nil && statefulSetReady(current, updated.Generation) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for managed StatefulSet restart: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (c *Client) clientset(kubeconfig []byte) (kubernetesclient.Interface, error) {
	prepared, err := c.Prepare(kubeconfig)
	if err != nil {
		return nil, err
	}
	clientset, err := kubernetesclient.NewForConfig(prepared.Config)
	if err != nil {
		return nil, errors.New("could not initialize Kubernetes client")
	}
	return clientset, nil
}

func statefulSetReady(value *appsv1.StatefulSet, generation int64) bool {
	desired := int32(1)
	if value.Spec.Replicas != nil {
		desired = *value.Spec.Replicas
	}
	return value.Status.ObservedGeneration >= generation && value.Status.ReadyReplicas == desired &&
		value.Status.CurrentReplicas == desired && value.Status.UpdatedReplicas == desired &&
		value.Status.CurrentRevision != "" && value.Status.CurrentRevision == value.Status.UpdateRevision
}

func cloneSecretData(data map[string][]byte) map[string][]byte {
	result := make(map[string][]byte, len(data))
	for key, value := range data {
		result[key] = append([]byte(nil), value...)
	}
	return result
}
