package kubernetes

import (
	"context"
	"errors"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	kubernetesclient "k8s.io/client-go/kubernetes"

	domainenvironment "github.com/smartx/sks-migration-center/internal/domain/environment"
)

var (
	dataUploads        = schema.GroupVersionResource{Group: "velero.io", Version: "v2alpha1", Resource: "datauploads"}
	dataDownloads      = schema.GroupVersionResource{Group: "velero.io", Version: "v2alpha1", Resource: "datadownloads"}
	backupRepositories = schema.GroupVersionResource{Group: "velero.io", Version: "v1", Resource: "backuprepositories"}
)

func (c *Client) CSIDataMoverCapabilities(ctx context.Context, kubeconfig []byte, namespace string) (domainenvironment.CSIDataMoverCapabilities, error) {
	prepared, err := c.Prepare(kubeconfig)
	if err != nil {
		return domainenvironment.CSIDataMoverCapabilities{}, err
	}
	clientset, err := kubernetesclient.NewForConfig(prepared.Config)
	if err != nil {
		return domainenvironment.CSIDataMoverCapabilities{}, errors.New("could not initialize Kubernetes data mover client")
	}
	dynamicClient, err := dynamic.NewForConfig(prepared.Config)
	if err != nil {
		return domainenvironment.CSIDataMoverCapabilities{}, errors.New("could not initialize Kubernetes data mover CR client")
	}
	snapshotAPI, err := resourceAPIAvailable(ctx, dynamicClient, schema.GroupVersionResource{
		Group: "snapshot.storage.k8s.io", Version: "v1", Resource: "volumesnapshotclasses",
	}, "")
	if err != nil {
		return domainenvironment.CSIDataMoverCapabilities{}, err
	}
	return discoverCSIDataMover(ctx, clientset, dynamicClient, namespace, snapshotAPI)
}

func discoverCSIDataMover(ctx context.Context, clientset kubernetesclient.Interface, dynamicClient dynamic.Interface, namespace string, snapshotAPI bool) (domainenvironment.CSIDataMoverCapabilities, error) {
	result := domainenvironment.CSIDataMoverCapabilities{SnapshotAPI: snapshotAPI}
	var err error
	if result.DataUploadAPI, err = resourceAPIAvailable(ctx, dynamicClient, dataUploads, namespace); err != nil {
		return result, err
	}
	if result.DataDownloadAPI, err = resourceAPIAvailable(ctx, dynamicClient, dataDownloads, namespace); err != nil {
		return result, err
	}
	if result.BackupRepositoryAPI, err = resourceAPIAvailable(ctx, dynamicClient, backupRepositories, namespace); err != nil {
		return result, err
	}
	deployment, err := clientset.AppsV1().Deployments(namespace).Get(ctx, "velero", metav1.GetOptions{})
	if err == nil {
		result.EnableCSI = deploymentEnablesCSI(deployment)
	} else if !apierrors.IsNotFound(err) {
		return result, err
	}
	nodeAgent, err := clientset.AppsV1().DaemonSets(namespace).Get(ctx, "node-agent", metav1.GetOptions{})
	if err == nil {
		result.NodeAgentDesired = nodeAgent.Status.DesiredNumberScheduled
		result.NodeAgentReady = nodeAgent.Status.NumberReady
	} else if !apierrors.IsNotFound(err) {
		return result, err
	}
	nodeAgentReady := result.NodeAgentDesired > 0 && result.NodeAgentReady == result.NodeAgentDesired
	result.BackupReady = result.SnapshotAPI && result.DataUploadAPI && result.BackupRepositoryAPI && result.EnableCSI && nodeAgentReady
	result.RestoreReady = result.DataDownloadAPI && result.BackupRepositoryAPI && result.EnableCSI && nodeAgentReady
	return result, nil
}

func resourceAPIAvailable(ctx context.Context, client dynamic.Interface, resource schema.GroupVersionResource, namespace string) (bool, error) {
	selector := client.Resource(resource)
	if namespace != "" {
		_, err := selector.Namespace(namespace).List(ctx, metav1.ListOptions{Limit: 1})
		if err == nil {
			return true, nil
		}
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	_, err := selector.List(ctx, metav1.ListOptions{Limit: 1})
	if err == nil {
		return true, nil
	}
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	return false, err
}

func deploymentEnablesCSI(value *appsv1.Deployment) bool {
	for _, container := range value.Spec.Template.Spec.Containers {
		for index, argument := range container.Args {
			features := ""
			if strings.HasPrefix(argument, "--features=") {
				features = strings.TrimPrefix(argument, "--features=")
			} else if argument == "--features" && index+1 < len(container.Args) {
				features = container.Args[index+1]
			}
			for _, feature := range strings.Split(features, ",") {
				if strings.EqualFold(strings.TrimSpace(feature), "EnableCSI") {
					return true
				}
			}
		}
	}
	return false
}
