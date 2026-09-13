# 窗口 11：STORAGE/NETWORK/SECURITY/IMAGE 规则与评估 UI

- 状态：完成
- 日期：2026-09-03
- 门禁：BLOCKER 能可靠禁止继续

## 已完成规则

### STORAGE

- `STORAGE_RAW_BLOCK`：`volumeMode=Block` 在 CSI Snapshot/Data Mover 能力确认前标记 BLOCKER，禁止伪装为文件级迁移。
- `STORAGE_RWX_TARGET`：RWX PVC 在目标能力快照没有 NFS StorageClass 时标记 BLOCKER，引导管理员先完成外部 NFS 接入。
- 已发现 `nfs.csi.k8s.io` 或名称/Provisioner 明确包含 NFS 的目标 StorageClass 时，RWX 规则通过。

### NETWORK

- `NETWORK_INGRESS_CLASS`：源 IngressClass 在目标不存在时产生 WARNING，并标记可通过映射自动修复。
- `NETWORK_EXPOSED_SERVICE`：NodePort/LoadBalancer Service 产生 WARNING，明确外部地址、端口和流量切换不自动继承。

### SECURITY

- `SECURITY_HOST_ACCESS`：识别 hostPath、hostNetwork、hostPID、hostIPC、hostPort 和 privileged。
- hostPath 与节点文件系统强耦合，产生 BLOCKER；其他主机/特权能力产生 WARNING，要求目标安全策略确认。
- Inventory 只保存风险代码，不保存 hostPath 实际路径或完整 SecurityContext。

### IMAGE

- `IMAGE_MUTABLE_REFERENCE`：隐式/显式 latest 产生 WARNING；普通 tag 未固定 digest 产生 INFO；digest 引用通过。
- `IMAGE_ARCHITECTURE_UNVERIFIED`：目标架构已知而 Registry manifest 尚未探测时产生 INFO，等待镜像凭证与映射阶段核验。

## Inventory 扩展

- 资源摘要新增 IngressClass、Service Type、Security Risk Codes。
- requests/limits 仍采用 Kubernetes 调度有效值，并通过 `missingRequests` 保留逐容器配置完整性。
- 所有新增字段均为评估所需的非敏感事实；Secret 值、ConfigMap 值、hostPath 路径和完整资源 Spec 不进入数据库。

## 评估界面

- Namespace Inventory 新增“迁移评估”页签。
- 仅列出已连接且完成能力发现的 SKS 工作负载集群；未满足条件时直接提示操作路径。
- 展示总分、BLOCKER/WARNING/INFO 计数，以及级别、类别、资源、问题描述和修复建议。
- 存在任一 BLOCKER 时显示红色门禁并禁用“继续目标映射”；无 BLOCKER 时才开放下一阶段按钮。
- 目标选择变更会清空旧结果，避免把前一目标的评估误用于当前目标。

## 验证

- 规则单元测试覆盖 raw block、RWX/NFS、IngressClass、外部 Service、hostPath/privileged、latest/digest 和镜像架构。
- Kubernetes 摘要测试覆盖所有安全风险代码，确认只返回风险类型，不返回主机路径。
- 1280×720 浏览器实测完成“发现 Namespace → 选择 SKS → 开始评估 → 查看四级摘要与问题 → BLOCKER 禁用继续”。
- 720px 抽屉、评估表格和页面均无水平溢出。
- 浏览器实测发现并修复 Ant Design Statistic 弃用属性，控制台不再产生该告警。
- `go test -race`、`make test`、`make lint`、`make build`：通过。

## 已知边界

- Registry 远程存在性、认证、digest 与多架构 manifest 探测将在窗口 12/镜像映射阶段实现；本窗口只基于安全 Inventory 做静态判断。
- raw block 的最终可迁移结论必须等窗口 21 实测 CSI Snapshot/Data Mover；当前按安全默认值阻断。
- Pod Security Admission、OPA/Gatekeeper/Kyverno 的具体目标约束将在 Preflight 中做服务端 dry-run；当前安全规则只评估显式资源事实。
