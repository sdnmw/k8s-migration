# 窗口 13：Transform Engine 与 Manifest Diff

- 状态：完成
- 日期：2026-09-03
- 门禁：Golden Manifest 测试通过

## 已完成能力

- 新增独立 Transform Engine，支持最多 10 MiB、1000 个对象的多文档 Kubernetes YAML，并按 Kind、Namespace、Name、API Version 确定性排序。
- 清理 `status`、UID、resourceVersion、generation、时间戳、managedFields、ownerReferences、finalizers 等目标集群不可复用的运行时字段。
- 清理 Service 动态 ClusterIP/NodePort、PVC volumeName、ServiceAccount secrets、Job selector 等绑定态字段；保留 Headless Service 的 `clusterIP: None` 与 `clusterIPs: [None]` 语义。
- 去除 controller 生成标签，且在旧 Deployment 升级 `apps/v1` 前先清理 Pod Template，避免生成的 selector 带入 `pod-template-hash`。
- 将旧版 Deployment、Ingress、CronJob 和 PodDisruptionBudget API 转换到稳定版本；Ingress backend、service port 与 pathType 同步升级。
- 将窗口 12 的 Namespace、StorageClass、IngressClass、Registry、NodeLabel 和外部 NFS 六类映射应用到目标对象。
- 执行对象保留真实 Secret 值供后续 Worker 使用；API 预览对象、源 YAML、目标 YAML 和 Unified Diff 全部使用独立副本脱敏。

## API 与安全

- 新增 `POST /api/v1/transforms/preview`，接收映射配置 ID 和 multipart YAML 文件。
- 请求体受 10 MiB 上限保护，上传临时文件在请求结束后清理；错误使用 `TRANSFORM_INVALID` 与 `MAPPING_NOT_FOUND` 稳定错误码。
- API JSON 明确排除内部可执行对象，只返回脱敏的 source/target/diff 文本。
- 审计事件只记录 profileId、对象数和变更数，不记录 Manifest、Secret 或其他业务内容。
- OpenAPI 增加 multipart 请求、TransformDocument 和 TransformResult 契约，校验保持有效。

## 管理界面

- “转换规则”升级为可执行的 Manifest 转换预览页。
- 支持选择目标映射、上传 YAML、查看对象数和变更数，并在资源列表中逐项切换。
- 每个对象提供目标 YAML、Unified Diff、源 YAML 三种视图；代码区域独立滚动，长 Registry 地址不会撑破页面。
- 页面明确提示 Secret 在三个视图中始终脱敏。

## 验证

- Golden Manifest 覆盖旧版 Deployment/Ingress、Namespace、SC、Registry、NodeLabel、NFS、Secret、Service 运行时字段和确定性输出。
- 单元测试覆盖非法/超限 YAML、Secret 执行对象保真与预览脱敏、Headless Service 语义、Profile 加载和 API 错误映射。
- 1280×720 浏览器实测资源切换、Unified Diff、Secret 脱敏和滚动布局；页面无水平溢出。
- `go test -race ./internal/transform ./internal/api`：通过。
- `make test && make lint && make build`：通过；OpenAPI 仍只有后续未实现接口的既有 18 条 4xx 告警。

## 已知边界

- 本窗口只生成可审阅、可执行的目标对象，不直接写入目标集群；部署由迁移计划和 Worker 后续步骤执行。
- CRD 和未知资源只执行通用运行时字段清理与适用的映射，不臆测第三方 API 转换规则。
- YAML 注释不会在结构化转换后保留；原始上传内容不持久化、不进入日志或审计。
