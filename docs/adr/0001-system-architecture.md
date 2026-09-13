# ADR 0001：系统架构基线

- 状态：已接受
- 日期：2026-09-02

## 背景

SKS Migration Center 需要同时编排 Kubernetes、Velero、Kompose、对象存储和外部主机上的 Compose 数据迁移。迁移步骤持续时间长，必须在 API 重启、页面刷新和临时网络故障后继续追踪。

## 决策

1. 后端使用 Go，分为 API 与 Worker 两个进程。
2. PostgreSQL 是任务、事件、审计和配置元数据的唯一持久化数据库。
3. Worker 通过数据库任务租约实现幂等执行和故障接管，不引入独立消息队列。
4. 前端使用 React、TypeScript、Ant Design 和 REST/SSE；不使用 WebSocket。
5. 正式迁移适配器调用 Kubernetes API 和 Velero CR，不通过 Shell 调用 Velero CLI。
6. 所有集群通过独立 kubeconfig 注册；不操作 CAPI 管控集群和 SKS 私有 API。
7. 系统部署到目标 SKS 工作负载集群。

## 后果

- API 可水平扩展，Worker 可通过任务租约扩展并安全接管失败任务。
- 长任务状态必须先持久化，再通过 SSE 投影到 UI。
- PostgreSQL 和凭证主密钥属于平台恢复所需的核心数据。
- 集群级安装 Velero、NFS CSI 等组件需要显式的高权限凭证和审计。

