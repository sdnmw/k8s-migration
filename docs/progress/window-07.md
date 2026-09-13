# 窗口 07：Kubernetes 集群能力发现

- 状态：完成
- 日期：2026-09-03
- 门禁：能力快照可持久化和刷新

## 已完成能力

- 在窗口 6 的基础连接检查之上实现完整 Kubernetes 能力发现。
- 采集 Kubernetes 版本、节点数、命名空间数与节点架构集合。
- 聚合所有节点的 `status.allocatable`，以 Kubernetes Quantity 字符串保存 CPU、内存、Pod 数及其他扩展资源。
- 发现 StorageClass 名称、Provisioner、默认标记、卷扩容能力和 VolumeBindingMode。
- 发现 CSI Driver、VolumeSnapshotClass 与 IngressClass。
- 发现 Core API 与聚合 API Group，并去重排序后持久化。
- 能力快照记录 TLS 验证状态，但不保存 kubeconfig、Token 或证书原文。
- 新增同步刷新、读取最近快照和命名空间列表 API。
- 发现失败时环境进入 `ERROR` 并保留上一份可用快照；可选资源缺失或权限不足只产生 WARNING 和部分结果。

## API

- `GET /api/v1/environments/{environmentId}/capabilities`
- `POST /api/v1/environments/{environmentId}/capabilities`
- `GET /api/v1/environments/{environmentId}/namespaces`
- 刷新与环境写操作共用登录、CSRF 和审计保护。
- OpenAPI 的 `ClusterCapabilities` 已补齐 `apiGroups`、`allocatable` 和 `security`。

## 前端

- 环境表新增“能力发现”操作。
- 点击集群名称可打开最近一次持久化快照；刷新成功后自动展示新快照。
- 能力抽屉展示版本、规模、可分配 CPU/内存、架构、CSI、快照类、IngressClass、StorageClass 和 API Groups。
- StorageClass 明确标识默认类、Provisioner 和在线扩容能力；不硬编码 SmartX StorageClass 名称。

## 自动化与集成验证

- TLS 测试集群覆盖 `/version`、Namespace、Node、StorageClass、CSIDriver、IngressClass、VolumeSnapshotClass、Core API 与 API Groups。
- 验证混合 `amd64`/`arm64` 架构去重排序。
- 验证两节点 CPU `8 + 16 = 24`、内存 `32Gi + 64Gi = 96Gi` 的 Quantity 聚合。
- 验证 `smtx-elf-csi-driver`、默认块 StorageClass、快照类与 IngressClass 发现。
- PostgreSQL 18 集成验证完整 JSONB 快照持久化和刷新时间更新。
- `go test -race`：Kubernetes Adapter、Environment Service、API 全部通过。
- `make test`、`make lint`、`make build`：通过。

## 浏览器验收

- 1280×900 下环境表无水平溢出，数据行保持 48px。
- 新增能力发现操作后，操作列仍完整可见。
- 560px 能力抽屉在 1280px 下完整展示，内部 StorageClass 表无页面级溢出。
- 修正 Ant Design Drawer 弃用属性后，浏览器控制台无 error 或 warning。

## 已知边界

- 当前能力刷新由 API 同步执行；窗口 15 接入统一 Worker 调度后，可切换为持久任务并通过 SSE 展示进度。
- VolumeSnapshotClass API 未安装时返回空能力而不是错误，这是源集群可能仅使用 FSB 的合法状态。
- 更细的 Pod Security、准入控制、可迁移 API 资源和资源依赖检查分别在 Inventory、Assessment 与 Preflight 窗口完成。
