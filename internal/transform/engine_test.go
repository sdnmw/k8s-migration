package transform

import (
	"os"
	"strings"
	"testing"

	"github.com/smartx/sks-migration-center/internal/domain/mapping"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestTransformMatchesGoldenManifestAndRedactsSecrets(t *testing.T) {
	source, err := os.ReadFile("testdata/source.yaml")
	if err != nil {
		t.Fatal(err)
	}
	profile := mapping.Profile{
		Storage:    []mapping.KeyValue{{Source: "legacy-block", Target: "smtx-elf-storageclass"}, {Source: "legacy-nfs", Target: "nfs-rwx"}},
		Namespaces: []mapping.KeyValue{{Source: "business", Target: "business-prod"}},
		Ingress:    []mapping.KeyValue{{Source: "traefik", Target: "nginx"}},
		Registries: []mapping.KeyValue{{Source: "registry.legacy.local", Target: "harbor.example.local/migrated"}},
		NFS:        []mapping.NFSMapping{{SourceServer: "10.20.30.60", SourceExport: "/business", TargetServer: "10.60.30.60", TargetExport: "/migrations/business", TargetStorageClass: "nfs-rwx"}},
		NodeLabels: []mapping.NodeLabelMapping{{Source: "legacy/rack", Target: "topology.kubernetes.io/zone", Action: "MAP"}, {Source: "deprecated/node", Action: "DROP"}},
	}
	result, err := NewEngine().Transform(source, profile)
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if len(result.Documents) != 6 || result.Changed != 6 {
		t.Fatalf("unexpected result: %+v", result)
	}
	actual := joinTargetYAML(result.Documents)
	expected, err := os.ReadFile("testdata/target.golden.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if actual != string(expected) {
		t.Fatalf("target manifest differs from golden:\n%s", actual)
	}
	for _, document := range result.Documents {
		if strings.Contains(document.SourceYAML+document.TargetYAML+document.UnifiedDiff, "c2VjcmV0") || strings.Contains(document.SourceYAML+document.TargetYAML+document.UnifiedDiff, "username: api") {
			t.Fatal("secret value leaked into preview or diff")
		}
		if document.Kind == "Secret" {
			data, _, _ := unstructured.NestedStringMap(document.Object.Object, "data")
			if data["password"] != "c2VjcmV0" {
				t.Fatalf("execution object lost secret value: %+v", data)
			}
		}
	}
}

func TestTransformRejectsInvalidAndOversizedInput(t *testing.T) {
	for _, input := range [][]byte{nil, []byte("kind: Service"), make([]byte, MaxManifestBytes+1)} {
		if _, err := NewEngine().Transform(input, mapping.Profile{}); err == nil {
			t.Fatalf("expected invalid input for %d bytes", len(input))
		}
	}
}

func TestTransformIsDeterministic(t *testing.T) {
	source := []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: z\n---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\n")
	first, _ := NewEngine().Transform(source, mapping.Profile{})
	second, _ := NewEngine().Transform(source, mapping.Profile{})
	if joinTargetYAML(first.Documents) != joinTargetYAML(second.Documents) || first.Documents[0].Name != "a" {
		t.Fatalf("transform is not deterministic: %+v", first.Documents)
	}
}

func TestTransformPreservesHeadlessServiceSemantics(t *testing.T) {
	result, err := NewEngine().Transform([]byte("apiVersion: v1\nkind: Service\nmetadata:\n  name: database\n  finalizers: [kubernetes.io/example]\nspec:\n  clusterIP: None\n  clusterIPs: [None]\n  selector:\n    app: database\n"), mapping.Profile{})
	if err != nil {
		t.Fatal(err)
	}
	object := result.Documents[0].Object.Object
	clusterIP, _, _ := unstructured.NestedString(object, "spec", "clusterIP")
	clusterIPs, _, _ := unstructured.NestedStringSlice(object, "spec", "clusterIPs")
	if clusterIP != "None" || len(clusterIPs) != 1 || clusterIPs[0] != "None" || len(result.Documents[0].Object.GetFinalizers()) != 0 {
		t.Fatalf("headless service semantics or runtime cleanup is incorrect:\n%s", result.Documents[0].TargetYAML)
	}
}

func TestRenderPreservesSecretDataWhilePreviewRedactsIt(t *testing.T) {
	engine := NewEngine()
	result, err := engine.Transform([]byte("apiVersion: v1\nkind: Secret\nmetadata:\n  name: credentials\nstringData:\n  password: top-secret\n"), mapping.Profile{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Documents[0].TargetYAML, "top-secret") {
		t.Fatal("preview leaked Secret data")
	}
	rendered, err := engine.Render(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rendered), "top-secret") {
		t.Fatalf("execution render lost Secret data: %s", rendered)
	}
}

func TestApplyPostRestoreMappingsChangesMutableFieldsOnly(t *testing.T) {
	object := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]any{"name": "api", "namespace": "target", "resourceVersion": "42"},
		"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
			"nodeSelector": map[string]any{"legacy/rack": "rack-a"},
			"containers":   []any{map[string]any{"name": "api", "image": "registry.old/api:v1"}},
			"volumes":      []any{map[string]any{"name": "shared", "nfs": map[string]any{"server": "10.0.0.1", "path": "/source"}}},
		}}},
	}}
	ApplyPostRestoreMappings(object, mapping.Profile{
		Registries: []mapping.KeyValue{{Source: "registry.old", Target: "harbor.local/sks"}},
		NodeLabels: []mapping.NodeLabelMapping{{Source: "legacy/rack", Target: "topology.kubernetes.io/zone", Action: "MAP"}},
		NFS:        []mapping.NFSMapping{{SourceServer: "10.0.0.1", SourceExport: "/source", TargetServer: "10.0.0.2", TargetExport: "/target"}},
	})
	image, _, _ := unstructured.NestedString(object.Object, "spec", "template", "spec", "containers", "0", "image")
	containers, _, _ := unstructured.NestedSlice(object.Object, "spec", "template", "spec", "containers")
	image = containers[0].(map[string]any)["image"].(string)
	selector, _, _ := unstructured.NestedStringMap(object.Object, "spec", "template", "spec", "nodeSelector")
	volumes, _, _ := unstructured.NestedSlice(object.Object, "spec", "template", "spec", "volumes")
	nfs := volumes[0].(map[string]any)["nfs"].(map[string]any)
	if image != "harbor.local/sks/api:v1" || selector["topology.kubernetes.io/zone"] != "rack-a" || nfs["server"] != "10.0.0.2" || nfs["path"] != "/target" || object.GetResourceVersion() != "42" {
		t.Fatalf("post-restore mapping not applied: image=%s selector=%v nfs=%v rv=%s", image, selector, nfs, object.GetResourceVersion())
	}
}

func joinTargetYAML(documents []Document) string {
	parts := make([]string, 0, len(documents))
	for _, document := range documents {
		parts = append(parts, strings.TrimSpace(document.TargetYAML))
	}
	return strings.Join(parts, "\n---\n") + "\n"
}
