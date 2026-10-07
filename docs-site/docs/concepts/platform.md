---
title: 平台对象
sidebar_position: 1
---

# 平台对象

## 环境、应用与 Inventory

- **源环境**：被迁移的 Kubernetes 集群或 Docker Compose 主机。
- **目标 SKS**：承载迁移结果的 SmartX Kubernetes Service 工作负载集群。
- **应用 Inventory**：平台发现并固化的资源清单。Kubernetes Inventory 来自实际 API 对象；Compose Inventory 来自 `docker compose ls -a`、Compose 配置、环境变量键名、镜像、卷、网络及引用路径。

自动发现适合完整应用；“按 Namespace 手选资源”适合只迁移某个 Deployment 及其 Service、Ingress、PVC、ConfigMap、Secret 等关联资源。手选不会绕过依赖与兼容性检查。

## Plan、Run、Step 与 Attempt

| 对象 | 含义 |
| --- | --- |
| Migration Plan | 可编辑的迁移意图，包括源应用、目标、策略和映射 |
| Run | 一次实际执行；重新执行会生成新 Run |
| Step | 执行阶段，例如最终备份、目标恢复、业务验证 |
| Attempt | Step 的一次尝试，包含开始时间、心跳、事件和错误 |

Plan 可以重复执行；Run 的证据快照不可变。目标集群当前状态会单独刷新，不会改写当时的历史结论。

## 证据快照与实时观察

迁移终态会保存资源拓扑、映射差异、验证结果、步骤事件和数据传输记录。页面刷新“当前状态”时，只更新实时观察字段并标记漂移；历史执行证据仍保持原值。
