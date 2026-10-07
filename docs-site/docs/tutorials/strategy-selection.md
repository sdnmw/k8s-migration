---
title: 资源与数据策略选择
sidebar_position: 3
---

# 资源与数据策略选择

| 应用情况 | 选择 | 关键检查 |
| --- | --- | --- |
| Kubernetes 无 PVC | 不迁移卷数据 | 镜像和 API 版本兼容，避免无意义停机 |
| SmartX 块存储 PVC | Velero FSB 或 CSI Data Mover | StorageClass、访问模式、容量、快照能力 |
| NFS PVC | Velero FSB | 两集群可达 NFS，对象存储一致，源目标不是同一子目录 |
| Compose named volume | Compose Kopia | SSH 读取权限、volume mount、数据库一致性 |
| Compose bind mount | Compose Kopia | 路径存在、可读、未越过允许根目录 |

资源映射只在需要改名或改写时配置。镜像仓库映射用于 Kubernetes 镜像；Compose 镜像由平台自动同步到联动 Harbor，并在专属项目创建失败时回退到 `library`。
