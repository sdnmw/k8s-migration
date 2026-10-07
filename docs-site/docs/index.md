---
id: index
title: SKS Migration Center 文档
slug: /
sidebar_position: 1
---

# 从发现应用到验证数据，一站完成迁移

SKS Migration Center 用于将 Docker Compose 或 Kubernetes 应用迁移到 SmartX SKS 工作负载集群。平台将应用发现、兼容性评估、资源转换、镜像同步、卷数据迁移、逐资源验证、人工切流与证据报告串联为可恢复、可审计的流程。

:::tip 第一次使用
先阅读[快速开始](quick-start/)和[第一次迁移](quick-start/first-migration/)，然后根据源类型选择 [Compose Nextcloud 教程](tutorials/compose-nextcloud/)或 [Kubernetes Nextcloud 教程](tutorials/kubernetes-nextcloud/)。
:::

<div className="feature-grid">
  <a className="feature-card" href="quick-start/"><h3>快速开始</h3><p>完成登录、环境接入和第一次迁移前检查。</p></a>
  <a className="feature-card" href="concepts/platform/"><h3>核心概念</h3><p>理解 Inventory、Mapping、Run、证据快照和切流。</p></a>
  <a className="feature-card" href="tutorials/compose-nextcloud/"><h3>Compose 教程</h3><p>迁移包含 MariaDB、Redis 和两个 named volume 的 Nextcloud。</p></a>
  <a className="feature-card" href="tutorials/kubernetes-nextcloud/"><h3>Kubernetes 教程</h3><p>迁移 Deployment、Service、Secret、ConfigMap 与 PVC。</p></a>
  <a className="feature-card" href="installation/offline/"><h3>离线安装</h3><p>从 Harbor 导入镜像并部署完整平台。</p></a>
  <a className="feature-card" href="troubleshooting/"><h3>故障排查</h3><p>根据步骤、心跳、事件和资源状态定位问题。</p></a>
</div>

## 支持范围

| 源类型 | 应用发现 | 资源迁移 | 数据迁移 | 镜像处理 |
| --- | --- | --- | --- | --- |
| Docker Compose | `docker compose ls -a` 与完整配置目录 | Kompose + 平台规范化 | Kopia：named volume、bind mount | 自动推送联动 Harbor |
| Kubernetes | 自动发现或按 Namespace 手选 | Velero + 字段映射 | FSB 或 CSI Data Mover | 按 Registry/Image 映射 |

平台不会自动修改企业 DNS、外部负载均衡或防火墙。目标验证通过后会进入“等待人工切流”，由管理员完成外部流量切换并确认。
