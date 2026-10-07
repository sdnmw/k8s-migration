---
title: 存储与资源映射
sidebar_position: 3
---

# 存储与资源映射

## 资源映射

资源映射可包含 Namespace、StorageClass、NFS、IngressClass、NodeLabel、Registry/Image 等规则。它是条件改写规则，不是所有任务的必填项。

- Compose：未选择映射时，平台按目标应用名创建 Namespace，并为 Service、Deployment、PVC 自动生成目标资源。
- Kubernetes：规则匹配时改写；没有匹配时保留源值或采用平台安全默认值。

## 存储配置

“准备存储并验证”用于首次在目标集群安装/配置所需组件并执行持久化读写测试；“重新验证读写”不重复安装，仅重新执行创建 PVC、写入、卸载、重挂载、读取和清理测试。

## 数据引擎

| 场景 | 建议策略 | 说明 |
| --- | --- | --- |
| Compose named volume | Compose Kopia | 文件级预同步和最终增量 |
| Compose bind mount | Compose Kopia | 要求源路径可读且允许迁移 |
| Kubernetes 普通 PVC | Velero FSB | 由 node-agent 进行文件系统备份 |
| 支持快照的数据卷 | CSI Data Mover | 快照与数据移动结合 |
| 同 NFS 导出不同子目录 | Velero FSB | 必须确认不是源、目标直接共享同一目录 |
| 无卷应用 | 不迁移数据 | 跳过卷备份；停源/恢复仍以任务策略及执行时序为准 |

对象存储是 Velero/Kopia 的中转和证据仓库。源、目标集群中的 BSL 必须使用一致的 endpoint、bucket 和 prefix。
