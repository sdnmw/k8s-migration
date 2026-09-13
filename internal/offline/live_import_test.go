package offline

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLiveWorkerFixImport(t *testing.T) {
	root := os.Getenv("LIVE_WORKER_IMPORT_ROOT")
	if root == "" {
		t.Skip("explicit live import only")
	}
	read := func(path string) []byte {
		b, e := os.ReadFile(filepath.Join(root, path))
		if e != nil {
			t.Fatal(e)
		}
		return b
	}
	var index ociIndex
	layout := os.Getenv("LIVE_WORKER_LAYOUT")
	if layout == "" { layout = "output/harbor-worker-fix" }
	if e := json.Unmarshal(read(layout+"/index.json"), &index); e != nil {
		t.Fatal(e)
	}
	importer, e := NewRegistryImporter(RegistryConfig{Endpoint: "https://192.168.112.30", Username: strings.TrimSpace(string(read(".data/secrets/harbor-username"))), Password: strings.TrimSpace(string(read(".data/secrets/harbor-password"))), Insecure: true})
	if e != nil {
		t.Fatal(e)
	}
	digest := index.Manifests[0].Digest
	target := os.Getenv("LIVE_WORKER_TARGET")
	if target == "" { target = "sks/sks-migration-worker:harbor-fix-20260910" }
	result, e := importer.Import(context.Background(), root, LockedImage{Name: "worker-fix", Source: "local/worker@" + digest, Target: target, Platforms: []string{"linux/amd64"}, LayoutPath: layout, SBOMPath: "sbom/worker.json", Signature: "signatures/worker.json", ScanPath: "scans/worker.json"})
	if e != nil {
		t.Fatal(e)
	}
	t.Log(result, digest)
}
