package assessment

import (
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/domain/application"
	domainassessment "github.com/smartx/sks-migration-center/internal/domain/assessment"
	"github.com/smartx/sks-migration-center/internal/domain/environment"
)

func TestDefaultAssessmentIsStableAndFindsComputeAPIAndDependencies(t *testing.T) {
	one := int64(1)
	app := application.SourceApplication{ID: uuid.New(), Inventory: application.Inventory{
		Resources: []application.ResourceSummary{
			{APIVersion: "extensions/v1beta1", Kind: "Ingress", Namespace: "shop", Name: "web"},
			{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "shop", Name: "api", Replicas: &one},
		},
		Workloads: []application.ResourceSummary{{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "shop", Name: "api", Replicas: &one}},
		Dependencies: []application.Dependency{
			{From: application.ResourceReference{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "shop", Name: "api"}, To: application.ResourceReference{APIVersion: "v1", Kind: "Secret", Namespace: "shop", Name: "database"}, Type: "READS_ENV_FROM", Required: true},
			{From: application.ResourceReference{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "shop", Name: "api"}, To: application.ResourceReference{APIVersion: "v1", Kind: "ConfigMap", Namespace: "shop", Name: "settings"}, Type: "READS_ENV_FROM", Required: true},
		},
	}}
	ctx := Context{Application: app, Target: environment.Capabilities{APIGroups: []string{"apps", "v1"}}}
	first := DefaultEngine().Assess(ctx)
	second := DefaultEngine().Assess(ctx)
	if first.Score != 0 || first.BlockerCount != 3 || first.WarningCount != 1 || first.InfoCount != 1 {
		t.Fatalf("unexpected assessment: %+v", first)
	}
	keys := func(value domainassessment.Assessment) []string {
		result := []string{}
		for _, issue := range value.Issues {
			result = append(result, string(issue.Severity)+":"+issue.RuleID+":"+issue.ResourceName)
		}
		return result
	}
	if !reflect.DeepEqual(keys(first), keys(second)) {
		t.Fatalf("assessment ordering is unstable: %v vs %v", keys(first), keys(second))
	}
	for _, issue := range first.Issues {
		if issue.RuleID == "DEPENDENCY_REQUIRED_MISSING" && (!strings.Contains(issue.Description, "ConfigMap/settings") || !strings.Contains(issue.Description, "Secret/database")) {
			t.Fatalf("missing dependencies were not aggregated: %+v", issue)
		}
	}
}

func TestEngineRejectsDuplicateRuleIDs(t *testing.T) {
	if _, err := NewEngine(ComputeRequestsRule{}, ComputeRequestsRule{}); err == nil {
		t.Fatal("duplicate rule ID must be rejected")
	}
}
