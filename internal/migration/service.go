package migration

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	domainapplication "github.com/smartx/sks-migration-center/internal/domain/application"
	domainassessment "github.com/smartx/sks-migration-center/internal/domain/assessment"
	domainenvironment "github.com/smartx/sks-migration-center/internal/domain/environment"
	domainmapping "github.com/smartx/sks-migration-center/internal/domain/mapping"
	domainmigration "github.com/smartx/sks-migration-center/internal/domain/migration"
	"github.com/smartx/sks-migration-center/internal/repository"
)

var ErrInvalidInput = errors.New("invalid migration plan input")

type Service struct {
	plans        repository.MigrationPlanRepository
	environments repository.EnvironmentRepository
	applications repository.ApplicationRepository
	assessments  repository.AssessmentRepository
	mappings     repository.MappingRepository
	clock        func() time.Time
}

func NewService(plans repository.MigrationPlanRepository, environments repository.EnvironmentRepository, applications repository.ApplicationRepository, assessments repository.AssessmentRepository, mappings repository.MappingRepository) (*Service, error) {
	if plans == nil || environments == nil || applications == nil || assessments == nil || mappings == nil {
		return nil, errors.New("migration plan, environment, application, assessment and mapping repositories are required")
	}
	return &Service{plans: plans, environments: environments, applications: applications, assessments: assessments, mappings: mappings, clock: func() time.Time { return time.Now().UTC() }}, nil
}

func (s *Service) Create(ctx context.Context, value domainmigration.Plan) (domainmigration.Plan, error) {
	value.ID = uuid.New()
	value.Name = strings.TrimSpace(value.Name)
	value.Status = domainmigration.PlanDraft
	if err := value.Validate(); err != nil {
		return domainmigration.Plan{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if err := s.validateReferences(ctx, value); err != nil {
		return domainmigration.Plan{}, err
	}
	now := s.clock()
	value.CreatedAt, value.UpdatedAt = now, now
	if err := s.plans.CreatePlan(ctx, value); err != nil {
		return domainmigration.Plan{}, err
	}
	return value, nil
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (domainmigration.Plan, error) {
	if id == uuid.Nil {
		return domainmigration.Plan{}, fmt.Errorf("%w: planId is required", ErrInvalidInput)
	}
	return s.plans.GetPlan(ctx, id)
}

func (s *Service) List(ctx context.Context) ([]domainmigration.Plan, error) {
	return s.plans.ListPlans(ctx)
}

func (s *Service) Preflight(ctx context.Context, id uuid.UUID) (domainmigration.PreflightResult, error) {
	if id == uuid.Nil {
		return domainmigration.PreflightResult{}, fmt.Errorf("%w: planId is required", ErrInvalidInput)
	}
	plan, err := s.plans.GetPlan(ctx, id)
	if err != nil {
		return domainmigration.PreflightResult{}, err
	}
	source, target, application, assessment, profile, err := s.references(ctx, plan)
	if err != nil {
		return domainmigration.PreflightResult{}, err
	}
	if err := validateReferenceValues(source, target, application, assessment, profile); err != nil {
		return domainmigration.PreflightResult{}, err
	}
	checks := make([]domainmigration.PreflightCheck, 0, 12)
	checks = append(checks, connectionCheck("source.connection", "SOURCE", "源环境连接", source))
	checks = append(checks, connectionCheck("target.connection", "TARGET", "目标 SKS 连接", target))
	checks = append(checks, sourceCapabilityChecks(source)...)
	checks = append(checks, targetCapabilityChecks(target)...)
	checks = append(checks, assessmentChecks(assessment)...)
	checks = append(checks, strategyChecks(plan, source, target, application)...)
	checks = append(checks, storageMappingChecks(application, target, profile)...)
	checks = append(checks, registryCheck(application, profile))
	sort.SliceStable(checks, func(i, j int) bool { return checks[i].ID < checks[j].ID })
	result := domainmigration.PreflightResult{PlanID: id, Checks: checks}
	for _, check := range checks {
		switch check.Status {
		case domainmigration.PreflightBlocker:
			result.BlockerCount++
		case domainmigration.PreflightWarning:
			result.WarningCount++
		}
	}
	result.Ready = result.BlockerCount == 0
	status := domainmigration.PlanBlocked
	if result.Ready {
		status = domainmigration.PlanReady
	}
	if err := s.plans.UpdatePlanStatus(ctx, id, status); err != nil {
		return domainmigration.PreflightResult{}, err
	}
	return result, nil
}

func (s *Service) validateReferences(ctx context.Context, plan domainmigration.Plan) error {
	source, target, application, assessment, profile, err := s.references(ctx, plan)
	if err != nil {
		return err
	}
	return validateReferenceValues(source, target, application, assessment, profile)
}

func validateReferenceValues(source, target domainenvironment.Environment, application domainapplication.SourceApplication, assessment domainassessment.Assessment, profile domainmapping.Profile) error {
	if source.Role != domainenvironment.RoleSource || target.Role != domainenvironment.RoleTarget || target.Kind != domainenvironment.KindKubernetes {
		return fmt.Errorf("%w: source and target environment roles are invalid", ErrInvalidInput)
	}
	if application.EnvironmentID != source.ID || assessment.ApplicationID != application.ID || profile.TargetEnvironmentID != target.ID {
		return fmt.Errorf("%w: application, assessment or mapping does not belong to the selected environments", ErrInvalidInput)
	}
	if (application.SourceType == domainapplication.SourceKubernetes) != (source.Kind == domainenvironment.KindKubernetes) {
		return fmt.Errorf("%w: application type does not match the source environment", ErrInvalidInput)
	}
	if (application.SourceType == domainapplication.SourceCompose) != (source.Kind == domainenvironment.KindDockerCompose) {
		return fmt.Errorf("%w: application type does not match the source environment", ErrInvalidInput)
	}
	return nil
}

func (s *Service) references(ctx context.Context, plan domainmigration.Plan) (domainenvironment.Environment, domainenvironment.Environment, domainapplication.SourceApplication, domainassessment.Assessment, domainmapping.Profile, error) {
	source, err := s.environments.Get(ctx, plan.SourceEnvironmentID)
	if err != nil {
		return domainenvironment.Environment{}, domainenvironment.Environment{}, domainapplication.SourceApplication{}, domainassessment.Assessment{}, domainmapping.Profile{}, err
	}
	target, err := s.environments.Get(ctx, plan.TargetEnvironmentID)
	if err != nil {
		return domainenvironment.Environment{}, domainenvironment.Environment{}, domainapplication.SourceApplication{}, domainassessment.Assessment{}, domainmapping.Profile{}, err
	}
	application, err := s.applications.Get(ctx, plan.SourceApplicationID)
	if err != nil {
		return domainenvironment.Environment{}, domainenvironment.Environment{}, domainapplication.SourceApplication{}, domainassessment.Assessment{}, domainmapping.Profile{}, err
	}
	assessment, err := s.assessments.Get(ctx, plan.AssessmentID)
	if err != nil {
		return domainenvironment.Environment{}, domainenvironment.Environment{}, domainapplication.SourceApplication{}, domainassessment.Assessment{}, domainmapping.Profile{}, err
	}
	profile, err := resolveMappingProfile(ctx, s.mappings, plan)
	if err != nil {
		return domainenvironment.Environment{}, domainenvironment.Environment{}, domainapplication.SourceApplication{}, domainassessment.Assessment{}, domainmapping.Profile{}, err
	}
	return source, target, application, assessment, profile, nil
}

func resolveMappingProfile(ctx context.Context, mappings repository.MappingRepository, plan domainmigration.Plan) (domainmapping.Profile, error) {
	if plan.MappingProfileID == nil {
		return domainmapping.Profile{Name: "自动目标配置", TargetEnvironmentID: plan.TargetEnvironmentID}, nil
	}
	return mappings.Get(ctx, *plan.MappingProfileID)
}

func connectionCheck(id, category, title string, environment domainenvironment.Environment) domainmigration.PreflightCheck {
	if environment.Status == domainenvironment.StatusConnected {
		return check(id, category, domainmigration.PreflightPassed, title, "连接状态正常。", "")
	}
	return check(id, category, domainmigration.PreflightBlocker, title, "环境当前未连接。", "重新执行连接测试并修复凭证或网络。")
}

func sourceCapabilityChecks(source domainenvironment.Environment) []domainmigration.PreflightCheck {
	if source.Kind == domainenvironment.KindKubernetes {
		if source.CapabilitiesUpdatedAt == nil {
			return []domainmigration.PreflightCheck{check("source.capabilities", "SOURCE", domainmigration.PreflightBlocker, "源集群能力快照", "源 Kubernetes 尚未完成能力发现。", "刷新源集群能力快照。")}
		}
		return []domainmigration.PreflightCheck{check("source.capabilities", "SOURCE", domainmigration.PreflightPassed, "源集群能力快照", "源集群能力快照可用。", "")}
	}
	if source.Capabilities.Runtime["dockerVersion"] == "" || source.Capabilities.Runtime["composeVersion"] == "" {
		return []domainmigration.PreflightCheck{check("source.compose-runtime", "SOURCE", domainmigration.PreflightBlocker, "Compose 运行时", "Docker 或 Compose 版本尚未探测。", "重新测试 SSH 环境连接。")}
	}
	return []domainmigration.PreflightCheck{check("source.compose-runtime", "SOURCE", domainmigration.PreflightPassed, "Compose 运行时", "Docker 与 Compose 运行时信息可用。", "")}
}

func targetCapabilityChecks(target domainenvironment.Environment) []domainmigration.PreflightCheck {
	checks := make([]domainmigration.PreflightCheck, 0, 2)
	if target.CapabilitiesUpdatedAt == nil {
		checks = append(checks, check("target.capabilities", "TARGET", domainmigration.PreflightBlocker, "目标能力快照", "目标集群尚未完成能力发现。", "刷新目标 SKS 的能力快照。"))
	} else {
		checks = append(checks, check("target.capabilities", "TARGET", domainmigration.PreflightPassed, "目标能力快照", "目标集群能力快照可用。", ""))
	}
	smartxCSI := false
	for _, driver := range target.Capabilities.CSIDrivers {
		if isSmartXCSI(driver) {
			smartxCSI = true
		}
	}
	for _, storageClass := range target.Capabilities.StorageClasses {
		if isSmartXCSI(storageClass.Provisioner) {
			smartxCSI = true
		}
	}
	if smartxCSI {
		checks = append(checks, check("target.smartx-csi", "STORAGE", domainmigration.PreflightPassed, "SmartX 块存储 CSI", "已发现 smtx-elf-csi-driver。", ""))
	} else {
		checks = append(checks, check("target.smartx-csi", "STORAGE", domainmigration.PreflightBlocker, "SmartX 块存储 CSI", "未发现 smtx-elf-csi-driver。", "确认导入的是 SKS 工作负载集群并刷新能力快照。"))
	}
	return checks
}

func assessmentChecks(value domainassessment.Assessment) []domainmigration.PreflightCheck {
	if value.Status != domainassessment.StatusCompleted {
		return []domainmigration.PreflightCheck{check("assessment.completed", "ASSESSMENT", domainmigration.PreflightBlocker, "迁移评估", "评估尚未完成。", "重新执行目标兼容性评估。")}
	}
	checks := []domainmigration.PreflightCheck{check("assessment.completed", "ASSESSMENT", domainmigration.PreflightPassed, "迁移评估", "评估已完成。", "")}
	if value.BlockerCount > 0 {
		checks = append(checks, check("assessment.blockers", "ASSESSMENT", domainmigration.PreflightBlocker, "BLOCKER 门禁", fmt.Sprintf("仍有 %d 个阻塞项。", value.BlockerCount), "返回评估页修复全部 BLOCKER。"))
	} else {
		checks = append(checks, check("assessment.blockers", "ASSESSMENT", domainmigration.PreflightPassed, "BLOCKER 门禁", "没有未解决的阻塞项。", ""))
	}
	return checks
}

func strategyChecks(plan domainmigration.Plan, source, target domainenvironment.Environment, application domainapplication.SourceApplication) []domainmigration.PreflightCheck {
	checks := make([]domainmigration.PreflightCheck, 0, 3)
	mode := plan.Strategy.VolumeMode
	validMode := (application.SourceType == domainapplication.SourceCompose && mode == domainmigration.VolumeComposeKopia) ||
		(application.SourceType == domainapplication.SourceKubernetes && mode != domainmigration.VolumeComposeKopia)
	if validMode {
		checks = append(checks, check("strategy.volume-mode", "STRATEGY", domainmigration.PreflightPassed, "数据迁移策略", "数据迁移引擎与源类型匹配。", ""))
	} else {
		checks = append(checks, check("strategy.volume-mode", "STRATEGY", domainmigration.PreflightBlocker, "数据迁移策略", "数据迁移引擎与源类型不匹配。", "Kubernetes 使用 FSB/CSI Data Mover，Compose 使用 Kopia。"))
	}
	rawBlock := false
	for _, volume := range application.Inventory.PVCs {
		if strings.EqualFold(volume.VolumeMode, "Block") {
			rawBlock = true
		}
	}
	if rawBlock && mode != domainmigration.VolumeCSIDataMover {
		checks = append(checks, check("strategy.raw-block", "STORAGE", domainmigration.PreflightBlocker, "Raw Block PVC", "文件级备份不能迁移 Raw Block 设备。", "选择 CSI Data Mover，或为该卷提供受支持的数据迁移方式。"))
	} else if rawBlock {
		checks = append(checks, check("strategy.raw-block", "STORAGE", domainmigration.PreflightPassed, "Raw Block PVC", "Raw Block 卷已选择 CSI Data Mover，继续核验快照驱动和运行时。", ""))
		if source.Capabilities.SupportsRawBlockDataMover() {
			checks = append(checks, check("strategy.raw-block-os", "STORAGE", domainmigration.PreflightPassed, "Raw Block节点操作系统", "源集群节点均为 Linux。", ""))
		} else {
			checks = append(checks, check("strategy.raw-block-os", "STORAGE", domainmigration.PreflightBlocker, "Raw Block节点操作系统", "无法确认 Raw Block工作负载仅运行在 Linux节点。", "Raw Block CSI Data Mover不支持 Windows；刷新节点能力或调整工作负载后重试。"))
		}
	}
	if mode == domainmigration.VolumeCSIDataMover {
		checks = append(checks, dataMoverStrategyChecks(source, target, application)...)
	} else if mode == domainmigration.VolumeFSBackup {
		checks = append(checks, check("strategy.fsb-runtime", "STORAGE", domainmigration.PreflightWarning, "FSB 节点访问", "node-agent 的 hostPath、MountPropagation 和 kubelet root 路径将在执行前在线验证。", ""))
	}
	return checks
}

func dataMoverStrategyChecks(source, target domainenvironment.Environment, application domainapplication.SourceApplication) []domainmigration.PreflightCheck {
	checks := make([]domainmigration.PreflightCheck, 0, 3)
	if source.Capabilities.CSIDataMover.BackupReady {
		checks = append(checks, check("strategy.data-mover-source", "STORAGE", domainmigration.PreflightPassed, "源端 CSI Data Mover", "DataUpload、BackupRepository、EnableCSI 和 node-agent 已就绪。", ""))
	} else {
		checks = append(checks, check("strategy.data-mover-source", "STORAGE", domainmigration.PreflightBlocker, "源端 CSI Data Mover", "源端 DataUpload、BackupRepository、EnableCSI 或 node-agent 未就绪。", "安装 Velero 1.18/node-agent 并刷新源集群能力快照。"))
	}
	if target.Capabilities.CSIDataMover.RestoreReady {
		checks = append(checks, check("strategy.data-mover-target", "STORAGE", domainmigration.PreflightPassed, "目标端 CSI Data Mover", "DataDownload、BackupRepository、EnableCSI 和 node-agent 已就绪。", ""))
	} else {
		checks = append(checks, check("strategy.data-mover-target", "STORAGE", domainmigration.PreflightBlocker, "目标端 CSI Data Mover", "目标端 DataDownload、BackupRepository、EnableCSI 或 node-agent 未就绪。", "安装 Velero 1.18/node-agent 并刷新目标集群能力快照。"))
	}
	unmatched := make([]string, 0)
	for _, volume := range application.Inventory.PVCs {
		if _, found := source.Capabilities.SnapshotDriverForStorageClass(volume.StorageClassName); !found {
			unmatched = append(unmatched, volume.Name)
		}
	}
	if len(unmatched) == 0 {
		checks = append(checks, check("strategy.snapshot-driver-match", "STORAGE", domainmigration.PreflightPassed, "源卷快照驱动匹配", "每个 PVC 的 StorageClass 均有同驱动 VolumeSnapshotClass。", ""))
	} else {
		sort.Strings(unmatched)
		checks = append(checks, check("strategy.snapshot-driver-match", "STORAGE", domainmigration.PreflightBlocker, "源卷快照驱动匹配", "以下 PVC 没有同驱动 VolumeSnapshotClass："+strings.Join(unmatched, "、"), "为对应 CSI Driver 创建 VolumeSnapshotClass 后刷新能力快照。"))
	}
	return checks
}

func isSmartXCSI(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return value == "smtx-elf-csi-driver" || value == "com.smartx.elf-csi-driver"
}

func storageMappingChecks(application domainapplication.SourceApplication, target domainenvironment.Environment, profile domainmapping.Profile) []domainmigration.PreflightCheck {
	if application.SourceType == domainapplication.SourceCompose || len(application.Inventory.PVCs) == 0 {
		return []domainmigration.PreflightCheck{check("storage.mapping", "STORAGE", domainmigration.PreflightPassed, "卷映射", "源应用没有需要检查的 Kubernetes PVC。", "")}
	}
	available := map[string]bool{}
	defaultClass := ""
	for _, item := range target.Capabilities.StorageClasses {
		available[item.Name] = true
		if item.Default {
			defaultClass = item.Name
		}
	}
	mapped := map[string]string{}
	for _, item := range profile.Storage {
		mapped[item.Source] = item.Target
	}
	missing := make([]string, 0)
	for _, volume := range application.Inventory.PVCs {
		class := volume.StorageClassName
		if targetClass, found := mapped[class]; found {
			class = targetClass
		}
		if class == "" {
			class = defaultClass
		}
		if class == "" || !available[class] {
			missing = append(missing, volume.Name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return []domainmigration.PreflightCheck{check("storage.mapping", "STORAGE", domainmigration.PreflightBlocker, "PVC StorageClass 映射", "以下 PVC 没有可用的目标 StorageClass："+strings.Join(missing, "、"), "补充 StorageClass 或外部 NFS 映射。")}
	}
	return []domainmigration.PreflightCheck{check("storage.mapping", "STORAGE", domainmigration.PreflightPassed, "PVC StorageClass 映射", "所有 PVC 均可解析到目标 StorageClass。", "")}
}

func registryCheck(application domainapplication.SourceApplication, profile domainmapping.Profile) domainmigration.PreflightCheck {
	if len(application.Inventory.Images) == 0 || len(profile.Registries) > 0 {
		return check("registry.mapping", "IMAGE", domainmigration.PreflightPassed, "镜像仓库映射", "镜像引用已有可用路径或 Registry 映射。", "")
	}
	return check("registry.mapping", "IMAGE", domainmigration.PreflightWarning, "镜像仓库映射", "未配置 Registry 映射，后续 Preflight 将直接检查源镜像可达性。", "")
}

func check(id, category string, status domainmigration.PreflightCheckStatus, title, message, remediation string) domainmigration.PreflightCheck {
	return domainmigration.PreflightCheck{ID: id, Category: category, Status: status, Title: title, Message: message, Remediation: remediation}
}
