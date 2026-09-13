package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPrepareRejectsLocalExecutionAndFileReferences(t *testing.T) {
	client := NewClient(time.Second)
	for name, body := range map[string]string{
		"exec plugin": kubeconfigFor("https://cluster.example.test", "    exec:\n      command: steal-secrets\n"),
		"token file":  kubeconfigFor("https://cluster.example.test", "    tokenFile: /var/run/secrets/token\n"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := client.Prepare([]byte(body)); err == nil {
				t.Fatal("expected unsafe kubeconfig to be rejected")
			}
		})
	}
}

func TestProbeAgainstTLSTestCluster(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/version":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"major": "1", "minor": "36", "gitVersion": "v1.36.2", "gitCommit": "test",
				"gitTreeState": "clean", "buildDate": "2026-06-12T00:00:00Z", "goVersion": "go1.26.0",
				"compiler": "gc", "platform": "linux/amd64",
			})
		case "/api/v1/namespaces":
			_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"NamespaceList","items":[{"metadata":{"name":"default"}},{"metadata":{"name":"business"}}]}`))
		case "/api/v1/nodes":
			_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"NodeList","items":[{"metadata":{"name":"worker-1"}},{"metadata":{"name":"worker-2"}},{"metadata":{"name":"worker-3"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(3 * time.Second)
	result, err := client.Probe(context.Background(), []byte(kubeconfigFor(server.URL, "    token: test-token\n")))
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if result.Endpoint != server.URL || result.Capabilities.KubernetesVersion != "v1.36.2" {
		t.Fatalf("unexpected cluster identity: %+v", result)
	}
	if result.Capabilities.NamespaceCount != 2 || result.Capabilities.NodeCount != 3 {
		t.Fatalf("unexpected counts: %+v", result.Capabilities)
	}
	if len(result.Checks) != 6 || result.Checks[5].Status != "WARNING" {
		t.Fatalf("expected TLS warning and successful checks: %+v", result.Checks)
	}
}

func TestProbeReturnsStructuredFailureWithoutServerDetail(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/version" {
			_, _ = w.Write([]byte(`{"major":"1","minor":"36","gitVersion":"v1.36.2"}`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure","message":"secret backend detail","reason":"Forbidden","code":403}`))
	}))
	defer server.Close()

	client := NewClient(3 * time.Second)
	result, err := client.Probe(context.Background(), []byte(kubeconfigFor(server.URL, "    token: test-token\n")))
	if err == nil {
		t.Fatal("expected probe failure")
	}
	if len(result.Checks) != 4 || result.Checks[2].Status != "PASSED" || result.Checks[3].Status != "FAILED" {
		t.Fatalf("unexpected checks: %+v", result.Checks)
	}
	if strings.Contains(fmt.Sprint(result.Checks), "secret backend detail") {
		t.Fatal("raw API error leaked into connection checks")
	}
}

func TestDiscoverCollectsStorageNetworkArchitectureAndCapacity(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/version":
			_, _ = w.Write([]byte(`{"major":"1","minor":"36","gitVersion":"v1.36.2","gitCommit":"test","gitTreeState":"clean","buildDate":"2026-06-12T00:00:00Z","goVersion":"go1.26.0","compiler":"gc","platform":"linux/amd64"}`))
		case "/api/v1/namespaces":
			_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"NamespaceList","items":[{"metadata":{"name":"business"}},{"metadata":{"name":"default"}}]}`))
		case "/api/v1/nodes":
			_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"NodeList","items":[` +
				`{"metadata":{"name":"worker-1"},"status":{"nodeInfo":{"architecture":"amd64","operatingSystem":"linux"},"allocatable":{"cpu":"8","memory":"32Gi","pods":"110"}}},` +
				`{"metadata":{"name":"worker-2"},"status":{"nodeInfo":{"architecture":"arm64","operatingSystem":"linux"},"allocatable":{"cpu":"16","memory":"64Gi","pods":"110"}}}]}`))
		case "/apis/storage.k8s.io/v1/storageclasses":
			_, _ = w.Write([]byte(`{"apiVersion":"storage.k8s.io/v1","kind":"StorageClassList","items":[{"metadata":{"name":"smtx-block","annotations":{"storageclass.kubernetes.io/is-default-class":"true"}},"provisioner":"smtx-elf-csi-driver","allowVolumeExpansion":true,"volumeBindingMode":"WaitForFirstConsumer"}]}`))
		case "/apis/storage.k8s.io/v1/csidrivers":
			_, _ = w.Write([]byte(`{"apiVersion":"storage.k8s.io/v1","kind":"CSIDriverList","items":[{"metadata":{"name":"smtx-elf-csi-driver"}}]}`))
		case "/apis/networking.k8s.io/v1/ingressclasses":
			_, _ = w.Write([]byte(`{"apiVersion":"networking.k8s.io/v1","kind":"IngressClassList","items":[{"metadata":{"name":"nginx"},"spec":{"controller":"k8s.io/ingress-nginx"}}]}`))
		case "/apis/snapshot.storage.k8s.io/v1/volumesnapshotclasses":
			_, _ = w.Write([]byte(`{"apiVersion":"snapshot.storage.k8s.io/v1","kind":"VolumeSnapshotClassList","items":[{"metadata":{"name":"smtx-snapshot"},"driver":"smtx-elf-csi-driver","deletionPolicy":"Retain"}]}`))
		case "/api":
			_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"APIVersions","versions":["v1"],"serverAddressByClientCIDRs":[]}`))
		case "/apis":
			_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"APIGroupList","groups":[{"name":"apps","versions":[{"groupVersion":"apps/v1","version":"v1"}],"preferredVersion":{"groupVersion":"apps/v1","version":"v1"}},{"name":"storage.k8s.io","versions":[{"groupVersion":"storage.k8s.io/v1","version":"v1"}],"preferredVersion":{"groupVersion":"storage.k8s.io/v1","version":"v1"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	result, err := NewClient(3*time.Second).Discover(context.Background(), []byte(kubeconfigFor(server.URL, "    token: test-token\n")))
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	capabilities := result.Capabilities
	if strings.Join(capabilities.Architectures, ",") != "amd64,arm64" {
		t.Fatalf("architectures=%v", capabilities.Architectures)
	}
	if strings.Join(capabilities.OperatingSystems, ",") != "linux" {
		t.Fatalf("operating systems=%v", capabilities.OperatingSystems)
	}
	if capabilities.Allocatable["cpu"] != "24" || capabilities.Allocatable["memory"] != "96Gi" {
		t.Fatalf("allocatable=%v", capabilities.Allocatable)
	}
	if len(capabilities.StorageClasses) != 1 || !capabilities.StorageClasses[0].Default || !capabilities.StorageClasses[0].AllowExpansion {
		t.Fatalf("storage classes=%+v", capabilities.StorageClasses)
	}
	if strings.Join(capabilities.CSIDrivers, ",") != "smtx-elf-csi-driver" || strings.Join(capabilities.VolumeSnapshotClasses, ",") != "smtx-snapshot" {
		t.Fatalf("storage capabilities=%+v", capabilities)
	}
	if len(capabilities.VolumeSnapshotClassDetails) != 1 || capabilities.VolumeSnapshotClassDetails[0].Driver != "smtx-elf-csi-driver" || capabilities.VolumeSnapshotClassDetails[0].DeletionPolicy != "Retain" {
		t.Fatalf("snapshot class details=%+v", capabilities.VolumeSnapshotClassDetails)
	}
	if strings.Join(capabilities.IngressClasses, ",") != "nginx" || strings.Join(capabilities.APIGroups, ",") != "apps,storage.k8s.io,v1" {
		t.Fatalf("API and ingress capabilities=%+v", capabilities)
	}
	if capabilities.Security["tlsVerification"] != false {
		t.Fatalf("security snapshot=%v", capabilities.Security)
	}
}

func kubeconfigFor(server, userConfig string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: cluster
  cluster:
    server: %s
    insecure-skip-tls-verify: true
contexts:
- name: active
  context:
    cluster: cluster
    user: operator
current-context: active
users:
- name: operator
  user:
%s`, server, userConfig)
}
