# 窗口 14：MigrationPlan、Preflight 与六步向导

- 状态：完成
- 日期：2026-09-03
- 门禁：六步向导端到端通过

## 已完成能力

- MigrationPlan 完整持久化名称、源/目标环境、源应用、Assessment、MappingProfile、迁移策略和验证策略。
- 创建计划前验证所有 UUID、源目标不同、应用与源环境归属、Assessment 与应用归属、MappingProfile 与目标归属，以及 Kubernetes/Compose 类型一致性。
- 策略支持 `NONE`、`FS_BACKUP`、`CSI_DATA_MOVER`、`COMPOSE_KOPIA`、在线预同步、覆盖/NodePort 选项和停机/回滚 Hook；Hook 和验证超时有明确上限。
- PostgreSQL Repository 支持创建、读取、列表和 DRAFT/READY/BLOCKED 状态更新，策略与验证策略使用 JSONB 往返。

## Preflight 门禁

- 检查源和目标环境连接状态。
- Kubernetes 源与目标必须具备能力快照；Compose 源必须已探测 Docker 与 Compose 版本。
- 目标必须发现 `smtx-elf-csi-driver` 或由其提供的 StorageClass，避免误把普通 Kubernetes 集群登记为目标 SKS。
- Assessment 必须完成且 BLOCKER 为 0。
- 数据迁移引擎必须与 Kubernetes/Compose 源类型匹配。
- Raw Block PVC 必须使用 CSI Data Mover；选择 Data Mover 时源和目标都必须发现 VolumeSnapshotClass。
- 每个 Kubernetes PVC 必须能通过 MappingProfile 或目标默认 SC 解析到真实目标 StorageClass。
- 缺少 Registry 映射、FSB node-agent 在线检查作为 WARNING 展示，不虚假标记为已经验证。
- 所有检查确定性排序；存在 BLOCKER 时计划变为 BLOCKED，否则变为 READY。

## API

- `GET /api/v1/migration-plans`
- `POST /api/v1/migration-plans`
- `GET /api/v1/migration-plans/{planId}`
- `POST /api/v1/migration-plans/{planId}/preflight`
- 新增 `MIGRATION_PLAN_INVALID`、`MIGRATION_PLAN_NOT_FOUND`、`MIGRATION_PLAN_CONFLICT` 错误契约。
- 审计只记录计划名称、状态及 Preflight 计数，不记录 Hook 内容。
- OpenAPI 增加 MigrationStrategy、ValidationPolicy、MigrationPlan、PreflightCheck/Result；既有告警由 18 项降至 16 项。

## 六步管理向导

1. 选择 Kubernetes 或 Docker Compose 来源和已导入环境。
2. 选择已完成 Inventory 的 Namespace/Compose Project。
3. 选择目标 SKS 并执行 Assessment；BLOCKER 未清零不能继续。
4. 选择 MappingProfile、卷迁移方式、预同步开关和 HTTP 验证地址。
5. 保存 DRAFT、执行 Preflight，并逐项展示 PASSED/WARNING/BLOCKER 与修复建议。
6. 汇总计划，要求明确勾选人工切流边界后才能确认。

从目标映射开始计划引用被固定，上一步按钮禁用，避免 UI 修改与已持久化 DRAFT 不一致。确认后只进入计划详情，不自动启动迁移或修改 DNS/LB。

## 验证

- 领域/服务测试覆盖完整创建、跨环境引用拒绝、READY 与多 BLOCKER 决策。
- API 测试覆盖完整策略解码、Preflight 返回和冲突错误。
- React 测试真实操作 Ant Design Select，完整走通六个步骤并验证最终确认门禁。
- PostgreSQL 18 实例验证策略/验证 JSONB、计划列表和 READY 状态更新。
- 1280×720 浏览器逐步实测，修复步骤切换后保留旧滚动位置的问题；最终页面无水平溢出。
- `go test -race` 覆盖 migration、API 和 PostgreSQL Repository：通过。
- `make test && make lint && make build`：通过。

## 已知边界

- Kubernetes Namespace Inventory 已可直接完成六步计划。Compose Project 需要窗口 22 的 Kompose 候选资源持久化后，才能从真实 Compose 主机完成同一向导。
- Preflight 本窗口使用持久化能力快照和规则检查；Velero BSL、node-agent、NFS 读写、MinIO S3 和目标容量的在线探针由对应安装窗口接入。
- 计划 READY 只表示静态门禁通过；窗口 15 才会创建 Run、排队任务并通过 SSE 展示执行进度。
