package addon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage/driver"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

var ErrReleaseNotFound = errors.New("Helm release not found")

type helmEngine struct {
	configuration *action.Configuration
}

func newHelmEngine(kubeconfig []byte, namespace string) (engine, error) {
	getter, err := newMemoryRESTClientGetter(kubeconfig, namespace)
	if err != nil {
		return nil, err
	}
	configuration := new(action.Configuration)
	if err := configuration.Init(getter, namespace, "secret", func(format string, values ...any) {
		slog.Debug("helm sdk", "message", fmt.Sprintf(format, values...))
	}); err != nil {
		return nil, err
	}
	return &helmEngine{configuration: configuration}, nil
}

func (e *helmEngine) Get(name string) (*release.Release, error) {
	value, err := action.NewGet(e.configuration).Run(name)
	if errors.Is(err, driver.ErrReleaseNotFound) {
		return nil, ErrReleaseNotFound
	}
	return value, err
}

func (e *helmEngine) Install(ctx context.Context, name, namespace string, chart *chart.Chart, values map[string]any, createNamespace bool, timeout time.Duration) (*release.Release, error) {
	client := action.NewInstall(e.configuration)
	client.ReleaseName, client.Namespace, client.CreateNamespace = name, namespace, createNamespace
	client.Atomic, client.Wait, client.WaitForJobs, client.Timeout = true, true, true, timeout
	return client.RunWithContext(ctx, chart, values)
}

func (e *helmEngine) Upgrade(ctx context.Context, name, namespace string, chart *chart.Chart, values map[string]any, timeout time.Duration) (*release.Release, error) {
	client := action.NewUpgrade(e.configuration)
	client.Namespace, client.Atomic, client.CleanupOnFail = namespace, true, true
	client.Wait, client.WaitForJobs, client.Timeout = true, true, timeout
	return client.RunWithContext(ctx, name, chart, values)
}

func (e *helmEngine) Uninstall(name string, timeout time.Duration) error {
	client := action.NewUninstall(e.configuration)
	client.Wait, client.Timeout = true, timeout
	_, err := client.Run(name)
	if errors.Is(err, driver.ErrReleaseNotFound) {
		return ErrReleaseNotFound
	}
	return err
}

type memoryRESTClientGetter struct {
	config    clientcmdapi.Config
	namespace string
}

func newMemoryRESTClientGetter(kubeconfig []byte, namespace string) (*memoryRESTClientGetter, error) {
	config, err := clientcmd.Load(kubeconfig)
	if err != nil {
		return nil, errors.New("kubeconfig is invalid")
	}
	return &memoryRESTClientGetter{config: *config.DeepCopy(), namespace: namespace}, nil
}

func (g *memoryRESTClientGetter) ToRESTConfig() (*rest.Config, error) {
	return g.loader().ClientConfig()
}

func (g *memoryRESTClientGetter) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	config, err := g.ToRESTConfig()
	if err != nil {
		return nil, err
	}
	client, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return nil, err
	}
	return memory.NewMemCacheClient(client), nil
}

func (g *memoryRESTClientGetter) ToRESTMapper() (meta.RESTMapper, error) {
	discoveryClient, err := g.ToDiscoveryClient()
	if err != nil {
		return nil, err
	}
	return restmapper.NewDeferredDiscoveryRESTMapper(discoveryClient), nil
}

func (g *memoryRESTClientGetter) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	return g.loader()
}

func (g *memoryRESTClientGetter) loader() clientcmd.ClientConfig {
	return clientcmd.NewDefaultClientConfig(g.config, &clientcmd.ConfigOverrides{Context: clientcmdapi.Context{Namespace: g.namespace}})
}
