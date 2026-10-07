---
title: 离线安装
sidebar_position: 1
---

# 离线安装

## 1. 校验与解压

```bash
sha256sum -c sks-migration-center-0.5.0-linux-amd64.tar.gz.sha256
mkdir sks-migration-center-0.5.0
tar -xzf sks-migration-center-0.5.0-linux-amd64.tar.gz -C sks-migration-center-0.5.0
cd sks-migration-center-0.5.0
```

校验失败不要继续部署。不要修改解压后的 bundle 交付文件；部署脚本会校验清单，已登记文件缺失或摘要不一致会拒绝未验证 bundle。当前安装器允许额外放置用户 kubeconfig、凭据文件或原始压缩包，但这些额外文件不属于可信交付内容。

## 2. 准备安全文件

```bash
install -d -m 700 /secure/sks-migration
chmod 600 /secure/sks-migration/harbor-username \
  /secure/sks-migration/harbor-password \
  /secure/sks-migration/target-sks.yaml
```

Harbor 用户名、密码和 kubeconfig 必须放在独立文件中，不作为明文命令参数。kubeconfig 应能直接执行 `kubectl get nodes`。

## 3. 一键部署

```bash
./deploy.sh \
  --harbor-address 192.168.112.28 \
  --harbor-project library \
  --harbor-username-file /secure/sks-migration/harbor-username \
  --harbor-password-file /secure/sks-migration/harbor-password \
  --sks-kubeconfig /secure/sks-migration/target-sks.yaml \
  --insecure-registry
```

| 参数 | 说明 |
| --- | --- |
| `--harbor-address` | Harbor 主机与可选端口，不包含协议路径 |
| `--harbor-project` | 镜像目标项目；推荐预先存在的 `library` 或专用项目 |
| `--harbor-username-file` | 只读用户名文件 |
| `--harbor-password-file` | 只读密码文件 |
| `--sks-kubeconfig` | 目标 SKS kubeconfig |
| `--insecure-registry` | 仅 Harbor 使用 HTTP 或不受信任证书时使用 |
| `--admin-password-file` | 新安装指定初始密码；升级且管理员密码已修改时，供脚本登录初始化使用 |
| `--master-key-file` | Base64 编码的 32 字节主密钥；首次省略会生成，升级不要更换 |
| `--storage-class` | 平台数据库与默认 MinIO 使用的 RWO StorageClass；省略自动发现 SmartX CSI |
| `--minio-storage-size` | 默认 MinIO PVC 容量，默认 `100Gi`，应按迁移数据量规划 |
| `--namespace` | 平台 Namespace，默认 `sks-migration-center`，不是业务目标 Namespace |
| `--cookie-secure` | 平台通过 HTTPS 访问时启用 Secure 登录 Cookie |
| `--skip-default-minio` | 不安装默认 MinIO；须在平台自行配置可达的对象存储 |

新安装默认账号为 `admin`，初始密码为 `SmartX@123456`。首次登录后立即修改；升级不会把已经修改的密码重置成默认值。密码不能仅凭应用 Secret 推断：Secret 可能保留首次初始化值，登录数据库中保存的是当前密码散列。

部署会导入完整 `linux/amd64` 镜像清单、安装 Helm Chart、等待 API/Worker/Web/PostgreSQL/MinIO，并注册默认目标集群与对象存储。安装器要求指定项目存在或允许创建；无项目创建权限时请让管理员预建项目，并使用具有推送权限的凭证。Compose **业务镜像同步**另有自动创建公有项目及 `library` 回退规则，不要与安装器参数混淆。

## 4. 安装后检查

1. `kubectl --kubeconfig /secure/sks-migration/target-sks.yaml get pods -n sks-migration-center` 全部 Ready（以 `--namespace` 实际参数为准）。
2. 打开脚本输出的平台地址并登录。
3. 修改初始管理员密码。
4. 检查 SmartX SKS 已连接、对象存储存在且测试通过。
5. 打开 `/docs/`，确认断网时仍可浏览和搜索。

如果对象存储初始化返回“需要已连接目标集群”，说明 API 尚未看到目标环境；部署脚本应等待并重试。容器 Ready 不等于初始化步骤全部成功，应以脚本最终摘要为准。
