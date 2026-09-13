package addon

import (
	"io"
	"path/filepath"
	"strings"
	"testing"

	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	helmengine "helm.sh/helm/v3/pkg/engine"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
)

func TestMinIOChartRendersSingleRWOInstanceWithTLSAndPinnedImage(t *testing.T) {
	value, err := loader.LoadDir(filepath.Join("..", "..", "deploy", "charts", "minio-snsd"))
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("b", 64)
	values, err := chartutil.CoalesceValues(value, map[string]any{
		"image":             map[string]any{"repository": "harbor.local/migration/minio", "digest": digest},
		"credentialsSecret": "minio-root",
		"tls":               map[string]any{"enabled": true, "existingSecret": "minio-tls"},
		"persistence":       map[string]any{"storageClass": "smtx-block", "size": "200Gi"},
	})
	if err != nil {
		t.Fatal(err)
	}
	renderValues, err := chartutil.ToRenderValues(value, values, chartutil.ReleaseOptions{Name: "sks-migration-minio", Namespace: "sks-migration-system", Revision: 1, IsInstall: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := helmengine.Render(value, renderValues)
	if err != nil {
		t.Fatal(err)
	}
	joined := ""
	for name, manifest := range rendered {
		if strings.HasSuffix(name, "NOTES.txt") {
			continue
		}
		joined += manifest
		decoder := utilyaml.NewYAMLOrJSONDecoder(strings.NewReader(manifest), 4096)
		for {
			var document map[string]any
			err := decoder.Decode(&document)
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("rendered manifest is invalid YAML: %v\n%s", err, manifest)
			}
		}
	}
	for _, required := range []string{
		"replicas: 1", "accessModes: [ReadWriteOnce]", "storageClassName: \"smtx-block\"",
		"harbor.local/migration/minio@" + digest, "scheme: HTTPS", "runAsNonRoot: true",
		"helm.sh/resource-policy: keep", "automountServiceAccountToken: false",
		"k8tz.io/inject: \"false\"",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("rendered MinIO chart does not contain %q\n%s", required, joined)
		}
	}
	if strings.Contains(joined, "registry.example.invalid") || strings.Contains(joined, "<no value>") {
		t.Fatal("rendered MinIO chart contains an unlocked default or unresolved value")
	}
}

func TestMinIOChartRejectsUnlockedDefaults(t *testing.T) {
	value, err := loader.LoadDir(filepath.Join("..", "..", "deploy", "charts", "minio-snsd"))
	if err != nil {
		t.Fatal(err)
	}
	values, err := chartutil.ToRenderValues(value, value.Values, chartutil.ReleaseOptions{Name: "minio", Namespace: "migration-system", IsInstall: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := helmengine.Render(value, values); err == nil {
		t.Fatal("expected empty digest or StorageClass to stop installation")
	}
}
