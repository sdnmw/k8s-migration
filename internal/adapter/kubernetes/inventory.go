package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/smartx/sks-migration-center/internal/domain/application"
)

var namespaceResources = []schema.GroupVersionResource{
	{Version: "v1", Resource: "services"}, {Version: "v1", Resource: "configmaps"},
	{Version: "v1", Resource: "secrets"}, {Version: "v1", Resource: "serviceaccounts"},
	{Version: "v1", Resource: "persistentvolumeclaims"},
	{Group: "apps", Version: "v1", Resource: "deployments"},
	{Group: "apps", Version: "v1", Resource: "statefulsets"},
	{Group: "apps", Version: "v1", Resource: "daemonsets"},
	{Group: "batch", Version: "v1", Resource: "jobs"},
	{Group: "batch", Version: "v1", Resource: "cronjobs"},
	{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"},
	{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"},
	{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "roles"},
	{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "rolebindings"},
	{Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers"},
	{Group: "policy", Version: "v1", Resource: "poddisruptionbudgets"},
}

func (c *Client) DiscoverNamespace(ctx context.Context, kubeconfig []byte, namespace string) (application.Inventory, error) {
	return c.discoverNamespace(ctx, kubeconfig, namespace, true)
}

// DiscoverNamespaceCore is used while scanning every Namespace for
// applications. Listing every CRD instance for every Namespace is both
// unnecessary at this stage and can consume hundreds of MiB on clusters with
// many operators. The selected Namespace still uses DiscoverNamespace and
// therefore receives the complete CR/CRD inventory before assessment.
func (c *Client) DiscoverNamespaceCore(ctx context.Context, kubeconfig []byte, namespace string) (application.Inventory, error) {
	return c.discoverNamespace(ctx, kubeconfig, namespace, false)
}

func (c *Client) discoverNamespace(ctx context.Context, kubeconfig []byte, namespace string, includeCustomResources bool) (application.Inventory, error) {
	namespace = strings.TrimSpace(namespace)
	if problems := validation.IsDNS1123Label(namespace); len(problems) > 0 {
		return application.Inventory{}, errors.New("namespace must be a valid DNS label")
	}
	prepared, err := c.Prepare(kubeconfig)
	if err != nil {
		return application.Inventory{}, err
	}
	clientset, err := kubernetes.NewForConfig(prepared.Config)
	if err != nil {
		return application.Inventory{}, errors.New("could not initialize Kubernetes inventory client")
	}
	if _, err := clientset.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			return application.Inventory{}, errors.New("namespace does not exist")
		}
		return application.Inventory{}, errors.New(permissionMessage(err))
	}
	dynamicClient, err := dynamic.NewForConfig(prepared.Config)
	if err != nil {
		return application.Inventory{}, errors.New("could not initialize Kubernetes dynamic inventory client")
	}
	objects := make([]unstructured.Unstructured, 0)
	warnings := make([]application.InventoryWarning, 0)
	for _, gvr := range namespaceResources {
		items, listErr := listNamespaceObjects(ctx, dynamicClient, gvr, namespace)
		if listErr != nil {
			if !apierrors.IsNotFound(listErr) && !apierrors.IsMethodNotSupported(listErr) {
				warnings = append(warnings, application.InventoryWarning{Code: "RESOURCE_LIST_FAILED", Resource: gvr.String(), Message: permissionMessage(listErr)})
			}
			continue
		}
		objects = append(objects, items...)
	}
	var crdSummaries []application.ResourceSummary
	if includeCustomResources {
		var customObjects []unstructured.Unstructured
		var crdWarnings []application.InventoryWarning
		crdSummaries, customObjects, crdWarnings = discoverCustomResources(ctx, dynamicClient, namespace)
		objects = append(objects, customObjects...)
		warnings = append(warnings, crdWarnings...)
	}
	return buildInventory(objects, crdSummaries, warnings), nil
}

func listNamespaceObjects(ctx context.Context, client dynamic.Interface, gvr schema.GroupVersionResource, namespace string) ([]unstructured.Unstructured, error) {
	result := make([]unstructured.Unstructured, 0)
	continueToken := ""
	for {
		list, err := client.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{Limit: 500, Continue: continueToken})
		if err != nil {
			return nil, err
		}
		result = append(result, list.Items...)
		continueToken = list.GetContinue()
		if continueToken == "" {
			return result, nil
		}
	}
}

func discoverCustomResources(ctx context.Context, client dynamic.Interface, namespace string) ([]application.ResourceSummary, []unstructured.Unstructured, []application.InventoryWarning) {
	crdGVR := schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
	list, err := client.Resource(crdGVR).List(ctx, metav1.ListOptions{Limit: 500})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil, nil
		}
		return nil, nil, []application.InventoryWarning{{Code: "CRD_DISCOVERY_FAILED", Resource: crdGVR.String(), Message: permissionMessage(err)}}
	}
	definitions := make([]application.ResourceSummary, 0)
	objects := make([]unstructured.Unstructured, 0)
	warnings := make([]application.InventoryWarning, 0)
	for _, crd := range list.Items {
		if scope, _, _ := unstructured.NestedString(crd.Object, "spec", "scope"); scope != "Namespaced" {
			continue
		}
		group, _, _ := unstructured.NestedString(crd.Object, "spec", "group")
		plural, _, _ := unstructured.NestedString(crd.Object, "spec", "names", "plural")
		kind, _, _ := unstructured.NestedString(crd.Object, "spec", "names", "kind")
		version := servedCRDVersion(crd.Object)
		if group == "" || plural == "" || kind == "" || version == "" {
			warnings = append(warnings, application.InventoryWarning{Code: "CRD_INVALID", Resource: crd.GetName(), Message: "CRD 缺少可服务的版本或资源名称"})
			continue
		}
		gvr := schema.GroupVersionResource{Group: group, Version: version, Resource: plural}
		items, listErr := listNamespaceObjects(ctx, client, gvr, namespace)
		if listErr != nil {
			warnings = append(warnings, application.InventoryWarning{Code: "CUSTOM_RESOURCE_LIST_FAILED", Resource: gvr.String(), Message: permissionMessage(listErr)})
			continue
		}
		if len(items) == 0 {
			continue
		}
		definitions = append(definitions, application.ResourceSummary{APIVersion: "apiextensions.k8s.io/v1", Kind: "CustomResourceDefinition", Name: crd.GetName()})
		objects = append(objects, items...)
	}
	return definitions, objects, warnings
}

func servedCRDVersion(object map[string]any) string {
	versions, _, _ := unstructured.NestedSlice(object, "spec", "versions")
	for _, candidate := range versions {
		value, _ := candidate.(map[string]any)
		if storage, _, _ := unstructured.NestedBool(value, "storage"); storage {
			return stringValue(value["name"])
		}
	}
	for _, candidate := range versions {
		value, _ := candidate.(map[string]any)
		if served, _, _ := unstructured.NestedBool(value, "served"); served {
			return stringValue(value["name"])
		}
	}
	return ""
}

func buildInventory(objects []unstructured.Unstructured, crds []application.ResourceSummary, warnings []application.InventoryWarning) application.Inventory {
	result := application.Inventory{
		Resources: []application.ResourceSummary{}, Workloads: []application.ResourceSummary{}, Services: []application.ResourceSummary{},
		Ingresses: []application.ResourceSummary{}, ConfigMaps: []application.ResourceSummary{}, Secrets: []application.ResourceSummary{},
		ServiceAccounts: []application.ResourceSummary{}, Roles: []application.ResourceSummary{}, RoleBindings: []application.ResourceSummary{},
		CRDs: crds, CustomResources: []application.ResourceSummary{}, PVCs: []application.VolumeSummary{}, Images: []application.ImageSummary{},
		Dependencies: []application.Dependency{}, Warnings: warnings, Counts: map[string]int{},
	}
	workloadLabels := make(map[string]map[string]string)
	imageSet := map[string]struct{}{}
	for _, object := range objects {
		summary := resourceSummary(object)
		result.Resources = append(result.Resources, summary)
		result.Counts[summary.Kind]++
		for _, image := range summary.Images {
			imageSet[image] = struct{}{}
		}
		switch summary.Kind {
		case "Deployment", "StatefulSet", "DaemonSet", "Job", "CronJob":
			result.Workloads = append(result.Workloads, summary)
			workloadLabels[referenceKey(referenceFor(object))] = podTemplateLabels(object)
		case "Service":
			result.Services = append(result.Services, summary)
		case "Ingress":
			result.Ingresses = append(result.Ingresses, summary)
		case "ConfigMap":
			result.ConfigMaps = append(result.ConfigMaps, summary)
		case "Secret":
			result.Secrets = append(result.Secrets, summary)
		case "ServiceAccount":
			result.ServiceAccounts = append(result.ServiceAccounts, summary)
		case "Role":
			result.Roles = append(result.Roles, summary)
		case "RoleBinding":
			result.RoleBindings = append(result.RoleBindings, summary)
		case "PersistentVolumeClaim":
			result.PVCs = append(result.PVCs, volumeSummary(object))
		default:
			if isCustomAPI(summary.APIVersion) {
				result.CustomResources = append(result.CustomResources, summary)
			}
		}
		result.Dependencies = append(result.Dependencies, objectDependencies(object)...)
	}
	for _, object := range objects {
		if object.GetKind() != "Service" {
			continue
		}
		selector, _, _ := unstructured.NestedStringMap(object.Object, "spec", "selector")
		if len(selector) == 0 {
			continue
		}
		for key, labels := range workloadLabels {
			if labelsMatch(selector, labels) {
				result.Dependencies = append(result.Dependencies, application.Dependency{From: referenceFor(object), To: referenceFromKey(key), Type: "SELECTS", Required: true})
			}
		}
	}
	for image := range imageSet {
		result.Images = append(result.Images, application.ImageSummary{Reference: image})
	}
	sortInventory(&result)
	return result
}

func resourceSummary(object unstructured.Unstructured) application.ResourceSummary {
	requests, limits := podResourceTotals(object)
	summary := application.ResourceSummary{APIVersion: object.GetAPIVersion(), Kind: object.GetKind(), Namespace: object.GetNamespace(), Name: object.GetName(), Labels: object.GetLabels(), Images: podImages(object), Requests: requests, Limits: limits, MissingRequests: missingContainerRequests(object), SecurityRisks: podSecurityRisks(object)}
	if spec := podSpec(object); spec != nil {
		summary.NodeSelectors, _, _ = unstructured.NestedStringMap(spec, "nodeSelector")
		summary.NFSSources = podNFSSources(spec)
	}
	if replicas, found, _ := unstructured.NestedInt64(object.Object, "spec", "replicas"); found {
		summary.Replicas = &replicas
	}
	if replicas, found, _ := unstructured.NestedInt64(object.Object, "status", "readyReplicas"); found {
		summary.ReadyReplicas = &replicas
	}
	if replicas, found, _ := unstructured.NestedInt64(object.Object, "status", "availableReplicas"); found {
		summary.AvailableReplicas = &replicas
	}
	if object.GetKind() == "ConfigMap" {
		summary.DataKeys = sortedNestedMapKeys(object.Object, "data", "binaryData")
	}
	if object.GetKind() == "Secret" {
		summary.SecretKeys = sortedNestedMapKeys(object.Object, "data", "stringData")
	}
	if object.GetKind() == "Ingress" {
		summary.IngressClassName, _, _ = unstructured.NestedString(object.Object, "spec", "ingressClassName")
	}
	if object.GetKind() == "Service" {
		summary.ServiceType, _, _ = unstructured.NestedString(object.Object, "spec", "type")
		if summary.ServiceType == "" {
			summary.ServiceType = "ClusterIP"
		}
	}
	return summary
}

func podNFSSources(spec map[string]any) []string {
	values := []string{}
	volumes, _, _ := unstructured.NestedSlice(spec, "volumes")
	for _, item := range volumes {
		volume, _ := item.(map[string]any)
		nfs, _ := volume["nfs"].(map[string]any)
		server, _ := nfs["server"].(string)
		path, _ := nfs["path"].(string)
		if server != "" && path != "" {
			values = append(values, server+":"+path)
		}
	}
	return uniqueSorted(values)
}

func podSecurityRisks(object unstructured.Unstructured) []string {
	spec := podSpec(object)
	if spec == nil {
		return nil
	}
	risks := map[string]struct{}{}
	for field, code := range map[string]string{"hostNetwork": "HOST_NETWORK", "hostPID": "HOST_PID", "hostIPC": "HOST_IPC"} {
		if enabled, _, _ := unstructured.NestedBool(spec, field); enabled {
			risks[code] = struct{}{}
		}
	}
	volumes, _, _ := unstructured.NestedSlice(spec, "volumes")
	for _, item := range volumes {
		volume, _ := item.(map[string]any)
		if _, found := volume["hostPath"]; found {
			risks["HOST_PATH"] = struct{}{}
		}
	}
	for _, field := range []string{"initContainers", "containers"} {
		containers, _, _ := unstructured.NestedSlice(spec, field)
		for _, item := range containers {
			container, _ := item.(map[string]any)
			securityContext, _ := container["securityContext"].(map[string]any)
			if privileged, _ := securityContext["privileged"].(bool); privileged {
				risks["PRIVILEGED"] = struct{}{}
			}
			ports, _ := container["ports"].([]any)
			for _, rawPort := range ports {
				port, _ := rawPort.(map[string]any)
				if hostPort, ok := port["hostPort"].(float64); ok && hostPort > 0 {
					risks["HOST_PORT"] = struct{}{}
				}
				if hostPort, ok := port["hostPort"].(int64); ok && hostPort > 0 {
					risks["HOST_PORT"] = struct{}{}
				}
			}
		}
	}
	result := make([]string, 0, len(risks))
	for risk := range risks {
		result = append(result, risk)
	}
	sort.Strings(result)
	return result
}

func missingContainerRequests(object unstructured.Unstructured) []string {
	spec := podSpec(object)
	if spec == nil {
		return nil
	}
	missing := map[string]struct{}{}
	for _, field := range []string{"initContainers", "containers"} {
		containers, _, _ := unstructured.NestedSlice(spec, field)
		for _, item := range containers {
			values := containerResources(item, "requests")
			for _, name := range []string{"cpu", "memory"} {
				if _, found := values[name]; !found {
					missing[name] = struct{}{}
				}
			}
		}
	}
	result := make([]string, 0, len(missing))
	for name := range missing {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func podResourceTotals(object unstructured.Unstructured) (map[string]string, map[string]string) {
	spec := podSpec(object)
	if spec == nil {
		return nil, nil
	}
	requests := effectivePodResources(spec, "requests")
	limits := effectivePodResources(spec, "limits")
	return quantityStrings(requests), quantityStrings(limits)
}

// effectivePodResources follows Kubernetes scheduling semantics: regular
// containers are summed while init containers contribute their per-resource
// maximum. Pod overhead is added after taking the larger value.
func effectivePodResources(spec map[string]any, resourceType string) map[string]resource.Quantity {
	regular := sumContainerResources(spec, "containers", resourceType)
	initMaximum := maxContainerResources(spec, "initContainers", resourceType)
	result := map[string]resource.Quantity{}
	for name, quantity := range regular {
		result[name] = quantity
	}
	for name, quantity := range initMaximum {
		current, found := result[name]
		if !found || quantity.Cmp(current) > 0 {
			result[name] = quantity
		}
	}
	overhead, _, _ := unstructured.NestedStringMap(spec, "overhead")
	for name, raw := range overhead {
		quantity, err := resource.ParseQuantity(raw)
		if err != nil {
			continue
		}
		total := result[name]
		total.Add(quantity)
		result[name] = total
	}
	return result
}

func sumContainerResources(spec map[string]any, field, resourceType string) map[string]resource.Quantity {
	result := map[string]resource.Quantity{}
	containers, _, _ := unstructured.NestedSlice(spec, field)
	for _, item := range containers {
		for name, quantity := range containerResources(item, resourceType) {
			total := result[name]
			total.Add(quantity)
			result[name] = total
		}
	}
	return result
}

func maxContainerResources(spec map[string]any, field, resourceType string) map[string]resource.Quantity {
	result := map[string]resource.Quantity{}
	containers, _, _ := unstructured.NestedSlice(spec, field)
	for _, item := range containers {
		for name, quantity := range containerResources(item, resourceType) {
			current, found := result[name]
			if !found || quantity.Cmp(current) > 0 {
				result[name] = quantity
			}
		}
	}
	return result
}

func containerResources(item any, resourceType string) map[string]resource.Quantity {
	result := map[string]resource.Quantity{}
	container, _ := item.(map[string]any)
	resources, _ := container["resources"].(map[string]any)
	values, _ := resources[resourceType].(map[string]any)
	for name, raw := range values {
		quantity, err := resource.ParseQuantity(stringValue(raw))
		if err == nil {
			result[name] = quantity
		}
	}
	return result
}

func quantityStrings(values map[string]resource.Quantity) map[string]string {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]string, len(values))
	for name, value := range values {
		result[name] = value.String()
	}
	return result
}

func volumeSummary(object unstructured.Unstructured) application.VolumeSummary {
	request, _, _ := unstructured.NestedString(object.Object, "spec", "resources", "requests", "storage")
	capacity := int64(0)
	if quantity, err := resource.ParseQuantity(request); err == nil {
		capacity = quantity.Value()
	}
	storageClass, _, _ := unstructured.NestedString(object.Object, "spec", "storageClassName")
	volumeMode, _, _ := unstructured.NestedString(object.Object, "spec", "volumeMode")
	accessModes, _, _ := unstructured.NestedStringSlice(object.Object, "spec", "accessModes")
	phase, _, _ := unstructured.NestedString(object.Object, "status", "phase")
	return application.VolumeSummary{Name: object.GetName(), Namespace: object.GetNamespace(), CapacityBytes: capacity, StorageClassName: storageClass, AccessModes: accessModes, VolumeMode: volumeMode, Phase: phase}
}

func objectDependencies(object unstructured.Unstructured) []application.Dependency {
	from := referenceFor(object)
	result := make([]application.Dependency, 0)
	add := func(to application.ResourceReference, dependencyType string, required bool) {
		if to.Name != "" {
			result = append(result, application.Dependency{From: from, To: to, Type: dependencyType, Required: required})
		}
	}
	for _, owner := range object.GetOwnerReferences() {
		add(application.ResourceReference{APIVersion: owner.APIVersion, Kind: owner.Kind, Namespace: object.GetNamespace(), Name: owner.Name}, "OWNED_BY", true)
	}
	if podSpec := podSpec(object); podSpec != nil {
		serviceAccount, _, _ := unstructured.NestedString(podSpec, "serviceAccountName")
		if serviceAccount != "" && serviceAccount != "default" {
			add(application.ResourceReference{APIVersion: "v1", Kind: "ServiceAccount", Namespace: object.GetNamespace(), Name: serviceAccount}, "USES_SERVICE_ACCOUNT", true)
		}
		imagePullSecrets, _, _ := unstructured.NestedSlice(podSpec, "imagePullSecrets")
		for _, item := range imagePullSecrets {
			secret, _ := item.(map[string]any)
			add(application.ResourceReference{APIVersion: "v1", Kind: "Secret", Namespace: object.GetNamespace(), Name: stringValue(secret["name"])}, "USES_IMAGE_PULL_SECRET", true)
		}
		volumes, _, _ := unstructured.NestedSlice(podSpec, "volumes")
		for _, item := range volumes {
			volume, _ := item.(map[string]any)
			for field, kind := range map[string]string{"persistentVolumeClaim": "PersistentVolumeClaim", "configMap": "ConfigMap", "secret": "Secret"} {
				if source, ok := volume[field].(map[string]any); ok {
					nameField := "name"
					if field == "persistentVolumeClaim" {
						nameField = "claimName"
					}
					add(application.ResourceReference{APIVersion: "v1", Kind: kind, Namespace: object.GetNamespace(), Name: stringValue(source[nameField])}, "MOUNTS", true)
				}
			}
		}
		for _, field := range []string{"initContainers", "containers"} {
			containers, _, _ := unstructured.NestedSlice(podSpec, field)
			for _, item := range containers {
				container, _ := item.(map[string]any)
				appendEnvironmentDependencies(&result, from, object.GetNamespace(), container)
			}
		}
	}
	if object.GetKind() == "Ingress" {
		appendIngressDependencies(&result, from, object)
	}
	if object.GetKind() == "RoleBinding" {
		roleRef, _, _ := unstructured.NestedMap(object.Object, "roleRef")
		kind := stringValue(roleRef["kind"])
		namespace := object.GetNamespace()
		if kind == "ClusterRole" {
			namespace = ""
		}
		add(application.ResourceReference{APIVersion: "rbac.authorization.k8s.io/v1", Kind: kind, Namespace: namespace, Name: stringValue(roleRef["name"])}, "BINDS_ROLE", true)
		subjects, _, _ := unstructured.NestedSlice(object.Object, "subjects")
		for _, item := range subjects {
			subject, _ := item.(map[string]any)
			if stringValue(subject["kind"]) == "ServiceAccount" {
				subjectNamespace := stringValue(subject["namespace"])
				if subjectNamespace == "" {
					subjectNamespace = object.GetNamespace()
				}
				add(application.ResourceReference{APIVersion: "v1", Kind: "ServiceAccount", Namespace: subjectNamespace, Name: stringValue(subject["name"])}, "GRANTS_TO", true)
			}
		}
	}
	if object.GetKind() == "HorizontalPodAutoscaler" {
		target, _, _ := unstructured.NestedMap(object.Object, "spec", "scaleTargetRef")
		add(application.ResourceReference{APIVersion: stringValue(target["apiVersion"]), Kind: stringValue(target["kind"]), Namespace: object.GetNamespace(), Name: stringValue(target["name"])}, "SCALES", true)
	}
	if object.GetKind() == "PersistentVolumeClaim" {
		storageClass, _, _ := unstructured.NestedString(object.Object, "spec", "storageClassName")
		add(application.ResourceReference{APIVersion: "storage.k8s.io/v1", Kind: "StorageClass", Name: storageClass}, "USES_STORAGE_CLASS", true)
	}
	return result
}

func appendEnvironmentDependencies(result *[]application.Dependency, from application.ResourceReference, namespace string, container map[string]any) {
	add := func(kind, name, dependencyType string, required bool) {
		if name != "" {
			*result = append(*result, application.Dependency{From: from, To: application.ResourceReference{APIVersion: "v1", Kind: kind, Namespace: namespace, Name: name}, Type: dependencyType, Required: required})
		}
	}
	envFrom, _, _ := unstructured.NestedSlice(container, "envFrom")
	for _, item := range envFrom {
		source, _ := item.(map[string]any)
		for field, kind := range map[string]string{"configMapRef": "ConfigMap", "secretRef": "Secret"} {
			if reference, ok := source[field].(map[string]any); ok {
				optional, _ := reference["optional"].(bool)
				add(kind, stringValue(reference["name"]), "READS_ENV_FROM", !optional)
			}
		}
	}
	env, _, _ := unstructured.NestedSlice(container, "env")
	for _, item := range env {
		variable, _ := item.(map[string]any)
		valueFrom, _ := variable["valueFrom"].(map[string]any)
		for field, kind := range map[string]string{"configMapKeyRef": "ConfigMap", "secretKeyRef": "Secret"} {
			if reference, ok := valueFrom[field].(map[string]any); ok {
				optional, _ := reference["optional"].(bool)
				add(kind, stringValue(reference["name"]), "READS_ENV_KEY", !optional)
			}
		}
	}
}

func appendIngressDependencies(result *[]application.Dependency, from application.ResourceReference, object unstructured.Unstructured) {
	addService := func(backend map[string]any) {
		service, _ := backend["service"].(map[string]any)
		if name := stringValue(service["name"]); name != "" {
			*result = append(*result, application.Dependency{From: from, To: application.ResourceReference{APIVersion: "v1", Kind: "Service", Namespace: object.GetNamespace(), Name: name}, Type: "ROUTES_TO", Required: true})
		}
	}
	if backend, found, _ := unstructured.NestedMap(object.Object, "spec", "defaultBackend"); found {
		addService(backend)
	}
	rules, _, _ := unstructured.NestedSlice(object.Object, "spec", "rules")
	for _, ruleItem := range rules {
		rule, _ := ruleItem.(map[string]any)
		httpRule, _ := rule["http"].(map[string]any)
		paths, _ := httpRule["paths"].([]any)
		for _, pathItem := range paths {
			path, _ := pathItem.(map[string]any)
			backend, _ := path["backend"].(map[string]any)
			addService(backend)
		}
	}
}

func podSpec(object unstructured.Unstructured) map[string]any {
	var fields []string
	switch object.GetKind() {
	case "Deployment", "StatefulSet", "DaemonSet", "Job":
		fields = []string{"spec", "template", "spec"}
	case "CronJob":
		fields = []string{"spec", "jobTemplate", "spec", "template", "spec"}
	default:
		return nil
	}
	value, _, _ := unstructured.NestedMap(object.Object, fields...)
	return value
}

func podTemplateLabels(object unstructured.Unstructured) map[string]string {
	fields := []string{"spec", "template", "metadata", "labels"}
	if object.GetKind() == "CronJob" {
		fields = []string{"spec", "jobTemplate", "spec", "template", "metadata", "labels"}
	}
	labels, _, _ := unstructured.NestedStringMap(object.Object, fields...)
	return labels
}

func podImages(object unstructured.Unstructured) []string {
	spec := podSpec(object)
	if spec == nil {
		return nil
	}
	images := make([]string, 0)
	for _, field := range []string{"initContainers", "containers"} {
		containers, _, _ := unstructured.NestedSlice(spec, field)
		for _, item := range containers {
			container, _ := item.(map[string]any)
			if image := stringValue(container["image"]); image != "" {
				images = append(images, image)
			}
		}
	}
	return uniqueSorted(images)
}

func referenceFor(object unstructured.Unstructured) application.ResourceReference {
	return application.ResourceReference{APIVersion: object.GetAPIVersion(), Kind: object.GetKind(), Namespace: object.GetNamespace(), Name: object.GetName()}
}

func referenceKey(value application.ResourceReference) string {
	return strings.Join([]string{value.APIVersion, value.Kind, value.Namespace, value.Name}, "\x00")
}

func referenceFromKey(key string) application.ResourceReference {
	parts := strings.Split(key, "\x00")
	return application.ResourceReference{APIVersion: parts[0], Kind: parts[1], Namespace: parts[2], Name: parts[3]}
}

func labelsMatch(selector, labels map[string]string) bool {
	for key, value := range selector {
		if labels[key] != value {
			return false
		}
	}
	return true
}

func sortedNestedMapKeys(object map[string]any, fields ...string) []string {
	set := map[string]struct{}{}
	for _, field := range fields {
		values, _, _ := unstructured.NestedMap(object, field)
		for key := range values {
			set[key] = struct{}{}
		}
	}
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func stringValue(value any) string {
	result, _ := value.(string)
	return result
}

func isCustomAPI(apiVersion string) bool {
	group := strings.Split(apiVersion, "/")[0]
	_, standard := map[string]struct{}{"v1": {}, "apps": {}, "batch": {}, "networking.k8s.io": {}, "rbac.authorization.k8s.io": {}, "autoscaling": {}, "policy": {}}[group]
	return !standard
}

func sortInventory(value *application.Inventory) {
	sortResources := func(items []application.ResourceSummary) {
		sort.Slice(items, func(i, j int) bool {
			return fmt.Sprintf("%s/%s/%s", items[i].Kind, items[i].Namespace, items[i].Name) < fmt.Sprintf("%s/%s/%s", items[j].Kind, items[j].Namespace, items[j].Name)
		})
	}
	for _, items := range [][]application.ResourceSummary{value.Resources, value.Workloads, value.Services, value.Ingresses, value.ConfigMaps, value.Secrets, value.ServiceAccounts, value.Roles, value.RoleBindings, value.CRDs, value.CustomResources} {
		sortResources(items)
	}
	sort.Slice(value.PVCs, func(i, j int) bool { return value.PVCs[i].Name < value.PVCs[j].Name })
	sort.Slice(value.Images, func(i, j int) bool { return value.Images[i].Reference < value.Images[j].Reference })
	seen := map[string]application.Dependency{}
	for _, edge := range value.Dependencies {
		key := referenceKey(edge.From) + "\x00" + referenceKey(edge.To) + "\x00" + edge.Type
		seen[key] = edge
	}
	value.Dependencies = value.Dependencies[:0]
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value.Dependencies = append(value.Dependencies, seen[key])
	}
	sort.Slice(value.Warnings, func(i, j int) bool {
		return value.Warnings[i].Code+value.Warnings[i].Resource < value.Warnings[j].Code+value.Warnings[j].Resource
	})
}
