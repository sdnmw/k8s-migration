# 窗口 09：Kubernetes Namespace Inventory 与依赖图

- 状态：完成
- 日期：2026-09-03
- 门禁：常用工作负载和 CRD Inventory 测试通过

## 已完成能力

- 从已登记的 `SOURCE/KUBERNETES` 环境选择 Namespace，发现并持久化源应用 Inventory。
- 标准资源范围包括 Deployment、StatefulSet、DaemonSet、Job、CronJob、Service、Ingress、NetworkPolicy、ConfigMap、Secret、ServiceAccount、PVC、Role、RoleBinding、HPA 和 PDB。
- 通过 `apiextensions.k8s.io/v1` CRD 定义反查 Namespaced 自定义资源，优先选择 storage version，并记录实际存在实例的 CRD。
- 所有资源统一规范化为稳定的 API Version、Kind、Namespace、Name、Label、镜像与数据键摘要；不保存 Kubernetes runtime 字段或完整 Manifest。
- Secret 与 ConfigMap 只记录排序后的键名，绝不把值写入 Inventory、数据库或 API 响应。
- PVC 规范化容量字节数、StorageClass、AccessMode 和 VolumeMode，不记录绑定 PV 名称。
- 镜像引用去重排序，资源、PVC、依赖边和警告均确定性排序，避免重扫产生无意义差异。
- 同一环境与 Namespace 重复发现使用数据库 upsert，保留 SourceApplication ID 与首次创建时间，并原子替换 Inventory。
- 可选 API 无权限或不可用时生成结构化 warning；Namespace 不存在或核心访问失败时明确失败。

## 依赖图

- Pod Template → ServiceAccount
- Pod Template → imagePullSecret
- Pod Template → PVC、ConfigMap、Secret volume
- Container env/envFrom → ConfigMap、Secret
- Service selector → 匹配的 Workload
- Ingress backend → Service
- RoleBinding → Role/ClusterRole 与 ServiceAccount
- HPA → scaleTargetRef
- PVC → StorageClass
- OwnerReference → Owner
- 所有依赖边记录类型、来源、目标和是否为必需依赖，并去重排序。

## API 与前端

- `POST /api/v1/applications/discover`
- `GET /api/v1/applications?environmentId=...`
- `GET /api/v1/applications/{applicationId}`
- OpenAPI 新增 SourceApplication、KubernetesInventory、ResourceSummary、Dependency、PVC、Image 与 Warning Schema。
- 源环境表新增“发现应用”，提供 Namespace 搜索选择和发现状态。
- 720px Inventory 抽屉提供资源、依赖、PVC、镜像四个视图，并显示资源/工作负载/PVC/依赖数量。
- 使用应用级 Ant Design Message Context，修复浏览器实际执行时的动态主题上下文告警。

## 自动化与集成验证

- 纯规范化测试覆盖工作负载、Service、Ingress、RBAC、PVC、CR、Secret/ConfigMap 脱敏与完整依赖图。
- TLS 测试 API Server 覆盖真实 dynamic client、Namespace 校验、标准资源列表、CRD 定义和 CR 实例。
- JSON 泄漏测试确认不存在 Secret 值、ConfigMap 值和绑定 PV 名称。
- PostgreSQL 18 集成测试验证 upsert 身份稳定、JSONB Inventory 更新和列表读取。
- `go test -race`：Kubernetes Adapter、Application Service、API、PostgreSQL Repository 全部通过。
- `make test`、`make lint`、`make build`：通过。

## 浏览器验收

- 1280×720 下源环境表无页面级水平溢出，新增操作列完整可见。
- Namespace 选择弹窗约 520×389px，无溢出。
- Inventory 抽屉宽 720px，摘要与资源表宽度完整落在抽屉内。
- 完整执行“发现应用 → 选择 Namespace → 展示 Inventory”，Vite 未收到新的浏览器控制台告警。

## 已知边界

- 当前发现为同步 API；窗口 15 会接入 PostgreSQL Worker 和 SSE，支持长任务进度与恢复。
- 当前 Inventory 是评估用安全摘要，不保存完整 Manifest；窗口 13 Transform Engine 会在受控读取后生成可部署 Manifest。
- CRD 依赖只能可靠识别 OwnerReference；自定义 Spec 内的业务引用需要窗口 10/11 的可扩展 Assessment 规则或后续显式映射。
- ClusterRole 等集群级依赖会出现在依赖图，但不会自动归入单 Namespace 迁移资源集合。
