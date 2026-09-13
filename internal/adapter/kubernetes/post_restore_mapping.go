package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	domainmapping "github.com/smartx/sks-migration-center/internal/domain/mapping"
	"github.com/smartx/sks-migration-center/internal/transform"
)

type PostRestoreMappingResult struct {
	Examined int `json:"examined"`
	Updated  int `json:"updated"`
	Verified int `json:"verified"`
}

func (c *Client) ApplyPostRestoreMappings(ctx context.Context, kubeconfig []byte, namespace string, profile domainmapping.Profile) (PostRestoreMappingResult, error) {
	if namespace == "" {
		return PostRestoreMappingResult{}, errors.New("target namespace is required")
	}
	prepared, err := c.Prepare(kubeconfig)
	if err != nil {
		return PostRestoreMappingResult{}, err
	}
	client, err := dynamic.NewForConfig(prepared.Config)
	if err != nil {
		return PostRestoreMappingResult{}, errors.New("could not initialize dynamic Kubernetes client")
	}
	resources := []schema.GroupVersionResource{
		{Group: "apps", Version: "v1", Resource: "deployments"},
		{Group: "apps", Version: "v1", Resource: "statefulsets"},
		{Group: "apps", Version: "v1", Resource: "daemonsets"},
		{Group: "batch", Version: "v1", Resource: "jobs"},
		{Group: "batch", Version: "v1", Resource: "cronjobs"},
		{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"},
	}
	result := PostRestoreMappingResult{}
	for _, resource := range resources {
		objects, listErr := client.Resource(resource).Namespace(namespace).List(ctx, metav1.ListOptions{})
		if apierrors.IsNotFound(listErr) {
			continue
		}
		if listErr != nil {
			return result, fmt.Errorf("list %s for post-restore mappings: %w", resource.Resource, listErr)
		}
		for index := range objects.Items {
			result.Examined++
			current := &objects.Items[index]
			desired := current.DeepCopy()
			transform.ApplyPostRestoreMappings(desired, profile)
			if reflect.DeepEqual(current.Object, desired.Object) {
				continue
			}
			updated, updateErr := client.Resource(resource).Namespace(namespace).Update(ctx, desired, metav1.UpdateOptions{FieldManager: "sks-migration-center"})
			if updateErr != nil {
				return result, fmt.Errorf("apply post-restore mappings to %s %s: %w", current.GetKind(), current.GetName(), updateErr)
			}
			result.Updated++
			verification := updated.DeepCopy()
			transform.ApplyPostRestoreMappings(verification, profile)
			if !reflect.DeepEqual(updated.Object, verification.Object) {
				return result, fmt.Errorf("post-restore mapping verification failed for %s %s", current.GetKind(), current.GetName())
			}
			result.Verified++
		}
	}
	return result, nil
}
