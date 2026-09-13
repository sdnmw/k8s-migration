package kubernetes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/smartx/sks-migration-center/internal/domain/application"
)

func TestBuildInventoryNormalizesResourcesAndBuildsDependencies(t *testing.T) {
	objects := []unstructured.Unstructured{
		objectFromJSON(t, `{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"api","namespace":"shop","labels":{"app":"api"},"uid":"runtime-only"},"spec":{"replicas":2,"template":{"metadata":{"labels":{"app":"api"}},"spec":{"serviceAccountName":"api","imagePullSecrets":[{"name":"registry"}],"containers":[{"name":"api","image":"registry.example/api:v1","envFrom":[{"secretRef":{"name":"database"}}],"env":[{"name":"MODE","valueFrom":{"configMapKeyRef":{"name":"settings","key":"mode"}}}],"volumeMounts":[{"name":"data","mountPath":"/data"}]}],"volumes":[{"name":"data","persistentVolumeClaim":{"claimName":"api-data"}},{"name":"config","configMap":{"name":"settings"}}]}}}}`),
		objectFromJSON(t, `{"apiVersion":"v1","kind":"Service","metadata":{"name":"api","namespace":"shop"},"spec":{"selector":{"app":"api"},"ports":[{"port":80,"targetPort":8080}]}}`),
		objectFromJSON(t, `{"apiVersion":"networking.k8s.io/v1","kind":"Ingress","metadata":{"name":"api","namespace":"shop"},"spec":{"rules":[{"http":{"paths":[{"path":"/","backend":{"service":{"name":"api","port":{"number":80}}}}]}}]}}`),
		objectFromJSON(t, `{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"settings","namespace":"shop"},"data":{"mode":"production","password":"must-not-leak"}}`),
		objectFromJSON(t, `{"apiVersion":"v1","kind":"Secret","metadata":{"name":"database","namespace":"shop"},"data":{"password":"c2VjcmV0","username":"YXBp"}}`),
		objectFromJSON(t, `{"apiVersion":"v1","kind":"Secret","metadata":{"name":"registry","namespace":"shop"},"data":{".dockerconfigjson":"must-not-leak"}}`),
		objectFromJSON(t, `{"apiVersion":"v1","kind":"ServiceAccount","metadata":{"name":"api","namespace":"shop"}}`),
		objectFromJSON(t, `{"apiVersion":"v1","kind":"PersistentVolumeClaim","metadata":{"name":"api-data","namespace":"shop"},"spec":{"accessModes":["ReadWriteOnce"],"volumeMode":"Filesystem","storageClassName":"smtx-block","resources":{"requests":{"storage":"1Gi"}},"volumeName":"runtime-pv"},"status":{"phase":"Bound"}}`),
		objectFromJSON(t, `{"apiVersion":"rbac.authorization.k8s.io/v1","kind":"Role","metadata":{"name":"reader","namespace":"shop"},"rules":[]}`),
		objectFromJSON(t, `{"apiVersion":"rbac.authorization.k8s.io/v1","kind":"RoleBinding","metadata":{"name":"reader","namespace":"shop"},"roleRef":{"apiGroup":"rbac.authorization.k8s.io","kind":"Role","name":"reader"},"subjects":[{"kind":"ServiceAccount","name":"api"}]}`),
		objectFromJSON(t, `{"apiVersion":"cache.example.io/v1","kind":"Redis","metadata":{"name":"cache","namespace":"shop","ownerReferences":[{"apiVersion":"apps/v1","kind":"Deployment","name":"api","uid":"ignored"}]},"spec":{"password":"must-not-leak"},"status":{"ready":true}}`),
	}
	crds := []application.ResourceSummary{{APIVersion: "apiextensions.k8s.io/v1", Kind: "CustomResourceDefinition", Name: "redis.cache.example.io"}}
	inventory := buildInventory(objects, crds, nil)

	if len(inventory.Resources) != len(objects) || len(inventory.Workloads) != 1 || len(inventory.CustomResources) != 1 || len(inventory.CRDs) != 1 {
		t.Fatalf("unexpected inventory counts: %+v", inventory.Counts)
	}
	if len(inventory.Images) != 1 || inventory.Images[0].Reference != "registry.example/api:v1" {
		t.Fatalf("unexpected images: %+v", inventory.Images)
	}
	if len(inventory.PVCs) != 1 || inventory.PVCs[0].CapacityBytes != 1073741824 || inventory.PVCs[0].StorageClassName != "smtx-block" {
		t.Fatalf("unexpected PVC: %+v", inventory.PVCs)
	}
	if strings.Join(inventory.Secrets[0].SecretKeys, ",") != "password,username" || strings.Join(inventory.ConfigMaps[0].DataKeys, ",") != "mode,password" {
		t.Fatalf("sensitive key normalization failed: secrets=%+v configmaps=%+v", inventory.Secrets, inventory.ConfigMaps)
	}
	for _, expected := range []string{"USES_SERVICE_ACCOUNT:api", "USES_IMAGE_PULL_SECRET:registry", "MOUNTS:api-data", "READS_ENV_FROM:database", "READS_ENV_KEY:settings", "SELECTS:api", "ROUTES_TO:api", "BINDS_ROLE:reader", "GRANTS_TO:api", "USES_STORAGE_CLASS:smtx-block", "OWNED_BY:api"} {
		if !hasDependency(inventory, expected) {
			t.Fatalf("missing dependency %s: %+v", expected, inventory.Dependencies)
		}
	}
	encoded, _ := json.Marshal(inventory)
	for _, secret := range []string{"must-not-leak", "c2VjcmV0", "runtime-pv"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("runtime or sensitive value leaked: %s", secret)
		}
	}
}

func TestDiscoverNamespaceRejectsInvalidNameBeforeConnecting(t *testing.T) {
	_, err := NewClient(0).DiscoverNamespace(context.Background(), nil, "../../etc")
	if err == nil || !strings.Contains(err.Error(), "DNS label") {
		t.Fatalf("expected namespace validation error, got %v", err)
	}
}

func TestPodResourceTotalsUseSchedulerSemantics(t *testing.T) {
	object := objectFromJSON(t, `{
		"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"api","namespace":"shop"},
		"spec":{"template":{"spec":{
			"overhead":{"cpu":"50m","memory":"16Mi"},
			"initContainers":[
				{"name":"first","resources":{"requests":{"cpu":"900m","memory":"128Mi"}}},
				{"name":"second","resources":{"requests":{"cpu":"500m","memory":"512Mi"}}}
			],
			"containers":[
				{"name":"api","resources":{"requests":{"cpu":"400m","memory":"256Mi"}}},
				{"name":"sidecar","resources":{"requests":{"cpu":"300m"}}}
			]
		}}}
	}`)
	requests, _ := podResourceTotals(object)
	if requests["cpu"] != "950m" || requests["memory"] != "528Mi" {
		t.Fatalf("effective requests=%v; want cpu=950m memory=528Mi", requests)
	}
	summary := resourceSummary(object)
	if strings.Join(summary.MissingRequests, ",") != "memory" {
		t.Fatalf("missing requests=%v; want memory", summary.MissingRequests)
	}
}

func TestResourceSummaryCapturesOnlySecurityAndNetworkFacts(t *testing.T) {
	workload := objectFromJSON(t, `{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"api"},"spec":{"template":{"spec":{"hostNetwork":true,"hostPID":true,"volumes":[{"name":"data","hostPath":{"path":"/srv/data"}}],"containers":[{"name":"api","securityContext":{"privileged":true},"ports":[{"containerPort":8080,"hostPort":8080}]}]}}}}`)
	summary := resourceSummary(workload)
	if strings.Join(summary.SecurityRisks, ",") != "HOST_NETWORK,HOST_PATH,HOST_PID,HOST_PORT,PRIVILEGED" {
		t.Fatalf("security risks=%v", summary.SecurityRisks)
	}
	service := resourceSummary(objectFromJSON(t, `{"apiVersion":"v1","kind":"Service","metadata":{"name":"api"},"spec":{"type":"LoadBalancer"}}`))
	ingress := resourceSummary(objectFromJSON(t, `{"apiVersion":"networking.k8s.io/v1","kind":"Ingress","metadata":{"name":"api"},"spec":{"ingressClassName":"traefik"}}`))
	if service.ServiceType != "LoadBalancer" || ingress.IngressClassName != "traefik" {
		t.Fatalf("network facts service=%+v ingress=%+v", service, ingress)
	}
}

func TestDiscoverNamespaceAgainstTLSTestClusterIncludesCRDInstances(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/namespaces/shop":
			_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"shop"}}`))
		case "/apis/apps/v1/namespaces/shop/deployments":
			_, _ = w.Write([]byte(`{"apiVersion":"apps/v1","kind":"DeploymentList","items":[{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"api","namespace":"shop"},"spec":{"template":{"metadata":{"labels":{"app":"api"}},"spec":{"containers":[{"name":"api","image":"example/api:v1"}]}}}}]}`))
		case "/api/v1/namespaces/shop/secrets":
			_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"SecretList","items":[{"apiVersion":"v1","kind":"Secret","metadata":{"name":"database","namespace":"shop"},"data":{"password":"must-not-leak"}}]}`))
		case "/apis/apiextensions.k8s.io/v1/customresourcedefinitions":
			_, _ = w.Write([]byte(`{"apiVersion":"apiextensions.k8s.io/v1","kind":"CustomResourceDefinitionList","items":[{"apiVersion":"apiextensions.k8s.io/v1","kind":"CustomResourceDefinition","metadata":{"name":"redis.cache.example.io"},"spec":{"group":"cache.example.io","scope":"Namespaced","names":{"plural":"redis","kind":"Redis"},"versions":[{"name":"v1","served":true,"storage":true}]}}]}`))
		case "/apis/cache.example.io/v1/namespaces/shop/redis":
			_, _ = w.Write([]byte(`{"apiVersion":"cache.example.io/v1","kind":"RedisList","items":[{"apiVersion":"cache.example.io/v1","kind":"Redis","metadata":{"name":"cache","namespace":"shop"},"spec":{"password":"must-not-leak"}}]}`))
		default:
			_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"List","items":[]}`))
		}
	}))
	defer server.Close()

	inventory, err := NewClient(0).DiscoverNamespace(context.Background(), []byte(kubeconfigFor(server.URL, "    token: test-token\n")), "shop")
	if err != nil {
		t.Fatalf("discover namespace: %v", err)
	}
	if len(inventory.Workloads) != 1 || len(inventory.Secrets) != 1 || len(inventory.CRDs) != 1 || len(inventory.CustomResources) != 1 {
		t.Fatalf("unexpected inventory: counts=%+v crds=%+v custom=%+v", inventory.Counts, inventory.CRDs, inventory.CustomResources)
	}
	encoded, _ := json.Marshal(inventory)
	if strings.Contains(string(encoded), "must-not-leak") {
		t.Fatalf("Kubernetes content value leaked: %s", encoded)
	}
	coreInventory, err := NewClient(0).DiscoverNamespaceCore(context.Background(), []byte(kubeconfigFor(server.URL, "    token: test-token\n")), "shop")
	if err != nil {
		t.Fatalf("discover core namespace inventory: %v", err)
	}
	if len(coreInventory.Workloads) != 1 || len(coreInventory.Secrets) != 1 {
		t.Fatalf("core inventory omitted built-in resources: counts=%+v", coreInventory.Counts)
	}
	if len(coreInventory.CRDs) != 0 || len(coreInventory.CustomResources) != 0 {
		t.Fatalf("core inventory should defer custom resources: crds=%+v custom=%+v", coreInventory.CRDs, coreInventory.CustomResources)
	}
}

func TestServedCRDVersionPrefersStorageVersion(t *testing.T) {
	object := objectFromJSON(t, `{"spec":{"versions":[{"name":"v1beta1","served":true,"storage":false},{"name":"v1","served":true,"storage":true}]}}`)
	if got := servedCRDVersion(object.Object); got != "v1" {
		t.Fatalf("served version=%q", got)
	}
}

func objectFromJSON(t *testing.T, value string) unstructured.Unstructured {
	t.Helper()
	var object map[string]any
	if err := json.Unmarshal([]byte(value), &object); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return unstructured.Unstructured{Object: object}
}

func hasDependency(inventory application.Inventory, expected string) bool {
	for _, dependency := range inventory.Dependencies {
		if dependency.Type+":"+dependency.To.Name == expected {
			return true
		}
	}
	return false
}
