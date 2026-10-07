---
title: 升级、回滚与卸载
sidebar_position: 2
---

# 升级、回滚与卸载

## 升级至 v0.5.0

1. 备份 PostgreSQL、平台 Secret 和自定义 Helm values。
2. 校验并解压 v0.5.0 离线包。
3. 使用与原安装一致的 Harbor、项目和 kubeconfig 参数执行 `deploy.sh`。
4. 等待数据库迁移和三个平台组件 Ready。
5. 验证原环境、计划、Run、对象存储和凭证记录仍存在。
6. 验证右上角“使用指引”和 `/docs/`。

升级不会覆盖 v0.4.0 Release，也不会主动删除历史任务或备份。

## 回滚

回滚前先确认数据库 schema 兼容。仅回退镜像而保留更高版本 schema 可能失败；优先恢复升级前数据库备份，再回退 Helm release。

## 卸载和数据保留

卸载前分别决定是否保留 PostgreSQL PVC、MinIO PVC、Velero/Kopia 备份和目标集群中的已迁移业务。不要把“卸载平台”与“删除迁移目标业务”视为同一操作。
