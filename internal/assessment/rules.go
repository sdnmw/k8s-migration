package assessment

import (
	"sort"
	"strings"

	"github.com/smartx/sks-migration-center/internal/domain/application"
	domainassessment "github.com/smartx/sks-migration-center/internal/domain/assessment"
)

type ComputeRequestsRule struct{}

func (ComputeRequestsRule) ID() string { return "COMPUTE_RESOURCE_REQUESTS" }
func (ComputeRequestsRule) Category() domainassessment.Category {
	return domainassessment.CategoryCompute
}
func (rule ComputeRequestsRule) Evaluate(ctx Context) []Finding {
	result := []Finding{}
	for _, workload := range ctx.Application.Inventory.Workloads {
		missing := append([]string(nil), workload.MissingRequests...)
		if len(missing) == 0 && len(workload.Requests) == 0 {
			missing = []string{"cpu", "memory"}
		}
		if len(missing) > 0 {
			result = append(result, Finding{Severity: domainassessment.SeverityWarning, Category: rule.Category(), Resource: resourceRef(workload), RuleID: rule.ID(), Title: "工作负载缺少资源请求", Description: "缺少 " + strings.Join(missing, "、") + " requests，目标集群无法可靠评估调度容量。", Remediation: "为所有容器配置 CPU 和内存 requests。"})
		}
	}
	return result
}

type ReplicaAvailabilityRule struct{}

func (ReplicaAvailabilityRule) ID() string { return "COMPUTE_SINGLE_REPLICA" }
func (ReplicaAvailabilityRule) Category() domainassessment.Category {
	return domainassessment.CategoryCompute
}
func (rule ReplicaAvailabilityRule) Evaluate(ctx Context) []Finding {
	result := []Finding{}
	for _, workload := range ctx.Application.Inventory.Workloads {
		if workload.Kind == "Deployment" || workload.Kind == "StatefulSet" {
			if workload.Replicas == nil || *workload.Replicas < 2 {
				result = append(result, Finding{Severity: domainassessment.SeverityInfo, Category: rule.Category(), Resource: resourceRef(workload), RuleID: rule.ID(), Title: "工作负载副本数低于 2", Description: "迁移恢复期间没有应用级副本冗余。", Remediation: "确认业务可接受单副本，或在切流前扩容。"})
			}
		}
	}
	return result
}

type DeprecatedAPIRule struct{}

func (DeprecatedAPIRule) ID() string                          { return "API_REMOVED_VERSION" }
func (DeprecatedAPIRule) Category() domainassessment.Category { return domainassessment.CategoryAPI }
func (rule DeprecatedAPIRule) Evaluate(ctx Context) []Finding {
	removed := map[string]struct{}{"extensions/v1beta1/Ingress": {}, "networking.k8s.io/v1beta1/Ingress": {}, "apps/v1beta1/Deployment": {}, "apps/v1beta2/Deployment": {}, "batch/v1beta1/CronJob": {}, "policy/v1beta1/PodDisruptionBudget": {}}
	result := []Finding{}
	for _, resource := range ctx.Application.Inventory.Resources {
		if _, ok := removed[resource.APIVersion+"/"+resource.Kind]; ok {
			result = append(result, Finding{Severity: domainassessment.SeverityBlocker, Category: rule.Category(), Resource: resourceRef(resource), RuleID: rule.ID(), Title: "目标版本已移除该 API", Description: resource.APIVersion + " " + resource.Kind + " 不能直接恢复。", Remediation: "在转换规则中升级到目标集群支持的稳定 API。", AutoFixable: true})
		}
	}
	return result
}

type TargetAPIAvailabilityRule struct{}

func (TargetAPIAvailabilityRule) ID() string { return "API_GROUP_UNAVAILABLE" }
func (TargetAPIAvailabilityRule) Category() domainassessment.Category {
	return domainassessment.CategoryAPI
}
func (rule TargetAPIAvailabilityRule) Evaluate(ctx Context) []Finding {
	available := map[string]struct{}{}
	for _, group := range ctx.Target.APIGroups {
		available[group] = struct{}{}
	}
	if len(available) == 0 {
		return nil
	}
	result := []Finding{}
	for _, resource := range ctx.Application.Inventory.Resources {
		group := resource.APIVersion
		if before, _, ok := strings.Cut(group, "/"); ok {
			group = before
		}
		if _, ok := available[group]; !ok {
			result = append(result, Finding{Severity: domainassessment.SeverityBlocker, Category: rule.Category(), Resource: resourceRef(resource), RuleID: rule.ID(), Title: "目标集群缺少 API Group", Description: "目标集群未发现 " + group + "。", Remediation: "先安装对应 CRD/Operator，或排除该资源。"})
		}
	}
	return result
}

type RequiredDependencyRule struct{}

func (RequiredDependencyRule) ID() string { return "DEPENDENCY_REQUIRED_MISSING" }
func (RequiredDependencyRule) Category() domainassessment.Category {
	return domainassessment.CategoryDependency
}
func (rule RequiredDependencyRule) Evaluate(ctx Context) []Finding {
	known := map[string]struct{}{}
	for _, resource := range ctx.Application.Inventory.Resources {
		known[refKey(resourceRef(resource))] = struct{}{}
	}
	for _, class := range ctx.Target.StorageClasses {
		known[refKey(application.ResourceReference{APIVersion: "storage.k8s.io/v1", Kind: "StorageClass", Name: class.Name})] = struct{}{}
	}
	type missingGroup struct {
		from     application.ResourceReference
		severity domainassessment.Severity
		targets  []string
	}
	groups := map[string]missingGroup{}
	for _, dep := range ctx.Application.Inventory.Dependencies {
		if !dep.Required {
			continue
		}
		if dep.To.Kind == "ClusterRole" {
			continue
		}
		if _, ok := known[refKey(dep.To)]; !ok {
			severity := domainassessment.SeverityBlocker
			if dep.To.Kind == "StorageClass" {
				severity = domainassessment.SeverityWarning
			}
			key := string(severity) + "\x00" + refKey(dep.From)
			group := groups[key]
			group.from, group.severity = dep.From, severity
			group.targets = append(group.targets, dep.To.Kind+"/"+dep.To.Name)
			groups[key] = group
		}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]Finding, 0, len(keys))
	for _, key := range keys {
		group := groups[key]
		sort.Strings(group.targets)
		group.targets = compactStrings(group.targets)
		result = append(result, Finding{Severity: group.severity, Category: rule.Category(), Resource: group.from, RuleID: rule.ID(), Title: "缺少必需依赖", Description: strings.Join(group.targets, "、") + " 不在源 Inventory 或目标能力中。", Remediation: "补充资源、建立目标映射或明确排除该依赖。"})
	}
	return result
}

func compactStrings(values []string) []string {
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}
func refKey(value application.ResourceReference) string {
	return strings.Join([]string{value.APIVersion, value.Kind, value.Namespace, value.Name}, "\x00")
}
