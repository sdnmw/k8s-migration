---
title: 环境、发现与组件修复
sidebar_position: 3
---

import GuideShot from '@site/src/components/GuideShot'

# 环境、发现与组件修复

## 添加源环境

### Kubernetes

填写名称并上传 kubeconfig。平台加密保存凭证；界面不会回显完整内容。保存后先 **测试连接**，再执行 **发现应用** 或 **能力发现**。

### Docker Compose

填写环境名称、SSH 地址、用户和认证方式。主机密钥 SHA-256 指纹为可选项：留空时平台读取并固定保存，后续连接会核对是否变化。首次自动获取并不能代替可信渠道核验；安全要求较高时请手动填写核对后的指纹。SSH 用户须能执行 Docker/Compose 并读取项目目录、`.env` 和引用文件。

## 发现应用

- Kubernetes：扫描 Namespace 和资源依赖，注册可迁移应用。
- Compose：执行 `docker compose ls -a` 并读取 Config Files；running 与 exited 项目都可发现。

## 能力发现

展示 Kubernetes 版本、节点、StorageClass、IngressClass、CSI/快照能力，或 Compose/Docker 版本、Compose 项目路径、卷、网络和镜像能力。

## SmartX SKS 与迁移组件

目标环境页用于检查目标能力、安装或修复 Velero、node-agent、BSL、BackupRepository 和存储组件。探测到组件被删除或配置漂移时，**修复迁移组件** 会重新核对并恢复缺失对象，不需要先删除整个环境。

删除环境失败时，页面应显示后端返回的具体依赖或权限原因；先删除/改绑引用该环境的计划，再重试。

## 操作顺序和成功标准

1. **测试连接**：验证凭证与网络。状态 CONNECTED 才继续；kubectl 能访问不代表平台保存的 kubeconfig 一定是同一份。
2. **能力发现**：读取现时配置，核对版本、CSI、Ingress 和备份能力。能力快照时间过旧时重新发现。
3. **发现应用**：注册 Inventory，而不是部署应用。Compose 的 `running`/`exited` 只表示源服务状态，不表示可迁移结论。
4. 在创建迁移第二步选择明确的项目名/Namespace；不要因为发现返回第一条就迁移第一条。
5. **修复迁移组件**：用于 Velero/BSL/node-agent 被删除或配置不一致。修复期间不删除业务 PVC，不需要先删除整个源环境。

## Compose 路径和权限

主机发现会读取项目的 Config Files、工作目录及引用文件。单独上传 compose YAML 时缺少 `env_file`、include 或 secrets 文件会报错，此时优先从主机发现完整项目。权限修复应限于读取所需的项目路径；平台不能凭空获得 sudo 权限。先确认用户拥有授权的 sudo/ACL 能力，再按诊断给出精确路径授权，禁止递归修改整台机器的权限。

手动注册表单的文件选择按钮上传的是本地 YAML 内容；输入 `/var/lib/smartx/harbor/docker-compose.yml` 文字并不等于让浏览器读取远端文件。远端目录请用主机发现；本地上传请同时保证所需引用完整。

## 图解操作

### 添加 Kubernetes 源环境

<GuideShot id={23} />

### 添加 Compose 主机

<GuideShot id={24} />

### Compose 能力发现

<GuideShot id={35} />

### Kubernetes 能力发现

<GuideShot id={36} />

### 手动注册 Compose

<GuideShot id={37} />
