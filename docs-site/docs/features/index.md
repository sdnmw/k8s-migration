---
title: 关键特性
sidebar_position: 4
---

# 关键特性

## 应用发现与依赖分析

Kubernetes 自动发现 Namespace 内的工作负载、Service、Ingress、PVC、ConfigMap、Secret、ServiceAccount、RBAC、HPA、PDB、NetworkPolicy 和自定义资源，并根据 selector、ownerReference、挂载、环境变量引用等关系构建应用。

Compose 发现基于 `docker compose ls -a` 读取实际 Config Files，解析 Service、image/build、depends_on、ports、environment 键名、volume、network、config、secret 与外部引用。即使项目已退出，只要 Compose 元数据仍在也可以发现。

## 镜像自动同步

Compose 的本地构建镜像和公有镜像都会先同步到环境联动 Harbor，再由目标集群拉取。平台优先创建应用专属公有项目；Harbor 不允许创建项目时，自动回退到部署配置的项目，通常是 `library`，不会仅因项目创建失败终止迁移。

## 数据与存储

- Compose Kopia：named volume、bind mount 的在线预同步与最终增量。
- Velero FSB：跨 StorageClass、NFS 或普通文件系统 PVC 的备份恢复。
- CSI Data Mover：适用于支持 CSI 快照能力的环境。
- NFS：支持源、目标连接同一服务端但使用不同动态目录，并记录传输字节与校验证据。

## 双端资源拓扑

单一画布展示源端、转换和目标端。Kubernetes 按 Ingress → Service → Deployment/StatefulSet → PVC/ConfigMap/Secret 层级组织；Compose 按入口/端口 → Service → 依赖服务 → volume/config/secret/network 组织。节点与迁移线均可聚焦关联路径。

## 可靠执行与诊断

Step/Attempt 记录重试、Worker 心跳、租约和关键事件。平台识别租约过期、长时间无心跳、外部备份等待和组件缺失，并给出可执行的修复入口。

## 可审计交付

终态快照不可变，实时目标状态单独展示；HTML 报告可离线查看并自动脱敏。历史任务可懒重建证据，无法补齐的字段会明确标记“证据不完整”。
