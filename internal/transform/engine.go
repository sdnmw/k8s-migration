package transform

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
	"github.com/smartx/sks-migration-center/internal/domain/mapping"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"
)

const MaxManifestBytes = 10 * 1024 * 1024
const MaxDocuments = 1000

var ErrInvalidManifest = errors.New("invalid Kubernetes manifest")

type Document struct {
	APIVersion  string                    `json:"apiVersion"`
	Kind        string                    `json:"kind"`
	Namespace   string                    `json:"namespace,omitempty"`
	Name        string                    `json:"name"`
	SourceYAML  string                    `json:"sourceYaml"`
	TargetYAML  string                    `json:"targetYaml"`
	UnifiedDiff string                    `json:"unifiedDiff"`
	Changed     bool                      `json:"changed"`
	Object      unstructured.Unstructured `json:"-"`
}

type Result struct {
	Documents []Document `json:"documents"`
	Changed   int        `json:"changed"`
}

type Engine struct{}

func NewEngine() *Engine { return &Engine{} }

func (e *Engine) Transform(manifests []byte, profile mapping.Profile) (Result, error) {
	objects, err := decodeManifests(manifests)
	if err != nil {
		return Result{}, err
	}
	documents := make([]Document, 0, len(objects))
	for _, source := range objects {
		target := *source.DeepCopy()
		applyTransforms(&target, profile)
		sourceYAML, err := previewYAML(source)
		if err != nil {
			return Result{}, err
		}
		targetYAML, err := previewYAML(target)
		if err != nil {
			return Result{}, err
		}
		diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{A: difflib.SplitLines(sourceYAML), B: difflib.SplitLines(targetYAML), FromFile: "source", ToFile: "target", Context: 3})
		if err != nil {
			return Result{}, fmt.Errorf("render manifest diff: %w", err)
		}
		documents = append(documents, Document{APIVersion: target.GetAPIVersion(), Kind: target.GetKind(), Namespace: target.GetNamespace(), Name: target.GetName(), SourceYAML: sourceYAML, TargetYAML: targetYAML, UnifiedDiff: diff, Changed: sourceYAML != targetYAML, Object: target})
	}
	sort.Slice(documents, func(i, j int) bool { return documentKey(documents[i]) < documentKey(documents[j]) })
	result := Result{Documents: documents}
	for _, document := range documents {
		if document.Changed {
			result.Changed++
		}
	}
	return result, nil
}

// Render returns transformed objects for internal execution. Unlike the
// preview fields on Document, Secret data is intentionally preserved here.
func (e *Engine) Render(result Result) ([]byte, error) {
	if len(result.Documents) == 0 || len(result.Documents) > MaxDocuments {
		return nil, fmt.Errorf("%w: transformed document count is invalid", ErrInvalidManifest)
	}
	var output bytes.Buffer
	for index := range result.Documents {
		encoded, err := json.Marshal(result.Documents[index].Object.Object)
		if err != nil {
			return nil, fmt.Errorf("marshal transformed manifest: %w", err)
		}
		manifest, err := yaml.JSONToYAML(encoded)
		if err != nil {
			return nil, fmt.Errorf("render transformed manifest: %w", err)
		}
		if index > 0 {
			output.WriteString("---\n")
		}
		output.Write(manifest)
	}
	if output.Len() > MaxManifestBytes {
		return nil, fmt.Errorf("%w: transformed payload exceeds 10 MiB", ErrInvalidManifest)
	}
	return output.Bytes(), nil
}

func decodeManifests(input []byte) ([]unstructured.Unstructured, error) {
	if len(input) == 0 || len(input) > MaxManifestBytes {
		return nil, fmt.Errorf("%w: payload must be between 1 byte and 10 MiB", ErrInvalidManifest)
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
			return nil, fmt.Errorf("%w: YAML cannot be decoded", ErrInvalidManifest)
		}
		if len(object) == 0 {
			continue
		}
		value := unstructured.Unstructured{Object: object}
		if value.GetAPIVersion() == "" || value.GetKind() == "" || value.GetName() == "" {
			return nil, fmt.Errorf("%w: every document requires apiVersion, kind and metadata.name", ErrInvalidManifest)
		}
		result = append(result, value)
		if len(result) > MaxDocuments {
			return nil, fmt.Errorf("%w: document count exceeds %d", ErrInvalidManifest, MaxDocuments)
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("%w: no Kubernetes objects found", ErrInvalidManifest)
	}
	return result, nil
}

func applyTransforms(object *unstructured.Unstructured, profile mapping.Profile) {
	removeRuntimeFields(object)
	convertAPIVersion(object)
	applyNamespaceMapping(object, profile.Namespaces)
	applyStorageMapping(object, profile.Storage)
	applyIngressMapping(object, profile.Ingress)
	applyRegistryMapping(object, profile.Registries)
	applyNodeLabelMapping(object, profile.NodeLabels)
	applyNFSMapping(object, profile.NFS)
}

// ApplyPostRestoreMappings applies only fields that remain mutable after a
// Velero restore. Runtime metadata and immutable storage fields are left
// untouched; StorageClass and Namespace mappings are handled by Velero.
func ApplyPostRestoreMappings(object *unstructured.Unstructured, profile mapping.Profile) {
	applyIngressMapping(object, profile.Ingress)
	applyRegistryMapping(object, profile.Registries)
	applyNodeLabelMapping(object, profile.NodeLabels)
	applyNFSMapping(object, profile.NFS)
}

func removeRuntimeFields(object *unstructured.Unstructured) {
	unstructured.RemoveNestedField(object.Object, "status")
	for _, name := range []string{"uid", "resourceVersion", "generation", "creationTimestamp", "deletionTimestamp", "deletionGracePeriodSeconds", "managedFields", "selfLink", "ownerReferences", "finalizers"} {
		unstructured.RemoveNestedField(object.Object, "metadata", name)
	}
	annotations := object.GetAnnotations()
	delete(annotations, "kubectl.kubernetes.io/last-applied-configuration")
	delete(annotations, "deployment.kubernetes.io/revision")
	object.SetAnnotations(annotations)
	removeGeneratedLabels(object.Object, "metadata", "labels")

	switch object.GetKind() {
	case "Service":
		clusterIP, _, _ := unstructured.NestedString(object.Object, "spec", "clusterIP")
		for _, name := range []string{"healthCheckNodePort", "ipFamilies"} {
			unstructured.RemoveNestedField(object.Object, "spec", name)
		}
		if clusterIP != "None" {
			unstructured.RemoveNestedField(object.Object, "spec", "clusterIP")
			unstructured.RemoveNestedField(object.Object, "spec", "clusterIPs")
		}
		ports, found, _ := unstructured.NestedSlice(object.Object, "spec", "ports")
		if found {
			for index := range ports {
				if port, ok := ports[index].(map[string]any); ok {
					delete(port, "nodePort")
				}
			}
			_ = unstructured.SetNestedSlice(object.Object, ports, "spec", "ports")
		}
	case "PersistentVolumeClaim":
		unstructured.RemoveNestedField(object.Object, "spec", "volumeName")
	case "ServiceAccount":
		unstructured.RemoveNestedField(object.Object, "secrets")
	case "Job":
		unstructured.RemoveNestedField(object.Object, "spec", "selector")
		unstructured.RemoveNestedField(object.Object, "spec", "manualSelector")
	case "Namespace":
		unstructured.RemoveNestedField(object.Object, "spec", "finalizers")
	}
	for _, kind := range []string{"Deployment", "StatefulSet", "DaemonSet", "ReplicaSet"} {
		if object.GetKind() == kind {
			removeGeneratedLabels(object.Object, "spec", "selector", "matchLabels")
		}
	}
	for _, path := range podTemplatePaths(object.GetKind()) {
		removeGeneratedLabels(object.Object, append(path[:len(path)-1], "metadata", "labels")...)
	}
}

func removeGeneratedLabels(object map[string]any, fields ...string) {
	labels, found, _ := unstructured.NestedStringMap(object, fields...)
	if !found {
		return
	}
	for _, key := range []string{"pod-template-hash", "controller-revision-hash", "controller-uid", "batch.kubernetes.io/controller-uid", "job-name", "batch.kubernetes.io/job-name", "statefulset.kubernetes.io/pod-name"} {
		delete(labels, key)
	}
	if len(labels) == 0 {
		unstructured.RemoveNestedField(object, fields...)
		return
	}
	_ = unstructured.SetNestedStringMap(object, labels, fields...)
}

func previewYAML(object unstructured.Unstructured) (string, error) {
	copy := *object.DeepCopy()
	if copy.GetKind() == "Secret" {
		for _, field := range []string{"data", "stringData"} {
			values, found, _ := unstructured.NestedMap(copy.Object, field)
			if found {
				for key := range values {
					values[key] = "<redacted>"
				}
				_ = unstructured.SetNestedMap(copy.Object, values, field)
			}
		}
	}
	encoded, err := json.Marshal(copy.Object)
	if err != nil {
		return "", fmt.Errorf("marshal manifest: %w", err)
	}
	result, err := yaml.JSONToYAML(encoded)
	if err != nil {
		return "", fmt.Errorf("render manifest YAML: %w", err)
	}
	return string(result), nil
}

func documentKey(value Document) string {
	return strings.Join([]string{value.Kind, value.Namespace, value.Name, value.APIVersion}, "\x00")
}
