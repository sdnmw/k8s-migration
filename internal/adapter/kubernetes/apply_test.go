package kubernetes

import (
	"strings"
	"testing"
)

func TestDecodeApplyObjectsRejectsClusterScopedResources(t *testing.T) {
	_, err := decodeApplyObjects([]byte("apiVersion: v1\nkind: Namespace\nmetadata:\n  name: unsafe\n"))
	if err == nil || !strings.Contains(err.Error(), "outside Compose migration scope") {
		t.Fatalf("expected cluster-scope rejection, got %v", err)
	}
}

func TestDecodeApplyObjectsAcceptsNamespacedResources(t *testing.T) {
	objects, err := decodeApplyObjects([]byte("apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: api\nspec: {}\n---\napiVersion: v1\nkind: Service\nmetadata:\n  name: api\nspec: {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(objects) != 2 || objects[0].GetName() != "api" {
		t.Fatalf("unexpected objects: %#v", objects)
	}
}
