package assessment

import (
	"strings"

	"github.com/smartx/sks-migration-center/internal/domain/application"
	domainassessment "github.com/smartx/sks-migration-center/internal/domain/assessment"
)

type ComposeUnresolvedVariablesRule struct{}

func (ComposeUnresolvedVariablesRule) ID() string { return "COMPOSE_UNRESOLVED_VARIABLES" }
func (ComposeUnresolvedVariablesRule) Category() domainassessment.Category {
	return domainassessment.CategoryDependency
}
func (rule ComposeUnresolvedVariablesRule) Evaluate(ctx Context) []Finding {
	compose := ctx.Application.Inventory.Compose
	if compose == nil {
		return nil
	}
	for _, warning := range compose.Warnings {
		if warning.Code != rule.ID() {
			continue
		}
		name := compose.ProjectName
		if name == "" {
			name = ctx.Application.Name
		}
		return []Finding{{
			Severity:    domainassessment.SeverityBlocker,
			Category:    rule.Category(),
			Resource:    application.ResourceReference{Kind: "ComposeProject", Name: name},
			RuleID:      rule.ID(),
			Title:       "Compose 环境变量尚未解析",
			Description: warning.Message,
			Remediation: "重新注册该 Compose 应用并上传受控 .env，或补齐运行所需变量后重新发现并评估。",
		}}
	}
	return nil
}

type RawBlockVolumeRule struct{}

func (RawBlockVolumeRule) ID() string { return "STORAGE_RAW_BLOCK" }
func (RawBlockVolumeRule) Category() domainassessment.Category {
	return domainassessment.CategoryStorage
}
func (rule RawBlockVolumeRule) Evaluate(ctx Context) []Finding {
	result := []Finding{}
	for _, volume := range ctx.Application.Inventory.PVCs {
		if strings.EqualFold(volume.VolumeMode, "Block") {
			severity := domainassessment.SeverityBlocker
			title := "原始块卷尚无可用迁移路径"
			description := "PVC 使用 volumeMode=Block，不能按文件系统备份迁移。"
			remediation := "在源端安装并验证 CSI Snapshot/Data Mover，在目标端验证 DataDownload 和 node-agent；未确认前禁止迁移。"
			_, snapshotMatch := ctx.Source.SnapshotDriverForStorageClass(volume.StorageClassName)
			if snapshotMatch && ctx.Source.SupportsRawBlockDataMover() && ctx.Source.CSIDataMover.BackupReady && ctx.Target.CSIDataMover.RestoreReady {
				severity = domainassessment.SeverityWarning
				title = "原始块卷必须使用 CSI Data Mover"
				description = "源 StorageClass 已匹配快照驱动，源端 DataUpload 和目标端 DataDownload 运行时均就绪。"
				remediation = "创建迁移计划时必须选择 CSI_DATA_MOVER；执行前仍会进行实时探测。Raw Block仅在 Linux节点上放行。"
			}
			result = append(result, Finding{Severity: severity, Category: rule.Category(), Resource: volumeRef(volume), RuleID: rule.ID(), Title: title, Description: description, Remediation: remediation})
		}
	}
	return result
}

type RWXStorageRule struct{}

func (RWXStorageRule) ID() string                          { return "STORAGE_RWX_TARGET" }
func (RWXStorageRule) Category() domainassessment.Category { return domainassessment.CategoryStorage }
func (rule RWXStorageRule) Evaluate(ctx Context) []Finding {
	hasNFS := false
	for _, class := range ctx.Target.StorageClasses {
		if strings.Contains(strings.ToLower(class.Provisioner+"/"+class.Name), "nfs") {
			hasNFS = true
		}
	}
	if hasNFS {
		return nil
	}
	result := []Finding{}
	for _, volume := range ctx.Application.Inventory.PVCs {
		if contains(volume.AccessModes, "ReadWriteMany") {
			result = append(result, Finding{Severity: domainassessment.SeverityBlocker, Category: rule.Category(), Resource: volumeRef(volume), RuleID: rule.ID(), Title: "目标集群缺少 RWX 文件存储", Description: "PVC 需要 ReadWriteMany，但目标能力快照中没有 NFS StorageClass。", Remediation: "通过外部 NFS 向导安装 NFS CSI 并创建、验证目标 StorageClass。"})
		}
	}
	return result
}

type IngressClassRule struct{}

func (IngressClassRule) ID() string                          { return "NETWORK_INGRESS_CLASS" }
func (IngressClassRule) Category() domainassessment.Category { return domainassessment.CategoryNetwork }
func (rule IngressClassRule) Evaluate(ctx Context) []Finding {
	available := stringSet(ctx.Target.IngressClasses)
	result := []Finding{}
	for _, ingress := range ctx.Application.Inventory.Ingresses {
		if ingress.IngressClassName != "" {
			if _, found := available[ingress.IngressClassName]; !found {
				result = append(result, Finding{Severity: domainassessment.SeverityWarning, Category: rule.Category(), Resource: resourceRef(ingress), RuleID: rule.ID(), Title: "IngressClass 需要映射", Description: "源 IngressClass " + ingress.IngressClassName + " 不在目标能力快照中。", Remediation: "选择目标 IngressClass 并在转换阶段改写。", AutoFixable: true})
			}
		}
	}
	return result
}

type ExposedServiceRule struct{}

func (ExposedServiceRule) ID() string { return "NETWORK_EXPOSED_SERVICE" }
func (ExposedServiceRule) Category() domainassessment.Category {
	return domainassessment.CategoryNetwork
}
func (rule ExposedServiceRule) Evaluate(ctx Context) []Finding {
	result := []Finding{}
	for _, service := range ctx.Application.Inventory.Services {
		if service.ServiceType == "NodePort" || service.ServiceType == "LoadBalancer" {
			result = append(result, Finding{Severity: domainassessment.SeverityWarning, Category: rule.Category(), Resource: resourceRef(service), RuleID: rule.ID(), Title: "外部服务地址不会自动迁移", Description: "Service 类型为 " + service.ServiceType + "，目标地址和端口需要重新分配。", Remediation: "在网络映射中确认目标暴露方式；切流仍需人工执行。"})
		}
	}
	return result
}

type PodSecurityRule struct{}

func (PodSecurityRule) ID() string                          { return "SECURITY_HOST_ACCESS" }
func (PodSecurityRule) Category() domainassessment.Category { return domainassessment.CategorySecurity }
func (rule PodSecurityRule) Evaluate(ctx Context) []Finding {
	result := []Finding{}
	for _, workload := range ctx.Application.Inventory.Workloads {
		if len(workload.SecurityRisks) == 0 {
			continue
		}
		severity := domainassessment.SeverityWarning
		if contains(workload.SecurityRisks, "HOST_PATH") {
			severity = domainassessment.SeverityBlocker
		}
		result = append(result, Finding{Severity: severity, Category: rule.Category(), Resource: resourceRef(workload), RuleID: rule.ID(), Title: "工作负载使用高风险主机能力", Description: "检测到 " + strings.Join(workload.SecurityRisks, "、") + "。", Remediation: "移除主机耦合和特权配置，或在目标安全策略中经过人工例外审批。"})
	}
	return result
}

type ImageReferenceRule struct{}

func (ImageReferenceRule) ID() string                          { return "IMAGE_MUTABLE_REFERENCE" }
func (ImageReferenceRule) Category() domainassessment.Category { return domainassessment.CategoryImage }
func (rule ImageReferenceRule) Evaluate(ctx Context) []Finding {
	result := []Finding{}
	for _, image := range ctx.Application.Inventory.Images {
		if strings.Contains(image.Reference, "@sha256:") {
			continue
		}
		severity := domainassessment.SeverityInfo
		title := "镜像未固定 digest"
		if imageHasMutableTag(image.Reference) {
			severity, title = domainassessment.SeverityWarning, "镜像使用可变 latest 标签"
		}
		result = append(result, Finding{Severity: severity, Category: rule.Category(), Resource: imageRef(image), RuleID: rule.ID(), Title: title, Description: image.Reference + " 无法保证恢复时内容与源端一致。", Remediation: "解析并锁定镜像 digest，必要时复制到目标 Harbor。", AutoFixable: true})
	}
	return result
}

type ImageArchitectureRule struct{}

func (ImageArchitectureRule) ID() string { return "IMAGE_ARCHITECTURE_UNVERIFIED" }
func (ImageArchitectureRule) Category() domainassessment.Category {
	return domainassessment.CategoryImage
}
func (rule ImageArchitectureRule) Evaluate(ctx Context) []Finding {
	if len(ctx.Target.Architectures) == 0 {
		return nil
	}
	result := []Finding{}
	for _, image := range ctx.Application.Inventory.Images {
		if len(image.Architectures) == 0 {
			result = append(result, Finding{Severity: domainassessment.SeverityInfo, Category: rule.Category(), Resource: imageRef(image), RuleID: rule.ID(), Title: "镜像架构尚未验证", Description: "目标节点架构为 " + strings.Join(ctx.Target.Architectures, "、") + "，尚未读取镜像 manifest。", Remediation: "配置 Registry 凭证后执行镜像存在性和架构检查。"})
		}
	}
	return result
}

func volumeRef(value application.VolumeSummary) application.ResourceReference {
	return application.ResourceReference{APIVersion: "v1", Kind: "PersistentVolumeClaim", Namespace: value.Namespace, Name: value.Name}
}

func imageRef(value application.ImageSummary) application.ResourceReference {
	return application.ResourceReference{Kind: "Image", Name: value.Reference}
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func imageHasMutableTag(reference string) bool {
	if strings.Contains(reference, "@") {
		return false
	}
	withoutDigest := strings.SplitN(reference, "@", 2)[0]
	lastSlash := strings.LastIndex(withoutDigest, "/")
	lastColon := strings.LastIndex(withoutDigest, ":")
	return lastColon <= lastSlash || strings.EqualFold(withoutDigest[lastColon+1:], "latest")
}
