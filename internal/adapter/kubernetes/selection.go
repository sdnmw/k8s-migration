package kubernetes

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/restmapper"

	domainapplication "github.com/smartx/sks-migration-center/internal/domain/application"
)

// LabelResources marks the exact objects selected by the administrator. Velero
// then uses the label selector so other objects of the same kind and namespace
// are not silently included in a manual migration.
func (c *Client) LabelResources(ctx context.Context, kubeconfig []byte, resources []domainapplication.ResourceReference, key, value string) error {
	prepared, err := c.Prepare(kubeconfig)
	if err != nil {
		return err
	}
	dynamicClient, err := dynamic.NewForConfig(prepared.Config)
	if err != nil {
		return err
	}
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(prepared.Config)
	if err != nil {
		return err
	}
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(discoveryClient))
	for _, reference := range resources {
		groupVersion, parseErr := schema.ParseGroupVersion(reference.APIVersion)
		if parseErr != nil {
			return fmt.Errorf("parse %s apiVersion: %w", reference.Kind, parseErr)
		}
		mapping, mapErr := mapper.RESTMapping(schema.GroupKind{Group: groupVersion.Group, Kind: reference.Kind}, groupVersion.Version)
		if mapErr != nil {
			return fmt.Errorf("map %s %s: %w", reference.Kind, reference.Name, mapErr)
		}
		resource := dynamicClient.Resource(mapping.Resource)
		var objectResource dynamic.ResourceInterface = resource
		if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
			objectResource = resource.Namespace(reference.Namespace)
		}
		object, getErr := objectResource.Get(ctx, reference.Name, metav1.GetOptions{})
		if getErr != nil {
			return fmt.Errorf("read selected %s %s: %w", reference.Kind, reference.Name, getErr)
		}
		labels := object.GetLabels()
		if labels == nil {
			labels = map[string]string{}
		}
		labels[key] = value
		object.SetLabels(labels)
		if _, updateErr := objectResource.Update(ctx, object, metav1.UpdateOptions{}); updateErr != nil {
			return fmt.Errorf("label selected %s %s: %w", reference.Kind, reference.Name, updateErr)
		}
	}
	return nil
}
