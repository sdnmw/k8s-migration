# Window 24 — 验证、人工切流与源端恢复

## 结果

Window 24 的工程门禁已通过。Kubernetes 与 Compose 两条执行链现在都会验证目标工作负载、PVC、Service、Ingress，并可在目标 Namespace 内执行 HTTP/TCP 连通性检查；验证失败会由任务状态机进入既有自动回滚路径。等待切流时，管理员可以确认外部切流完成或主动恢复源业务。

## 已完成

- 基础验证增加 Service 与 Ingress 资源读取，保留 Deployment/StatefulSet 就绪和 PVC Bound 检查。
- 新增目标集群内 Endpoint Validation Job：HTTP 使用 `wget --spider`，TCP 使用 `nc`；Job 不挂载 ServiceAccount Token，使用 digest 固定辅助镜像并在结束后清理。
- Kubernetes/Velero 与 Compose Executor 均消费 MigrationPlan 的 `HTTPChecks`、`TCPChecks` 和超时时间。
- 验证事件记录工作负载、PVC、Service、Ingress以及 HTTP/TCP 检查数量。
- `POST /migration-runs/{runId}/cutover` 已真正接入路由：只允许 `AWAITING_CUTOVER`，记录操作者和检查清单，完成等待步骤、Run和Plan。
- `POST /migration-runs/{runId}/rollback` 已接入路由：停机后的 Run进入 `ROLLING_BACK` 并调度幂等回滚步骤。
- `GET /migration-runs/{runId}/report` 已接入路由：导出 Plan、Run、Step、全部分页事件和逐卷传输记录。
- 迁移详情页在等待切流阶段显示“确认切流”和“回滚”，并始终提供 JSON报告下载。
- OpenAPI 补齐新增接口的成功响应体与 4XX错误契约，API lint 不再有警告。

## 回滚闭环

- Kubernetes源：恢复停机前持久化的 Deployment/StatefulSet副本数并删除 staging Pod。
- Compose源：重新使用注册时加密保存的 Compose定义和 `.env` 执行受控 `docker compose up -d`。
- 目标资源默认保留用于诊断，不自动删除。
- 人工确认切流后 Run进入 `COMPLETED`，不再允许自动回滚。

## 可复现验证

- Endpoint Validation Job构造测试覆盖 HTTP、TCP、无 ServiceAccount Token和非法目标拦截。
- API Handler测试覆盖切流操作者传递、主动回滚和报告下载头。
- Compose全链测试覆盖目标恢复后的源端重启。
- `make test`、`make lint`、`make build` 全部通过。

## 尚未执行的真实验收

- 尚未在真实 `mw` 目标 Namespace执行 HTTP/TCP验证 Job；需要先将平台和辅助镜像导入目标 Harbor。
- 真实验证失败后的 `sida`/Compose源恢复仍需要对应源端凭证和可停机测试业务。

## 下一窗口

Window 25 将完成 JSON报告细化、Prometheus可观测性、资源限制、NetworkPolicy、备份清理、升级/卸载和故障注入回归。
