package acceptance

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubernetesclient "k8s.io/client-go/kubernetes"

	"github.com/smartx/sks-migration-center/internal/adapter/kubernetes"
	"github.com/smartx/sks-migration-center/internal/adapter/s3"
	"github.com/smartx/sks-migration-center/internal/addon"
)

const (
	acceptanceNamespace = "sks-migration-system"
	acceptanceRelease   = "sks-migration-minio"
	acceptanceWorkload  = "sks-migration-minio"
	acceptedMinIODigest = "sha256:14cea493d9a34af32f524e538b8346cf79f3321eff8e708c1e2960462bd8936e"
)

// TestMinIOOnSKS is intentionally opt-in because it installs a real StatefulSet
// and leaves its PVC and release in place for the migration service. Run with:
//
// TEST_SKS_KUBECONFIG=/path/to/config go test -v ./internal/acceptance -run TestMinIOOnSKS
func TestMinIOOnSKS(t *testing.T) {
	kubeconfigPath := os.Getenv("TEST_SKS_KUBECONFIG")
	if kubeconfigPath == "" {
		t.Skip("TEST_SKS_KUBECONFIG is not set")
	}
	kubeconfig, err := os.ReadFile(kubeconfigPath)
	if err != nil {
		t.Fatalf("read acceptance kubeconfig: %v", err)
	}
	defer zero(kubeconfig)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	cluster := kubernetes.NewClient(30 * time.Second)
	prepared, err := cluster.Prepare(kubeconfig)
	if err != nil {
		t.Fatalf("prepare acceptance kubeconfig: %v", err)
	}
	clientset, err := kubernetesclient.NewForConfig(prepared.Config)
	if err != nil {
		t.Fatalf("create acceptance Kubernetes client: %v", err)
	}
	nodeIPs := discoverNodeIPs(ctx, t, clientset)
	caPEM, certificatePEM, privateKeyPEM := issueServerCertificate(t, nodeIPs)
	accessKey, secretKey := randomCredentials(t)
	defer func() {
		accessKey, secretKey = "", ""
	}()

	if err := cluster.EnsureNamespace(ctx, kubeconfig, acceptanceNamespace); err != nil {
		t.Fatalf("ensure managed namespace: %v", err)
	}
	if err := cluster.PutOpaqueSecret(ctx, kubeconfig, acceptanceNamespace, "sks-migration-minio-root", map[string][]byte{
		"access-key": []byte(accessKey), "secret-key": []byte(secretKey),
	}); err != nil {
		t.Fatalf("write MinIO credential Secret: %v", err)
	}
	if err := cluster.PutOpaqueSecret(ctx, kubeconfig, acceptanceNamespace, "sks-migration-minio-tls", map[string][]byte{
		"public.crt": certificatePEM, "private.key": privateKeyPEM,
	}); err != nil {
		t.Fatalf("write MinIO TLS Secret: %v", err)
	}

	manager, err := addon.NewManager(filepath.Join("..", "..", "deploy", "charts"))
	if err != nil {
		t.Fatalf("initialize Helm SDK manager: %v", err)
	}
	state, err := manager.InstallOrUpgrade(ctx, append([]byte(nil), kubeconfig...), addon.InstallRequest{
		ReleaseName: acceptanceRelease,
		Namespace:   acceptanceNamespace,
		ChartPath:   "minio-snsd",
		Version:     "0.1.0",
		Timeout:     20 * time.Minute,
		Values: map[string]any{
			"image": map[string]any{
				"repository": "m.daocloud.io/docker.io/minio/minio",
				"digest":     acceptedMinIODigest,
			},
			"credentialsSecret": "sks-migration-minio-root",
			"tls": map[string]any{
				"enabled":        true,
				"existingSecret": "sks-migration-minio-tls",
			},
			"persistence": map[string]any{
				"storageClass": "smtx-elf-csi-driver",
				"size":         "20Gi",
			},
			"service": map[string]any{"type": "NodePort"},
		},
	})
	if err != nil {
		t.Fatalf("install MinIO with Helm SDK: %v", err)
	}
	if state.Status != "deployed" {
		t.Fatalf("MinIO release status = %q, want deployed", state.Status)
	}

	service, err := clientset.CoreV1().Services(acceptanceNamespace).Get(ctx, acceptanceWorkload, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("read MinIO Service: %v", err)
	}
	nodePort := int32(0)
	for _, port := range service.Spec.Ports {
		if port.Name == "s3" {
			nodePort = port.NodePort
			break
		}
	}
	if nodePort == 0 {
		t.Fatal("MinIO S3 Service does not expose a NodePort")
	}

	config := s3.Config{
		Endpoint:  fmt.Sprintf("https://%s:%d", nodeIPs[0].String(), nodePort),
		Region:    "minio",
		AccessKey: accessKey,
		SecretKey: secretKey,
		CABundle:  caPEM,
		TLSVerify: true,
	}
	s3Client := s3.NewClient()
	probe, err := s3Client.WriteProbe(ctx, config, "velero")
	if err != nil {
		t.Fatalf("write S3 acceptance probe: %v", err)
	}
	if err := s3Client.VerifyProbe(ctx, config, probe, false); err != nil {
		t.Fatalf("verify S3 acceptance probe: %v", err)
	}
	if err := cluster.RestartStatefulSet(ctx, kubeconfig, acceptanceNamespace, acceptanceWorkload); err != nil {
		t.Fatalf("restart MinIO StatefulSet: %v", err)
	}
	if err := s3Client.VerifyProbe(ctx, config, probe, true); err != nil {
		t.Fatalf("verify S3 probe after restart: %v", err)
	}

	pvcs, err := clientset.CoreV1().PersistentVolumeClaims(acceptanceNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/instance=" + acceptanceRelease,
	})
	if err != nil || len(pvcs.Items) != 1 || pvcs.Items[0].Status.Phase != corev1.ClaimBound {
		t.Fatalf("expected one Bound MinIO PVC, got count=%d err=%v", len(pvcs.Items), err)
	}
	t.Logf("MinIO acceptance passed: release=%s revision=%d storageClass=%s PVC=%s S3 read/write/restart checksum=passed",
		state.Name, state.Revision, ptrValue(pvcs.Items[0].Spec.StorageClassName), pvcs.Items[0].Name)
}

func discoverNodeIPs(ctx context.Context, t *testing.T, clientset kubernetesclient.Interface) []net.IP {
	t.Helper()
	nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list SKS nodes: %v", err)
	}
	var result []net.IP
	for _, node := range nodes.Items {
		for _, address := range node.Status.Addresses {
			if address.Type == corev1.NodeInternalIP {
				if parsed := net.ParseIP(address.Address); parsed != nil {
					result = append(result, parsed)
				}
			}
		}
	}
	if len(result) == 0 {
		t.Fatal("SKS nodes have no InternalIP address")
	}
	return result
}

func issueServerCertificate(t *testing.T, nodeIPs []net.IP) ([]byte, []byte, []byte) {
	t.Helper()
	now := time.Now().UTC()
	caKey, err := rsa.GenerateKey(rand.Reader, 3072)
	if err != nil {
		t.Fatalf("generate acceptance CA key: %v", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          randomSerial(t),
		Subject:               pkix.Name{CommonName: "SKS Migration Center acceptance CA"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(365 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("issue acceptance CA: %v", err)
	}
	serverKey, err := rsa.GenerateKey(rand.Reader, 3072)
	if err != nil {
		t.Fatalf("generate MinIO TLS key: %v", err)
	}
	serverTemplate := &x509.Certificate{
		SerialNumber: randomSerial(t),
		Subject:      pkix.Name{CommonName: acceptanceWorkload},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: []string{
			acceptanceWorkload,
			acceptanceWorkload + "." + acceptanceNamespace,
			acceptanceWorkload + "." + acceptanceNamespace + ".svc",
			acceptanceWorkload + "." + acceptanceNamespace + ".svc.cluster.local",
		},
		IPAddresses: nodeIPs,
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caTemplate, &serverKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("issue MinIO TLS certificate: %v", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(serverKey)
	if err != nil {
		t.Fatalf("marshal MinIO TLS key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverDER}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
}

func randomSerial(t *testing.T) *big.Int {
	t.Helper()
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	value, err := rand.Int(rand.Reader, limit)
	if err != nil {
		t.Fatalf("generate certificate serial: %v", err)
	}
	return value
}

func randomCredentials(t *testing.T) (string, string) {
	t.Helper()
	access, secret := make([]byte, 15), make([]byte, 48)
	if _, err := rand.Read(access); err != nil {
		t.Fatalf("generate MinIO access key: %v", err)
	}
	if _, err := rand.Read(secret); err != nil {
		t.Fatalf("generate MinIO secret key: %v", err)
	}
	defer zero(access)
	defer zero(secret)
	return base64.RawURLEncoding.EncodeToString(access), base64.RawURLEncoding.EncodeToString(secret)
}

func ptrValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func zero(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
