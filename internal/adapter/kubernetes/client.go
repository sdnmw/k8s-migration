package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	domainenvironment "github.com/smartx/sks-migration-center/internal/domain/environment"
)

const defaultTimeout = 12 * time.Second

type Client struct {
	timeout time.Duration
}

type PreparedConfig struct {
	Endpoint string
	Config   *rest.Config
}

type ProbeResult struct {
	Endpoint     string
	Capabilities domainenvironment.Capabilities
	Checks       []domainenvironment.ConnectionCheck
}

func NewClient(timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Client{timeout: timeout}
}

func (c *Client) Prepare(kubeconfig []byte) (PreparedConfig, error) {
	if len(kubeconfig) == 0 {
		return PreparedConfig{}, errors.New("kubeconfig is required")
	}
	parsed, err := clientcmd.Load(kubeconfig)
	if err != nil {
		return PreparedConfig{}, errors.New("kubeconfig is not valid YAML")
	}
	if err := validatePortableConfig(parsed); err != nil {
		return PreparedConfig{}, err
	}
	config, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return PreparedConfig{}, errors.New("kubeconfig current context is incomplete")
	}
	endpoint, err := validateEndpoint(config.Host)
	if err != nil {
		return PreparedConfig{}, err
	}
	config.Timeout = c.timeout
	config.UserAgent = "sks-migration-center/0.1"
	config.QPS = 20
	config.Burst = 30
	return PreparedConfig{Endpoint: endpoint, Config: config}, nil
}

func (c *Client) Probe(ctx context.Context, kubeconfig []byte) (ProbeResult, error) {
	prepared, err := c.Prepare(kubeconfig)
	if err != nil {
		return ProbeResult{}, err
	}
	clientset, err := kubernetes.NewForConfig(prepared.Config)
	if err != nil {
		return ProbeResult{}, errors.New("could not initialize Kubernetes client")
	}

	result := ProbeResult{Endpoint: prepared.Endpoint}
	version, err := clientset.Discovery().ServerVersion()
	if err != nil {
		result.Checks = append(result.Checks, failedCheck("API Server", "无法连接或 TLS 校验失败"))
		return result, &ProbeError{Result: result, Cause: err}
	}
	result.Capabilities.KubernetesVersion = version.GitVersion
	result.Checks = append(result.Checks,
		passedCheck("API Server", "连接正常"),
		passedCheck("Kubernetes Version", version.GitVersion),
	)

	namespaceCount, err := countNamespaces(ctx, clientset)
	if err != nil {
		if apierrors.IsForbidden(err) {
			result.Checks = append(result.Checks,
				passedCheck("Authentication", "身份已由 API Server 识别"),
				failedCheck("Namespace Count", "凭证缺少命名空间读取权限"),
			)
		} else {
			result.Checks = append(result.Checks, failedCheck("Authentication", permissionMessage(err)))
		}
		return result, &ProbeError{Result: result, Cause: err}
	}
	result.Capabilities.NamespaceCount = namespaceCount
	result.Checks = append(result.Checks,
		passedCheck("Authentication", "身份认证通过"),
		passedCheck("Namespace Count", fmt.Sprintf("%d", namespaceCount)),
	)

	nodeCount, err := countNodes(ctx, clientset)
	if err != nil {
		result.Checks = append(result.Checks, failedCheck("Node Count", permissionMessage(err)))
		return result, &ProbeError{Result: result, Cause: err}
	}
	result.Capabilities.NodeCount = nodeCount
	result.Checks = append(result.Checks, passedCheck("Node Count", fmt.Sprintf("%d", nodeCount)))
	if prepared.Config.TLSClientConfig.Insecure {
		result.Checks = append(result.Checks, domainenvironment.ConnectionCheck{
			Name: "TLS 证书校验", Status: domainenvironment.CheckWarning, Message: "kubeconfig 已关闭 TLS 证书校验",
		})
	} else {
		result.Checks = append(result.Checks, passedCheck("TLS 证书校验", "已启用"))
	}
	return result, nil
}

func (c *Client) Discover(ctx context.Context, kubeconfig []byte) (ProbeResult, error) {
	result, err := c.Probe(ctx, kubeconfig)
	if err != nil {
		return result, err
	}
	prepared, err := c.Prepare(kubeconfig)
	if err != nil {
		return ProbeResult{}, err
	}
	clientset, err := kubernetes.NewForConfig(prepared.Config)
	if err != nil {
		return ProbeResult{}, errors.New("could not initialize Kubernetes discovery client")
	}
	dynamicClient, err := dynamic.NewForConfig(prepared.Config)
	if err != nil {
		return ProbeResult{}, errors.New("could not initialize Kubernetes dynamic client")
	}

	result.Capabilities.Security = map[string]any{"tlsVerification": !prepared.Config.TLSClientConfig.Insecure}
	nodes, listErr := listAllNodes(ctx, clientset)
	if listErr == nil {
		result.Capabilities.Architectures = architectures(nodes)
		result.Capabilities.OperatingSystems = operatingSystems(nodes)
		result.Capabilities.Allocatable = allocatable(nodes)
	} else {
		result.Checks = append(result.Checks, warningCheck("节点能力", permissionMessage(listErr)))
	}

	storageClasses, listErr := clientset.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
	if listErr == nil {
		for _, item := range storageClasses.Items {
			bindingMode := ""
			if item.VolumeBindingMode != nil {
				bindingMode = string(*item.VolumeBindingMode)
			}
			result.Capabilities.StorageClasses = append(result.Capabilities.StorageClasses, domainenvironment.StorageClass{
				Name: item.Name, Provisioner: item.Provisioner, Default: isDefaultStorageClass(item.Annotations),
				AllowExpansion: item.AllowVolumeExpansion != nil && *item.AllowVolumeExpansion, VolumeBindingMode: bindingMode,
			})
		}
		sort.Slice(result.Capabilities.StorageClasses, func(i, j int) bool {
			return result.Capabilities.StorageClasses[i].Name < result.Capabilities.StorageClasses[j].Name
		})
	} else {
		result.Checks = append(result.Checks, warningCheck("StorageClass", permissionMessage(listErr)))
	}

	csiDrivers, listErr := clientset.StorageV1().CSIDrivers().List(ctx, metav1.ListOptions{})
	if listErr == nil {
		for _, item := range csiDrivers.Items {
			result.Capabilities.CSIDrivers = append(result.Capabilities.CSIDrivers, item.Name)
		}
		sort.Strings(result.Capabilities.CSIDrivers)
	} else {
		result.Checks = append(result.Checks, warningCheck("CSI Driver", permissionMessage(listErr)))
	}

	ingressClasses, listErr := clientset.NetworkingV1().IngressClasses().List(ctx, metav1.ListOptions{})
	if listErr == nil {
		for _, item := range ingressClasses.Items {
			result.Capabilities.IngressClasses = append(result.Capabilities.IngressClasses, item.Name)
		}
		sort.Strings(result.Capabilities.IngressClasses)
	} else {
		result.Checks = append(result.Checks, warningCheck("IngressClass", permissionMessage(listErr)))
	}

	snapshotClasses, listErr := dynamicClient.Resource(schema.GroupVersionResource{
		Group: "snapshot.storage.k8s.io", Version: "v1", Resource: "volumesnapshotclasses",
	}).List(ctx, metav1.ListOptions{})
	if listErr == nil {
		result.Capabilities.CSIDataMover.SnapshotAPI = true
		for _, item := range snapshotClasses.Items {
			result.Capabilities.VolumeSnapshotClasses = append(result.Capabilities.VolumeSnapshotClasses, item.GetName())
			driver, _, _ := unstructured.NestedString(item.Object, "driver")
			deletionPolicy, _, _ := unstructured.NestedString(item.Object, "deletionPolicy")
			result.Capabilities.VolumeSnapshotClassDetails = append(result.Capabilities.VolumeSnapshotClassDetails, domainenvironment.VolumeSnapshotClass{
				Name: item.GetName(), Driver: driver, DeletionPolicy: deletionPolicy,
			})
		}
		sort.Strings(result.Capabilities.VolumeSnapshotClasses)
		sort.Slice(result.Capabilities.VolumeSnapshotClassDetails, func(i, j int) bool {
			return result.Capabilities.VolumeSnapshotClassDetails[i].Name < result.Capabilities.VolumeSnapshotClassDetails[j].Name
		})
	} else if !apierrors.IsNotFound(listErr) {
		result.Checks = append(result.Checks, warningCheck("VolumeSnapshotClass", permissionMessage(listErr)))
	}
	dataMover, moverErr := discoverCSIDataMover(ctx, clientset, dynamicClient, "velero", result.Capabilities.CSIDataMover.SnapshotAPI)
	if moverErr != nil {
		result.Checks = append(result.Checks, warningCheck("CSI Data Mover", permissionMessage(moverErr)))
	} else {
		result.Capabilities.CSIDataMover = dataMover
	}

	groups, groupErr := clientset.Discovery().ServerGroups()
	if groupErr == nil || groups != nil {
		result.Capabilities.APIGroups = []string{"v1"}
		if groups != nil {
			for _, group := range groups.Groups {
				result.Capabilities.APIGroups = append(result.Capabilities.APIGroups, group.Name)
			}
		}
		result.Capabilities.APIGroups = uniqueSorted(result.Capabilities.APIGroups)
	}
	if groupErr != nil {
		result.Checks = append(result.Checks, warningCheck("API Groups", "部分 API Group 无法发现"))
	}
	return result, nil
}

func (c *Client) ListNamespaces(ctx context.Context, kubeconfig []byte) ([]string, error) {
	prepared, err := c.Prepare(kubeconfig)
	if err != nil {
		return nil, err
	}
	clientset, err := kubernetes.NewForConfig(prepared.Config)
	if err != nil {
		return nil, errors.New("could not initialize Kubernetes client")
	}
	names := make([]string, 0)
	continueToken := ""
	for {
		list, listErr := clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{Limit: 500, Continue: continueToken})
		if listErr != nil {
			return nil, errors.New(permissionMessage(listErr))
		}
		for _, item := range list.Items {
			names = append(names, item.Name)
		}
		continueToken = list.Continue
		if continueToken == "" {
			sort.Strings(names)
			return names, nil
		}
	}
}

type ProbeError struct {
	Result ProbeResult
	Cause  error
}

func (e *ProbeError) Error() string { return "Kubernetes connection test failed" }
func (e *ProbeError) Unwrap() error { return e.Cause }

func validatePortableConfig(config *clientcmdapi.Config) error {
	if config.CurrentContext == "" || config.Contexts[config.CurrentContext] == nil {
		return errors.New("kubeconfig must define a valid current-context")
	}
	for _, cluster := range config.Clusters {
		if cluster.CertificateAuthority != "" {
			return errors.New("kubeconfig certificate-authority must use embedded certificate-authority-data")
		}
		if cluster.ProxyURL != "" {
			return errors.New("kubeconfig proxy-url is not allowed")
		}
	}
	for _, authInfo := range config.AuthInfos {
		if authInfo.Exec != nil {
			return errors.New("kubeconfig exec authentication plugins are not allowed")
		}
		if authInfo.AuthProvider != nil {
			return errors.New("kubeconfig auth-provider plugins are not allowed")
		}
		if authInfo.ClientCertificate != "" || authInfo.ClientKey != "" || authInfo.TokenFile != "" {
			return errors.New("kubeconfig credentials must be embedded; local file references are not allowed")
		}
	}
	return nil
}

func validateEndpoint(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return "", errors.New("Kubernetes API endpoint must be an HTTPS URL without user information")
	}
	return parsed.String(), nil
}

func countNamespaces(ctx context.Context, clientset kubernetes.Interface) (int, error) {
	count := 0
	continueToken := ""
	for {
		list, err := clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{Limit: 500, Continue: continueToken})
		if err != nil {
			return 0, err
		}
		count += len(list.Items)
		continueToken = list.Continue
		if continueToken == "" {
			return count, nil
		}
	}
}

func countNodes(ctx context.Context, clientset kubernetes.Interface) (int, error) {
	count := 0
	continueToken := ""
	for {
		list, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 500, Continue: continueToken})
		if err != nil {
			return 0, err
		}
		count += len(list.Items)
		continueToken = list.Continue
		if continueToken == "" {
			return count, nil
		}
	}
}

func listAllNodes(ctx context.Context, clientset kubernetes.Interface) ([]corev1.Node, error) {
	nodes := make([]corev1.Node, 0)
	continueToken := ""
	for {
		list, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 500, Continue: continueToken})
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, list.Items...)
		continueToken = list.Continue
		if continueToken == "" {
			return nodes, nil
		}
	}
}

func architectures(nodes []corev1.Node) []string {
	values := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if node.Status.NodeInfo.Architecture != "" {
			values = append(values, node.Status.NodeInfo.Architecture)
		}
	}
	return uniqueSorted(values)
}

func operatingSystems(nodes []corev1.Node) []string {
	values := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if node.Status.NodeInfo.OperatingSystem != "" {
			values = append(values, node.Status.NodeInfo.OperatingSystem)
		}
	}
	return uniqueSorted(values)
}

func allocatable(nodes []corev1.Node) map[string]string {
	totals := corev1.ResourceList{}
	for _, node := range nodes {
		for name, quantity := range node.Status.Allocatable {
			total := totals[name]
			total.Add(quantity)
			totals[name] = total
		}
	}
	result := make(map[string]string, len(totals))
	for name, quantity := range totals {
		result[string(name)] = quantity.String()
	}
	return result
}

func isDefaultStorageClass(annotations map[string]string) bool {
	return annotations["storageclass.kubernetes.io/is-default-class"] == "true" ||
		annotations["storageclass.beta.kubernetes.io/is-default-class"] == "true"
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func permissionMessage(err error) string {
	if strings.Contains(strings.ToLower(err.Error()), "forbidden") {
		return "凭证缺少迁移发现所需的读取权限"
	}
	if strings.Contains(strings.ToLower(err.Error()), "unauthorized") {
		return "Kubernetes 凭证未通过认证"
	}
	return "资源读取失败"
}

func passedCheck(name, message string) domainenvironment.ConnectionCheck {
	return domainenvironment.ConnectionCheck{Name: name, Status: domainenvironment.CheckPassed, Message: message}
}

func failedCheck(name, message string) domainenvironment.ConnectionCheck {
	return domainenvironment.ConnectionCheck{Name: name, Status: domainenvironment.CheckFailed, Message: message}
}

func warningCheck(name, message string) domainenvironment.ConnectionCheck {
	return domainenvironment.ConnectionCheck{Name: name, Status: domainenvironment.CheckWarning, Message: message}
}
