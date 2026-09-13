# 窗口 10：Assessment 规则引擎与 COMPUTE/API/DEPENDENCY 规则

- 状态：完成
- 日期：2026-09-03
- 门禁：规则可独立注册、评分与问题顺序稳定

## 已完成能力

- 新增可插拔 Assessment Engine；每条规则通过稳定 ID、Category 和 `Evaluate` 接口独立注册，重复 ID 在启动时拒绝。
- 规则按 ID、问题按严重级别/类别/规则/资源确定性排序；相同输入的评分、计数和问题顺序稳定。
- 评分基准为 100 分：BLOCKER 每项扣 30 分、WARNING 每项扣 10 分、INFO 每项扣 2 分，最低为 0 分。
- Assessment 与 Issue 使用独立 UUID，完成时间、严重级别计数、修复建议和是否可自动修复均持久化。
- 创建评估前强制校验目标必须是已连接、已刷新能力快照的 `TARGET/KUBERNETES` 环境。

## 首批规则

- `COMPUTE_RESOURCE_REQUESTS`：任一业务或 Init Container 缺少 CPU/内存 requests 时产生 WARNING。
- `COMPUTE_SINGLE_REPLICA`：Deployment/StatefulSet 副本数低于 2 时产生 INFO。
- `API_REMOVED_VERSION`：识别已移除的 Ingress、Deployment、CronJob、PDB beta API，产生可自动转换的 BLOCKER。
- `API_GROUP_UNAVAILABLE`：源资源 API Group 不在目标能力快照时产生 BLOCKER。
- `DEPENDENCY_REQUIRED_MISSING`：必需的 Secret、ConfigMap、ServiceAccount 等依赖缺失时产生 BLOCKER；缺少目标 StorageClass 时产生 WARNING。
- 同一资源缺失多个同严重级别依赖时合并为一个问题，既完整列出目标，也满足数据库唯一性约束。

## Kubernetes 资源计算修正

- Pod requests/limits 使用调度语义计算：普通容器逐项求和，Init Container 逐资源取最大值，两者再取较大值，最后增加 Pod overhead。
- 单独保留 `missingRequests`，避免“一个容器配置了资源、另一个没有配置”被总量掩盖。
- 资源统计与缺失配置均写入安全 Inventory 摘要，不保存完整 Pod Spec。

## API 与持久化

- `POST /api/v1/assessments`：输入 `applicationId` 和 `targetEnvironmentId`，同步返回并持久化完成结果。
- `GET /api/v1/assessments/{assessmentId}`：读取评估与确定性排序的问题列表。
- 创建评估及失败尝试写入审计事件；错误统一映射为 `ASSESSMENT_INVALID`、`ASSESSMENT_NOT_FOUND` 或通用内部错误。
- PostgreSQL 使用单事务写入 Assessment 与全部 Issue，避免部分结果可见。
- OpenAPI 补齐创建请求、Assessment、AssessmentIssue、201/400/404 契约。

## 自动化与集成验证

- 单元测试覆盖重复规则拒绝、评分与排序稳定、缺失依赖聚合、目标环境能力门禁。
- Kubernetes 测试覆盖多容器、多个 Init Container 和 Pod overhead 的有效资源计算，以及逐容器 requests 缺失检测。
- API 测试覆盖成功响应和稳定错误码。
- PostgreSQL 18 集成测试覆盖 Assessment/Issue 事务写入与完整回读。
- `go test -race`、`make test`、`make lint`、`make build`：通过。

## 已知边界

- 本窗口仅实现 COMPUTE、API、DEPENDENCY 三类规则；STORAGE、NETWORK、SECURITY、IMAGE 规则与评估页面在窗口 11 完成。
- 当前评估为快速同步计算；窗口 15 的 Worker/SSE 接入后，耗时的 Registry、镜像架构和在线探测规则将作为可恢复任务运行。
- beta API 列表是明确的兼容性基线；目标 Kubernetes 版本感知和更完整的 API 迁移矩阵将在窗口 11/13 继续扩展。
