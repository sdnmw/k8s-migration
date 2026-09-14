package offline

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestImageLockRejectsTagsTraversalAndUnknownFields(t *testing.T) {
	invalid := `apiVersion: migration.smartx.com/v1alpha1
kind: OfflineImageLock
bundleVersion: test
images:
  - name: api
    source: example/api:latest
    target: project/api:v1
    platforms: [linux/amd64]
    layoutPath: ../escape
    sbomPath: sbom/api.json
    signaturePath: signatures/api.sig
    scanPath: scans/api.json
    unexpected: true
`
	if _, err := LoadImageLock(strings.NewReader(invalid)); err == nil {
		t.Fatal("expected an invalid lock to be rejected")
	}
}

func TestBundlePackAndVerifyDetectTampering(t *testing.T) {
	root, _, _ := createBundleFixture(t)
	archivePath := filepath.Join(t.TempDir(), "bundle.tar.gz")
	archive, err := os.OpenFile(archivePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := PackDirectory(root, archive); err != nil {
		t.Fatalf("PackDirectory: %v", err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	extracted := t.TempDir()
	extractFixtureArchive(t, archivePath, extracted)
	manifest, err := VerifyDirectory(extracted)
	if err != nil || manifest.BundleVersion != "test-v1" || len(manifest.Files) < 7 {
		t.Fatalf("VerifyDirectory = %+v, %v", manifest, err)
	}
	if err := os.WriteFile(filepath.Join(extracted, "target-sks.yaml"), []byte("user supplied kubeconfig"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extracted, "bundle.tar.gz"), []byte("original archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyDirectory(extracted); err != nil {
		t.Fatalf("untracked deployment inputs must not invalidate the signed bundle files: %v", err)
	}
	if err := os.WriteFile(filepath.Join(extracted, "sbom", "api.spdx.json"), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyDirectory(extracted); err == nil || !strings.Contains(err.Error(), "verification failed") {
		t.Fatalf("tampered verification error = %v", err)
	}
}

func TestBundleIsReproducibleAndRejectsMismatchedOCIRoot(t *testing.T) {
	root, lock, _ := createBundleFixture(t)
	var first, second bytes.Buffer
	if err := PackDirectory(root, &first); err != nil {
		t.Fatal(err)
	}
	if err := PackDirectory(root, &second); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("identical staging directories did not produce identical archives")
	}
	lock.Images[0].Source = "registry.example/api@sha256:" + strings.Repeat("0", 64)
	value, _ := json.Marshal(lock)
	if err := os.WriteFile(filepath.Join(root, ImageLockName), value, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := PackDirectory(root, io.Discard); err == nil || !strings.Contains(err.Error(), "does not match locked source digest") {
		t.Fatalf("mismatched OCI root error = %v", err)
	}
}

func TestRegistryImporterPushesVerifiedOCIImage(t *testing.T) {
	root, lock, _ := createBundleFixture(t)
	var mu sync.Mutex
	uploads, manifests := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		username, password, ok := request.BasicAuth()
		if !ok || username != "robot$offline" || password != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case request.Method == http.MethodHead && strings.Contains(request.URL.Path, "/blobs/"):
			w.WriteHeader(http.StatusNotFound)
		case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/blobs/uploads/"):
			w.Header().Set("Location", serverURL(request)+"/upload/fixture")
			w.WriteHeader(http.StatusAccepted)
		case request.Method == http.MethodPut && request.URL.Path == "/upload/fixture":
			if request.URL.Query().Get("digest") == "" {
				t.Error("blob upload did not include digest")
			}
			_, _ = io.Copy(io.Discard, request.Body)
			mu.Lock()
			uploads++
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
		case request.Method == http.MethodPut && strings.Contains(request.URL.Path, "/manifests/"):
			mu.Lock()
			manifests++
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
		default:
			http.Error(w, request.Method+" "+request.URL.Path, http.StatusNotFound)
		}
	}))
	defer server.Close()
	importer, err := NewRegistryImporter(RegistryConfig{Endpoint: server.URL, Username: "robot$offline", Password: "secret", Insecure: true})
	if err != nil {
		t.Fatalf("NewRegistryImporter: %v", err)
	}
	result, err := importer.Import(context.Background(), root, lock.Images[0])
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if result.UploadedBlobs != 2 || result.Manifests != 2 || uploads != 2 || manifests != 2 {
		t.Fatalf("result=%+v uploads=%d manifests=%d", result, uploads, manifests)
	}
}

func TestRegistryRequestPreservesRequiredUploadTrailingSlash(t *testing.T) {
	importer, err := NewRegistryImporter(RegistryConfig{Endpoint: "https://registry.example", Insecure: true})
	if err != nil {
		t.Fatal(err)
	}
	request, err := importer.request(context.Background(), http.MethodPost, "v2/project/image/blobs/uploads/", nil)
	if err != nil {
		t.Fatal(err)
	}
	if request.URL.Path != "/v2/project/image/blobs/uploads/" {
		t.Fatalf("upload path = %q", request.URL.Path)
	}
}

func TestRetargetImageUsesInstallerSelectedHarborProject(t *testing.T) {
	image := LockedImage{Target: "default/sks-migration-api:0.1.0"}
	retargeted, err := RetargetImage(image, "customer_migration")
	if err != nil {
		t.Fatal(err)
	}
	if retargeted.Target != "customer_migration/sks-migration-api:0.1.0" {
		t.Fatalf("unexpected target %q", retargeted.Target)
	}
	if _, err := RetargetImage(image, "Invalid/Project"); err == nil {
		t.Fatal("expected invalid Harbor project to fail")
	}
}

func TestRegistryImporterSupportsBearerChallenge(t *testing.T) {
	requests := 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/token" {
			username, password, ok := request.BasicAuth()
			if !ok || username != "robot" || password != "password" {
				t.Error("token request omitted basic credentials")
			}
			_, _ = io.WriteString(w, `{"token":"registry-token"}`)
			return
		}
		requests++
		if request.Header.Get("Authorization") != "Bearer registry-token" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+server.URL+`/token",service="harbor-registry",scope="repository:project/demo:pull,push"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	importer, err := NewRegistryImporter(RegistryConfig{Endpoint: server.URL, Username: "robot", Password: "password", Insecure: true})
	if err != nil {
		t.Fatal(err)
	}
	request, _ := importer.request(context.Background(), http.MethodHead, "v2/project/demo/blobs/sha256:test", nil)
	response, err := importer.do(request)
	if err != nil || response.StatusCode != http.StatusOK || requests != 2 {
		t.Fatalf("bearer retry response=%v error=%v requests=%d", response, err, requests)
	}
	_ = response.Body.Close()
}

func createBundleFixture(t *testing.T) (string, ImageLock, string) {
	t.Helper()
	root := t.TempDir()
	layout := filepath.Join(root, "images", "api")
	for _, directory := range []string{filepath.Join(layout, "blobs", "sha256"), filepath.Join(root, "sbom"), filepath.Join(root, "signatures"), filepath.Join(root, "scans"), filepath.Join(root, "charts")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(name string, value []byte) ociDescriptor {
		hash := sha256.Sum256(value)
		digest := hex.EncodeToString(hash[:])
		if err := os.WriteFile(filepath.Join(layout, "blobs", "sha256", digest), value, 0o600); err != nil {
			t.Fatal(err)
		}
		return ociDescriptor{Digest: "sha256:" + digest, Size: int64(len(value))}
	}
	config := write("config", []byte(`{"architecture":"amd64","os":"linux"}`))
	config.MediaType = "application/vnd.oci.image.config.v1+json"
	layer := write("layer", []byte("fixture-layer"))
	layer.MediaType = "application/vnd.oci.image.layer.v1.tar"
	manifestValue, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": mediaOCIManifest, "config": config, "layers": []ociDescriptor{layer}})
	manifest := write("manifest", manifestValue)
	manifest.MediaType = mediaOCIManifest
	indexValue, _ := json.Marshal(ociIndex{SchemaVersion: 2, Manifests: []ociDescriptor{manifest}})
	if err := os.WriteFile(filepath.Join(layout, "index.json"), indexValue, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout, "oci-layout"), []byte(`{"imageLayoutVersion":"1.0.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sbom", "api.spdx.json"), []byte(`{"spdxVersion":"SPDX-2.3"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "signatures", "api.sig"), []byte("fixture-signature"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "scans", "api.json"), []byte(`{"critical":0,"high":0}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "charts", "base.tgz"), []byte("fixture-chart"), 0o600); err != nil {
		t.Fatal(err)
	}
	lock := ImageLock{APIVersion: LockAPIVersion, Kind: LockKind, BundleVersion: "test-v1", Images: []LockedImage{{Name: "api", Source: "registry.example/api@" + manifest.Digest, Target: "project/api:v1", Platforms: []string{"linux/amd64"}, LayoutPath: "images/api", SBOMPath: "sbom/api.spdx.json", Signature: "signatures/api.sig", ScanPath: "scans/api.json"}}}
	lockValue, _ := json.Marshal(lock)
	// JSON is valid YAML and keeps the fixture independent from an encoder's formatting.
	if err := os.WriteFile(filepath.Join(root, ImageLockName), lockValue, 0o600); err != nil {
		t.Fatal(err)
	}
	return root, lock, manifest.Digest
}

func extractFixtureArchive(t *testing.T, archivePath, destination string) {
	t.Helper()
	file, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := validateRelativePath(header.Name); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(destination, filepath.FromSlash(header.Name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		value, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, value, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func serverURL(request *http.Request) string {
	return "http://" + request.Host
}
