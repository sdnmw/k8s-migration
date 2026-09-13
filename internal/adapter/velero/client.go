package velero

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	backupStorageLocations = schema.GroupVersionResource{Group: "velero.io", Version: "v1", Resource: "backupstoragelocations"}
	backups                = schema.GroupVersionResource{Group: "velero.io", Version: "v1", Resource: "backups"}
	restores               = schema.GroupVersionResource{Group: "velero.io", Version: "v1", Resource: "restores"}
	podVolumeBackups       = schema.GroupVersionResource{Group: "velero.io", Version: "v1", Resource: "podvolumebackups"}
	dataUploads            = schema.GroupVersionResource{Group: "velero.io", Version: "v2alpha1", Resource: "datauploads"}
	dataDownloads          = schema.GroupVersionResource{Group: "velero.io", Version: "v2alpha1", Resource: "datadownloads"}
	deleteBackupRequests   = schema.GroupVersionResource{Group: "velero.io", Version: "v1", Resource: "deletebackuprequests"}
	dnsNamePattern         = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
)

type dynamicFactory func(*rest.Config) (dynamic.Interface, error)

type Client struct {
	timeout time.Duration
	factory dynamicFactory
}

type BackupStorageLocationSpec struct {
	Namespace        string
	Name             string
	Provider         string
	Bucket           string
	Prefix           string
	Region           string
	Endpoint         string
	CredentialSecret string
	CredentialKey    string
	CABundle         []byte
	AccessMode       string
}

type BackupStorageLocationStatus struct {
	Name               string    `json:"name"`
	Phase              string    `json:"phase"`
	Message            string    `json:"message,omitempty"`
	LastValidationTime time.Time `json:"lastValidationTime,omitempty"`
}

type BackupSpec struct {
	Namespace          string
	Name               string
	StorageLocation    string
	IncludedNamespaces []string
	PlanID             string
	RunID              string
	TTL                time.Duration
	SnapshotMoveData   bool
	DataMover          string
	LabelSelector      map[string]string
}

type BackupStatus struct {
	Name            string    `json:"name"`
	Phase           string    `json:"phase"`
	Message         string    `json:"message,omitempty"`
	Warnings        int64     `json:"warnings"`
	Errors          int64     `json:"errors"`
	ItemsBackedUp   int64     `json:"itemsBackedUp"`
	TotalItems      int64     `json:"totalItems"`
	StartedAt       time.Time `json:"startedAt,omitempty"`
	CompletedAt     time.Time `json:"completedAt,omitempty"`
	ResourceVersion string    `json:"-"`
}

type RestoreSpec struct {
	ExcludedResources  []string
	Namespace          string
	Name               string
	BackupName         string
	IncludedNamespaces []string
	NamespaceMappings  map[string]string
	PlanID             string
	RunID              string
	RestorePVs         bool
}

type RestoreStatus struct {
	Name            string    `json:"name"`
	Phase           string    `json:"phase"`
	Message         string    `json:"message,omitempty"`
	Warnings        int64     `json:"warnings"`
	Errors          int64     `json:"errors"`
	ItemsRestored   int64     `json:"itemsRestored"`
	TotalItems      int64     `json:"totalItems"`
	StartedAt       time.Time `json:"startedAt,omitempty"`
	CompletedAt     time.Time `json:"completedAt,omitempty"`
	ResourceVersion string    `json:"-"`
}

type VolumeTransfer struct {
	Name       string `json:"name"`
	Pod        string `json:"pod"`
	Volume     string `json:"volume"`
	Node       string `json:"node,omitempty"`
	Phase      string `json:"phase"`
	BytesDone  int64  `json:"bytesDone"`
	TotalBytes int64  `json:"totalBytes"`
	Message    string `json:"message,omitempty"`
}

type ArtifactCleanup struct {
	BackupsDeleted  int `json:"backupsDeleted"`
	RestoresDeleted int `json:"restoresDeleted"`
}

func NewClient(timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	return &Client{timeout: timeout, factory: func(config *rest.Config) (dynamic.Interface, error) {
		return dynamic.NewForConfig(config)
	}}
}

func newClient(timeout time.Duration, factory dynamicFactory) *Client {
	return &Client{timeout: timeout, factory: factory}
}

func (c *Client) EnsureBackupStorageLocation(ctx context.Context, kubeconfig []byte, spec BackupStorageLocationSpec) (BackupStorageLocationStatus, error) {
	if err := validateLocation(spec); err != nil {
		return BackupStorageLocationStatus{}, err
	}
	client, err := c.dynamicClient(kubeconfig)
	if err != nil {
		return BackupStorageLocationStatus{}, err
	}
	resource := client.Resource(backupStorageLocations).Namespace(spec.Namespace)
	desired := desiredLocation(spec)
	existing, err := resource.Get(ctx, spec.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		existing, err = resource.Create(ctx, desired, metav1.CreateOptions{})
	} else if err == nil {
		desired.SetResourceVersion(existing.GetResourceVersion())
		desired.SetUID(existing.GetUID())
		existing, err = resource.Update(ctx, desired, metav1.UpdateOptions{})
	}
	if err != nil {
		return BackupStorageLocationStatus{}, fmt.Errorf("ensure Velero BackupStorageLocation: %w", err)
	}
	return locationStatus(existing), nil
}

func (c *Client) BackupStorageLocationStatus(ctx context.Context, kubeconfig []byte, namespace, name string) (BackupStorageLocationStatus, error) {
	if !validName(namespace) || !validName(name) {
		return BackupStorageLocationStatus{}, errors.New("Velero namespace and BackupStorageLocation name are invalid")
	}
	client, err := c.dynamicClient(kubeconfig)
	if err != nil {
		return BackupStorageLocationStatus{}, err
	}
	value, err := client.Resource(backupStorageLocations).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return BackupStorageLocationStatus{}, fmt.Errorf("read Velero BackupStorageLocation: %w", err)
	}
	return locationStatus(value), nil
}

func (c *Client) CreateBackup(ctx context.Context, kubeconfig []byte, spec BackupSpec) (BackupStatus, error) {
	if err := validateBackup(spec); err != nil {
		return BackupStatus{}, err
	}
	client, err := c.dynamicClient(kubeconfig)
	if err != nil {
		return BackupStatus{}, err
	}
	resource := client.Resource(backups).Namespace(spec.Namespace)
	desired := desiredBackup(spec)
	value, err := resource.Create(ctx, desired, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		value, err = resource.Get(ctx, spec.Name, metav1.GetOptions{})
		if err == nil && !sameBackupIdentity(value, spec) {
			return BackupStatus{}, errors.New("Velero Backup name is already used by a different migration run")
		}
	}
	if err != nil {
		return BackupStatus{}, fmt.Errorf("create Velero Backup: %w", err)
	}
	return backupStatus(value), nil
}

func (c *Client) BackupStatus(ctx context.Context, kubeconfig []byte, namespace, name string) (BackupStatus, error) {
	client, err := c.dynamicClient(kubeconfig)
	if err != nil {
		return BackupStatus{}, err
	}
	value, err := client.Resource(backups).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return BackupStatus{}, fmt.Errorf("read Velero Backup: %w", err)
	}
	return backupStatus(value), nil
}

func (c *Client) CreateRestore(ctx context.Context, kubeconfig []byte, spec RestoreSpec) (RestoreStatus, error) {
	if err := validateRestore(spec); err != nil {
		return RestoreStatus{}, err
	}
	client, err := c.dynamicClient(kubeconfig)
	if err != nil {
		return RestoreStatus{}, err
	}
	resource := client.Resource(restores).Namespace(spec.Namespace)
	desired := desiredRestore(spec)
	value, err := resource.Create(ctx, desired, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		value, err = resource.Get(ctx, spec.Name, metav1.GetOptions{})
		if err == nil && !sameRestoreIdentity(value, spec) {
			return RestoreStatus{}, errors.New("Velero Restore name is already used by a different migration run")
		}
	}
	if err != nil {
		return RestoreStatus{}, fmt.Errorf("create Velero Restore: %w", err)
	}
	return restoreStatus(value), nil
}

func (c *Client) RestoreStatus(ctx context.Context, kubeconfig []byte, namespace, name string) (RestoreStatus, error) {
	if !validName(namespace) || !validName(name) {
		return RestoreStatus{}, errors.New("Velero namespace and Restore name are invalid")
	}
	client, err := c.dynamicClient(kubeconfig)
	if err != nil {
		return RestoreStatus{}, err
	}
	value, err := client.Resource(restores).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return RestoreStatus{}, fmt.Errorf("read Velero Restore: %w", err)
	}
	return restoreStatus(value), nil
}

// DeleteRunArtifacts removes only Velero objects carrying the exact migration
// run label. On the source it submits DeleteBackupRequest resources so Velero
// also removes object-store data; synced target Backup CRs are deleted locally.
func (c *Client) DeleteRunArtifacts(ctx context.Context, kubeconfig []byte, namespace, runID string, deleteBackupData bool) (ArtifactCleanup, error) {
	if !validName(namespace) {
		return ArtifactCleanup{}, errors.New("Velero namespace is invalid")
	}
	if _, err := uuid.Parse(runID); err != nil {
		return ArtifactCleanup{}, errors.New("migration run ID is invalid")
	}
	client, err := c.dynamicClient(kubeconfig)
	if err != nil {
		return ArtifactCleanup{}, err
	}
	selector := "migration.smartx.com/run-id=" + runID
	result := ArtifactCleanup{}
	for _, item := range []struct {
		resource schema.GroupVersionResource
		count    *int
		kind     string
	}{{restores, &result.RestoresDeleted, "Restore"}} {
		values, listErr := client.Resource(item.resource).Namespace(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if listErr != nil {
			return result, fmt.Errorf("list Velero %s artifacts: %w", item.kind, listErr)
		}
		for index := range values.Items {
			policy := metav1.DeletePropagationBackground
			if deleteErr := client.Resource(item.resource).Namespace(namespace).Delete(ctx, values.Items[index].GetName(), metav1.DeleteOptions{PropagationPolicy: &policy}); deleteErr != nil && !apierrors.IsNotFound(deleteErr) {
				return result, fmt.Errorf("delete Velero %s %s: %w", item.kind, values.Items[index].GetName(), deleteErr)
			}
			(*item.count)++
		}
	}
	values, err := client.Resource(backups).Namespace(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return result, fmt.Errorf("list Velero Backup artifacts: %w", err)
	}
	for index := range values.Items {
		name := values.Items[index].GetName()
		if deleteBackupData {
			request := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "velero.io/v1", "kind": "DeleteBackupRequest",
				"metadata": map[string]any{"name": deleteRequestName(name), "namespace": namespace, "labels": map[string]any{
					"app.kubernetes.io/managed-by": "sks-migration-center", "migration.smartx.com/run-id": runID,
				}},
				"spec": map[string]any{"backupName": name},
			}}
			_, createErr := client.Resource(deleteBackupRequests).Namespace(namespace).Create(ctx, request, metav1.CreateOptions{})
			if createErr != nil && !apierrors.IsAlreadyExists(createErr) {
				return result, fmt.Errorf("request Velero Backup deletion %s: %w", name, createErr)
			}
		} else {
			policy := metav1.DeletePropagationBackground
			if deleteErr := client.Resource(backups).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{PropagationPolicy: &policy}); deleteErr != nil && !apierrors.IsNotFound(deleteErr) {
				return result, fmt.Errorf("delete synced Velero Backup %s: %w", name, deleteErr)
			}
		}
		result.BackupsDeleted++
	}
	return result, nil
}

func deleteRequestName(backupName string) string {
	digest := sha256.Sum256([]byte(backupName))
	return fmt.Sprintf("smc-delete-%x", digest[:8])
}

func (c *Client) WatchBackup(ctx context.Context, kubeconfig []byte, namespace, name, resourceVersion string) (<-chan BackupStatus, <-chan error, error) {
	if !validName(namespace) || !validName(name) {
		return nil, nil, errors.New("Velero namespace and Backup name are invalid")
	}
	client, err := c.dynamicClient(kubeconfig)
	if err != nil {
		return nil, nil, err
	}
	stream, err := client.Resource(backups).Namespace(namespace).Watch(ctx, metav1.ListOptions{
		FieldSelector: "metadata.name=" + name, ResourceVersion: resourceVersion, AllowWatchBookmarks: true,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("watch Velero Backup: %w", err)
	}
	updates, failures := make(chan BackupStatus, 4), make(chan error, 1)
	go forwardBackupWatch(ctx, stream, name, updates, failures)
	return updates, failures, nil
}

func (c *Client) VolumeTransfers(ctx context.Context, kubeconfig []byte, namespace, backupName string) ([]VolumeTransfer, error) {
	client, err := c.dynamicClient(kubeconfig)
	if err != nil {
		return nil, err
	}
	list, err := client.Resource(podVolumeBackups).Namespace(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "velero.io/backup-name=" + backupName,
	})
	if err != nil {
		return nil, fmt.Errorf("list Velero PodVolumeBackups: %w", err)
	}
	result := make([]VolumeTransfer, 0, len(list.Items))
	for index := range list.Items {
		value := &list.Items[index]
		result = append(result, VolumeTransfer{
			Name: value.GetName(), Pod: nestedString(value.Object, "spec", "pod", "name"), Volume: nestedString(value.Object, "spec", "volume"),
			Node: nestedString(value.Object, "spec", "node"), Phase: nestedString(value.Object, "status", "phase"),
			BytesDone: nestedInt64(value.Object, "status", "progress", "bytesDone"), TotalBytes: nestedInt64(value.Object, "status", "progress", "totalBytes"),
			Message: nestedString(value.Object, "status", "message"),
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func (c *Client) DataUploads(ctx context.Context, kubeconfig []byte, namespace, backupName string) ([]VolumeTransfer, error) {
	return c.dataMoveTransfers(ctx, kubeconfig, namespace, dataUploads, "velero.io/backup-name="+backupName, true)
}

func (c *Client) DataDownloads(ctx context.Context, kubeconfig []byte, namespace, restoreName string) ([]VolumeTransfer, error) {
	return c.dataMoveTransfers(ctx, kubeconfig, namespace, dataDownloads, "velero.io/restore-name="+restoreName, false)
}

func (c *Client) dataMoveTransfers(ctx context.Context, kubeconfig []byte, namespace string, resource schema.GroupVersionResource, labelSelector string, upload bool) ([]VolumeTransfer, error) {
	client, err := c.dynamicClient(kubeconfig)
	if err != nil {
		return nil, err
	}
	list, err := client.Resource(resource).Namespace(namespace).List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
	if err != nil {
		return nil, fmt.Errorf("list Velero data mover transfers: %w", err)
	}
	result := make([]VolumeTransfer, 0, len(list.Items))
	for index := range list.Items {
		value := &list.Items[index]
		transfer := VolumeTransfer{
			Name: value.GetName(), Phase: nestedString(value.Object, "status", "phase"),
			BytesDone: nestedInt64(value.Object, "status", "progress", "bytesDone"), TotalBytes: nestedInt64(value.Object, "status", "progress", "totalBytes"),
			Message: nestedString(value.Object, "status", "message"), Node: nestedString(value.Object, "status", "acceptedByNode"),
		}
		if upload {
			transfer.Pod = nestedString(value.Object, "spec", "sourceNamespace")
			transfer.Volume = nestedString(value.Object, "spec", "sourcePVC")
		} else {
			transfer.Pod = nestedString(value.Object, "spec", "sourceNamespace")
			transfer.Volume = nestedString(value.Object, "spec", "targetVolume", "pvc")
			if transfer.Pod == "" {
				transfer.Pod = nestedString(value.Object, "spec", "targetVolume", "namespace")
			}
		}
		result = append(result, transfer)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func (c *Client) dynamicClient(kubeconfig []byte) (dynamic.Interface, error) {
	if len(kubeconfig) == 0 || len(kubeconfig) > 1<<20 {
		return nil, errors.New("kubeconfig must contain between 1 byte and 1 MiB")
	}
	config, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return nil, errors.New("kubeconfig current context is incomplete")
	}
	config.Timeout, config.UserAgent, config.QPS, config.Burst = c.timeout, "sks-migration-center/0.1", 20, 30
	value, err := c.factory(config)
	if err != nil {
		return nil, errors.New("could not initialize Velero CR client")
	}
	return value, nil
}

func validateLocation(spec BackupStorageLocationSpec) error {
	endpoint, err := url.Parse(spec.Endpoint)
	if !validName(spec.Namespace) || !validName(spec.Name) || spec.Provider != "aws" || strings.TrimSpace(spec.Bucket) == "" ||
		strings.TrimSpace(spec.Region) == "" || !validName(spec.CredentialSecret) || strings.TrimSpace(spec.CredentialKey) == "" {
		return errors.New("Velero BackupStorageLocation fields are invalid")
	}
	if spec.AccessMode != "" && spec.AccessMode != "ReadWrite" && spec.AccessMode != "ReadOnly" {
		return errors.New("Velero BackupStorageLocation access mode is invalid")
	}
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return errors.New("Velero S3 endpoint must be a verified HTTPS origin")
	}
	return nil
}

func validateBackup(spec BackupSpec) error {
	if !validName(spec.Namespace) || !validName(spec.Name) || !validName(spec.StorageLocation) || len(spec.IncludedNamespaces) == 0 || spec.PlanID == "" || spec.RunID == "" {
		return errors.New("Velero Backup namespace, name, storage location, migration identity and included namespaces are required")
	}
	for _, namespace := range spec.IncludedNamespaces {
		if !validName(namespace) {
			return errors.New("Velero Backup contains an invalid namespace")
		}
	}
	return nil
}

func validateRestore(spec RestoreSpec) error {
	if !validName(spec.Namespace) || !validName(spec.Name) || !validName(spec.BackupName) || len(spec.IncludedNamespaces) == 0 || spec.PlanID == "" || spec.RunID == "" {
		return errors.New("Velero Restore namespace, name, backup, migration identity and included namespaces are required")
	}
	for _, namespace := range spec.IncludedNamespaces {
		if !validName(namespace) {
			return errors.New("Velero Restore contains an invalid included namespace")
		}
	}
	for source, target := range spec.NamespaceMappings {
		if !validName(source) || !validName(target) {
			return errors.New("Velero Restore contains an invalid namespace mapping")
		}
	}
	return nil
}

func validName(value string) bool {
	return len(value) > 0 && len(value) <= 63 && dnsNamePattern.MatchString(value)
}

func desiredLocation(spec BackupStorageLocationSpec) *unstructured.Unstructured {
	objectStorage := map[string]any{"bucket": spec.Bucket}
	if spec.Prefix != "" {
		objectStorage["prefix"] = spec.Prefix
	}
	if len(spec.CABundle) > 0 {
		objectStorage["caCert"] = base64.StdEncoding.EncodeToString(spec.CABundle)
	}
	accessMode := spec.AccessMode
	if accessMode == "" {
		accessMode = "ReadWrite"
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "velero.io/v1", "kind": "BackupStorageLocation",
		"metadata": map[string]any{"name": spec.Name, "namespace": spec.Namespace, "labels": map[string]any{"app.kubernetes.io/managed-by": "sks-migration-center"}},
		"spec": map[string]any{
			"provider": spec.Provider, "objectStorage": objectStorage, "accessMode": accessMode,
			"credential": map[string]any{"name": spec.CredentialSecret, "key": spec.CredentialKey},
			"config":     map[string]any{"region": spec.Region, "s3Url": spec.Endpoint, "s3ForcePathStyle": "true"},
		},
	}}
}

func desiredBackup(spec BackupSpec) *unstructured.Unstructured {
	ttl := spec.TTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	namespaces := make([]any, 0, len(spec.IncludedNamespaces))
	for _, value := range spec.IncludedNamespaces {
		namespaces = append(namespaces, value)
	}
	backupSpec := map[string]any{
		"includedNamespaces": namespaces, "storageLocation": spec.StorageLocation, "ttl": ttl.String(),
	}
	if len(spec.LabelSelector) > 0 {
		matchLabels := make(map[string]any, len(spec.LabelSelector))
		for key, value := range spec.LabelSelector {
			matchLabels[key] = value
		}
		backupSpec["labelSelector"] = map[string]any{"matchLabels": matchLabels}
	}
	if spec.SnapshotMoveData {
		dataMover := strings.TrimSpace(spec.DataMover)
		if dataMover == "" {
			dataMover = "velero"
		}
		backupSpec["snapshotVolumes"] = true
		backupSpec["snapshotMoveData"] = true
		backupSpec["defaultVolumesToFsBackup"] = false
		backupSpec["datamover"] = dataMover
	} else {
		backupSpec["snapshotVolumes"] = false
		backupSpec["defaultVolumesToFsBackup"] = true
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "velero.io/v1", "kind": "Backup",
		"metadata": map[string]any{"name": spec.Name, "namespace": spec.Namespace, "labels": map[string]any{
			"app.kubernetes.io/managed-by": "sks-migration-center", "migration.smartx.com/plan-id": spec.PlanID, "migration.smartx.com/run-id": spec.RunID,
		}},
		"spec": backupSpec,
	}}
}

func desiredRestore(spec RestoreSpec) *unstructured.Unstructured {
	excluded := make([]any, 0, len(spec.ExcludedResources))
	for _, resource := range spec.ExcludedResources {
		excluded = append(excluded, resource)
	}
	namespaces := make([]any, 0, len(spec.IncludedNamespaces))
	for _, value := range spec.IncludedNamespaces {
		namespaces = append(namespaces, value)
	}
	mappings := make(map[string]any, len(spec.NamespaceMappings))
	for source, target := range spec.NamespaceMappings {
		mappings[source] = target
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "velero.io/v1", "kind": "Restore",
		"metadata": map[string]any{"name": spec.Name, "namespace": spec.Namespace, "labels": map[string]any{
			"app.kubernetes.io/managed-by": "sks-migration-center", "migration.smartx.com/plan-id": spec.PlanID, "migration.smartx.com/run-id": spec.RunID,
		}},
		"spec": map[string]any{
			"backupName": spec.BackupName, "includedNamespaces": namespaces, "namespaceMapping": mappings,
			"excludedResources": excluded,
			"restorePVs":        spec.RestorePVs, "existingResourcePolicy": "update",
		},
	}}
}

func sameBackupIdentity(value *unstructured.Unstructured, spec BackupSpec) bool {
	labels := value.GetLabels()
	return labels["migration.smartx.com/plan-id"] == spec.PlanID && labels["migration.smartx.com/run-id"] == spec.RunID
}

func sameRestoreIdentity(value *unstructured.Unstructured, spec RestoreSpec) bool {
	labels := value.GetLabels()
	return labels["migration.smartx.com/plan-id"] == spec.PlanID && labels["migration.smartx.com/run-id"] == spec.RunID
}

func locationStatus(value *unstructured.Unstructured) BackupStorageLocationStatus {
	status := BackupStorageLocationStatus{Name: value.GetName(), Phase: nestedString(value.Object, "status", "phase"), Message: nestedString(value.Object, "status", "message")}
	status.LastValidationTime = nestedTime(value.Object, "status", "lastValidationTime")
	return status
}

func backupStatus(value *unstructured.Unstructured) BackupStatus {
	return BackupStatus{
		Name: value.GetName(), Phase: nestedString(value.Object, "status", "phase"), Message: nestedString(value.Object, "status", "failureReason"),
		Warnings: nestedInt64(value.Object, "status", "warnings"), Errors: nestedInt64(value.Object, "status", "errors"),
		ItemsBackedUp: nestedInt64(value.Object, "status", "progress", "itemsBackedUp"), TotalItems: nestedInt64(value.Object, "status", "progress", "totalItems"),
		StartedAt: nestedTime(value.Object, "status", "startTimestamp"), CompletedAt: nestedTime(value.Object, "status", "completionTimestamp"), ResourceVersion: value.GetResourceVersion(),
	}
}

func restoreStatus(value *unstructured.Unstructured) RestoreStatus {
	return RestoreStatus{
		Name: value.GetName(), Phase: nestedString(value.Object, "status", "phase"), Message: nestedString(value.Object, "status", "failureReason"),
		Warnings: nestedInt64(value.Object, "status", "warnings"), Errors: nestedInt64(value.Object, "status", "errors"),
		ItemsRestored: nestedInt64(value.Object, "status", "progress", "itemsRestored"), TotalItems: nestedInt64(value.Object, "status", "progress", "totalItems"),
		StartedAt: nestedTime(value.Object, "status", "startTimestamp"), CompletedAt: nestedTime(value.Object, "status", "completionTimestamp"), ResourceVersion: value.GetResourceVersion(),
	}
}

func forwardBackupWatch(ctx context.Context, stream watch.Interface, name string, updates chan<- BackupStatus, failures chan<- error) {
	defer close(updates)
	defer close(failures)
	defer stream.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-stream.ResultChan():
			if !ok {
				failures <- errors.New("Velero Backup watch closed")
				return
			}
			if event.Type == watch.Bookmark {
				continue
			}
			if event.Type == watch.Error {
				failures <- apierrors.FromObject(event.Object)
				return
			}
			value, err := runtime.DefaultUnstructuredConverter.ToUnstructured(event.Object)
			if err != nil {
				failures <- errors.New("decode Velero Backup watch event")
				return
			}
			object := &unstructured.Unstructured{Object: value}
			if object.GetName() == name {
				updates <- backupStatus(object)
			}
		}
	}
}

func nestedString(value map[string]any, fields ...string) string {
	result, _, _ := unstructured.NestedString(value, fields...)
	return result
}

func nestedInt64(value map[string]any, fields ...string) int64 {
	if result, ok, _ := unstructured.NestedInt64(value, fields...); ok {
		return result
	}
	if result, ok, _ := unstructured.NestedFloat64(value, fields...); ok {
		return int64(result)
	}
	return 0
}

func nestedTime(value map[string]any, fields ...string) time.Time {
	text := nestedString(value, fields...)
	result, _ := time.Parse(time.RFC3339, text)
	return result
}
