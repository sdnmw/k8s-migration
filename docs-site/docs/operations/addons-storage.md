---
title: Velero、MinIO 与 NFS
sidebar_position: 2
---

# Velero、MinIO 与 NFS

## Velero 检查

```bash
kubectl -n velero get deploy,ds,pod
kubectl -n velero get backupstoragelocation
kubectl get backuprepositories -A
```

检查 controller、node-agent、BSL Available 和 BackupRepository Ready。源、目标 BSL 的 endpoint、bucket、prefix 必须一致。组件被用户删除时，在环境详情点击 **修复迁移组件**，平台应重新探测并补齐缺失资源。

## MinIO

检查 endpoint 可达、bucket 存在、凭证有效和容量充足。平台只有一个对象存储配置，不代表各集群中旧 BSL 自动消失；执行前仍需校验实际对象。

## NFS CSI

“准备存储并验证”用于部署/配置 NFS CSI 并持久化测试；“重新验证读写”只执行测试。验证项目包括创建 PVC、写入数据、卸载、重新挂载、读取相同内容和清理资源。

NFS 迁移还需确认：源和目标节点均可访问服务端；NFSv3/v4 与导出方式匹配；源、目标动态卷不是同一个子目录；FSB 有非零传输量。
