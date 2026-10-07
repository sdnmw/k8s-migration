---
title: Docker Compose Nextcloud 完整迁移
sidebar_position: 1
description: 从 192.168.112.51 将 Nextcloud、MariaDB、Redis 和 named volume 迁移到 nextcloud-compose-demo。
---

import GuideShot from '@site/src/components/GuideShot'

# Docker Compose Nextcloud 完整迁移

本教程使用 `192.168.112.51` 上的 Nextcloud Compose 项目，目标 Namespace 为 `nextcloud-compose-demo`。迁移包含 Nextcloud、MariaDB、Redis、数据库卷和文件卷，采用 Compose Kopia。

真实项目名为 `nextcloud-redis-mariadb`，配置文件为 `/root/awesome-compose/nextcloud-redis-mariadb/compose.yaml`。服务名为 `nc`、`db`、`redis`，named volume 为 `nc_data` 和 `db_data`；示例中的服务显示名以 Inventory 为准。

:::caution 保护凭证
截图和报告只展示环境变量键名，不展示管理员密码、数据库密码、SSH 密钥或 Harbor Token。
:::

## 1. 建立数据基线

1. 登录源 Nextcloud。
2. 新建目录 `SKS-Compose-Migration-v0.5.0`。
3. 上传教程文件，例如 `compose-nextcloud-migration.txt`。
4. 记录文件大小与 SHA-256；迁移后必须使用相同方法复核。

预期结果：源端可以下载文件，校验值稳定，MariaDB 和 Nextcloud volume 均有实际数据。

## 2. 发现应用

1. 进入 **环境 → 源环境**。
2. 找到 Compose 主机，先点 **测试连接**。
3. 点击 **发现应用**。平台读取 `docker compose ls -a` 和项目 Config Files。
4. 打开 Nextcloud Inventory，确认 Service 为 Nextcloud、MariaDB、Redis，volume 至少包含文件卷和数据库卷。

如果只发现部分应用，检查 SSH 用户是否能读取 Compose 文件、`.env` 和引用目录。不要单独上传脱离项目目录的 compose 文件代替主机发现。

## 3. 创建迁移

1. 进入 **迁移任务 → 创建迁移**。
2. 选择 Compose 源环境和 Nextcloud 应用。
3. 目标选择 SmartX SKS。
4. 展开兼容性得分，处理全部 BLOCKER；逐项确认 WARNING。
5. 通常目标映射可留空，平台按项目名自动创建 Namespace。本教程需要独立命名空间：先在“资源映射”创建 `nextcloud-redis-mariadb` → `nextcloud-compose-demo` 规则，再在向导选择该映射。向导没有独立的 Namespace 输入框。
6. 数据策略选择 **Compose Kopia**。
7. 确认在线预同步、停止源业务、最终备份、验证和人工切流设置。

## 4. 执行与观察

执行时序应依次出现：执行期检查、镜像同步、在线预同步、停止源业务、最终备份、数据传输、资源转换、目标恢复、业务验证和等待人工切流。

在 **数据迁移** 页确认两个 volume 均为完成，且传输量不是 0。拓扑中每个 Compose Service 应映射到目标 Service 与 Deployment，每个 named volume 映射到 PVC。

## 5. 目标验证与切流

1. 通过目标入口登录 Nextcloud。
2. 打开 `SKS-Compose-Migration-v0.5.0` 目录并下载教程文件。
3. 核对文件名、大小和 SHA-256 与源端一致。
4. 检查目标 MariaDB、Redis 和 Nextcloud Pod Ready，PVC Bound。
5. 在外部完成 DNS/VIP/Ingress 切换后，回到平台点击 **确认人工切流**。

## 6. 恢复源端

教程场景从任务右上角操作菜单选择 **恢复源端**。平台会启动迁移前处于运行状态的 Compose 服务，不删除目标资源。恢复后再次访问源端并复核文件。

## 验收清单

本次实迁文件 `compose-nextcloud-migration.txt` 为 76 字节；迁移前源端、目标端及恢复后源端的 SHA-256 均为：

升级后的 v0.5.0 原生复测 Run 为 `2a972bfc-559e-4837-bbda-38e9944b00fd`，迁移成功且源端恢复；两个卷共传输 1,127,906,204 字节。第一次截图任务和复测任务均保留，方便在列表中对照。

```text
a6886a06a915f73e5f91542539812f15d12cbc5bb3d068423942990618e2b4a9
```

目标测试入口为 `http://192.168.118.206:31999`。若 Nextcloud 提示“不受信任域名”，这是应用入口配置，不代表数据丢失：在**目标** `nc` 容器中通过 `occ config:system:set trusted_domains` 添加实际目标主机与端口，再登录验证。生产切流需管理员配置真实 DNS/VIP；本教程只切换测试访问入口，不修改公共 DNS。

- 任务结论为迁移成功，失败/缺失为 0。
- 目标 Namespace 是 `nextcloud-compose-demo`。
- 两个卷都有完成状态和实际传输量。
- 目标与恢复后的源端都能登录。
- 教程文件 SHA-256 三次一致：迁移前源端、目标端、恢复后源端。

## 图解操作

### Compose 数据基线

<GuideShot id={14} />

### Compose 等待人工切流

<GuideShot id={46} />

<GuideShot id={47} focus={{x: 33, y: 42, label: '目标映射可选；本例选择教程 Namespace 映射'}} />

<GuideShot id={48} />

<GuideShot id={49} focus={{x: 28, y: 72, label: '核对摘要后确认人工切流声明'}} />

<GuideShot id={29} />

### Compose 卷传输

<GuideShot id={30} />

### 确认 Compose 人工切流

<GuideShot id={31} />

### 目标 Compose 文件验证

<GuideShot id={32} />

### 恢复 Compose 源端

<GuideShot id={33} />

### 应用视图

<GuideShot id={38} />
