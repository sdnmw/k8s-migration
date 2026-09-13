# 窗口 15：Worker 调度、SSE、取消与断点恢复

- 状态：完成
- 日期：2026-09-04
- 门禁：页面刷新和服务重启后任务、步骤与事件进度不丢失

## 持久化任务编排

- 新增独立 `RunService`，只允许 READY 的 MigrationPlan 启动；失败或取消的 Run 使用新 Run 编号重试，保留原任务和事件历史。
- 创建 Run、确定递增 RunNumber、写入完整步骤链、入队首步骤、更新 Plan 为 RUNNING、记录 `RUN_CREATED` 事件在一个 PostgreSQL 事务中完成。
- 步骤链按计划策略确定：可选 PRESYNC、可选卷 TRANSFER，以及 PREFLIGHT、QUIESCE、FINAL_BACKUP、TRANSFORM、RESTORE、VALIDATION 和 AWAIT_CUTOVER。
- 每个步骤持久化稳定 idempotency key；进程重启后 Worker 从数据库租约继续领取，不依赖内存队列、Redis 或消息中间件。

## Worker 推进与故障处理

- Worker 领取步骤时在同一事务中更新 Step 与 MigrationRun 状态，并记录 `STEP_STARTED`。
- 步骤成功后记录 `STEP_SUCCEEDED`、计算整体进度并入队下一个持久化步骤。
- VALIDATION 后不把人工切流当作自动任务：`AWAIT_CUTOVER` 进入运行中门禁，但不创建 Worker job。
- 临时失败按数据库 `available_at` 退避，记录 `STEP_RETRY_SCHEDULED`；默认最多执行 5 次，耗尽后持久化 FAILED。
- 停机前失败直接结束 Run；QUIESCE 之后失败强制创建 ROLLBACK job，禁止绕过源业务恢复。
- 原有行级 `FOR UPDATE SKIP LOCKED`、心跳和过期租约接管继续生效，失去租约的旧 Worker 无法提交结果。

## 取消与人工重试

- PENDING、PREFLIGHT、PRESYNC 阶段取消会删除租约、跳过未完成步骤、将 Run 置为 CANCELLED，并把 Plan 释放回 READY。
- QUIESCE 到 AWAITING_CUTOVER 阶段取消会把 Run 置为 ROLLING_BACK、清理原 job 并创建唯一 `rollback:v1` 步骤；回滚完成后才成为 CANCELLED。
- FAILED/CANCELLED 任务可通过重试 API 创建新的 Run；运行中、已完成或正在回滚的任务返回稳定 409 错误。
- 所有启动、取消和重试操作写管理员审计；事件 detail 仅包含 UUID、步骤类型、次数和时间，不包含凭证或 Hook。

## API 与 SSE

- `POST /api/v1/migration-plans/{planId}/runs`
- `GET /api/v1/migration-runs/{runId}`
- `POST /api/v1/migration-runs/{runId}/cancel`
- `POST /api/v1/migration-runs/{runId}/retry`
- `GET /api/v1/migration-runs/{runId}/events`
- Run 查询返回 `{run, steps}` 的完整持久化快照。
- SSE 先重放 `id > Last-Event-ID` 的数据库事件，再持续轮询；15 秒发送注释心跳，并关闭代理缓冲。
- 非法游标、UUID、未找到和状态冲突均有稳定 Problem Details 错误码；OpenAPI 增加 RunSnapshot、Step、时间戳、取消和重试契约。

## 管理界面

- 六步向导最后确认后真实创建 Run，并跳转到 Run ID 路由，不再误把 Plan ID 当作任务 ID。
- 迁移详情页读取真实 RunSnapshot，展示总进度、每步状态/次数、数据字节数、任务时间和错误。
- Native EventSource 监听命名 `migration` 事件；浏览器断线自动携带 Last-Event-ID，收到事件后去重并刷新持久化快照。
- 页面依据状态只显示合法操作：运行中可取消，FAILED/CANCELLED 可重试，ROLLING_BACK 禁止重复取消。
- 明确展示系统不会自动修改 DNS、负载均衡和防火墙，AWAITING_CUTOVER 是人工门禁。

## 验证

- 单元测试覆盖策略生成、非法重试状态、启动/取消/重试 API、SSE Last-Event-ID 和 5 次重试耗尽。
- PostgreSQL 18 真实测试覆盖原子调度、步骤自动推进、QUIESCE 后取消、ROLLBACK 完成、事件游标续传及过期租约接管。
- React 测试从六步向导确认启动并进入真实迁移时间线预览。
- 1440×900 和 1280×900 浏览器渲染通过；浏览器控制台检查发现并修复 Ant Design 6 Timeline 废弃属性告警。
- `make test lint build` 在真实 PostgreSQL 环境通过；OpenAPI 有效，未来窗口尚未实现的占位端点仍有 13 条 4xx 建议。
- `go test -race ./internal/...` 在真实 PostgreSQL 环境通过。

## 已知边界

- 本窗口完成执行框架，不虚假执行 Kubernetes、Velero、MinIO、NFS、Kompose 或 Kopia 操作；对应 Handler 在窗口 17 至 24 按真实适配器注册。
- 当前 Worker 对尚未注册的步骤明确返回 `no migration step handler registered`，按持久化重试预算失败，而不是把未执行的迁移标记成功。
- 人工切流确认和源业务恢复的真实 Kubernetes/Compose Handler 属于窗口 24；当前已经固化等待门禁和回滚调度语义。
- 事件传输使用一秒数据库轮询，避免新增消息中间件；窗口 25 会补充指标、资源限制与长周期容量验证。
