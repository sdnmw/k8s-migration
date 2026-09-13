package contract

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOpenAPIContractContainsRequiredOperations(t *testing.T) {
	contents, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatalf("read OpenAPI contract: %v", err)
	}
	var document struct {
		OpenAPI string                    `yaml:"openapi"`
		Paths   map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(contents, &document); err != nil {
		t.Fatalf("parse OpenAPI contract: %v", err)
	}
	if document.OpenAPI != "3.1.0" {
		t.Fatalf("expected OpenAPI 3.1.0, got %q", document.OpenAPI)
	}
	required := map[string]string{
		"/auth/login":   "post",
		"/environments": "post",
		"/environments/{environmentId}/capabilities": "get",
		"/applications/discover":                     "post",
		"/applications/discover-all":                 "post",
		"/applications/discover-compose":             "post",
		"/applications/preview":                      "post",
		"/credentials":                               "post",
		"/applications/{applicationId}":              "get",
		"/storage-profiles/{profileId}/install":      "post",
		"/migration-plans/{planId}/preflight":        "post",
		"/migration-runs/{runId}/events":             "get",
		"/migration-runs/{runId}/cutover":            "post",
		"/migration-runs/{runId}/rollback":           "post",
		"/migration-runs/{runId}/restore-source":     "post",
	}
	for path, method := range required {
		operations, ok := document.Paths[path]
		if !ok {
			t.Errorf("required path %s is missing", path)
			continue
		}
		if _, ok := operations[method]; !ok {
			t.Errorf("required operation %s %s is missing", method, path)
		}
	}
}
