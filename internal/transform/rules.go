package transform

import (
	"strings"

	"github.com/smartx/sks-migration-center/internal/domain/mapping"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func convertAPIVersion(object *unstructured.Unstructured) {
	key := object.GetAPIVersion() + "/" + object.GetKind()
	switch key {
	case "extensions/v1beta1/Ingress", "networking.k8s.io/v1beta1/Ingress":
		object.SetAPIVersion("networking.k8s.io/v1")
		convertIngressBackend(object.Object, "spec", "backend")
		rules, _, _ := unstructured.NestedSlice(object.Object, "spec", "rules")
		for _, rawRule := range rules {
			rule, _ := rawRule.(map[string]any)
			http, _ := rule["http"].(map[string]any)
			paths, _ := http["paths"].([]any)
			for _, rawPath := range paths {
				path, _ := rawPath.(map[string]any)
				if _, found := path["pathType"]; !found {
					path["pathType"] = "ImplementationSpecific"
				}
				convertIngressBackendMap(path, "backend")
			}
		}
		if rules != nil {
			_ = unstructured.SetNestedSlice(object.Object, rules, "spec", "rules")
		}
		unstructured.RemoveNestedField(object.Object, "spec", "backend")
		if backend, found, _ := unstructured.NestedMap(object.Object, "spec", "defaultBackend"); !found {
			if legacy, foundLegacy, _ := unstructured.NestedMap(object.Object, "spec", "_convertedDefaultBackend"); foundLegacy {
				_ = unstructured.SetNestedMap(object.Object, legacy, "spec", "defaultBackend")
			}
		} else {
			_ = unstructured.SetNestedMap(object.Object, backend, "spec", "defaultBackend")
		}
		unstructured.RemoveNestedField(object.Object, "spec", "_convertedDefaultBackend")
	case "apps/v1beta1/Deployment", "apps/v1beta2/Deployment":
		object.SetAPIVersion("apps/v1")
		ensureWorkloadSelector(object)
	case "batch/v1beta1/CronJob":
		object.SetAPIVersion("batch/v1")
	case "policy/v1beta1/PodDisruptionBudget":
		object.SetAPIVersion("policy/v1")
	}
}

func convertIngressBackend(object map[string]any, fields ...string) {
	legacy, found, _ := unstructured.NestedMap(object, fields...)
	if !found {
		return
	}
	converted := ingressBackend(legacy)
	_ = unstructured.SetNestedMap(object, converted, "spec", "_convertedDefaultBackend")
}

func convertIngressBackendMap(parent map[string]any, field string) {
	legacy, found := parent[field].(map[string]any)
	if !found {
		return
	}
	parent[field] = ingressBackend(legacy)
}

func ingressBackend(value map[string]any) map[string]any {
	if _, already := value["service"]; already {
		return value
	}
	name, _ := value["serviceName"].(string)
	port := value["servicePort"]
	if name == "" {
		return value
	}
	portValue := map[string]any{}
	switch typed := port.(type) {
	case string:
		portValue["name"] = typed
	case int64:
		portValue["number"] = typed
	case float64:
		portValue["number"] = int64(typed)
	case int:
		portValue["number"] = int64(typed)
	}
	return map[string]any{"service": map[string]any{"name": name, "port": portValue}}
}

func ensureWorkloadSelector(object *unstructured.Unstructured) {
	if _, found, _ := unstructured.NestedMap(object.Object, "spec", "selector"); found {
		return
	}
	labels, found, _ := unstructured.NestedStringMap(object.Object, "spec", "template", "metadata", "labels")
	if found && len(labels) > 0 {
		_ = unstructured.SetNestedMap(object.Object, map[string]any{"matchLabels": stringMapAny(labels)}, "spec", "selector")
	}
}

func applyNamespaceMapping(object *unstructured.Unstructured, values []mapping.KeyValue) {
	mapped := keyValueMap(values)
	if target, found := mapped[object.GetNamespace()]; found {
		object.SetNamespace(target)
	}
	if object.GetKind() == "Namespace" {
		if target, found := mapped[object.GetName()]; found {
			object.SetName(target)
		}
	}
	subjects, found, _ := unstructured.NestedSlice(object.Object, "subjects")
	if found {
		for _, raw := range subjects {
			subject, _ := raw.(map[string]any)
			if subject["kind"] == "ServiceAccount" {
				if namespace, ok := subject["namespace"].(string); ok {
					if target, mappedOK := mapped[namespace]; mappedOK {
						subject["namespace"] = target
					}
				}
			}
		}
		_ = unstructured.SetNestedSlice(object.Object, subjects, "subjects")
	}
}

func applyStorageMapping(object *unstructured.Unstructured, values []mapping.KeyValue) {
	mapped := keyValueMap(values)
	rewriteNestedString(object.Object, mapped, "spec", "storageClassName")
	claims, found, _ := unstructured.NestedSlice(object.Object, "spec", "volumeClaimTemplates")
	if found {
		for _, raw := range claims {
			claim, _ := raw.(map[string]any)
			rewriteNestedString(claim, mapped, "spec", "storageClassName")
		}
		_ = unstructured.SetNestedSlice(object.Object, claims, "spec", "volumeClaimTemplates")
	}
}

func applyIngressMapping(object *unstructured.Unstructured, values []mapping.KeyValue) {
	if object.GetKind() != "Ingress" {
		return
	}
	mapped := keyValueMap(values)
	rewriteNestedString(object.Object, mapped, "spec", "ingressClassName")
	annotations := object.GetAnnotations()
	if target, found := mapped[annotations["kubernetes.io/ingress.class"]]; found {
		annotations["kubernetes.io/ingress.class"] = target
		object.SetAnnotations(annotations)
	}
}

func applyRegistryMapping(object *unstructured.Unstructured, values []mapping.KeyValue) {
	for _, path := range podTemplatePaths(object.GetKind()) {
		spec, found, _ := unstructured.NestedMap(object.Object, path...)
		if !found {
			continue
		}
		for _, field := range []string{"initContainers", "containers"} {
			containers, _ := spec[field].([]any)
			for _, raw := range containers {
				container, _ := raw.(map[string]any)
				if image, ok := container["image"].(string); ok {
					container["image"] = rewritePrefix(image, values)
				}
			}
		}
		_ = unstructured.SetNestedMap(object.Object, spec, path...)
	}
}

func applyNodeLabelMapping(object *unstructured.Unstructured, values []mapping.NodeLabelMapping) {
	for _, path := range podTemplatePaths(object.GetKind()) {
		spec, found, _ := unstructured.NestedMap(object.Object, path...)
		if !found {
			continue
		}
		nodeSelector, _ := spec["nodeSelector"].(map[string]any)
		rewriteLabelMap(nodeSelector, values)
		if len(nodeSelector) == 0 {
			delete(spec, "nodeSelector")
		}
		affinity, _ := spec["affinity"].(map[string]any)
		rewriteAffinity(affinity, values)
		if len(affinity) == 0 {
			delete(spec, "affinity")
		}
		constraints, _ := spec["topologySpreadConstraints"].([]any)
		filtered := constraints[:0]
		for _, raw := range constraints {
			constraint, _ := raw.(map[string]any)
			key, _ := constraint["topologyKey"].(string)
			target, action := labelTarget(key, values)
			if action == "DROP" {
				continue
			}
			if action == "MAP" {
				constraint["topologyKey"] = target
			}
			filtered = append(filtered, raw)
		}
		if constraints != nil {
			if len(filtered) == 0 {
				delete(spec, "topologySpreadConstraints")
			} else {
				spec["topologySpreadConstraints"] = filtered
			}
		}
		_ = unstructured.SetNestedMap(object.Object, spec, path...)
	}
}

func applyNFSMapping(object *unstructured.Unstructured, values []mapping.NFSMapping) {
	if object.GetKind() == "PersistentVolume" {
		if nfs, found, _ := unstructured.NestedMap(object.Object, "spec", "nfs"); found {
			if storageClass := rewriteNFS(nfs, values); storageClass != "" {
				_ = unstructured.SetNestedField(object.Object, storageClass, "spec", "storageClassName")
			}
			_ = unstructured.SetNestedMap(object.Object, nfs, "spec", "nfs")
		}
	}
	for _, path := range podTemplatePaths(object.GetKind()) {
		spec, found, _ := unstructured.NestedMap(object.Object, path...)
		if !found {
			continue
		}
		volumes, _ := spec["volumes"].([]any)
		for _, raw := range volumes {
			volume, _ := raw.(map[string]any)
			nfs, _ := volume["nfs"].(map[string]any)
			_ = rewriteNFS(nfs, values)
		}
		_ = unstructured.SetNestedMap(object.Object, spec, path...)
	}
}

func rewriteNFS(nfs map[string]any, values []mapping.NFSMapping) string {
	server, _ := nfs["server"].(string)
	path, _ := nfs["path"].(string)
	for _, value := range values {
		if value.SourceServer == server && value.SourceExport == path {
			nfs["server"], nfs["path"] = value.TargetServer, value.TargetExport
			return value.TargetStorageClass
		}
	}
	return ""
}
func rewriteNestedString(object map[string]any, mapped map[string]string, fields ...string) {
	value, found, _ := unstructured.NestedString(object, fields...)
	if found {
		if target, ok := mapped[value]; ok {
			_ = unstructured.SetNestedField(object, target, fields...)
		}
	}
}
func keyValueMap(values []mapping.KeyValue) map[string]string {
	result := map[string]string{}
	for _, value := range values {
		result[value.Source] = value.Target
	}
	return result
}
func rewritePrefix(value string, mappings []mapping.KeyValue) string {
	for _, item := range mappings {
		source := strings.TrimSuffix(item.Source, "/")
		if value == source || strings.HasPrefix(value, source+"/") {
			return strings.TrimSuffix(item.Target, "/") + strings.TrimPrefix(value, source)
		}
	}
	return value
}
func stringMapAny(values map[string]string) map[string]any {
	result := map[string]any{}
	for key, value := range values {
		result[key] = value
	}
	return result
}

func podTemplatePaths(kind string) [][]string {
	switch kind {
	case "Pod":
		return [][]string{{"spec"}}
	case "CronJob":
		return [][]string{{"spec", "jobTemplate", "spec", "template", "spec"}}
	case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job":
		return [][]string{{"spec", "template", "spec"}}
	default:
		return nil
	}
}

func rewriteLabelMap(labels map[string]any, values []mapping.NodeLabelMapping) {
	for source, raw := range labels {
		target, action := labelTarget(source, values)
		switch action {
		case "DROP":
			delete(labels, source)
		case "MAP":
			delete(labels, source)
			labels[target] = raw
		}
	}
}
func labelTarget(source string, values []mapping.NodeLabelMapping) (string, string) {
	for _, value := range values {
		if value.Source == source {
			return value.Target, value.Action
		}
	}
	return source, ""
}
func rewriteAffinity(affinity map[string]any, values []mapping.NodeLabelMapping) {
	node, _ := affinity["nodeAffinity"].(map[string]any)
	required, _ := node["requiredDuringSchedulingIgnoredDuringExecution"].(map[string]any)
	rewriteNodeSelectorTerms(required, values)
	if len(required) == 0 {
		delete(node, "requiredDuringSchedulingIgnoredDuringExecution")
	}
	preferred, _ := node["preferredDuringSchedulingIgnoredDuringExecution"].([]any)
	filtered := preferred[:0]
	for _, raw := range preferred {
		item, _ := raw.(map[string]any)
		preference, _ := item["preference"].(map[string]any)
		if rewriteNodeSelectorTerm(preference, values) {
			filtered = append(filtered, raw)
		}
	}
	if preferred != nil {
		if len(filtered) == 0 {
			delete(node, "preferredDuringSchedulingIgnoredDuringExecution")
		} else {
			node["preferredDuringSchedulingIgnoredDuringExecution"] = filtered
		}
	}
	if len(node) == 0 {
		delete(affinity, "nodeAffinity")
	}
}

func rewriteNodeSelectorTerms(root map[string]any, values []mapping.NodeLabelMapping) {
	terms, _ := root["nodeSelectorTerms"].([]any)
	filtered := terms[:0]
	for _, raw := range terms {
		term, _ := raw.(map[string]any)
		if rewriteNodeSelectorTerm(term, values) {
			filtered = append(filtered, raw)
		}
	}
	if terms != nil {
		if len(filtered) == 0 {
			delete(root, "nodeSelectorTerms")
		} else {
			root["nodeSelectorTerms"] = filtered
		}
	}
}
func rewriteNodeSelectorTerm(term map[string]any, values []mapping.NodeLabelMapping) bool {
	for _, field := range []string{"matchExpressions", "matchFields"} {
		expressions, _ := term[field].([]any)
		filtered := expressions[:0]
		for _, raw := range expressions {
			expression, _ := raw.(map[string]any)
			key, _ := expression["key"].(string)
			target, action := labelTarget(key, values)
			if action == "DROP" {
				continue
			}
			if action == "MAP" {
				expression["key"] = target
			}
			filtered = append(filtered, raw)
		}
		if expressions != nil {
			if len(filtered) == 0 {
				delete(term, field)
			} else {
				term[field] = filtered
			}
		}
	}
	return len(term) > 0
}
