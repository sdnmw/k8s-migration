package kubernetes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8svalidation "k8s.io/apimachinery/pkg/util/validation"
	kubernetesclient "k8s.io/client-go/kubernetes"
)

type PVCStagingSpec struct {
	Namespace   string
	RunID       string
	PVCNames    []string
	HelperImage string
	Labels      map[string]string
}

type PVCStagingPod struct {
	Name string
	PVC  string
}

type ScalableWorkload struct {
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Replicas  int32  `json:"replicas"`
}

type NamespaceValidation struct {
	Deployments  int                        `json:"deployments"`
	StatefulSets int                        `json:"statefulSets"`
	PVCs         int                        `json:"pvcs"`
	Services     int                        `json:"services"`
	Ingresses    int                        `json:"ingresses"`
	Results      []ResourceValidationResult `json:"results"`
}

type ResourceValidationResult struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Message   string `json:"message,omitempty"`
}

func (c *Client) PreparePVCStaging(ctx context.Context, kubeconfig []byte, spec PVCStagingSpec) ([]PVCStagingPod, error) {
	if err := validateStagingSpec(spec); err != nil {
		return nil, err
	}
	clientset, err := c.clientset(kubeconfig)
	if err != nil {
		return nil, err
	}
	return preparePVCStaging(ctx, clientset, spec)
}

func (c *Client) DeletePVCStaging(ctx context.Context, kubeconfig []byte, namespace, runID string) error {
	clientset, err := c.clientset(kubeconfig)
	if err != nil {
		return err
	}
	return deletePVCStaging(ctx, clientset, namespace, runID)
}

func (c *Client) ListScalableWorkloads(ctx context.Context, kubeconfig []byte, namespace string) ([]ScalableWorkload, error) {
	clientset, err := c.clientset(kubeconfig)
	if err != nil {
		return nil, err
	}
	return listScalableWorkloads(ctx, clientset, namespace)
}

func (c *Client) ScaleWorkloads(ctx context.Context, kubeconfig []byte, values []ScalableWorkload) error {
	clientset, err := c.clientset(kubeconfig)
	if err != nil {
		return err
	}
	return scaleWorkloads(ctx, clientset, values)
}

func (c *Client) EnsureVeleroStorageClassMappings(ctx context.Context, kubeconfig []byte, namespace, runID string, mappings map[string]string) error {
	if len(mappings) == 0 {
		return nil
	}
	clientset, err := c.clientset(kubeconfig)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(mappings)
	if err != nil {
		return fmt.Errorf("encode Velero StorageClass mappings: %w", err)
	}
	configMaps := clientset.CoreV1().ConfigMaps(namespace)
	for attempt := 0; attempt < 5; attempt++ {
		current, getErr := configMaps.Get(ctx, storageMappingConfigName(runID), metav1.GetOptions{})
		if apierrors.IsNotFound(getErr) {
			desired := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
				Name: storageMappingConfigName(runID), Namespace: namespace,
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "sks-migration-center", "velero.io/plugin-config": "", "velero.io/change-storage-class": "RestoreItemAction",
				},
				Annotations: map[string]string{storageMappingRunAnnotation(runID): string(encoded)},
			}, Data: cloneStringMap(mappings)}
			if _, createErr := configMaps.Create(ctx, desired, metav1.CreateOptions{}); createErr == nil {
				return nil
			} else if apierrors.IsAlreadyExists(createErr) {
				continue
			} else {
				return fmt.Errorf("create Velero StorageClass mappings: %w", createErr)
			}
		}
		if getErr != nil {
			return fmt.Errorf("read Velero StorageClass mappings: %w", getErr)
		}
		if current.Labels["app.kubernetes.io/managed-by"] != "sks-migration-center" {
			return errors.New("Velero StorageClass mapping ConfigMap is not managed by SKS Migration Center")
		}
		current = current.DeepCopy()
		if current.Annotations == nil {
			current.Annotations = map[string]string{}
		}
		current.Annotations[storageMappingRunAnnotation(runID)] = string(encoded)
		merged, mergeErr := mergeStorageMappingOwners(current.Annotations)
		if mergeErr != nil {
			return mergeErr
		}
		current.Data = merged
		if _, updateErr := configMaps.Update(ctx, current, metav1.UpdateOptions{}); updateErr == nil {
			return nil
		} else if !apierrors.IsConflict(updateErr) {
			return fmt.Errorf("update Velero StorageClass mappings: %w", updateErr)
		}
	}
	return errors.New("ensure Velero StorageClass mappings exceeded conflict retries")
}

func (c *Client) DeleteVeleroStorageClassMappings(ctx context.Context, kubeconfig []byte, namespace, runID string) error {
	clientset, err := c.clientset(kubeconfig)
	if err != nil {
		return err
	}
	configMaps := clientset.CoreV1().ConfigMaps(namespace)
	for attempt := 0; attempt < 5; attempt++ {
		current, getErr := configMaps.Get(ctx, storageMappingConfigName(runID), metav1.GetOptions{})
		if apierrors.IsNotFound(getErr) {
			return nil
		}
		if getErr != nil {
			return fmt.Errorf("read Velero StorageClass mappings for cleanup: %w", getErr)
		}
		current = current.DeepCopy()
		delete(current.Annotations, storageMappingRunAnnotation(runID))
		merged, mergeErr := mergeStorageMappingOwners(current.Annotations)
		if mergeErr != nil {
			return mergeErr
		}
		if len(merged) == 0 {
			deleteErr := configMaps.Delete(ctx, current.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &current.UID, ResourceVersion: &current.ResourceVersion}})
			if deleteErr == nil || apierrors.IsNotFound(deleteErr) {
				return nil
			}
			if apierrors.IsConflict(deleteErr) {
				continue
			}
			return fmt.Errorf("delete Velero StorageClass mappings: %w", deleteErr)
		}
		current.Data = merged
		if _, updateErr := configMaps.Update(ctx, current, metav1.UpdateOptions{}); updateErr == nil {
			return nil
		} else if !apierrors.IsConflict(updateErr) {
			return fmt.Errorf("update Velero StorageClass mappings for cleanup: %w", updateErr)
		}
	}
	return errors.New("delete Velero StorageClass mappings exceeded conflict retries")
}

func (c *Client) ValidateNamespace(ctx context.Context, kubeconfig []byte, namespace string) (NamespaceValidation, error) {
	clientset, err := c.clientset(kubeconfig)
	if err != nil {
		return NamespaceValidation{}, err
	}
	return validateNamespaceWithClient(ctx, clientset, namespace)
}

func validateNamespaceWithClient(ctx context.Context, clientset kubernetesclient.Interface, namespace string) (NamespaceValidation, error) {
	result := NamespaceValidation{Results: []ResourceValidationResult{}}
	problems := []error{}
	deployments, err := clientset.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		problems = append(problems, fmt.Errorf("list restored Deployments: %w", err))
	} else {
		result.Deployments = len(deployments.Items)
		for index := range deployments.Items {
			value := &deployments.Items[index]
			desired := replicaValue(value.Spec.Replicas)
			validation := ResourceValidationResult{Kind: "Deployment", Namespace: namespace, Name: value.Name, Status: "SUCCEEDED", Message: fmt.Sprintf("desired=%d available=%d", desired, value.Status.AvailableReplicas)}
			if value.Status.ObservedGeneration < value.Generation || value.Status.UpdatedReplicas != desired || value.Status.AvailableReplicas != desired || value.Status.UnavailableReplicas != 0 {
				validation.Status = "FAILED"
				problem := fmt.Errorf("Deployment %s/%s is not ready: desired=%d available=%d", namespace, value.Name, desired, value.Status.AvailableReplicas)
				validation.Message = problem.Error()
				problems = append(problems, problem)
			}
			result.Results = append(result.Results, validation)
		}
	}
	statefulSets, err := clientset.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		problems = append(problems, fmt.Errorf("list restored StatefulSets: %w", err))
	} else {
		result.StatefulSets = len(statefulSets.Items)
		for index := range statefulSets.Items {
			value := &statefulSets.Items[index]
			desired := replicaValue(value.Spec.Replicas)
			validation := ResourceValidationResult{Kind: "StatefulSet", Namespace: namespace, Name: value.Name, Status: "SUCCEEDED", Message: fmt.Sprintf("desired=%d ready=%d", desired, value.Status.ReadyReplicas)}
			if value.Status.ObservedGeneration < value.Generation || value.Status.CurrentReplicas != desired || value.Status.UpdatedReplicas != desired || value.Status.ReadyReplicas != desired {
				validation.Status = "FAILED"
				problem := fmt.Errorf("StatefulSet %s/%s is not ready: desired=%d ready=%d", namespace, value.Name, desired, value.Status.ReadyReplicas)
				validation.Message = problem.Error()
				problems = append(problems, problem)
			}
			result.Results = append(result.Results, validation)
		}
	}
	pvcs, err := clientset.CoreV1().PersistentVolumeClaims(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		problems = append(problems, fmt.Errorf("list restored PVCs: %w", err))
	} else {
		result.PVCs = len(pvcs.Items)
		for index := range pvcs.Items {
			value := &pvcs.Items[index]
			validation := ResourceValidationResult{Kind: "PersistentVolumeClaim", Namespace: namespace, Name: value.Name, Status: "SUCCEEDED", Message: string(value.Status.Phase)}
			if value.Status.Phase != corev1.ClaimBound {
				validation.Status = "FAILED"
				problem := fmt.Errorf("PVC %s/%s is not Bound", namespace, value.Name)
				validation.Message = problem.Error()
				problems = append(problems, problem)
			}
			result.Results = append(result.Results, validation)
		}
	}
	services, err := clientset.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		problems = append(problems, fmt.Errorf("list restored Services: %w", err))
	} else {
		result.Services = len(services.Items)
		for index := range services.Items {
			value := &services.Items[index]
			status, message := "SUCCEEDED", "Service exists"
			if len(value.Spec.Selector) == 0 && value.Spec.Type != corev1.ServiceTypeExternalName {
				status, message = "WARNING", "Service has no selector"
			}
			result.Results = append(result.Results, ResourceValidationResult{Kind: "Service", Namespace: namespace, Name: value.Name, Status: status, Message: message})
		}
	}
	ingresses, err := clientset.NetworkingV1().Ingresses(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		problems = append(problems, fmt.Errorf("list restored Ingresses: %w", err))
	} else {
		result.Ingresses = len(ingresses.Items)
		for index := range ingresses.Items {
			value := &ingresses.Items[index]
			result.Results = append(result.Results, ResourceValidationResult{Kind: "Ingress", Namespace: namespace, Name: value.Name, Status: "SUCCEEDED", Message: "Ingress exists"})
		}
	}
	return result, errors.Join(problems...)
}

func validateStagingSpec(spec PVCStagingSpec) error {
	if len(k8svalidation.IsDNS1123Label(spec.Namespace)) > 0 || strings.TrimSpace(spec.RunID) == "" || strings.TrimSpace(spec.HelperImage) == "" {
		return errors.New("staging namespace, run ID and helper image are required")
	}
	for _, name := range spec.PVCNames {
		if len(k8svalidation.IsDNS1123Subdomain(name)) > 0 {
			return fmt.Errorf("invalid staging PVC name %q", name)
		}
	}
	return nil
}

func preparePVCStaging(ctx context.Context, clientset kubernetesclient.Interface, spec PVCStagingSpec) ([]PVCStagingPod, error) {
	pods, err := clientset.CoreV1().Pods(spec.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list Pods before PVC staging: %w", err)
	}
	mounted := map[string]bool{}
	for index := range pods.Items {
		pod := &pods.Items[index]
		if pod.DeletionTimestamp != nil || pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}
		if pod.Labels["migration.smartx.com/purpose"] == "pvc-staging" && pod.Labels["migration.smartx.com/run-id"] == spec.RunID {
			continue
		}
		for _, volume := range pod.Spec.Volumes {
			if volume.PersistentVolumeClaim != nil {
				mounted[volume.PersistentVolumeClaim.ClaimName] = true
			}
		}
	}
	unique := map[string]bool{}
	created := make([]PVCStagingPod, 0)
	for _, pvc := range spec.PVCNames {
		if mounted[pvc] || unique[pvc] {
			continue
		}
		unique[pvc] = true
		name := stagingPodName(spec.RunID, pvc)
		desired := stagingPod(spec, name, pvc)
		current, err := clientset.CoreV1().Pods(spec.Namespace).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			current, err = clientset.CoreV1().Pods(spec.Namespace).Create(ctx, desired, metav1.CreateOptions{})
		} else if err == nil && (current.Labels["migration.smartx.com/run-id"] != spec.RunID || current.Annotations["migration.smartx.com/pvc"] != pvc) {
			return nil, fmt.Errorf("staging Pod %s is owned by another migration", name)
		}
		if err != nil {
			return nil, fmt.Errorf("create staging Pod for PVC %s: %w", pvc, err)
		}
		if err := waitForStagingPod(ctx, clientset, spec.Namespace, current.Name); err != nil {
			return nil, err
		}
		created = append(created, PVCStagingPod{Name: name, PVC: pvc})
	}
	sort.Slice(created, func(i, j int) bool { return created[i].PVC < created[j].PVC })
	return created, nil
}

func deletePVCStaging(ctx context.Context, clientset kubernetesclient.Interface, namespace, runID string) error {
	if len(k8svalidation.IsDNS1123Label(namespace)) > 0 || strings.TrimSpace(runID) == "" {
		return errors.New("staging cleanup namespace and run ID are required")
	}
	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: "migration.smartx.com/run-id=" + runID + ",migration.smartx.com/purpose=pvc-staging"})
	if err != nil {
		return fmt.Errorf("list staging Pods for cleanup: %w", err)
	}
	zero := int64(0)
	for index := range pods.Items {
		if err := clientset.CoreV1().Pods(namespace).Delete(ctx, pods.Items[index].Name, metav1.DeleteOptions{GracePeriodSeconds: &zero}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete staging Pod %s: %w", pods.Items[index].Name, err)
		}
	}
	return nil
}

func stagingPod(spec PVCStagingSpec, name, pvc string) *corev1.Pod {
	runAs := int64(65532)
	allowEscalation, readOnlyRoot, automount := false, true, false
	labels := map[string]string{
		"app.kubernetes.io/managed-by": "sks-migration-center", "migration.smartx.com/purpose": "pvc-staging", "migration.smartx.com/run-id": spec.RunID,
	}
	for key, value := range spec.Labels {
		labels[key] = value
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: spec.Namespace,
			Labels: labels,
			Annotations: map[string]string{
				"backup.velero.io/backup-volumes": "data", "migration.smartx.com/pvc": pvc, "k8tz.io/inject": "false",
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyAlways, AutomountServiceAccountToken: &automount,
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot: boolPointer(true), RunAsUser: &runAs, RunAsGroup: &runAs,
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
			Containers: []corev1.Container{{
				Name: "staging", Image: spec.HelperImage, ImagePullPolicy: corev1.PullIfNotPresent,
				Resources: helperResources(),
				Command:   []string{"/bin/sh", "-c", "trap : TERM INT; sleep infinity & wait"},
				SecurityContext: &corev1.SecurityContext{
					AllowPrivilegeEscalation: &allowEscalation, ReadOnlyRootFilesystem: &readOnlyRoot,
					Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
				},
				VolumeMounts: []corev1.VolumeMount{{Name: "data", MountPath: "/staging"}},
			}},
			Volumes: []corev1.Volume{{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: pvc}}}},
		},
	}
}

func stagingPodName(runID, pvc string) string {
	digest := sha256.Sum256([]byte(runID + "\x00" + pvc))
	return "migration-staging-" + hex.EncodeToString(digest[:])[:16]
}

func storageMappingConfigName(runID string) string {
	return "sks-migration-storage-mappings"
}

func storageMappingRunAnnotation(runID string) string {
	digest := sha256.Sum256([]byte(runID))
	return "migration.smartx.com/run-" + hex.EncodeToString(digest[:])[:16]
}

func mergeStorageMappingOwners(annotations map[string]string) (map[string]string, error) {
	merged := map[string]string{}
	for key, encoded := range annotations {
		if !strings.HasPrefix(key, "migration.smartx.com/run-") {
			continue
		}
		var mappings map[string]string
		if err := json.Unmarshal([]byte(encoded), &mappings); err != nil {
			return nil, fmt.Errorf("decode Velero StorageClass mapping owner %s: %w", key, err)
		}
		for source, target := range mappings {
			if existing, ok := merged[source]; ok && existing != target {
				return nil, fmt.Errorf("Velero StorageClass mapping conflict for %s: %s and %s", source, existing, target)
			}
			merged[source] = target
		}
	}
	return merged, nil
}

func cloneStringMap(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func waitForStagingPod(ctx context.Context, clientset kubernetesclient.Interface, namespace, name string) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		pod, err := clientset.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			if pod.Status.Phase == corev1.PodRunning {
				return nil
			}
			if pod.Status.Phase == corev1.PodFailed {
				return fmt.Errorf("staging Pod %s failed: %s", name, podFailureMessage(pod))
			}
		}
		if err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("read staging Pod %s: %w", name, err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for staging Pod %s: %w", name, ctx.Err())
		case <-ticker.C:
		}
	}
}

func listScalableWorkloads(ctx context.Context, clientset kubernetesclient.Interface, namespace string) ([]ScalableWorkload, error) {
	deployments, err := clientset.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list Deployments for quiesce: %w", err)
	}
	statefulSets, err := clientset.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list StatefulSets for quiesce: %w", err)
	}
	result := make([]ScalableWorkload, 0, len(deployments.Items)+len(statefulSets.Items))
	for index := range deployments.Items {
		value := &deployments.Items[index]
		result = append(result, ScalableWorkload{Namespace: namespace, Kind: "Deployment", Name: value.Name, Replicas: replicaValue(value.Spec.Replicas)})
	}
	for index := range statefulSets.Items {
		value := &statefulSets.Items[index]
		result = append(result, ScalableWorkload{Namespace: namespace, Kind: "StatefulSet", Name: value.Name, Replicas: replicaValue(value.Spec.Replicas)})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Kind+"/"+result[i].Name < result[j].Kind+"/"+result[j].Name })
	return result, nil
}

func scaleWorkloads(ctx context.Context, clientset kubernetesclient.Interface, values []ScalableWorkload) error {
	// Update every workload before waiting for readiness. Compose applications
	// commonly have dependency cycles (for example Harbor core, registry and
	// jobservice); waiting for each workload immediately after scaling it can
	// deadlock startup while its dependencies are still held at zero replicas.
	deployments := make([]*appsv1.Deployment, 0, len(values))
	statefulSets := make([]*appsv1.StatefulSet, 0, len(values))
	for _, value := range values {
		if value.Replicas < 0 || len(k8svalidation.IsDNS1123Label(value.Namespace)) > 0 || len(k8svalidation.IsDNS1123Subdomain(value.Name)) > 0 {
			return errors.New("workload scale target is invalid")
		}
		switch value.Kind {
		case "Deployment":
			current, err := clientset.AppsV1().Deployments(value.Namespace).Get(ctx, value.Name, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("read Deployment %s/%s: %w", value.Namespace, value.Name, err)
			}
			current = current.DeepCopy()
			current.Spec.Replicas = int32Pointer(value.Replicas)
			updated, err := clientset.AppsV1().Deployments(value.Namespace).Update(ctx, current, metav1.UpdateOptions{})
			if err != nil {
				return fmt.Errorf("scale Deployment %s/%s: %w", value.Namespace, value.Name, err)
			}
			deployments = append(deployments, updated)
		case "StatefulSet":
			current, err := clientset.AppsV1().StatefulSets(value.Namespace).Get(ctx, value.Name, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("read StatefulSet %s/%s: %w", value.Namespace, value.Name, err)
			}
			current = current.DeepCopy()
			current.Spec.Replicas = int32Pointer(value.Replicas)
			updated, err := clientset.AppsV1().StatefulSets(value.Namespace).Update(ctx, current, metav1.UpdateOptions{})
			if err != nil {
				return fmt.Errorf("scale StatefulSet %s/%s: %w", value.Namespace, value.Name, err)
			}
			statefulSets = append(statefulSets, updated)
		default:
			return fmt.Errorf("unsupported scalable workload kind %s", value.Kind)
		}
	}
	for _, deployment := range deployments {
		if err := waitForDeploymentScale(ctx, clientset, deployment); err != nil {
			return err
		}
	}
	for _, statefulSet := range statefulSets {
		if err := waitForStatefulSetScale(ctx, clientset, statefulSet); err != nil {
			return err
		}
	}
	return nil
}

func waitForDeploymentScale(ctx context.Context, clientset kubernetesclient.Interface, desired *appsv1.Deployment) error {
	return waitForWorkloadScale(ctx, func(ctx context.Context) (bool, error) {
		current, err := clientset.AppsV1().Deployments(desired.Namespace).Get(ctx, desired.Name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		replicas := replicaValue(desired.Spec.Replicas)
		if replicas == 0 {
			return current.Status.Replicas == 0 && current.Status.ReadyReplicas == 0, nil
		}
		return current.Status.ObservedGeneration >= desired.Generation && current.Status.UpdatedReplicas == replicas && current.Status.AvailableReplicas == replicas && current.Status.UnavailableReplicas == 0, nil
	}, "Deployment "+desired.Namespace+"/"+desired.Name)
}

func waitForStatefulSetScale(ctx context.Context, clientset kubernetesclient.Interface, desired *appsv1.StatefulSet) error {
	return waitForWorkloadScale(ctx, func(ctx context.Context) (bool, error) {
		current, err := clientset.AppsV1().StatefulSets(desired.Namespace).Get(ctx, desired.Name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		replicas := replicaValue(desired.Spec.Replicas)
		if replicas == 0 {
			return current.Status.CurrentReplicas == 0 && current.Status.ReadyReplicas == 0, nil
		}
		return current.Status.ObservedGeneration >= desired.Generation && current.Status.CurrentReplicas == replicas && current.Status.UpdatedReplicas == replicas && current.Status.ReadyReplicas == replicas, nil
	}, "StatefulSet "+desired.Namespace+"/"+desired.Name)
}

func waitForWorkloadScale(ctx context.Context, ready func(context.Context) (bool, error), description string) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		ok, err := ready(ctx)
		if err != nil {
			return fmt.Errorf("read scaled %s: %w", description, err)
		}
		if ok {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for %s scale: %w", description, ctx.Err())
		case <-ticker.C:
		}
	}
}

func replicaValue(value *int32) int32 {
	if value == nil {
		return 1
	}
	return *value
}

func int32Pointer(value int32) *int32 { return &value }
