---
title: 快速开始
description: 第一次使用 SKS Migration Center 前需要了解的登录、导航、检查项和成功标准。
sidebar_position: 1
---

import GuideShot from '@site/src/components/GuideShot'

# 快速开始

SKS Migration Center 用于把 Kubernetes 或 Docker Compose 应用迁移到 SmartX SKS。平台不仅执行资源创建和数据复制，还保留评估、映射、步骤、重试、资源验证、切流和恢复证据。

## 适用场景

- Kubernetes Namespace 内的工作负载、网络、配置和 PVC 迁移。
- Compose Service、named volume、bind mount 和网络转换为 Kubernetes 资源。
- SmartX 块存储、NFS、Velero FSB、CSI Data Mover 和 Compose Kopia 数据迁移。
- 迁移前兼容性评估、迁移后资源与业务验证、失败后的编辑与重试。

## 第一次登录

1. 打开部署脚本输出的平台地址。
2. 使用安装时设置的管理员密码登录；首次登录后，点击右上角管理员菜单中的 **修改密码**。
3. 新密码至少 12 个字符。修改成功会注销全部现有会话，需要重新登录。
4. 不要把管理员密码、Harbor Token、kubeconfig、SSH 私钥或 Secret 内容写入迁移名称和备注。

## 导航速览

| 菜单 | 用途 | 首次使用要做什么 |
| --- | --- | --- |
| 概览 | 查看环境、任务、组件健康和告警 | 确认服务均为正常 |
| 迁移任务 | 创建、筛选、编辑、重试和删除迁移 | 暂无任务时从“创建迁移”开始 |
| 环境 / 源环境 | 注册 Kubernetes 或 Compose 源端 | 测试连接并发现应用 |
| 环境 / SmartX SKS | 注册目标工作负载集群 | 检查能力和迁移组件 |
| 迁移策略 | 管理资源映射、镜像仓库和转换规则 | 按需配置，不是所有任务都必选 |
| 系统设置 | 管理存储、对象存储和凭证 | 完成持久化存储验证 |

## 第一次迁移前检查

- 源端和目标端网络互通，平台 Worker 可访问两个环境。
- 目标集群节点可拉取联动 Harbor 中的镜像。
- 有卷任务已配置对象存储，源、目标 Velero BSL 的 endpoint、bucket、prefix 一致。
- 目标 StorageClass 存在；NFS 配置已通过“准备存储并验证”或“重新验证读写”。
- 业务有可复核的迁移前数据基线，例如文件 SHA-256、数据库记录数或 API 返回值。
- 已明确入口切换方式，以及迁移后是否需要恢复源端。

:::warning 不要只看 100%
“流程进度 100%”只表示步骤已经走完；迁移成功还要求必要资源验证通过、数据校验通过且任务结论成功。失败任务也可能显示 100% 流程进度。
:::

下一步请阅读 [第一次完整迁移](./first-migration.md)。

## 图解操作

### 登录平台

<GuideShot id={1} />

<GuideShot id={50} focus={{x: 82, y: 4, label: '在新标签页打开离线使用指引'}} />

### 查看概览

<GuideShot id={2} />
