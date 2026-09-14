package main

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/smartx/sks-migration-center/internal/offline"
)

type secretWriterStub struct {
	data map[string][]byte
}

func (s *secretWriterStub) PutOpaqueSecret(_ context.Context, _ []byte, _, _ string, data map[string][]byte) error {
	s.data = make(map[string][]byte, len(data))
	for key, value := range data {
		s.data[key] = append([]byte(nil), value...)
	}
	return nil
}

func TestDeployOptionsRequireOnlyRegistryAndKubeconfigInputs(t *testing.T) {
	options := deployOptions{
		directory: "bundle", harborAddress: "https://harbor.test", harborProject: "migration",
		usernameFile: "username", passwordFile: "password", kubeconfigFile: "target.yaml", namespace: defaultPlatformNamespace,
	}
	if err := options.validate(); err != nil {
		t.Fatalf("minimal deployment inputs were rejected: %v", err)
	}
}

func TestDiscoverSmartXStorageClassPrefersDefault(t *testing.T) {
	client := fake.NewSimpleClientset(
		&storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: "smtx-secondary"}, Provisioner: "com.smartx.elf-csi-driver"},
		&storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: "smtx-default", Annotations: map[string]string{"storageclass.kubernetes.io/is-default-class": "true"}}, Provisioner: "smtx-elf-csi-driver"},
	)
	name, err := discoverSmartXStorageClass(context.Background(), client)
	if err != nil || name != "smtx-default" {
		t.Fatalf("StorageClass discovery = %q, %v", name, err)
	}
}

func TestApplicationSecretIsGeneratedForFreshInstall(t *testing.T) {
	client := fake.NewSimpleClientset()
	writer := &secretWriterStub{}
	password, err := ensureApplicationSecret(context.Background(), writer, client, []byte("kubeconfig"), "migration", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(password) < 24 || string(writer.data["admin-password"]) != password || len(writer.data["postgres-password"]) < 32 {
		t.Fatal("generated platform credentials are incomplete")
	}
	key, err := base64.StdEncoding.DecodeString(string(writer.data["master-key"]))
	if err != nil || len(key) != 32 {
		t.Fatal("generated master key is not a base64-encoded 32-byte key")
	}
	if !strings.Contains(string(writer.data["database-url"]), "sks-migration-center-postgres") {
		t.Fatal("generated database URL does not use the in-cluster service")
	}
}

func TestPlatformInstallationDetectionAllowsPasswordIndependentUpgrade(t *testing.T) {
	client := fake.NewSimpleClientset(&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: platformAPIDeployment, Namespace: defaultPlatformNamespace}})
	exists, err := platformInstallationExists(context.Background(), client, defaultPlatformNamespace)
	if err != nil || !exists {
		t.Fatalf("existing platform detection = %v, %v", exists, err)
	}
	exists, err = platformInstallationExists(context.Background(), client, "fresh-install")
	if err != nil || exists {
		t.Fatalf("fresh platform detection = %v, %v", exists, err)
	}
}

func TestDeploymentValuesUseRetargetedHarborImages(t *testing.T) {
	names := []string{"platform-api", "platform-worker", "platform-web", "postgresql", "minio", "velero", "velero-plugin-for-aws", "nfsplugin", "csi-provisioner", "csi-resizer", "csi-node-driver-registrar", "livenessprobe", "nfs-probe-helper", "kompose", "kopia"}
	lock := offline.ImageLock{}
	for _, name := range names {
		lock.Images = append(lock.Images, offline.LockedImage{Name: name, Source: "official/" + name + "@sha256:" + strings.Repeat("a", 64), Target: "customer/" + name + ":v1"})
	}
	values, err := deploymentValues(lock, "harbor.test", "customer", "smtx-block", false)
	if err != nil {
		t.Fatal(err)
	}
	addons := values["addonImages"].(map[string]any)
	minio := addons["minio"].(map[string]any)
	if minio["repository"] != "harbor.test/customer/minio" || minio["digest"] != "sha256:"+strings.Repeat("a", 64) {
		t.Fatalf("unexpected MinIO deployment value: %+v", minio)
	}
	global := values["global"].(map[string]any)
	if global["storageClass"] != "smtx-block" {
		t.Fatalf("unexpected global values: %+v", global)
	}
	policy := values["networkPolicy"].(map[string]any)
	if enabled, ok := policy["enabled"].(bool); !ok || enabled {
		t.Fatalf("one-time installer must leave NetworkPolicy disabled by default: %+v", policy)
	}
}

func TestGeneratedDefaultMinIOTLSCoversEverySKSNode(t *testing.T) {
	nodeIPs := []net.IP{net.ParseIP("192.168.118.202"), net.ParseIP("192.168.118.206")}
	certificatePEM, privateKey, err := generateMinIOTLS(nodeIPs, time.Unix(1_700_000_000, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(privateKey) == 0 {
		t.Fatal("generated MinIO private key is empty")
	}
	block, _ := pem.Decode(certificatePEM)
	if block == nil {
		t.Fatal("generated MinIO certificate is not PEM")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	for _, nodeIP := range nodeIPs {
		if err := certificate.VerifyHostname(nodeIP.String()); err != nil {
			t.Fatalf("certificate does not cover %s: %v", nodeIP, err)
		}
	}
	selected, err := certificateNodeIP(certificatePEM, []net.IP{nodeIPs[1]})
	if err != nil || selected != nodeIPs[1].String() {
		t.Fatalf("certificate node selection = %q, %v", selected, err)
	}
}

func TestDefaultMinIOServiceRequiresNamedS3NodePort(t *testing.T) {
	service := &corev1.Service{Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Name: "console", NodePort: 30001}, {Name: "s3", NodePort: 30000}}}}
	if serviceNodePort(service, "s3") != 30000 || serviceNodePort(service, "missing") != 0 {
		t.Fatal("default MinIO service port selection is incorrect")
	}
}

func TestPlatformLoginAcceptsNoContentWithSessionCookies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.SetCookie(response, &http.Cookie{Name: "sks_migration_session", Value: "session", Path: "/"})
		http.SetCookie(response, &http.Cookie{Name: "sks_migration_csrf", Value: "csrf", Path: "/"})
		response.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	session, csrf, err := platformLogin(context.Background(), server.Client(), server.URL, "password")
	if err != nil || session == nil || csrf == nil || session.Value != "session" || csrf.Value != "csrf" {
		t.Fatalf("204 login result session=%v csrf=%v err=%v", session, csrf, err)
	}
}
