package addon

import (
	"path/filepath"
	"strings"
	"testing"

	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	helmengine "helm.sh/helm/v3/pkg/engine"
)

func TestLockedVeleroChartMetadata(t *testing.T) {
	path := filepath.Join("..", "..", "deploy", "charts", "velero-12.1.0.tgz")
	value, err := loader.Load(path)
	if err != nil {
		t.Fatalf("load locked Velero chart: %v; run make vendor-velero-chart", err)
	}
	if value.Metadata.Name != "velero" || value.Metadata.Version != "12.1.0" || value.Metadata.AppVersion != "1.18.1" {
		t.Fatalf("unexpected Velero chart metadata: %#v", value.Metadata)
	}
	if len(value.CRDObjects()) < 12 {
		t.Fatalf("expected complete upstream Velero CRDs, got %d", len(value.CRDObjects()))
	}
	values, err := chartutil.CoalesceValues(value, map[string]any{
		"image": map[string]any{
			"repository": "harbor.local/migration/velero", "digest": "sha256:11459094b1b21ec7c817b08f8067d9e89380835547915cac9c4132ff05b55b90",
		},
		"credentials": map[string]any{"useSecret": true, "existingSecret": "cloud-credentials"},
		"initContainers": []any{map[string]any{
			"name": "velero-plugin-for-aws", "image": "harbor.local/migration/velero-plugin-for-aws@sha256:7e82f717f44e89671212e0dfce7e061321c386ea84a33bca64a671670ca6c278",
			"volumeMounts": []any{map[string]any{"mountPath": "/target", "name": "plugins"}},
		}},
		"configuration":    map[string]any{"backupStorageLocation": []any{}, "defaultVolumesToFsBackup": true, "uploaderType": "kopia", "features": "EnableCSI"},
		"snapshotsEnabled": false, "deployNodeAgent": true,
		"nodeAgent": map[string]any{
			"podVolumePath": "/var/lib/kubelet/pods", "pluginVolumePath": "/var/lib/kubelet/plugins",
			"containerSecurityContext": map[string]any{"privileged": true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	renderValues, err := chartutil.ToRenderValues(value, values, chartutil.ReleaseOptions{Name: "sks-migration-velero", Namespace: "velero", Revision: 1, IsInstall: true}, nil)
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
	}
	for _, required := range []string{
		"kind: Deployment", "kind: DaemonSet", "name: node-agent", "privileged: true",
		"/var/lib/kubelet/pods", "harbor.local/migration/velero@sha256:11459094",
		"harbor.local/migration/velero-plugin-for-aws@sha256:7e82f717",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("rendered Velero chart does not contain %q", required)
		}
	}
}
