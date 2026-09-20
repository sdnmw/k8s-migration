package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubernetesclient "k8s.io/client-go/kubernetes"

	kubernetesadapter "github.com/smartx/sks-migration-center/internal/adapter/kubernetes"
	"github.com/smartx/sks-migration-center/internal/addon"
	"github.com/smartx/sks-migration-center/internal/offline"
)

const (
	defaultMinIONamespace        = "sks-migration-system"
	defaultMinIORelease          = "sks-migration-minio"
	defaultMinIOCredentialSecret = "sks-migration-minio-root"
	defaultMinIOTLSSecret        = "sks-migration-minio-tls"
)

type defaultMinIOResult struct {
	Endpoint string
	Reused   bool
}

func ensureDefaultMinIO(ctx context.Context, manager *addon.Manager, cluster *kubernetesadapter.Client, clientset kubernetesclient.Interface, kubeconfig []byte, lock offline.ImageLock, registry, storageClass, storageSize string, registryCredentials registryCredential) (defaultMinIOResult, error) {
	if err := cluster.EnsureNamespace(ctx, kubeconfig, defaultMinIONamespace); err != nil {
		return defaultMinIOResult{}, err
	}
	if err := putRegistrySecret(ctx, clientset, defaultMinIONamespace, registry, registryCredentials.username, registryCredentials.password); err != nil {
		return defaultMinIOResult{}, err
	}
	nodeIPs, err := clusterNodeIPs(ctx, clientset)
	if err != nil {
		return defaultMinIOResult{}, err
	}
	statefulSet, statefulSetErr := clientset.AppsV1().StatefulSets(defaultMinIONamespace).Get(ctx, defaultMinIORelease, metav1.GetOptions{})
	existing := statefulSetErr == nil
	if statefulSetErr != nil && !apierrors.IsNotFound(statefulSetErr) {
		return defaultMinIOResult{}, fmt.Errorf("inspect default MinIO StatefulSet: %w", statefulSetErr)
	}
	credentials, err := ensureMinIOCredentialSecret(ctx, cluster, clientset, kubeconfig, existing)
	if err != nil {
		return defaultMinIOResult{}, err
	}
	defer clearStringMap(credentials)
	certificate, err := ensureMinIOTLSSecret(ctx, cluster, clientset, kubeconfig, nodeIPs, existing)
	if err != nil {
		return defaultMinIOResult{}, err
	}
	if !existing {
		image, err := lockedImageValues(lock, "minio", registry)
		if err != nil {
			return defaultMinIOResult{}, err
		}
		if _, err := manager.InstallOrUpgrade(ctx, append([]byte(nil), kubeconfig...), addon.InstallRequest{
			ReleaseName: defaultMinIORelease, Namespace: defaultMinIONamespace, ChartPath: "minio-snsd", Version: "0.1.0",
			Values: map[string]any{
				"image": image, "imagePullSecrets": []any{map[string]any{"name": platformRegistrySecret}},
				"credentialsSecret": defaultMinIOCredentialSecret,
				"tls":               map[string]any{"enabled": true, "existingSecret": defaultMinIOTLSSecret},
				"persistence":       map[string]any{"storageClass": storageClass, "size": storageSize},
				"service":           map[string]any{"type": "NodePort"},
			}, Timeout: 30 * time.Minute,
		}); err != nil {
			return defaultMinIOResult{}, err
		}
	} else if !statefulSetAvailable(statefulSet) {
		return defaultMinIOResult{}, errors.New("existing default MinIO StatefulSet is not ready; repair it before rerunning the installer")
	}
	service, err := clientset.CoreV1().Services(defaultMinIONamespace).Get(ctx, defaultMinIORelease, metav1.GetOptions{})
	if err != nil {
		return defaultMinIOResult{}, fmt.Errorf("read default MinIO Service: %w", err)
	}
	nodePort := serviceNodePort(service, "s3")
	if nodePort == 0 {
		return defaultMinIOResult{}, errors.New("default MinIO S3 Service has no NodePort")
	}
	host, err := certificateNodeIP(certificate, nodeIPs)
	if err != nil {
		return defaultMinIOResult{}, err
	}
	return defaultMinIOResult{Endpoint: fmt.Sprintf("https://%s:%d", host, nodePort), Reused: existing}, nil
}

type registryCredential struct{ username, password string }

func lockedImageValues(lock offline.ImageLock, name, registry string) (map[string]any, error) {
	for _, image := range lock.Images {
		if image.Name != name {
			continue
		}
		at, colon := strings.LastIndex(image.Source, "@"), strings.LastIndex(image.Target, ":")
		if at <= 0 || colon <= 0 {
			return nil, fmt.Errorf("offline image %s is invalid", name)
		}
		return map[string]any{"repository": registry + "/" + image.Target[:colon], "digest": image.Source[at+1:]}, nil
	}
	return nil, fmt.Errorf("offline bundle is missing required image %s", name)
}

func ensureMinIOCredentialSecret(ctx context.Context, cluster *kubernetesadapter.Client, clientset kubernetesclient.Interface, kubeconfig []byte, mustExist bool) (map[string]string, error) {
	existing, err := clientset.CoreV1().Secrets(defaultMinIONamespace).Get(ctx, defaultMinIOCredentialSecret, metav1.GetOptions{})
	if err == nil {
		accessKey, secretKey := strings.TrimSpace(string(existing.Data["access-key"])), strings.TrimSpace(string(existing.Data["secret-key"]))
		if accessKey == "" || secretKey == "" {
			return nil, errors.New("existing default MinIO credential Secret is invalid")
		}
		return map[string]string{"access-key": accessKey, "secret-key": secretKey}, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, err
	}
	if mustExist {
		return nil, errors.New("existing default MinIO is missing its credential Secret")
	}
	accessKey, err := randomURLSecret(15)
	if err != nil {
		return nil, err
	}
	secretKey, err := randomURLSecret(36)
	if err != nil {
		return nil, err
	}
	if err := cluster.PutOpaqueSecret(ctx, kubeconfig, defaultMinIONamespace, defaultMinIOCredentialSecret, map[string][]byte{"access-key": []byte(accessKey), "secret-key": []byte(secretKey)}); err != nil {
		return nil, err
	}
	return map[string]string{"access-key": accessKey, "secret-key": secretKey}, nil
}

func ensureMinIOTLSSecret(ctx context.Context, cluster *kubernetesadapter.Client, clientset kubernetesclient.Interface, kubeconfig []byte, nodeIPs []net.IP, mustExist bool) ([]byte, error) {
	existing, err := clientset.CoreV1().Secrets(defaultMinIONamespace).Get(ctx, defaultMinIOTLSSecret, metav1.GetOptions{})
	if err == nil {
		certificate, privateKey := existing.Data["public.crt"], existing.Data["private.key"]
		if len(certificate) == 0 {
			certificate = existing.Data[corev1.TLSCertKey]
		}
		if len(privateKey) == 0 {
			privateKey = existing.Data[corev1.TLSPrivateKeyKey]
		}
		if len(certificate) == 0 || len(privateKey) == 0 {
			return nil, errors.New("existing default MinIO TLS Secret is invalid")
		}
		return append([]byte(nil), certificate...), nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, err
	}
	if mustExist {
		return nil, errors.New("existing default MinIO is missing its TLS Secret")
	}
	certificate, privateKey, err := generateMinIOTLS(nodeIPs, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	defer clearBytes(privateKey)
	if err := cluster.PutOpaqueSecret(ctx, kubeconfig, defaultMinIONamespace, defaultMinIOTLSSecret, map[string][]byte{"public.crt": certificate, "private.key": privateKey, "ca.crt": certificate}); err != nil {
		return nil, err
	}
	return certificate, nil
}

func generateMinIOTLS(nodeIPs []net.IP, now time.Time) ([]byte, []byte, error) {
	if len(nodeIPs) == 0 {
		return nil, nil, errors.New("cannot issue MinIO TLS certificate without SKS node IPs")
	}
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, nil, err
	}
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: defaultMinIORelease},
		NotBefore: now.Add(-5 * time.Minute), NotAfter: now.AddDate(10, 0, 0),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true, IsCA: true,
		DNSNames:    []string{defaultMinIORelease, defaultMinIORelease + "." + defaultMinIONamespace, defaultMinIORelease + "." + defaultMinIONamespace + ".svc"},
		IPAddresses: nodeIPs,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return nil, nil, err
	}
	encodedKey, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encodedKey}), nil
}

func clusterNodeIPs(ctx context.Context, clientset kubernetesclient.Interface) ([]net.IP, error) {
	nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list SKS node addresses: %w", err)
	}
	var values []net.IP
	for _, node := range nodes.Items {
		for _, address := range node.Status.Addresses {
			if address.Type == corev1.NodeInternalIP {
				if parsed := net.ParseIP(address.Address); parsed != nil {
					values = append(values, parsed)
				}
			}
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i].String() < values[j].String() })
	if len(values) == 0 {
		return nil, errors.New("SKS nodes expose no InternalIP")
	}
	return values, nil
}

func certificateNodeIP(certificatePEM []byte, nodeIPs []net.IP) (string, error) {
	block, _ := pem.Decode(certificatePEM)
	if block == nil {
		return "", errors.New("default MinIO TLS certificate is invalid")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", errors.New("default MinIO TLS certificate is invalid")
	}
	for _, nodeIP := range nodeIPs {
		for _, certificateIP := range certificate.IPAddresses {
			if nodeIP.Equal(certificateIP) {
				return nodeIP.String(), nil
			}
		}
	}
	return "", errors.New("default MinIO TLS certificate does not cover a current SKS node IP")
}

func serviceNodePort(service *corev1.Service, name string) int32 {
	for _, port := range service.Spec.Ports {
		if port.Name == name {
			return port.NodePort
		}
	}
	return 0
}

func statefulSetAvailable(value *appsv1.StatefulSet) bool {
	return value != nil && value.Status.ReadyReplicas == 1
}

func registerDefaultObjectStorage(ctx context.Context, baseURL, adminPassword string, kubeconfig []byte, endpoint string) error {
	client := &http.Client{Timeout: 2 * time.Minute}
	sessionCookie, csrfCookie, err := platformLogin(ctx, client, baseURL, adminPassword)
	if err != nil {
		return err
	}
	var environments []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Role string `json:"role"`
	}
	if err := platformJSON(ctx, client, http.MethodGet, baseURL+"/api/v1/environments", sessionCookie, csrfCookie, nil, &environments); err != nil {
		return err
	}
	targetID := ""
	for _, environment := range environments {
		if environment.Role == "TARGET" {
			targetID = environment.ID
			break
		}
	}
	if targetID == "" {
		var created struct {
			ID string `json:"id"`
		}
		payload := map[string]any{"name": "target-sks", "role": "TARGET", "kind": "KUBERNETES", "credential": string(kubeconfig)}
		if err := platformJSON(ctx, client, http.MethodPost, baseURL+"/api/v1/environments", sessionCookie, csrfCookie, payload, &created); err != nil {
			return fmt.Errorf("register target SKS in platform: %w", err)
		}
		targetID = created.ID
	}
	var connection struct {
		Success bool `json:"success"`
	}
	if err := platformJSON(ctx, client, http.MethodPost, baseURL+"/api/v1/environments/"+targetID+"/test", sessionCookie, csrfCookie, nil, &connection); err != nil {
		return fmt.Errorf("test target SKS connection in platform: %w", err)
	}
	if !connection.Success {
		return errors.New("test target SKS connection in platform: connection checks did not pass")
	}
	adopt := map[string]any{"environmentId": targetID, "name": "managed-minio", "endpoint": endpoint, "bucket": "velero", "region": "minio", "tlsSecretName": defaultMinIOTLSSecret}
	if err := platformJSON(ctx, client, http.MethodPost, baseURL+"/api/v1/object-storage/minio/adopt", sessionCookie, csrfCookie, adopt, nil); err != nil {
		return fmt.Errorf("register default MinIO in platform: %w", err)
	}
	return nil
}

func platformLogin(ctx context.Context, client *http.Client, baseURL, adminPassword string) (*http.Cookie, *http.Cookie, error) {
	payload, _ := json.Marshal(map[string]string{"username": "admin", "password": adminPassword})
	var lastErr error
	for attempt := 0; attempt < 30; attempt++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/v1/auth/login", bytes.NewReader(payload))
		if err != nil {
			return nil, nil, err
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err == nil && (response.StatusCode == http.StatusOK || response.StatusCode == http.StatusNoContent) {
			_ = response.Body.Close()
			var session, csrf *http.Cookie
			for _, cookie := range response.Cookies() {
				switch cookie.Name {
				case "sks_migration_session":
					session = cookie
				case "sks_migration_csrf":
					csrf = cookie
				}
			}
			if session != nil && csrf != nil {
				return session, csrf, nil
			}
			lastErr = errors.New("platform login did not return session cookies")
		} else if err != nil {
			lastErr = err
		} else {
			body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
			_ = response.Body.Close()
			lastErr = fmt.Errorf("platform login returned %s: %s", response.Status, strings.TrimSpace(string(body)))
		}
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return nil, nil, fmt.Errorf("platform did not become ready: %w", lastErr)
}

func platformJSON(ctx context.Context, client *http.Client, method, requestURL string, session, csrf *http.Cookie, input, output any) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		defer clearBytes(encoded)
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, requestURL, body)
	if err != nil {
		return err
	}
	request.AddCookie(session)
	request.AddCookie(csrf)
	request.Header.Set("X-CSRF-Token", csrf.Value)
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		value, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return fmt.Errorf("platform API returned %s: %s", response.Status, strings.TrimSpace(string(value)))
	}
	if output != nil {
		return json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(output)
	}
	return nil
}

func clearStringMap(value map[string]string) {
	for key := range value {
		value[key] = ""
	}
}
