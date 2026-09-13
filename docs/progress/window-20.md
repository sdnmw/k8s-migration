# 窗口 20：Kubernetes FSB 预同步、最终备份与目标恢复

- 状态：工程实现完成，真实集群验收待外部条件
- 日期：2026-09-05
- 当前门禁：单元、集成、契约、Helm渲染及构建通过；双集群真实链路待 `sida` kubeconfig和目标旧 Velero策略

## 本段已完成

- Velero CR Adapter新增 Restore：幂等创建、Run/Plan身份保护、Namespace Mapping、PVC恢复开关、状态与资源进度读取。
- 新增 Kubernetes VeleroExecutor，按 Run/Plan/Application/Environment/Mapping解析源端与目标端，凭证只在内存中解密并在步骤结束时清零。
- `PRESYNC` 创建在线 FSB Backup；`FINAL_BACKUP` 使用确定性名称创建停机后的最终 Backup；重试不会创建重复备份。
- `TRANSFER` 确认最终 Backup已 Completed并汇总 PodVolumeBackup；`RESTORE` 等待目标 Velero从共享 BSL同步最终 Backup CR，再创建目标 Restore。
- Namespace Mapping进入 Restore spec；Restore结果包含 itemsRestored/totalItems、warning/error和完成事件。
- 新增统一 VolumeTransfer领域模型和 PostgreSQL Repository，支持 Velero FSB、CSI Data Mover、Compose Kopia三种引擎。
- 逐卷记录源 Pod/Volume、目标卷、总字节、已传字节、吞吐、重试、校验状态和失败信息；聚合字节同步到 MigrationRun。
- 新增 `/api/v1/migration-runs/{runId}/volume-transfers`，迁移详情页显示逐卷引擎、源卷、目标卷、字节、吞吐、重试、校验和状态；SSE进度事件触发刷新。
- 对未被运行中 Pod挂载的 PVC自动创建确定性 staging Pod，挂载后用 `backup.velero.io/backup-volumes` 交给 node-agent；在线预同步和最终备份完成或失败后均清理。
- 停机前持久化 Deployment/StatefulSet原始副本数，然后等待源工作负载缩容到 0；重试不会覆盖首次副本快照。
- 目标 Restore完成后删除临时 StorageClass映射和恢复出的 staging Pod，再按原始副本数启动目标工作负载。
- 基础 Validation验证目标 Deployment、StatefulSet就绪且 PVC全部 Bound；验证或恢复失败时由既有状态机进入 Rollback，并按持久化快照恢复源端副本数。
- Worker已接入 `PREFLIGHT → PRESYNC → QUIESCE → FINAL_BACKUP → TRANSFER → TRANSFORM → RESTORE → VALIDATION → ROLLBACK` 全部 Kubernetes执行步骤；`AWAIT_CUTOVER`仍由人工确认 API控制。
- Worker和 Helm/Compose部署配置已挂载凭证主密钥，并传入 digest固定的 staging helper镜像。

## 自动化验证

- Fake dynamic client覆盖 Restore spec、Namespace Mapping、状态进度和同 Run幂等。
- Fake Kubernetes client覆盖只为未挂载 PVC创建 staging Pod、重复调用幂等、确定性清理、副本缩放、StorageClass映射及目标基础验证。
- Executor覆盖在线预同步、源端停机、最终备份、最终传输确认、目标备份同步、Namespace/StorageClass映射恢复、目标副本启动、逐卷字节持久化及源端回滚。
- PostgreSQL集成用例覆盖 VolumeTransfer upsert、状态推进、Run聚合字节、进度事件和原始副本快照首次写入保护。
- Helm内存渲染验证 Worker主密钥挂载和 staging helper镜像；`docker compose config`验证开发部署配置有效。
- `GOPROXY=https://goproxy.cn,direct make test lint build` 全量通过；OpenAPI有效，保留 5 个既有未来端点 warning。

## 尚未冒充完成的真实验收

- `sida` 仍缺少可导入的 kubeconfig，因此尚不能执行 `sida → mw` 真迁移。
- `mw` 已存在非本系统管理的 Velero 1.13.2；在用户决定复用、升级或隔离命名空间前，系统不会覆盖安装。
- 双 Kind有状态迁移和真实 SKS验收尚未执行；这些属于本窗口的运行验收遗留，不影响执行内核代码门禁，但会阻塞最终上线签收。
- 当前 Validation只覆盖工作负载/PVC基础健康；HTTP/TCP和数据内容校验在窗口 24完成。
- Registry映射尚未进入恢复后的镜像改写链路，在事件中明确标为 pending，后续窗口处理。
