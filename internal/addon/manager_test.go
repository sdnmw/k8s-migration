package addon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/release"
)

type fakeEngine struct {
	existing      *release.Release
	getErr        error
	installed     bool
	upgraded      bool
	uninstalled   bool
	values        map[string]any
	timeout       time.Duration
	createNS      bool
	releaseResult *release.Release
}

func (e *fakeEngine) Get(string) (*release.Release, error) { return e.existing, e.getErr }

func (e *fakeEngine) Install(_ context.Context, _, _ string, _ *chart.Chart, values map[string]any, createNamespace bool, timeout time.Duration) (*release.Release, error) {
	e.installed, e.values, e.createNS, e.timeout = true, values, createNamespace, timeout
	return e.releaseResult, nil
}

func (e *fakeEngine) Upgrade(_ context.Context, _, _ string, _ *chart.Chart, values map[string]any, timeout time.Duration) (*release.Release, error) {
	e.upgraded, e.values, e.timeout = true, values, timeout
	return e.releaseResult, nil
}

func (e *fakeEngine) Uninstall(string, time.Duration) error {
	e.uninstalled = true
	return nil
}

func TestManagerInstallAndUpgradeUseLockedChart(t *testing.T) {
	root := writeChart(t, "1.2.3")
	for _, test := range []struct {
		name      string
		existing  *release.Release
		getErr    error
		installed bool
		upgraded  bool
	}{
		{name: "install", getErr: ErrReleaseNotFound, installed: true},
		{name: "upgrade", existing: &release.Release{Name: "demo"}, upgraded: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := &release.Release{
				Name: "demo", Namespace: "addons", Version: 2,
				Info:  &release.Info{Status: release.StatusDeployed},
				Chart: &chart.Chart{Metadata: &chart.Metadata{Name: "demo", Version: "1.2.3", AppVersion: "4.5.6"}},
			}
			fake := &fakeEngine{existing: test.existing, getErr: test.getErr, releaseResult: result}
			var captured []byte
			manager, err := newManager(root, func(value []byte, namespace string) (engine, error) {
				captured = append([]byte(nil), value...)
				if namespace != "addons" {
					t.Fatalf("unexpected namespace %q", namespace)
				}
				return fake, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			kubeconfig := []byte("secret-kubeconfig")
			values := map[string]any{"replicas": 1}
			state, err := manager.InstallOrUpgrade(context.Background(), kubeconfig, InstallRequest{
				ReleaseName: "demo", Namespace: "addons", ChartPath: "demo", Version: "1.2.3",
				Values: values, CreateNamespace: true, Timeout: 2 * time.Hour,
			})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(captured, []byte("secret-kubeconfig")) {
				t.Fatal("factory did not receive kubeconfig")
			}
			for _, value := range kubeconfig {
				if value != 0 {
					t.Fatal("manager did not clear caller-owned kubeconfig buffer")
				}
			}
			if fake.installed != test.installed || fake.upgraded != test.upgraded {
				t.Fatalf("unexpected branch: installed=%v upgraded=%v", fake.installed, fake.upgraded)
			}
			if fake.timeout != 15*time.Minute {
				t.Fatalf("unsafe timeout was not replaced: %s", fake.timeout)
			}
			if state.Status != "deployed" || state.Chart != "demo" || state.Revision != 2 {
				t.Fatalf("unexpected release state: %#v", state)
			}
			values["replicas"] = 3
			if fake.values["replicas"] != 1 {
				t.Fatal("manager did not isolate top-level values map")
			}
		})
	}
}

func TestManagerRejectsVersionMismatchAndSymlinkEscape(t *testing.T) {
	root := writeChart(t, "1.2.3")
	manager, err := newManager(root, func([]byte, string) (engine, error) {
		return &fakeEngine{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.InstallOrUpgrade(context.Background(), []byte("kubeconfig"), InstallRequest{
		ReleaseName: "demo", Namespace: "addons", ChartPath: "demo", Version: "9.9.9",
	})
	if err == nil {
		t.Fatal("expected locked version mismatch")
	}

	external := writeChart(t, "1.2.3")
	if err := os.Symlink(filepath.Join(external, "demo"), filepath.Join(root, "escaped")); err != nil {
		t.Fatal(err)
	}
	_, err = manager.InstallOrUpgrade(context.Background(), []byte("kubeconfig"), InstallRequest{
		ReleaseName: "demo", Namespace: "addons", ChartPath: "escaped", Version: "1.2.3",
	})
	if err == nil {
		t.Fatal("expected symlink escape rejection")
	}
}

func TestManagerPropagatesUnexpectedGetFailure(t *testing.T) {
	root := writeChart(t, "1.2.3")
	manager, err := newManager(root, func([]byte, string) (engine, error) {
		return &fakeEngine{getErr: errors.New("cluster unavailable")}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.InstallOrUpgrade(context.Background(), []byte("kubeconfig"), InstallRequest{
		ReleaseName: "demo", Namespace: "addons", ChartPath: "demo", Version: "1.2.3",
	})
	if err == nil {
		t.Fatal("expected cluster error")
	}
}

func TestMemoryRESTClientGetterParsesWithoutTemporaryFile(t *testing.T) {
	value := []byte(`apiVersion: v1
kind: Config
current-context: demo
clusters:
- name: cluster
  cluster:
    server: https://127.0.0.1:6443
    insecure-skip-tls-verify: true
users:
- name: admin
  user:
    token: token-value
contexts:
- name: demo
  context:
    cluster: cluster
    user: admin
`)
	getter, err := newMemoryRESTClientGetter(value, "addons")
	if err != nil {
		t.Fatal(err)
	}
	config, err := getter.ToRESTConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.Host != "https://127.0.0.1:6443" || config.BearerToken != "token-value" {
		t.Fatalf("unexpected REST config: host=%q token=%q", config.Host, config.BearerToken)
	}
	namespace, _, err := getter.ToRawKubeConfigLoader().Namespace()
	if err != nil || namespace != "addons" {
		t.Fatalf("unexpected namespace %q: %v", namespace, err)
	}
}

func writeChart(t *testing.T, version string) string {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "demo")
	if err := os.MkdirAll(filepath.Join(path, "templates"), 0o750); err != nil {
		t.Fatal(err)
	}
	chartYAML := "apiVersion: v2\nname: demo\nversion: " + version + "\nappVersion: 4.5.6\ntype: application\n"
	if err := os.WriteFile(filepath.Join(path, "Chart.yaml"), []byte(chartYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "templates", "configmap.yaml"), []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: demo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}
