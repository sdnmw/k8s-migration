package addon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/release"
)

var releaseNamePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

type InstallRequest struct {
	ReleaseName     string
	Namespace       string
	ChartPath       string
	Version         string
	Values          map[string]any
	CreateNamespace bool
	Timeout         time.Duration
}

type ReleaseState struct {
	Name       string `json:"name"`
	Namespace  string `json:"namespace"`
	Revision   int    `json:"revision"`
	Status     string `json:"status"`
	Chart      string `json:"chart"`
	Version    string `json:"version"`
	AppVersion string `json:"appVersion"`
}

type engine interface {
	Get(string) (*release.Release, error)
	Install(context.Context, string, string, *chart.Chart, map[string]any, bool, time.Duration) (*release.Release, error)
	Upgrade(context.Context, string, string, *chart.Chart, map[string]any, time.Duration) (*release.Release, error)
	Uninstall(string, time.Duration) error
}

type engineFactory func([]byte, string) (engine, error)

type Manager struct {
	chartRoot string
	factory   engineFactory
}

func NewManager(chartRoot string) (*Manager, error) {
	return newManager(chartRoot, newHelmEngine)
}

func newManager(chartRoot string, factory engineFactory) (*Manager, error) {
	absolute, err := filepath.Abs(chartRoot)
	if err != nil || strings.TrimSpace(chartRoot) == "" || factory == nil {
		return nil, errors.New("chart root and Helm SDK engine factory are required")
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, fmt.Errorf("resolve offline chart root: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return nil, errors.New("offline chart root must be an existing directory")
	}
	return &Manager{chartRoot: resolved, factory: factory}, nil
}

func (m *Manager) InstallOrUpgrade(ctx context.Context, kubeconfig []byte, request InstallRequest) (ReleaseState, error) {
	defer clearBytes(kubeconfig)
	chart, err := m.loadChart(request)
	if err != nil {
		return ReleaseState{}, err
	}
	engine, err := m.prepare(kubeconfig, request.Namespace)
	if err != nil {
		return ReleaseState{}, err
	}
	if request.Timeout <= 0 || request.Timeout > time.Hour {
		request.Timeout = 15 * time.Minute
	}
	existing, err := engine.Get(request.ReleaseName)
	var installed *release.Release
	if err == nil && existing != nil {
		installed, err = engine.Upgrade(ctx, request.ReleaseName, request.Namespace, chart, cloneValues(request.Values), request.Timeout)
	} else if errors.Is(err, ErrReleaseNotFound) {
		installed, err = engine.Install(ctx, request.ReleaseName, request.Namespace, chart, cloneValues(request.Values), request.CreateNamespace, request.Timeout)
	}
	if err != nil {
		return ReleaseState{}, fmt.Errorf("Helm SDK install or upgrade %s: %w", request.ReleaseName, err)
	}
	return releaseState(installed), nil
}

func (m *Manager) Status(kubeconfig []byte, namespace, releaseName string) (ReleaseState, error) {
	defer clearBytes(kubeconfig)
	if err := validateReleaseIdentity(namespace, releaseName); err != nil {
		return ReleaseState{}, err
	}
	engine, err := m.prepare(kubeconfig, namespace)
	if err != nil {
		return ReleaseState{}, err
	}
	value, err := engine.Get(releaseName)
	if err != nil {
		return ReleaseState{}, err
	}
	return releaseState(value), nil
}

func (m *Manager) Uninstall(kubeconfig []byte, namespace, releaseName string, timeout time.Duration) error {
	defer clearBytes(kubeconfig)
	if err := validateReleaseIdentity(namespace, releaseName); err != nil {
		return err
	}
	if timeout <= 0 || timeout > time.Hour {
		timeout = 15 * time.Minute
	}
	engine, err := m.prepare(kubeconfig, namespace)
	if err != nil {
		return err
	}
	if err := engine.Uninstall(releaseName, timeout); err != nil {
		return fmt.Errorf("Helm SDK uninstall %s: %w", releaseName, err)
	}
	return nil
}

func (m *Manager) loadChart(request InstallRequest) (*chart.Chart, error) {
	if err := validateReleaseIdentity(request.Namespace, request.ReleaseName); err != nil {
		return nil, err
	}
	if strings.TrimSpace(request.Version) == "" {
		return nil, errors.New("locked add-on version is required")
	}
	chartPath, err := confinedChartPath(m.chartRoot, request.ChartPath)
	if err != nil {
		return nil, err
	}
	value, err := loader.Load(chartPath)
	if err != nil {
		return nil, fmt.Errorf("load locked Helm chart: %w", err)
	}
	if value.Metadata == nil || (value.Metadata.Version != request.Version && value.Metadata.AppVersion != request.Version) {
		return nil, fmt.Errorf("chart version %q does not match locked add-on version %q", value.Metadata.Version, request.Version)
	}
	return value, nil
}

func (m *Manager) prepare(kubeconfig []byte, namespace string) (engine, error) {
	if len(kubeconfig) == 0 || len(kubeconfig) > 1<<20 {
		return nil, errors.New("kubeconfig must contain between 1 byte and 1 MiB")
	}
	value, err := m.factory(kubeconfig, namespace)
	clearBytes(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("initialize Helm SDK from in-memory kubeconfig: %w", err)
	}
	return value, nil
}

func validateReleaseIdentity(namespace, releaseName string) error {
	if !releaseNamePattern.MatchString(releaseName) || len(releaseName) > 53 {
		return errors.New("release name must be a valid lower-case Helm release name of at most 53 characters")
	}
	if !releaseNamePattern.MatchString(namespace) || len(namespace) > 63 {
		return errors.New("namespace must be a valid lower-case Kubernetes name of at most 63 characters")
	}
	return nil
}

func confinedChartPath(root, relative string) (string, error) {
	if relative == "" || filepath.IsAbs(relative) || filepath.Clean(relative) != relative {
		return "", errors.New("chart path must be a clean relative bundle path")
	}
	candidate := filepath.Join(root, relative)
	path, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve offline chart path: %w", err)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("chart path escapes the offline chart root")
	}
	return path, nil
}

func releaseState(value *release.Release) ReleaseState {
	if value == nil {
		return ReleaseState{}
	}
	state := ReleaseState{Name: value.Name, Namespace: value.Namespace, Version: "unknown", AppVersion: "unknown"}
	if value.Info != nil {
		state.Status = value.Info.Status.String()
	}
	state.Revision = value.Version
	if value.Chart != nil && value.Chart.Metadata != nil {
		state.Chart, state.Version, state.AppVersion = value.Chart.Metadata.Name, value.Chart.Metadata.Version, value.Chart.Metadata.AppVersion
	}
	return state
}

func cloneValues(values map[string]any) map[string]any {
	result := make(map[string]any, len(values))
	for key, value := range values {
		result[key] = cloneValue(value)
	}
	return result
}

func cloneValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneValues(typed)
	case []any:
		result := make([]any, len(typed))
		for index := range typed {
			result[index] = cloneValue(typed[index])
		}
		return result
	default:
		return value
	}
}

func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
