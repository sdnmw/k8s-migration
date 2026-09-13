package kubernetes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	quantity "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/restmapper"
)

type ApplyManifestSpec struct {
	DefaultNamespace string
	Manifests        []byte
	Overwrite        bool
}

type AppliedResource struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name"`
}

func (c *Client) ApplyManifests(ctx context.Context, kubeconfig []byte, spec ApplyManifestSpec) ([]AppliedResource, error) {
	if spec.DefaultNamespace == "" || len(spec.Manifests) == 0 {
		return nil, errors.New("default namespace and manifests are required")
	}
	if err := c.EnsureNamespace(ctx, kubeconfig, spec.DefaultNamespace); err != nil {
		return nil, err
	}
	prepared, err := c.Prepare(kubeconfig)
	if err != nil {
		return nil, err
	}
	dynamicClient, err := dynamic.NewForConfig(prepared.Config)
	if err != nil {
		return nil, errors.New("could not initialize dynamic Kubernetes client")
	}
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(prepared.Config)
	if err != nil {
		return nil, errors.New("could not initialize Kubernetes discovery client")
	}
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(discoveryClient))
	objects, err := decodeApplyObjects(spec.Manifests)
	if err != nil {
		return nil, err
	}
	result := make([]AppliedResource, 0, len(objects))
	for index := range objects {
		object := &objects[index]
		gvk := object.GroupVersionKind()
		mapping, mapErr := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if mapErr != nil {
			return result, fmt.Errorf("resolve API for %s %s: %w", gvk.Kind, object.GetName(), mapErr)
		}
		resource := dynamicClient.Resource(mapping.Resource)
		var target dynamic.ResourceInterface = resource
		if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
			namespace := object.GetNamespace()
			if namespace == "" {
				namespace = spec.DefaultNamespace
				object.SetNamespace(namespace)
			}
			target = resource.Namespace(namespace)
		} else {
			object.SetNamespace("")
		}
		if !spec.Overwrite {
			_, getErr := target.Get(ctx, object.GetName(), metav1.GetOptions{})
			if getErr == nil {
				return result, fmt.Errorf("target %s %s/%s already exists", object.GetKind(), object.GetNamespace(), object.GetName())
			}
			if !apierrors.IsNotFound(getErr) {
				return result, fmt.Errorf("check target %s %s: %w", object.GetKind(), object.GetName(), getErr)
			}
		}
		if spec.Overwrite && object.GetKind() == "PersistentVolumeClaim" {
			current, getErr := target.Get(ctx, object.GetName(), metav1.GetOptions{})
			if getErr != nil && !apierrors.IsNotFound(getErr) {
				return result, getErr
			}
			if getErr == nil {
				preserveExpandedPVC(object, current)
			}
		}
		body, marshalErr := json.Marshal(object.Object)
		if marshalErr != nil {
			return result, fmt.Errorf("marshal %s %s: %w", object.GetKind(), object.GetName(), marshalErr)
		}
		force := spec.Overwrite
		if _, patchErr := target.Patch(ctx, object.GetName(), types.ApplyPatchType, body, metav1.PatchOptions{FieldManager: "sks-migration-center", Force: &force}); patchErr != nil {
			return result, fmt.Errorf("apply %s %s/%s: %w", object.GetKind(), object.GetNamespace(), object.GetName(), patchErr)
		}
		result = append(result, AppliedResource{APIVersion: object.GetAPIVersion(), Kind: object.GetKind(), Namespace: object.GetNamespace(), Name: object.GetName()})
	}
	return result, nil
}

func preserveExpandedPVC(desired, current *unstructured.Unstructured) {
	path := []string{"spec", "resources", "requests", "storage"}
	oldValue, _, _ := unstructured.NestedString(current.Object, path...)
	newValue, _, _ := unstructured.NestedString(desired.Object, path...)
	oldSize, e1 := quantity.ParseQuantity(oldValue)
	newSize, e2 := quantity.ParseQuantity(newValue)
	if e1 == nil && e2 == nil && oldSize.Cmp(newSize) > 0 {
		_ = unstructured.SetNestedField(desired.Object, oldValue, path...)
	}
}

func decodeApplyObjects(input []byte) ([]unstructured.Unstructured, error) {
	if len(input) > maxKomposeOutputBytes {
		return nil, errors.New("manifest payload exceeds 10 MiB")
	}
	decoder := k8syaml.NewYAMLOrJSONDecoder(bytes.NewReader(input), 4096)
	result := make([]unstructured.Unstructured, 0)
	for {
		var object map[string]any
		err := decoder.Decode(&object)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, errors.New("manifest YAML cannot be decoded")
		}
		if len(object) == 0 {
			continue
		}
		value := unstructured.Unstructured{Object: object}
		if value.GetName() == "" || value.GetKind() == "" || value.GetAPIVersion() == "" {
			return nil, errors.New("each manifest requires apiVersion, kind and metadata.name")
		}
		if strings.EqualFold(value.GetKind(), "Namespace") || value.GroupVersionKind() == (schema.GroupVersionKind{Group: "apiextensions.k8s.io", Version: "v1", Kind: "CustomResourceDefinition"}) {
			return nil, fmt.Errorf("cluster-scoped %s is outside Compose migration scope", value.GetKind())
		}
		result = append(result, value)
		if len(result) > 1000 {
			return nil, errors.New("manifest document count exceeds 1000")
		}
	}
	if len(result) == 0 {
		return nil, errors.New("no Kubernetes manifests found")
	}
	return result, nil
}
