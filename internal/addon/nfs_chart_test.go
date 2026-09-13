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

func TestNFSCSIChartRendersPinnedOfficialImagesAndRequiredWorkloads(t *testing.T) {
	value, err := loader.LoadDir(filepath.Join("..", "..", "deploy", "charts", "csi-driver-nfs"))
	if err != nil {
		t.Fatal(err)
	}
	values, err := chartutil.CoalesceValues(value, nil)
	if err != nil {
		t.Fatal(err)
	}
	renderValues, err := chartutil.ToRenderValues(value, values, chartutil.ReleaseOptions{Name: "sks-migration-nfs-csi", Namespace: "kube-system", Revision: 1, IsInstall: true}, nil)
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
			if err := decoder.Decode(&document); err == io.EOF {
				break
			} else if err != nil {
				t.Fatalf("rendered NFS chart is invalid YAML: %v\n%s", err, manifest)
			}
		}
	}
	for _, required := range []string{
		"kind: CSIDriver", "name: nfs.csi.k8s.io", "kind: Deployment", "kind: DaemonSet",
		"privileged: true", "mountPropagation: Bidirectional", "k8tz.io/inject: \"false\"",
		"nfsplugin@sha256:1eb5a851", "csi-provisioner@sha256:a4b0b1a3",
		"csi-resizer@sha256:a2d40c1c", "livenessprobe@sha256:06da0d5b",
		"csi-node-driver-registrar@sha256:f9de845b",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("rendered NFS CSI chart does not contain %q\n%s", required, joined)
		}
	}
	if strings.Contains(joined, ":v4.13.4") || strings.Contains(joined, "<no value>") {
		t.Fatal("rendered NFS CSI chart contains a tag-only or unresolved image")
	}
}
