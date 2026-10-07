---
title: 状态、事件与参考
sidebar_position: 2
---

# 状态、事件与参考

## 任务状态

| 状态 | 含义 | 操作 |
| --- | --- | --- |
| 进行中 | Worker 正在执行或等待外部资源 | 查看心跳和等待原因 |
| 成功 | 必要资源和验证均通过 | 切流/下载报告/按需恢复源端 |
| 成功有告警 | 主目标完成，存在非阻断问题 | 打开告警明细 |
| 失败 | Step 或必要资源失败 | 编辑并重新执行 |
| 需要处理 | 迁移主体完成但收尾失败，或人工动作未完成 | 按直接原因处理 |
| 已取消 | 用户终止或系统取消 | 确认源端和临时资源 |

## 常见事件类型

`STEP_STARTED`、`STEP_SUCCEEDED`、`STEP_FAILED`、`WORKER_HEARTBEAT`、`LEASE_EXPIRED`、`SOURCE_QUIESCED`、`BACKUP_COMPLETED`、`DATA_TRANSFER_COMPLETED`、`RESOURCE_APPLIED`、`VALIDATION_FAILED`、`CUTOVER_CONFIRMED`、`SOURCE_RESTORED`。

## 端口与资源

平台入口以部署脚本输出为准。内部包含 Web、API、PostgreSQL 和 MinIO Service；不要为了文档站新增端口，文档固定使用平台同源 `/docs/`。

实际 CPU、内存和存储需求取决于并发任务、Inventory 数量和备份数据量。生产环境应为 PostgreSQL 与 MinIO 使用持久化存储并配置容量告警。

## API 与报告

仓库根目录 `api/openapi.yaml` 是 API 字段参考。HTML 报告包含任务摘要、环境与策略、拓扑、步骤/尝试、数据传输、验证和事件；敏感值必须脱敏。
