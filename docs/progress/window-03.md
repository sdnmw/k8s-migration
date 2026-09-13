# 窗口 03：状态机、任务租约与幂等执行

- 状态：完成
- 日期：2026-09-02
- 门禁：崩溃恢复及非法状态转换测试通过

## 已完成能力

- 建立 `MigrationRun` 严格状态机，覆盖预检、预同步、停机、最终备份、传输、转换、恢复、验证、等待切流与回滚。
- 源业务进入 `QUIESCE` 后，失败或取消必须先进入 `ROLLING_BACK`，防止绕过源端恢复。
- 建立可幂等创建的 `MigrationStep`，以 `(migration_run_id, idempotency_key)` 保证重复调度不会生成重复步骤。
- 建立 PostgreSQL `JobLease` 队列：使用行锁和 `SKIP LOCKED` 竞争任务，不引入 Redis、Kafka 或 RabbitMQ。
- 实现领取、心跳续租、延迟重试、完成确认和过期租约接管。
- Worker 使用每进程唯一 owner ID；丢失租约时立即取消 handler，禁止旧 Worker 完成或重排已被接管的任务。
- API 与 Worker 均在启动时执行带 advisory lock 和 checksum 的数据库迁移。

## 状态安全约束

- `PENDING/PREFLIGHT/PRESYNC` 阶段失败或取消时，源业务仍在运行，可直接终止。
- 从 `QUIESCE` 到人工切流确认前，任何失败或取消都必须执行回滚。
- `AWAITING_CUTOVER` 只有人工确认后才能进入 `COMPLETED`；取消必须进入回滚。
- 非法跃迁（例如 `PREFLIGHT -> COMPLETED`）由领域层和事务 Repository 共同拒绝。

## 数据库并发语义

- 任务领取在单个事务内锁定一行并更新 owner、过期时间和 attempt。
- 有效租约不会被第二个 Worker 领取。
- Worker 崩溃且租约过期后，另一 Worker 可接管相同步骤，attempt 单调增加。
- 旧 owner 的心跳、完成或重试请求返回 `ErrNotFound`，不能覆盖新 owner。

## 验证结果

- Go 单元测试：通过。
- Go race detector：领域状态机、Worker 与 PostgreSQL Repository 通过。
- PostgreSQL 18 真实容器集成测试：通过。
- 相同步骤重复创建、相同任务重复入队：均返回原记录。
- 两 Worker 租约竞争：未过期不可重复领取，过期后成功接管。
- 旧 Worker 接管后续租被拒绝：通过。
- 非法状态跃迁被拒绝且数据库状态未改变：通过。
- 测试夹具执行后 MigrationPlan、JobLease、Environment 均清理为 0。

## 未完成项

- 各步骤的 Kubernetes、Velero、Kopia、Kompose handler 将在相应集成窗口注册。
- SSE 事件流、取消 API 和页面断点续传在窗口 15 实现。
- 最大重试次数和最终失败策略将在具体任务类型接入时按错误分类补齐。

## 本地验证

```bash
make lint
make test
make build

TEST_DATABASE_URL='postgres://migration:migration@127.0.0.1:5432/migration?sslmode=disable' \
  go test -race -count=1 ./internal/domain/migration ./internal/worker ./internal/repository/postgres
```
