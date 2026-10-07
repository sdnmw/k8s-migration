---
title: 资源映射、镜像仓库与转换规则
sidebar_position: 4
---

import GuideShot from '@site/src/components/GuideShot'

# 资源映射、镜像仓库与转换规则

## 资源映射

维护 Namespace、StorageClass、NFS、IngressClass、NodeLabel 等源到目标规则。列表中的计数显示每类规则数量。冲突、重复源规则或目标能力不存在会被阻止。

映射是可选项：Compose 会自动创建目标 Namespace；Kubernetes 只有匹配到规则时才改写，未匹配的资源不会因为“没有选择映射”而无法创建。

### 创建教程的 Namespace 映射

1. 点击右上角 **创建资源映射**。
2. 配置名称填 `Nextcloud Compose 教程映射`，目标选 `mw`（读者替换为自己的目标集群）。
3. 切到 **Namespace**，点击 **添加映射**。
4. 源填 `nextcloud-redis-mariadb`，目标填 `nextcloud-compose-demo`。Kubernetes 教程则填 `nextcloud-k8s-source` → `nextcloud-k8s-demo`。
5. 保存后回到创建迁移的“目标映射”步骤选择该配置。不填写的其他类型规则不会被自动清空源端字段。

| 页签 | 源字段 | 目标/动作 | 适用例子 |
| --- | --- | --- | --- |
| StorageClass | 原 PVC 的 StorageClass 名称 | 从目标能力快照选择 StorageClass | legacy-block → smtx-elf-csi-driver |
| 外部 NFS | Server、绝对 Export | Server、绝对 Export、目标 StorageClass | 旧 NFS 路径迁移到新的导出路径 |
| Namespace | 源命名空间或 Compose 项目名 | 目标命名空间 | 本教程的两个独立目标 Namespace |
| IngressClass | 源类名 | 目标已发现的类名 | traefik → nginx |
| NodeLabel | 标签键/约束 | MAP 或 DROP；MAP 填目标值 | 目标没有旧硬件标签时删除旧约束 |

保存失败先检查目标环境能力是否刷新、重复源规则和 Namespace 冲突。不要为解决一个冲突给全部资源加“特权”或“restricted”策略；安全上下文与业务兼容性需要逐项核对。

## 镜像仓库

配置源 Registry 到目标 Harbor 的地址、项目和凭证。Kubernetes 使用规则改写镜像；Compose 会自动同步本地构建镜像和公有镜像。无法创建专属项目时回退到部署配置的项目，例如 `library`。

镜像映射只改写前缀，不等同于 Kubernetes 镜像复制。创建时写清源 Registry 前缀与目标 Harbor 前缀，保持镜像名及标签/摘要一致。Compose 会先从源 Docker 获取镜像，再推到联动 Harbor；本地 build 镜像没有公网拉取地址也可以同步。项目创建失败可回退 library，但 registry 认证、推送权限或镜像架构失败仍必须处理。

建议使用不可变版本或 digest。`nextcloud:apache` 等可变标签在不同节点缓存中可能对应不同版本；迁移 Nextcloud 数据后不能用旧版本应用读取新版本数据。本次 Kubernetes 教程采用 `nextcloud:35.0.1-apache`。

## 资源转换规则

当前页面是转换预览工具，不是任意脚本规则编辑器。选择已有映射配置，点击“选择 YAML”上传源端 Kubernetes 清单，再点“生成转换预览”。左侧选择资源，右侧可切换“目标 YAML”“Unified Diff”“源 YAML”。预览清理运行时字段、转换旧 API、应用匹配映射；Secret 内容始终脱敏。该操作不部署任何资源。

未选映射或 YAML 时按钮不可用；转换失败应根据返回错误检查 API 版本、资源结构和重复规则。平台不会无条件给目标业务 Namespace 加限制性安全上下文。

## 图解操作

### 资源映射列表

<GuideShot id={6} />

### 镜像仓库映射

<GuideShot id={7} />

### 资源转换入口

<GuideShot id={8} />
