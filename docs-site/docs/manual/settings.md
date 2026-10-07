---
title: 存储、对象存储与凭证
sidebar_position: 5
---

import GuideShot from '@site/src/components/GuideShot'

# 存储、对象存储与凭证

## 存储配置

- **准备存储并验证**（旧版“安装并验证”）：首次安装或配置 NFS CSI 等依赖，并执行端到端持久化读写测试。
- **重新验证读写**（旧版“重新验证”）：不重复安装，仅用现有配置重跑 PVC 创建、写入、卸载、重挂载、读取与清理测试；配置必须处于 READY。

验证通过不等于所有业务 PVC 一定兼容，还需核对访问模式、容量、协议版本和挂载参数。

## 对象存储

对象存储为 Velero/Kopia 提供备份仓库。填写 endpoint、bucket、region、access key 与 secret key 后测试连接。凭证值保存后不回显。平台只有一个对象存储配置时，各集群里的 BSL 仍可能残留旧 endpoint/prefix，应在任务执行期检查中核对。

## 凭证

1. 点击 **添加凭证**，填写一个可识别的名称。
2. 类型选择 Kubernetes kubeconfig、SSH、Registry 或 S3。kubeconfig 输入完整 YAML，其他类型输入对应的 JSON。不要把 Linux 文件路径当作凭证内容。
3. 点击 **加密保存**。字段未填或 JSON 无效时先按界面错误修改，不要反复点击。
4. 列表只显示名称、类型和创建时间，不提供密码回读。
5. 删除前确认没有环境、对象存储或镜像规则引用该凭证；有引用时后端会拒绝删除。

## 新建 NFS 配置逐项说明

在“存储配置”点击创建，目标环境选工作负载集群。NFS Server 填 IP/主机名，Export 填绝对路径（例如 `/maven-nfs`），StorageClass 填唯一名称，回收策略选择 Retain 或 Delete。Retain 适合需要保留数据的迁移窗口；Delete 会影响动态创建卷的回收，不代表平台会自动删除源应用数据。

先点击“准备存储并验证”，等待 READY；再在资源映射中的“外部 NFS”配置源 server/export 到目标 server/export/StorageClass。同一 NFS 后端复用与复制到独立目录是两种语义：复用时源、目标可能共享同一份数据，不能把同时写入造成的冲突归为“同步成功”。

## 对象存储填写与检查

Endpoint 是平台 Worker 及源/目标 Velero 都能访问的 S3 地址；Bucket 是备份容器，Region 按服务要求填写。Access Key/Secret Key 由对象存储管理员提供。测试通过后仍要检查集群 BSL 的 endpoint、bucket、prefix 和凭证是否一致。删除对象存储配置不会代替清理已有备份文件。

## 图解操作

### 存储配置列表

<GuideShot id={9} />

### 对象存储列表

<GuideShot id={10} />

### 凭证列表

<GuideShot id={11} />

### 添加凭证的输入表单

<GuideShot id={44} />
