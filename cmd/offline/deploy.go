package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubernetesclient "k8s.io/client-go/kubernetes"

	kubernetesadapter "github.com/smartx/sks-migration-center/internal/adapter/kubernetes"
	"github.com/smartx/sks-migration-center/internal/addon"
	"github.com/smartx/sks-migration-center/internal/offline"
)

const (
	defaultPlatformNamespace  = "sks-migration-center"
	platformAPIDeployment     = "sks-migration-center-api"
	platformRegistrySecret    = "sks-migration-registry"
	platformApplicationSecret = "sks-migration-center-secrets"
)

type deployOptions struct {
	directory         string
	harborAddress     string
	harborProject     string
	usernameFile      string
	passwordFile      string
	kubeconfigFile    string
	adminPasswordFile string
	masterKeyFile     string
	storageClass      string
	minioStorageSize  string
	namespace         string
	insecureRegistry  bool
	cookieSecure      bool
	skipDefaultMinIO  bool
}

type deploymentResult struct {
	Release                string   `json:"release"`
	Revision               int      `json:"revision"`
	Namespace              string   `json:"namespace"`
	StorageClass           string   `json:"storageClass"`
	ImportedImages         int      `json:"importedImages"`
	URLs                   []string `json:"urls"`
	DefaultMinIOEndpoint   string   `json:"defaultMinioEndpoint,omitempty"`
	DefaultMinIORegistered bool     `json:"defaultMinioRegistered"`
	GeneratedAdminPassword string   `json:"generatedAdminPassword,omitempty"`
}

type platformSecretWriter interface {
	PutOpaqueSecret(context.Context, []byte, string, string, map[string][]byte) error
}

func deployBundle(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("deploy", flag.ContinueOnError)
	options := deployOptions{}
	flags.StringVar(&options.directory, "directory", "", "extracted verified offline bundle directory")
	flags.StringVar(&options.harborAddress, "harbor-address", "", "Harbor base URL, for example https://harbor.example.com")
	flags.StringVar(&options.harborProject, "harbor-project", "", "existing or new Harbor project")
	flags.StringVar(&options.usernameFile, "harbor-username-file", "", "Harbor username file")
	flags.StringVar(&options.passwordFile, "harbor-password-file", "", "Harbor password file")
	flags.StringVar(&options.kubeconfigFile, "sks-kubeconfig", "", "target SKS workload-cluster kubeconfig")
	flags.StringVar(&options.adminPasswordFile, "admin-password-file", "", "optional platform administrator password file; generated when omitted")
	flags.StringVar(&options.masterKeyFile, "master-key-file", "", "optional base64-encoded 32-byte credential master key file; generated when omitted")
	flags.StringVar(&options.storageClass, "storage-class", "", "optional target RWO StorageClass; SmartX ELF CSI is discovered when omitted")
	flags.StringVar(&options.minioStorageSize, "minio-storage-size", "100Gi", "default MinIO RWO PVC size")
	flags.StringVar(&options.namespace, "namespace", defaultPlatformNamespace, "platform namespace")
	flags.BoolVar(&options.insecureRegistry, "insecure-registry", false, "allow a trusted lab Harbor with an untrusted certificate or HTTP")
	flags.BoolVar(&options.cookieSecure, "cookie-secure", false, "mark login cookies Secure when the platform is exposed through HTTPS")
	flags.BoolVar(&options.skipDefaultMinIO, "skip-default-minio", false, "install the platform without its default managed MinIO")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := options.validate(); err != nil {
		return err
	}
	if _, err := offline.VerifyDirectory(options.directory); err != nil {
		return fmt.Errorf("refuse deployment from unverified bundle: %w", err)
	}

	username, err := readSecret(options.usernameFile)
	if err != nil {
		return err
	}
	password, err := readSecret(options.passwordFile)
	if err != nil {
		return err
	}
	harborURL, err := normalizeHarborURL(options.harborAddress, options.insecureRegistry)
	if err != nil {
		return err
	}
	if err := ensureHarborProject(context.Background(), harborURL, options.harborProject, username, password, options.insecureRegistry); err != nil {
		return err
	}
	lock, err := loadBundleLock(options.directory)
	if err != nil {
		return err
	}
	retargeted, err := retargetLock(lock, options.harborProject)
	if err != nil {
		return err
	}
	importer, err := offline.NewRegistryImporter(offline.RegistryConfig{
		Endpoint: harborURL.String(), Username: username, Password: password, Insecure: options.insecureRegistry,
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	encoder := json.NewEncoder(output)
	for _, image := range retargeted.Images {
		result, importErr := importer.Import(ctx, options.directory, image)
		if importErr != nil {
			return importErr
		}
		if err := encoder.Encode(map[string]any{"stage": "import", "result": result}); err != nil {
			return err
		}
	}

	kubeconfig, err := os.ReadFile(options.kubeconfigFile)
	if err != nil {
		return fmt.Errorf("read SKS kubeconfig: %w", err)
	}
	defer clearBytes(kubeconfig)
	adminPassword := ""
	if options.adminPasswordFile != "" {
		adminPassword, err = readSecret(options.adminPasswordFile)
		if err != nil {
			return err
		}
	}
	masterKey := ""
	if options.masterKeyFile != "" {
		masterKey, err = readSecret(options.masterKeyFile)
		if err != nil {
			return err
		}
	}

	cluster := kubernetesadapter.NewClient(30 * time.Second)
	prepared, err := cluster.Prepare(kubeconfig)
	if err != nil {
		return err
	}
	clientset, err := kubernetesclient.NewForConfig(prepared.Config)
	if err != nil {
		return fmt.Errorf("create SKS client: %w", err)
	}
	platformAlreadyInstalled, err := platformInstallationExists(ctx, clientset, options.namespace)
	if err != nil {
		return err
	}
	if err := cluster.EnsureNamespace(ctx, kubeconfig, options.namespace); err != nil {
		return err
	}
	if options.storageClass == "" {
		options.storageClass, err = discoverSmartXStorageClass(ctx, clientset)
		if err != nil {
			return err
		}
	}
	if err := putRegistrySecret(ctx, clientset, options.namespace, harborURL.Host, username, password); err != nil {
		return err
	}
	generatedAdminPassword, err := ensureApplicationSecret(ctx, cluster, clientset, kubeconfig, options.namespace, adminPassword, masterKey)
	if err != nil {
		return err
	}

	manager, err := addon.NewManager(filepath.Join(options.directory, "charts"))
	if err != nil {
		return err
	}
	defaultMinIO := defaultMinIOResult{}
	if !options.skipDefaultMinIO {
		defaultMinIO, err = ensureDefaultMinIO(ctx, manager, cluster, clientset, kubeconfig, retargeted, harborURL.Host, options.storageClass, options.minioStorageSize, registryCredential{username: username, password: password})
		if err != nil {
			return fmt.Errorf("prepare default MinIO: %w", err)
		}
	}
	values, err := deploymentValues(retargeted, harborURL.Host, options.harborProject, options.storageClass, options.cookieSecure)
	if err != nil {
		return err
	}
	state, err := manager.InstallOrUpgrade(ctx, append([]byte(nil), kubeconfig...), addon.InstallRequest{
		ReleaseName: defaultPlatformNamespace, Namespace: options.namespace, ChartPath: "sks-migration-center",
		Version: "0.1.0", Values: values, Timeout: 30 * time.Minute,
	})
	if err != nil {
		return err
	}
	urls, err := platformURLs(ctx, clientset, options.namespace)
	if err != nil {
		return err
	}
	defaultMinIORegistered := false
	if defaultMinIO.Endpoint != "" {
		applicationSecret, err := clientset.CoreV1().Secrets(options.namespace).Get(ctx, platformApplicationSecret, metav1.GetOptions{})
		if err != nil || len(applicationSecret.Data["admin-password"]) == 0 {
			return errors.New("read platform administrator password for default MinIO registration")
		}
		if err := registerDefaultObjectStorage(ctx, urls[0], string(applicationSecret.Data["admin-password"]), kubeconfig, defaultMinIO.Endpoint); err != nil {
			// An administrator may have changed the password after the first
			// installation. Do not make a normal upgrade depend on the stale
			// bootstrap value retained in the Kubernetes Secret. A fresh install
			// must still fail closed so it cannot finish without object storage.
			if !platformAlreadyInstalled {
				return err
			}
		} else {
			defaultMinIORegistered = true
		}
	}
	return encoder.Encode(map[string]any{"stage": "complete", "result": deploymentResult{
		Release: state.Name, Revision: state.Revision, Namespace: options.namespace, StorageClass: options.storageClass,
		ImportedImages: len(retargeted.Images), URLs: urls, DefaultMinIOEndpoint: defaultMinIO.Endpoint,
		DefaultMinIORegistered: defaultMinIORegistered, GeneratedAdminPassword: generatedAdminPassword,
	}})
}

func platformInstallationExists(ctx context.Context, clientset kubernetesclient.Interface, namespace string) (bool, error) {
	_, err := clientset.AppsV1().Deployments(namespace).Get(ctx, platformAPIDeployment, metav1.GetOptions{})
	if err == nil {
		return true, nil
	}
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	return false, fmt.Errorf("inspect existing platform installation: %w", err)
}

func (o deployOptions) validate() error {
	for name, value := range map[string]string{
		"directory": o.directory, "harbor-address": o.harborAddress, "harbor-project": o.harborProject,
		"harbor-username-file": o.usernameFile, "harbor-password-file": o.passwordFile,
		"sks-kubeconfig": o.kubeconfigFile, "namespace": o.namespace,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("deploy requires --%s", name)
		}
	}
	_, err := offline.RetargetImage(offline.LockedImage{Target: "default/probe:v1"}, o.harborProject)
	return err
}

func normalizeHarborURL(value string, insecure bool) (*url.URL, error) {
	value = strings.TrimSpace(value)
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	parsed, err := url.Parse(strings.TrimRight(value, "/"))
	if err != nil || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("Harbor address must be a base registry URL without a path")
	}
	if parsed.Scheme != "https" && !(insecure && parsed.Scheme == "http") {
		return nil, errors.New("Harbor address must use HTTPS unless --insecure-registry is explicit")
	}
	parsed.Path = ""
	return parsed, nil
}

func ensureHarborProject(ctx context.Context, endpoint *url.URL, project, username, password string, insecure bool) error {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 -- explicit lab option.
	}
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	payload, _ := json.Marshal(map[string]any{"project_name": project, "public": false})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String()+"/api/v2.0/projects", strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	request.SetBasicAuth(username, password)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("create or inspect Harbor project: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return fmt.Errorf("Harbor project request returned %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

func loadBundleLock(directory string) (offline.ImageLock, error) {
	file, err := os.Open(filepath.Join(directory, offline.ImageLockName))
	if err != nil {
		return offline.ImageLock{}, err
	}
	defer file.Close()
	return offline.LoadImageLock(file)
}

func retargetLock(lock offline.ImageLock, project string) (offline.ImageLock, error) {
	result := lock
	result.Images = make([]offline.LockedImage, len(lock.Images))
	for index, image := range lock.Images {
		retargeted, err := offline.RetargetImage(image, project)
		if err != nil {
			return offline.ImageLock{}, err
		}
		result.Images[index] = retargeted
	}
	return result, result.Validate()
}

func putRegistrySecret(ctx context.Context, clientset kubernetesclient.Interface, namespace, registry, username, password string) error {
	auth := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	config, err := json.Marshal(map[string]any{"auths": map[string]any{registry: map[string]string{
		"username": username, "password": password, "auth": auth,
	}}})
	if err != nil {
		return err
	}
	defer clearBytes(config)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: platformRegistrySecret, Namespace: namespace, Labels: map[string]string{"app.kubernetes.io/managed-by": defaultPlatformNamespace}},
		Type:       corev1.SecretTypeDockerConfigJson, Data: map[string][]byte{corev1.DockerConfigJsonKey: config},
	}
	secrets := clientset.CoreV1().Secrets(namespace)
	existing, err := secrets.Get(ctx, secret.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = secrets.Create(ctx, secret, metav1.CreateOptions{})
	} else if err == nil {
		secret.ResourceVersion = existing.ResourceVersion
		_, err = secrets.Update(ctx, secret, metav1.UpdateOptions{})
	}
	if err != nil {
		return fmt.Errorf("write Harbor image pull Secret: %w", err)
	}
	return nil
}

func ensureApplicationSecret(ctx context.Context, cluster platformSecretWriter, clientset kubernetesclient.Interface, kubeconfig []byte, namespace, adminPassword, masterKey string) (string, error) {
	existing, err := clientset.CoreV1().Secrets(namespace).Get(ctx, platformApplicationSecret, metav1.GetOptions{})
	if err == nil {
		for _, key := range []string{"admin-password", "master-key", "postgres-password", "database-url"} {
			if len(existing.Data[key]) == 0 {
				return "", fmt.Errorf("existing platform Secret is missing %q", key)
			}
		}
		return "", nil
	}
	if !apierrors.IsNotFound(err) {
		return "", err
	}
	generatedAdminPassword := ""
	if adminPassword == "" {
		adminPassword, err = randomURLSecret(24)
		if err != nil {
			return "", err
		}
		generatedAdminPassword = adminPassword
	}
	if masterKey == "" {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return "", err
		}
		masterKey = base64.StdEncoding.EncodeToString(key)
		clearBytes(key)
	}
	decodedKey, err := base64.StdEncoding.DecodeString(masterKey)
	if err != nil || len(decodedKey) != 32 {
		return "", errors.New("master key file must contain one base64-encoded 32-byte key")
	}
	clearBytes(decodedKey)
	randomPassword := make([]byte, 32)
	if _, err := rand.Read(randomPassword); err != nil {
		return "", err
	}
	postgresPassword := base64.RawURLEncoding.EncodeToString(randomPassword)
	clearBytes(randomPassword)
	data := map[string][]byte{
		"admin-password": []byte(adminPassword), "master-key": []byte(masterKey),
		"postgres-password": []byte(postgresPassword),
		"database-url":      []byte(fmt.Sprintf("postgres://migration:%s@sks-migration-center-postgres:5432/migration?sslmode=disable", postgresPassword)),
	}
	defer func() {
		for _, value := range data {
			clearBytes(value)
		}
	}()
	if err := cluster.PutOpaqueSecret(ctx, kubeconfig, namespace, platformApplicationSecret, data); err != nil {
		return "", err
	}
	return generatedAdminPassword, nil
}

func randomURLSecret(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	result := base64.RawURLEncoding.EncodeToString(value)
	clearBytes(value)
	return result, nil
}

func discoverSmartXStorageClass(ctx context.Context, clientset kubernetesclient.Interface) (string, error) {
	classes, err := clientset.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("discover target StorageClass: %w", err)
	}
	type candidate struct {
		name      string
		isDefault bool
	}
	var candidates []candidate
	for _, class := range classes.Items {
		if class.Provisioner == "smtx-elf-csi-driver" || class.Provisioner == "com.smartx.elf-csi-driver" {
			isDefault := class.Annotations["storageclass.kubernetes.io/is-default-class"] == "true" || class.Annotations["storageclass.beta.kubernetes.io/is-default-class"] == "true"
			candidates = append(candidates, candidate{name: class.Name, isDefault: isDefault})
		}
	}
	if len(candidates) == 0 {
		return "", errors.New("target cluster has no SmartX ELF CSI StorageClass; pass --storage-class explicitly after configuring storage")
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].isDefault != candidates[j].isDefault {
			return candidates[i].isDefault
		}
		return candidates[i].name < candidates[j].name
	})
	return candidates[0].name, nil
}

func deploymentValues(lock offline.ImageLock, registry, project, storageClass string, cookieSecure bool) (map[string]any, error) {
	images := make(map[string]offline.LockedImage, len(lock.Images))
	for _, image := range lock.Images {
		images[image.Name] = image
	}
	value := func(name string) (map[string]any, error) {
		image, ok := images[name]
		if !ok {
			return nil, fmt.Errorf("offline bundle is missing required image %s", name)
		}
		at := strings.LastIndex(image.Source, "@")
		colon := strings.LastIndex(image.Target, ":")
		repository := image.Target[:colon]
		return map[string]any{"repository": registry + "/" + repository, "digest": image.Source[at+1:]}, nil
	}
	required := []string{"platform-api", "platform-worker", "platform-web", "postgresql", "minio", "velero", "velero-plugin-for-aws", "nfsplugin", "csi-provisioner", "csi-resizer", "csi-node-driver-registrar", "livenessprobe", "nfs-probe-helper", "kompose", "kopia"}
	resolved := map[string]map[string]any{}
	for _, name := range required {
		entry, err := value(name)
		if err != nil {
			return nil, err
		}
		resolved[name] = entry
	}
	return map[string]any{
		"global": map[string]any{
			"storageClass":           storageClass,
			"imagePullSecrets":       []any{map[string]any{"name": platformRegistrySecret}},
			"composeImageRepository": registry + "/" + project + "/compose-migrations",
			"registryPullSecretName": platformRegistrySecret,
		},
		"image": map[string]any{"api": resolved["platform-api"], "worker": resolved["platform-worker"], "web": resolved["platform-web"], "postgres": resolved["postgresql"], "pullPolicy": "IfNotPresent"},
		"addonImages": map[string]any{
			"minio":  resolved["minio"],
			"velero": resolved["velero"], "veleroAWSPlugin": resolved["velero-plugin-for-aws"],
			"nfsPlugin": resolved["nfsplugin"], "nfsProvisioner": resolved["csi-provisioner"], "nfsResizer": resolved["csi-resizer"],
			"nfsLiveness": resolved["livenessprobe"], "nfsRegistrar": resolved["csi-node-driver-registrar"], "nfsProbe": resolved["nfs-probe-helper"],
			"kompose": resolved["kompose"], "kopia": resolved["kopia"],
		},
		"service":       map[string]any{"type": "NodePort", "port": 80},
		"config":        map[string]any{"cookieSecure": cookieSecure},
		"networkPolicy": map[string]any{"enabled": false},
	}, nil
}

func platformURLs(ctx context.Context, clientset kubernetesclient.Interface, namespace string) ([]string, error) {
	service, err := clientset.CoreV1().Services(namespace).Get(ctx, defaultPlatformNamespace, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	var nodePort int32
	for _, port := range service.Spec.Ports {
		if port.Name == "http" {
			nodePort = port.NodePort
		}
	}
	if nodePort == 0 {
		return nil, errors.New("platform NodePort was not allocated")
	}
	nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var result []string
	for _, node := range nodes.Items {
		for _, address := range node.Status.Addresses {
			if address.Type == corev1.NodeInternalIP && net.ParseIP(address.Address) != nil {
				result = append(result, fmt.Sprintf("http://%s:%d", address.Address, nodePort))
			}
		}
	}
	if len(result) == 0 {
		return nil, errors.New("SKS nodes expose no InternalIP")
	}
	return result, nil
}

func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
