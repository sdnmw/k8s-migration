package migration

import (
	"fmt"
	"strings"

	composeanalyzer "github.com/smartx/sks-migration-center/internal/compose"
	"github.com/smartx/sks-migration-center/internal/transform"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func applyComposeRuntime(result *transform.Result, runtime composeanalyzer.TargetRuntime, namespace string) error {
	for _, proxy := range runtime.Proxies {
		found := false
		for i := range result.Documents {
			object := &result.Documents[i].Object
			if object.GetKind() != "Deployment" && object.GetKind() != "StatefulSet" {
				continue
			}
			if object.GetName() != strings.ReplaceAll(proxy.Service, "_", "-") && object.GetLabels()["io.kompose.service"] != proxy.Service {
				continue
			}
			containers, _, _ := unstructured.NestedSlice(object.Object, "spec", "template", "spec", "containers")
			if len(containers) != 1 {
				return fmt.Errorf("Traefik 服务 %s 的目标容器不明确", proxy.Service)
			}
			container := containers[0].(map[string]any)
			mounts, _ := container["volumeMounts"].([]any)
			container["volumeMounts"] = append(mounts, map[string]any{"name": "migration-traefik-routing", "mountPath": composeanalyzer.TraefikRuntimeDirectory, "readOnly": true})
			if err := unstructured.SetNestedSlice(object.Object, containers, "spec", "template", "spec", "containers"); err != nil {
				return err
			}
			volumes, _, _ := unstructured.NestedSlice(object.Object, "spec", "template", "spec", "volumes")
			volumes = append(volumes, map[string]any{"name": "migration-traefik-routing", "configMap": map[string]any{"name": proxy.ConfigName}})
			if err := unstructured.SetNestedSlice(object.Object, volumes, "spec", "template", "spec", "volumes"); err != nil {
				return err
			}
			annotations := object.GetAnnotations()
			if annotations == nil {
				annotations = map[string]string{}
			}
			annotations["migration.smartx.com/runtime-adapter"] = "traefik-file-provider"
			object.SetAnnotations(annotations)
			found = true
		}
		if !found {
			return fmt.Errorf("Traefik 服务 %s 没有生成目标工作负载", proxy.Service)
		}
		config := unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": proxy.ConfigName, "namespace": namespace}, "data": map[string]any{"dynamic.yaml": string(proxy.Configuration)}}}
		result.Documents = append(result.Documents, transform.Document{APIVersion: "v1", Kind: "ConfigMap", Name: proxy.ConfigName, Namespace: namespace, Changed: true, Object: config})
		for service, port := range proxy.BackendPorts {
			name := strings.ReplaceAll(service, "_", "-")
			found := false
			for i := range result.Documents {
				object := &result.Documents[i].Object
				if object.GetKind() != "Service" || object.GetName() != name {
					continue
				}
				ports, _, _ := unstructured.NestedSlice(object.Object, "spec", "ports")
				hasPort := false
				for _, raw := range ports {
					p, _ := raw.(map[string]any)
					if fmt.Sprint(p["port"]) == fmt.Sprint(port) {
						hasPort = true
					}
				}
				if !hasPort {
					ports = append(ports, map[string]any{"name": fmt.Sprintf("runtime-%d", port), "port": int64(port), "targetPort": int64(port), "protocol": "TCP"})
					if err := unstructured.SetNestedSlice(object.Object, ports, "spec", "ports"); err != nil {
						return err
					}
				}
				found = true
			}
			if !found {
				object := unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Service", "metadata": map[string]any{"name": name, "namespace": namespace}, "spec": map[string]any{"type": "ClusterIP", "selector": map[string]any{"io.kompose.service": name}, "ports": []any{map[string]any{"name": "runtime-http", "port": int64(port), "targetPort": int64(port), "protocol": "TCP"}}}}}
				result.Documents = append(result.Documents, transform.Document{APIVersion: "v1", Kind: "Service", Name: name, Namespace: namespace, Changed: true, Object: object})
			}
		}
	}
	return nil
}
