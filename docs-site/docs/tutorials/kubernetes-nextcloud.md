---
title: Kubernetes Nextcloud 完整迁移
sidebar_position: 2
description: 从 sida 的 nextcloud-k8s-source 迁移 Nextcloud、MariaDB、Redis 和两个 SmartX PVC。
---

import GuideShot from '@site/src/components/GuideShot'
import OfflineDownload from '@site/src/components/OfflineDownload'

# Kubernetes Nextcloud 完整迁移

源 Namespace 为 `nextcloud-k8s-source`，包含 Nextcloud、MariaDB、Redis、Service、Secret、ConfigMap，以及 Nextcloud 和 MariaDB 两个使用 `smtx-elf-csi-driver` 的 PVC。目标 Namespace 为 `nextcloud-k8s-demo`，数据策略为 Velero FSB。

本实验室部分截图中的源环境显示为 `aa`，它是同一个 sida 工作负载集群的既有登记别名，并非另一个集群。实际操作请以自己导入的 sida 环境及其 API 地址为准，不要照抄截图中的名称或 UUID。

## 1. 准备源应用和数据

文档站内提供 <OfflineDownload path="/examples/nextcloud-k8s-source.yaml" label="Nextcloud Kubernetes 示例清单（离线下载）" />，保存到 `examples/nextcloud-k8s-source.yaml`。先创建命名空间，在本地安全文件中准备 `db-password`、`db-root-password`、`admin-password`（文件名即键名），再创建 Secret，最后应用清单；不要将密码写入 Git：

```bash
kubectl --kubeconfig /secure/source-sida.yaml create ns nextcloud-k8s-source
kubectl --kubeconfig /secure/source-sida.yaml -n nextcloud-k8s-source create secret generic nextcloud-credentials \
  --from-file=db-password=/secure/nextcloud/db-password \
  --from-file=db-root-password=/secure/nextcloud/db-root-password \
  --from-file=admin-password=/secure/nextcloud/admin-password
kubectl --kubeconfig /secure/source-sida.yaml apply -f examples/nextcloud-k8s-source.yaml
```

示例访问端口 `31555`；安装前确认端口未被占用。账号为 `tutorial-admin`。示例固定 Nextcloud `35.0.1-apache`，避免同一可变标签在两端缓存成不同版本。

1. 等待 Nextcloud、MariaDB、Redis 全部 Ready，两个 PVC 为 Bound。
2. 登录源 Nextcloud，新建 `SKS-Kubernetes-Migration-v0.5.0`。
3. 上传与 Compose 教程不同的文件，例如 `k8s-nextcloud-migration.txt`。
4. 记录大小和 SHA-256。

## 2. 演示两种选择方式

### 自动发现应用

在 **源环境 → sida → 发现应用** 中扫描 Namespace，选择平台按资源依赖生成的 Nextcloud 应用。适合迁移完整业务。

### 按 Namespace 手选资源

在创建迁移第二步切换为 **按 Namespace 手选资源**，选择 `nextcloud-k8s-source`，点击 **读取资源**。勾选 Nextcloud、MariaDB、Redis 的 Deployment、Service、PVC、ConfigMap、Secret 和 ServiceAccount，保存为迁移应用。

预期结果：选择工作负载后，相关 Service、PVC、配置和密钥会显示关联信息；平台不会要求迁移 Namespace 中无关的系统资源。

## 3. 评估与映射

1. 目标选择 SmartX SKS。
2. 展开兼容性得分，BLOCKER 必须为 0。
3. Namespace 规则设为 `nextcloud-k8s-source` → `nextcloud-k8s-demo`。
4. StorageClass 可保留 `smtx-elf-csi-driver`；只有名称不同时才配置映射。
5. 数据策略选择 **Velero 文件系统备份（FSB）**。

## 4. 迁移执行

执行期检查会验证 Velero、node-agent、BSL 和 BackupRepository。最终备份完成后，目标端恢复资源和两个 PVC，再启动工作负载。

如果页面长时间等待，进入执行时序查看最近心跳与当前等待原因，并在目标环境使用 **修复迁移组件**。修复后创建新 Run，不要修改历史事件。

## 5. 验证、切流与恢复

1. 确认目标两个 PVC Bound，Nextcloud、MariaDB、Redis Ready。
2. 登录目标 Nextcloud，下载教程文件并核对 SHA-256。
3. 确认外部入口切换后点击 **确认人工切流**。
4. 选择 **恢复源端**，平台按迁移前快照恢复源端副本数。
5. 登录恢复后的源 Nextcloud，再次复核文件。

## 验收清单

本次实迁文件 `k8s-nextcloud-migration.txt` 为 79 字节；迁移前源端、目标端及恢复后源端的 SHA-256 均为：

升级后的 v0.5.0 原生复测 Run 为 `60c222e4-a43e-4cbd-b763-d9efaddbabd4`，迁移成功且源端恢复；两个 PVC 共传输 1,083,926,041 字节。本次复测使用已固定版本的源镜像，无需再次修补目标镜像。

```text
99fcb9758506e2b9500abf47b0096230e81f527f176e017f3122b50225ce527d
```

示例中曾遇到目标缓存旧 Nextcloud 镜像而退出：先查看 Pod 日志确认版本，统一目标镜像版本后重新验证。不要把“Pod Running”或传输完成当作应用验证通过。

- 目标 Namespace 是 `nextcloud-k8s-demo`。
- 目标资源验证通过，两个 PVC 数据迁移完成。
- 目标文件 SHA-256 与源端一致。
- 人工切流和恢复源端均有成功事件。
- 源端恢复后副本数与迁移前一致。

## 图解操作

### Kubernetes 数据基线

<GuideShot id={13} />

### 向导第 1 步：源环境

<GuideShot id={12} />

### 自动发现 Kubernetes 应用

<GuideShot id={15} />

### 核对已发现应用

<GuideShot id={16} />

### 手选 Namespace 资源

<GuideShot id={17} focus={{x: 30, y: 34, label: '选择 Namespace 后读取并勾选资源'}} />

### 向导第 3 步：评估

<GuideShot id={18} />

### 查看评分细则

<GuideShot id={19} />

### 观察执行时序

<GuideShot id={20} />

### Kubernetes 卷传输

<GuideShot id={21} />

### 核对任务信息

<GuideShot id={22} />

### 确认 Kubernetes 人工切流

<GuideShot id={25} />

### 恢复 Kubernetes 源端

<GuideShot id={26} />

### 目标 Kubernetes 文件验证

<GuideShot id={27} />

### Kubernetes 完成拓扑

<GuideShot id={28} />
