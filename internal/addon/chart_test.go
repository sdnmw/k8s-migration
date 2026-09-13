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

func TestBaseChartRendersOnlyDigestPinnedImages(t *testing.T) {
	chartPath := filepath.Join("..", "..", "deploy", "charts", "sks-migration-center")
	value, err := loader.LoadDir(chartPath)
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	values := map[string]any{
		"global": map[string]any{"storageClass": "smtx-block", "imagePullSecrets": []any{}},
		"image": map[string]any{
			"api":      map[string]any{"repository": "harbor.local/migration/api", "digest": digest},
			"worker":   map[string]any{"repository": "harbor.local/migration/worker", "digest": digest},
			"web":      map[string]any{"repository": "harbor.local/migration/web", "digest": digest},
			"postgres": map[string]any{"repository": "harbor.local/migration/postgres", "digest": digest},
		},
		"addonImages": map[string]any{
			"minio":           map[string]any{"repository": "harbor.local/migration/minio", "digest": digest},
			"velero":          map[string]any{"repository": "harbor.local/migration/velero", "digest": digest},
			"veleroAWSPlugin": map[string]any{"repository": "harbor.local/migration/velero-plugin-for-aws", "digest": digest},
			"nfsPlugin":       map[string]any{"repository": "harbor.local/migration/nfsplugin", "digest": digest},
			"nfsProvisioner":  map[string]any{"repository": "harbor.local/migration/csi-provisioner", "digest": digest},
			"nfsResizer":      map[string]any{"repository": "harbor.local/migration/csi-resizer", "digest": digest},
			"nfsLiveness":     map[string]any{"repository": "harbor.local/migration/livenessprobe", "digest": digest},
			"nfsRegistrar":    map[string]any{"repository": "harbor.local/migration/csi-node-driver-registrar", "digest": digest},
			"nfsProbe":        map[string]any{"repository": "harbor.local/migration/nfs-probe-helper", "digest": digest},
			"kompose":         map[string]any{"repository": "harbor.local/migration/kompose", "digest": digest},
			"kopia":           map[string]any{"repository": "harbor.local/migration/kopia", "digest": digest},
		},
	}
	values, err = chartutil.CoalesceValues(value, values)
	if err != nil {
		t.Fatal(err)
	}
	renderValues, err := chartutil.ToRenderValues(value, values, chartutil.ReleaseOptions{Name: "migration", Namespace: "migration-system", Revision: 1, IsInstall: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := helmengine.Render(value, renderValues)
	if err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, manifest := range rendered {
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
		"kind: Deployment", "kind: StatefulSet", "storageClassName: \"smtx-block\"",
		"kind: NetworkPolicy", "sks-migration-center-default-deny", "WORKER_METRICS_ADDR",
		"harbor.local/migration/api@" + digest,
		"MINIO_IMAGE: \"harbor.local/migration/minio@" + digest + "\"",
		"VELERO_IMAGE: \"harbor.local/migration/velero@" + digest + "\"",
		"VELERO_AWS_PLUGIN_IMAGE: \"harbor.local/migration/velero-plugin-for-aws@" + digest + "\"",
		"STAGING_HELPER_IMAGE: \"harbor.local/migration/nfs-probe-helper@" + digest + "\"",
		"KOMPOSE_IMAGE: \"harbor.local/migration/kompose@" + digest + "\"",
		"KOPIA_IMAGE: \"harbor.local/migration/kopia@" + digest + "\"",
		"name: CREDENTIAL_MASTER_KEY_FILE",
		"mountPath: /run/secrets/sks-migration",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("rendered chart does not contain %q", required)
		}
	}
	if strings.Contains(joined, "registry.example.invalid") || strings.Contains(joined, "<no value>") {
		t.Fatal("rendered chart contains an unlocked default or unresolved value")
	}
}

func TestBaseChartRejectsUnlockedDefaults(t *testing.T) {
	value, err := loader.LoadDir(filepath.Join("..", "..", "deploy", "charts", "sks-migration-center"))
	if err != nil {
		t.Fatal(err)
	}
	values, err := chartutil.ToRenderValues(value, value.Values, chartutil.ReleaseOptions{Name: "migration", Namespace: "migration-system", IsInstall: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := helmengine.Render(value, values); err == nil {
		t.Fatal("expected empty digest or StorageClass to stop installation")
	}
}
