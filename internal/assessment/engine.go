package assessment

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/domain/application"
	domainassessment "github.com/smartx/sks-migration-center/internal/domain/assessment"
	"github.com/smartx/sks-migration-center/internal/domain/environment"
)

type Context struct {
	Application application.SourceApplication
	Source      environment.Capabilities
	Target      environment.Capabilities
}

type Finding struct {
	Severity    domainassessment.Severity
	Category    domainassessment.Category
	Resource    application.ResourceReference
	RuleID      string
	Title       string
	Description string
	Remediation string
	AutoFixable bool
}

type Rule interface {
	ID() string
	Category() domainassessment.Category
	Evaluate(Context) []Finding
}

type Engine struct {
	rules map[string]Rule
	clock func() time.Time
}

func NewEngine(rules ...Rule) (*Engine, error) {
	engine := &Engine{rules: map[string]Rule{}, clock: func() time.Time { return time.Now().UTC() }}
	for _, rule := range rules {
		if rule == nil || strings.TrimSpace(rule.ID()) == "" {
			return nil, errors.New("assessment rule ID is required")
		}
		if _, exists := engine.rules[rule.ID()]; exists {
			return nil, fmt.Errorf("duplicate assessment rule %s", rule.ID())
		}
		engine.rules[rule.ID()] = rule
	}
	return engine, nil
}

func DefaultEngine() *Engine {
	engine, _ := NewEngine(
		ComputeRequestsRule{}, ReplicaAvailabilityRule{}, DeprecatedAPIRule{}, TargetAPIAvailabilityRule{}, RequiredDependencyRule{},
		RawBlockVolumeRule{}, RWXStorageRule{}, IngressClassRule{}, ExposedServiceRule{}, PodSecurityRule{}, ImageReferenceRule{}, ImageArchitectureRule{},
		ComposeUnresolvedVariablesRule{},
	)
	return engine
}

func (e *Engine) Assess(context Context) domainassessment.Assessment {
	now := e.clock()
	result := domainassessment.Assessment{ID: uuid.New(), ApplicationID: context.Application.ID, Score: 100, Status: domainassessment.StatusCompleted, Issues: []domainassessment.Issue{}, CreatedAt: now, CompletedAt: &now}
	ruleIDs := make([]string, 0, len(e.rules))
	for id := range e.rules {
		ruleIDs = append(ruleIDs, id)
	}
	sort.Strings(ruleIDs)
	findings := make([]Finding, 0)
	for _, id := range ruleIDs {
		findings = append(findings, e.rules[id].Evaluate(context)...)
	}
	sort.Slice(findings, func(i, j int) bool { return findingKey(findings[i]) < findingKey(findings[j]) })
	seen := map[string]struct{}{}
	for _, finding := range findings {
		key := findingKey(finding)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		issue := domainassessment.Issue{ID: uuid.NewSHA1(result.ID, []byte(key)), AssessmentID: result.ID, Severity: finding.Severity, Category: finding.Category, ResourceKind: finding.Resource.Kind, ResourceNamespace: finding.Resource.Namespace, ResourceName: finding.Resource.Name, RuleID: finding.RuleID, Title: finding.Title, Description: finding.Description, Remediation: finding.Remediation, AutoFixable: finding.AutoFixable}
		result.Issues = append(result.Issues, issue)
		switch finding.Severity {
		case domainassessment.SeverityBlocker:
			result.BlockerCount++
			result.Score -= 30
		case domainassessment.SeverityWarning:
			result.WarningCount++
			result.Score -= 10
		case domainassessment.SeverityInfo:
			result.InfoCount++
			result.Score -= 2
		}
	}
	if result.Score < 0 {
		result.Score = 0
	}
	return result
}

func findingKey(value Finding) string {
	return strings.Join([]string{string(value.Severity), string(value.Category), value.RuleID, value.Resource.APIVersion, value.Resource.Kind, value.Resource.Namespace, value.Resource.Name}, "\x00")
}

func resourceRef(value application.ResourceSummary) application.ResourceReference {
	return application.ResourceReference{APIVersion: value.APIVersion, Kind: value.Kind, Namespace: value.Namespace, Name: value.Name}
}
